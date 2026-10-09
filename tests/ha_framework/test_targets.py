"""Real HA 2026.8.2 owners; Spotify and Speakers are inert loopback HTTP.

Run in the pinned HA image. This directory is intentionally outside the
dependency-free boundary-test discovery root. No provider or speaker proof.
"""

from __future__ import annotations

import asyncio
from contextlib import AsyncExitStack
import importlib
from pathlib import Path
import sys
from tempfile import TemporaryDirectory
import time
from types import MappingProxyType
import unittest
from unittest.mock import patch

from aiohttp import ClientConnectionError, ClientSession, web
from aiohttp.test_utils import TestClient, TestServer
from zeroconf import IPVersion

from homeassistant import auth, bootstrap, loader
from homeassistant.components.api import APIDomainServicesView, APIServicesView
from homeassistant.components.http import HomeAssistantHTTP
from homeassistant.components.zeroconf.const import DATA_INSTANCE
from homeassistant.components.zeroconf.models import HaAsyncZeroconf, HaZeroconf
from homeassistant.config_entries import (
    ConfigEntries, ConfigEntry, ConfigEntryState, SIGNAL_CONFIG_ENTRY_CHANGED,
)
from homeassistant.core import HomeAssistant, SupportsResponse, callback
from homeassistant.exceptions import HomeAssistantError
from homeassistant.helpers import entity_registry
from homeassistant.helpers.dispatcher import async_dispatcher_connect
from homeassistant.helpers.config_entry_oauth2_flow import (
    LocalOAuth2Implementation,
    async_register_implementation,
)


ROOT = Path(__file__).resolve().parents[2]
sys.path.insert(0, str(ROOT))
# Normal complete-module imports, not extracted handlers or a fake HA package.
podconnect = importlib.import_module("custom_components.podconnect")
spotify_api = importlib.import_module("custom_components.podconnect.api")
from custom_components.podconnect.const import DOMAIN, SPOTIFY_SCOPES  # noqa: E402


class TargetFrameworkTests(unittest.IsolatedAsyncioTestCase):
    async def asyncSetUp(self):
        self.stack = AsyncExitStack()
        await self.stack.__aenter__()
        self.addAsyncCleanup(self._cleanup)
        self.temp = TemporaryDirectory()
        self.stack.callback(self.temp.cleanup)
        self.tasks = []
        self.holds = []
        self.calls = []
        self.hold_path = None
        self.hold_post = False
        self.entered = asyncio.Event()
        self.release = asyncio.Event()
        self.holds.append(self.release)
        self.move_finished = asyncio.Event()
        self.hass = None
        self.loop_errors = []
        loop = asyncio.get_running_loop()
        original_handler = loop.get_exception_handler()

        def observe_error(current_loop, context):
            self.loop_errors.append(type(context.get("exception")).__name__)
            if original_handler is not None:
                original_handler(current_loop, context)
            else:
                current_loop.default_exception_handler(context)

        loop.set_exception_handler(observe_error)
        self.stack.callback(loop.set_exception_handler, original_handler)
        async with asyncio.timeout(30):
            app = web.Application()
            app.router.add_route("*", "/{path:.*}", self._backend)
            self.backend = TestServer(app, host="127.0.0.1")
            await self.backend.start_server()
            self.stack.push_async_callback(self.backend.close)
            self.base = str(self.backend.make_url("")).rstrip("/")
            self.stack.enter_context(patch.object(spotify_api, "SPOTIFY_API", self.base + "/spotify"))

            (Path(self.temp.name) / "custom_components").symlink_to(ROOT / "custom_components", target_is_directory=True)
            self.hass = HomeAssistant(self.temp.name)
            self.hass.config.skip_pip = True
            loader.async_setup(self.hass)
            self.hass.config_entries = ConfigEntries(self.hass, {})
            self.assertTrue(await bootstrap.async_load_base_functionality(self.hass))
            # The network-none fixture has no multicast-capable interface.
            # Keep HA's real shared DNS/client owners, with real unicast zeroconf
            # explicitly bound to loopback; no discovery/service registration.
            fixture_zc = HaZeroconf(interfaces=["127.0.0.1"], unicast=True, ip_version=IPVersion.V4Only)
            fixture_async_zc = HaAsyncZeroconf(zc=fixture_zc)
            self.hass.data[DATA_INSTANCE] = fixture_async_zc
            self.stack.push_async_callback(fixture_async_zc.ha_async_close)
            self.hass.auth = await auth.auth_manager_from_config(self.hass, [], [])
            user = await self.hass.auth.async_create_user("Framework fixture")
            refresh = await self.hass.auth.async_create_refresh_token(user, client_id=self.base)
            access = self.hass.auth.async_create_access_token(refresh)
            self.hass.http = HomeAssistantHTTP(
                self.hass, None, None, None, ["127.0.0.1"], 0, [], "modern"
            )
            await self.hass.http.async_initialize(
                cors_origins=[], use_x_forwarded_for=False, login_threshold=-1,
                is_ban_enabled=False, use_x_frame_options=True,
            )
            async_register_implementation(
                self.hass, DOMAIN,
                LocalOAuth2Implementation(
                    self.hass, DOMAIN, "fixture", "fixture",
                    self.base + "/oauth/authorize", self.base + "/oauth/token",
                ),
            )
            self.entries = []
            for ident, title in (("account-alpha", "Account Alpha"), ("account-beta", "Account Beta")):
                entry = ConfigEntry(
                    domain=DOMAIN, entry_id=ident, version=1, minor_version=1,
                    source="user", title=title, unique_id=ident,
                    discovery_keys=MappingProxyType({}), subentries_data=None,
                    pref_disable_polling=True,
                    options={"speakers_url": self.base},
                    data={"auth_implementation": DOMAIN, "token": {
                        "access_token": "inert-fixture-only", "refresh_token": "inert-fixture-only",
                        "expires_at": time.time() + 86400,
                        "scope": " ".join(SPOTIFY_SCOPES),
                    }},
                )
                await self.hass.config_entries.async_add(entry)
                self.assertIs(entry.state, ConfigEntryState.LOADED)
                self.assertTrue(entry.runtime_data.active)
                self.entries.append(entry)
            await self.hass.async_block_till_done()
            # Actual platform dependency setup can replace hass.http. Register
            # the real API views on the final current app, before it is frozen.
            self.hass.http.register_view(APIServicesView)
            self.hass.http.register_view(APIDomainServicesView)
            # Dependency/platform setup may register HTTP views. Freeze the real
            # aiohttp application only after both actual config entries load.
            self.client = TestClient(
                TestServer(self.hass.http.app, host="127.0.0.1"),
                headers={"Authorization": "Bearer " + access},
            )
            await self.client.start_server()
            self.stack.push_async_callback(self.client.close)
            for module in (podconnect, spotify_api, importlib.import_module("custom_components.podconnect.targets"),
                           importlib.import_module("custom_components.podconnect.media_player")):
                self.assertEqual(Path(module.__file__).resolve().parent, ROOT / "custom_components/podconnect")

    async def _cleanup(self):
        # Release only fixture-owned barriers before joining actual service work.
        for hold in getattr(self, "holds", ()):
            hold.set()
        primary = None

        def retain_failure(error):
            nonlocal primary
            if primary is None:
                primary = error
            elif error is not primary:
                primary.add_note("Secondary cleanup failure: " + type(error).__name__)

        try:
            async with asyncio.timeout(15):
                tasks = getattr(self, "tasks", ())
                for task in tasks:
                    if not task.done():
                        task.cancel()
                if tasks:
                    await asyncio.gather(*tasks, return_exceptions=True)
                hass = getattr(self, "hass", None)
                if hass is not None:
                    await hass.async_block_till_done()
                    for entry in hass.config_entries.async_entries(DOMAIN):
                        if entry.state is ConfigEntryState.LOADED:
                            old_data = entry.runtime_data
                            self.assertTrue(await hass.config_entries.async_unload(entry.entry_id))
                            self.assertFalse(old_data.active)
        except BaseException as error:
            retain_failure(error)

        # One second 15s phase: a stop failure cannot skip endpoint/zeroconf
        # cleanup or give its finally a fresh time budget.
        deadline = asyncio.get_running_loop().time() + 15
        try:
            async with asyncio.timeout_at(deadline):
                hass = getattr(self, "hass", None)
                if hass is not None:
                    await hass.async_stop(force=True)
        except BaseException as error:
            retain_failure(error)
        finally:
            try:
                async with asyncio.timeout_at(deadline):
                    await self.stack.aclose()
            except BaseException as error:
                retain_failure(error)
        try:
            self.assertEqual(getattr(self, "loop_errors", []), [])
        except BaseException as error:
            retain_failure(error)
        if primary is not None:
            raise primary

    def _task(self, awaitable):
        task = asyncio.create_task(awaitable)
        self.tasks.append(task)
        return task

    async def _backend(self, request):
        body = await request.json() if request.can_read_body else None
        self.calls.append((request.method, request.path, body, "Authorization" in request.headers))
        if request.path == self.hold_path or (
            self.hold_post and request.method == "POST" and request.path == "/api/room-switch"
        ):
            self.entered.set()
            await self.release.wait()
        if request.path.startswith("/spotify/"):
            self.assertEqual(request.headers.get("Authorization"), "Bearer inert-fixture-only")
            if request.method == "PUT" and request.path == "/spotify/me/player":
                return web.Response(status=204)
            if request.path == "/spotify/me/player/devices":
                return web.json_response({"devices": [{"id": "spotify-one", "name": "Fixture speaker",
                    "is_restricted": False, "is_active": True, "volume_percent": 30}]})
            if request.path == "/spotify/me/player":
                return web.json_response({"is_playing": False, "device": {"id": "spotify-one"},
                    "item": None, "progress_ms": 0, "repeat_state": "off", "shuffle_state": False})
        self.assertNotIn("Authorization", request.headers)
        binding = {"ready": True, "incarnation": "fixture-incarnation", "registry": "fixture-registry", "room_id": "room-alpha"}
        if request.path == "/api/room-switch":
            if request.method == "GET":
                return web.json_response({"binding": binding, "aliases": [
                    {"id": 1, "room_id": "room-alpha", "name": "Alpha"},
                    {"id": 2, "room_id": "room-beta", "name": "Beta"}]})
            if request.method == "POST":
                self.move_finished.set()
                return web.json_response({"accepted_local": True, "binding": {**binding, "room_id": body["room_id"]}})
        if request.path == "/api/rooms":
            return web.json_response({"rooms": [
                {"id": "room-alpha", "homepod_id": "saved-output-alpha"},
                {"id": "room-beta", "homepod_id": "saved-output-beta"}]})
        if request.path == "/api/state":
            return web.json_response({"owntone_up": True, "devices": [{"id": "output-one",
                "name": "Observed fixture", "selected": False, "needs_auth": False}]})
        self.fail("Unexpected inert backend request: " + request.method + " " + request.path)

    def _writes(self):
        return [call for call in self.calls if call[0] in ("POST", "PUT", "DELETE")]

    async def _service(self, name, data, *, response=True):
        suffix = "?return_response" if response else ""
        result = await self.client.post("/api/services/podconnect/" + name + suffix, json=data)
        if result.status == 200:
            return result.status, await result.json()
        return result.status, await result.text()

    async def test_registration_account_discovery_and_real_schema(self):
        async with asyncio.timeout(15):
            response = await self.client.get("/api/services")
            self.assertEqual(response.status, 200)
            domain = next(row for row in await response.json() if row["domain"] == DOMAIN)
            services = domain["services"]
            for name in ("get_targets", "move_playback"):
                self.assertEqual(services[name]["response"], {"optional": False})
                self.assertIs(self.hass.services.supports_response(DOMAIN, name), SupportsResponse.ONLY)
            self.assertFalse(services["get_targets"]["fields"]["config_entry_id"]["required"])
            self.assertTrue(services["move_playback"]["fields"]["config_entry_id"]["required"])
            count = len(self.calls)
            status, body = await self._service("get_targets", {})
            self.assertEqual(status, 200)
            self.assertEqual(set(body), {"changed_states", "service_response"})
            self.assertEqual(body["service_response"], {"accounts": [
                {"config_entry_id": "account-alpha", "title": "Account Alpha"},
                {"config_entry_id": "account-beta", "title": "Account Beta"}]})
            self.assertEqual(len(self.calls), count)  # No implicit account/backend discovery.
            for data in ({}, {"config_entry_id": "account-alpha", "kind": "configured_alias"},
                         {"config_entry_id": "account-alpha", "kind": "bogus", "target_id": "room-beta"},
                         {"config_entry_id": "account-alpha", "kind": "configured_alias", "target_id": "room-beta", "extra": True}):
                self.assertEqual((await self._service("move_playback", data))[0], 400)
            self.assertEqual((await self._service("get_targets", {}, response=False))[0], 400)
            self.assertEqual(len(self.calls), count)
            self.assertEqual(self._writes(), [])
            # Separate session uses the same actual authentication middleware.
            async with ClientSession() as session:
                response = await session.get(self.client.make_url("/api/services"))
                self.assertEqual(response.status, 401)

    async def test_typed_catalog_id_bound_local_move_and_read_only_output(self):
        async with asyncio.timeout(15):
            status, body = await self._service("get_targets", {"config_entry_id": "account-beta"})
            self.assertEqual(status, 200)
            catalog = body["service_response"]
            self.assertEqual(catalog["config_entry_id"], "account-beta")
            self.assertEqual(catalog["errors"], {})
            targets = {(row["kind"], row["target_id"]): row for row in catalog["targets"]}
            self.assertEqual(targets[("configured_alias", "room-beta")]["homepod_id"], "saved-output-beta")
            self.assertTrue(targets[("observed_output", "output-one")]["read_only"])
            data = {"config_entry_id": "account-beta", "kind": "configured_alias", "target_id": "room-beta"}
            status, body = await self._service("move_playback", data)
            self.assertEqual(status, 200)
            self.assertTrue(body["service_response"]["accepted_local"])
            self.assertNotIn("playing", body["service_response"])
            self.assertEqual(self._writes(), [("POST", "/api/room-switch", {
                "room_id": "room-beta", "expected_incarnation": "fixture-incarnation", "expected_registry": "fixture-registry"}, False)])
            data["kind"], data["target_id"] = "observed_output", "output-one"
            self.assertNotEqual((await self._service("move_playback", data))[0], 200)
            self.assertEqual(len(self._writes()), 1)
            data["kind"], data["target_id"] = "spotify_device", "spotify-one"
            status, body = await self._service("move_playback", data)
            self.assertEqual(status, 200)
            self.assertTrue(body["service_response"]["provider_request_accepted"])
            self.assertNotIn("accepted_local", body["service_response"])
            self.assertEqual(self._writes()[1], ("PUT", "/spotify/me/player", {
                "device_ids": ["spotify-one"], "play": False}, True))
            self.assertEqual(len(self._writes()), 2)  # No invented resume/second command.

    async def test_unload_retires_held_catalog_and_preserves_other_account(self):
        async with asyncio.timeout(15):
            old = self.entries[0].runtime_data
            self.hold_path = "/api/room-switch"
            call = self._task(self._service("move_playback", {
                "config_entry_id": "account-alpha", "kind": "configured_alias", "target_id": "room-beta"}))
            await self.entered.wait()
            self.assertTrue(await self.hass.config_entries.async_unload("account-alpha"))
            self.assertFalse(old.active)
            self.release.set()
            self.assertNotEqual((await call)[0], 200)
            self.assertEqual(self._writes(), [])
            accounts = (await self._service("get_targets", {}))[1]["service_response"]["accounts"]
            self.assertEqual(accounts, [{"config_entry_id": "account-beta", "title": "Account Beta"}])

    async def test_real_options_reload_denies_old_call_then_new_owner_moves_once(self):
        async with asyncio.timeout(15):
            entry = self.entries[0]
            old = entry.runtime_data
            self.hold_path = "/api/room-switch"
            data = {"config_entry_id": entry.entry_id, "kind": "configured_alias", "target_id": "room-beta"}
            call = self._task(self._service("move_playback", data))
            await self.entered.wait()
            replacement = asyncio.Event()

            @callback
            def entry_changed(_change, current):
                if current is entry and current.state is ConfigEntryState.LOADED and current.runtime_data is not old:
                    replacement.set()

            unsubscribe = async_dispatcher_connect(self.hass, SIGNAL_CONFIG_ENTRY_CHANGED, entry_changed)
            self.stack.callback(unsubscribe)
            changed = self.hass.config_entries.async_update_entry(entry, options={"speakers_url": self.base + "/", "fixture_revision": 2})
            self.assertTrue(changed)
            # Actual ConfigEntry LOADED publication after the update-listener reload.
            await replacement.wait()
            self.assertFalse(old.active)
            self.assertIsNot(entry.runtime_data, old)
            self.assertTrue(entry.runtime_data.active)
            self.assertEqual(entry.options["speakers_url"], self.base + "/")
            self.release.set()
            self.assertNotEqual((await call)[0], 200)
            self.assertEqual(self._writes(), [])
            self.hold_path = None
            self.assertEqual((await self._service("move_playback", data))[0], 200)
            self.assertEqual(len(self._writes()), 1)

    async def test_actual_entity_held_spotify_lookup_cannot_transfer_after_unload(self):
        async with asyncio.timeout(15):
            entry = self.entries[0]
            entity_id = entity_registry.async_get(self.hass).async_get_entity_id(
                "media_player", DOMAIN, entry.entry_id + "_spotify-one")
            self.assertIsNotNone(entity_id)
            self.hold_path = "/spotify/me/player/devices"
            call = self._task(self.hass.services.async_call(
                "media_player", "select_source", {"entity_id": entity_id, "source": "Fixture speaker"}, blocking=True))
            await self.entered.wait()
            self.assertTrue(await self.hass.config_entries.async_unload(entry.entry_id))
            self.release.set()
            with self.assertRaises(HomeAssistantError):
                await call
            self.assertEqual(self._writes(), [])

    async def test_http_disconnect_does_not_cancel_or_repeat_already_sent_service(self):
        async with asyncio.timeout(15):
            # Hold only actual POST, after fresh catalog resolution and service admission.
            self.hold_post = True
            call = self._task(self.client.post("/api/services/podconnect/move_playback?return_response", json={
                "config_entry_id": "account-alpha", "kind": "configured_alias", "target_id": "room-beta"}))
            await self.entered.wait()
            self.assertEqual(len(self._writes()), 1)
            await self.client.session.close()
            with self.assertRaises(ClientConnectionError):
                await call
            self.assertFalse(self.move_finished.is_set())
            self.release.set()
            await self.move_finished.wait()
            await self.hass.async_block_till_done()
            self.assertEqual(len(self._writes()), 1)


if __name__ == "__main__":
    unittest.main()
