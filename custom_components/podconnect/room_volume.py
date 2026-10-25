"""Separate exact-room volume authority; no generic or attention fallback."""

from __future__ import annotations

import asyncio
import copy
import re
import time

from .room_transport import valid_identity
from .speakers import SpeakersError

_TOKEN = re.compile(r"[a-f0-9]{32}")
_DECIMAL = re.compile(r"0|[1-9][0-9]{0,19}")
_STATES = {
    "prepared",
    "pending_claim",
    "reserved",
    "pending",
    "admitted_pending",
    "protocol_accepted",
    "rejected_before_admission",
    "expired_before_admission",
    "cancelled_before_admission",
    "unknown",
}
_RESULT_KEYS = {
    "schema",
    "ok",
    "prepare_id",
    "state",
    "reservation_accepted",
    "protocol_accepted",
    "possible_effect",
    "terminal",
    "deadline_retired",
    "contexts_drained",
    "retry_allowed",
    "physical_result_verified",
}


def valid_volume_state(value):
    return (
        isinstance(value, dict)
        and set(value)
        == {"base_revision", "attention_revision", "base_percent", "effective_percent"}
        and all(
            isinstance(value[k], str)
            and _DECIMAL.fullmatch(value[k]) is not None
            and int(value[k]) < 2**64
            for k in ("base_revision", "attention_revision")
        )
        and all(
            type(value[k]) is int and 0 <= value[k] <= 100
            for k in ("base_percent", "effective_percent")
        )
    )


def valid_context(value, target):
    if not isinstance(value, dict) or set(value) != {
        "room_id",
        "homepod_id",
        "native",
        "route",
        "volume_state",
    }:
        return False
    native = value["native"]
    scope = {
        "account_user_id": native.get("account_user_id")
        if isinstance(native, dict)
        else None,
        "target_id": target,
        "room_id": value["room_id"],
        "homepod_id": value["homepod_id"],
    }
    return valid_identity(native, value["route"], scope) and valid_volume_state(
        value["volume_state"]
    )


def valid_result(value, prepare_id, suffix="commit"):
    if (
        not isinstance(value, dict)
        or set(value) != _RESULT_KEYS
        or value["schema"] != "podconnect.room_volume_" + suffix + ".v1"
        or value["prepare_id"] != prepare_id
        or not isinstance(value["state"], str)
        or value["state"] not in _STATES
        or any(
            type(value[k]) is not bool
            for k in _RESULT_KEYS - {"schema", "prepare_id", "state"}
        )
        or value["retry_allowed"] is not False
        or value["physical_result_verified"] is not False
    ):
        return False
    if value["protocol_accepted"] != (value["state"] == "protocol_accepted"):
        return False
    if value["protocol_accepted"] and not (
        value["ok"] and value["reservation_accepted"] and value["possible_effect"]
    ):
        return False
    # A drained terminal certificate is distinct from a response timeout.
    if value["terminal"] and not value["contexts_drained"]:
        return False
    return not (
        not value["possible_effect"]
        and value["state"] in {"admitted_pending", "protocol_accepted"}
    )


class RoomVolume:
    def __init__(self, commands):
        self.commands = commands
        self.cleanups = set()

    def _scope(self, hass, entry, target, context):
        return self.commands.room_transport.scope(
            hass, entry, target, context["room_id"], context["homepod_id"]
        )

    async def read(self, hass, entry, target, room, homepod, *, passive=False):
        from .commands import CommandRejected

        context = {"room_id": room, "homepod_id": homepod}
        scope = self._scope(hass, entry, target, context)
        before = self.commands.controls(entry, target)
        try:
            state = await self.commands.data.speakers.room_volume_state(scope)
        except SpeakersError as exc:
            raise CommandRejected("room_volume_unavailable") from exc
        if self._scope(hass, entry, target, context) != scope:
            raise CommandRejected("native_account_unproven")
        latest = self.commands.controls(entry, target)
        if any(before[k] != latest[k] for k in ("owner_epoch", "poll_sequence")):
            raise CommandRejected("stale_observation")
        if (
            not isinstance(state, dict)
            or set(state)
            != {
                "schema",
                "ok",
                "native",
                "route",
                "volume_state",
                "actions",
                "physical_result_verified",
            }
            or state["schema"] != "podconnect.room_volume_state.v1"
            or state["ok"] is not True
            or state["physical_result_verified"] is not False
            or state["actions"] not in (["volume"], [])
            or not valid_identity(state["native"], state["route"], scope)
            or not valid_volume_state(state["volume_state"])
        ):
            raise CommandRejected("room_volume_unavailable")
        return {
            **latest,
            "schema": "podconnect.room_volume_controls.v1",
            "room_id": room,
            "homepod_id": homepod,
            "native": copy.deepcopy(state["native"]),
            "route": copy.deepcopy(state["route"]),
            "volume_state": copy.deepcopy(state["volume_state"]),
            "actions": state["actions"]
            if passive or latest["status"] == "ready"
            else [],
            "volume_percent": None,
            "track_play_supported": False,
            "physical_result_verified": False,
        }

    def _cancel(self, hass, client, prepare_id):
        async def cleanup():
            try:
                async with asyncio.timeout(1):
                    await client.cancel_room_volume(prepare_id)
            except (SpeakersError, TimeoutError):
                return  # No retry, route unlock, or receipt upgrade.

        task = hass.async_create_background_task(
            cleanup(), "podconnect room volume cancel", eager_start=False
        )
        self.cleanups.add(task)
        task.add_done_callback(self.cleanups.discard)

    async def commit(self, hass, entry, target, context, percent, guard, sending):
        from .commands import CommandRejected

        if len(self.cleanups) >= 8:
            raise CommandRejected("room_volume_cleanup_busy")
        scope = self._scope(hass, entry, target, context)
        if not valid_identity(context["native"], context["route"], scope):
            raise CommandRejected("invalid_room_identity")
        client = self.commands.data.speakers
        prepare_id = None
        try:
            guard()
            started = time.monotonic()
            try:
                prepared = await client.prepare_room_volume(scope, context, percent)
            except SpeakersError as exc:
                raise CommandRejected("room_volume_unavailable") from exc
            # Capture only a well-formed opaque ID for bounded cancellation, even
            # when another reply field is invalid. It never grants commit authority.
            if (
                isinstance(prepared, dict)
                and isinstance(prepared.get("prepare_id"), str)
                and _TOKEN.fullmatch(prepared["prepare_id"])
            ):
                prepare_id = prepared["prepare_id"]
            if (
                not isinstance(prepared, dict)
                or set(prepared)
                != {
                    "schema",
                    "ok",
                    "prepare_id",
                    "expires_after_ms",
                    "native",
                    "route",
                    "volume_state",
                    "action",
                    "volume_percent",
                    "physical_result_verified",
                }
                or prepared["schema"] != "podconnect.room_volume_prepare.v1"
                or prepared["ok"] is not True
                or prepare_id is None
                or type(prepared["expires_after_ms"]) is not int
                or not 1 <= prepared["expires_after_ms"] <= 5000
                or not valid_identity(prepared["native"], prepared["route"], scope)
                or not valid_volume_state(prepared["volume_state"])
                or prepared["native"] != context["native"]
                or prepared["route"] != context["route"]
                or prepared["volume_state"] != context["volume_state"]
                or prepared["action"] != "volume"
                or type(prepared["volume_percent"]) is not int
                or prepared["volume_percent"] != percent
                or prepared["physical_result_verified"] is not False
            ):
                raise CommandRejected("room_volume_unavailable")
            guard()
            if self._scope(hass, entry, target, context) != scope:
                raise CommandRejected("native_account_unproven")
            if time.monotonic() - started >= prepared["expires_after_ms"] / 1000:
                raise CommandRejected("room_volume_expired")
            sending()
            ack = await client.commit_room_volume(prepare_id)
            if not valid_result(ack, prepare_id):
                raise SpeakersError("Room volume outcome unknown")
            if not ack["protocol_accepted"]:
                self._cancel(hass, client, prepare_id)
            return ack
        except BaseException:
            if prepare_id is not None:
                self._cancel(hass, client, prepare_id)
            raise
