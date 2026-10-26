"""Entry-scoped target services using fresh requests to existing owners."""

from __future__ import annotations

import asyncio
import copy
import secrets

import voluptuous as vol
from aiohttp import ClientError
from homeassistant.core import SupportsResponse
from homeassistant.exceptions import HomeAssistantError
from homeassistant.helpers import (
    area_registry as ar,
)
from homeassistant.helpers import (
    device_registry as dr,
)
from homeassistant.helpers import (
    entity_registry as er,
)

from .api import SpotifyApiError
from .const import CONF_ROOM_AREAS, DOMAIN
from .speakers import SpeakersError


def spotify_devices(rows) -> list:
    """Reject an ambiguous provider catalog rather than choose a first match."""
    if not isinstance(rows, list):
        raise HomeAssistantError("Invalid Spotify device catalog")
    seen = set()
    for row in rows:
        if (
            not isinstance(row, dict)
            or not isinstance(row.get("id"), str)
            or not row["id"]
            or not isinstance(row.get("name"), str)
            or not row["name"]
            or type(row.get("is_restricted")) is not bool
            or row["id"] in seen
        ):
            raise HomeAssistantError("Ambiguous Spotify device catalog")
        seen.add(row["id"])
    return rows


async def spotify_transfer_target(
    api, *, target_id=None, name=None
) -> tuple[str, bool]:
    """Resolve current ID/restrictions and preserve only known playback state."""
    async with asyncio.timeout(10):
        rows = spotify_devices(await api.devices())
        matches = [
            r
            for r in rows
            if (r["id"] == target_id if target_id is not None else r["name"] == name)
        ]
        if len(matches) != 1 or matches[0]["is_restricted"]:
            raise HomeAssistantError("Spotify target unavailable or ambiguous")
        playback = await api.playback_state()
    if not isinstance(playback, dict) or type(playback.get("is_playing")) is not bool:
        raise HomeAssistantError("Current Spotify play/pause state is unknown")
    return matches[0]["id"], playback["is_playing"]


def _spotify_ha_context(hass, entry_id: str, target_id: str) -> dict | None:
    """Current HA language metadata for one exact fresh provider target, never a route."""
    entities = er.async_get(hass)
    matches = [
        entry
        for entry in er.async_entries_for_config_entry(entities, entry_id)
        if entry.domain == "media_player"
        and entry.platform == DOMAIN
        and entry.unique_id == f"{entry_id}_{target_id}"
    ]
    if len(matches) != 1:
        return None
    entity = matches[0]
    if (
        entity.config_entry_id != entry_id
        or entity.disabled_by is not None
        or not entity.device_id
    ):
        return None
    device = dr.async_get(hass).async_get(entity.device_id)
    if (
        device is None
        or device.config_entry_id != entry_id
        or device.disabled_by is not None
        or (DOMAIN, target_id) not in device.identifiers
    ):
        return None

    def text(value):
        return isinstance(value, str) and 1 <= len(value) <= 1024

    def aliases(values):
        return (
            isinstance(values, list)
            and len(values) <= 16
            and all(text(v) for v in values)
        )

    name = er.async_get_full_entity_name(hass, entity)
    names = er.async_get_entity_aliases(hass, entity)
    if not text(entity.entity_id) or not text(name) or not aliases(names):
        return None
    context = {
        "entity_id": entity.entity_id,
        "name": name,
        "aliases": names,
        "area": None,
    }
    # An explicit entity override remains authoritative even if its area was deleted.
    area_id = entity.area_id if entity.area_id is not None else device.area_id
    if area_id is not None:
        area = ar.async_get(hass).async_get_area(area_id)
        if area is not None:
            names = sorted(area.aliases)
            if not text(area.id) or not text(area.name) or not aliases(names):
                return None
            context["area"] = {"id": area.id, "name": area.name, "aliases": names}
    return context


async def configured_alias_targets(speakers) -> list[dict]:
    """One fresh alias/native identity observation for services and Options."""
    catalog = await speakers.aliases()
    rooms = await speakers.rooms()
    saved = {r["id"]: r for r in rooms}
    # Separate GETs are observations, never a write-admission snapshot.
    if any(r["room_id"] not in saved for r in catalog["aliases"]):
        raise SpeakersError("Configured room association unavailable")
    targets = [
        {
            "kind": "configured_alias",
            "target_id": r["room_id"],
            "name": r["name"],
            "homepod_id": saved[r["room_id"]]["homepod_id"],
            "binding": catalog["binding"],
        }
        for r in catalog["aliases"]
    ]
    # Display knowledge only. Every write resolves the live catalogue again.
    speakers._configured_alias_targets = copy.deepcopy(targets)
    return targets


async def select_playback_source(
    hass,
    entry_id,
    *,
    name=None,
    kind=None,
    target_id=None,
    entity_guard=None,
    send=None,
):
    """Both HA adapters enter the existing room/release owners once."""
    if (name is None) == (kind is None or target_id is None) or (
        kind is not None and kind not in {"configured_alias", "spotify_device"}
    ):
        raise HomeAssistantError("Invalid source selection")
    from .commands import CommandRejected
    from .observations import current
    from .speakers import SpeakersError

    data = current(hass, entry_id)
    coordinator, api, speakers, commands, entry = (
        data.coordinator,
        data.api,
        data.speakers,
        data.commands,
        data.coordinator.config_entry,
    )
    epoch, poll = coordinator.owner_epoch, coordinator.poll_sequence
    account = entry.unique_id
    binding_owner = (data.session, data.observations, api._session, api._web)

    def owned(*, same_poll=True):
        current(hass, entry_id, data)
        if entity_guard is not None:
            entity_guard()
        if (
            data.coordinator is not coordinator
            or data.api is not api
            or coordinator.api is not api
            or data.speakers is not speakers
            or data.commands is not commands
            or hass.config_entries.async_get_entry(entry_id) is not entry
            or entry.unique_id != account
            or coordinator.owner_epoch != epoch
            or (same_poll and coordinator.poll_sequence != poll)
            or any(
                now is not expected
                for now, expected in zip(
                    (data.session, data.observations, api._session, api._web),
                    binding_owner,
                )
            )
        ):
            raise HomeAssistantError("PodConnect selection owner changed")
        return True

    owned()
    if speakers is None or commands is None:
        raise HomeAssistantError("Native selection identity unavailable")
    try:
        native_id = await speakers.start_identity()
        owned()
        aliases = await configured_alias_targets(speakers)
        owned()
        devices = spotify_devices(await api.devices())
        owned()
        playback = await api.playback_state()
        owned()
        if playback is not None and (
            not isinstance(playback, dict)
            or type(playback.get("is_playing")) is not bool
        ):
            raise HomeAssistantError("Current playback state unavailable")
        alias_matches = (
            [row for row in aliases if row["name"] == name]
            if name is not None
            else [
                row
                for row in aliases
                if kind == "configured_alias" and row["target_id"] == target_id
            ]
        )
        device_matches = (
            [row for row in devices if row["name"] == name]
            if name is not None
            else [
                row
                for row in devices
                if kind == "spotify_device" and row["id"] == target_id
            ]
        )
        if (
            not alias_matches
            and len(device_matches) == 1
            and device_matches[0]["id"] == native_id
        ):
            binding = aliases[0]["binding"] if aliases else None
            alias_matches = [
                row
                for row in aliases
                if binding and row["target_id"] == binding["room_id"]
            ]
            if len(alias_matches) != 1:
                raise HomeAssistantError("Native selected alias unavailable")
        if (
            len(alias_matches) > 1
            or len(device_matches) > 1
            or (alias_matches and any(row["id"] != native_id for row in device_matches))
        ):
            raise HomeAssistantError("Source name is ambiguous")
        native_playback = (
            isinstance(playback, dict)
            and isinstance(playback.get("device"), dict)
            and playback["device"].get("id") == native_id
        )
        if alias_matches:
            selected = alias_matches[0]
            if not selected["homepod_id"]:
                raise HomeAssistantError("Configured output unavailable")
            if not native_playback:
                result = await speakers.move(
                    selected["target_id"],
                    {
                        "binding": selected["binding"],
                        "aliases": [{"room_id": row["target_id"]} for row in aliases],
                    },
                    idle_only=True,
                    execution_guard=owned,
                )
                owned()
                return {
                    "kind": "configured_alias",
                    "target_id": selected["target_id"],
                    **result,
                }
            move = commands.room_move
            room, homepod = selected["target_id"], selected["homepod_id"]
            if room == selected["binding"]["room_id"]:
                await commands.room_transport.observe(
                    hass, entry_id, native_id, room, homepod
                )
                owned()
                return {
                    "kind": "configured_alias",
                    "target_id": room,
                    "observed_current": True,
                    "physical_result_verified": False,
                }
            controls = await move.read(hass, entry_id, native_id, room, homepod)
            owned()
            sequence = controls["next_command_sequence"]
            if (
                controls["status"] != "ready"
                or "move_existing_session" not in controls["actions"]
                or type(sequence) is not int
                or controls["owner_epoch"] != epoch
                or controls["poll_sequence"] != poll
            ):
                raise HomeAssistantError("Native move admission unavailable")
            request = {
                "config_entry_id": entry_id,
                "kind": "spotify_device",
                "target_id": native_id,
                "room_id": room,
                "homepod_id": homepod,
                "owner_epoch": epoch,
                "expected_poll_sequence": poll,
                "command_sequence": sequence,
                "request_id": f"{epoch}:{sequence}",
                "action": "move_existing_session",
                "args": {},
                "move_offer": controls["move_offer"],
            }
            prepared = started = False
            try:
                preparation = await move.prepare(hass, request)
                prepared = preparation.get("state") == "staged"
                owned(same_poll=False)
                if not prepared:
                    raise HomeAssistantError("Native move preparation unavailable")
                latest = await configured_alias_targets(speakers)
                owned(same_poll=False)
                if [
                    row for row in latest if row["target_id"] == selected["target_id"]
                ] != [selected]:
                    raise HomeAssistantError("Configured output changed")
                started = True
                result = await move.start(hass, request)
                owned(same_poll=False)
                if result.get("ok") is not True or result.get("outcome") != "accepted":
                    raise HomeAssistantError("Native move rejected or outcome unknown")
                return {
                    "kind": "configured_alias",
                    "target_id": selected["target_id"],
                    **result,
                }
            finally:
                if prepared and not started:
                    await move.cancel(
                        hass,
                        {
                            "config_entry_id": entry_id,
                            "owner_epoch": epoch,
                            "command_sequence": sequence,
                            "request_id": request["request_id"],
                            "offer_id": request["move_offer"]["offer_id"],
                            "binding_id": request["move_offer"]["binding_id"],
                        },
                    )
        if (
            len(device_matches) != 1
            or device_matches[0]["is_restricted"]
            or device_matches[0]["id"] == native_id
            or playback is None
        ):
            raise HomeAssistantError("External source unavailable")
        binding = aliases[0]["binding"] if aliases else None
        if not binding:
            raise HomeAssistantError("Native release owner unavailable")

        async def release_owner():
            matches = [
                row
                for row in aliases
                if row["target_id"] == binding["room_id"] and row["homepod_id"]
            ]
            if len(matches) != 1:
                raise HomeAssistantError("Native release route unavailable")
            await commands.room_transport.observe(
                hass, entry_id, native_id, binding["room_id"], matches[0]["homepod_id"]
            )
            owned()

        await speakers.ensure_no_owned_session(
            binding["room_id"],
            secrets.token_hex(16),
            execution_guard=owned,
            owner_guard=release_owner,
        )
        owned()

        async def external_ready():
            # Token refresh and release can yield; resolve the exact external
            # target again immediately before its existing provider adapter.
            rows = spotify_devices(await api.devices())
            owned()
            matches = [row for row in rows if row["id"] == device_matches[0]["id"]]
            if (
                len(matches) != 1
                or matches[0]["is_restricted"]
                or (name is not None and matches[0]["name"] != name)
            ):
                raise HomeAssistantError("External source changed")
            # A new Native actor can appear during OAuth or the device read.
            # Observe its current absence once; never release or retry that actor.
            idle = await speakers.aliases(idle_only=True)
            owned()
            if any(
                idle["binding"][key] != binding[key] for key in ("room_id", "registry")
            ):
                raise HomeAssistantError("Native idle selection changed")

        transfer = api.transfer(
            device_matches[0]["id"],
            play=playback["is_playing"],
            execution_guard=owned,
            async_execution_guard=external_ready,
        )
        if send is None:
            await transfer
        else:
            await send(transfer)
        return {
            "kind": "spotify_device",
            "target_id": device_matches[0]["id"],
            "provider_request_accepted": True,
            "play": playback["is_playing"],
        }
    except (
        CommandRejected,
        SpeakersError,
        SpotifyApiError,
        ClientError,
        TimeoutError,
    ) as err:
        raise HomeAssistantError("Source selection unavailable") from err


def _configured_ha_area(hass, entry, target) -> dict | None:
    """Explicit account Options metadata, fenced by the current native target ID."""
    associations = entry.options.get(CONF_ROOM_AREAS)
    if not isinstance(associations, dict) or not target["homepod_id"]:
        return None
    saved = associations.get(target["target_id"])
    if (
        not isinstance(saved, dict)
        or set(saved) != {"homepod_id", "area_id"}
        or saved["homepod_id"] != target["homepod_id"]
        or not isinstance(saved["area_id"], str)
    ):
        return None
    area = ar.async_get(hass).async_get_area(saved["area_id"])
    if area is None:
        return None
    aliases = sorted(area.aliases)
    if (
        not all(
            isinstance(v, str) and 1 <= len(v) <= 1024
            for v in (area.id, area.name, *aliases)
        )
        or len(aliases) > 16
    ):
        return None
    return {"id": area.id, "name": area.name, "aliases": aliases}


def register_target_services(hass) -> None:
    """Register once; each invocation resolves its explicitly selected live entry."""

    def current(entry_id, expected=None):
        entries = [
            e
            for e in hass.config_entries.async_entries(DOMAIN)
            if e.entry_id == entry_id
        ]
        if len(entries) != 1:
            raise HomeAssistantError("PodConnect account entry unavailable")
        data = getattr(entries[0], "runtime_data", None)
        if (
            data is None
            or not data.active
            or (expected is not None and data is not expected)
        ):
            raise HomeAssistantError("PodConnect account entry unavailable")
        return data

    async def catalogue(call, *, with_context=False, with_rooms=False):
        if "config_entry_id" not in call.data:
            accounts, seen = [], set()
            for entry in hass.config_entries.async_entries(DOMAIN):
                data = getattr(entry, "runtime_data", None)
                if data is None or not data.active:
                    continue
                if (
                    not isinstance(entry.entry_id, str)
                    or not 1 <= len(entry.entry_id) <= 1024
                    or entry.entry_id in seen
                    or not isinstance(entry.title, str)
                    or not 1 <= len(entry.title) <= 1024
                ):
                    raise HomeAssistantError("Ambiguous PodConnect account catalog")
                seen.add(entry.entry_id)
                accounts.append(
                    {"config_entry_id": entry.entry_id, "title": entry.title}
                )
            return {"accounts": accounts}
        data = current(call.data["config_entry_id"])

        async def read(kind):
            if kind == "spotify_device":
                try:
                    async with asyncio.timeout(10):
                        devices = spotify_devices(await data.api.devices())
                    return [
                        {
                            "kind": kind,
                            "target_id": r["id"],
                            "name": r["name"],
                            "restricted": r["is_restricted"],
                        }
                        for r in devices
                    ], None
                except (SpotifyApiError, ClientError, TimeoutError, HomeAssistantError):
                    return [], "unavailable"
            if data.speakers is None:
                return [], "not_configured"
            try:
                if kind == "configured_alias":
                    return await configured_alias_targets(data.speakers), None
                state = await data.speakers.outputs()
                return [
                    {
                        "kind": kind,
                        "target_id": r["id"],
                        "name": r["name"],
                        "selected": r["selected"],
                        "needs_auth": r["needs_auth"],
                        "query_up": state["owntone_up"],
                        "read_only": True,
                    }
                    for r in state["devices"]
                ], None
            except SpeakersError:
                return [], "unavailable"

        # Independent read owners share one response deadline, leaving 500ms
        # for validation/HA response before the unchanged 8s caller read timeout.
        reads = {
            kind: asyncio.create_task(read(kind), name="podconnect-catalogue-" + kind)
            for kind in ("spotify_device", "configured_alias", "observed_output")
        }
        try:
            completed, _ = await asyncio.wait(reads.values(), timeout=7.5)
        finally:
            # No read can outlive this call or publish into its later/new owner.
            for task in reads.values():
                if not task.done():
                    task.cancel()
            await asyncio.gather(*reads.values(), return_exceptions=True)
        targets, errors = [], {}
        for kind, task in reads.items():
            if task not in completed or task.cancelled():
                errors[kind] = "unavailable"
                continue
            rows, error = task.result()
            targets.extend(rows)
            if error is not None:
                errors[kind] = error
        current(call.data["config_entry_id"], data)
        if with_context:
            # Read registry metadata after all backend awaits. No registry entity can
            # create a target absent from the fresh provider response above.
            for target in targets:
                if target["kind"] == "spotify_device":
                    context = _spotify_ha_context(
                        hass, call.data["config_entry_id"], target["target_id"]
                    )
                    if context is not None:
                        target["ha_context"] = context
                elif with_rooms and target["kind"] == "configured_alias":
                    entry = next(
                        e
                        for e in hass.config_entries.async_entries(DOMAIN)
                        if e.entry_id == call.data["config_entry_id"]
                    )
                    area = _configured_ha_area(hass, entry, target)
                    if area is not None:
                        target["ha_area"] = area
        return {
            "config_entry_id": call.data["config_entry_id"],
            "targets": targets,
            "errors": errors,
        }

    async def get_targets(call):
        return await catalogue(call)

    async def get_targets_with_context(call):
        return await catalogue(call, with_context=True)

    async def get_targets_with_room_context(call):
        return await catalogue(call, with_context=True, with_rooms=True)

    async def move_playback(call):
        return await select_playback_source(
            hass,
            call.data["config_entry_id"],
            kind=call.data["kind"],
            target_id=call.data["target_id"],
        )

    for name, handler, schema in (
        (
            "get_targets",
            get_targets,
            {
                vol.Optional("config_entry_id"): vol.All(
                    str, vol.Length(min=1, max=1024)
                )
            },
        ),
        (
            "get_targets_with_context",
            get_targets_with_context,
            {
                vol.Optional("config_entry_id"): vol.All(
                    str, vol.Length(min=1, max=1024)
                )
            },
        ),
        (
            "get_targets_with_room_context",
            get_targets_with_room_context,
            {
                vol.Optional("config_entry_id"): vol.All(
                    str, vol.Length(min=1, max=1024)
                )
            },
        ),
        (
            "move_playback",
            move_playback,
            {
                vol.Required("config_entry_id"): vol.All(
                    str, vol.Length(min=1, max=1024)
                ),
                vol.Required("kind"): vol.In(
                    ("configured_alias", "spotify_device", "observed_output")
                ),
                vol.Required("target_id"): vol.All(str, vol.Length(min=1, max=1024)),
            },
        ),
    ):
        if not hass.services.has_service(DOMAIN, name):
            hass.services.async_register(
                DOMAIN,
                name,
                handler,
                schema=vol.Schema(schema),
                supports_response=SupportsResponse.ONLY,
            )
