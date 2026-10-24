"""Actual HA services and HTTP with inert native/OwnTone manager fixtures only."""

import asyncio
import copy
import time
import unittest
from datetime import timedelta
from unittest.mock import patch

import voluptuous as vol
from aiohttp import web
from test_commands import CommandFrameworkTests


class RoomTransportFrameworkTests(CommandFrameworkTests):
    async def asyncSetUp(self):
        self.output_observation = False
        self.output_reads = []
        self.output_selected = ["123"]
        self.output_hold = False
        self.output_hold_path = "/api/room-transport/state"
        self.output_failure_status = 200
        self.output_entered = asyncio.Event()
        self.output_release = asyncio.Event()
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
        self.holds.extend(
            (self.room_release, self.room_commit_release, self.output_release)
        )

    def native(self):
        return {
            "account_user_id": self.entries[0].unique_id,
            "target_id": "spotify-one",
            "room_id": getattr(self, "output_room", "room-alpha"),
            "homepod_id": getattr(self, "output_homepod", "123"),
            "connection_id": "connection-a",
            "session_id": "session-a",
            "alias_incarnation": "incarnation-a",
            "alias_registry": "registry-a",
            "alias_revision": getattr(self, "output_revision", 7),
        }

    def route(self):
        return {
            "process": "12345678-1234-4234-8234-123456789abc",
            "device_id": getattr(self, "output_homepod", "123"),
            "device_incarnation": "1",
            "session_epoch": "2",
            "route_revision": "3",
            "selection_revision": "4",
            "input_generation": "5",
            "input_item_id": "6",
            "input_path_digest": "b" * 64,
        }

    async def _backend(self, request):
        if self.output_observation and request.path in {
            "/api/room-switch",
            "/api/state",
            "/api/room-transport/state",
        }:
            self.output_reads.append(request.path)
            self.assertEqual(request.method, "GET")
            self.assertNotIn("Authorization", request.headers)
            if request.path == self.output_hold_path and self.output_hold:
                self.output_entered.set()
                await self.output_release.wait()
            if self.output_failure_status != 200:
                return web.Response(status=self.output_failure_status)
            if request.path == "/api/room-switch":
                native = self.native()
                return web.json_response(
                    {
                        "binding": {
                            "ready": True,
                            "incarnation": native["alias_incarnation"],
                            "registry": native["alias_registry"],
                            "room_id": native["room_id"],
                        },
                        "aliases": [
                            {"id": 1, "room_id": "room-alpha", "name": "SAME NAME"},
                            {"id": 2, "room_id": "room-beta", "name": "SAME NAME"},
                        ],
                    }
                )
            if request.path == "/api/state":
                return web.json_response(
                    {
                        "owntone_up": True,
                        "devices": [
                            {
                                "id": ident,
                                "name": "SAME NAME",
                                "selected": True,
                                "needs_auth": False,
                            }
                            for ident in self.output_selected
                        ],
                    }
                )
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

    async def output_snapshot(self, include=True):
        self.output_observation = True
        return await self.hass.services.async_call(
            "podconnect",
            "get_playback_snapshot",
            {"config_entry_id": "account-alpha", "include_output": include},
            blocking=True,
            return_response=True,
        )

    async def test_playback_output_default_v1_remains_closed_and_read_free(self):
        value = await self.output_snapshot(False)
        self.assertEqual(
            set(value),
            {
                "ok",
                "schema",
                "config_entry_id",
                "owner_epoch",
                "poll_sequence",
                "observation",
                "playback",
                "physical_result_verified",
            },
        )
        self.assertEqual(value["schema"], "podconnect.playback.v1")
        self.assertEqual(self.output_reads, [])
        for invalid in (1, "true", None):
            with self.subTest(value=invalid), self.assertRaises(vol.Invalid):
                await self.output_snapshot(invalid)
        self.assertEqual(self.calls, [])

    async def test_playback_output_exact_v2_preserves_original_metadata_and_has_no_effects(
        self,
    ):
        data = self.entries[0].runtime_data
        snapshots = []
        original = data.observations.playback

        def capture(entry):
            value = original(entry)
            snapshots.append(copy.deepcopy(value))
            return value

        with patch.object(data.observations, "playback", capture):
            value = await self.output_snapshot()
        self.assertEqual(set(value), set(snapshots[0]) | {"output"})
        self.assertEqual(
            {**value, "schema": "podconnect.playback.v1"} | {},
            {**snapshots[0], "output": value["output"]},
        )
        output = value["output"]
        self.assertEqual(set(output), {"status", "reason", "binding"})
        self.assertEqual((output["status"], output["reason"]), ("observed", None))
        binding = output["binding"]
        self.assertEqual(
            set(binding),
            {"target_id", "native_room_id", "homepod_id", "binding_id", "valid_until"},
        )
        self.assertEqual(
            (binding["target_id"], binding["native_room_id"], binding["homepod_id"]),
            ("spotify-one", "room-alpha", "123"),
        )
        self.assertRegex(binding["binding_id"], "^[a-f0-9]{64}$")
        self.assertGreater(binding["valid_until"], time.time())
        self.assertLessEqual(binding["valid_until"], time.time() + 5)
        self.assertEqual(self.room_posts, [])
        self.assertEqual(self.native_prepares, 0)
        self.assertEqual(self.native_commits, 0)
        self.assertEqual(self.calls, [])

    async def test_playback_output_valid_actionless_facts_do_not_create_controls(self):
        self.state_corrupt = lambda state: state.update(actions=[])
        provider = self.entries[0].runtime_data.coordinator.data["playback"]
        provider["actions"] = {
            "disallows": {
                "pausing": True,
                "resuming": True,
                "skipping_next": True,
                "skipping_prev": True,
            }
        }
        provider["device"]["supports_volume"] = False
        value = await self.output_snapshot()
        self.assertEqual(value["output"]["status"], "observed")
        controls = await self.room_offer()
        self.assertFalse(controls["ok"])
        self.assertEqual(controls["error_kind"], "room_transport_unavailable")
        self.assertEqual(self.room_posts, [])

    async def test_playback_output_resolves_actual_alias_on_shared_target_without_names(
        self,
    ):
        alpha = await self.output_snapshot()
        self.output_room, self.output_homepod, self.output_revision = (
            "room-beta",
            "456",
            8,
        )
        self.output_selected = ["456"]
        beta = await self.output_snapshot()
        self.assertEqual(beta["output"]["status"], "observed")
        self.assertEqual(
            beta["output"]["binding"]["target_id"],
            alpha["output"]["binding"]["target_id"],
        )
        self.assertEqual(beta["output"]["binding"]["native_room_id"], "room-beta")
        self.assertEqual(beta["output"]["binding"]["homepod_id"], "456")
        self.assertNotEqual(
            beta["output"]["binding"]["binding_id"],
            alpha["output"]["binding"]["binding_id"],
        )
        self.assertNotIn("/api/rooms", self.output_reads)

    async def test_playback_output_absent_and_ambiguous_do_not_choose_first(self):
        for selected, reason in (
            ([], "route_unavailable"),
            (["123", "456"], "ambiguous_output"),
        ):
            self.output_selected = selected
            self.output_reads.clear()
            value = await self.output_snapshot()
            self.assertEqual(
                value["output"],
                {"status": "unavailable", "reason": reason, "binding": None},
            )
            self.assertNotIn("/api/room-transport/state", self.output_reads)

    async def test_playback_output_binding_is_route_identity_not_poll_or_progress(self):
        first = await self.output_snapshot()
        coordinator = self.entries[0].runtime_data.coordinator
        coordinator.data["poll_sequence"] += 1
        coordinator.data["playback"]["progress_ms"] = 1500
        second = await self.output_snapshot()
        self.assertGreater(second["poll_sequence"], first["poll_sequence"])
        self.assertEqual(
            second["output"]["binding"]["binding_id"],
            first["output"]["binding"]["binding_id"],
        )
        original = self.native
        self.native = lambda: {**original(), "session_id": "new-session"}
        third = await self.output_snapshot()
        self.assertNotEqual(
            third["output"]["binding"]["binding_id"],
            first["output"]["binding"]["binding_id"],
        )
        self.native = original
        self.state_corrupt = lambda state: state["route"].update(input_generation="9")
        fourth = await self.output_snapshot()
        self.assertNotEqual(
            fourth["output"]["binding"]["binding_id"],
            first["output"]["binding"]["binding_id"],
        )

    async def test_playback_output_account_changes_during_await_cannot_mix(self):
        coordinator = self.entries[0].runtime_data.coordinator
        baseline = copy.deepcopy(coordinator.data)
        for change in ("poll", "body", "expiry", "owner"):
            coordinator.data = copy.deepcopy(baseline)
            self.output_entered.clear()
            self.output_release.clear()
            self.output_hold = True
            task = self._task(self.output_snapshot())
            await asyncio.wait_for(self.output_entered.wait(), timeout=2)
            if change == "poll":
                coordinator.data["poll_sequence"] += 1
            elif change == "body":
                coordinator.data["playback"]["progress_ms"] += 1
            elif change == "expiry":
                coordinator.data["playback_observed_monotonic"] -= 16
            else:
                coordinator.owner_epoch = "new-Control-owner"
            self.output_release.set()
            value = await task
            self.assertEqual(value, {"ok": False, "error_kind": "unavailable"})
        self.assertEqual(self.calls, [])

    async def test_playback_output_changed_native_owner_is_not_a_binding(self):
        self.state_corrupt = lambda state: state["native"].update(
            alias_incarnation="new-native-owner"
        )
        value = await self.output_snapshot()
        self.assertEqual(
            value["output"],
            {"status": "unavailable", "reason": "owner_changed", "binding": None},
        )

    async def test_playback_output_idle_errors_and_original_ttl_are_not_renewed(self):
        coordinator = self.entries[0].runtime_data.coordinator
        saved = copy.deepcopy(coordinator.data)
        coordinator.data["playback"] = None
        empty = await self.output_snapshot()
        self.assertEqual(empty["playback"]["state"], "no_active_playback")
        self.assertEqual(
            empty["output"],
            {"status": "unavailable", "reason": "route_unavailable", "binding": None},
        )
        self.assertEqual(self.output_reads, [])
        coordinator.last_update_success = False
        self.assertEqual(
            await self.output_snapshot(), {"ok": False, "error_kind": "unavailable"}
        )
        coordinator.last_update_success = True
        coordinator.data = saved
        coordinator.data["playback_observed_monotonic"] -= 14
        coordinator.data["playback_observed_at"] -= timedelta(seconds=14)
        value = await self.output_snapshot()
        self.assertLessEqual(
            value["output"]["binding"]["valid_until"],
            coordinator.data["playback_observed_at"].timestamp() + 15,
        )
        self.assertEqual(self.calls, [])

    async def test_playback_output_error_after_account_replacement_cannot_publish_metadata(
        self,
    ):
        self.output_failure_status = 500
        current = await self.output_snapshot()
        self.assertEqual(
            current["output"],
            {"status": "unavailable", "reason": "route_unavailable", "binding": None},
        )
        self.output_hold, self.output_hold_path = True, "/api/room-switch"
        entry = self.entries[0]
        original = entry.runtime_data
        task = self._task(self.output_snapshot())
        await asyncio.wait_for(self.output_entered.wait(), timeout=2)
        try:
            entry.runtime_data = copy.copy(original)
            self.output_release.set()
            self.assertEqual(await task, {"ok": False, "error_kind": "unavailable"})
        finally:
            entry.runtime_data = original
            self.output_release.set()
        self.assertEqual(self.room_posts, [])
        self.assertEqual(self.calls, [])

    async def test_playback_output_timeout_after_source_expiry_cannot_publish_metadata(
        self,
    ):
        data = self.entries[0].runtime_data
        entered, release = asyncio.Event(), asyncio.Event()
        self.holds.append(release)

        async def held_timeout():
            entered.set()
            await release.wait()
            raise TimeoutError

        with patch.object(data.speakers, "aliases", held_timeout):
            task = self._task(self.output_snapshot())
            await asyncio.wait_for(entered.wait(), timeout=2)
            data.coordinator.data["playback_observed_monotonic"] -= 16
            release.set()
            self.assertEqual(await task, {"ok": False, "error_kind": "unavailable"})
        self.assertEqual(self.room_posts, [])
        self.assertEqual(self.calls, [])


def load_tests(loader, tests, pattern):
    return unittest.TestSuite(
        RoomTransportFrameworkTests(name)
        for name in loader.getTestCaseNames(RoomTransportFrameworkTests)
        if name.startswith(("test_room_", "test_playback_output_"))
    )
