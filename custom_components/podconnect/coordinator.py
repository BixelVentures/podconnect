"""Data update coordinator for PodConnect — polls Spotify playback + devices."""

from __future__ import annotations

import asyncio
import hashlib
import secrets
import time
from datetime import timedelta
from typing import Any

from homeassistant.core import HomeAssistant, callback
from homeassistant.helpers.event import async_call_later
from homeassistant.helpers.update_coordinator import DataUpdateCoordinator, UpdateFailed
from homeassistant.util import dt as dt_util

from .api import SpotifyApi, SpotifyApiError
from .const import (
    ACTIVE_POLL_INTERVAL_SECONDS,
    DOMAIN,
    EVENT_PLAYBACK_CHANGED,
    LOGGER,
    POLL_INTERVAL_SECONDS,
)
from .speakers import SpeakersError


class PodConnectCoordinator(DataUpdateCoordinator[dict[str, Any]]):
    """Polls /me/player and /me/player/devices once per cycle for all entities."""

    _NATIVE_ENGINE_RECHECK_SECONDS = 60

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
        self._playback_key = None
        self._followup_unsubs = []
        self._native_client = None
        self._native_watch_scope = None
        self._native_watch_task = None
        self._native_refresh_task = None
        self._native_pending = None
        self._native_last_started = 0.0
        self._native_last_completed = 0.0

    async def _async_refresh(self, *args, native_scope=None, **kwargs):
        completed_before = self._native_last_completed
        async with self._poll_lock:
            if (
                kwargs.get("scheduled")
                and completed_before != self._native_last_completed
            ):
                return  # The demand read already advanced and rescheduled this poll.
            if native_scope is not None:
                if (
                    native_scope != self._native_watch_scope
                    or native_scope != self._native_pending
                    or self._native_refresh_task is not asyncio.current_task()
                ):
                    return
                if (
                    self.command_owner_guard is not None
                    and not self.command_owner_guard()
                ):
                    self._native_pending = None
                    return
                self._native_last_started = self.hass.loop.time()
            # Any read beginning now covers hints received before this point. A hint
            # during collection can request one successor behind the same lock.
            self._native_pending = None
            await super()._async_refresh(*args, **kwargs)
            if native_scope is not None:
                self._native_last_completed = self._native_last_started
            self._publish_playback_change()
            self._sync_native_watch()

    def set_native_playback_client(self, client, *, restore_poll=False):
        self._native_client = client
        self._sync_native_watch()
        # A failed platform unload can resume after cancellation of a demand read
        # whose HA coordinator had already cleared the old fallback timer.
        if (
            restore_poll
            and self._listeners
            and self._unsub_refresh is None
            and not self._poll_lock.locked()
        ):
            self._schedule_refresh()

    async def async_stop_native_playback(self):
        tasks = tuple(
            task
            for task in (self._native_watch_task, self._native_refresh_task)
            if task
        )
        self.set_native_playback_client(None)
        if tasks:
            await asyncio.gather(*tasks, return_exceptions=True)

    def _sync_native_watch(self):
        scope = None
        entry = self.config_entry
        if (
            self._native_client is not None
            and entry is not None
            and not entry.pref_disable_polling
            and not self.hass.is_stopping
            and self.last_update_success
            and isinstance(self.data, dict)
        ):
            playback = self.data.get("playback")
            if (
                isinstance(playback, dict)
                and type(playback.get("is_playing")) is bool
                and isinstance(playback.get("item"), dict)
            ):
                device = playback.get("device")
                target = device.get("id") if isinstance(device, dict) else None
                account = entry.unique_id
                if (
                    isinstance(account, str)
                    and 1 <= len(account) <= 160
                    and isinstance(target, str)
                    and len(target) == 40
                    and all(char in "0123456789abcdef" for char in target)
                ):
                    scope = (account, target)
        if scope == self._native_watch_scope:
            return
        self._native_watch_scope = scope
        self._native_pending = None
        for task in (self._native_watch_task, self._native_refresh_task):
            if task is not None and task is not asyncio.current_task():
                task.cancel()
        self._native_watch_task = self._native_refresh_task = None
        if scope is not None:
            self._native_watch_task = self.hass.async_create_background_task(
                self._watch_native_playback(self._native_client, scope),
                "podconnect native playback revision",
                eager_start=False,
            )

    async def _watch_native_playback(self, client, scope):
        account, target = scope
        cursor = "seed"
        delay = 1
        engine_checked = False
        while self._native_watch_task is asyncio.current_task():
            try:
                if not engine_checked:
                    if await client.start_identity() != target:
                        await asyncio.sleep(self._NATIVE_ENGINE_RECHECK_SECONDS)
                        continue
                    engine_checked = True
                reply = await client.wait_playback_revision(account, target, cursor)
            except SpeakersError:
                # Older/unavailable Speakers keep normal Spotify polling in charge.
                engine_checked = False
                await asyncio.sleep(delay)
                delay = min(60, delay * 2)
                continue
            delay = 1
            if reply is None:
                # Real quiet responses take 20s; cap retries for a broken fast server.
                await asyncio.sleep(0.25)
                continue
            next_cursor, changed = reply
            if self._native_watch_task is not asyncio.current_task():
                return
            generation, _, revision = cursor.partition(":")
            next_generation, next_revision = next_cursor.split(":")
            if not changed:
                if next_generation != generation:
                    if cursor != "seed":
                        await asyncio.sleep(0.25)
                    cursor = next_cursor
                else:
                    await asyncio.sleep(0.25)
                continue
            if next_generation != generation or int(next_revision) <= int(revision):
                await asyncio.sleep(0.25)
                continue
            cursor = next_cursor
            self._native_pending = scope
            if self._native_refresh_task is None or self._native_refresh_task.done():
                self._native_refresh_task = self.hass.async_create_background_task(
                    self._refresh_native_playback(scope),
                    "podconnect native playback observation",
                    eager_start=False,
                )
            # A fast stream can coalesce later changes without a hot request loop.
            await asyncio.sleep(0.25)

    async def _refresh_native_playback(self, scope):
        while (
            self._native_pending == scope
            and self._native_refresh_task is asyncio.current_task()
        ):
            delay = self._native_last_started + 0.75 - self.hass.loop.time()
            if delay > 0:
                await asyncio.sleep(delay)
            await self._async_refresh(native_scope=scope)

    def _publish_playback_change(self) -> None:
        """Fire one HA event per real playback change, after the data is published.

        Dashboards (PodView) refresh on this event instead of polling. While music is
        playing, Spotify is read every few seconds so external changes show up fast.
        """
        try:
            data = self.data if isinstance(self.data, dict) else {}
            playback = (
                data.get("playback") if isinstance(data.get("playback"), dict) else {}
            )
            item = (
                playback.get("item") if isinstance(playback.get("item"), dict) else {}
            )
            device = (
                playback.get("device")
                if isinstance(playback.get("device"), dict)
                else {}
            )
            # Spotify's state-change timestamp also changes for a same-item seek.
            # Routine progress updates alone must never fire an invalidation.
            changed_at = playback.get("timestamp")
            key = (
                bool(playback.get("is_playing")),
                item.get("uri"),
                device.get("id"),
                device.get("volume_percent"),
                playback.get("shuffle_state"),
                playback.get("repeat_state"),
                changed_at if type(changed_at) is int and changed_at > 0 else None,
            )
            self.update_interval = timedelta(
                seconds=ACTIVE_POLL_INTERVAL_SECONDS
                if playback.get("is_playing")
                else POLL_INTERVAL_SECONDS
            )
            if key == self._playback_key:
                return
            previous, self._playback_key = self._playback_key, key
            if previous is not None and self.config_entry is not None:
                self.hass.bus.async_fire(
                    EVENT_PLAYBACK_CHANGED,
                    {"config_entry_id": self.config_entry.entry_id},
                )
        except Exception:  # noqa: BLE001
            return  # Notification only; never affects the observation itself.

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
            self._schedule_followup_refreshes()
        try:
            async with asyncio.timeout(0.2):
                await asyncio.shield(self._command_refresh_task)
        except TimeoutError:
            return

    # Spotify's Web API often still reports the pre-command state right after a
    # command. Two short follow-up reads make the screen settle in ~1-3 s
    # instead of waiting for the next 10 s scheduled poll.
    _FOLLOWUP_DELAYS_S = (1.2, 3.0)

    def _schedule_followup_refreshes(self) -> None:
        for unsub in self._followup_unsubs:
            unsub()
        self._followup_unsubs = []

        @callback
        def fire(_now) -> None:
            if self.hass.is_stopping:
                return
            self.hass.async_create_background_task(
                self._followup_refresh(),
                "podconnect post-command follow-up observation",
                eager_start=False,
            )

        for delay in self._FOLLOWUP_DELAYS_S:
            self._followup_unsubs.append(async_call_later(self.hass, delay, fire))

    async def _followup_refresh(self) -> None:
        try:
            async with asyncio.timeout(4):
                self._check_command_owner()
                await self.async_refresh()
        except (TimeoutError, UpdateFailed):
            return

    async def async_shutdown(self) -> None:
        await self.async_stop_native_playback()
        for unsub in self._followup_unsubs:
            unsub()
        self._followup_unsubs = []
        await super().async_shutdown()

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
