"""Distinct staged queue purpose; shared PlaybackCommands owns irreversible sequence."""

from __future__ import annotations

import asyncio
import copy
import hashlib
import json
import logging
import re
import time
import uuid

from homeassistant.exceptions import HomeAssistantError

from .artwork import validate_spotify_artwork
from .observations import QUEUE_CACHE_S, current
from .room_transport import valid_identity
from .speakers import SpeakersError

PURPOSE = "queue_occurrence_v1"
ACTION = "play_queue_occurrence"
_H32 = re.compile(r"[a-f0-9]{32}")
_H24 = re.compile(r"[a-f0-9]{24}")
_DISPLAY_ID = re.compile(r"display-[a-f0-9]{24}")
_H64 = re.compile(r"[a-f0-9]{64}")
_DEC = re.compile(r"0|[1-9][0-9]{0,19}")
_TRACK = re.compile(r"spotify:track:[A-Za-z0-9]{22}")
_LOGGER = logging.getLogger(__name__)


# Finite CommandRejected kinds that may appear in a diagnostic log line.
_REJECT_KINDS = frozenset(
    {
        "owner_replaced",
        "stale_observation",
        "room_queue_unavailable",
        "room_queue_expired",
        "target_not_current",
        "invalid_target",
        "capabilities_unavailable",
        "unavailable",
    }
)


def _first(*checks):
    """Name the first failing check, so a rejection is never a guess (#140)."""
    for failed, reason in checks:
        if failed:
            return reason
    return None


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
        self._queue_read_log_after = {}
        self._metadata_task = None

    def _warm_queue_metadata(self, hass, entry_id):
        if self._metadata_task is not None and not self._metadata_task.done():
            return
        entry = hass.config_entries.async_get_entry(entry_id)
        if entry is None:
            return
        data = self.commands.data
        reader = data.observations
        if (
            reader._queue_cache is not None
            and time.monotonic() - reader._queue_cached_at < reader._queue_cache_ttl
        ):
            return

        def verify():
            current(hass, entry_id, data)
            if data.observations is not reader:
                raise HomeAssistantError("PodConnect account observations replaced")

        async def warm():
            try:
                verify()
                await reader.queue(entry_id, 20, verify)
            except HomeAssistantError:
                return

        self._metadata_task = entry.async_create_background_task(
            hass, warm(), "podconnect native queue labels", eager_start=False
        )

    def _trace_read_failure(self, reason, observed=None):
        try:
            now = time.monotonic()
            if now < self._queue_read_log_after.get(reason, 0):
                return
            self._queue_read_log_after[reason] = now + 10
            poll_ref = "none"
            if observed is not None:
                epoch, sequence = observed["owner_epoch"], observed["poll_sequence"]
                if (
                    isinstance(epoch, str)
                    and 1 <= len(epoch) <= 160
                    and type(sequence) is int
                ):
                    poll_ref = hashlib.sha256(
                        f"{epoch}:{sequence}".encode()
                    ).hexdigest()[:16]
            _LOGGER.warning(
                "room_queue_read event=failed reason=%s poll_ref=%s", reason, poll_ref
            )
        except (Exception, asyncio.CancelledError):  # noqa: BLE001
            return  # Diagnostic failures must preserve the original rejection.

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
        # The selected occurrence is fenced by Speakers (observation, session,
        # queue revision, route and writer) at prepare and commit. A routine
        # Spotify Web API poll landing meanwhile is not a new owner and must
        # not reject the user's explicit queue choice (issue #140).
        if observed["owner_epoch"] != request["owner_epoch"]:
            raise CommandRejected("stale_observation")
        return observed

    async def read(self, hass, entry, target, room, homepod, *, autoplay_preview=False):
        from .commands import CommandRejected

        observed = None
        reason = "scope_unavailable"
        try:
            scope = self._scope(hass, entry, target, room, homepod)
            reason = "playback_before_unavailable"
            before = self.commands.controls(entry, target)
            observed = before
            # Several screens read the same room's queue. A newer read never
            # invalidates an older one; Speakers fences each occurrence at
            # prepare and commit, so only expired observations are dropped.
            now = time.monotonic()
            for key in [k for k, v in self.observations.items() if v["deadline"] <= now]:
                del self.observations[key]
            started = time.monotonic()
            reason = "speakers_unavailable"
            value = await self.commands.data.speakers.room_queue_state(
                scope, autoplay_preview=autoplay_preview
            )
            reason = "playback_after_unavailable"
            latest = self.commands.controls(entry, target)
            observed = latest
            reason = "scope_changed"
            if self._scope(hass, entry, target, room, homepod) != scope:
                raise CommandRejected("stale_observation")
            reason = "owner_epoch_changed"
            if before["owner_epoch"] != latest["owner_epoch"]:
                raise CommandRejected("stale_observation")
            # A routine Spotify poll landing meanwhile is not a new owner
            # (AGENTS.md); the queue read stays valid.
        except (CommandRejected, SpeakersError):
            self._trace_read_failure(reason, observed)
            raise
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
            or (
                set(value) != keys
                and (not autoplay_preview or set(value) != keys | {"autoplay"})
            )
            or value["schema"] != "podconnect.room_queue_state.v1"
            or value["ok"] is not True
            or value["purpose"] != PURPOSE
            or value["physical_result_verified"] is not False
            or value["actions"] not in ([], [ACTION])
        ):
            self._trace_read_failure("native_response_invalid", latest)
            raise CommandRejected("room_queue_unavailable")
        if not identity(value["native"], value["route"], scope):
            self._trace_read_failure("native_route_identity_invalid", latest)
            raise CommandRejected("room_queue_unavailable")
        queue = value["queue"]
        if (
            not isinstance(queue, dict)
            or set(queue) not in (
                {"observation_id", "queue_revision", "expires_after_ms", "items", "truncated"},
                {"observation_id", "queue_revision", "expires_after_ms", "items", "truncated", "metadata"},
            )
            or not _hex(queue["observation_id"], _H32)
            or not _int(queue["queue_revision"], 0, 2**53 - 1)
            or not _int(queue["expires_after_ms"], 1, 10000)
            or type(queue["truncated"]) is not bool
            or not isinstance(queue["items"], list)
            or len(queue["items"]) > 20
        ):
            self._trace_read_failure("queue_payload_invalid", latest)
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
                self._trace_read_failure("queue_rows_invalid", latest)
                raise CommandRejected("room_queue_unavailable")
            seen.add(row["id"])
        autoplay = None
        if "autoplay" in value:
            autoplay = value["autoplay"]
            if autoplay == {"kind": "pending"}:
                if seen or queue["truncated"] or value["actions"]:
                    self._trace_read_failure("queue_payload_invalid", latest)
                    raise CommandRejected("room_queue_unavailable")
            elif (
                not isinstance(autoplay, dict)
                or set(autoplay) != {"kind", "display_ids"}
                or autoplay["kind"] not in ("preview", "promotion")
                or not isinstance(display_ids := autoplay["display_ids"], dict)
                or not display_ids
                or not display_ids.keys() <= seen
                or (autoplay["kind"] == "preview" and display_ids.keys() != seen)
                or not all(_hex(display_id, _DISPLAY_ID) for display_id in display_ids.values())
                or len(set(display_ids.values())) != len(display_ids)
            ):
                self._trace_read_failure("queue_payload_invalid", latest)
                raise CommandRejected("room_queue_unavailable")
        from datetime import datetime

        stamp = datetime.fromisoformat(latest["observation"]["observed_at"]).timestamp()
        observation = latest["observation"]
        wall_now, monotonic_now = time.time(), time.monotonic()
        remaining = min(
            queue["expires_after_ms"] / 1000 - (monotonic_now - started),
            stamp + observation["max_age_ms"] / 1000 - wall_now,
            (observation["max_age_ms"] - observation["age_ms"]) / 1000,
        )
        if remaining <= 0:
            self._trace_read_failure("queue_expired", latest)
            raise CommandRejected("room_queue_expired")
        if autoplay != {"kind": "pending"}:
            self.observations[queue["observation_id"]] = {
                "value": copy.deepcopy(value),
                "scope": scope,
                "entry": entry,
                "epoch": latest["owner_epoch"],
                "poll": latest["poll_sequence"],
                "deadline": monotonic_now + remaining,
            }
        metadata = {}
        cache = getattr(self.commands.data.observations, "_queue_cache", None)
        cached_at = getattr(self.commands.data.observations, "_queue_cached_at", 0)
        cache_current = (
            isinstance(cache, dict)
            and cache.get("ok") is True
            and cache.get("config_entry_id") == entry
            and cache.get("owner_epoch") == latest["owner_epoch"]
            and 0 <= time.monotonic() - cached_at < QUEUE_CACHE_S
        )
        if cache_current:
            for row in cache.get("items", []):
                if isinstance(row, dict) and isinstance(row.get("uri"), str):
                    metadata[row["uri"]] = row
        native_metadata = queue.get("metadata")
        if isinstance(native_metadata, dict):
            native_uris = {row["uri"] for row in queue["items"]}
            for uri, label in native_metadata.items():
                if uri not in native_uris or not isinstance(label, dict):
                    continue
                name, artist = label.get("name"), label.get("artist")
                if (
                    not isinstance(name, str)
                    or not 1 <= len(name) <= 256
                    or not isinstance(artist, str)
                    or not 1 <= len(artist) <= 512
                ):
                    continue
                image = label.get("artwork")
                artwork = (
                    {"kind": "album_cover", "provider": "spotify", "url": image}
                    if isinstance(image, str)
                    else None
                )
                metadata[uri] = {"name": name, "artists": [artist], "artwork": artwork}
        items = []
        for row in queue["items"]:
            label = metadata.get(row["uri"], {})
            name = label.get("name")
            metadata_known = isinstance(name, str) and 1 <= len(name) <= 256 and bool(name.strip())
            name = name if metadata_known else "Nummer i køen"
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
                    "metadata_known": metadata_known,
                    "artist": artist,
                    "artwork": artwork["url"] if artwork else None,
                }
            )
        # Account metadata can label an existing Native occurrence, but it
        # cannot supply order, membership, authority, or an action deadline.
        if (
            (autoplay is None or autoplay["kind"] != "preview")
            and not cache_current
            and any(not row["metadata_known"] for row in items)
        ):
            self._warm_queue_metadata(hass, entry)
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
            **({"autoplay": copy.deepcopy(autoplay)} if autoplay is not None else {}),
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
            "valid_until": wall_now + remaining,
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
        stage = "request"

        def reject(reason):
            _LOGGER.warning(
                "room_queue_prepare event=rejected reason=%s "
                "physical_result_verified=false",
                reason,
            )
            return rejected

        try:
            ctx = {
                k: request[k]
                for k in ("room_id", "homepod_id", "native", "route", "purpose")
            }
            if request["action"] != ACTION or not context(
                ctx, request["target_id"], request["args"]
            ):
                return reject("request_invalid")
            sequence = request["command_sequence"]
            reason = _first(
                (
                    type(sequence) is not int or not 1 <= sequence < 2**53,
                    "sequence_invalid",
                ),
                (sequence != commands.command_sequence + 1, "sequence_not_next"),
                (
                    request["request_id"] != f"{request['owner_epoch']}:{sequence}",
                    "request_id_invalid",
                ),
                (commands.busy, "control_busy"),
                (len(self.preparations) >= 64, "preparations_full"),
            )
            if reason:
                return reject(reason)
            record = self.observations.get(request["args"]["observation_id"])
            if record is None:
                return reject("observation_unknown")
            if time.monotonic() >= record["deadline"]:
                return reject("observation_expired")
            value, scope = record["value"], record["scope"]
            reason = _first(
                (record["entry"] != request["config_entry_id"], "entry_changed"),
                (record["epoch"] != request["owner_epoch"], "epoch_changed"),
                (record["poll"] != request["expected_poll_sequence"], "poll_mismatch"),
                (value["native"] != ctx["native"], "native_mismatch"),
                (value["route"] != ctx["route"], "route_mismatch"),
                (ACTION not in value["actions"], "action_unavailable"),
                (
                    request["args"]["queue_revision"]
                    != value["queue"]["queue_revision"],
                    "revision_mismatch",
                ),
                (
                    not any(
                        row["id"] == request["args"]["occurrence_id"]
                        for row in value["queue"]["items"]
                    ),
                    "occurrence_missing",
                ),
            )
            if reason:
                return reject(reason)
            stage = "fresh_before_speakers"
            self._fresh(hass, request, scope)
            self.observations.pop(request["args"]["observation_id"])
            commands.busy = active = True
            started = time.monotonic()
            stage = "speakers_prepare"
            staged = await commands.data.speakers.prepare_room_queue(
                scope, ctx["native"], ctx["route"], request["args"]
            )
            stage = "fresh_after_speakers"
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
                return reject("speakers_response_invalid")
            deadline = min(
                record["deadline"], started + staged["expires_after_ms"] / 1000
            )
            if (
                time.monotonic() >= deadline
                or sequence != commands.command_sequence + 1
            ):
                return reject("expired_after_speakers")
            remaining_ms = int((deadline - time.monotonic()) * 1000)
            if remaining_ms < 1:
                return reject("expired_after_speakers")
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
        ) as err:
            detail = (
                str(err)
                if isinstance(err, CommandRejected) and str(err) in _REJECT_KINDS
                else type(err).__name__
            )
            return reject(f"{stage}:{detail}")
        finally:
            if active:
                commands.busy = False

    async def commit(self, hass, request, ctx, guard, sending):
        from .commands import CommandRejected

        record = self.preparations.pop(ctx["prepare_id"], None)
        original = {**request, **{k: v for k, v in ctx.items() if k != "prepare_id"}}
        reason = _first(
            (record is None, "preparation_unknown"),
            (
                record is not None and time.monotonic() >= record["deadline"],
                "preparation_expired",
            ),
            (
                record is not None
                and _fingerprint(original) != record["fingerprint"],
                "request_changed",
            ),
        )
        if reason:
            _LOGGER.warning(
                "room_queue_commit event=rejected reason=%s "
                "physical_result_verified=false",
                reason,
            )
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
