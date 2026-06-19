"""The PodConnect Speakers integration (wraps the add-on manager HTTP API)."""

from __future__ import annotations

from dataclasses import dataclass

from homeassistant.config_entries import ConfigEntry
from homeassistant.const import Platform
from homeassistant.core import HomeAssistant

from .api import SpeakersApi
from .const import CONF_BASE_URL
from .coordinator import PodConnectSpeakersCoordinator

PLATFORMS: list[Platform] = [Platform.MEDIA_PLAYER, Platform.BUTTON]


@dataclass
class PodConnectSpeakersData:
    """Runtime data stored on the config entry."""

    api: SpeakersApi
    coordinator: PodConnectSpeakersCoordinator


PodConnectSpeakersConfigEntry = ConfigEntry[PodConnectSpeakersData]


async def async_setup_entry(
    hass: HomeAssistant, entry: PodConnectSpeakersConfigEntry
) -> bool:
    """Set up PodConnect Speakers from a config entry."""
    api = SpeakersApi(hass, entry.data[CONF_BASE_URL])
    coordinator = PodConnectSpeakersCoordinator(hass, entry, api)
    await coordinator.async_config_entry_first_refresh()

    entry.runtime_data = PodConnectSpeakersData(api=api, coordinator=coordinator)
    await hass.config_entries.async_forward_entry_setups(entry, PLATFORMS)
    return True


async def async_unload_entry(
    hass: HomeAssistant, entry: PodConnectSpeakersConfigEntry
) -> bool:
    """Unload a config entry."""
    return await hass.config_entries.async_unload_platforms(entry, PLATFORMS)
