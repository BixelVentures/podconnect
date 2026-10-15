"""One-use native queue offers; Spotify REST metadata never selects an occurrence."""
from __future__ import annotations

import copy
import re
import time
from datetime import datetime

from .artwork import validate_spotify_artwork
from .observations import current
from .speakers import SpeakersError

_ID = re.compile(r"^[a-f0-9]{32}$")
_OPTION = re.compile(r"^[a-f0-9]{24}$")
_TRACK = re.compile(r"^spotify:track:[A-Za-z0-9]{22}$")
_NATIVE_KEYS = {"schema", "target_id", "room_id", "account_user_id", "connection_id", "session_id", "alias_incarnation", "observation_id", "queue_revision", "expires_after_ms", "items", "truncated", "homepod_id"}
_ARG_KEYS = {"room_id", "observation_id", "session_id", "queue_revision", "occurrence_id"}


def valid_arguments(args):
    return (isinstance(args, dict) and set(args) == _ARG_KEYS
        and all(isinstance(args[k], str) and 1 <= len(args[k]) <= 160 for k in ("room_id", "session_id"))
        and isinstance(args["observation_id"], str) and _ID.fullmatch(args["observation_id"]) is not None
        and isinstance(args["occurrence_id"], str) and _OPTION.fullmatch(args["occurrence_id"]) is not None
        and type(args["queue_revision"]) is int and 0 <= args["queue_revision"] < 2**53)


def valid_snapshot(value, target, room, account):
    if not isinstance(value, dict) or set(value) != _NATIVE_KEYS:
        return False
    if (value["schema"] != "podconnect.native_queue.v1" or value["target_id"] != target
        or value["room_id"] != room or value["account_user_id"] != account
        or any(not isinstance(value[k], str) or not 1 <= len(value[k]) <= 160 for k in ("connection_id", "session_id", "alias_incarnation", "homepod_id"))
        or not isinstance(value["observation_id"], str) or _ID.fullmatch(value["observation_id"]) is None
        or type(value["queue_revision"]) is not int or not 0 <= value["queue_revision"] < 2**53
        or type(value["expires_after_ms"]) is not int or not 1 <= value["expires_after_ms"] <= 10000
        or type(value["truncated"]) is not bool or not isinstance(value["items"], list) or len(value["items"]) > 20):
        return False
    ids = set()
    for index, row in enumerate(value["items"]):
        if (not isinstance(row, dict) or set(row) != {"id", "position", "uri", "queued"}
            or not isinstance(row["id"], str) or _OPTION.fullmatch(row["id"]) is None or row["id"] in ids
            or type(row["position"]) is not int or row["position"] != index
            or not isinstance(row["uri"], str) or _TRACK.fullmatch(row["uri"]) is None or type(row["queued"]) is not bool):
            return False
        ids.add(row["id"])
    return True


class QueueControls:
    """A single exact observation in this existing Control runtime."""
    def __init__(self, commands):
        self.commands = commands
        self.offer = None
        self.read_revision = 0

    async def read(self, hass, entry, target, room):
        from .commands import CommandRejected
        commands, data = self.commands, self.commands.data
        current(hass, entry, data)
        observed = commands.controls(entry, target)
        if "next" not in observed["actions"] or data.speakers is None:
            raise CommandRejected("native_queue_unavailable")
        config = hass.config_entries.async_get_entry(entry)
        account = config.unique_id if config is not None else None
        if not isinstance(account, str) or not 1 <= len(account) <= 160:
            raise CommandRejected("native_account_unproven")
        # A new read invalidates the prior UI offer even if this read fails.
        self.offer = None
        self.read_revision += 1
        read_revision = self.read_revision
        started = time.monotonic()
        try:
            reply = await data.speakers.queue_occurrences(room, target, account)
        except SpeakersError as exc:
            raise CommandRejected("native_queue_unavailable") from exc
        current(hass, entry, data)
        latest = commands.controls(entry, target)
        if read_revision != self.read_revision or latest["owner_epoch"] != observed["owner_epoch"] or latest["poll_sequence"] != observed["poll_sequence"] or "next" not in latest["actions"]:
            raise CommandRejected("stale_observation")
        if not valid_snapshot(reply, target, room, account):
            raise CommandRejected("native_queue_unavailable")
        source = latest["observation"]
        source_until = datetime.fromisoformat(source["observed_at"]).timestamp() + source["max_age_ms"] / 1000
        remaining = min(reply["expires_after_ms"] / 1000 - (time.monotonic() - started),
            source_until - time.time(), (source["max_age_ms"] - source["age_ms"]) / 1000)
        if remaining <= 0:
            raise CommandRejected("native_queue_stale")
        metadata = {}
        cache = getattr(data.observations, "_queue_cache", None)
        if (isinstance(cache, dict) and cache.get("ok") is True and cache.get("config_entry_id") == entry
            and cache.get("owner_epoch") == observed["owner_epoch"]
            and time.monotonic() - getattr(data.observations, "_queue_cached_at", 0) < 10):
            for row in cache.get("items", []):
                if isinstance(row, dict):
                    metadata[row.get("uri")] = row
        items = []
        for row in reply["items"]:
            label = metadata.get(row["uri"], {})
            artwork = validate_spotify_artwork(label.get("artwork"))
            items.append({"option_id": row["id"], "index": row["position"], "uri": row["uri"],
                "name": label.get("name", "Nummer i køen")[:256], "artist": ", ".join(label.get("artists", []))[:512], "artwork": artwork["url"] if artwork else None})
        self.offer = {"reply": copy.deepcopy(reply), "entry": entry, "target": target,
            "account": account, "epoch": observed["owner_epoch"], "sequence": observed["poll_sequence"],
            "expires": time.monotonic() + remaining}
        return {"ok": True, "schema": "podconnect.queue_controls.v1", "config_entry_id": entry,
            "owner_epoch": observed["owner_epoch"], "poll_sequence": observed["poll_sequence"],
            "next_command_sequence": observed["next_command_sequence"], "target": observed["target"], "observation": source,
            "native": {k: reply[k] for k in ("observation_id", "session_id", "queue_revision", "room_id", "homepod_id", "alias_incarnation")} | {"expires_in_ms": int(remaining * 1000)},
            "items": items, "returned_count": len(items), "truncated": reply["truncated"],
            "actions": ["play_queue_occurrence"], "valid_until": min(time.time() + remaining, source_until), "physical_result_verified": False}

    async def commit(self, hass, entry, target, args, guard, sending):
        from .commands import CommandRejected
        offer = self.offer
        if offer is None or offer["reply"]["observation_id"] != args["observation_id"]:
            raise CommandRejected("native_queue_stale")
        self.offer = None
        reply = offer["reply"]
        current(hass, entry, self.commands.data)
        config = hass.config_entries.async_get_entry(entry)
        if (time.monotonic() >= offer["expires"] or offer["entry"] != entry or offer["target"] != target
            or config is None or config.unique_id != offer["account"]
            or any(args[k] != reply[k] for k in ("room_id", "session_id", "queue_revision"))
            or not any(row["id"] == args["occurrence_id"] for row in reply["items"])):
            raise CommandRejected("native_queue_stale")
        observed = self.commands.controls(entry, target, for_command=True)
        if observed["owner_epoch"] != offer["epoch"] or observed["poll_sequence"] != offer["sequence"]:
            raise CommandRejected("stale_observation")
        guard()
        sending()
        ack = await self.commands.data.speakers.play_queue_occurrence(target, offer["account"], args)
        if (not isinstance(ack, dict) or set(ack) != {"ok", "schema", "observation_id", "occurrence_id", "physical_result_verified"}
            or ack["ok"] is not True or ack["schema"] != "podconnect.native_queue_commit.v1"
            or ack["observation_id"] != args["observation_id"] or ack["occurrence_id"] != args["occurrence_id"]
            or ack["physical_result_verified"] is not False):
            raise SpeakersError("Native queue outcome unknown")
