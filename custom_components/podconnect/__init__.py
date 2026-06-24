"""The PodConnect integration (Spotify control for HA)."""

from __future__ import annotations

from dataclasses import dataclass

import aiohttp

from homeassistant.config_entries import ConfigEntry
from homeassistant.const import Platform
from homeassistant.core import (
    HomeAssistant,
    ServiceCall,
    ServiceResponse,
    SupportsResponse,
)
from homeassistant.exceptions import ConfigEntryAuthFailed, ConfigEntryNotReady
from homeassistant.helpers.config_entry_oauth2_flow import (
    ImplementationUnavailableError,
    OAuth2Session,
    async_get_config_entry_implementation,
)

from .api import SpotifyApi, SpotifyApiError
from .const import DOMAIN, LOGGER, SPOTIFY_SCOPES
from .coordinator import PodConnectCoordinator

PLATFORMS: list[Platform] = [Platform.MEDIA_PLAYER]


@dataclass
class PodConnectData:
    """Runtime data stored on the config entry."""

    api: SpotifyApi
    coordinator: PodConnectCoordinator
    session: OAuth2Session


PodConnectConfigEntry = ConfigEntry[PodConnectData]

# Listening-history "data tools" for an AI assist. Browse exposes these too, but browse is
# WebSocket-only — these are response-returning REST services so a generic caller (PodVoice's
# home_call, scripts) can FETCH the user's top tracks / recent / liked and then play a chosen URI.
# Account-wide (not per-speaker), so they're domain services, not entity services.
_LIBRARY_SERVICES = ("top_tracks", "recently_played", "liked")


def _track_list(items: list[dict]) -> ServiceResponse:
    """Map raw Spotify track objects to a compact {name, artist, uri} list for an LLM."""
    return {
        "tracks": [
            {
                "name": t.get("name"),
                "artist": ", ".join(a.get("name") for a in (t.get("artists") or []) if a.get("name")),
                "uri": t.get("uri"),
            }
            for t in items
            if t and t.get("uri")
        ]
    }


def _register_services(hass: HomeAssistant) -> None:
    """Register the response-returning library services once (idempotent across config entries)."""
    if hass.services.has_service(DOMAIN, _LIBRARY_SERVICES[0]):
        return

    def _api() -> SpotifyApi | None:
        for entry in hass.config_entries.async_entries(DOMAIN):
            data = getattr(entry, "runtime_data", None)
            if data is not None:
                return data.api
        return None

    def _make(method_name: str):
        async def _handler(call: ServiceCall) -> ServiceResponse:
            api = _api()
            if api is None:
                return {"tracks": []}
            try:
                items = await getattr(api, method_name)()
            except SpotifyApiError as err:
                LOGGER.warning("%s service failed (re-auth may be needed): %s", method_name, err)
                return {"tracks": []}
            return _track_list(items)

        return _handler

    # service name -> SpotifyApi method
    for svc, method in (("top_tracks", "top_tracks"), ("recently_played", "recently_played"), ("liked", "saved_tracks")):
        hass.services.async_register(DOMAIN, svc, _make(method), supports_response=SupportsResponse.ONLY)


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

    entry.runtime_data = PodConnectData(api=api, coordinator=coordinator, session=session)
    _register_services(hass)
    await hass.config_entries.async_forward_entry_setups(entry, PLATFORMS)
    return True


async def async_unload_entry(hass: HomeAssistant, entry: PodConnectConfigEntry) -> bool:
    """Unload a config entry."""
    return await hass.config_entries.async_unload_platforms(entry, PLATFORMS)
