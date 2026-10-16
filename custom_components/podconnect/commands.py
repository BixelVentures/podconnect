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
_COMMAND_ACTIONS = (*_ACTIONS, "play_track", "play_context", "play_queue_occurrence")
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
    if action == "play_queue_occurrence":
        from .queue_controls import valid_arguments

        if not valid_arguments(args):
            raise CommandRejected("invalid_arguments")
        return
    if not isinstance(args, dict) or set(args) != (
        {"percent"}
        if action == "volume"
        else {
            "context_uri" if action == "play_context" else "track_uri",
            "max_volume",
            "room_id",
            "homepod_id",
            "start_policy_revision",
            "alias_incarnation",
        }
        if action in {"play_track", "play_context"}
        else set()
    ):
        raise CommandRejected("invalid_arguments")
    if action == "volume":
        try:
            _integer(args["percent"])
        except vol.Invalid as exc:
            raise CommandRejected("invalid_arguments") from exc
    if action in {"play_track", "play_context"} and (
        not isinstance(
            args.get("context_uri" if action == "play_context" else "track_uri"), str
        )
        or re.fullmatch(
            r"spotify:playlist:[A-Za-z0-9]{22}"
            if action == "play_context"
            else r"spotify:track:[A-Za-z0-9]{22}",
            args["context_uri" if action == "play_context" else "track_uri"],
        )
        is None
    ):
        raise CommandRejected("invalid_arguments")
    if action in {"play_track", "play_context"}:
        try:
            _integer(args["max_volume"])
        except vol.Invalid as exc:
            raise CommandRejected("invalid_arguments") from exc
        if (
            any(
                not isinstance(args[key], str) or not 1 <= len(args[key]) <= 128
                for key in ("room_id", "homepod_id", "alias_incarnation")
            )
            or not isinstance(args["start_policy_revision"], str)
            or len(args["start_policy_revision"]) != 64
        ):
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
        self.queue_controls = None
        from .room_transport import RoomTransport

        self.room_transport = RoomTransport(self)
        from .room_volume import RoomVolume

        self.room_volume = RoomVolume(self)
        from .room_content import RoomContent

        self.room_content = RoomContent(self)
        from .room_move import RoomMove

        self.room_move = RoomMove(self)
        coordinator = data.coordinator
        coordinator.command_owner_guard = lambda: self._refresh_owner_current(
            coordinator.hass, coordinator.config_entry.entry_id
        )

    def retire_preparations_before(self, hass, sequence):
        for preparation in (self.room_content, self.room_move):
            preparation.retire_before(hass, sequence)

    def _refresh_owner_current(self, hass, entry_id):
        try:
            current(hass, entry_id, self.data)
        except HomeAssistantError:
            return False
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
        return (
            data.commands is self
            and data.coordinator.owner_epoch == self.owner_epoch
            and all(now is expected for now, expected in zip(binding, self.binding))
            and not data.api._web.closed
        )

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
            target = (
                playback.get("playback", {}).get("target")
                if playback.get("ok")
                else None
            )
            if not isinstance(target, dict) or target.get("target_id") != target_id:
                raise CommandRejected("native_account_unproven")
        result["native_start"] = proof
        return result

    async def queue_start_controls(self, hass, entry_id, target_id, room_id):
        from .queue_controls import QueueControls

        if self.queue_controls is None:
            self.queue_controls = QueueControls(self)
        return await self.queue_controls.read(hass, entry_id, target_id, room_id)

    async def execute(self, hass, request, *, room_transport=None, room_volume=None):
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

        if room_transport is not None:
            receipt["accepted_native"] = False
        if room_volume is not None:
            receipt.update(
                protocol_accepted=False,
                reservation_accepted=False,
                possible_effect=False,
            )

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
        if room_volume is not None:
            from .room_volume import valid_context as valid_volume_context

            if (
                room_transport is not None
                or action != "volume"
                or not valid_volume_context(room_volume, target_id)
            ):
                return rejected("invalid_arguments")
        if room_transport is not None:
            from .room_transport import ACTIONS, valid_context, valid_identity

            if action not in ACTIONS or not valid_context(room_transport):
                return rejected("invalid_arguments")
            scope = {
                "account_user_id": room_transport["native"].get("account_user_id")
                if isinstance(room_transport["native"], dict)
                else None,
                "target_id": target_id,
                "room_id": room_transport["room_id"],
                "homepod_id": room_transport["homepod_id"],
            }
            if not valid_identity(
                room_transport["native"], room_transport["route"], scope
            ):
                return rejected("invalid_room_identity")
        fingerprint = hashlib.sha256(
            json.dumps(
                {"request": request, "room_volume": room_volume}
                if room_volume is not None
                else {"request": request, "room_transport": room_transport}
                if room_transport is not None
                else request,
                sort_keys=True,
                allow_nan=False,
            ).encode()
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
                    if action in {"play_track", "play_context"}
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
                if (
                    room_volume is None
                    and (
                        "play_track"
                        if action == "play_context"
                        else "next"
                        if action == "play_queue_occurrence"
                        else action
                    )
                    not in observed["actions"]
                ):
                    raise CommandRejected("unsupported_action")
                if (
                    action in {"play_track", "play_context"}
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
            if room_volume is not None:
                receipt["possible_effect"] = True

        self.busy = True
        # Monotonic admission is the permanent replay fence, even after eviction.
        self.retire_preparations_before(hass, command_sequence)
        self.command_sequence = command_sequence
        self.receipts[command_sequence] = (
            fingerprint,
            {**receipt, "outcome": "unknown"},
        )
        while len(self.receipts) > MAX_RECEIPTS:
            del self.receipts[next(iter(self.receipts))]
        try:
            guard()
            if room_volume is not None:
                async with asyncio.timeout(8):
                    ack = await self.room_volume.commit(
                        hass,
                        entry_id,
                        target_id,
                        room_volume,
                        request["args"]["percent"],
                        guard,
                        sending,
                    )
                receipt.update(
                    ok=ack["protocol_accepted"],
                    outcome="accepted" if ack["protocol_accepted"] else "unknown",
                    protocol_accepted=ack["protocol_accepted"],
                    reservation_accepted=ack["reservation_accepted"],
                    possible_effect=ack["possible_effect"],
                    volume_operation_state=ack["state"],
                )
                if not ack["protocol_accepted"]:
                    receipt["error_kind"] = "outcome_unknown"
            elif room_transport is not None:
                if action not in ("pause", "resume", "next", "previous"):
                    raise CommandRejected("unsupported_action")
                async with asyncio.timeout(18):
                    await self.room_transport.commit(
                        hass,
                        entry_id,
                        target_id,
                        room_transport,
                        action,
                        guard,
                        sending,
                    )
                receipt = {
                    **receipt,
                    "ok": True,
                    "outcome": "accepted",
                    "accepted_native": True,
                }
            else:
                lease = None
                if action in {"play_track", "play_context"}:
                    if self.data.speakers is None:
                        raise CommandRejected("native_start_unavailable")
                    args = request["args"]
                    try:
                        proof = await self.data.speakers.start_readiness(
                            args["room_id"], target_id
                        )
                    except SpeakersError as exc:
                        raise CommandRejected("native_start_unavailable") from exc
                    if (
                        proof["homepod_id"] != args["homepod_id"]
                        or proof["policy_revision"] != args["start_policy_revision"]
                        or proof["alias_binding"]["incarnation"]
                        != args["alias_incarnation"]
                        or args["max_volume"] < proof["start_volume_percent"]
                        or (
                            proof["mode"] == "same_session"
                            and proof["output_volume_percent"] > args["max_volume"]
                        )
                    ):
                        raise CommandRejected("native_start_policy_changed")
                    if proof["mode"] == "same_session":
                        playback = self.data.observations.playback(entry_id)
                        current_target = (
                            playback.get("playback", {}).get("target")
                            if playback.get("ok")
                            else None
                        )
                        if (
                            not isinstance(current_target, dict)
                            or current_target.get("target_id") != target_id
                        ):
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
                        await self.data.speakers.commit_start(
                            lease["lease_id"],
                            lease["proof"]["policy_revision"],
                            request["args"]["max_volume"],
                        )
                    except SpeakersError as exc:
                        raise CommandRejected("native_start_unavailable") from exc

                methods = {
                    "pause": self.data.api.pause,
                    "resume": self.data.api.play,
                    "next": self.data.api.next,
                    "previous": self.data.api.previous,
                    "volume": self.data.api.set_volume,
                    "play_track": self.data.api.play,
                    "play_context": self.data.api.play,
                }
                kwargs = {"execution_guard": guard, "on_send": sending}
                async with asyncio.timeout(
                    14
                    if action in {"play_track", "play_context"}
                    else 8
                    if action == "play_queue_occurrence"
                    else COMMAND_TIMEOUT_S
                ):
                    if action == "play_queue_occurrence":
                        if self.queue_controls is None:
                            raise CommandRejected("native_queue_stale")
                        await self.queue_controls.commit(
                            hass, entry_id, target_id, request["args"], guard, sending
                        )
                    elif action in {"play_track", "play_context"}:
                        await methods[action](
                            target_id,
                            **(
                                {"context_uri": request["args"]["context_uri"]}
                                if action == "play_context"
                                else {"uris": [request["args"]["track_uri"]]}
                            ),
                            async_execution_guard=native_commit,
                            **kwargs,
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
            receipt = (
                {**receipt, "outcome": "unknown", "error_kind": "outcome_unknown"}
                if (room_transport is not None or room_volume is not None)
                and possible_send
                else rejected(str(exc))
            )
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
            if (
                room_volume is not None
                and possible_send
                and receipt["outcome"] == "unknown"
            ):
                receipt["possible_effect"] = True
            if possible_send:
                self.poll_floor = max(
                    self.poll_floor, self.data.coordinator.poll_sequence
                )
            receipt["observe_after_poll_sequence"] = self.poll_floor
            self.receipts[command_sequence] = (fingerprint, receipt)
            self.busy = False
            if possible_send and self._refresh_owner_current(hass, entry_id):
                await self.binding[1].async_refresh_after_command()
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
            return await data.commands.track_start_controls(
                entry_id, call.data["target_id"], call.data["room_id"]
            )
        except CommandRejected as exc:
            return {"ok": False, "error_kind": str(exc), "retry_allowed": False}

    async def context_controls(call):
        result = await track_controls(call)
        if result.get("ok") is True:
            result["actions"] = ["play_context"] if result["status"] == "ready" else []
            result["context_play_supported"] = True
        return result

    async def queue_controls(call):
        entry_id = call.data["config_entry_id"]
        data = current(hass, entry_id)
        try:
            return await data.commands.queue_start_controls(
                hass, entry_id, call.data["target_id"], call.data["room_id"]
            )
        except CommandRejected as exc:
            return {"ok": False, "error_kind": str(exc), "retry_allowed": False}

    async def room_controls(call):
        entry_id = call.data["config_entry_id"]
        data = current(hass, entry_id)
        try:
            return await data.commands.room_transport.read(
                hass,
                entry_id,
                call.data["target_id"],
                call.data["room_id"],
                call.data["homepod_id"],
            )
        except CommandRejected as exc:
            return {"ok": False, "error_kind": str(exc), "retry_allowed": False}

    async def room_command(call):
        data = current(hass, call.data["config_entry_id"])
        request = dict(call.data)
        context = {
            k: request.pop(k) for k in ("room_id", "homepod_id", "native", "route")
        }
        return await data.commands.execute(hass, request, room_transport=context)

    async def volume_controls(call):
        entry_id = call.data["config_entry_id"]
        data = current(hass, entry_id)
        try:
            return await data.commands.room_volume.read(
                hass,
                entry_id,
                call.data["target_id"],
                call.data["room_id"],
                call.data["homepod_id"],
            )
        except CommandRejected as exc:
            return {"ok": False, "error_kind": str(exc), "retry_allowed": False}

    async def volume_command(call):
        data = current(hass, call.data["config_entry_id"])
        request = dict(call.data)
        context = {
            k: request.pop(k)
            for k in ("room_id", "homepod_id", "native", "route", "volume_state")
        }
        return await data.commands.execute(hass, request, room_volume=context)

    async def content_controls(call):
        entry_id = call.data["config_entry_id"]
        data = current(hass, entry_id)
        try:
            return await data.commands.room_content.read(
                hass,
                entry_id,
                call.data["target_id"],
                call.data["room_id"],
                call.data["homepod_id"],
            )
        except CommandRejected as exc:
            return {
                "ok": False,
                "error_kind": str(exc),
                "retry_allowed": False,
                "physical_result_verified": False,
            }

    async def content_prepare(call):
        data = current(hass, call.data["config_entry_id"])
        return await data.commands.room_content.prepare(hass, dict(call.data))

    async def content_start(call):
        data = current(hass, call.data["config_entry_id"])
        return await data.commands.room_content.start(hass, dict(call.data))

    async def content_cancel(call):
        data = current(hass, call.data["config_entry_id"])
        return await data.commands.room_content.cancel(hass, dict(call.data))

    async def move_controls(call):
        entry_id = call.data["config_entry_id"]
        data = current(hass, entry_id)
        try:
            return await data.commands.room_move.read(
                hass,
                entry_id,
                call.data["target_id"],
                call.data["room_id"],
                call.data["homepod_id"],
            )
        except CommandRejected as exc:
            return {
                "ok": False,
                "error_kind": str(exc),
                "retry_allowed": False,
                "physical_result_verified": False,
            }

    async def move_prepare(call):
        data = current(hass, call.data["config_entry_id"])
        return await data.commands.room_move.prepare(hass, dict(call.data))

    async def move_start(call):
        data = current(hass, call.data["config_entry_id"])
        return await data.commands.room_move.start(hass, dict(call.data))

    async def move_cancel(call):
        data = current(hass, call.data["config_entry_id"])
        return await data.commands.room_move.cancel(hass, dict(call.data))

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
    room_identity = {
        **identity,
        vol.Required("room_id"): target,
        vol.Required("homepod_id"): target,
    }
    room_command_schema = {
        **command_schema,
        **room_identity,
        vol.Required("action"): vol.In(("pause", "resume", "next", "previous")),
        vol.Required("native"): dict,
        vol.Required("route"): dict,
    }
    volume_command_schema = {
        **command_schema,
        **room_identity,
        vol.Required("action"): vol.In(("volume",)),
        vol.Required("native"): dict,
        vol.Required("route"): dict,
        vol.Required("volume_state"): dict,
    }
    content_command_schema = {
        **command_schema,
        **room_identity,
        vol.Required("action"): vol.In(("play_track", "play_context")),
        vol.Required("start_offer"): dict,
    }
    content_cancel_schema = {
        vol.Required("config_entry_id"): target,
        vol.Required("owner_epoch"): text,
        vol.Required("command_sequence"): _sequence,
        vol.Required("request_id"): vol.All(str, vol.Length(min=1, max=145)),
        vol.Required("offer_id"): text,
        vol.Required("binding_id"): text,
    }
    move_command_schema = {
        **command_schema,
        **room_identity,
        vol.Required("action"): vol.In(("move_existing_session",)),
        vol.Required("move_offer"): dict,
    }
    for name, handler, schema in (
        ("get_playback_controls", controls, identity),
        ("get_room_move_controls", move_controls, room_identity),
        ("prepare_room_move_playback", move_prepare, move_command_schema),
        ("move_room_playback", move_start, move_command_schema),
        ("cancel_room_move_preparation", move_cancel, content_cancel_schema),
        ("get_room_content_controls", content_controls, room_identity),
        ("prepare_room_selected_content", content_prepare, content_command_schema),
        ("start_room_selected_content", content_start, content_command_schema),
        ("cancel_room_content_preparation", content_cancel, content_cancel_schema),
        ("get_room_volume_controls", volume_controls, room_identity),
        ("control_room_volume", volume_command, volume_command_schema),
        ("get_room_playback_controls", room_controls, room_identity),
        ("control_room_playback", room_command, room_command_schema),
        (
            "get_track_play_controls",
            track_controls,
            {**identity, vol.Required("room_id"): target},
        ),
        (
            "get_context_play_controls",
            context_controls,
            {**identity, vol.Required("room_id"): target},
        ),
        (
            "get_queue_controls",
            queue_controls,
            {
                vol.Required("config_entry_id"): target,
                vol.Required("target_id"): target,
                vol.Required("room_id"): target,
            },
        ),
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
