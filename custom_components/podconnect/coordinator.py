"""Data update coordinator for PodConnect — polls Spotify playback + devices."""

from __future__ import annotations

import asyncio
import secrets
import time
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
        self.poll_sequence = 0
        self.owner_epoch = secrets.token_hex(16)
        # Covers collection AND HA publication for scheduled/manual/demand polls.
        self._poll_lock = asyncio.Lock()
        self._command_refresh_task = None
        self.command_owner_guard = None

    async def _async_refresh(self, *args, **kwargs):
        async with self._poll_lock:
            await super()._async_refresh(*args, **kwargs)

    def _check_command_owner(self):
        if self.command_owner_guard is not None and not self.command_owner_guard():
            raise UpdateFailed("Observation owner replaced")

    async def async_refresh_after_command(self):
        """One bounded shared read; caller cancellation cannot cancel its owner."""
        if self._command_refresh_task is None or self._command_refresh_task.done():

            async def refresh():
                try:
                    async with asyncio.timeout(1):
                        self._check_command_owner()
                        await self.async_refresh()
                except (TimeoutError, UpdateFailed):
                    return

            self._command_refresh_task = self.hass.async_create_background_task(
                refresh(), "podconnect post-command observation", eager_start=False
            )
        try:
            async with asyncio.timeout(0.2):
                await asyncio.shield(self._command_refresh_task)
        except TimeoutError:
            return

    async def _async_update_data(self) -> dict[str, Any]:
        """Fetch playback state + the device list."""
        self._check_command_owner()
        self.poll_sequence += 1
        poll_sequence = self.poll_sequence

        async def read_playback():
            playback = await self.api.playback_state()
            self._check_command_owner()
            return playback, dt_util.utcnow(), time.monotonic()

        reads = (
            asyncio.create_task(read_playback()),
            asyncio.create_task(self.api.devices()),
        )
        try:
            playback_result, devices = await asyncio.gather(*reads)
            self._check_command_owner()
            playback, playback_observed_at, playback_observed_monotonic = (
                playback_result
            )
        except SpotifyApiError as err:
            raise UpdateFailed(str(err)) from err
        finally:
            for read in reads:
                if not read.done():
                    read.cancel()
            await asyncio.gather(*reads, return_exceptions=True)
        self._check_command_owner()
        return {
            "poll_sequence": poll_sequence,
            "playback": playback,
            "playback_observed_at": playback_observed_at,
            "playback_observed_monotonic": playback_observed_monotonic,
            "devices": devices,
            "fetched_at": dt_util.utcnow(),
        }
