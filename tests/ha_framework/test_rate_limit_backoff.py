"""Spotify 429 backoff against real HA owners; Spotify is inert loopback HTTP.

Green 2026-11-20: the playback poll kept calling Spotify every ~3 s while it
answered 429, and a 429 during setup left the entry in setup_retry for hours.
"""

from __future__ import annotations

import importlib
import logging
import unittest
from unittest.mock import patch

from aiohttp import web
from homeassistant.config_entries import ConfigEntryState
from homeassistant.exceptions import HomeAssistantError
from homeassistant.helpers import entity_registry as er

import test_targets as fixture

spotify_api = importlib.import_module("custom_components.podconnect.api")
room_player = importlib.import_module("custom_components.podconnect.room_player")
LOGGER_NAME = "custom_components.podconnect"


class RateLimitFixture(unittest.IsolatedAsyncioTestCase):
    engine_identity = fixture.TargetFrameworkTests.engine_identity
    _cleanup = fixture.TargetFrameworkTests._cleanup
    _task = fixture.TargetFrameworkTests._task
    rate_limited_at_setup = False

    async def asyncSetUp(self):
        self.now = 1000.0
        self.limit_paths = None  # None: every /spotify/ path while limited.
        self.retry_after = "120"
        self.limited = self.rate_limited_at_setup
        self.spotify_calls = 0
        self.playback_503 = False
        test = self
        for name, value in (
            ("_clock", lambda self: test.now),
            ("_jitter", lambda self: 0.0),
        ):
            patcher = patch.object(spotify_api.SpotifyApi, name, value, create=True)
            patcher.start()
            self.addCleanup(patcher.stop)
        await fixture.TargetFrameworkTests.asyncSetUp(self)
        self.calls.clear()
        self.spotify_calls = 0

    async def _backend(self, request):
        if request.path.startswith("/spotify/"):
            self.spotify_calls += 1
            if self.playback_503 and request.path == "/spotify/me/player":
                return web.Response(status=503, text="down")
            if self.limited and (
                self.limit_paths is None or request.path in self.limit_paths
            ):
                headers = (
                    {} if self.retry_after is None else {"Retry-After": self.retry_after}
                )
                return web.Response(status=429, text="slow down", headers=headers)
            if request.path == "/spotify/me/top/tracks":
                return web.json_response({"items": []})
        return await fixture.TargetFrameworkTests._backend(self, request)

    def api(self, index=0):
        return self.entries[index].runtime_data.api

    def coordinator(self, index=0):
        return self.entries[index].runtime_data.coordinator

    async def service(self, name, **data):
        return await self.hass.services.async_call(
            "podconnect",
            name,
            {"config_entry_id": "account-alpha", **data},
            blocking=True,
            return_response=True,
        )

    async def assert_spotify_services_fail_fast(self):
        before = self.spotify_calls
        for name, data in (
            ("top_tracks", {}),
            ("search_tracks", {"query": "jazz"}),
            ("top_artists", {}),
        ):
            with self.subTest(service=name):
                with self.assertRaisesRegex(HomeAssistantError, "rate limited"):
                    await self.service(name, **data)
        self.assertEqual(
            (await self.service("get_playback_snapshot"))["error_kind"],
            "rate_limited",
        )
        self.assertEqual(
            (await self.service("get_queue"))["error_kind"], "rate_limited"
        )
        # Commands never reach Spotify either; nothing is sent, so nothing is unknown.
        with self.assertRaises(spotify_api.SpotifyApiError) as caught:
            await self.api().pause("a" * 40)
        self.assertEqual(caught.exception.status, 429)
        self.assertEqual(self.spotify_calls, before)


class RateLimitedPollTests(RateLimitFixture):
    async def test_poll_under_429_makes_no_spotify_call_until_deadline(self):
        coordinator = self.coordinator()
        await coordinator.async_refresh()
        self.assertTrue(coordinator.last_update_success)
        last_known = coordinator.data
        self.limited = True
        with self.assertLogs(LOGGER_NAME, logging.WARNING) as logs:
            await coordinator.async_refresh()
            first = self.spotify_calls
            self.assertGreaterEqual(first, 1)
            self.assertFalse(coordinator.last_update_success)
            # Many ticks (scheduled 3 s polls, follow-ups) inside the backoff.
            for step in range(40):
                self.now += 2.9
                await coordinator.async_refresh()
            self.assertEqual(self.spotify_calls, first)
            # Last-known state is kept and marked as rate limited (stale).
            self.assertIs(coordinator.data, last_known)
            self.assertTrue(coordinator.rate_limited)
            await self.assert_spotify_services_fail_fast()
            self.now = 1000.0 + 119
            await coordinator.async_refresh()
            self.assertEqual(self.spotify_calls, first)
            # Spotify's deadline passes: polling resumes and succeeds.
            self.limited = False
            self.now = 1000.0 + 121
            await coordinator.async_refresh()
            self.assertGreater(self.spotify_calls, first)
            self.assertTrue(coordinator.last_update_success)
            self.assertFalse(coordinator.rate_limited)
        lines = [record.getMessage() for record in logs.records]
        started = [line for line in lines if "spotify_rate_limit event=started" in line]
        ended = [line for line in lines if "spotify_rate_limit event=ended" in line]
        self.assertEqual(len(started), 1, lines)
        self.assertIn("retry_after_s=120", started[0])
        self.assertEqual(len(ended), 1, lines)
        self.assertLessEqual(
            len([line for line in lines if "playback_poll event=failed" in line]), 1
        )
        self.assertNotIn("inert-fixture-only", "\n".join(lines))
        # The other account has its own Spotify owner and keeps polling.
        before = self.spotify_calls
        self.limited = False
        await self.coordinator(1).async_refresh()
        self.assertGreater(self.spotify_calls, before)

    async def test_backoff_blocks_while_it_holds_even_for_other_reads(self):
        self.limited = True
        self.limit_paths = {"/spotify/me/player/queue"}
        self.assertEqual(
            (await self.service("get_queue"))["error_kind"], "rate_limited"
        )
        calls = self.spotify_calls
        # A 429 on one read stops every Spotify request for the account.
        await self.coordinator().async_refresh()
        self.assertEqual(self.spotify_calls, calls)
        self.assertFalse(self.coordinator().last_update_success)

    async def test_missing_retry_after_backs_off_exponentially_to_fifteen_minutes(self):
        self.limited = True
        self.retry_after = None
        coordinator = self.coordinator()
        waits = []
        for _ in range(7):
            await coordinator.async_refresh()
            remaining = self.api().rate_limit_remaining()
            waits.append(round(remaining))
            calls = self.spotify_calls
            self.now += remaining - 1
            await coordinator.async_refresh()
            self.assertEqual(self.spotify_calls, calls)
            self.now += 1.01
        self.assertEqual(waits, [30, 60, 120, 240, 480, 900, 900])
        self.limited = False
        await coordinator.async_refresh()
        self.assertTrue(coordinator.last_update_success)
        # Success resets the fallback.
        self.limited = True
        await coordinator.async_refresh()
        self.assertEqual(round(self.api().rate_limit_remaining()), 30)


class RateLimitedSetupTests(RateLimitFixture):
    rate_limited_at_setup = True

    async def test_setup_with_429_loads_entry_services_and_rooms(self):
        # asyncSetUp already asserted both entries are LOADED and active.
        for entry in self.entries:
            self.assertIs(entry.state, ConfigEntryState.LOADED)
            self.assertFalse(entry.runtime_data.coordinator.last_update_success)
            self.assertTrue(entry.runtime_data.coordinator.rate_limited)
        for name in (
            "get_playback_snapshot",
            "control_playback",
            "top_tracks",
            "search_tracks",
            "get_targets",
            "control_room_playback",
        ):
            self.assertTrue(self.hass.services.has_service("podconnect", name), name)
        # Local Speakers rooms do not depend on Spotify and stay usable.
        registry = er.async_get(self.hass)
        rooms = [
            item
            for item in er.async_entries_for_config_entry(registry, "account-alpha")
            if item.unique_id.startswith(room_player.room_unique_id("account-alpha", ""))
        ]
        self.assertTrue(rooms)
        for item in rooms:
            self.assertNotEqual(self.hass.states.get(item.entity_id).state, "unavailable")
        await self.assert_spotify_services_fail_fast()

    async def test_setup_with_429_recovers_after_deadline(self):
        coordinator = self.coordinator()
        await coordinator.async_refresh()
        self.assertEqual(self.spotify_calls, 0)
        self.now += 121
        self.limited = False
        await coordinator.async_refresh()
        self.assertTrue(coordinator.last_update_success)
        self.assertGreater(self.spotify_calls, 0)
        self.assertEqual(
            (await self.service("get_playback_snapshot"))["ok"], True
        )
        result = await self.service("top_tracks")
        self.assertEqual(result["config_entry_id"], "account-alpha")

    async def test_setup_error_other_than_rate_limit_still_retries(self):
        entry = self.entries[0]
        self.assertTrue(await self.hass.config_entries.async_unload(entry.entry_id))
        self.now += 121
        self.limited = False
        self.playback_503 = True
        await self.hass.config_entries.async_setup(entry.entry_id)
        await self.hass.async_block_till_done()
        self.assertIs(entry.state, ConfigEntryState.SETUP_RETRY)
