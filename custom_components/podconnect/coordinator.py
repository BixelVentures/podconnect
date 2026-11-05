"""Data update coordinator for PodConnect — polls Spotify playback + devices."""

from __future__ import annotations

import asyncio
import hashlib
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
        self._failed_poll_ref = None

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
        poll_owner_epoch = getattr(self, "owner_epoch", None)

        async def read_playback():
            playback = await self.api.playback_state()
            self._check_command_owner()
            return playback, dt_util.utcnow(), time.monotonic()

        reads = (
            asyncio.create_task(read_playback()),
            asyncio.create_task(self.api.devices()),
        )

        def diagnose(outcome):
            # Introspect the same completed tasks only; no provider/body/error
            # text crosses this content-free best-effort diagnostic boundary.
            try:
                if (
                    not isinstance(poll_owner_epoch, str)
                    or not 1 <= len(poll_owner_epoch) <= 160
                ):
                    return
                poll_ref = hashlib.sha256(
                    f"{poll_owner_epoch}:{poll_sequence}".encode()
                ).hexdigest()[:16]
                if outcome == "success":
                    previous = getattr(self, "_failed_poll_ref", None)
                    if previous is not None:
                        self._failed_poll_ref = None
                        LOGGER.info(
                            "playback_poll event=recovered poll_ref=%s previous_ref=%s",
                            poll_ref,
                            previous,
                        )
                    return
                rows = []
                for read in reads:
                    state, kind, status = "pending", "none", 0
                    if read.cancelled():
                        state = "cancelled"
                    elif read.done():
                        error = read.exception()
                        state = "success" if error is None else "error"
                        if isinstance(error, SpotifyApiError):
                            kind = "spotify_api"
                            value = getattr(error, "status", None)
                            status = (
                                value
                                if type(value) is int and 100 <= value <= 599
                                else 0
                            )
                        elif isinstance(error, UpdateFailed):
                            kind = "owner_changed"
                        elif isinstance(error, TimeoutError):
                            kind = "timeout"
                        elif isinstance(error, OSError):
                            kind = "transport"
                        elif error is not None:
                            kind = "unexpected"
                    rows.extend((state, kind, status))
                self._failed_poll_ref = poll_ref
                LOGGER.warning(
                    "playback_poll event=failed poll_ref=%s kind=%s playback=%s playback_kind=%s playback_status=%s devices=%s devices_kind=%s devices_status=%s",
                    poll_ref,
                    outcome,
                    *rows,
                )
            except (Exception, asyncio.CancelledError):  # noqa: BLE001
                return  # Hash/status/log sinks cannot alter source or cancellation.

        outcome = "success"
        try:
            playback_result, devices = await asyncio.gather(*reads)
            self._check_command_owner()
            playback, playback_observed_at, playback_observed_monotonic = (
                playback_result
            )
        except SpotifyApiError as err:
            outcome = "spotify_api"
            raise UpdateFailed(str(err)) from err
        except asyncio.CancelledError:
            outcome = "outer_cancelled"
            raise
        except UpdateFailed:
            outcome = "owner_changed"
            raise
        except Exception:
            outcome = "unexpected"
            raise
        finally:
            for read in reads:
                if not read.done():
                    read.cancel()
            try:
                await asyncio.gather(*reads, return_exceptions=True)
            except asyncio.CancelledError:
                outcome = "outer_cancelled"
                raise
            except Exception:
                outcome = "unexpected"
                raise
            finally:
                if outcome != "success":
                    diagnose(outcome)
        try:
            self._check_command_owner()
        except asyncio.CancelledError:
            diagnose("outer_cancelled")
            raise
        except UpdateFailed:
            diagnose("owner_changed")
            raise
        except Exception:
            diagnose("unexpected")
            raise
        result = {
            "poll_sequence": poll_sequence,
            "playback": playback,
            "playback_observed_at": playback_observed_at,
            "playback_observed_monotonic": playback_observed_monotonic,
            "devices": devices,
            "fetched_at": dt_util.utcnow(),
        }
        diagnose("success")
        return result
