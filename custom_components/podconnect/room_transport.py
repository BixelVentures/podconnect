"""Exact-room native audio transport, within PlaybackCommands' sequence authority."""

from __future__ import annotations

import asyncio
import copy
import hashlib
import json
import re
import time
import uuid

from homeassistant.exceptions import HomeAssistantError

from .observations import PLAYBACK_MAX_AGE_S, _bounded, current
from .speakers import ROOM_READ_REASONS, SpeakersError

_OBSERVED_ACTIONS = ("pause", "resume", "next", "previous")
ACTIONS = (*_OBSERVED_ACTIONS, "seek", "shuffle", "repeat")
_NATIVE = {
    "account_user_id",
    "target_id",
    "room_id",
    "homepod_id",
    "connection_id",
    "session_id",
    "alias_incarnation",
    "alias_registry",
    "alias_revision",
}
_ROUTE = {
    "process",
    "device_id",
    "device_incarnation",
    "session_epoch",
    "route_revision",
    "selection_revision",
    "input_generation",
    "input_item_id",
    "input_path_digest",
}
_TOKEN = re.compile(r"[a-f0-9]{32}")
_DIGEST = re.compile(r"[a-f0-9]{64}")
_DECIMAL = re.compile(r"0|[1-9][0-9]{0,19}")


def _text(value):
    return isinstance(value, str) and 1 <= len(value) <= 1024


def valid_identity(native, route, scope):
    if (
        not isinstance(native, dict)
        or set(native) != _NATIVE
        or not isinstance(route, dict)
        or set(route) != _ROUTE
        or any(not _text(native[k]) for k in _NATIVE - {"alias_revision"})
        or type(native["alias_revision"]) is not int
        or not 0 <= native["alias_revision"] < 2**53
        or any(native[k] != scope[k] for k in scope)
        or route["device_id"] != scope["homepod_id"]
        or not isinstance(route["input_path_digest"], str)
        or _DIGEST.fullmatch(route["input_path_digest"]) is None
    ):
        return False
    try:
        if (
            not isinstance(route["process"], str)
            or str(uuid.UUID(route["process"])) != route["process"]
        ):
            return False
    except (ValueError, AttributeError):
        return False
    return all(
        isinstance(route[k], str)
        and _DECIMAL.fullmatch(route[k]) is not None
        and int(route[k]) < 2**64
        for k in _ROUTE - {"process", "input_path_digest"}
    )


def valid_context(value):
    return (
        isinstance(value, dict)
        and set(value) == {"room_id", "homepod_id", "native", "route"}
        and _text(value["room_id"])
        and _text(value["homepod_id"])
    )


def _snapshot(value, schema, extra, scope):
    return (
        isinstance(value, dict)
        and set(value)
        == {"schema", "ok", "native", "route", "physical_result_verified"} | extra
        and value["schema"] == schema
        and value["ok"] is True
        and value["physical_result_verified"] is False
        and valid_identity(value["native"], value["route"], scope)
    )


def valid_effect(value, native):
    return value is None or (
        isinstance(value, dict)
        and set(value)
        == {
            "prepare_id",
            "action",
            "effect_revision",
            "native_revision",
            "native_owner",
        }
        and isinstance(value["prepare_id"], str)
        and _TOKEN.fullmatch(value["prepare_id"]) is not None
        and value["action"] in (*ACTIONS, "volume")
        and all(
            isinstance(value[key], str)
            and _DECIMAL.fullmatch(value[key]) is not None
            and 0 < int(value[key]) < 2**64
            for key in ("effect_revision", "native_revision")
        )
        and value["native_owner"] == native
    )


class RoomTransport:
    """No replay cache, sequence, routing mutation, or generic provider fallback."""

    def __init__(self, commands):
        self.commands = commands

    def scope(self, hass, entry, target, room, homepod):
        from .commands import CommandRejected

        current(hass, entry, self.commands.data)
        config = hass.config_entries.async_get_entry(entry)
        account = config.unique_id if config is not None else None
        if not _text(account):
            raise CommandRejected("native_account_unproven")
        if self.commands.data.speakers is None:
            raise CommandRejected("room_transport_unavailable")
        return {
            "account_user_id": account,
            "target_id": target,
            "room_id": room,
            "homepod_id": homepod,
        }

    def observation_source(self, hass, entry):
        from .commands import CommandRejected

        data = current(hass, entry, self.commands.data)
        if not self.commands._refresh_owner_current(hass, entry):
            raise CommandRejected("owner_replaced")
        observed = data.observations.playback(entry)
        if not observed.get("ok"):
            raise CommandRejected("stale_observation")
        raw = data.coordinator.data
        config = hass.config_entries.async_get_entry(entry)
        return (
            config.unique_id,
            observed["owner_epoch"],
            observed["poll_sequence"],
            raw["playback_observed_at"],
            raw["playback_observed_monotonic"],
            copy.deepcopy(raw["playback"]),
        )

    async def observe(self, hass, entry, target, room, homepod):
        """Validate passive Native/C facts without requiring a permitted action."""
        from .commands import CommandRejected

        scope = self.scope(hass, entry, target, room, homepod)
        before = self.observation_source(hass, entry)
        try:
            state = await self.commands.data.speakers.room_transport_state(scope)
        except SpeakersError as exc:
            rejected = CommandRejected("room_transport_unavailable")
            if exc.room_read_reason in ROOM_READ_REASONS:
                rejected.room_read_reason = exc.room_read_reason
            raise rejected from exc
        if self.scope(hass, entry, target, room, homepod) != scope:
            raise CommandRejected("native_account_unproven")
        if self.observation_source(hass, entry) != before:
            raise CommandRejected("stale_observation")
        if (
            not _snapshot(
                state,
                "podconnect.room_transport_state.v1",
                {"actions", "effect"},
                scope,
            )
            or not valid_effect(state["effect"], state["native"])
            or not isinstance(state["actions"], list)
            or any(
                not isinstance(action, str) or action not in ACTIONS
                for action in state["actions"]
            )
            or len(state["actions"]) != len(set(state["actions"]))
        ):
            raise CommandRejected("room_transport_unavailable")
        return state

    async def playback_output(self, hass, entry, observed, *, include_controls=False):
        """One read-only resolver; the deadline is an observation, never a lease."""
        from .commands import CommandRejected

        def aggregate(reason, binding=None, controls=None):
            value = {
                **observed,
                "schema": "podconnect.playback.v3"
                if include_controls
                else "podconnect.playback.v2",
                "output": {
                    "status": "observed" if binding is not None else "unavailable",
                    "reason": reason,
                    "binding": binding,
                },
            }
            if include_controls:
                value["controls"] = controls or {
                    "status": "unavailable",
                    "reason": reason or "route_unavailable",
                    "binding_id": None,
                    "valid_until": None,
                    "supported_actions": [],
                    "volume": None,
                }
            try:
                return _bounded(value)
            except (ValueError, TypeError):
                return {"ok": False, "error_kind": "malformed_response"}

        if observed["playback"]["state"] == "no_active_playback":
            return aggregate("route_unavailable")
        target = observed["playback"]["target"]
        if target is None or self.commands.data.speakers is None:
            return aggregate("route_unavailable")
        started, started_wall = time.monotonic(), time.time()
        current_guard = None

        def unavailable():
            # A failed awaited read cannot publish an older account projection.
            try:
                if current_guard is None:
                    return {"ok": False, "error_kind": "unavailable"}
                current_guard()
            except (
                CommandRejected,
                HomeAssistantError,
                ValueError,
                TypeError,
                KeyError,
            ):
                return {"ok": False, "error_kind": "unavailable"}
            return aggregate("route_unavailable")

        try:
            source = self.observation_source(hass, entry)
            account_until = source[4] + PLAYBACK_MAX_AGE_S
            deadline = min(account_until, started + 5)
            valid_until = min(
                source[3].timestamp() + PLAYBACK_MAX_AGE_S, started_wall + 5
            )

            def guard():
                if self.observation_source(hass, entry) != source:
                    raise CommandRejected("stale_observation")
                if time.monotonic() >= deadline or time.time() >= valid_until:
                    raise CommandRejected("stale_observation")

            current_guard = guard
            speakers = self.commands.data.speakers
            async with asyncio.timeout(5):
                catalog = await speakers.aliases()
                guard()
                outputs = await speakers.outputs()
                guard()
                selected = [row for row in outputs["devices"] if row["selected"]]
                if len(selected) > 1:
                    return aggregate("ambiguous_output")
                if (
                    not outputs["owntone_up"]
                    or not selected
                    or selected[0]["needs_auth"]
                ):
                    return aggregate("route_unavailable")
                room, homepod = catalog["binding"]["room_id"], selected[0]["id"]
                state = await self.observe(
                    hass, entry, target["target_id"], room, homepod
                )
                guard()
                native = state["native"]
                if (
                    native["alias_incarnation"] != catalog["binding"]["incarnation"]
                    or native["alias_registry"] != catalog["binding"]["registry"]
                ):
                    return aggregate("owner_changed")
                controls = None
                if include_controls:
                    allowed = self.commands.controls(
                        entry,
                        target["target_id"],
                        native_room_phase=True,
                        for_observation=True,
                    )
                    actions = [
                        action
                        for action in _OBSERVED_ACTIONS
                        if action in state["actions"] and action in allowed["actions"]
                    ]
                    volume, reason = None, None
                    try:
                        volume_read = await self.commands.room_volume.read(
                            hass,
                            entry,
                            target["target_id"],
                            room,
                            homepod,
                            passive=True,
                        )
                    except CommandRejected as exc:
                        guard()
                        if str(exc) != "room_volume_unavailable":
                            raise
                        reason = "volume_unavailable"
                    else:
                        guard()
                        if (
                            volume_read["native"] != native
                            or volume_read["route"] != state["route"]
                        ):
                            return aggregate("owner_changed")
                        actual = volume_read["volume_state"]
                        volume = {
                            "base_percent": actual["base_percent"],
                            "effective_percent": actual["effective_percent"],
                        }
                        if (
                            "volume" in volume_read["actions"]
                            and "volume" in allowed["actions"]
                        ):
                            actions.append("volume")
                    controls = {
                        "status": "observed",
                        "reason": reason,
                        "binding_id": None,  # Filled only from the joined route below.
                        "valid_until": valid_until,
                        "supported_actions": actions,
                        "volume": volume,
                    }
                # Poll sequence/progress fence this publication above, but do not
                # turn the same physical route into a new identity each poll.
                identity = [entry, source[1], native, state["route"]]
                binding_id = hashlib.sha256(
                    json.dumps(
                        identity,
                        sort_keys=True,
                        separators=(",", ":"),
                        allow_nan=False,
                    ).encode()
                ).hexdigest()
                guard()
                if controls is not None:
                    controls["binding_id"] = binding_id
                return aggregate(
                    None,
                    {
                        "target_id": target["target_id"],
                        "native_room_id": native["room_id"],
                        "homepod_id": state["route"]["device_id"],
                        "binding_id": binding_id,
                        "valid_until": valid_until,
                    },
                    controls,
                )
        except CommandRejected as exc:
            if str(exc) in {
                "owner_replaced",
                "stale_observation",
                "native_account_unproven",
            }:
                return {"ok": False, "error_kind": "unavailable"}
            return unavailable()
        except HomeAssistantError:
            if not include_controls:
                raise
            return {"ok": False, "error_kind": "unavailable"}
        except (SpeakersError, TimeoutError, ValueError, TypeError, KeyError):
            return unavailable()

    async def read(self, hass, entry, target, room, homepod):
        from .commands import CommandRejected

        before = self.commands.controls(entry, target, native_room_phase=True)
        state = await self.observe(hass, entry, target, room, homepod)
        latest = self.commands.controls(entry, target, native_room_phase=True)
        if any(before[k] != latest[k] for k in ("owner_epoch", "poll_sequence")):
            raise CommandRejected("stale_observation")
        if not state["actions"]:
            raise CommandRejected("room_transport_unavailable")
        return {
            **latest,
            "schema": "podconnect.room_controls.v1",
            "actions": [a for a in latest["actions"] if a in state["actions"]],
            "native": copy.deepcopy(state["native"]),
            "route": copy.deepcopy(state["route"]),
            "room_id": room,
            "homepod_id": homepod,
            "track_play_supported": False,
            "volume_percent": None,
            "volume_supported": False,
            "volume_unavailable_reason": "room_volume_not_supported",
            "physical_result_verified": False,
        }

    async def commit(
        self, hass, entry, target, context, action, guard, sending, *, args=None
    ):
        from .commands import CommandRejected

        if action not in ACTIONS or not valid_context(context):
            raise CommandRejected("invalid_arguments")
        args = {} if args is None else args
        if not isinstance(args, dict):
            raise CommandRejected("invalid_arguments")
        from .commands import _arguments

        _arguments(action, args)
        scope = self.scope(
            hass, entry, target, context["room_id"], context["homepod_id"]
        )
        if not valid_identity(context["native"], context["route"], scope):
            raise CommandRejected("invalid_room_identity")
        guard()
        started = time.monotonic()
        try:
            arguments = (
                {"position_ms": args["position_ms"]}
                if action == "seek"
                else {"shuffle_enabled": args["enabled"]}
                if action == "shuffle"
                else {"repeat_mode": args["mode"]}
                if action == "repeat"
                else {}
            )
            prepared = await self.commands.data.speakers.prepare_room_transport(
                scope, context["native"], context["route"], action, **arguments
            )
        except SpeakersError as exc:
            raise CommandRejected("room_transport_unavailable") from exc
        if (
            not _snapshot(
                prepared,
                "podconnect.room_transport_prepare.v1",
                {"prepare_id", "expires_after_ms", "action", "volume_percent"}
                | set(arguments),
                scope,
            )
            or prepared["native"] != context["native"]
            or prepared["route"] != context["route"]
            or prepared["action"] != action
            or type(prepared["volume_percent"]) is not int
            or prepared["volume_percent"] != 0
            or any(
                type(prepared.get(key)) is not type(value) or prepared[key] != value
                for key, value in arguments.items()
            )
            or not isinstance(prepared["prepare_id"], str)
            or _TOKEN.fullmatch(prepared["prepare_id"]) is None
            or type(prepared["expires_after_ms"]) is not int
            or not 1 <= prepared["expires_after_ms"] <= 5000
        ):
            raise CommandRejected("room_transport_unavailable")
        # Last local owner/account/poll checks, after the awaited prepare, before
        # the irreversible commit request. Native/C perform final mutator checks.
        guard()
        if (
            self.scope(hass, entry, target, context["room_id"], context["homepod_id"])
            != scope
        ):
            raise CommandRejected("native_account_unproven")
        if time.monotonic() - started >= prepared["expires_after_ms"] / 1000:
            raise CommandRejected("room_transport_expired")
        sending(prepared["prepare_id"])
        ack = await self.commands.data.speakers.commit_room_transport(
            prepared["prepare_id"]
        )
        if (
            not isinstance(ack, dict)
            or set(ack)
            != {
                "schema",
                "ok",
                "prepare_id",
                "outcome",
                "accepted_native",
                "retry_allowed",
                "physical_result_verified",
            }
            or ack["schema"] != "podconnect.room_transport_commit.v1"
            or ack["ok"] is not True
            or ack["prepare_id"] != prepared["prepare_id"]
            or ack["outcome"] != "accepted"
            or ack["accepted_native"] is not True
            or ack["retry_allowed"] is not False
            or ack["physical_result_verified"] is not False
        ):
            raise SpeakersError("Room transport outcome unknown")
