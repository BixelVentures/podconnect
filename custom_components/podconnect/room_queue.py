"""Distinct staged queue purpose; shared PlaybackCommands owns irreversible sequence."""

from __future__ import annotations

import copy
import hashlib
import json
import re
import time
import uuid

from homeassistant.exceptions import HomeAssistantError

from .artwork import validate_spotify_artwork
from .observations import current
from .room_transport import valid_identity
from .speakers import SpeakersError

PURPOSE = "queue_occurrence_v1"
ACTION = "play_queue_occurrence"
_H32 = re.compile(r"[a-f0-9]{32}")
_H24 = re.compile(r"[a-f0-9]{24}")
_H64 = re.compile(r"[a-f0-9]{64}")
_DEC = re.compile(r"0|[1-9][0-9]{0,19}")
_TRACK = re.compile(r"spotify:track:[A-Za-z0-9]{22}")


def _hex(value, pattern):
    return isinstance(value, str) and pattern.fullmatch(value) is not None


def _int(value, minimum, maximum):
    return type(value) is int and minimum <= value <= maximum


def _decimal(value, minimum=0, maximum=2**64 - 1):
    return (
        isinstance(value, str)
        and _DEC.fullmatch(value) is not None
        and minimum <= int(value) <= maximum
    )


def selection(value):
    return (
        isinstance(value, dict)
        and set(value)
        == {"observation_id", "session_id", "queue_revision", "occurrence_id"}
        and _hex(value["observation_id"], _H32)
        and _hex(value["occurrence_id"], _H24)
        and isinstance(value["session_id"], str)
        and 1 <= len(value["session_id"]) <= 255
        and _int(value["queue_revision"], 0, 2**53 - 1)
    )


def identity(native, route, scope):
    if not valid_identity(native, route, scope):
        return False
    for key, value in native.items():
        if key == "alias_revision":
            continue
        limit = 160 if key == "account_user_id" else 255
        if len(value) > limit or any(
            ord(c) < 32 or ord(c) > 126 or c in '\\"' for c in value
        ):
            return False
    return (
        _hex(native["alias_incarnation"], _H32)
        and _hex(native["alias_registry"], _H64)
        and _decimal(native["homepod_id"], 1)
        and all(
            _decimal(
                route[k], 0 if k in {"route_revision", "selection_revision"} else 1
            )
            for k in set(route) - {"process", "input_path_digest"}
        )
    )


def writer(value, route):
    if not isinstance(value, dict) or set(value) != {
        "native_process",
        "writer_fd",
        "pipe_identity",
        "source_epoch",
        "open_generation",
        "input_path_digest",
    }:
        return False
    process, pipe = value["native_process"], value["pipe_identity"]
    if not isinstance(process, dict) or set(process) != {
        "boot_id",
        "pid",
        "proc_start_ticks",
    }:
        return False
    try:
        if (
            not isinstance(process["boot_id"], str)
            or str(uuid.UUID(process["boot_id"])) != process["boot_id"]
        ):
            return False
    except (ValueError, AttributeError):
        return False
    return (
        isinstance(pipe, dict)
        and set(pipe) == {"device", "inode", "type"}
        and pipe["type"] == "fifo"
        and _decimal(pipe["device"], 1)
        and _decimal(pipe["inode"], 1)
        and _decimal(process["pid"], 1, 2**31 - 1)
        and _decimal(process["proc_start_ticks"], 1)
        and _decimal(value["writer_fd"], 0, 2**31 - 1)
        and _decimal(value["source_epoch"])
        and _decimal(value["open_generation"], 1)
        and _hex(value["input_path_digest"], _H64)
        and value["input_path_digest"] == route["input_path_digest"]
    )


def context(value, target, args, *, prepared=False):
    keys = {"room_id", "homepod_id", "native", "route", "purpose"} | (
        {"prepare_id"} if prepared else set()
    )
    if (
        not isinstance(value, dict)
        or set(value) != keys
        or value["purpose"] != PURPOSE
        or not selection(args)
    ):
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
    return (
        identity(native, value["route"], scope)
        and args["session_id"] == native["session_id"]
        and (not prepared or _hex(value["prepare_id"], _H32))
    )


def _fingerprint(request):
    return hashlib.sha256(
        json.dumps(request, sort_keys=True, allow_nan=False).encode()
    ).digest()


class RoomQueue:
    def __init__(self, commands):
        self.commands = commands
        self.observations = {}
        self.preparations = {}
        self.read_revision = 0

    def _scope(self, hass, entry, target, room, homepod):
        from .commands import CommandRejected

        current(hass, entry, self.commands.data)
        config = hass.config_entries.async_get_entry(entry)
        account = config.unique_id if config is not None else None
        if (
            not self.commands._refresh_owner_current(hass, entry)
            or not isinstance(account, str)
            or not account
            or self.commands.data.speakers is None
        ):
            raise CommandRejected("room_queue_unavailable")
        return {
            "account_user_id": account,
            "target_id": target,
            "room_id": room,
            "homepod_id": homepod,
        }

    def _fresh(self, hass, request, scope):
        from .commands import CommandRejected

        if (
            self._scope(
                hass,
                request["config_entry_id"],
                request["target_id"],
                scope["room_id"],
                scope["homepod_id"],
            )
            != scope
        ):
            raise CommandRejected("owner_replaced")
        observed = self.commands.controls(
            request["config_entry_id"], request["target_id"], for_command=True
        )
        if (
            observed["owner_epoch"] != request["owner_epoch"]
            or observed["poll_sequence"] != request["expected_poll_sequence"]
            or observed["poll_sequence"] <= self.commands.poll_floor
        ):
            raise CommandRejected("stale_observation")
        return observed

    async def read(self, hass, entry, target, room, homepod):
        from .commands import CommandRejected

        scope = self._scope(hass, entry, target, room, homepod)
        before = self.commands.controls(entry, target)
        self.read_revision += 1
        revision = self.read_revision
        self.observations.clear()
        started = time.monotonic()
        value = await self.commands.data.speakers.room_queue_state(scope)
        latest = self.commands.controls(entry, target)
        if (
            self._scope(hass, entry, target, room, homepod) != scope
            or revision != self.read_revision
            or any(before[k] != latest[k] for k in ("owner_epoch", "poll_sequence"))
        ):
            raise CommandRejected("stale_observation")
        keys = {
            "schema",
            "ok",
            "purpose",
            "native",
            "route",
            "queue",
            "actions",
            "physical_result_verified",
        }
        if (
            not isinstance(value, dict)
            or set(value) != keys
            or value["schema"] != "podconnect.room_queue_state.v1"
            or value["ok"] is not True
            or value["purpose"] != PURPOSE
            or value["physical_result_verified"] is not False
            or value["actions"] not in ([], [ACTION])
            or not identity(value["native"], value["route"], scope)
        ):
            raise CommandRejected("room_queue_unavailable")
        queue = value["queue"]
        if (
            not isinstance(queue, dict)
            or set(queue)
            != {
                "observation_id",
                "queue_revision",
                "expires_after_ms",
                "items",
                "truncated",
            }
            or not _hex(queue["observation_id"], _H32)
            or not _int(queue["queue_revision"], 0, 2**53 - 1)
            or not _int(queue["expires_after_ms"], 1, 10000)
            or type(queue["truncated"]) is not bool
            or not isinstance(queue["items"], list)
            or len(queue["items"]) > 20
        ):
            raise CommandRejected("room_queue_unavailable")
        seen = set()
        for index, row in enumerate(queue["items"]):
            if (
                not isinstance(row, dict)
                or set(row) != {"id", "position", "uri", "queued"}
                or not _hex(row["id"], _H24)
                or row["id"] in seen
                or type(row["position"]) is not int
                or row["position"] != index
                or not _hex(row["uri"], _TRACK)
                or type(row["queued"]) is not bool
            ):
                raise CommandRejected("room_queue_unavailable")
            seen.add(row["id"])
        from datetime import datetime

        stamp = datetime.fromisoformat(latest["observation"]["observed_at"]).timestamp()
        observation = latest["observation"]
        remaining = min(
            queue["expires_after_ms"] / 1000 - (time.monotonic() - started),
            stamp + observation["max_age_ms"] / 1000 - time.time(),
            (observation["max_age_ms"] - observation["age_ms"]) / 1000,
        )
        if remaining <= 0:
            raise CommandRejected("room_queue_expired")
        self.observations[queue["observation_id"]] = {
            "value": copy.deepcopy(value),
            "scope": scope,
            "entry": entry,
            "epoch": latest["owner_epoch"],
            "poll": latest["poll_sequence"],
            "deadline": time.monotonic() + remaining,
        }
        metadata = {}
        cache = getattr(self.commands.data.observations, "_queue_cache", None)
        cached_at = getattr(self.commands.data.observations, "_queue_cached_at", 0)
        if (
            isinstance(cache, dict)
            and cache.get("ok") is True
            and cache.get("config_entry_id") == entry
            and cache.get("owner_epoch") == latest["owner_epoch"]
            and 0 <= time.monotonic() - cached_at < 10
        ):
            for row in cache.get("items", []):
                if isinstance(row, dict) and isinstance(row.get("uri"), str):
                    metadata[row["uri"]] = row
        items = []
        for row in queue["items"]:
            label = metadata.get(row["uri"], {})
            name = label.get("name")
            name = (
                name
                if isinstance(name, str) and 1 <= len(name) <= 256
                else "Nummer i køen"
            )
            artists = label.get("artists", [])
            artist = (
                ", ".join(artists)[:512]
                if isinstance(artists, list)
                and all(isinstance(v, str) for v in artists)
                else ""
            )
            artwork = validate_spotify_artwork(label.get("artwork"))
            items.append(
                {
                    "option_id": row["id"],
                    "index": row["position"],
                    "uri": row["uri"],
                    "name": name,
                    "artist": artist,
                    "artwork": artwork["url"] if artwork else None,
                }
            )
        return {
            **{
                key: latest[key]
                for key in (
                    "ok",
                    "config_entry_id",
                    "owner_epoch",
                    "poll_sequence",
                    "next_command_sequence",
                    "target",
                    "observation",
                )
            },
            "schema": "podconnect.room_queue_controls.v1",
            "purpose": PURPOSE,
            "native": copy.deepcopy(value["native"]),
            "route": copy.deepcopy(value["route"]),
            "queue": {
                "observation_id": queue["observation_id"],
                "session_id": value["native"]["session_id"],
                "queue_revision": queue["queue_revision"],
                "expires_in_ms": int(remaining * 1000),
            },
            "items": items,
            "returned_count": len(queue["items"]),
            "truncated": queue["truncated"],
            "actions": value["actions"] if latest["status"] == "ready" else [],
            "valid_until": time.time() + remaining,
            "physical_result_verified": False,
        }

    async def prepare(self, hass, request):
        from .commands import CommandRejected

        rejected = {
            "ok": False,
            "error_kind": "room_queue_unavailable",
            "retry_allowed": False,
            "physical_result_verified": False,
        }
        commands = self.commands
        self.preparations = {
            key: value
            for key, value in self.preparations.items()
            if time.monotonic() < value["deadline"]
        }
        active = False
        try:
            ctx = {
                k: request[k]
                for k in ("room_id", "homepod_id", "native", "route", "purpose")
            }
            if request["action"] != ACTION or not context(
                ctx, request["target_id"], request["args"]
            ):
                return rejected
            sequence = request["command_sequence"]
            if (
                type(sequence) is not int
                or not 1 <= sequence < 2**53
                or sequence != commands.command_sequence + 1
                or request["request_id"] != f"{request['owner_epoch']}:{sequence}"
                or commands.busy
                or len(self.preparations) >= 64
            ):
                return rejected
            record = self.observations.get(request["args"]["observation_id"])
            if record is None or time.monotonic() >= record["deadline"]:
                return rejected
            value, scope = record["value"], record["scope"]
            if (
                record["entry"] != request["config_entry_id"]
                or record["epoch"] != request["owner_epoch"]
                or record["poll"] != request["expected_poll_sequence"]
                or value["native"] != ctx["native"]
                or value["route"] != ctx["route"]
                or ACTION not in value["actions"]
                or request["args"]["queue_revision"] != value["queue"]["queue_revision"]
                or not any(
                    row["id"] == request["args"]["occurrence_id"]
                    for row in value["queue"]["items"]
                )
            ):
                return rejected
            self._fresh(hass, request, scope)
            self.observations.pop(request["args"]["observation_id"])
            commands.busy = active = True
            started = time.monotonic()
            staged = await commands.data.speakers.prepare_room_queue(
                scope, ctx["native"], ctx["route"], request["args"]
            )
            self._fresh(hass, request, scope)
            keys = {
                "schema",
                "ok",
                "purpose",
                "prepare_id",
                "native",
                "route",
                "queue",
                "writer_proof",
                "expires_after_ms",
                "physical_result_verified",
            }
            if (
                not isinstance(staged, dict)
                or set(staged) != keys
                or staged["schema"] != "podconnect.room_queue_prepare.v1"
                or staged["ok"] is not True
                or staged["purpose"] != PURPOSE
                or staged["physical_result_verified"] is not False
                or not _hex(staged["prepare_id"], _H32)
                or staged["native"] != ctx["native"]
                or staged["route"] != ctx["route"]
                or staged["queue"] != request["args"]
                or not writer(staged["writer_proof"], staged["route"])
                or not _int(staged["expires_after_ms"], 1, 5000)
            ):
                return rejected
            deadline = min(
                record["deadline"], started + staged["expires_after_ms"] / 1000
            )
            if (
                time.monotonic() >= deadline
                or sequence != commands.command_sequence + 1
            ):
                return rejected
            remaining_ms = int((deadline - time.monotonic()) * 1000)
            if remaining_ms < 1:
                return rejected
            self.preparations[staged["prepare_id"]] = {
                "request": copy.deepcopy(request),
                "scope": scope,
                "deadline": deadline,
                "fingerprint": _fingerprint(request),
                "proof": copy.deepcopy(staged),
            }
            return {
                "ok": True,
                "schema": "podconnect.room_queue_preparation.v1",
                "purpose": PURPOSE,
                "config_entry_id": request["config_entry_id"],
                "owner_epoch": request["owner_epoch"],
                "request_id": request["request_id"],
                "command_sequence": sequence,
                "prepare_id": staged["prepare_id"],
                "expires_after_ms": remaining_ms,
                "native": staged["native"],
                "route": staged["route"],
                "queue": staged["queue"],
                "writer_proof": staged["writer_proof"],
                "physical_result_verified": False,
            }
        except (
            KeyError,
            ValueError,
            TypeError,
            SpeakersError,
            CommandRejected,
            HomeAssistantError,
        ):
            return rejected
        finally:
            if active:
                commands.busy = False

    async def commit(self, hass, request, ctx, guard, sending):
        from .commands import CommandRejected

        record = self.preparations.pop(ctx["prepare_id"], None)
        original = {**request, **{k: v for k, v in ctx.items() if k != "prepare_id"}}
        if (
            record is None
            or time.monotonic() >= record["deadline"]
            or _fingerprint(original) != record["fingerprint"]
        ):
            raise CommandRejected("room_queue_expired")
        self._fresh(hass, request, record["scope"])
        guard()
        sending()
        ack = await self.commands.data.speakers.commit_room_queue(ctx["prepare_id"])
        keys = {
            "schema",
            "ok",
            "prepare_id",
            "outcome",
            "accepted_native",
            "retry_allowed",
            "physical_result_verified",
        }
        if (
            not isinstance(ack, dict)
            or set(ack) != keys
            or ack["schema"] != "podconnect.room_queue_commit.v1"
            or ack["prepare_id"] != ctx["prepare_id"]
            or ack["ok"] is not True
            or ack["outcome"] != "accepted"
            or ack["accepted_native"] is not True
            or ack["retry_allowed"] is not False
            or ack["physical_result_verified"] is not False
        ):
            raise SpeakersError("Room queue outcome unknown")
        guard()

    def retire_before(self, hass, sequence):
        self.preparations = {
            key: value
            for key, value in self.preparations.items()
            if value["request"]["command_sequence"] >= sequence
            and time.monotonic() < value["deadline"]
        }
