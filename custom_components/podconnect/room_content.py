"""Selected-content staging and explicit commit under the canonical owner."""

from __future__ import annotations

import asyncio
import copy
import hashlib
import json
import re
import time

_OFFER_KEYS = {
    "schema",
    "offer_id",
    "binding_id",
    "native_incarnation",
    "policy_revision",
    "start_volume_percent",
    "expires_after_ms",
}
_RECEIPT_KEYS = {
    "ok",
    "schema",
    "config_entry_id",
    "owner_epoch",
    "request_id",
    "command_sequence",
    "action",
    "target",
    "outcome",
    "provider_request_accepted",
    "retry_allowed",
    "physical_result_verified",
    "accepted_selected_content",
    "start_operation_id",
    "start_volume_percent",
    "room_id",
    "homepod_id",
}
_IDENTITY_KEYS = {
    "config_entry_id",
    "owner_epoch",
    "request_id",
    "command_sequence",
    "action",
    "target",
    "room_id",
    "homepod_id",
}
_HEX32 = re.compile(r"[a-f0-9]{32}")
_HEX64 = re.compile(r"[a-f0-9]{64}")
_TRACK = re.compile(r"spotify:track:[A-Za-z0-9]{22}")
_PLAYLIST = re.compile(r"spotify:playlist:[A-Za-z0-9]{22}")


def _text(value, limit=1024):
    return isinstance(value, str) and 1 <= len(value) <= limit


def _hex(value, pattern):
    return isinstance(value, str) and pattern.fullmatch(value) is not None


def valid_start_offer(value):
    return (
        isinstance(value, dict)
        and set(value) == _OFFER_KEYS
        and value["schema"] == "podconnect.room_content_offer.v1"
        and all(_hex(value[k], _HEX32) for k in ("offer_id", "native_incarnation"))
        and all(_hex(value[k], _HEX64) for k in ("binding_id", "policy_revision"))
        and type(value["start_volume_percent"]) is int
        and value["start_volume_percent"] == 35
        and type(value["expires_after_ms"]) is int
        and 1 <= value["expires_after_ms"] <= 5000
    )


def valid_selected_arguments(action, value):
    if not isinstance(action, str) or action not in {"play_track", "play_context"}:
        return False
    key, pattern = (
        ("track_uri", _TRACK) if action == "play_track" else ("context_uri", _PLAYLIST)
    )
    return (
        isinstance(value, dict)
        and set(value) == {key, "max_volume"}
        and isinstance(value[key], str)
        and pattern.fullmatch(value[key]) is not None
        and type(value["max_volume"]) is int
        and 35 <= value["max_volume"] <= 100
    )


def valid_selected_receipt(
    value,
    identity,
    *,
    accepted_key="accepted_selected_content",
    operation_key="start_operation_id",
    volume_key="start_volume_percent",
    actions=("play_track", "play_context"),
    volume_valid=None,
):
    if volume_valid is None:
        volume_valid = lambda volume: type(volume) is int and volume == 35
    keys = (
        _RECEIPT_KEYS
        - {"accepted_selected_content", "start_operation_id", "start_volume_percent"}
    ) | {accepted_key, operation_key, volume_key}
    if (
        not isinstance(value, dict)
        or not keys <= set(value)
        or set(value) - keys - {"error_kind", "observe_after_poll_sequence"}
        or not isinstance(identity, dict)
        or set(identity) != _IDENTITY_KEYS
        or any(value[k] != identity[k] for k in identity)
        or value["schema"] != "podconnect.command.v1"
        or not isinstance(value["action"], str)
        or value["action"] not in actions
        or not all(
            _text(value[k]) for k in ("config_entry_id", "room_id", "homepod_id")
        )
        or not _text(value["owner_epoch"], 128)
        or type(value["command_sequence"]) is not int
        or not 1 <= value["command_sequence"] < 2**53
        or value["request_id"] != f"{value['owner_epoch']}:{value['command_sequence']}"
        or not isinstance(value["target"], dict)
        or set(value["target"]) != {"kind", "target_id"}
        or value["target"]["kind"] != "spotify_device"
        or not _text(value["target"]["target_id"])
        or not isinstance(value["outcome"], str)
        or value["outcome"] not in {"accepted", "unknown", "rejected"}
        or value["provider_request_accepted"] is not False
        or value["retry_allowed"] is not False
        or value["physical_result_verified"] is not False
    ):
        return False
    accepted = value["outcome"] == "accepted"
    if value["ok"] is not accepted or value[accepted_key] is not accepted:
        return False
    operation = value[operation_key]
    volume = value[volume_key]
    if operation is not None and not _hex(operation, _HEX32):
        return False
    if volume is not None and not volume_valid(volume):
        return False
    if accepted and (operation is None or not volume_valid(volume)):
        return False
    if "observe_after_poll_sequence" in value and (
        type(value["observe_after_poll_sequence"]) is not int
        or not 0 <= value["observe_after_poll_sequence"] < 2**53
    ):
        return False
    return "error_kind" not in value or _text(value["error_kind"], 128)


_MANAGER_KEYS = {
    "schema",
    "ok",
    "offer_id",
    "binding_id",
    "state",
    "accepted_selected_content",
    "start_operation_id",
    "start_volume_percent",
    "room_id",
    "homepod_id",
    "physical_result_verified",
}
_MANAGER_STATES = {
    "staging",
    "staged",
    "committing",
    "complete",
    "rejected",
    "cancelled",
    "unknown",
}
STAGING_TIMEOUT_S = 32
COMMIT_TIMEOUT_S = 16
STATUS_INTERVAL_S = 0.1
MAX_CONTENT_RECORDS = 128
STAGED_COMMIT_TTL_S = 5


def valid_manager_result(
    value,
    request,
    suffix,
    *,
    namespace="room_content",
    accepted_key="accepted_selected_content",
    operation_key="start_operation_id",
    volume_key="start_volume_percent",
    offer_key="start_offer",
    expected_volume=35,
):
    keys = (
        _MANAGER_KEYS
        - {"accepted_selected_content", "start_operation_id", "start_volume_percent"}
    ) | {accepted_key, operation_key, volume_key}
    if (
        not isinstance(value, dict)
        or set(value) != keys
        or value["schema"] != "podconnect." + namespace + "_" + suffix + ".v1"
        or value["offer_id"] != request[offer_key]["offer_id"]
        or value["binding_id"] != request[offer_key]["binding_id"]
        or value["room_id"] != request["room_id"]
        or value["homepod_id"] != request["homepod_id"]
        or not isinstance(value["state"], str)
        or value["state"] not in _MANAGER_STATES
        or value["physical_result_verified"] is not False
    ):
        return False
    complete = value["state"] == "complete"
    if value["ok"] is not complete or value[accepted_key] is not complete:
        return False
    if value[operation_key] not in (None, request[offer_key]["offer_id"]):
        return False
    volume = value[volume_key]
    if volume is not None and (type(volume) is not int or volume != expected_volume):
        return False
    return not complete or (
        value[operation_key] is not None and volume == expected_volume
    )


class RoomContent:
    """Staging records use the existing canonical ledger, never another counter."""

    namespace = "room_content"
    offer_key = "start_offer"
    accepted_key = "accepted_selected_content"
    operation_key = "start_operation_id"
    volume_key = "start_volume_percent"
    actions = ("play_track", "play_context")
    staging_timeout = STAGING_TIMEOUT_S
    valid_offer = staticmethod(valid_start_offer)
    valid_arguments = staticmethod(valid_selected_arguments)

    def valid_result(self, value, request, suffix):
        return valid_manager_result(value, request, suffix)

    def expected_volume(self, request):
        return 35

    def client_method(self, client, purpose):
        method = (
            self.namespace + "_state"
            if purpose == "state"
            else self.namespace + "_result"
            if purpose == "result"
            else purpose + "_" + self.namespace
        )
        return getattr(client, method)

    def __init__(self, commands):
        self.commands = commands
        self.records = {}
        self.cleanups = set()

    def _identity(self, request):
        return {
            "config_entry_id": request["config_entry_id"],
            "owner_epoch": request["owner_epoch"],
            "request_id": request["request_id"],
            "command_sequence": request["command_sequence"],
            "action": request["action"],
            "target": {"kind": "spotify_device", "target_id": request["target_id"]},
            "room_id": request["room_id"],
            "homepod_id": request["homepod_id"],
        }

    def _receipt(self, request, outcome="rejected", error="unavailable"):
        result = {
            **self._identity(request),
            "schema": "podconnect.command.v1",
            "ok": outcome == "accepted",
            "outcome": outcome,
            "provider_request_accepted": False,
            "retry_allowed": False,
            "physical_result_verified": False,
            self.accepted_key: outcome == "accepted",
            self.operation_key: None,
            self.volume_key: None,
        }
        if outcome != "accepted":
            result["error_kind"] = error
        return result

    def _preparation(self, request, state, *, cancel=False):
        return {
            **self._identity(request),
            "schema": "podconnect." + self.namespace + "_preparation_cancel.v1"
            if cancel
            else "podconnect." + self.namespace + "_preparation.v1",
            "ok": state == ("cancelled" if cancel else "staged"),
            "state": state,
            "retry_allowed": False,
            "physical_result_verified": False,
        }

    def _fingerprint(self, request):
        return hashlib.sha256(
            json.dumps(
                {self.namespace: request}, sort_keys=True, allow_nan=False
            ).encode()
        ).hexdigest()

    def _validate(self, request):
        from .commands import CommandRejected

        if (
            not self.valid_offer(request[self.offer_key])
            or not self.valid_arguments(request["action"], request["args"])
            or request["owner_epoch"] != self.commands.owner_epoch
            or request["request_id"]
            != f"{request['owner_epoch']}:{request['command_sequence']}"
        ):
            raise CommandRejected("invalid_arguments")

    def _owner(self, hass, request):
        from .commands import CommandRejected

        if not self.commands._refresh_owner_current(hass, request["config_entry_id"]):
            raise CommandRejected("owner_replaced")
        config = hass.config_entries.async_get_entry(request["config_entry_id"])
        if config is None or not _text(config.unique_id):
            raise CommandRejected("native_account_unproven")
        if self.commands.data.speakers is None:
            raise CommandRejected("native_start_unavailable")
        return {
            "account_user_id": config.unique_id,
            "target_id": request["target_id"],
            "room_id": request["room_id"],
            "homepod_id": request["homepod_id"],
        }

    def _fresh(self, hass, request, *, initial=False, scope=None, floor=None):
        from .commands import CommandRejected

        actual = self._owner(hass, request)
        if scope is not None and actual != scope:
            raise CommandRejected("native_account_unproven")
        observed = self.commands.data.observations.playback(request["config_entry_id"])
        if not observed.get("ok"):
            raise CommandRejected(observed.get("error_kind", "unavailable"))
        if observed["owner_epoch"] != request["owner_epoch"]:
            raise CommandRejected("owner_replaced")
        sequence = observed["poll_sequence"]
        if (
            sequence != request["expected_poll_sequence"]
            if initial
            else sequence < request["expected_poll_sequence"]
        ):
            raise CommandRejected("stale_observation")
        if sequence <= (self.commands.poll_floor if floor is None else floor):
            raise CommandRejected("awaiting_observation")
        return actual, observed

    def _schedule_cancel(self, hass, record):
        if record.get("cancel_scheduled"):
            return
        record["cancel_scheduled"] = True
        client, request = record["client"], record["request"]

        async def cancel():
            from .speakers import SpeakersError

            try:
                async with asyncio.timeout(1):
                    await self.client_method(client, "cancel")(
                        request[self.offer_key]["offer_id"],
                        request[self.offer_key]["binding_id"],
                    )
            except (SpeakersError, TimeoutError):
                return

        task = hass.async_create_background_task(
            cancel(), "podconnect staged content cleanup", eager_start=False
        )
        self.cleanups.add(task)
        task.add_done_callback(self.cleanups.discard)

    def retire_before(self, hass, sequence):
        for old_sequence, record in self.records.items():
            if old_sequence < sequence and record["state"] in {"staging", "staged"}:
                record["state"] = "expired"
                if record["preparation"]["state"] == "unknown":
                    record["preparation"] = self._preparation(
                        record["request"], "expired"
                    )
                terminal = self._receipt(record["request"], error="stale_command")
                record["terminal"] = terminal
                self.commands.receipts[old_sequence] = record["fingerprint"], terminal
                self._schedule_cancel(hass, record)

    async def read(self, hass, entry, target, room, homepod):
        from .commands import CommandRejected

        request = {
            "config_entry_id": entry,
            "target_id": target,
            "room_id": room,
            "homepod_id": homepod,
        }
        scope = self._owner(hass, request)
        before = self.commands.data.observations.playback(entry)
        if not before.get("ok"):
            raise CommandRejected(before.get("error_kind", "unavailable"))
        from .speakers import SpeakersError

        try:
            result = await self.client_method(self.commands.data.speakers, "state")(
                scope
            )
        except SpeakersError as exc:
            if self._owner(hass, request) != scope:
                raise CommandRejected("native_account_unproven") from exc
            if (
                getattr(exc, "content_read_reason", None)
                == "native_account_claim_required"
            ):
                raise CommandRejected("native_account_claim_required") from exc
            raise CommandRejected("native_start_unavailable") from exc
        if self._owner(hass, request) != scope:
            raise CommandRejected("native_account_unproven")
        observed = self.commands.data.observations.playback(entry)
        if not observed.get("ok") or any(
            before[k] != observed[k] for k in ("owner_epoch", "poll_sequence")
        ):
            raise CommandRejected("stale_observation")
        if (
            not isinstance(result, dict)
            or set(result)
            != {"schema", "ok", self.offer_key, "actions", "physical_result_verified"}
            or result["schema"] != "podconnect." + self.namespace + "_state.v1"
            or result["ok"] is not True
            or not self.valid_offer(result[self.offer_key])
            or result["physical_result_verified"] is not False
            or not isinstance(result["actions"], list)
            or any(
                not isinstance(a, str) or a not in self.actions
                for a in result["actions"]
            )
            or len(result["actions"]) != len(set(result["actions"]))
        ):
            raise CommandRejected("native_start_unavailable")
        capacity = self.commands.command_sequence < 2**53 - 1
        ready = (
            capacity
            and not self.commands.busy
            and observed["poll_sequence"] > self.commands.poll_floor
        )
        return {
            "ok": True,
            "schema": "podconnect." + self.namespace + "_controls.v1",
            "config_entry_id": entry,
            "owner_epoch": observed["owner_epoch"],
            "poll_sequence": observed["poll_sequence"],
            "next_command_sequence": self.commands.command_sequence + 1
            if capacity
            else None,
            "observation": observed["observation"],
            "target": {"kind": "spotify_device", "target_id": target},
            "room_id": room,
            "homepod_id": homepod,
            "actions": copy.deepcopy(result["actions"]) if ready else [],
            "status": "ready"
            if ready
            else "awaiting_observation"
            if capacity
            else "sequence_exhausted",
            self.offer_key: copy.deepcopy(result[self.offer_key]),
            "physical_result_verified": False,
        }

    async def prepare(self, hass, request):
        from .commands import MAX_RECEIPTS, CommandRejected
        from .speakers import SpeakersError

        commands = self.commands
        try:
            self._validate(request)
            fingerprint = self._fingerprint(request)
        except CommandRejected:
            return self._preparation(request, "rejected")
        sequence = request["command_sequence"]
        old = self.records.get(sequence)
        if old is not None:
            if old["fingerprint"] != fingerprint:
                return self._preparation(request, "rejected")
            if old["state"] == "staging":
                async with asyncio.timeout(self.staging_timeout + 1):
                    await old["done"].wait()
            return copy.deepcopy(old["preparation"])
        if (
            sequence in commands.receipts
            or sequence != commands.command_sequence + 1
            or commands.busy
            or len(self.cleanups) >= 8
        ):
            return self._preparation(request, "rejected")
        try:
            scope, _ = self._fresh(hass, request, initial=True)
        except CommandRejected:
            return self._preparation(request, "rejected")
        commands.retire_preparations_before(hass, sequence)
        # This synchronous reservation does not block later physical intents
        # while detached metadata/media staging awaits.
        commands.command_sequence = sequence
        record = {
            "request": copy.deepcopy(request),
            "fingerprint": fingerprint,
            "scope": scope,
            "floor": commands.poll_floor,
            "state": "staging",
            "client": commands.data.speakers,
            "preparation": self._preparation(request, "unknown"),
            "terminal": None,
            "done": asyncio.Event(),
        }
        self.records[sequence] = record
        # A typed reservation is not a published canonical unknown completion.
        commands.receipts[sequence] = (
            fingerprint,
            {"schema": "podconnect.content_reservation.v1", "state": "staging"},
        )
        while len(commands.receipts) > MAX_RECEIPTS:
            del commands.receipts[next(iter(commands.receipts))]
        while len(self.records) > MAX_CONTENT_RECORDS:
            del self.records[next(iter(self.records))]
        try:
            async with asyncio.timeout(self.staging_timeout):
                result = await self.client_method(record["client"], "prepare")(
                    scope, request
                )
                while True:
                    if (
                        self._owner(hass, request) != scope
                        or record["state"] != "staging"
                    ):
                        raise CommandRejected("owner_replaced")
                    if not self.valid_result(
                        result,
                        request,
                        "prepare" if not record.get("queried") else "result",
                    ):
                        raise SpeakersError("Invalid content staging receipt")
                    if result["state"] == "staged":
                        record["state"] = "staged"
                        record["staged_at"] = time.monotonic()
                        record["preparation"] = self._preparation(request, "staged")
                        return copy.deepcopy(record["preparation"])
                    if result["state"] != "staging":
                        raise CommandRejected("native_start_unavailable")
                    await asyncio.sleep(STATUS_INTERVAL_S)
                    self._owner(hass, request)
                    record["queried"] = True
                    result = await self.client_method(record["client"], "result")(
                        request[self.offer_key]["offer_id"],
                        request[self.offer_key]["binding_id"],
                    )
        except asyncio.CancelledError:
            record["state"] = "cancelled"
            record["preparation"] = self._preparation(request, "cancelled")
            raise
        except (CommandRejected, SpeakersError, TimeoutError):
            if record["state"] not in {"cancelled", "expired"}:
                record["state"] = "unknown"
                record["preparation"] = self._preparation(request, "unknown")
        finally:
            if record["state"] != "staged":
                if record["terminal"] is None:
                    record["terminal"] = self._receipt(
                        request, error="native_start_unavailable"
                    )
                if sequence in commands.receipts:
                    commands.receipts[sequence] = fingerprint, record["terminal"]
                self._schedule_cancel(hass, record)
            record["done"].set()
        return copy.deepcopy(record["preparation"])

    async def start(self, hass, request):
        from .commands import CommandRejected
        from .speakers import SpeakersError

        try:
            self._validate(request)
            fingerprint = self._fingerprint(request)
        except CommandRejected:
            return self._receipt(request, error="invalid_arguments")
        sequence = request["command_sequence"]
        record = self.records.get(sequence)
        if record is None or sequence not in self.commands.receipts:
            return self._receipt(
                request,
                error="stale_command"
                if sequence <= self.commands.command_sequence
                else "content_not_prepared",
            )
        if fingerprint != record["fingerprint"]:
            return self._receipt(request, error="request_reused")
        if record["terminal"] is not None:
            return copy.deepcopy(record["terminal"])
        if record["state"] == "committing":
            async with asyncio.timeout(COMMIT_TIMEOUT_S + 2):
                await record["commit_done"].wait()
            return copy.deepcopy(record["terminal"])
        if (
            record["state"] != "staged"
            or sequence != self.commands.command_sequence
            or self.commands.busy
        ):
            return self._receipt(request, error="content_not_prepared")
        if time.monotonic() - record["staged_at"] >= STAGED_COMMIT_TTL_S:
            record["state"] = "expired"
            record["terminal"] = self._receipt(request, error="content_expired")
            self.commands.receipts[sequence] = fingerprint, record["terminal"]
            self._schedule_cancel(hass, record)
            return copy.deepcopy(record["terminal"])
        # Consume commit eligibility before refresh/network awaits. Same owner only.
        record["state"] = "committing"
        record["commit_done"] = asyncio.Event()
        self.commands.busy = True
        possible_send = False
        receipt = self._receipt(request)
        try:
            try:
                self._fresh(hass, request, scope=record["scope"], floor=record["floor"])
            except CommandRejected as exc:
                if str(exc) not in {"stale", "unavailable", "awaiting_observation"}:
                    raise
                await self.commands.binding[1].async_refresh_after_command()
            self._fresh(hass, request, scope=record["scope"], floor=record["floor"])
            if record["state"] != "committing":
                raise CommandRejected("content_not_prepared")
            possible_send = True
            record["possible_send"] = True
            async with asyncio.timeout(COMMIT_TIMEOUT_S):
                result = await self.client_method(record["client"], "commit")(
                    request[self.offer_key]["offer_id"],
                    request[self.offer_key]["binding_id"],
                )
                queried = False
                while True:
                    if record["terminal"] is not None:
                        receipt = record["terminal"]
                        break
                    if self._owner(hass, request) != record["scope"]:
                        raise CommandRejected("owner_replaced")
                    if not self.valid_result(
                        result, request, "result" if queried else "commit"
                    ):
                        raise SpeakersError("Invalid content completion receipt")
                    if result["state"] == "complete":
                        receipt = self._receipt(request, "accepted")
                        receipt[self.operation_key] = result[self.operation_key]
                        receipt[self.volume_key] = self.expected_volume(request)
                        break
                    if result["state"] != "committing":
                        raise SpeakersError("Content outcome unknown")
                    await asyncio.sleep(STATUS_INTERVAL_S)
                    queried = True
                    result = await self.client_method(record["client"], "result")(
                        request[self.offer_key]["offer_id"],
                        request[self.offer_key]["binding_id"],
                    )
        except asyncio.CancelledError:
            receipt = self._receipt(
                request,
                "unknown" if possible_send else "rejected",
                "outcome_unknown" if possible_send else "unavailable",
            )
            raise
        except (CommandRejected, SpeakersError, TimeoutError) as exc:
            receipt = self._receipt(
                request,
                "unknown" if possible_send else "rejected",
                "outcome_unknown"
                if possible_send
                else str(exc)
                if isinstance(exc, CommandRejected)
                else "unavailable",
            )
        finally:
            if record["terminal"] is None:
                record["terminal"] = receipt
            else:
                receipt = record["terminal"]
            if possible_send:
                self.commands.poll_floor = max(
                    self.commands.poll_floor,
                    self.commands.data.coordinator.poll_sequence,
                )
            receipt.setdefault("observe_after_poll_sequence", self.commands.poll_floor)
            self.commands.receipts[sequence] = fingerprint, receipt
            record["state"] = (
                "complete"
                if receipt["outcome"] == "accepted"
                else "unknown"
                if possible_send
                else "rejected"
            )
            self.commands.busy = False
            record["commit_done"].set()
            if receipt["outcome"] != "accepted":
                self._schedule_cancel(hass, record)
            if possible_send and self.commands._refresh_owner_current(
                hass, request["config_entry_id"]
            ):
                await self.commands.binding[1].async_refresh_after_command()
        return copy.deepcopy(receipt)

    async def cancel(self, hass, args):
        from .speakers import SpeakersError

        record = self.records.get(args["command_sequence"])
        if (
            record is None
            or any(
                record["request"][key] != args[key]
                for key in (
                    "config_entry_id",
                    "owner_epoch",
                    "command_sequence",
                    "request_id",
                )
            )
            or any(
                record["request"][self.offer_key][key] != args[key]
                for key in ("offer_id", "binding_id")
            )
        ):
            return {
                "ok": False,
                "error_kind": "preparation_unavailable",
                "retry_allowed": False,
                "physical_result_verified": False,
            }
        request = record["request"]
        if record["state"] == "complete":
            return self._preparation(request, "unknown", cancel=True)
        possible = record.get("possible_send", False)
        record["state"] = "unknown" if possible else "cancelled"
        if record["terminal"] is None:
            record["terminal"] = self._receipt(
                request,
                "unknown" if possible else "rejected",
                "outcome_unknown" if possible else "cancelled",
            )
            if possible:
                record["terminal"]["observe_after_poll_sequence"] = (
                    self.commands.poll_floor
                )
            self.commands.receipts[args["command_sequence"]] = (
                record["fingerprint"],
                record["terminal"],
            )
        if record["preparation"]["state"] == "unknown" and not record["done"].is_set():
            record["preparation"] = self._preparation(request, "cancelled")
        try:
            async with asyncio.timeout(2):
                reply = await self.client_method(record["client"], "cancel")(
                    args["offer_id"], args["binding_id"]
                )
            definite = (
                self.valid_result(reply, request, "cancel")
                and reply["state"] == "cancelled"
                and not possible
            )
        except (SpeakersError, TimeoutError):
            definite = False
        return self._preparation(
            request, "cancelled" if definite else "unknown", cancel=True
        )
