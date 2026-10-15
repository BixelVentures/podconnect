"""Exact direct-device commands through the existing account-owned Spotify client."""

from __future__ import annotations

import asyncio
import copy
import hashlib
import json
import re

import voluptuous as vol
from homeassistant.core import SupportsResponse
from homeassistant.exceptions import HomeAssistantError

from .const import DOMAIN
from .observations import current
from .speakers import SpeakersError
from .targets import spotify_devices

COMMAND_TIMEOUT_S = 6
MAX_RECEIPTS = 128
_ACTIONS = ("pause", "resume", "next", "previous", "volume")
_COMMAND_ACTIONS = (*_ACTIONS, "play_track")
_TRACK_URI = re.compile(r"^spotify:track:[A-Za-z0-9]{22}$")
_DISALLOWED = {
    "pause": "pausing",
    "resume": "resuming",
    "next": "skipping_next",
    "previous": "skipping_prev",
}


class CommandRejected(Exception):
    """An admission failed before the provider send boundary."""


def _integer(value):
    if type(value) is not int or not 0 <= value <= 100:
        raise vol.Invalid("expected integer percent from 0 to 100")
    return value


def _sequence(value):
    if type(value) is not int or not 1 <= value < 2**53:
        raise vol.Invalid("expected positive integer poll sequence")
    return value


def _arguments(action, args):
    if not isinstance(args, dict) or set(args) != (
        {"percent"}
        if action == "volume"
        else {"track_uri", "max_volume", "room_id", "homepod_id", "start_policy_revision", "alias_incarnation"}
        if action == "play_track"
        else set()
    ):
        raise CommandRejected("invalid_arguments")
    if action == "volume":
        try:
            _integer(args["percent"])
        except vol.Invalid as exc:
            raise CommandRejected("invalid_arguments") from exc
    if action == "play_track" and (
        not isinstance(args["track_uri"], str)
        or _TRACK_URI.fullmatch(args["track_uri"]) is None
    ):
        raise CommandRejected("invalid_arguments")
    if action == "play_track":
        try:
            _integer(args["max_volume"])
        except vol.Invalid as exc:
            raise CommandRejected("invalid_arguments") from exc
        if any(
            not isinstance(args[key], str) or not 1 <= len(args[key]) <= 128
            for key in ("room_id", "homepod_id", "alias_incarnation")
        ) or not isinstance(args["start_policy_revision"], str) or len(args["start_policy_revision"]) != 64:
            raise CommandRejected("invalid_arguments")


class PlaybackCommands:
    """No poller, optimistic state or second provider client; one bounded command owner."""

    def __init__(self, data):
        if data.commands is not None:
            raise ValueError("Command namespace requires a fresh integration runtime")
        self.data = data
        self.owner_epoch = data.coordinator.owner_epoch
        self.binding = (
            data.api,
            data.coordinator,
            data.session,
            data.observations,
            data.speakers,
            data.api._session,
            data.api._web,
        )
        self.busy = False
        self.receipts = {}
        self.poll_floor = 0
        self.command_sequence = 0

    def controls(self, entry_id, target_id, *, for_command=False):
        data = self.data
        binding = (
            data.api,
            data.coordinator,
            data.session,
            data.observations,
            data.speakers,
            data.api._session,
            data.api._web,
        )
        if data.coordinator.owner_epoch != self.owner_epoch:
            raise CommandRejected("owner_replaced")
        if data.commands is not self or any(
            now is not expected for now, expected in zip(binding, self.binding)
        ):
            raise CommandRejected("owner_replaced")
        if data.api._web.closed:
            raise CommandRejected("unavailable")
        observed = data.observations.playback(entry_id)
        if not observed.get("ok"):
            raise CommandRejected(observed.get("error_kind", "unavailable"))
        playback = observed["playback"]
        if playback["target"] is None or playback["target"]["target_id"] != target_id:
            raise CommandRejected("target_not_current")
        raw = data.coordinator.data
        try:
            matches = [
                row
                for row in spotify_devices(raw.get("devices"))
                if row["id"] == target_id
            ]
        except HomeAssistantError as exc:
            raise CommandRejected("invalid_target") from exc
        if len(matches) != 1 or matches[0]["is_restricted"]:
            raise CommandRejected("invalid_target")
        provider = raw["playback"]
        device = provider["device"]
        if device.get("is_restricted") is not False:
            raise CommandRejected("invalid_target")
        disallows = (
            provider.get("actions", {}).get("disallows")
            if isinstance(provider.get("actions"), dict)
            else None
        )
        if not isinstance(disallows, dict) or any(
            type(flag) is not bool for flag in disallows.values()
        ):
            raise CommandRejected("capabilities_unavailable")
        actions = [
            action
            for action, key in _DISALLOWED.items()
            if not disallows.get(key, False)
        ]
        actions = [
            action
            for action in actions
            if not (
                action == "pause"
                and playback["state"] != "playing"
                or action == "resume"
                and playback["state"] != "paused"
            )
        ]
        volume = device.get("volume_percent")
        volume = volume if type(volume) is int and 0 <= volume <= 100 else None
        if device.get("supports_volume") is True and volume is not None:
            actions.append("volume")
        capacity = for_command or self.command_sequence < 2**53 - 1
        ready = (
            capacity
            and (for_command or not self.busy)
            and observed["poll_sequence"] > self.poll_floor
        )
        return {
            "ok": True,
            "schema": "podconnect.controls.v1",
            "config_entry_id": entry_id,
            "owner_epoch": observed["owner_epoch"],
            "poll_sequence": observed["poll_sequence"],
            "next_command_sequence": self.command_sequence + 1 if capacity else None,
            "observation": observed["observation"],
            "target": playback["target"],
            "actions": actions if ready else [],
            "volume_percent": volume,
            "track_play_supported": True,
            "status": "ready"
            if ready
            else "awaiting_observation"
            if capacity
            else "sequence_exhausted",
            "physical_result_verified": False,
        }

    def track_controls(self, entry_id, target_id, *, for_command=False):
        """Offer one exact fresh device, including an inactive selected output."""
        data = self.data
        if (
            data.coordinator.owner_epoch != self.owner_epoch
            or data.commands is not self
        ):
            raise CommandRejected("owner_replaced")
        binding = (
            data.api,
            data.coordinator,
            data.session,
            data.observations,
            data.speakers,
            data.api._session,
            data.api._web,
        )
        if (
            any(now is not expected for now, expected in zip(binding, self.binding))
            or data.api._web.closed
        ):
            raise CommandRejected("owner_replaced")
        observed = data.observations.playback(entry_id)
        if not observed.get("ok"):
            raise CommandRejected(observed.get("error_kind", "unavailable"))
        raw = data.coordinator.data
        try:
            matches = [
                row
                for row in spotify_devices(raw.get("devices"))
                if row["id"] == target_id
            ]
        except HomeAssistantError as exc:
            raise CommandRejected("invalid_target") from exc
        if (
            len(matches) != 1
            or matches[0]["is_restricted"]
            or type(matches[0].get("is_active")) is not bool
            or len(matches[0]["name"]) > 1024
        ):
            raise CommandRejected("invalid_target")
        volume = matches[0].get("volume_percent")
        if type(volume) is not int or not 0 <= volume <= 100:
            raise CommandRejected("capabilities_unavailable")
        capacity = for_command or self.command_sequence < 2**53 - 1
        ready = (
            capacity
            and (for_command or not self.busy)
            and observed["poll_sequence"] > self.poll_floor
        )
        return {
            "ok": True,
            "schema": "podconnect.controls.v1",
            "config_entry_id": entry_id,
            "owner_epoch": observed["owner_epoch"],
            "poll_sequence": observed["poll_sequence"],
            "next_command_sequence": self.command_sequence + 1 if capacity else None,
            "observation": observed["observation"],
            "target": {
                "kind": "spotify_device",
                "target_id": target_id,
                "name": matches[0]["name"],
            },
            "target_is_active": matches[0]["is_active"],
            "actions": ["play_track"] if ready else [],
            "volume_percent": volume,
            "track_play_supported": True,
            "status": "ready"
            if ready
            else "awaiting_observation"
            if capacity
            else "sequence_exhausted",
            "physical_result_verified": False,
        }

    async def track_start_controls(self, entry_id, target_id, room_id):
        """A track offer needs the native room/output join, never a name match."""
        result = self.track_controls(entry_id, target_id)
        if self.data.speakers is None:
            raise CommandRejected("native_start_unavailable")
        try:
            proof = await self.data.speakers.start_readiness(room_id, target_id)
        except SpeakersError as exc:
            raise CommandRejected("native_start_unavailable") from exc
        current_result = self.track_controls(entry_id, target_id)
        if (
            current_result["owner_epoch"] != result["owner_epoch"]
            or current_result["poll_sequence"] != result["poll_sequence"]
        ):
            raise CommandRejected("stale_observation")
        if proof["mode"] == "same_session":
            playback = self.data.observations.playback(entry_id)
            target = playback.get("playback", {}).get("target") if playback.get("ok") else None
            if not isinstance(target, dict) or target.get("target_id") != target_id:
                raise CommandRejected("native_account_unproven")
        result["native_start"] = proof
        return result

    async def execute(self, hass, request):
        entry_id, target_id = request["config_entry_id"], request["target_id"]
        epoch, sequence = request["owner_epoch"], request["expected_poll_sequence"]
        request_id, action = request["request_id"], request["action"]
        command_sequence = request["command_sequence"]
        receipt = {
            "ok": False,
            "schema": "podconnect.command.v1",
            "config_entry_id": entry_id,
            "owner_epoch": epoch,
            "request_id": request_id,
            "command_sequence": command_sequence,
            "action": action,
            "target": {"kind": "spotify_device", "target_id": target_id},
            "outcome": "rejected",
            "provider_request_accepted": False,
            "retry_allowed": False,
            "physical_result_verified": False,
        }

        def rejected(kind):
            return {**receipt, "error_kind": kind}

        if epoch != self.owner_epoch:
            return rejected("stale_owner")
        if request_id != f"{epoch}:{command_sequence}":
            return rejected("invalid_request_identity")
        try:
            _arguments(action, request["args"])
        except CommandRejected as exc:
            return rejected(str(exc))
        fingerprint = hashlib.sha256(
            json.dumps(request, sort_keys=True, allow_nan=False).encode()
        ).hexdigest()
        old = self.receipts.get(command_sequence)
        if old:
            return (
                copy.deepcopy(old[1])
                if old[0] == fingerprint
                else rejected("request_reused")
            )
        if command_sequence <= self.command_sequence:
            return rejected("stale_command")
        if command_sequence != self.command_sequence + 1:
            return rejected("command_sequence_gap")
        if self.busy:
            return rejected("busy")

        def guard():
            try:
                current(hass, entry_id, self.data)
                observed = (
                    self.track_controls(entry_id, target_id, for_command=True)
                    if action == "play_track"
                    else self.controls(entry_id, target_id, for_command=True)
                )
                if (
                    observed["owner_epoch"] != epoch
                    or observed["poll_sequence"] != sequence
                ):
                    raise CommandRejected("stale_observation")
                # busy belongs to this exact in-flight request; the prior terminal
                # command's floor still prevents acting on pre-command observations.
                if sequence <= self.poll_floor:
                    raise CommandRejected("awaiting_observation")
                if action not in observed["actions"]:
                    raise CommandRejected("unsupported_action")
                if (
                    action == "play_track"
                    and observed["volume_percent"] > request["args"]["max_volume"]
                ):
                    raise CommandRejected("volume_above_approved_limit")
            except HomeAssistantError as exc:
                raise CommandRejected("owner_replaced") from exc
            return True

        possible_send = False

        def sending():
            nonlocal possible_send
            possible_send = True

        self.busy = True
        # Monotonic admission is the permanent replay fence, even after eviction.
        self.command_sequence = command_sequence
        self.receipts[command_sequence] = (
            fingerprint,
            {**receipt, "outcome": "unknown"},
        )
        while len(self.receipts) > MAX_RECEIPTS:
            del self.receipts[next(iter(self.receipts))]
        try:
            guard()
            lease = None
            if action == "play_track":
                if self.data.speakers is None:
                    raise CommandRejected("native_start_unavailable")
                args = request["args"]
                try:
                    proof = await self.data.speakers.start_readiness(args["room_id"], target_id)
                except SpeakersError as exc:
                    raise CommandRejected("native_start_unavailable") from exc
                if (
                    proof["homepod_id"] != args["homepod_id"]
                    or proof["policy_revision"] != args["start_policy_revision"]
                    or proof["alias_binding"]["incarnation"] != args["alias_incarnation"]
                    or args["max_volume"] < proof["start_volume_percent"]
                    or (
                        proof["mode"] == "same_session"
                        and proof["output_volume_percent"] > args["max_volume"]
                    )
                ):
                    raise CommandRejected("native_start_policy_changed")
                if proof["mode"] == "same_session":
                    playback = self.data.observations.playback(entry_id)
                    current_target = playback.get("playback", {}).get("target") if playback.get("ok") else None
                    if not isinstance(current_target, dict) or current_target.get("target_id") != target_id:
                        raise CommandRejected("native_account_unproven")
                guard()
                try:
                    lease = await self.data.speakers.prepare_start(proof)
                except SpeakersError as exc:
                    raise CommandRejected("native_start_unavailable") from exc

            async def native_commit():
                guard()
                if lease is None:
                    raise CommandRejected("native_start_unavailable")
                try:
                    await self.data.speakers.commit_start(lease["lease_id"], lease["proof"]["policy_revision"], request["args"]["max_volume"])
                except SpeakersError as exc:
                    raise CommandRejected("native_start_unavailable") from exc

            methods = {
                "pause": self.data.api.pause,
                "resume": self.data.api.play,
                "next": self.data.api.next,
                "previous": self.data.api.previous,
                "volume": self.data.api.set_volume,
                "play_track": self.data.api.play,
            }
            kwargs = {"execution_guard": guard, "on_send": sending}
            async with asyncio.timeout(14 if action == "play_track" else COMMAND_TIMEOUT_S):
                if action == "play_track":
                    await methods[action](
                        target_id, uris=[request["args"]["track_uri"]],
                        async_execution_guard=native_commit, **kwargs
                    )
                elif action == "volume":
                    await methods[action](
                        request["args"]["percent"], target_id, **kwargs
                    )
                else:
                    await methods[action](target_id, **kwargs)
            receipt = {
                **receipt,
                "ok": True,
                "outcome": "accepted",
                "provider_request_accepted": True,
            }
        except CommandRejected as exc:
            receipt = rejected(str(exc))
        except asyncio.CancelledError:
            receipt = {
                **receipt,
                "outcome": "unknown" if possible_send else "rejected",
                "error_kind": "outcome_unknown" if possible_send else "unavailable",
            }
            raise
        except Exception:  # noqa: BLE001 - any possible send must retain an unknown receipt
            receipt = {
                **receipt,
                "outcome": "unknown" if possible_send else "rejected",
                "error_kind": "outcome_unknown" if possible_send else "unavailable",
            }
        finally:
            if possible_send:
                self.poll_floor = max(
                    self.poll_floor, self.data.coordinator.poll_sequence
                )
            receipt["observe_after_poll_sequence"] = self.poll_floor
            self.receipts[command_sequence] = (fingerprint, receipt)
            self.busy = False
        return copy.deepcopy(receipt)


def register_command_services(hass):
    text = vol.All(str, vol.Length(min=1, max=128))
    target = vol.All(str, vol.Length(min=1, max=1024))
    identity = {
        vol.Required("config_entry_id"): target,
        vol.Required("kind"): vol.In(("spotify_device",)),
        vol.Required("target_id"): target,
    }

    async def controls(call):
        entry_id = call.data["config_entry_id"]
        data = current(hass, entry_id)
        try:
            return data.commands.controls(entry_id, call.data["target_id"])
        except CommandRejected as exc:
            return {"ok": False, "error_kind": str(exc), "retry_allowed": False}

    async def track_controls(call):
        entry_id = call.data["config_entry_id"]
        data = current(hass, entry_id)
        try:
            return await data.commands.track_start_controls(entry_id, call.data["target_id"], call.data["room_id"])
        except CommandRejected as exc:
            return {"ok": False, "error_kind": str(exc), "retry_allowed": False}

    async def command(call):
        data = current(hass, call.data["config_entry_id"])
        return await data.commands.execute(hass, dict(call.data))

    command_schema = {
        **identity,
        vol.Required("owner_epoch"): text,
        vol.Required("expected_poll_sequence"): _sequence,
        vol.Required("request_id"): vol.All(str, vol.Length(min=1, max=145)),
        vol.Required("command_sequence"): _sequence,
        vol.Required("action"): vol.In(_COMMAND_ACTIONS),
        vol.Required("args"): dict,
    }
    for name, handler, schema in (
        ("get_playback_controls", controls, identity),
        ("get_track_play_controls", track_controls, {**identity, vol.Required("room_id"): target}),
        ("control_playback", command, command_schema),
    ):
        if not hass.services.has_service(DOMAIN, name):
            hass.services.async_register(
                DOMAIN,
                name,
                handler,
                schema=vol.Schema(schema),
                supports_response=SupportsResponse.ONLY,
            )
