"""Data update coordinator for PodConnect — polls Spotify playback + devices."""

from __future__ import annotations

from datetime import timedelta
from typing import Any

from homeassistant.core import HomeAssistant
from homeassistant.helpers.update_coordinator import DataUpdateCoordinator, UpdateFailed
from homeassistant.util import dt as dt_util

from .api import SpotifyApi, SpotifyApiError
from .const import DOMAIN, LOGGER, POLL_INTERVAL_SECONDS


class PodConnectCoordinator(DataUpdateCoordinator[dict[str, Any]]):
    """Polls /me/player and /me/player/devices once per cycle for all entities."""

    def __init__(self, hass: HomeAssistant, entry, api: SpotifyApi) -> None:
        """Initialise the coordinator."""
        super().__init__(
            hass,
            LOGGER,
            name=DOMAIN,
            config_entry=entry,
            update_interval=timedelta(seconds=POLL_INTERVAL_SECONDS),
        )
        self.api = api

    async def _async_update_data(self) -> dict[str, Any]:
        """Fetch playback state + the device list."""
        try:
            playback = await self.api.playback_state()
            devices = await self.api.devices()
        except SpotifyApiError as err:
            raise UpdateFailed(str(err)) from err
        return {
            "playback": playback,
            "devices": devices,
            "fetched_at": dt_util.utcnow(),
        }
