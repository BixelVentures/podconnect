"""Bounded exact-account playlist search, through the existing Spotify owner."""

import json
import re

import voluptuous as vol
from homeassistant.core import SupportsResponse
from homeassistant.exceptions import HomeAssistantError

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
