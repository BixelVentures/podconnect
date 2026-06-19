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
from .const import CONF_BASE_URL, LOGGER, SPOTIFY_SCOPES
from .coordinator import PodConnectCoordinator
from .speakers_api import SpeakersApi
from .speakers_coordinator import SpeakersCoordinator

# media_player is always forwarded (Spotify control). button is always forwarded too, but
# only adds entities when the optional add-on manager is configured (it returns early
# otherwise), keeping setup simple while honoring "Control works without the add-on".
PLATFORMS: list[Platform] = [Platform.MEDIA_PLAYER, Platform.BUTTON]


@dataclass
class PodConnectData:
    """Runtime data stored on the config entry."""

    api: SpotifyApi
    coordinator: PodConnectCoordinator
    session: OAuth2Session
    # Present only when the optional add-on manager URL is set in the entry's options.
    speakers_api: SpeakersApi | None = None
    speakers_coordinator: SpeakersCoordinator | None = None


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

    data = PodConnectData(api=api, coordinator=coordinator, session=session)

    # Optional: local (account-agnostic) speakers via the add-on manager. Only when a URL is
    # configured in options. On failure, log + continue WITHOUT local speakers — never break
    # Spotify control (docs/releasing.md principle #2).
    base_url = (entry.options.get(CONF_BASE_URL) or "").strip()
    if base_url:
        speakers_api = SpeakersApi(hass, base_url)
        speakers_coordinator = SpeakersCoordinator(hass, entry, speakers_api)
        try:
            await speakers_coordinator.async_config_entry_first_refresh()
        except Exception as err:  # noqa: BLE001 - never let the add-on break Spotify control
            LOGGER.warning(
                "PodConnect add-on at %s unreachable; local speakers disabled "
                "(Spotify control unaffected): %s",
                base_url,
                err,
            )
        else:
            data.speakers_api = speakers_api
            data.speakers_coordinator = speakers_coordinator

    entry.runtime_data = data
    await hass.config_entries.async_forward_entry_setups(entry, PLATFORMS)

    # Reload when options change so adding/clearing the add-on URL spins local speakers up/down.
    entry.async_on_unload(entry.add_update_listener(_async_update_listener))
    return True


async def _async_update_listener(
    hass: HomeAssistant, entry: PodConnectConfigEntry
) -> None:
    """Reload the entry when its options change."""
    await hass.config_entries.async_reload(entry.entry_id)


async def async_unload_entry(hass: HomeAssistant, entry: PodConnectConfigEntry) -> bool:
    """Unload a config entry."""
    return await hass.config_entries.async_unload_platforms(entry, PLATFORMS)
