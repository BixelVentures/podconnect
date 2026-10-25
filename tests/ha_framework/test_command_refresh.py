"""Actual HA coordinator/HTTP causality; no provider effects outside loopback."""

import asyncio
import unittest

from test_room_volume import RoomVolumeFrameworkTests


class CommandRefreshFrameworkTests(RoomVolumeFrameworkTests):
    async def asyncSetUp(self):
        self.refresh_playback_delay = 0.0
        self.refresh_devices_delay = 0.0
        self.refresh_provider_lag = False
        self.refresh_native_paused = False
        self.refresh_hold = False
        self.refresh_entered = asyncio.Event()
        self.refresh_release = asyncio.Event()
        self.playback_reads = 0
        self.active_reads = 0
        self.max_active_reads = 0
        await super().asyncSetUp()
        self.holds.append(self.refresh_release)
        self.playing = True
        await self.entries[0].runtime_data.coordinator.async_refresh()
        self.playback_reads = 0
        self.refresh_entered.clear()

    async def _backend(self, request):
        if request.path == "/spotify/me/player/devices" and request.method == "GET":
            await asyncio.sleep(self.refresh_devices_delay)
        if request.path == "/spotify/me/player" and request.method == "GET":
            self.playback_reads += 1
            self.active_reads += 1
            self.max_active_reads = max(self.max_active_reads, self.active_reads)
            try:
                await asyncio.sleep(self.refresh_playback_delay)
                response = await super()._backend(request)
                self.refresh_entered.set()
                if self.refresh_hold:
                    await self.refresh_release.wait()
                return response
            finally:
                self.active_reads -= 1
        response = await super()._backend(request)
        if request.path == "/api/room-transport/commit" and response.status == 200:
            # Actual inert backend changes its source state only after admission.
            action = next(
                body["action"]
                for path, body in reversed(self.room_posts)
                if path.endswith("prepare")
            )
            if action in {"pause", "resume"}:
                self.refresh_native_paused = action == "pause"
                if not self.refresh_provider_lag:
                    self.playing = action == "resume"
        return response

    async def demand_done(self):
        task = self.volume_commands.data.coordinator._command_refresh_task
        if task is not None:
            await asyncio.wait_for(asyncio.shield(task), 2)

    async def test_refresh_real_pause_offer_resume_volume_burst_under_one_second(self):
        start = asyncio.get_running_loop().time()
        pause = await self.room_invoke(self.room_request("pause"))
        self.assertEqual(pause["outcome"], "accepted")
        offered = await self.room_offer()
        self.assertIn("resume", offered["actions"])
        self.assertGreater(
            offered["poll_sequence"], pause["observe_after_poll_sequence"]
        )
        self.assertEqual(
            self.entries[0].runtime_data.coordinator.data["playback"]["is_playing"],
            False,
        )
        resume = await self.room_invoke(self.room_request("resume"))
        self.assertEqual(resume["outcome"], "accepted")
        self.assertTrue(
            self.entries[0].runtime_data.coordinator.data["playback"]["is_playing"]
        )
        volume_offer = await self.volume_offer()
        self.assertEqual(volume_offer["actions"], ["volume"])
        self.assertGreater(
            volume_offer["poll_sequence"], resume["observe_after_poll_sequence"]
        )
        volume = await self.volume_invoke(self.volume_request(41))
        self.assertEqual(volume["outcome"], "accepted")
        elapsed = asyncio.get_running_loop().time() - start
        self.assertLess(elapsed, 1.0)
        self.assertEqual(self.playback_reads, 3)
        self.assertEqual(self.max_active_reads, 1)
        self.assertEqual(self._writes(), [])
        self.assertEqual(self.manual_percent, 27)
        print(f"INERT_BURST_SECONDS={elapsed:.6f}", flush=True)

    async def test_refresh_slow_provider_stays_awaiting_until_real_response(self):
        self.refresh_hold = True
        result = await self.room_invoke(self.room_request("pause"))
        self.assertEqual(result["outcome"], "accepted")
        offered = await self.room_offer()
        self.assertEqual(offered["status"], "awaiting_observation")
        self.assertEqual(offered["actions"], [])
        self.assertLessEqual(
            offered["poll_sequence"], result["observe_after_poll_sequence"]
        )
        self.assertTrue(
            self.entries[0].runtime_data.coordinator.data["playback"]["is_playing"]
        )
        self.refresh_release.set()
        await self.demand_done()
        fresh = await self.room_offer()
        self.assertIn("resume", fresh["actions"])
        self.assertGreater(
            fresh["poll_sequence"], result["observe_after_poll_sequence"]
        )
        self.assertEqual(self.playback_reads, 1)
        self.assertEqual(len(self.room_posts), 2)

    async def test_refresh_scheduled_poll_serializes_before_demand_without_regression(
        self,
    ):
        data = self.entries[0].runtime_data
        coordinator = data.coordinator
        published = []
        remove = coordinator.async_add_listener(
            lambda: published.append(coordinator.data["poll_sequence"])
        )
        self.addCleanup(remove)
        self.refresh_hold = True
        old_poll = self._task(coordinator._async_refresh(scheduled=True))
        await asyncio.wait_for(self.refresh_entered.wait(), 2)
        result = await self.room_invoke(self.room_request("pause"))
        self.assertEqual((await self.room_offer())["status"], "awaiting_observation")
        self.assertEqual(self.playback_reads, 1)
        self.refresh_release.set()
        await old_poll
        await self.demand_done()
        self.assertEqual(self.playback_reads, 2)
        self.assertEqual(self.max_active_reads, 1)
        self.assertEqual(published, sorted(published))
        self.assertGreater(
            coordinator.data["poll_sequence"], result["observe_after_poll_sequence"]
        )
        self.assertFalse(coordinator.data["playback"]["is_playing"])
        self.assertIn("resume", (await self.room_offer())["actions"])

    async def test_refresh_requests_coalesce_and_cancelled_waiter_does_not_cancel_poll(
        self,
    ):
        coordinator = self.entries[0].runtime_data.coordinator
        self.refresh_hold = True
        command = self._task(self.room_invoke(self.room_request("pause")))
        await asyncio.wait_for(self.refresh_entered.wait(), 2)
        waiters = [
            self._task(coordinator.async_refresh_after_command()) for _ in range(6)
        ]
        await asyncio.sleep(0)
        waiters[0].cancel()
        with self.assertRaises(asyncio.CancelledError):
            await waiters[0]
        self.assertFalse(coordinator._command_refresh_task.cancelled())
        self.refresh_release.set()
        self.assertEqual((await command)["outcome"], "accepted")
        await asyncio.gather(*waiters[1:])
        self.assertEqual(self.playback_reads, 1)
        self.assertEqual(self.max_active_reads, 1)
        self.assertIn("resume", (await self.room_offer())["actions"])

    async def test_refresh_owner_replacement_during_read_never_publishes_old_owner(
        self,
    ):
        data = self.entries[0].runtime_data
        before = data.coordinator.data["poll_sequence"]
        self.refresh_hold = True
        request = self.room_request("pause")
        task = self._task(self.room_invoke(request))
        await asyncio.wait_for(self.refresh_entered.wait(), 2)
        data.coordinator.owner_epoch = "replacement"
        self.refresh_release.set()
        self.assertEqual((await task)["outcome"], "accepted")
        await self.demand_done()
        self.assertEqual(data.coordinator.data["poll_sequence"], before)
        self.assertFalse(data.coordinator.last_update_success)
        self.assertEqual(len(self.room_posts), 2)
        self.assertEqual(
            await self.room_invoke(request),
            data.commands.receipts[request["command_sequence"]][1],
        )

    async def test_refresh_cancelled_command_waiter_preserves_receipt_and_owned_read(
        self,
    ):
        self.refresh_hold = True
        request = self.room_request("pause")
        task = self._task(self.room_invoke(request))
        await asyncio.wait_for(self.refresh_entered.wait(), 2)
        task.cancel()
        with self.assertRaises(asyncio.CancelledError):
            await task
        cached = self.entries[0].runtime_data.commands.receipts[
            request["command_sequence"]
        ][1]
        self.assertEqual(cached["outcome"], "accepted")
        self.refresh_release.set()
        await self.demand_done()
        self.assertEqual(await self.room_invoke(request), cached)
        self.assertEqual(self.playback_reads, 1)
        self.assertIn("resume", (await self.room_offer())["actions"])
        self.assertEqual(len(self.room_posts), 2)

    async def test_refresh_timeout_is_bounded_and_never_fabricates_ready_or_retries(
        self,
    ):
        self.refresh_hold = True
        request = self.room_request("pause")
        result = await self.room_invoke(request)
        await self.demand_done()
        coordinator = self.entries[0].runtime_data.coordinator
        self.assertTrue(coordinator._command_refresh_task.done())
        self.assertLessEqual(
            coordinator.data["poll_sequence"], result["observe_after_poll_sequence"]
        )
        unavailable = await self.room_offer()
        self.assertFalse(unavailable.get("ok"))
        self.assertEqual(self.playback_reads, 1)
        self.assertEqual(len(self.room_posts), 2)
        self.refresh_release.set()
        self.assertEqual(await self.room_invoke(request), result)

    async def test_refresh_two_healthy_600ms_reads_publish_inside_existing_budget(self):
        self.refresh_playback_delay = 0.6
        self.refresh_devices_delay = 0.6
        started = asyncio.get_running_loop().time()
        pause = await self.room_invoke(self.room_request("pause"))
        self.assertEqual(pause["outcome"], "accepted")
        await self.demand_done()
        coordinator = self.entries[0].runtime_data.coordinator
        print(
            "HEALTHY_600MS_READS",
            {
                "elapsed": asyncio.get_running_loop().time() - started,
                "published_poll": coordinator.data["poll_sequence"],
                "floor": pause["observe_after_poll_sequence"],
                "last_update_success": coordinator.last_update_success,
            },
            flush=True,
        )
        self.assertGreater(
            coordinator.data["poll_sequence"], pause["observe_after_poll_sequence"]
        )
        self.assertTrue(coordinator.last_update_success)
        self.assertIn("resume", (await self.room_offer())["actions"])

    async def test_refresh_exact_native_pause_overrides_provider_phase_lag_only(self):
        self.refresh_provider_lag = True

        def native_actions(value):
            if self.refresh_native_paused:
                value["actions"] = ["resume", "next", "previous"]

        self.state_corrupt = native_actions
        pause = await self.room_invoke(self.room_request("pause"))
        self.assertEqual(pause["outcome"], "accepted")
        await self.demand_done()
        offered = await self.room_offer()
        paused_before_resume = self.refresh_native_paused
        self.assertTrue(paused_before_resume)
        resume = await self.room_invoke(self.room_request("resume"))
        print(
            "EXACT_NATIVE_PROVIDER_PHASE_LAG",
            {
                "provider_playing": self.playing,
                "native_paused_before_resume": paused_before_resume,
                "actions": offered["actions"],
                "resume_outcome": resume["outcome"],
                "resume_error": resume.get("error_kind"),
                "manager_posts": len(self.room_posts),
            },
            flush=True,
        )
        self.assertIn("resume", offered["actions"])
        self.assertEqual(resume["outcome"], "accepted")

    async def test_refresh_native_phase_keeps_provider_disallows_and_generic_phase(
        self,
    ):
        self.state_corrupt = lambda value: value.update(actions=["resume"])
        data = self.entries[0].runtime_data
        generic = await self.invoke(self.request("resume"))
        self.assertEqual(generic["error_kind"], "unsupported_action")
        self.assertEqual(self.room_posts, [])
        data.coordinator.data["playback"]["actions"]["disallows"]["resuming"] = True
        self.assertNotIn("resume", (await self.room_offer())["actions"])
        denied = await self.room_invoke(self.room_request("resume"))
        self.assertEqual(denied["error_kind"], "unsupported_action")
        self.assertEqual(self.room_posts, [])
        self.assertEqual(self._writes(), [])

    async def test_refresh_native_phase_current_poll_still_required_before_commit(self):
        self.state_corrupt = lambda value: value.update(actions=["resume"])
        self.room_hold = True
        task = self._task(self.room_invoke(self.room_request("resume")))
        await asyncio.wait_for(self.room_entered.wait(), 2)
        await self.entries[0].runtime_data.coordinator.async_refresh()
        self.room_release.set()
        result = await task
        self.assertEqual(result["error_kind"], "stale_observation")
        self.assertEqual(len(self.room_posts), 1)
        self.assertEqual(self._writes(), [])

    async def test_refresh_native_phase_foreign_current_room_never_commits(self):
        self.state_corrupt = lambda value: value.update(actions=["resume"])
        self.room_hold = True
        task = self._task(self.room_invoke(self.room_request("resume")))
        await asyncio.wait_for(self.room_entered.wait(), 2)
        self.native_source_room = "room-beta"
        self.room_release.set()
        result = await task
        self.assertEqual(result["outcome"], "rejected")
        self.assertEqual(len(self.room_posts), 1)
        self.assertEqual(self._writes(), [])


def load_tests(loader, tests, pattern):
    return unittest.TestSuite(
        CommandRefreshFrameworkTests(name)
        for name in loader.getTestCaseNames(CommandRefreshFrameworkTests)
        if name.startswith("test_refresh_")
    )
