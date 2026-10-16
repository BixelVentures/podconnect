"""Actual HA services and HTTP with inert native/OwnTone manager fixtures only."""

import asyncio
import copy
import unittest

import voluptuous as vol
from aiohttp import web
from test_commands import CommandFrameworkTests


class RoomTransportFrameworkTests(CommandFrameworkTests):
    async def asyncSetUp(self):
        self.room_posts = []
        self.room_reads = []
        self.room_hold = False
        self.room_entered = asyncio.Event()
        self.room_release = asyncio.Event()
        self.room_commit_hold = False
        self.room_commit_entered = asyncio.Event()
        self.room_commit_release = asyncio.Event()
        self.room_status = 200
        self.room_corrupt = None
        self.state_corrupt = None
        self.prepare_corrupt = None
        self.ttl = 5000
        await super().asyncSetUp()
        self.holds.extend((self.room_release, self.room_commit_release))

    def native(self):
        return {
            "account_user_id": self.entries[0].unique_id,
            "target_id": "spotify-one",
            "room_id": "room-alpha",
            "homepod_id": "123",
            "connection_id": "connection-a",
            "session_id": "session-a",
            "alias_incarnation": "incarnation-a",
            "alias_registry": "registry-a",
            "alias_revision": 7,
        }

    def route(self):
        return {
            "process": "12345678-1234-4234-8234-123456789abc",
            "device_id": "123",
            "device_incarnation": "1",
            "session_epoch": "2",
            "route_revision": "3",
            "selection_revision": "4",
            "input_generation": "5",
            "input_item_id": "6",
            "input_path_digest": "b" * 64,
        }

    async def _backend(self, request):
        if request.path == "/api/room-transport/state":
            self.room_reads.append(dict(request.query))
            value = {
                "schema": "podconnect.room_transport_state.v1",
                "ok": True,
                "native": self.native(),
                "route": self.route(),
                "actions": ["pause", "resume", "next", "previous"],
                "physical_result_verified": False,
            }
            if self.state_corrupt:
                self.state_corrupt(value)
            return web.json_response(value)
        if request.path in {
            "/api/room-transport/prepare",
            "/api/room-transport/commit",
        }:
            body = await request.json()
            self.room_posts.append((request.path, copy.deepcopy(body)))
            if request.path.endswith("prepare"):
                self.room_entered.set()
                if self.room_hold:
                    await self.room_release.wait()
                if self.native_source_room != body["scope"]["room_id"]:
                    return web.Response(status=409)
                value = {
                    "schema": "podconnect.room_transport_prepare.v1",
                    "ok": True,
                    "native": self.native(),
                    "route": self.route(),
                    "prepare_id": "a" * 32,
                    "expires_after_ms": self.ttl,
                    "action": body["action"],
                    "volume_percent": 0,
                    "physical_result_verified": False,
                }
                if self.prepare_corrupt:
                    self.prepare_corrupt(value)
                return web.json_response(value)
            self.room_commit_entered.set()
            if self.room_commit_hold:
                await self.room_commit_release.wait()
            if self.room_status != 200:
                return web.Response(status=self.room_status)
            value = {
                "schema": "podconnect.room_transport_commit.v1",
                "ok": True,
                "prepare_id": "a" * 32,
                "outcome": "accepted",
                "accepted_native": True,
                "retry_allowed": False,
                "physical_result_verified": False,
            }
            if self.room_corrupt:
                self.room_corrupt(value)
            return web.json_response(value)
        return await super()._backend(request)

    async def room_offer(self, **overrides):
        body = {
            "config_entry_id": "account-alpha",
            "kind": "spotify_device",
            "target_id": "spotify-one",
            "room_id": "room-alpha",
            "homepod_id": "123",
            **overrides,
        }
        return await self.hass.services.async_call(
            "podconnect",
            "get_room_playback_controls",
            body,
            blocking=True,
            return_response=True,
        )

    def room_request(self, action="resume", **overrides):
        return self.request(
            action,
            room_id="room-alpha",
            homepod_id="123",
            native=self.native(),
            route=self.route(),
            **overrides,
        )

    async def room_invoke(self, request):
        return await self.hass.services.async_call(
            "podconnect",
            "control_room_playback",
            request,
            blocking=True,
            return_response=True,
        )

    async def test_room_offer_and_four_actions_use_exact_manager_only(self):
        offer = await self.room_offer()
        self.assertEqual(offer["schema"], "podconnect.room_controls.v1")
        self.assertFalse(offer["volume_supported"])
        self.assertNotIn("volume", offer["actions"])
        self.assertEqual(
            self.room_reads[0],
            {
                k: self.native()[k]
                for k in ("account_user_id", "target_id", "room_id", "homepod_id")
            },
        )
        for action in ("resume", "pause", "next", "previous"):
            self.playing = action != "resume"
            await self.entries[0].runtime_data.coordinator.async_refresh()
            request = self.room_request(action)
            result = await self.room_invoke(request)
            self.assertEqual(result["outcome"], "accepted")
            self.assertTrue(result["accepted_native"])
            self.assertFalse(result["provider_request_accepted"])
            self.assertFalse(result["physical_result_verified"])
            self.assertEqual(await self.room_invoke(request), result)
        self.assertEqual(len(self.room_posts), 8)
        self.assertEqual(
            self.room_posts[0][1],
            {
                "scope": {
                    k: self.native()[k]
                    for k in ("account_user_id", "target_id", "room_id", "homepod_id")
                },
                "native": self.native(),
                "route": self.route(),
                "action": "resume",
                "volume_percent": 0,
            },
        )
        self.assertEqual(self.room_posts[1][1], {"prepare_id": "a" * 32})
        self.assertEqual(self._writes(), [])
        self.assertEqual((self.native_prepares, self.native_commits), (0, 0))

    async def test_room_services_are_closed_and_volume_is_unavailable(self):
        for body in (
            self.room_request("volume", args={"percent": 27}),
            {**self.room_request(), "unexpected": True},
            {k: v for k, v in self.room_request().items() if k != "native"},
        ):
            with self.assertRaises(vol.Invalid):
                await self.room_invoke(body)
        with self.assertRaises(vol.Invalid):
            await self.room_offer(unexpected=True)
        self.assertEqual(self.room_posts, [])

    async def test_room_prepare_poll_or_owner_replacement_denies_before_commit(self):
        for replacement in ("poll", "owner", "account"):
            self.room_posts.clear()
            self.room_entered.clear()
            self.room_release.clear()
            self.room_hold = True
            data = self.entries[0].runtime_data
            data.coordinator.owner_epoch = data.commands.owner_epoch
            await data.coordinator.async_refresh()
            request = self.room_request()
            task = self._task(self.room_invoke(request))
            await asyncio.wait_for(self.room_entered.wait(), 2)
            if replacement == "poll":
                await data.coordinator.async_refresh()
            elif replacement == "owner":
                data.coordinator.owner_epoch = "replaced"
            else:
                self.hass.config_entries.async_update_entry(
                    self.entries[0], unique_id="other-account"
                )
            self.room_release.set()
            result = await task
            self.assertEqual(result["outcome"], "rejected")
            self.assertEqual(len(self.room_posts), 1)
        self.assertEqual(self._writes(), [])

    async def test_room_prepare_expired_in_transit_never_commits(self):
        self.ttl = 1
        self.room_hold = True
        task = self._task(self.room_invoke(self.room_request()))
        await asyncio.wait_for(self.room_entered.wait(), 2)
        await asyncio.sleep(0.01)
        self.room_release.set()
        result = await task
        self.assertEqual(result["error_kind"], "room_transport_expired")
        self.assertEqual(len(self.room_posts), 1)
        self.assertEqual(self._writes(), [])

    async def test_room_cancellation_after_commit_keeps_unknown_receipt(self):
        self.room_commit_hold = True
        request = self.room_request()
        task = self._task(self.room_invoke(request))
        await asyncio.wait_for(self.room_commit_entered.wait(), 2)
        task.cancel()
        with self.assertRaises(asyncio.CancelledError):
            await task
        result = await self.room_invoke(request)
        self.assertEqual(result["outcome"], "unknown")
        self.assertFalse(result["retry_allowed"])
        self.room_commit_release.set()
        self.assertEqual(len(self.room_posts), 2)
        self.assertEqual(self._writes(), [])

    async def test_room_same_engine_alias_change_during_prepare_never_sends(self):
        data = self.entries[0].runtime_data
        offer = await self.room_offer()
        self.assertTrue(offer["ok"])
        request = self.room_request()
        original = (
            data.coordinator.owner_epoch,
            data.coordinator.poll_sequence,
            self.entries[0].unique_id,
        )
        self.room_hold = True
        task = self._task(self.room_invoke(request))
        await asyncio.wait_for(self.room_entered.wait(), 2)
        self.native_source_room = "room-beta"
        self.assertEqual(
            (
                data.coordinator.owner_epoch,
                data.coordinator.poll_sequence,
                self.entries[0].unique_id,
            ),
            original,
        )
        self.room_release.set()
        result = await task
        self.assertEqual(result["outcome"], "rejected")
        self.assertEqual(result["error_kind"], "room_transport_unavailable")
        self.assertEqual(len(self.room_posts), 1)
        self.assertEqual(self._writes(), [])
        self.assertEqual((self.native_prepares, self.native_commits), (0, 0))

    async def test_room_response_only_http_service_descriptors_match_callers(self):
        response = await self.client.get("/api/services")
        services = next(
            row["services"]
            for row in await response.json()
            if row["domain"] == "podconnect"
        )
        self.assertEqual(
            services["control_room_playback"]["response"], {"optional": False}
        )
        self.assertEqual(
            set(services["control_room_playback"]["fields"]), set(self.room_request())
        )
        self.assertEqual(
            services["get_room_playback_controls"]["response"], {"optional": False}
        )
        self.assertEqual(
            set(services["get_room_playback_controls"]["fields"]),
            {"config_entry_id", "kind", "target_id", "room_id", "homepod_id"},
        )
        status, _body = await self._service(
            "control_room_playback", self.room_request(), response=False
        )
        self.assertNotEqual(status, 200)
        self.assertEqual(self.room_posts, [])

    async def test_room_mixed_or_malformed_state_is_unavailable(self):
        cases = [
            lambda v: v.update(extra=True),
            lambda v: v["native"].update(alias_revision=True),
            lambda v: v["native"].update(account_user_id="foreign"),
            lambda v: v["route"].update(selection_revision="04"),
            lambda v: v.update(actions=[{}]),
            lambda v: v.update(actions=["volume"]),
            lambda v: v["route"].update(device_id="456"),
            lambda v: v["route"].update(input_path_digest="B" * 64),
            lambda v: v["route"].pop("input_path_digest"),
        ]
        for corrupt in cases:
            self.state_corrupt = corrupt
            self.assertFalse((await self.room_offer())["ok"])
        self.assertEqual(self.room_posts, [])

    async def test_room_prepare_changed_identity_or_invalid_cutoff_never_commits(self):
        for corrupt in (
            lambda v: v["native"].update(alias_revision=8),
            lambda v: v["route"].update(selection_revision="9"),
            lambda v: v["route"].update(input_path_digest="c" * 64),
            lambda v: v.update(expires_after_ms=5001),
            lambda v: v.update(volume_percent=True),
        ):
            self.prepare_corrupt = corrupt
            await self.entries[0].runtime_data.coordinator.async_refresh()
            result = await self.room_invoke(self.room_request())
            self.assertEqual(result["outcome"], "rejected")
        self.assertEqual(len(self.room_posts), 5)
        self.assertTrue(all(path.endswith("prepare") for path, _ in self.room_posts))

    async def test_room_unknown_is_consumed_and_shared_with_legacy_sequence(self):
        self.room_status = 502
        request = self.room_request()
        result = await self.room_invoke(request)
        self.assertEqual(result["outcome"], "unknown")
        self.assertFalse(result["retry_allowed"])
        self.assertEqual(await self.room_invoke(request), result)
        self.assertEqual(len(self.room_posts), 2)
        self.assertEqual(
            (
                await self.invoke(
                    self.request(command_sequence=request["command_sequence"])
                )
            )["error_kind"],
            "request_reused",
        )
        data = self.entries[0].runtime_data
        data.commands.receipts.clear()
        self.assertEqual(
            (await self.room_invoke(request))["error_kind"], "stale_command"
        )
        self.assertEqual(self._writes(), [])

    async def test_room_invalid_ack_after_send_is_unknown_and_never_replayed(self):
        for corrupt in (
            lambda v: v.update(extra=True),
            lambda v: v.update(accepted_native=False),
            lambda v: v.update(physical_result_verified=True),
            lambda v: v.update(ok=False, outcome="unknown"),
        ):
            self.room_corrupt = corrupt
            await self.entries[0].runtime_data.coordinator.async_refresh()
            request = self.room_request()
            result = await self.room_invoke(request)
            self.assertEqual(result["outcome"], "unknown")
            self.assertEqual(await self.room_invoke(request), result)
        self.assertEqual(len(self.room_posts), 8)
        self.assertEqual(self._writes(), [])


def load_tests(loader, tests, pattern):
    return unittest.TestSuite(
        RoomTransportFrameworkTests(name)
        for name in loader.getTestCaseNames(RoomTransportFrameworkTests)
        if name.startswith("test_room_")
    )
