"""Data update coordinator for PodConnect Speakers — polls the add-on manager API."""

from __future__ import annotations

from datetime import timedelta
from typing import Any

from homeassistant.config_entries import ConfigEntry
from homeassistant.core import HomeAssistant
from homeassistant.helpers.update_coordinator import DataUpdateCoordinator, UpdateFailed

from .api import SpeakersApi, SpeakersApiError
from .const import DOMAIN, LOGGER, POLL_INTERVAL_SECONDS


class PodConnectSpeakersCoordinator(DataUpdateCoordinator[dict[str, Any]]):
    """Polls /api/state once per cycle for all entities on this speaker."""

    def __init__(
        self, hass: HomeAssistant, entry: ConfigEntry, api: SpeakersApi
    ) -> None:
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
        """Fetch the speaker state from the manager."""
        try:
            return await self.api.state()
        except SpeakersApiError as err:
            raise UpdateFailed(str(err)) from err
