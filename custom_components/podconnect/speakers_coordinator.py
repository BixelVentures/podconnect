"""Data update coordinator for PodConnect local speakers — polls the add-on manager API."""

from __future__ import annotations

from datetime import timedelta

from homeassistant.config_entries import ConfigEntry
from homeassistant.core import HomeAssistant
from homeassistant.helpers.update_coordinator import DataUpdateCoordinator, UpdateFailed

from .const import DOMAIN, LOGGER, SPEAKERS_POLL_INTERVAL_SECONDS
from .speakers_api import SpeakersApi, SpeakersApiError


class SpeakersCoordinator(DataUpdateCoordinator[list[dict]]):
    """Polls /api/rooms once per cycle for all local speaker entities."""

    def __init__(self, hass: HomeAssistant, entry: ConfigEntry, api: SpeakersApi) -> None:
        """Initialise the coordinator."""
        super().__init__(
            hass,
            LOGGER,
            name=f"{DOMAIN}_speakers",
            config_entry=entry,
            update_interval=timedelta(seconds=SPEAKERS_POLL_INTERVAL_SECONDS),
        )
        self.api = api

    async def _async_update_data(self) -> list[dict]:
        """Fetch the per-room speaker state from the manager."""
        try:
            return await self.api.rooms()
        except SpeakersApiError as err:
            raise UpdateFailed(str(err)) from err
