"""Actual HA setup, registration and bounded native account diagnostic client."""
import asyncio
import hashlib
import time
import unittest

from aiohttp import web
from homeassistant.core import SupportsResponse
from homeassistant.exceptions import HomeAssistantError
import test_targets as fixture

ENGINE = "a" * 40
SERVICE = "get_account_identifier_diagnostic"


class AccountDiagnosticFrameworkTests(unittest.IsolatedAsyncioTestCase):
    asyncSetUp = fixture.TargetFrameworkTests.asyncSetUp
    _cleanup = fixture.TargetFrameworkTests._cleanup
    _task = fixture.TargetFrameworkTests._task
    _writes = fixture.TargetFrameworkTests._writes
    _service = fixture.TargetFrameworkTests._service

    async def _backend(self, request):
        if request.path != "/api/start-volume/identity":
            return await fixture.TargetFrameworkTests._backend(self, request)
        self.assertEqual(request.method, "GET")
        self.assertEqual(dict(request.query), {"account": "1"})
        self.assertNotIn("Authorization", request.headers)
        self.calls.append((request.method, request.path, None, False))
        if self.hold_path == request.path:
            self.entered.set()
            await self.release.wait()
        now = time.time_ns() // 1_000_000
        row = {"schema": "podconnect.start_identity.v1", "engine_device_id": ENGINE,
               "account_observation": {"schema": "podconnect.native_account_observation.v1",
                    "method": "sha256", "domain": "spotify.ap.canonical_username",
                    "identifier_sha256": hashlib.sha256(b"account-alpha").hexdigest(),
                    "engine_device_id": ENGINE, "run_generation": "1", "connection_generation": "2",
                    "observation_sequence": "3", "observed_at_unix_ms": now - 1,
                    "expires_at_unix_ms": now + 4999}}
        if getattr(self, "foreign_diagnostic", False):
            row["account_observation"]["engine_device_id"] = "b" * 40
        return web.json_response(row)

    def args(self):
        return {"config_entry_id": "account-alpha", "engine_device_id": ENGINE}

    async def test_actual_setup_registration_schema_and_single_local_read(self):
        self.assertIs(self.hass.services.supports_response("podconnect", SERVICE), SupportsResponse.ONLY)
        count = len(self.calls)
        status, body = await self._service(SERVICE, self.args())
        self.assertEqual(status, 200)
        self.assertEqual(body["service_response"], {"comparison": "identifiers_equal", "identity_relation": "unknown"})
        self.assertEqual(self.calls[count:], [("GET", "/api/start-volume/identity", None, False)])
        self.assertEqual(self._writes(), [])
        for invalid in ({}, {"config_entry_id": "account-alpha"}, {**self.args(), "engine_device_id": "bad"}, {**self.args(), "extra": True}):
            self.assertEqual((await self._service(SERVICE, invalid))[0], 400)
        self.assertEqual((await self._service(SERVICE, self.args(), response=False))[0], 400)

    async def test_actual_entry_owner_replacement_while_native_read_rejects(self):
        self.hold_path = "/api/start-volume/identity"
        task = self._task(self.hass.services.async_call("podconnect", SERVICE, self.args(), blocking=True, return_response=True))
        await self.entered.wait()
        self.entries[0].runtime_data.active = False
        self.release.set()
        with self.assertRaises(HomeAssistantError):
            await task
        self.assertEqual(self._writes(), [])

    async def test_foreign_engine_is_unknown_without_authority(self):
        self.foreign_diagnostic = True
        status, body = await self._service(SERVICE, self.args())
        self.assertEqual(status, 200)
        self.assertEqual(body["service_response"], {"comparison": "unknown", "identity_relation": "unknown"})
        self.assertEqual(self._writes(), [])
