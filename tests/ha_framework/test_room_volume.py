"""Real HA service/client boundaries; volume manager endpoints are inert fixtures."""

import asyncio
import copy
import time
import unittest
from unittest.mock import patch

import voluptuous as vol
from aiohttp import web
from test_room_transport import RoomTransportFrameworkTests


class RoomVolumeFrameworkTests(RoomTransportFrameworkTests):
    async def asyncSetUp(self):
        self.volume_posts = []
        self.volume_hold = False
        self.volume_entered = asyncio.Event()
        self.volume_release = asyncio.Event()
        self.volume_commit_hold = False
        self.volume_commit_entered = asyncio.Event()
        self.volume_commit_release = asyncio.Event()
        self.volume_status = 200
        self.volume_ack_corrupt = None
        self.volume_prepare_corrupt = None
        self.volume_state_corrupt = None
        self.volume_state_reads = []
        self.volume_state_hold = False
        self.volume_state_entered = asyncio.Event()
        self.volume_state_release = asyncio.Event()
        self.volume_ttl = 5000
        self.volume_outcome = "protocol_accepted"
        self.manual_percent = 27
        self.prepared_percent = None
        await super().asyncSetUp()
        self.holds.extend(
            (self.volume_release, self.volume_commit_release, self.volume_state_release)
        )
        self.volume_commands = self.entries[0].runtime_data.commands

    def volume_snapshot(self):
        return {
            "base_revision": "7",
            "attention_revision": "8",
            "base_percent": 27,
            "effective_percent": 10,
        }

    def volume_result(self, suffix="commit"):
        accepted = self.volume_outcome == "protocol_accepted"
        return {
            "schema": "podconnect.room_volume_" + suffix + ".v1",
            "ok": accepted,
            "prepare_id": "c" * 32,
            "state": self.volume_outcome,
            "reservation_accepted": accepted,
            "protocol_accepted": accepted,
            "possible_effect": accepted,
            "terminal": accepted,
            "deadline_retired": accepted,
            "contexts_drained": accepted,
            "retry_allowed": False,
            "physical_result_verified": False,
        }

    async def _backend(self, request):
        if request.path == "/api/room-volume/state":
            self.assertEqual(request.method, "GET")
            self.assertNotIn("Authorization", request.headers)
            self.volume_state_reads.append(dict(request.query))
            self.volume_state_entered.set()
            if self.volume_state_hold:
                await self.volume_state_release.wait()
            value = {
                "schema": "podconnect.room_volume_state.v1",
                "ok": True,
                "native": self.native(),
                "route": self.route(),
                "volume_state": self.volume_snapshot(),
                "actions": ["volume"],
                "physical_result_verified": False,
            }
            if self.volume_state_corrupt:
                self.volume_state_corrupt(value)
            return web.json_response(value)
        if request.path == "/api/room-volume/result":
            return web.json_response(self.volume_result("result"))
        if request.path.startswith("/api/room-volume/"):
            body = await request.json()
            self.volume_posts.append((request.path, copy.deepcopy(body)))
            if request.path.endswith("prepare"):
                self.volume_entered.set()
                if self.volume_hold:
                    await self.volume_release.wait()
                self.prepared_percent = body["volume_percent"]
                value = {
                    "schema": "podconnect.room_volume_prepare.v1",
                    "ok": True,
                    "prepare_id": "c" * 32,
                    "expires_after_ms": self.volume_ttl,
                    "native": self.native(),
                    "route": self.route(),
                    "volume_state": self.volume_snapshot(),
                    "action": "volume",
                    "volume_percent": body["volume_percent"],
                    "physical_result_verified": False,
                }
                if self.volume_prepare_corrupt:
                    self.volume_prepare_corrupt(value)
                return web.json_response(value)
            if request.path.endswith("cancel"):
                result = self.volume_result("cancel")
                result.update(
                    state="cancelled_before_admission",
                    ok=True,
                    protocol_accepted=False,
                    possible_effect=False,
                    reservation_accepted=False,
                    terminal=True,
                    contexts_drained=True,
                )
                return web.json_response(result)
            self.volume_commit_entered.set()
            if self.volume_commit_hold:
                await self.volume_commit_release.wait()
            if self.volume_status != 200:
                return web.Response(status=self.volume_status)
            value = self.volume_result()
            if self.volume_ack_corrupt:
                self.volume_ack_corrupt(value)
            return web.json_response(value)
        return await super()._backend(request)

    async def volume_offer(self, **overrides):
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
            "get_room_volume_controls",
            body,
            blocking=True,
            return_response=True,
        )

    def volume_request(self, percent=41, **overrides):
        return {
            **self.room_request("volume", args={"percent": percent}),
            "volume_state": self.volume_snapshot(),
            **overrides,
        }

    async def volume_invoke(self, request):
        return await self.hass.services.async_call(
            "podconnect",
            "control_room_volume",
            request,
            blocking=True,
            return_response=True,
        )

    async def cleanup_finished(self):
        tasks = list(self.volume_commands.room_volume.cleanups)
        if tasks:
            await asyncio.wait_for(asyncio.gather(*tasks), 2)

    def commits(self):
        return [body for path, body in self.volume_posts if path.endswith("commit")]

    async def test_volume_read_and_response_only_descriptors_are_exact(self):
        offer = await self.volume_offer()
        self.assertEqual(offer["schema"], "podconnect.room_volume_controls.v1")
        self.assertEqual(offer["volume_state"], self.volume_snapshot())
        self.assertIsNone(offer["volume_percent"])
        self.assertEqual(offer["actions"], ["volume"])
        self.assertEqual(self.volume_posts, [])
        response = await self.client.get("/api/services")
        services = next(
            row["services"]
            for row in await response.json()
            if row["domain"] == "podconnect"
        )
        self.assertEqual(
            services["control_room_volume"]["response"], {"optional": False}
        )
        self.assertEqual(
            set(services["control_room_volume"]["fields"]), set(self.volume_request())
        )
        self.assertEqual(
            services["get_room_volume_controls"]["response"], {"optional": False}
        )
        status, _body = await self._service(
            "control_room_volume", self.volume_request(), response=False
        )
        self.assertNotEqual(status, 200)
        self.assertEqual(self.volume_posts, [])
        with self.assertRaises(vol.Invalid):
            await self.volume_offer(extra=True)

    async def test_volume_explicit_percent_and_zero_use_only_native_route(self):
        # This physical capability is independent of Spotify's generic flag.
        self.entries[0].runtime_data.coordinator.data["playback"]["device"][
            "supports_volume"
        ] = False
        self.assertEqual((await self.volume_offer())["actions"], ["volume"])
        for percent in (0, 41):
            if percent:
                await self.entries[0].runtime_data.coordinator.async_refresh()
            request = self.volume_request(percent)
            result = await self.volume_invoke(request)
            self.assertEqual(result["outcome"], "accepted")
            self.assertTrue(result["protocol_accepted"])
            self.assertTrue(result["reservation_accepted"])
            self.assertFalse(result["provider_request_accepted"])
            self.assertFalse(result["physical_result_verified"])
            self.assertEqual(await self.volume_invoke(request), result)
            self.assertEqual(self.prepared_percent, percent)
        self.assertEqual(len(self.commits()), 2)
        self.assertEqual(self.commits()[0], {"prepare_id": "c" * 32})
        self.assertEqual(
            self.volume_posts[0][1],
            {
                "scope": {
                    k: self.native()[k]
                    for k in ("account_user_id", "target_id", "room_id", "homepod_id")
                },
                "native": self.native(),
                "route": self.route(),
                "volume_state": self.volume_snapshot(),
                "action": "volume",
                "volume_percent": 0,
            },
        )
        self.assertEqual(self.manual_percent, 27)
        self.assertEqual(self._writes(), [])
        self.assertEqual((self.native_prepares, self.native_commits), (0, 0))

    async def test_volume_invalid_percent_and_closed_service_never_prepare(self):
        for percent in (True, 1.5, float("nan"), -1, 101):
            result = await self.volume_invoke(self.volume_request(percent))
            self.assertEqual(result["error_kind"], "invalid_arguments")
        with self.assertRaises(vol.Invalid):
            await self.volume_invoke({**self.volume_request(), "extra": True})
        with self.assertRaises(vol.Invalid):
            await self.volume_invoke({**self.volume_request(), "action": "resume"})
        self.assertEqual(
            (await self.volume_invoke(self.volume_request(args={"delta": 2})))[
                "outcome"
            ],
            "rejected",
        )
        self.assertEqual(self.volume_posts, [])
        self.assertEqual(self.manual_percent, 27)

    async def test_volume_state_malformed_revision_or_capability_denies(self):
        for corrupt in (
            lambda v: v.update(extra=True),
            lambda v: v["volume_state"].update(base_percent=True),
            lambda v: v["volume_state"].update(base_revision="07"),
            lambda v: v["route"].pop("input_path_digest"),
            lambda v: v.update(actions=["resume"]),
        ):
            self.volume_state_corrupt = corrupt
            self.assertFalse((await self.volume_offer())["ok"])
        self.volume_state_corrupt = lambda v: v.update(actions=[])
        self.assertEqual((await self.volume_offer())["actions"], [])
        self.assertEqual(self.volume_posts, [])

    async def test_volume_foreign_native_or_mutated_snapshot_never_prepares(self):
        for change in (
            lambda r: r["native"].update(account_user_id="foreign"),
            lambda r: r["route"].update(input_path_digest="invalid"),
            lambda r: r["volume_state"].update(base_percent=False),
        ):
            request = self.volume_request()
            change(request)
            self.assertEqual((await self.volume_invoke(request))["outcome"], "rejected")
        self.assertEqual(self.volume_posts, [])

    async def test_volume_held_prepare_poll_owner_or_account_change_denies(self):
        for operation in ("poll", "owner", "account"):
            self.volume_posts.clear()
            self.volume_entered.clear()
            self.volume_release.clear()
            self.volume_hold = True
            data = self.entries[0].runtime_data
            data.coordinator.owner_epoch = data.commands.owner_epoch
            await data.coordinator.async_refresh()
            task = self._task(self.volume_invoke(self.volume_request()))
            await asyncio.wait_for(self.volume_entered.wait(), 2)
            if operation == "poll":
                await data.coordinator.async_refresh()
            elif operation == "owner":
                data.coordinator.owner_epoch = "replacement"
            else:
                self.hass.config_entries.async_update_entry(
                    self.entries[0], unique_id="other-account"
                )
            self.volume_release.set()
            result = await task
            self.assertEqual(result["outcome"], "rejected")
            await self.cleanup_finished()
            self.assertEqual(self.commits(), [])
        self.assertEqual(self.manual_percent, 27)
        self.assertEqual(self._writes(), [])

    async def test_volume_changed_prepare_binding_or_percent_never_commits(self):
        for corrupt in (
            lambda v: v["native"].update(alias_revision=8),
            lambda v: v["route"].update(input_path_digest="d" * 64),
            lambda v: v["volume_state"].update(attention_revision="9"),
            lambda v: v.update(volume_percent=42),
            lambda v: v.update(expires_after_ms=5001),
        ):
            self.volume_prepare_corrupt = corrupt
            await self.entries[0].runtime_data.coordinator.async_refresh()
            result = await self.volume_invoke(self.volume_request())
            self.assertEqual(result["outcome"], "rejected")
            await self.cleanup_finished()
        self.assertEqual(self.commits(), [])
        self.assertEqual(self.manual_percent, 27)

    async def test_volume_expired_prepare_is_cancelled_without_dispatch(self):
        self.volume_hold = True
        self.volume_ttl = 1
        task = self._task(self.volume_invoke(self.volume_request()))
        await asyncio.wait_for(self.volume_entered.wait(), 2)
        await asyncio.sleep(0.01)
        self.volume_release.set()
        self.assertEqual((await task)["error_kind"], "room_volume_expired")
        await self.cleanup_finished()
        self.assertEqual(self.commits(), [])
        self.assertTrue(any(path.endswith("cancel") for path, _ in self.volume_posts))

    async def test_volume_pending_and_late_ack_cannot_upgrade_unknown_receipt(self):
        self.volume_outcome = "reserved"
        request = self.volume_request()
        result = await self.volume_invoke(request)
        self.assertEqual(result["outcome"], "unknown")
        self.assertFalse(result["protocol_accepted"])
        self.assertTrue(result["possible_effect"])
        await self.cleanup_finished()
        self.volume_outcome = "protocol_accepted"
        observed = await self.entries[0].runtime_data.speakers.room_volume_result(
            "c" * 32
        )
        self.assertEqual(observed["state"], "protocol_accepted")
        self.assertEqual(await self.volume_invoke(request), result)
        self.assertEqual(len(self.commits()), 1)
        self.assertEqual(self.manual_percent, 27)

    async def test_volume_postdispatch_errors_are_consumed_shared_unknown(self):
        self.volume_status = 502
        request = self.volume_request()
        result = await self.volume_invoke(request)
        self.assertEqual(result["outcome"], "unknown")
        self.assertTrue(result["possible_effect"])
        self.assertFalse(result["retry_allowed"])
        await self.cleanup_finished()
        self.assertEqual(await self.volume_invoke(request), result)
        self.assertEqual(
            (
                await self.invoke(
                    self.request(
                        "volume",
                        args={"percent": 41},
                        command_sequence=request["command_sequence"],
                    )
                )
            )["error_kind"],
            "request_reused",
        )
        self.entries[0].runtime_data.commands.receipts.clear()
        self.assertEqual(
            (await self.volume_invoke(request))["error_kind"], "stale_command"
        )
        self.assertEqual(len(self.commits()), 1)
        self.assertEqual(self._writes(), [])

    async def test_volume_malformed_ack_is_unknown_not_physical_success(self):
        for corrupt in (
            lambda v: v.update(extra=True),
            lambda v: v.update(physical_result_verified=True),
            lambda v: v.update(protocol_accepted=False),
            lambda v: v.update(possible_effect=False),
            lambda v: v.update(contexts_drained=False),
        ):
            self.volume_ack_corrupt = corrupt
            await self.entries[0].runtime_data.coordinator.async_refresh()
            result = await self.volume_invoke(self.volume_request())
            self.assertEqual(result["outcome"], "unknown")
            self.assertFalse(result["protocol_accepted"])
            self.assertTrue(result["possible_effect"])
            await self.cleanup_finished()
        self.assertEqual(self._writes(), [])

    async def test_volume_caller_cancel_after_dispatch_cancels_exact_operation_once(
        self,
    ):
        self.volume_commit_hold = True
        request = self.volume_request()
        task = self._task(self.volume_invoke(request))
        await asyncio.wait_for(self.volume_commit_entered.wait(), 2)
        task.cancel()
        with self.assertRaises(asyncio.CancelledError):
            await task
        result = await self.volume_invoke(request)
        self.assertEqual(result["outcome"], "unknown")
        self.assertTrue(result["possible_effect"])
        await self.cleanup_finished()
        cancels = [body for path, body in self.volume_posts if path.endswith("cancel")]
        self.assertEqual(cancels, [{"prepare_id": "c" * 32}])
        self.assertEqual(len(self.commits()), 1)
        self.assertEqual(self.manual_percent, 27)
        self.volume_commit_release.set()

    async def playback_controls_snapshot(self, **overrides):
        self.output_observation = True
        return await self.hass.services.async_call(
            "podconnect",
            "get_playback_snapshot",
            {
                "config_entry_id": "account-alpha",
                "include_output": True,
                "include_controls": True,
                **overrides,
            },
            blocking=True,
            return_response=True,
        )

    async def test_volume_playback_v3_exact_read_only_projection(self):
        commands = self.volume_commands
        before = copy.deepcopy(
            (
                commands.command_sequence,
                commands.receipts,
                commands.room_content.records,
                commands.room_move.records,
                commands.room_queue.preparations,
            )
        )
        captured = []
        original = self.entries[0].runtime_data.observations.playback

        def capture(entry):
            value = original(entry)
            captured.append(copy.deepcopy(value))
            return value

        with patch.object(
            self.entries[0].runtime_data.observations, "playback", capture
        ):
            value = await self.playback_controls_snapshot()
        self.assertEqual(value["schema"], "podconnect.playback.v3")
        self.assertEqual(set(value), set(captured[0]) | {"output", "controls"})
        self.assertEqual(
            {
                k: v
                for k, v in value.items()
                if k not in {"schema", "output", "controls"}
            },
            {k: v for k, v in captured[0].items() if k != "schema"},
        )
        controls = value["controls"]
        self.assertEqual(
            set(controls),
            {
                "status",
                "reason",
                "binding_id",
                "valid_until",
                "supported_actions",
                "volume",
            },
        )
        self.assertEqual((controls["status"], controls["reason"]), ("observed", None))
        binding = value["output"]["binding"]
        self.assertEqual(controls["binding_id"], binding["binding_id"])
        self.assertLessEqual(controls["valid_until"], binding["valid_until"])
        self.assertEqual(
            controls["volume"], {"base_percent": 27, "effective_percent": 10}
        )
        self.assertEqual(
            set(controls["supported_actions"]),
            {"pause", "resume", "next", "previous", "volume"},
        )
        self.assertEqual(len(controls["supported_actions"]), 5)
        self.assertEqual(
            self.volume_state_reads,
            [
                {
                    k: self.native()[k]
                    for k in ("account_user_id", "target_id", "room_id", "homepod_id")
                }
            ],
        )
        self.assertEqual(
            before,
            (
                commands.command_sequence,
                commands.receipts,
                commands.room_content.records,
                commands.room_move.records,
                commands.room_queue.preparations,
            ),
        )
        self.assertEqual((self.room_posts, self.volume_posts, self.calls), ([], [], []))
        self.assertEqual((self.native_prepares, self.native_commits), (0, 0))

    async def test_volume_playback_v3_requires_output_and_keeps_v1_v2(self):
        for overrides in (
            {"include_output": False},
            {"include_controls": 1},
            {"include_controls": "true"},
            {"include_controls": None},
        ):
            with self.subTest(overrides=overrides), self.assertRaises(vol.Invalid):
                await self.playback_controls_snapshot(**overrides)
        with self.assertRaises(vol.Invalid):
            await self.hass.services.async_call(
                "podconnect",
                "get_playback_snapshot",
                {"config_entry_id": "account-alpha", "include_controls": True},
                blocking=True,
                return_response=True,
            )
        self.assertEqual((self.output_reads, self.volume_state_reads), ([], []))
        v1 = await self.output_snapshot(False)
        v2 = await self.output_snapshot(True)
        explicit_v2 = await self.playback_controls_snapshot(include_controls=False)
        self.assertEqual(v1["schema"], "podconnect.playback.v1")
        self.assertEqual(v2["schema"], "podconnect.playback.v2")
        self.assertEqual(explicit_v2["schema"], "podconnect.playback.v2")
        self.assertEqual(set(v2), set(v1) | {"output"})
        self.assertEqual(set(explicit_v2), set(v2))
        self.assertNotIn("controls", v1)
        self.assertNotIn("controls", v2)
        self.assertEqual(self.volume_state_reads, [])
        self.assertEqual((self.room_posts, self.volume_posts, self.calls), ([], [], []))

    async def test_volume_playback_v3_intersects_native_provider_actions(self):
        provider = self.entries[0].runtime_data.coordinator.data["playback"]
        provider["actions"]["disallows"] = {"pausing": True, "skipping_prev": True}
        self.state_corrupt = lambda state: state.update(actions=["pause", "next"])
        self.volume_commands.busy = True
        value = await self.playback_controls_snapshot()
        self.assertEqual(value["controls"]["status"], "observed")
        self.assertEqual(
            set(value["controls"]["supported_actions"]), {"next", "volume"}
        )
        self.assertEqual(
            value["controls"]["volume"], {"base_percent": 27, "effective_percent": 10}
        )
        self.assertEqual((self.room_posts, self.volume_posts, self.calls), ([], [], []))

    async def test_volume_playback_v3_actionless_facts_keep_volume(self):
        self.state_corrupt = lambda state: state.update(actions=[])
        self.volume_state_corrupt = lambda state: state.update(actions=[])
        provider = self.entries[0].runtime_data.coordinator.data["playback"]
        provider["actions"]["disallows"] = {
            "pausing": True,
            "resuming": True,
            "skipping_next": True,
            "skipping_prev": True,
        }
        provider["device"]["supports_volume"] = False
        value = await self.playback_controls_snapshot()
        self.assertEqual(value["output"]["status"], "observed")
        self.assertEqual(value["controls"]["status"], "observed")
        self.assertEqual(value["controls"]["supported_actions"], [])
        self.assertEqual(
            value["controls"]["volume"], {"base_percent": 27, "effective_percent": 10}
        )
        self.assertEqual((self.room_posts, self.volume_posts, self.calls), ([], [], []))

    async def test_volume_playback_v3_foreign_native_or_route_never_publishes_volume(
        self,
    ):
        for corrupt in (
            lambda state: state["native"].update(account_user_id="foreign"),
            lambda state: state["native"].update(alias_revision=8),
            lambda state: state["route"].update(input_generation="9"),
        ):
            with self.subTest(corrupt=corrupt):
                self.volume_state_corrupt = corrupt
                value = await self.playback_controls_snapshot()
                self.assertIsNone(value["controls"]["volume"])
                self.assertNotIn("volume", value["controls"]["supported_actions"])
        self.assertEqual((self.room_posts, self.volume_posts, self.calls), ([], [], []))

    async def test_volume_playback_v3_held_read_poll_or_owner_change_fails_closed(self):
        data = self.entries[0].runtime_data
        coordinator = data.coordinator
        baseline, owner = copy.deepcopy(coordinator.data), coordinator.owner_epoch
        for change in ("poll", "owner", "inactive", "expiry"):
            with self.subTest(change=change):
                coordinator.data = copy.deepcopy(baseline)
                coordinator.owner_epoch = owner
                self.volume_state_entered.clear()
                self.volume_state_release.clear()
                self.volume_state_hold = True
                task = self._task(self.playback_controls_snapshot())
                await asyncio.wait_for(self.volume_state_entered.wait(), 2)
                try:
                    if change == "poll":
                        coordinator.data["poll_sequence"] += 1
                    elif change == "owner":
                        coordinator.owner_epoch = "replacement"
                    elif change == "inactive":
                        data.active = False
                    if change == "expiry":
                        with (
                            patch(
                                "custom_components.podconnect.room_transport.time"
                            ) as clock,
                            patch(
                                "custom_components.podconnect.observations.time", clock
                            ),
                        ):
                            clock.monotonic.return_value = time.monotonic() + 6
                            clock.time.return_value = time.time() + 6
                            self.assertTrue(
                                data.observations.playback("account-alpha")["ok"]
                            )
                            self.volume_state_release.set()
                            result = await task
                    else:
                        self.volume_state_release.set()
                        result = await task
                    self.assertEqual(result, {"ok": False, "error_kind": "unavailable"})
                finally:
                    data.active = True
                    coordinator.owner_epoch = owner
                    self.volume_state_release.set()
        self.assertEqual((self.room_posts, self.volume_posts, self.calls), ([], [], []))

    async def test_volume_playback_v3_malformed_volume_has_no_provider_fallback(self):
        for corrupt in (
            lambda state: state["volume_state"].update(base_percent=True),
            lambda state: state["volume_state"].update(effective_percent=101),
            lambda state: state["volume_state"].pop("base_percent"),
            lambda state: state["volume_state"].update(base_revision="07"),
        ):
            with self.subTest(corrupt=corrupt):
                self.volume_state_corrupt = corrupt
                value = await self.playback_controls_snapshot()
                self.assertIsNone(value["controls"]["volume"])
                self.assertNotIn("volume", value["controls"]["supported_actions"])
        self.assertEqual((self.room_posts, self.volume_posts, self.calls), ([], [], []))

    async def test_volume_playback_v3_read_cancellation_propagates(self):
        self.volume_state_hold = True
        task = self._task(self.playback_controls_snapshot())
        await asyncio.wait_for(self.volume_state_entered.wait(), 2)
        task.cancel()
        with self.assertRaises(asyncio.CancelledError):
            await task
        self.volume_state_release.set()
        self.assertEqual((self.room_posts, self.volume_posts, self.calls), ([], [], []))
        self.assertEqual((self.native_prepares, self.native_commits), (0, 0))


def load_tests(loader, tests, pattern):
    return unittest.TestSuite(
        RoomVolumeFrameworkTests(name)
        for name in loader.getTestCaseNames(RoomVolumeFrameworkTests)
        if name.startswith("test_volume_")
    )
