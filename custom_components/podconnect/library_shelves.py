"""Bounded, exact-account library reads for PodView's Find musik shelves.

Response-only and read-only: no playback credit, no provider rows, no invented
names or images. Every Spotify send is re-checked against the selected account
and its owner epoch, and a rate limit is one honest failure (never retried).
"""

import asyncio
import json
import re

import voluptuous as vol
from aiohttp import ClientError
from homeassistant.core import SupportsResponse
from homeassistant.exceptions import HomeAssistantError

from .api import SpotifyApiError
from .artwork import spotify_image_artwork, spotify_playlist_artwork
from .const import DOMAIN
from .observations import current

SHELF_MAX_LIMIT = 20
RECENT_CONTEXT_MAX_LIMIT = 12
RECENT_PLAYS_READ = 50
READ_TIMEOUT_S = 8
RECENT_TIMEOUT_S = 12
REPLY_MAX_BYTES = 65536
TIME_RANGES = ("short_term", "medium_term", "long_term")
CONTEXT_KINDS = ("playlist", "album", "artist", "show")

_ID = re.compile(r"^[A-Za-z0-9]{22}$")
_PLAYED_AT = re.compile(r"^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:\.\d{1,6})?Z$")


class RateLimited(HomeAssistantError):
    """Spotify asked us to slow down; the caller decides when to read again."""


def _malformed(kind):
    return HomeAssistantError("Malformed Spotify " + kind)


def _name(value, limit=256):
    return isinstance(value, str) and 1 <= len(value) <= limit


def _object(row, kind):
    """Exact id/uri/name of one Spotify object of `kind`, or None when invalid."""
    if (
        not isinstance(row, dict)
        or row.get("type") != kind
        or not isinstance(row.get("id"), str)
        or not _ID.fullmatch(row["id"])
        or row.get("uri") != "spotify:" + kind + ":" + row["id"]
        or not _name(row.get("name"))
    ):
        return None
    return {"id": row["id"], "uri": row["uri"], "name": row["name"]}


def _artist_text(artists):
    if not isinstance(artists, list) or not 1 <= len(artists) <= 20:
        return None
    names = [a.get("name") for a in artists if isinstance(a, dict)]
    if len(names) != len(artists) or not all(_name(n, 128) for n in names):
        return None
    text = ", ".join(names)
    return text if len(text) <= 512 else None


def _page_rows(raw, limit, kind):
    """Validate one first page; Spotify may null an unavailable row (skipped)."""
    if not isinstance(raw, dict):
        raise _malformed(kind)
    rows, next_page = raw.get("items"), raw.get("next")
    if (
        not isinstance(rows, list)
        or len(rows) > limit
        or (next_page is not None and not _name(next_page, 2048))
    ):
        raise _malformed(kind)
    total = raw.get("total")
    more = next_page is not None or (type(total) is int and total > len(rows))
    return [row for row in rows if row is not None], more


def _bounded(body):
    if len(json.dumps(body, allow_nan=False).encode()) > REPLY_MAX_BYTES:
        raise HomeAssistantError("Library reply too large")
    return body


def _envelope(schema, account, epoch, limit, key, items, more, **extra):
    return _bounded(
        {
            "ok": True,
            "schema": "podconnect." + schema + ".v1",
            "config_entry_id": account,
            "owner_epoch": epoch,
            "source": "spotify_web_api",
            "coverage": "first_page",
            **extra,
            "limit": limit,
            "returned_count": len(items),
            "source_has_more": more,
            key: items,
        }
    )


def _unique(items, kind):
    if len({item["uri"] for item in items}) != len(items):
        raise _malformed(kind)
    return items


def project_top_artists(raw, account, epoch, limit, time_range):
    rows, more = _page_rows(raw, limit, "top artists")
    artists = []
    for row in rows:
        item = _object(row, "artist")
        if item is None:
            raise _malformed("top artists")
        artwork = spotify_image_artwork(row, "artist_image")
        if artwork is not None:
            item["artwork"] = artwork
        artists.append(item)
    return _envelope(
        "top_artists",
        account,
        epoch,
        limit,
        "artists",
        _unique(artists, "top artists"),
        more,
        time_range=time_range,
    )


def project_saved_albums(raw, account, epoch, limit):
    rows, more = _page_rows(raw, limit, "saved albums")
    albums = []
    for row in rows:
        album = row.get("album") if isinstance(row, dict) else None
        item = _object(album, "album")
        artist = _artist_text(album.get("artists")) if item else None
        if item is None or artist is None:
            raise _malformed("saved albums")
        item["artist"] = artist
        artwork = spotify_image_artwork(album, "album_cover")
        if artwork is not None:
            item["artwork"] = artwork
        albums.append(item)
    return _envelope(
        "saved_albums",
        account,
        epoch,
        limit,
        "albums",
        _unique(albums, "saved albums"),
        more,
    )


def project_saved_shows(raw, account, epoch, limit):
    rows, more = _page_rows(raw, limit, "saved shows")
    shows = []
    for row in rows:
        show = row.get("show") if isinstance(row, dict) else None
        item = _object(show, "show")
        if item is None:
            raise _malformed("saved shows")
        # Spotify removed `publisher` in February 2026; null, never invented.
        publisher = show.get("publisher")
        item["publisher"] = publisher if _name(publisher) else None
        artwork = spotify_image_artwork(show, "show_cover")
        if artwork is not None:
            item["artwork"] = artwork
        shows.append(item)
    return _envelope(
        "saved_shows",
        account,
        epoch,
        limit,
        "shows",
        _unique(shows, "saved shows"),
        more,
    )


def recent_context_candidates(raw, limit):
    """Distinct playable contexts, newest first, with the newest play of each."""
    if not isinstance(raw, dict):
        raise _malformed("recently played")
    rows = raw.get("items")
    if not isinstance(rows, list) or len(rows) > RECENT_PLAYS_READ:
        raise _malformed("recently played")
    seen, candidates = set(), []
    for row in rows:
        if not isinstance(row, dict):
            continue
        context = row.get("context")
        if not isinstance(context, dict):
            continue  # A play without a context (e.g. a single track) is skipped.
        kind, uri = context.get("type"), context.get("uri")
        if (
            kind not in CONTEXT_KINDS
            or not isinstance(uri, str)
            or not uri.startswith("spotify:" + kind + ":")
            or not _ID.fullmatch(uri.rsplit(":", 1)[1])
            or uri.count(":") != 2
            or uri in seen
        ):
            continue
        seen.add(uri)
        played_at = row.get("played_at")
        candidates.append(
            {
                "type": kind,
                "uri": uri,
                "track": row.get("track"),
                "played_at": played_at
                if isinstance(played_at, str) and _PLAYED_AT.fullmatch(played_at)
                else None,
            }
        )
        if len(candidates) == limit:
            break
    return candidates, len(rows)


def resolved_context(candidate, value):
    """Project a resolved object only when it is exactly the played context."""
    kind = candidate["type"]
    item = _object(value, kind)
    if item is None or item["uri"] != candidate["uri"]:
        return None
    projected = {"type": kind, "uri": item["uri"], "name": item["name"]}
    artwork = (
        spotify_playlist_artwork(value)
        if kind == "playlist"
        else spotify_image_artwork(
            value,
            {"album": "album_cover", "artist": "artist_image", "show": "show_cover"}[
                kind
            ],
        )
    )
    if artwork is not None:
        projected["artwork"] = artwork
    if candidate["played_at"] is not None:
        projected["last_played_at"] = candidate["played_at"]
    return projected


def project_recent_contexts(contexts, account, epoch, limit, scanned, dropped):
    return _bounded(
        {
            "ok": True,
            "schema": "podconnect.recent_contexts.v1",
            "config_entry_id": account,
            "owner_epoch": epoch,
            "source": "spotify_web_api",
            "coverage": "recently_played_window",
            "limit": limit,
            "scanned_play_count": scanned,
            "dropped_count": dropped,
            "returned_count": len(contexts),
            "contexts": contexts,
        }
    )


# Cheapest still-available lookups (February 2026): one single-object GET each.
_RESOLVE = {
    "album": ("/albums/", None, 262144),
    "artist": ("/artists/", None, 65536),
    "playlist": ("/playlists/", {"fields": "type,id,uri,name,images"}, 262144),
    "show": ("/shows/", None, 524288),
}


def _limit(maximum):
    def validate(value):
        if type(value) is not int or not 1 <= value <= maximum:
            raise vol.Invalid("limit must be an integer from 1 to " + str(maximum))
        return value

    return validate


def _time_range(value):
    if value not in TIME_RANGES:
        raise vol.Invalid("time_range must be short_term, medium_term or long_term")
    return value


def _owner_scope(hass, library_owner, account, label):
    entry, owner = library_owner(hass, account)
    epoch, identity = owner.coordinator.owner_epoch, entry.unique_id
    if not _name(identity, 1024) or not _name(epoch, 1024):
        raise HomeAssistantError(label + " account unavailable")

    def check_owner():
        current(hass, account, owner)
        if (
            hass.config_entries.async_get_entry(account) is not entry
            or entry.unique_id != identity
            or owner.coordinator.owner_epoch != epoch
        ):
            raise HomeAssistantError(label + " owner replaced")

    async def before_send():
        check_owner()

    check_owner()
    return owner, epoch, check_owner, before_send


def _provider_error(err, label):
    if isinstance(err, SpotifyApiError) and err.status == 429:
        return RateLimited("Spotify rate limited " + label + "; try again later")
    return HomeAssistantError("Spotify " + label + " unavailable")


async def _read(owner, check_owner, before_send, label, path, params, max_bytes):
    try:
        async with asyncio.timeout(READ_TIMEOUT_S):
            raw = await owner.api.library_read(
                path, params, owner_guard=before_send, max_bytes=max_bytes
            )
    except (SpotifyApiError, ClientError, TimeoutError, ValueError) as err:
        check_owner()
        raise _provider_error(err, label) from err
    check_owner()
    return raw


async def _resolve(owner, check_owner, before_send, candidate):
    """Resolve one context's name/image; None drops it, a rate limit aborts."""
    kind, uri = candidate["type"], candidate["uri"]
    track = candidate["track"]
    if kind == "album" and isinstance(track, dict):
        found = resolved_context(candidate, track.get("album"))
        if found is not None:  # The played track already carries its album.
            return found
    path, params, max_bytes = _RESOLVE[kind]
    try:
        value = await owner.api.library_read(
            path + uri.rsplit(":", 1)[1],
            params,
            owner_guard=before_send,
            max_bytes=max_bytes,
        )
    except SpotifyApiError as err:
        check_owner()
        if err.status == 429:
            raise _provider_error(err, "recent contexts") from err
        return None
    except (ClientError, ValueError):
        check_owner()
        return None
    check_owner()
    return resolved_context(candidate, value)


def register_library_shelves(hass, library_owner):
    """Register the four response-only Find musik reads once."""
    account_field = vol.All(str, vol.Length(min=1, max=1024))

    async def top_artists(call):
        account, limit, time_range = (
            call.data[k] for k in ("config_entry_id", "limit", "time_range")
        )
        owner, epoch, check, send = _owner_scope(
            hass, library_owner, account, "Top artists"
        )
        raw = await _read(
            owner,
            check,
            send,
            "top artists",
            "/me/top/artists",
            {"limit": limit, "offset": 0, "time_range": time_range},
            262144,
        )
        return project_top_artists(raw, account, epoch, limit, time_range)

    async def saved_albums(call):
        account, limit = (call.data[k] for k in ("config_entry_id", "limit"))
        owner, epoch, check, send = _owner_scope(
            hass, library_owner, account, "Saved albums"
        )
        raw = await _read(
            owner,
            check,
            send,
            "saved albums",
            "/me/albums",
            {"limit": limit, "offset": 0},
            524288,
        )
        return project_saved_albums(raw, account, epoch, limit)

    async def saved_shows(call):
        account, limit = (call.data[k] for k in ("config_entry_id", "limit"))
        owner, epoch, check, send = _owner_scope(
            hass, library_owner, account, "Saved shows"
        )
        raw = await _read(
            owner,
            check,
            send,
            "saved shows",
            "/me/shows",
            {"limit": limit, "offset": 0},
            262144,
        )
        return project_saved_shows(raw, account, epoch, limit)

    async def recent_contexts(call):
        account, limit = (call.data[k] for k in ("config_entry_id", "limit"))
        owner, epoch, check, send = _owner_scope(
            hass, library_owner, account, "Recent contexts"
        )
        try:
            async with asyncio.timeout(RECENT_TIMEOUT_S):
                raw = await _read(
                    owner,
                    check,
                    send,
                    "recent contexts",
                    "/me/player/recently-played",
                    {"limit": RECENT_PLAYS_READ},
                    524288,
                )
                candidates, scanned = recent_context_candidates(raw, limit)
                contexts = []
                for candidate in candidates:
                    found = await _resolve(owner, check, send, candidate)
                    if found is not None:
                        contexts.append(found)
        except TimeoutError as err:
            check()
            raise HomeAssistantError("Spotify recent contexts unavailable") from err
        return project_recent_contexts(
            contexts, account, epoch, limit, scanned, len(candidates) - len(contexts)
        )

    shelf_limit = vol.Optional("limit", default=SHELF_MAX_LIMIT)
    services = {
        "top_artists": (
            top_artists,
            {
                vol.Required("config_entry_id"): account_field,
                shelf_limit: _limit(SHELF_MAX_LIMIT),
                vol.Optional("time_range", default="medium_term"): _time_range,
            },
        ),
        "saved_albums": (
            saved_albums,
            {
                vol.Required("config_entry_id"): account_field,
                shelf_limit: _limit(SHELF_MAX_LIMIT),
            },
        ),
        "saved_shows": (
            saved_shows,
            {
                vol.Required("config_entry_id"): account_field,
                shelf_limit: _limit(SHELF_MAX_LIMIT),
            },
        ),
        "recent_contexts": (
            recent_contexts,
            {
                vol.Required("config_entry_id"): account_field,
                vol.Optional("limit", default=RECENT_CONTEXT_MAX_LIMIT): _limit(
                    RECENT_CONTEXT_MAX_LIMIT
                ),
            },
        ),
    }
    for name, (handler, schema) in services.items():
        if not hass.services.has_service(DOMAIN, name):
            hass.services.async_register(
                DOMAIN,
                name,
                handler,
                schema=vol.Schema(schema),
                supports_response=SupportsResponse.ONLY,
            )
