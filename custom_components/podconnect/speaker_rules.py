"""The family's speaker rules, enforced on every PodConnect write path.

Until now PodVoice applied these rules before calling PodConnect (per-room music
permissions, a per-room volume ceiling and the "another Spotify account plays here"
guard). Anything else that called PodConnect's entities and services directly (the
PodView HA panel, automations, other dashboards, Assist) bypassed them. PodConnect
now owns them, so they hold no matter who calls.

Stored on the config entry's options under ``speaker_rules``::

    {
        "rooms": {"<room_id>": {"actions": ["pause", ...], "max_volume": 40}},
        "other_account_guard": true,
        "unlisted_rooms": "allow"   # or "deny"
    }

No ``speaker_rules`` option means no rules: every caller behaves as before.

The rule vocabulary and decisions are PodVoice's (``podview_controls.policy`` and
``podview_room_player``), so a room behaves the same through PodConnect as through
PodView today:

* an action is allowed only when the room's rule lists it;
* a volume above the room's ``max_volume`` is lowered to it (PodVoice clamps:
  ``min(value, maxVolume)``); a start never asks for more than the ceiling either;
* pause/resume/next/previous/seek/volume are refused while another Spotify account
  plays in the room; starting your own music there stays allowed.

This module has no Home Assistant imports so the decisions can be tested on their
own; ``enforce`` raises Home Assistant's ``ServiceValidationError`` lazily.
"""

from __future__ import annotations

import hashlib
import hmac
import json
import re
from typing import Any

OPTION = "speaker_rules"

# PodVoice's music action vocabulary (podview_controls._MUSIC).
PLAYER_ACTIONS = ("pause", "resume", "next", "previous", "seek", "volume")
RULE_ACTIONS = frozenset(
    {
        *PLAYER_ACTIONS,
        "play_track",
        "play_context",
        "play_queue_occurrence",
        "move_existing_session",
    }
)
# Actions that steer what is playing; refused while another account plays.
GUARDED_ACTIONS = frozenset(PLAYER_ACTIONS)
MOVE = "move_existing_session"
# PodConnect's own action names that are the same thing under PodVoice's name.
_ALIASES = {
    "play_episode": "play_track",
    "play_tracks": "play_track",
    "play": "resume",
    "volume_set": "volume",
}
# Not part of PodVoice's rules today; never restricted here.
UNGOVERNED = frozenset({"shuffle", "repeat"})

DENIED = "room_action_not_allowed"
OTHER_ACCOUNT = "other_account_playing"
ROOM_UNKNOWN = "room_unknown"

_MAX_ROOMS = 64


class RulesInvalid(ValueError):
    """The rules (or the PodVoice policy they come from) are malformed."""


def _room_key(value: Any) -> bool:
    return isinstance(value, str) and 1 <= len(value) <= 128


def normalize(value: Any) -> dict | None:
    """A closed, validated copy of stored rules; None for no rules."""
    if value is None:
        return None
    if not isinstance(value, dict) or not set(value) <= {
        "rooms",
        "other_account_guard",
        "unlisted_rooms",
    }:
        raise RulesInvalid("unknown_keys")
    rooms = value.get("rooms", {})
    guard = value.get("other_account_guard", True)
    unlisted = value.get("unlisted_rooms", "allow")
    if not isinstance(rooms, dict) or len(rooms) > _MAX_ROOMS:
        raise RulesInvalid("rooms")
    if type(guard) is not bool:
        raise RulesInvalid("other_account_guard")
    if unlisted not in ("allow", "deny"):
        raise RulesInvalid("unlisted_rooms")
    clean = {}
    for room, rule in rooms.items():
        if not _room_key(room):
            raise RulesInvalid("room_id")
        if not isinstance(rule, dict) or set(rule) != {"actions", "max_volume"}:
            raise RulesInvalid("room_rule")
        actions, limit = rule["actions"], rule["max_volume"]
        if (
            not isinstance(actions, list)
            or any(not isinstance(a, str) or a not in RULE_ACTIONS for a in actions)
            or len(set(actions)) != len(actions)
        ):
            raise RulesInvalid("actions")
        if type(limit) is not int or not 0 <= limit <= 100:
            raise RulesInvalid("max_volume")
        clean[room] = {
            "actions": sorted(actions),
            "max_volume": limit,
        }
    return {
        "rooms": clean,
        "other_account_guard": guard,
        "unlisted_rooms": unlisted,
    }


def from_podview_policy(text: str, entry_id: str) -> tuple[dict, int]:
    """Rules for one PodConnect account from PodVoice's ``podview_control_policy``.

    Uses PodVoice's own acceptance rules for the music part (closed row shapes,
    integer ``maxVolume`` 0..100, known actions, and "pause or resume also grants
    seek" on native rooms). Only rows for this account (``accountId`` is the
    config entry id) that name a PodConnect room (``nativeRoomId``) become room
    rules; the count of other rows is returned so a caller can say they were left out.
    """
    if not isinstance(text, str) or len(text) > 8192:
        raise RulesInvalid("policy")
    try:
        data = json.loads(text)
    except ValueError as err:
        raise RulesInvalid("policy_json") from err
    if not isinstance(data, dict) or not isinstance(data.get("music"), list):
        raise RulesInvalid("policy")
    rows = data["music"]
    if len(rows) > 8:
        raise RulesInvalid("policy")
    required = {"accountId", "targetId", "actions", "maxVolume"}
    native = {"nativeRoomId", "nativeHomepodId"}
    shapes = (required, required | native, required | native | {"default"})
    rooms: dict[str, dict] = {}
    ignored = 0
    for row in rows:
        if not isinstance(row, dict) or set(row) not in shapes:
            raise RulesInvalid("policy_row")
        if any(
            not isinstance(row[k], str) or not 1 <= len(row[k]) <= 1024
            for k in ("accountId", "targetId")
        ):
            raise RulesInvalid("policy_row")
        actions = row["actions"]
        if (
            not isinstance(actions, list)
            or not actions
            or any(not isinstance(a, str) or a not in RULE_ACTIONS for a in actions)
            or len(set(actions)) != len(actions)
        ):
            raise RulesInvalid("policy_actions")
        limit = row["maxVolume"]
        if type(limit) is not int or not 0 <= limit <= 100:
            raise RulesInvalid("policy_max_volume")
        room = row.get("nativeRoomId")
        if room is not None and (
            not _room_key(room)
            or not isinstance(row.get("nativeHomepodId"), str)
            or not 1 <= len(row["nativeHomepodId"]) <= 128
        ):
            raise RulesInvalid("policy_row")
        if row["accountId"] != entry_id or room is None:
            ignored += 1
            continue
        if room in rooms:
            # PodVoice grants nothing when more than one rule matches a room.
            raise RulesInvalid("policy_duplicate_room")
        granted = list(actions)
        if {"pause", "resume"} & set(granted) and "seek" not in granted:
            granted.append("seek")
        rooms[room] = {"actions": sorted(granted), "max_volume": limit}
    rules = normalize(
        {"rooms": rooms, "other_account_guard": True, "unlisted_rooms": "allow"}
    )
    assert rules is not None
    return rules, ignored


def rule_action(action: str) -> str | None:
    """PodVoice's name for a PodConnect write; None when the rules do not cover it."""
    action = _ALIASES.get(action, action)
    if action in UNGOVERNED:
        return None
    return action if action in RULE_ACTIONS else None


def decide(
    rules: dict | None,
    room_id: str | None,
    action: str,
    *,
    account: str | None = None,
) -> str | None:
    """None when allowed, else the refusal reason (a translation key)."""
    if rules is None:
        return None
    wanted = rule_action(action)
    if wanted is None:
        return None
    if room_id is None:
        return ROOM_UNKNOWN if rules["rooms"] or rules["unlisted_rooms"] == "deny" else None
    rule = rules["rooms"].get(room_id)
    if rule is None:
        if rules["unlisted_rooms"] == "deny":
            return DENIED
    else:
        granted = set(rule["actions"])
        if wanted == MOVE:
            # PodView's "Flyt til" moves playback into a room whose rule grants any
            # player action; the separate move flow needs move_existing_session.
            if MOVE not in granted and not granted & set(PLAYER_ACTIONS):
                return DENIED
        elif wanted not in granted:
            return DENIED
    if (
        rules["other_account_guard"]
        and wanted in GUARDED_ACTIONS
        and account == "other"
    ):
        return OTHER_ACCOUNT
    return None


def ceiling(rules: dict | None, room_id: str | None) -> int | None:
    """The room's volume ceiling in percent, when it has one."""
    if rules is None or room_id is None:
        return None
    rule = rules["rooms"].get(room_id)
    return None if rule is None else rule["max_volume"]


def capped(rules: dict | None, room_id: str | None, percent: int) -> int:
    """PodVoice's ``min(value, maxVolume)``: the requested volume, never above the ceiling."""
    limit = ceiling(rules, room_id)
    return percent if limit is None else min(percent, limit)


_HEX64 = re.compile(r"[a-f0-9]{64}\Z")


def room_account(row: dict | None, configured_id: str | None) -> str:
    """"own"/"other" only from a byte comparison of the engine's logged-in username digest
    with the configured account's /me.id; "unknown" whenever either side is missing."""
    digest = (row or {}).get("account_sha256")
    if (
        not isinstance(digest, str)
        or _HEX64.fullmatch(digest) is None
        or not isinstance(configured_id, str)
        or not configured_id
    ):
        return "unknown"
    mine = hashlib.sha256(configured_id.encode("utf-8")).hexdigest()
    return "own" if hmac.compare_digest(mine, digest) else "other"


# --- Home Assistant side -----------------------------------------------------------


def rules_of(entry) -> dict | None:
    """The entry's current rules. Stored options were validated when they were set."""
    try:
        return normalize((getattr(entry, "options", None) or {}).get(OPTION))
    except RulesInvalid:
        # Never weaker than the stored intent: malformed rules deny every governed write.
        return {"rooms": {}, "other_account_guard": True, "unlisted_rooms": "deny"}


def room_row(entry, room_id: str | None) -> dict | None:
    rooms = getattr(getattr(entry, "runtime_data", None), "rooms", None)
    data = getattr(rooms, "data", None) or {}
    return data.get(room_id) if room_id is not None else None


def room_account_of(entry, room_id: str | None) -> str:
    return room_account(room_row(entry, room_id), getattr(entry, "unique_id", None))


def enforce(entry, room_id: str | None, action: str, *, percent: int | None = None):
    """Raise ServiceValidationError when the rules refuse; return the volume to send."""
    rules = rules_of(entry)
    if rules is None:
        return percent
    reason = decide(rules, room_id, action, account=room_account_of(entry, room_id))
    if reason is not None:
        from homeassistant.exceptions import ServiceValidationError

        from .const import DOMAIN

        row = room_row(entry, room_id) or {}
        name = row.get("name") if isinstance(row.get("name"), str) else None
        raise ServiceValidationError(
            translation_domain=DOMAIN,
            translation_key=reason,
            translation_placeholders={
                "room": name or room_id or "?",
                "action": rule_action(action) or action,
            },
        )
    if percent is None:
        return None
    return capped(rules, room_id, percent)


def governs(entry) -> bool:
    return rules_of(entry) is not None


async def route_room(entry) -> str | None:
    """The room the account's PodConnect speaker currently plays in, from Speakers."""
    speakers = getattr(getattr(entry, "runtime_data", None), "speakers", None)
    if speakers is None:
        return None
    from aiohttp import ClientError

    from .speakers import SpeakersError

    try:
        catalog = await speakers.aliases()
    except (SpeakersError, ClientError, TimeoutError):
        return None
    room = (catalog.get("binding") or {}).get("room_id")
    return room if isinstance(room, str) else None


async def enforce_on_route(entry, action: str, *, percent: int | None = None):
    """``enforce`` for writes addressed to the speaker rather than to a room."""
    if not governs(entry) or rule_action(action) is None:
        return percent
    if getattr(getattr(entry, "runtime_data", None), "speakers", None) is None:
        return percent  # Spotify devices only: no PodConnect rooms to govern.
    return enforce(entry, await route_room(entry), action, percent=percent)
