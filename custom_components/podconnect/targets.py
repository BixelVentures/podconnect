"""Entry-scoped target services using fresh requests to existing owners."""

from __future__ import annotations

import asyncio

import voluptuous as vol
from aiohttp import ClientError

from homeassistant.core import SupportsResponse
from homeassistant.exceptions import HomeAssistantError
from homeassistant.helpers import area_registry as ar, device_registry as dr, entity_registry as er

from .api import SpotifyApiError
from .const import DOMAIN
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


async def spotify_transfer_target(api, *, target_id=None, name=None) -> tuple[str, bool]:
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
    if entity.config_entry_id != entry_id or entity.disabled_by is not None or not entity.device_id:
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
        return isinstance(values, list) and len(values) <= 16 and all(text(v) for v in values)

    name = er.async_get_full_entity_name(hass, entity)
    names = er.async_get_entity_aliases(hass, entity)
    if not text(entity.entity_id) or not text(name) or not aliases(names):
        return None
    context = {"entity_id": entity.entity_id, "name": name, "aliases": names, "area": None}
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


def register_target_services(hass) -> None:
    """Register once; each invocation resolves its explicitly selected live entry."""

    def current(entry_id, expected=None):
        entries = [e for e in hass.config_entries.async_entries(DOMAIN) if e.entry_id == entry_id]
        if len(entries) != 1:
            raise HomeAssistantError("PodConnect account entry unavailable")
        data = getattr(entries[0], "runtime_data", None)
        if data is None or not data.active or (expected is not None and data is not expected):
            raise HomeAssistantError("PodConnect account entry unavailable")
        return data

    async def catalogue(call, *, with_context=False):
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
                accounts.append({"config_entry_id": entry.entry_id, "title": entry.title})
            return {"accounts": accounts}
        data = current(call.data["config_entry_id"])
        targets, errors = [], {}
        try:
            async with asyncio.timeout(10):
                devices = spotify_devices(await data.api.devices())
            targets.extend(
                {
                    "kind": "spotify_device",
                    "target_id": r["id"],
                    "name": r["name"],
                    "restricted": r["is_restricted"],
                }
                for r in devices
            )
        except (SpotifyApiError, ClientError, TimeoutError, HomeAssistantError):
            errors["spotify_device"] = "unavailable"
        if data.speakers is not None:
            for kind in ("configured_alias", "observed_output"):
                try:
                    if kind == "configured_alias":
                        catalog = await data.speakers.aliases()
                        rooms = await data.speakers.rooms()
                        saved = {r["id"]: r for r in rooms}
                        # Separate GETs are observations, never a write-admission snapshot.
                        if any(r["room_id"] not in saved for r in catalog["aliases"]):
                            raise SpeakersError("Configured room association unavailable")
                        targets.extend(
                            {
                                "kind": kind,
                                "target_id": r["room_id"],
                                "name": r["name"],
                                "homepod_id": saved[r["room_id"]]["homepod_id"],
                                "binding": catalog["binding"],
                            }
                            for r in catalog["aliases"]
                        )
                    else:
                        state = await data.speakers.outputs()
                        targets.extend(
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
                        )
                except SpeakersError:
                    errors[kind] = "unavailable"
        else:
            errors["configured_alias"] = errors["observed_output"] = "not_configured"
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
        return {
            "config_entry_id": call.data["config_entry_id"],
            "targets": targets,
            "errors": errors,
        }

    async def get_targets(call):
        return await catalogue(call)

    async def get_targets_with_context(call):
        return await catalogue(call, with_context=True)

    async def move_playback(call):
        data = current(call.data["config_entry_id"])
        kind, target = call.data["kind"], call.data["target_id"]
        if kind not in ("configured_alias", "spotify_device", "observed_output"):
            raise HomeAssistantError("Unknown playback target kind")
        if kind == "observed_output":
            raise HomeAssistantError("Observed outputs are read-only")
        try:
            if kind == "configured_alias":
                if data.speakers is None:
                    raise HomeAssistantError("Speakers connection is not configured")
                catalog = await data.speakers.aliases()
                current(call.data["config_entry_id"], data)
                return {
                    "kind": kind,
                    "target_id": target,
                    **await data.speakers.move(target, catalog),
                }
            device, playing = await spotify_transfer_target(data.api, target_id=target)
            current(call.data["config_entry_id"], data)
            async with asyncio.timeout(10):
                await data.api.transfer(device, play=playing)
            return {
                "kind": kind,
                "target_id": device,
                "provider_request_accepted": True,
                "play": playing,
            }
        except (SpotifyApiError, ClientError, SpeakersError, TimeoutError) as err:
            raise HomeAssistantError("Playback move failed; outcome unknown") from err

    for name, handler, schema in (
        (
            "get_targets",
            get_targets,
            {vol.Optional("config_entry_id"): vol.All(str, vol.Length(min=1, max=1024))},
        ),
        (
            "get_targets_with_context",
            get_targets_with_context,
            {vol.Optional("config_entry_id"): vol.All(str, vol.Length(min=1, max=1024))},
        ),
        (
            "move_playback",
            move_playback,
            {
                vol.Required("config_entry_id"): vol.All(str, vol.Length(min=1, max=1024)),
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
