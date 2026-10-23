"""Bounded exact-account playlist search, through the existing Spotify owner."""

import asyncio
import json
import re
from urllib.parse import parse_qs, urlsplit

import voluptuous as vol
from aiohttp import ClientError
from homeassistant.core import SupportsResponse
from homeassistant.exceptions import HomeAssistantError

from .api import SpotifyApiError
from .artwork import spotify_playlist_artwork
from .const import DOMAIN
from .observations import current

PLAYLIST = re.compile(r"^spotify:playlist:[A-Za-z0-9]{22}$")


def project_playlists(raw, account, epoch, query, limit, *, include_artwork=False):
    rows = raw.get("playlists", {}).get("items") if isinstance(raw, dict) else None
    if not isinstance(rows, list) or len(rows) > 20:
        raise HomeAssistantError("Malformed playlist search")
    result = []
    for row in rows[:limit]:
        if row is None:  # Spotify may omit unavailable objects; never invent one.
            continue
        if (
            not isinstance(row, dict)
            or row.get("type") != "playlist"
            or not isinstance(row.get("uri"), str)
            or not PLAYLIST.fullmatch(row["uri"])
            or not isinstance(row.get("name"), str)
            or not 1 <= len(row["name"]) <= 256
        ):
            raise HomeAssistantError("Malformed playlist search")
        projected = {"uri": row["uri"], "name": row["name"]}
        if include_artwork:
            artwork = spotify_playlist_artwork(row)
            if artwork is not None:
                projected["artwork"] = artwork
        result.append(projected)
    body = {
        "ok": True,
        "schema": "podconnect.search_playlists.v1",
        "config_entry_id": account,
        "owner_epoch": epoch,
        "source": "spotify_web_api",
        "query": query,
        "limit": limit,
        "playlists": result,
        "returned_count": len(result),
    }
    if len(json.dumps(body).encode()) > 16384:
        raise HomeAssistantError("Playlist search too large")
    return body


def register_playlist_search(hass, library_owner, query_validator, limit_validator):
    # Both passive playlist reads share the established setup registration hook.
    register_user_playlists(hass, library_owner)
    if hass.services.has_service(DOMAIN, "search_playlists"):
        return

    async def search(call):
        account, query, limit = (
            call.data[key] for key in ("config_entry_id", "query", "limit")
        )
        include_artwork = call.data["include_artwork"]
        entry, owner = library_owner(hass, account)
        epoch = owner.coordinator.owner_epoch
        raw = await owner.api.search(query, types="playlist", limit=limit)
        current(hass, entry.entry_id, owner)
        if owner.coordinator.owner_epoch != epoch:
            raise HomeAssistantError("Playlist owner replaced")
        return project_playlists(
            raw, account, epoch, query, limit, include_artwork=include_artwork
        )

    hass.services.async_register(
        DOMAIN,
        "search_playlists",
        search,
        schema=vol.Schema(
            {
                vol.Required("config_entry_id"): vol.All(
                    str, vol.Length(min=1, max=1024)
                ),
                vol.Required("query"): query_validator,
                vol.Optional("limit", default=3): limit_validator,
                vol.Optional("include_artwork", default=False): bool,
            }
        ),
        supports_response=SupportsResponse.ONLY,
    )


def _user_playlist_limit(value):
    if type(value) is not int or not 1 <= value <= 20:
        raise vol.Invalid("Playlist limit must be 1..20")
    return value


def project_user_playlists(raw, account, epoch, limit):
    """Validate the complete first page before exposing its small public DTO."""
    if not isinstance(raw, dict):
        raise HomeAssistantError("Malformed user playlists")
    rows, total, next_page = (raw.get(key) for key in ("items", "total", "next"))
    if (
        not isinstance(rows, list)
        or len(rows) > limit
        or type(raw.get("offset")) is not int
        or raw["offset"] != 0
        or type(raw.get("limit")) is not int
        or raw["limit"] != limit
        or type(total) is not int
        or not len(rows) <= total < 2**53
        or "next" not in raw
    ):
        raise HomeAssistantError("Malformed user playlists")
    has_more = total > len(rows)
    if next_page is not None:
        if not isinstance(next_page, str) or not 1 <= len(next_page) <= 2048:
            raise HomeAssistantError("Malformed playlist paging")
        try:
            url = urlsplit(next_page)
            query = parse_qs(url.query, strict_parsing=True)
            valid = (
                url.scheme == "https"
                and url.netloc == "api.spotify.com"
                and url.path == "/v1/me/playlists"
                and not url.fragment
                and set(query) == {"limit", "offset"}
                and query["limit"] == [str(limit)]
                and query["offset"] == [str(limit)]
            )
        except ValueError:
            valid = False
        if not valid or not has_more or len(rows) != limit:
            raise HomeAssistantError("Malformed playlist paging")
    elif has_more:
        raise HomeAssistantError("Malformed playlist paging")
    # No prefix slicing: validate even the final row before any projection.
    seen = set()
    for row in rows:
        if (
            not isinstance(row, dict)
            or row.get("type") != "playlist"
            or not isinstance(row.get("uri"), str)
            or not PLAYLIST.fullmatch(row["uri"])
            or row["uri"] in seen
            or not isinstance(row.get("name"), str)
            or not 1 <= len(row["name"]) <= 256
        ):
            raise HomeAssistantError("Malformed user playlists")
        seen.add(row["uri"])
    result = []
    for row in rows:
        item = {"uri": row["uri"], "name": row["name"]}
        artwork = spotify_playlist_artwork(row)
        if artwork is not None:
            item["artwork"] = artwork
        result.append(item)
    body = {
        "ok": True,
        "schema": "podconnect.user_playlists.v1",
        "config_entry_id": account,
        "owner_epoch": epoch,
        "source": "spotify_web_api",
        "coverage": "first_page",
        "limit": limit,
        "returned_count": len(result),
        "source_has_more": has_more,
        "playlists": result,
    }
    if len(json.dumps(body, allow_nan=False).encode()) > 65536:
        raise HomeAssistantError("User playlists too large")
    return body


def register_user_playlists(hass, library_owner):
    """Register a response-only first-page read; never creates playback credit."""
    if hass.services.has_service(DOMAIN, "get_user_playlists"):
        return

    async def user_playlists(call):
        account, limit = (call.data[key] for key in ("config_entry_id", "limit"))
        entry, owner = library_owner(hass, account)
        epoch, identity = owner.coordinator.owner_epoch, entry.unique_id
        if (
            not isinstance(identity, str)
            or not identity
            or not isinstance(epoch, str)
            or not epoch
        ):
            raise HomeAssistantError("Playlist account unavailable")

        def check_owner():
            current(hass, account, owner)
            if (
                hass.config_entries.async_get_entry(account) is not entry
                or entry.unique_id != identity
                or owner.coordinator.owner_epoch != epoch
            ):
                raise HomeAssistantError("Playlist owner replaced")

        async def before_send():
            check_owner()

        check_owner()
        try:
            async with asyncio.timeout(8):
                raw = await owner.api.user_playlists(limit, owner_guard=before_send)
        except (SpotifyApiError, ClientError, TimeoutError, ValueError) as err:
            check_owner()
            raise HomeAssistantError("Spotify user playlists unavailable") from err
        check_owner()
        return project_user_playlists(raw, account, epoch, limit)

    hass.services.async_register(
        DOMAIN,
        "get_user_playlists",
        user_playlists,
        schema=vol.Schema(
            {
                vol.Required("config_entry_id"): vol.All(
                    str, vol.Length(min=1, max=1024)
                ),
                vol.Optional("limit", default=20): _user_playlist_limit,
            }
        ),
        supports_response=SupportsResponse.ONLY,
    )
