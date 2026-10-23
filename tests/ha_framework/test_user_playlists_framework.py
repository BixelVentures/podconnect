"""Actual HA response-only service and OAuth client; inert loopback provider only."""

import asyncio
import time
import unittest
from types import SimpleNamespace
from unittest.mock import patch

import test_targets as fixture
from aiohttp import web
from homeassistant.core import SupportsResponse

SERVICE = "get_user_playlists"


class UserPlaylistFrameworkTests(unittest.IsolatedAsyncioTestCase):
    asyncSetUp = fixture.TargetFrameworkTests.asyncSetUp
    _cleanup = fixture.TargetFrameworkTests._cleanup
    _task = fixture.TargetFrameworkTests._task
    _writes = fixture.TargetFrameworkTests._writes
    _service = fixture.TargetFrameworkTests._service

    async def _backend(self, request):
        if request.path != "/spotify/me/playlists":
            return await fixture.TargetFrameworkTests._backend(self, request)
        self.assertEqual(request.method, "GET")
        self.assertEqual(dict(request.query), {"limit": "20", "offset": "0"})
        self.assertEqual(
            request.headers.get("Authorization"), "Bearer inert-fixture-only"
        )
        self.calls.append((request.method, request.path, None, True))
        if self.hold_path == request.path:
            self.entered.set()
            await self.release.wait()
        if getattr(self, "provider_status", 200) != 200:
            return web.Response(
                status=self.provider_status,
                text="PRIVATE provider body",
                headers={
                    "Retry-After": "1",
                    "Location": self.base + "/spotify/redirected",
                },
            )
        if getattr(self, "oversized", False):
            return web.json_response({"oversized": "x" * 262145})
        return web.json_response(
            {
                "items": [
                    {
                        "type": "playlist",
                        "uri": "spotify:playlist:" + "A" * 22,
                        "name": "Actual account playlist",
                        "images": [{"url": "https://i.scdn.co/image/real"}],
                        "owner": {"id": "PRIVATE"},
                    }
                ],
                "limit": 20,
                "offset": 0,
                "total": 1,
                "next": None,
            }
        )

    async def test_actual_registration_schema_exact_account_and_single_read(self):
        # Existing search registration must not hide a missing second read.
        from custom_components.podconnect.playlist_catalogue import (
            register_playlist_search,
        )

        registry = self.hass.services
        search = registry.async_services()["podconnect"]["search_playlists"]
        registry.async_remove("podconnect", SERVICE)
        for _ in range(2):
            register_playlist_search(
                self.hass,
                fixture.podconnect._library_owner,
                fixture.podconnect._search_query,
                fixture.podconnect._search_limit,
            )
            self.assertIs(
                registry.async_services()["podconnect"]["search_playlists"], search
            )
            current_read = registry.async_services()["podconnect"][SERVICE]
            if _ == 0:
                first_read = current_read
            else:
                self.assertIs(current_read, first_read)
        self.assertIs(
            self.hass.services.supports_response("podconnect", SERVICE),
            SupportsResponse.ONLY,
        )
        self.calls.clear()
        epoch = self.entries[1].runtime_data.coordinator.owner_epoch
        status, body = await self._service(SERVICE, {"config_entry_id": "account-beta"})
        self.assertEqual(status, 200)
        reply = body["service_response"]
        self.assertEqual(
            (reply["config_entry_id"], reply["owner_epoch"], reply["limit"]),
            ("account-beta", epoch, 20),
        )
        self.assertEqual(reply["schema"], "podconnect.user_playlists.v1")
        self.assertEqual(self.calls, [("GET", "/spotify/me/playlists", None, True)])
        self.assertNotIn("PRIVATE", str(reply))
        self.assertEqual(self._writes(), [])
        for invalid in (
            {},
            {"config_entry_id": "account-alpha", "limit": True},
            {"config_entry_id": "account-alpha", "limit": 21},
            {"config_entry_id": "account-alpha", "offset": 1},
        ):
            self.assertEqual((await self._service(SERVICE, invalid))[0], 400)
        self.assertEqual(
            (
                await self._service(
                    SERVICE, {"config_entry_id": "account-alpha"}, response=False
                )
            )[0],
            400,
        )
        self.assertEqual(len(self.calls), 1)

    async def test_owner_changes_during_oauth_and_http_do_not_escape(self):
        owner = self.entries[0].runtime_data
        original_epoch = owner.coordinator.owner_epoch

        async def replace_after_oauth():
            owner.coordinator.owner_epoch = "new-oauth-owner"

        self.calls.clear()
        with patch.object(
            owner.api._session, "async_ensure_token_valid", replace_after_oauth
        ):
            self.assertNotEqual(
                (await self._service(SERVICE, {"config_entry_id": "account-alpha"}))[0],
                200,
            )
        self.assertEqual(self.calls, [])
        owner.coordinator.owner_epoch = original_epoch
        self.hold_path = "/spotify/me/playlists"
        pending = self._task(
            self._service(SERVICE, {"config_entry_id": "account-alpha"})
        )
        await self.entered.wait()
        owner.coordinator.owner_epoch = "new-http-owner"
        self.release.set()
        self.assertNotEqual((await pending)[0], 200)
        self.assertEqual(len(self.calls), 1)
        self.assertEqual(self._writes(), [])

    async def test_runtime_and_account_binding_changes_after_read_reject(self):
        entry = self.entries[0]
        owner = entry.runtime_data
        for change in ("runtime", "account"):
            self.entered, self.release = asyncio.Event(), asyncio.Event()
            self.holds.append(self.release)
            self.hold_path = "/spotify/me/playlists"
            pending = self._task(
                self._service(SERVICE, {"config_entry_id": entry.entry_id})
            )
            await self.entered.wait()
            identity = entry.unique_id
            try:
                if change == "runtime":
                    entry.runtime_data = SimpleNamespace(active=True)
                else:
                    self.hass.config_entries.async_update_entry(
                        entry, unique_id="replacement-account"
                    )
                self.release.set()
                self.assertNotEqual((await pending)[0], 200)
            finally:
                entry.runtime_data = owner
                self.hass.config_entries.async_update_entry(entry, unique_id=identity)
        self.assertEqual(self._writes(), [])

    async def test_errors_size_and_rate_limit_are_single_sanitized_failure(self):
        for status in (302, 403, 429, 500):
            self.calls.clear()
            self.provider_status = status
            code, body = await self._service(
                SERVICE, {"config_entry_id": "account-alpha"}
            )
            self.assertNotEqual(code, 200)
            self.assertNotIn("PRIVATE", str(body))
            self.assertEqual(len(self.calls), 1)
        self.provider_status = 200
        self.oversized = True
        self.calls.clear()
        self.assertNotEqual(
            (await self._service(SERVICE, {"config_entry_id": "account-alpha"}))[0], 200
        )
        self.assertEqual(len(self.calls), 1)
        self.assertEqual(self._writes(), [])

    async def test_actual_eight_second_total_budget_and_cancellation(self):
        self.hold_path = "/spotify/me/playlists"
        self.calls.clear()
        began = time.monotonic()
        code, _ = await self._service(SERVICE, {"config_entry_id": "account-alpha"})
        elapsed = time.monotonic() - began
        self.assertNotEqual(code, 200)
        self.assertGreaterEqual(elapsed, 7.8)
        self.assertLess(elapsed, 10)
        self.assertEqual(len(self.calls), 1)
        # A cancelled OAuth wait is joined by this caller, never detached/retried.
        owner = self.entries[0].runtime_data
        entered, joined = asyncio.Event(), asyncio.Event()

        async def held_oauth():
            entered.set()
            try:
                await asyncio.Event().wait()
            finally:
                joined.set()

        with patch.object(owner.api._session, "async_ensure_token_valid", held_oauth):
            pending = self._task(
                self.hass.services.async_call(
                    "podconnect",
                    SERVICE,
                    {"config_entry_id": "account-alpha"},
                    blocking=True,
                    return_response=True,
                )
            )
            await entered.wait()
            pending.cancel()
            with self.assertRaises(asyncio.CancelledError):
                await pending
        self.assertTrue(joined.is_set())
        self.assertEqual(len(self.calls), 1)
        self.assertEqual(self._writes(), [])
