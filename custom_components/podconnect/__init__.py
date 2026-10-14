"""The PodConnect integration (Spotify control for HA)."""

from __future__ import annotations

import json
import re
from dataclasses import dataclass

import aiohttp
import voluptuous as vol

from homeassistant.config_entries import ConfigEntry
from homeassistant.const import Platform
from homeassistant.core import (
    HomeAssistant,
    ServiceCall,
    ServiceResponse,
    SupportsResponse,
)
from homeassistant.exceptions import HomeAssistantError, ConfigEntryAuthFailed, ConfigEntryNotReady
from homeassistant.helpers.aiohttp_client import async_get_clientsession
from homeassistant.helpers.config_entry_oauth2_flow import (
    ImplementationUnavailableError,
    OAuth2Session,
    async_get_config_entry_implementation,
)

from .api import SpotifyApi, SpotifyApiError
from .artwork import spotify_album_artwork
from .commands import PlaybackCommands, register_command_services
from .const import CONF_SPEAKERS_URL, DOMAIN, LOGGER, SPOTIFY_SCOPES
from .coordinator import PodConnectCoordinator
from .observations import ObservationReader, current, register_observation_services
from .speakers import SpeakersClient
from .targets import register_target_services

PLATFORMS: list[Platform] = [Platform.MEDIA_PLAYER]


@dataclass
class PodConnectData:
    """Runtime data stored on the config entry."""

    api: SpotifyApi
    coordinator: PodConnectCoordinator
    session: OAuth2Session
    speakers: SpeakersClient | None = None
    active: bool = False
    observations: ObservationReader | None = None
    commands: PlaybackCommands | None = None


PodConnectConfigEntry = ConfigEntry[PodConnectData]

# Listening-history "data tools" for an AI assist. Browse exposes these too, but browse is
# WebSocket-only — these are response-returning REST services so a generic caller (PodVoice's
# home_call, scripts) can FETCH the user's top tracks / recent / liked and then play a chosen URI.
# Account-wide (not per-speaker), so they're domain services, not entity services.
_LIBRARY_SERVICES = ("top_tracks", "recently_played", "liked")
_SEARCH_URI = re.compile(r"^spotify:track:[A-Za-z0-9]{22}$")
_SEARCH_MAX_LIMIT = 20
_SEARCH_MAX_REPLY_BYTES = 65536


def _search_query(value: object) -> str:
    """Normalize and bound an actual Spotify search query."""
    if not isinstance(value, str):
        raise vol.Invalid("query must be text")
    query = value.strip()
    if not query or len(query) > 160:
        raise vol.Invalid("query must contain 1 to 160 characters")
    return query


def _search_limit(value: object) -> int:
    """Reject bools and coercions; search limits are exact integers."""
    if type(value) is not int or not 1 <= value <= _SEARCH_MAX_LIMIT:
        raise vol.Invalid("limit must be an integer from 1 to 20")
    return value


def _search_projection(result: object, entry_id: str, query: str, limit: int) -> ServiceResponse:
    """Return a bounded, closed Spotify search sample, never provider rows."""
    if not isinstance(result, dict):
        raise HomeAssistantError("Spotify search returned malformed data")
    container = result.get("tracks")
    rows = container.get("items") if isinstance(container, dict) else None
    if not isinstance(rows, list):
        raise HomeAssistantError("Spotify search returned malformed data")
    sample = rows[:limit]
    projected = []
    for track in sample:
        if (
            not isinstance(track, dict)
            or track.get("type") != "track"
            or not isinstance(track.get("uri"), str)
            or not _SEARCH_URI.fullmatch(track["uri"])
            or not isinstance(track.get("name"), str)
            or not 1 <= len(track["name"]) <= 256
        ):
            raise HomeAssistantError("Spotify search returned malformed track data")
        artists = track.get("artists")
        if not isinstance(artists, list) or not 1 <= len(artists) <= 20:
            raise HomeAssistantError("Spotify search returned malformed artist data")
        names = [artist.get("name") for artist in artists if isinstance(artist, dict)]
        if len(names) != len(artists) or any(
            not isinstance(name, str) or not 1 <= len(name) <= 128 for name in names
        ):
            raise HomeAssistantError("Spotify search returned malformed artist data")
        artist = ", ".join(names)
        if len(artist) > 512:
            raise HomeAssistantError("Spotify search returned oversized artist data")
        item = {"name": track["name"], "artist": artist, "uri": track["uri"]}
        artwork = spotify_album_artwork(track)
        if artwork is not None:
            item["artwork"] = artwork
        projected.append(item)

    total = container.get("total")
    if len(rows) > len(sample):
        more = True
    elif type(total) is int and total >= len(sample):
        more = total > len(sample)
    else:
        more = None
    response = {
        "ok": True,
        "schema": "podconnect.search_tracks.v1",
        "config_entry_id": entry_id,
        "source": "spotify_web_api",
        "query": query,
        "coverage": "spotify_search_sample",
        "limit": limit,
        "source_sample_count": len(sample),
        "returned_count": len(projected),
        "source_has_more": more,
        "tracks": projected,
    }
    if len(json.dumps(response, allow_nan=False).encode()) > _SEARCH_MAX_REPLY_BYTES:
        raise HomeAssistantError("Spotify search reply exceeds size limit")
    return response


def _track_list(items: list[dict], config_entry_id: str | None = None) -> ServiceResponse:
    """Map raw Spotify tracks to a closed list with optional validated album art."""
    tracks = []
    for track in items:
        if not isinstance(track, dict) or not track.get("uri"):
            continue
        projected = {
            "name": track.get("name"),
            "artist": ", ".join(
                artist.get("name")
                for artist in (track.get("artists") or [])
                if isinstance(artist, dict) and artist.get("name")
            ),
            "uri": track["uri"],
        }
        artwork = spotify_album_artwork(track)
        if artwork is not None:
            projected["artwork"] = artwork
        tracks.append(projected)
    result = {"tracks": tracks}
    if config_entry_id is not None:
        result["config_entry_id"] = config_entry_id
    return result


def _library_owner(hass, config_entry_id=None):
    """Select an exact owner; retain omission only for one active account."""
    if config_entry_id is not None:
        entry = hass.config_entries.async_get_entry(config_entry_id)
        data = current(hass, config_entry_id)
        return entry, data
    active = [
        entry
        for entry in hass.config_entries.async_entries(DOMAIN)
        if getattr(getattr(entry, "runtime_data", None), "active", False)
    ]
    if len(active) != 1:
        raise HomeAssistantError("Select one PodConnect account")
    entry = active[0]
    data = current(hass, entry.entry_id, entry.runtime_data)
    return entry, data


def _register_services(hass: HomeAssistant) -> None:
    """Register the response-returning library services once (idempotent across config entries)."""
    def _make(method_name: str):
        async def _handler(call: ServiceCall) -> ServiceResponse:
            entry, owner = _library_owner(
                hass, call.data.get("config_entry_id")
            )
            try:
                items = await getattr(owner.api, method_name)()
            except SpotifyApiError as err:
                raise HomeAssistantError(f"Spotify library request failed: {err}") from err
            current(hass, entry.entry_id, owner)
            return _track_list(items, entry.entry_id)

        return _handler

    # service name -> SpotifyApi method
    for svc, method in (("top_tracks", "top_tracks"), ("recently_played", "recently_played"), ("liked", "saved_tracks")):
        if not hass.services.has_service(DOMAIN, svc):
            hass.services.async_register(
                DOMAIN,
                svc,
                _make(method),
                schema=vol.Schema(
                    {
                        vol.Optional("config_entry_id"): vol.All(
                            str, vol.Length(min=1, max=1024)
                        )
                    }
                ),
                supports_response=SupportsResponse.ONLY,
            )

    if not hass.services.has_service(DOMAIN, "search_tracks"):
        async def _search(call: ServiceCall) -> ServiceResponse:
            entry_id = call.data["config_entry_id"]
            query = call.data["query"]
            limit = call.data["limit"]
            entry, owner = _library_owner(hass, entry_id)
            try:
                result = await owner.api.search(query, types="track", limit=limit)
            except SpotifyApiError as err:
                current(hass, entry.entry_id, owner)
                raise HomeAssistantError("Spotify search failed") from err
            current(hass, entry.entry_id, owner)
            return _search_projection(result, entry.entry_id, query, limit)

        hass.services.async_register(
            DOMAIN,
            "search_tracks",
            _search,
            schema=vol.Schema(
                {
                    vol.Required("config_entry_id"): vol.All(
                        str, vol.Length(min=1, max=1024)
                    ),
                    vol.Required("query"): _search_query,
                    vol.Optional("limit", default=3): _search_limit,
                }
            ),
            supports_response=SupportsResponse.ONLY,
        )


async def async_setup_entry(hass: HomeAssistant, entry: PodConnectConfigEntry) -> bool:
    """Set up PodConnect from a config entry."""
    try:
        implementation = await async_get_config_entry_implementation(hass, entry)
    except ImplementationUnavailableError as err:
        raise ConfigEntryNotReady("OAuth2 implementation not available yet") from err

    session = OAuth2Session(hass, entry, implementation)
    try:
        await session.async_ensure_token_valid()
    except aiohttp.ClientError as err:
        raise ConfigEntryNotReady from err

    # Spotify returns granted scopes space-separated in the token.
    if not set(session.token["scope"].split(" ")).issuperset(SPOTIFY_SCOPES):
        raise ConfigEntryAuthFailed("Spotify scopes changed; please re-authenticate")

    api = SpotifyApi(hass, session)
    coordinator = PodConnectCoordinator(hass, entry, api)
    await coordinator.async_config_entry_first_refresh()

    base = entry.options.get(CONF_SPEAKERS_URL)
    speakers = SpeakersClient(async_get_clientsession(hass), base) if base else None
    entry.runtime_data = PodConnectData(api=api, coordinator=coordinator, session=session, speakers=speakers)
    entry.runtime_data.observations = ObservationReader(coordinator, api)
    entry.runtime_data.commands = PlaybackCommands(entry.runtime_data)
    _register_services(hass)
    register_command_services(hass)
    register_observation_services(hass)
    register_target_services(hass)
    entry.async_on_unload(entry.add_update_listener(_options_updated))
    await hass.config_entries.async_forward_entry_setups(entry, PLATFORMS)
    entry.runtime_data.active = True
    return True


async def async_unload_entry(hass: HomeAssistant, entry: PodConnectConfigEntry) -> bool:
    """Unload a config entry."""
    data = entry.runtime_data
    previously_active = data.active
    data.active = False  # Retire new admission before the platform unload can yield.
    unloaded = await hass.config_entries.async_unload_platforms(entry, PLATFORMS)
    if not unloaded:
        data.active = previously_active
    return unloaded


async def _options_updated(hass: HomeAssistant, entry: PodConnectConfigEntry) -> None:
    """Replace the entry-owned client through the normal HA unload/setup path."""
    await hass.config_entries.async_reload(entry.entry_id)
