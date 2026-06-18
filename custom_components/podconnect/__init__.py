"""The PodConnect integration (Spotify control for HA)."""

from __future__ import annotations

from dataclasses import dataclass

import aiohttp

from homeassistant.config_entries import ConfigEntry
from homeassistant.const import Platform
from homeassistant.core import HomeAssistant
from homeassistant.exceptions import ConfigEntryAuthFailed, ConfigEntryNotReady
from homeassistant.helpers.config_entry_oauth2_flow import (
    ImplementationUnavailableError,
    OAuth2Session,
    async_get_config_entry_implementation,
)

from .api import SpotifyApi
from .const import SPOTIFY_SCOPES
from .coordinator import PodConnectCoordinator

PLATFORMS: list[Platform] = [Platform.MEDIA_PLAYER]


@dataclass
class PodConnectData:
    """Runtime data stored on the config entry."""

    api: SpotifyApi
    coordinator: PodConnectCoordinator
    session: OAuth2Session


PodConnectConfigEntry = ConfigEntry[PodConnectData]


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
    await hass.config_entries.async_forward_entry_setups(entry, PLATFORMS)
    return True


async def async_unload_entry(hass: HomeAssistant, entry: PodConnectConfigEntry) -> bool:
    """Unload a config entry."""
    return await hass.config_entries.async_unload_platforms(entry, PLATFORMS)
