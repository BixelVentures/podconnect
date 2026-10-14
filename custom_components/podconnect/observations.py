"""Account-owned observations; no optimistic entity state or playback mutations."""

from __future__ import annotations

import asyncio
import copy
import json
import time
from datetime import datetime

import voluptuous as vol
from aiohttp import ClientError
from homeassistant.core import SupportsResponse
from homeassistant.exceptions import HomeAssistantError
from homeassistant.util import dt as dt_util

from .api import SpotifyApiError
from .artwork import spotify_album_artwork
from .const import DOMAIN

PLAYBACK_MAX_AGE_S = 15.0
QUEUE_CACHE_S = 10.0
QUEUE_TIMEOUT_S = 6.0
PUBLIC_MAX_BYTES = 65536


def _error(kind: str) -> dict:
    return {"ok": False, "error_kind": kind}


def _text(value, *, nullable=False):
    if value is None and nullable:
        return None
    if not isinstance(value, str) or not 1 <= len(value) <= 1024:
        raise ValueError("invalid text")
    return value


def _number(value):
    if value is not None and (type(value) is not int or not 0 <= value < 2**53):
        raise ValueError("invalid number")
    return value


def _item(value):
    if value is None:
        return None
    if not isinstance(value, dict) or value.get("type") not in ("track", "episode"):
        raise ValueError("unsupported item")
    kind = value["type"]
    uri = _text(value.get("uri"))
    if not uri.startswith("spotify:" + kind + ":"):
        raise ValueError("invalid item uri")
    if kind == "track":
        artists = value.get("artists")
        if not isinstance(artists, list) or len(artists) > 20:
            raise ValueError("invalid artists")
        labels = [_text(artist.get("name")) for artist in artists if isinstance(artist, dict)]
        if len(labels) != len(artists):
            raise ValueError("invalid artist")
    else:
        show = value.get("show")
        labels = [_text(show["name"])] if isinstance(show, dict) and show.get("name") else []
    projected = {
        "type": kind,
        "uri": uri,
        "name": _text(value.get("name")),
        "artists": labels,
        "duration_ms": _number(value.get("duration_ms")),
    }
    artwork = spotify_album_artwork(value) if kind == "track" else None
    if artwork is not None:
        projected["artwork"] = artwork
    return projected


def _bounded(value):
    if len(json.dumps(value, allow_nan=False).encode()) > PUBLIC_MAX_BYTES:
        raise ValueError("response too large")
    return value


def current(hass, entry_id, expected=None):
    """Re-resolve the precise loaded account, including after every external await."""
    entry = hass.config_entries.async_get_entry(entry_id)
    data = getattr(entry, "runtime_data", None)
    if (
        entry is None
        or entry.domain != DOMAIN
        or data is None
        or not data.active
        or (expected is not None and data is not expected)
    ):
        raise HomeAssistantError("PodConnect account entry unavailable")
    return data


class ObservationReader:
    """One reader per runtime-data owner. No background tasks or polling."""

    def __init__(self, coordinator, api):
        self.coordinator, self.api = coordinator, api
        self._queue_busy = False
        self._queue_sequence = 0
        self._queue_cache = None
        self._queue_cached_at = 0.0
        self._queue_cache_ttl = QUEUE_CACHE_S
        self._queue_next_read_at = 0.0

    def playback(self, entry_id):
        coordinator = self.coordinator
        data = coordinator.data
        if not coordinator.last_update_success or not isinstance(data, dict):
            return _error("unavailable")
        observed = data.get("playback_observed_at")
        monotonic = data.get("playback_observed_monotonic")
        sequence = data.get("poll_sequence")
        if (
            not isinstance(observed, datetime)
            or type(monotonic) not in (float, int)
            or type(sequence) is not int
            or sequence < 1
        ):
            return _error("unavailable")
        age = time.monotonic() - monotonic
        if not 0 <= age <= PLAYBACK_MAX_AGE_S:
            return _error("stale")
        playback = data.get("playback")
        try:
            if playback is None:
                projected = {"state": "no_active_playback", "target": None, "item": None}
            else:
                if not isinstance(playback, dict) or type(playback.get("is_playing")) is not bool:
                    raise ValueError("invalid playback")
                device = playback.get("device")
                if not isinstance(device, dict):
                    raise ValueError("invalid device")
                device_id = _text(device.get("id"), nullable=True)
                shuffle = playback.get("shuffle_state")
                repeat = playback.get("repeat_state")
                if shuffle is not None and type(shuffle) is not bool:
                    raise ValueError("invalid shuffle")
                if repeat not in (None, "off", "track", "context"):
                    raise ValueError("invalid repeat")
                projected = {
                    "state": "playing" if playback["is_playing"] else "paused",
                    "target": {
                        "kind": "spotify_device", "target_id": device_id,
                        "name": _text(device.get("name"), nullable=True),
                    } if device_id is not None else None,
                    "item": _item(playback.get("item")),
                    "progress_ms": _number(playback.get("progress_ms")),
                    "provider_changed_at_ms": _number(playback.get("timestamp")),
                    "shuffle": shuffle,
                    "repeat": repeat,
                }
            return _bounded({
                "ok": True, "schema": "podconnect.playback.v1",
                "config_entry_id": entry_id, "owner_epoch": coordinator.owner_epoch,
                "poll_sequence": sequence,
                "observation": {
                    "status": "fresh", "observed_at": observed.isoformat(),
                    "age_ms": int(age * 1000), "max_age_ms": int(PLAYBACK_MAX_AGE_S * 1000),
                    "source": "spotify_web_api",
                },
                "playback": projected, "physical_result_verified": False,
            })
        except (ValueError, TypeError, KeyError):
            return _error("malformed_response")

    async def queue(self, entry_id, limit, is_current):
        if self._queue_cache is not None and time.monotonic() - self._queue_cached_at < self._queue_cache_ttl:
            return self._queue_view(self._queue_cache, limit)
        if self._queue_busy or time.monotonic() < self._queue_next_read_at:
            return _error("busy")
        self._queue_busy = True  # No await before admission; no unbounded waiter queue.
        self._queue_next_read_at = time.monotonic() + QUEUE_CACHE_S
        cache_ttl = QUEUE_CACHE_S
        self._queue_sequence += 1
        snapshot_id = f"{self.coordinator.owner_epoch}:queue:{self._queue_sequence}"
        try:
            async with asyncio.timeout(QUEUE_TIMEOUT_S):
                raw = await self.api.queue()
            is_current()
            if not isinstance(raw, dict) or not isinstance(raw.get("queue"), list):
                raise ValueError("invalid queue")
            rows = raw["queue"]
            # The HTTP layer bounds bytes before JSON parsing. Preserve order and duplicates.
            if len(rows) > 1000:
                raise ValueError("too many queue rows")
            items = []
            for index, row in enumerate(rows[:20]):
                item = _item(row)
                if item is None:
                    raise ValueError("invalid queue row")
                items.append({"option_id": f"{snapshot_id}:{index}", **item})
            result = _bounded({
                "ok": True, "schema": "podconnect.queue.v1", "config_entry_id": entry_id,
                "owner_epoch": self.coordinator.owner_epoch, "snapshot_id": snapshot_id,
                "observed_at": dt_util.utcnow().isoformat(), "scope": "account",
                "coverage": "provider_response", "relation_to_playback": "not_atomic",
                "currently_playing": _item(raw.get("currently_playing")), "items": items,
                "returned_count": len(items), "truncated": len(rows) > len(items),
            })
        except SpotifyApiError as err:
            if err.status == 429 and err.retry_after is not None:
                cache_ttl = max(QUEUE_CACHE_S, min(3600, err.retry_after))
            result = _error({401: "authentication_required", 403: "forbidden", 429: "rate_limited"}.get(err.status, "unavailable"))
        except (ClientError, TimeoutError):
            result = _error("unavailable")
        except (ValueError, TypeError, KeyError):
            result = _error("malformed_response")
        finally:
            self._queue_busy = False
        is_current()  # No cache publication after account unload/replacement, including errors.
        self._queue_cache, self._queue_cached_at = result, time.monotonic()
        self._queue_cache_ttl = cache_ttl
        return self._queue_view(result, limit)

    @staticmethod
    def _queue_view(result, limit):
        value = copy.deepcopy(result)
        if value.get("ok"):
            value["truncated"] |= len(value["items"]) > limit
            value["items"] = value["items"][:limit]
            value["returned_count"] = len(value["items"])
        return value


def _limit(value):
    if type(value) is not int:
        raise vol.Invalid("limit must be an integer")
    return value


def register_observation_services(hass):
    async def playback(call):
        entry_id = call.data["config_entry_id"]
        data = current(hass, entry_id)
        return data.observations.playback(entry_id)

    async def queue(call):
        entry_id = call.data["config_entry_id"]
        data = current(hass, entry_id)
        return await data.observations.queue(
            entry_id, call.data.get("limit", 20), lambda: current(hass, entry_id, data)
        )

    account = {vol.Required("config_entry_id"): vol.All(str, vol.Length(min=1, max=1024))}
    for name, handler, schema in (
        ("get_playback_snapshot", playback, account),
        ("get_queue", queue, {**account, vol.Optional("limit", default=20): vol.All(_limit, vol.Range(min=1, max=20))}),
    ):
        if not hass.services.has_service(DOMAIN, name):
            hass.services.async_register(DOMAIN, name, handler, schema=vol.Schema(schema), supports_response=SupportsResponse.ONLY)
