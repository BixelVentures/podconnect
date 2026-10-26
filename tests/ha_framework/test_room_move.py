"""Actual HA public split services; native/manager effects are inert HTTP fixtures."""

import asyncio
import copy
import hashlib
import json
import unittest
from unittest.mock import AsyncMock, patch

import voluptuous as vol
from aiohttp import web
from test_room_content import RoomContentFrameworkTests

from custom_components.podconnect.room_move import valid_move_receipt


class RoomMoveFrameworkTests(RoomContentFrameworkTests):
    async def test_current_completed_move_rejects_later_operation(self):
        request = self.move_request()
        self.assertEqual(
            (await self.move_invoke("prepare_room_move_playback", request))["state"],
            "staged",
        )
        receipt = await self.move_invoke("move_room_playback", request)
        self.assertEqual(receipt["outcome"], "accepted")
        data = self.entries[0].runtime_data
        native = {
            **self.native(),
            "target_id": request["target_id"],
            "room_id": request["room_id"],
            "homepod_id": request["homepod_id"],
        }
        route = {**self.route(), "device_id": request["homepod_id"]}
        identity = [request["config_entry_id"], request["owner_epoch"], native, route]
        binding_id = hashlib.sha256(
            json.dumps(
                identity, sort_keys=True, separators=(",", ":"), allow_nan=False
            ).encode()
        ).hexdigest()
        projection = {
            "output": {
                "binding": {
                    "target_id": request["target_id"],
                    "native_room_id": request["room_id"],
                    "homepod_id": request["homepod_id"],
                    "binding_id": binding_id,
                }
            }
        }
        witness = {
            "schema": "podconnect.native_completed_current.v1",
            "offer_id": request["move_offer"]["offer_id"],
            "binding_id": request["move_offer"]["binding_id"],
            "operation_id": request["move_offer"]["offer_id"],
            "native": native,
            "route": route,
            "target": {
                "process": route["process"],
                "device_id": route["device_id"],
                "device_incarnation": route["device_incarnation"],
            },
            "physical_result_verified": False,
        }
        with (
            patch.object(
                data.speakers, "room_move_current", AsyncMock(return_value=witness)
            ),
            patch.object(
                data.commands.room_transport,
                "observe",
                AsyncMock(return_value={"native": native, "route": route}),
            ),
        ):

            async def read():
                return await data.commands.settlement(
                    self.hass,
                    request["config_entry_id"],
                    request["request_id"],
                    request["command_sequence"],
                    projection,
                )

            self.assertEqual((await read())["status"], "confirmed")
            witness["operation_id"] = "f" * 32
            self.assertEqual((await read())["status"], "unknown")
            witness["operation_id"] = request["move_offer"]["offer_id"]
            data.commands.command_sequence += 1
            self.assertEqual((await read())["status"], "superseded")

    async def asyncSetUp(self):
        self.move_posts = []
        self.move_hold = False
        self.move_entered = asyncio.Event()
        self.move_release = asyncio.Event()
        self.move_commit_hold = False
        self.move_commit_entered = asyncio.Event()
        self.move_commit_release = asyncio.Event()
        self.move_prepare_status = 200
        self.move_commit_status = 200
        self.move_commit_corrupt = None
        self.move_offer_id = "a" * 32
        self.move_target = "native-owned-engine"
        await super().asyncSetUp()
        self.holds.extend((self.move_release, self.move_commit_release))
        self.move_commands = self.entries[0].runtime_data.commands

    def move_offer_value(self):
        return {
            "schema": "podconnect.room_output_move_offer.v1",
            "offer_id": self.move_offer_id,
            "binding_id": "b" * 64,
            "native_incarnation": "c" * 32,
            "policy_revision": "d" * 64,
            "session_volume_percent": 27,
            "expires_after_ms": 5000,
        }

    def move_result(self, suffix, state):
        complete = state == "complete"
        return {
            "schema": "podconnect.room_move_" + suffix + ".v1",
            "ok": complete,
            "offer_id": self.move_offer_id,
            "binding_id": "b" * 64,
            "state": state,
            "accepted_room_move": complete,
            "move_operation_id": self.move_offer_id,
            "session_volume_percent": 27,
            "room_id": "room-beta",
            "homepod_id": "456",
            "physical_result_verified": False,
        }

    async def _backend(self, request):
        if request.path == "/api/room-move/state":
            if request.query.get("target_id") != self.move_target:
                return web.Response(status=409)
            return web.json_response(
                {
                    "schema": "podconnect.room_move_state.v1",
                    "ok": True,
                    "move_offer": self.move_offer_value(),
                    "actions": ["move_existing_session"],
                    "physical_result_verified": False,
                }
            )
        if request.path == "/api/room-move/result":
            return web.json_response(self.move_result("result", "staged"))
        if request.path.startswith("/api/room-move/"):
            body = await request.json()
            self.move_posts.append((request.path, copy.deepcopy(body)))
            if request.path.endswith("prepare"):
                self.move_entered.set()
                if self.move_hold:
                    await self.move_release.wait()
                if self.move_prepare_status != 200:
                    return web.Response(status=self.move_prepare_status)
                return web.json_response(self.move_result("prepare", "staged"))
            if request.path.endswith("cancel"):
                return web.json_response(self.move_result("cancel", "cancelled"))
            self.move_commit_entered.set()
            if self.move_commit_hold:
                await self.move_commit_release.wait()
            if self.move_commit_status != 200:
                return web.Response(status=self.move_commit_status)
            result = self.move_result("commit", "complete")
            if self.move_commit_corrupt:
                self.move_commit_corrupt(result)
            return web.json_response(result)
        return await super()._backend(request)

    def move_request(self, **overrides):
        return {
            **self.request("next"),
            "action": "move_existing_session",
            "target_id": self.move_target,
            "room_id": "room-beta",
            "homepod_id": "456",
            "args": {},
            "move_offer": self.move_offer_value(),
            **overrides,
        }

    async def move_invoke(self, service, request):
        return await self.hass.services.async_call(
            "podconnect", service, request, blocking=True, return_response=True
        )

    def prepares(self):
        return [body for path, body in self.move_posts if path.endswith("prepare")]

    def move_commits(self):
        return [body for path, body in self.move_posts if path.endswith("commit")]

    def cancel_args(self, request):
        return {
            **{
                k: request[k]
                for k in (
                    "config_entry_id",
                    "owner_epoch",
                    "command_sequence",
                    "request_id",
                )
            },
            "offer_id": request["move_offer"]["offer_id"],
            "binding_id": request["move_offer"]["binding_id"],
        }

    async def move_cleanup(self):
        tasks = list(self.move_commands.room_move.cleanups)
        if tasks:
            await asyncio.wait_for(asyncio.gather(*tasks), 2)

    async def test_move_read_exact_native_owner_without_webapi_volume_flag(
        self,
    ):
        data = self.entries[0].runtime_data
        data.coordinator.data["devices"] = []
        value = await self.move_invoke(
            "get_room_move_controls",
            {
                "config_entry_id": "account-alpha",
                "kind": "spotify_device",
                "target_id": self.move_target,
                "room_id": "room-beta",
                "homepod_id": "456",
            },
        )
        self.assertEqual(len(value), 14)
        self.assertEqual(
            value["target"],
            {"kind": "spotify_device", "target_id": self.move_target},
        )
        self.assertEqual(value["actions"], ["move_existing_session"])
        self.assertNotIn("volume_percent", value)
        self.assertNotIn("session_id", value)
        self.assertEqual(self.move_posts, [])
        response = await self.client.get("/api/services")
        services = next(
            row["services"]
            for row in await response.json()
            if row["domain"] == "podconnect"
        )
        for service in ("prepare_room_move_playback", "move_room_playback"):
            self.assertEqual(services[service]["response"], {"optional": False})
            self.assertEqual(set(services[service]["fields"]), set(self.move_request()))
        self.assertEqual(
            set(services["cancel_room_move_preparation"]["fields"]),
            set(self.cancel_args(self.move_request())),
        )

    async def test_move_stage_returns_to_caller_before_explicit_same_request_start(
        self,
    ):
        request = self.move_request()
        staged = await self.move_invoke("prepare_room_move_playback", request)
        self.assertEqual(len(staged), 13)
        self.assertEqual(staged["schema"], "podconnect.room_move_preparation.v1")
        self.assertEqual(staged["state"], "staged")
        self.assertEqual(self.move_commits(), [])
        # Actual trusted PV grant validation is a separate composed integration.
        result = await self.move_invoke("move_room_playback", request)
        identity = {
            k: result[k]
            for k in (
                "config_entry_id",
                "owner_epoch",
                "request_id",
                "command_sequence",
                "action",
                "target",
                "room_id",
                "homepod_id",
            )
        }
        self.assertTrue(valid_move_receipt(result, identity))
        self.assertTrue(result["accepted_room_move"])
        self.assertFalse(result["provider_request_accepted"])
        self.assertEqual(result["session_volume_percent"], 27)
        self.assertEqual(await self.move_invoke("move_room_playback", request), result)
        self.assertEqual(
            await self.move_invoke("prepare_room_move_playback", request), staged
        )
        self.assertEqual(len(self.prepares()), 1)
        self.assertEqual(
            self.move_commits(), [{"offer_id": "a" * 32, "binding_id": "b" * 64}]
        )
        self.assertEqual(self.prepares()[0]["args"], request["args"])
        self.assertEqual(self._writes(), [])
        self.assertEqual((self.native_prepares, self.native_commits), (0, 0))

    async def test_move_unprepared_changed_or_invalid_request_never_starts(self):
        request = self.move_request()
        self.assertEqual(
            (await self.move_invoke("move_room_playback", request))["error_kind"],
            "content_not_prepared",
        )
        for percent in (True, 34, 35.0, float("nan"), 101):
            bad = {
                **request,
                "args": {
                    "track_uri": "spotify:track:" + "A" * 22,
                    "max_volume": percent,
                },
            }
            self.assertFalse(
                (await self.move_invoke("prepare_room_move_playback", bad))["ok"]
            )
        with self.assertRaises(vol.Invalid):
            await self.move_invoke(
                "prepare_room_move_playback", {**request, "extra": True}
            )
        self.assertTrue(
            (await self.move_invoke("prepare_room_move_playback", request))["ok"]
        )
        changed = {**request, "room_id": "another-owned-destination"}
        self.assertEqual(
            (await self.move_invoke("move_room_playback", changed))["error_kind"],
            "request_reused",
        )
        self.assertEqual(self.move_commits(), [])

    async def test_move_delayed_stage_allows_genuine_advancing_poll_same_captured_owner(
        self,
    ):
        request = self.move_request()
        self.move_hold = True
        task = self._task(self.move_invoke("prepare_room_move_playback", request))
        await asyncio.wait_for(self.move_entered.wait(), 2)
        data = self.entries[0].runtime_data
        await data.coordinator.async_refresh()
        self.assertGreater(
            data.coordinator.data["poll_sequence"], request["expected_poll_sequence"]
        )
        self.assertEqual(self.move_commits(), [])
        self.move_release.set()
        self.assertTrue((await task)["ok"])
        result = await self.move_invoke("move_room_playback", request)
        self.assertEqual(result["outcome"], "accepted")
        self.assertEqual(len(self.move_commits()), 1)

    async def test_move_higher_intent_during_held_stage_is_not_blocked_and_retires_old(
        self,
    ):
        request = self.move_request()
        self.move_hold = True
        task = self._task(self.move_invoke("prepare_room_move_playback", request))
        await asyncio.wait_for(self.move_entered.wait(), 2)
        self.assertFalse(self.entries[0].runtime_data.commands.busy)
        higher = await self.invoke(self.request("next"))
        self.assertEqual(higher["outcome"], "accepted")
        self.move_release.set()
        self.assertFalse((await task)["ok"])
        rejected = await self.move_invoke("move_room_playback", request)
        self.assertEqual(rejected["outcome"], "rejected")
        self.assertEqual(self.move_commits(), [])
        await self.move_cleanup()

    async def test_move_concurrent_duplicate_prepares_coalesce_without_published_unknown(
        self,
    ):
        request = self.move_request()
        self.move_hold = True
        first = self._task(self.move_invoke("prepare_room_move_playback", request))
        await asyncio.wait_for(self.move_entered.wait(), 2)
        second = self._task(self.move_invoke("prepare_room_move_playback", request))
        await asyncio.sleep(0)
        self.assertFalse(second.done())
        self.move_release.set()
        self.assertEqual(await first, await second)
        self.assertEqual(len(self.prepares()), 1)
        self.assertEqual(self.move_commits(), [])

    async def test_move_replaced_owner_while_staging_denies_physical_commit(self):
        request = self.move_request()
        self.move_hold = True
        task = self._task(self.move_invoke("prepare_room_move_playback", request))
        await asyncio.wait_for(self.move_entered.wait(), 2)
        self.entries[0].runtime_data.coordinator.owner_epoch = "replacement"
        self.move_release.set()
        self.assertFalse((await task)["ok"])
        self.assertEqual(
            (await self.move_invoke("move_room_playback", request))["outcome"],
            "rejected",
        )
        self.assertEqual(self.move_commits(), [])
        await self.move_cleanup()

    async def test_move_revoked_task_cancels_after_stage_before_start(self):
        request = self.move_request()
        self.assertTrue(
            (await self.move_invoke("prepare_room_move_playback", request))["ok"]
        )
        cancelled = await self.move_invoke(
            "cancel_room_move_preparation", self.cancel_args(request)
        )
        self.assertTrue(cancelled["ok"])
        self.assertEqual(cancelled["state"], "cancelled")
        self.assertEqual(
            (await self.move_invoke("move_room_playback", request))["outcome"],
            "rejected",
        )
        self.assertEqual(self.move_commits(), [])

    async def test_move_prepare_unknown_cannot_be_upgraded_by_late_staging(self):
        self.move_prepare_status = 502
        request = self.move_request()
        unknown = await self.move_invoke("prepare_room_move_playback", request)
        self.assertEqual(unknown["state"], "unknown")
        self.move_prepare_status = 200
        self.assertEqual(
            await self.move_invoke("prepare_room_move_playback", request), unknown
        )
        self.assertEqual(
            (await self.move_invoke("move_room_playback", request))["outcome"],
            "rejected",
        )
        self.assertEqual(len(self.prepares()), 1)
        self.assertEqual(self.move_commits(), [])
        await self.move_cleanup()

    async def test_move_cancelled_prepare_never_auto_commits_on_late_response(self):
        request = self.move_request()
        self.move_hold = True
        task = self._task(self.move_invoke("prepare_room_move_playback", request))
        await asyncio.wait_for(self.move_entered.wait(), 2)
        task.cancel()
        with self.assertRaises(asyncio.CancelledError):
            await task
        self.move_release.set()
        replay = await self.move_invoke("prepare_room_move_playback", request)
        self.assertFalse(replay["ok"])
        self.assertEqual(replay["state"], "cancelled")
        self.assertEqual(self.move_commits(), [])
        await self.move_cleanup()

    async def test_move_missing_or_intermediate_ack_is_consumed_unknown_never_complete(
        self,
    ):
        for index, corrupt in enumerate(
            (
                lambda v: v.update(state="staged", ok=False, accepted_room_move=False),
                lambda v: v.update(accepted_room_move=False),
                lambda v: v.update(move_operation_id=None),
                lambda v: v.update(room_id="wrong"),
                lambda v: v.update(physical_result_verified=True),
            )
        ):
            self.move_offer_id = f"{index + 1:032x}"
            self.move_commit_corrupt = corrupt
            request = self.move_request()
            self.assertTrue(
                (await self.move_invoke("prepare_room_move_playback", request))["ok"]
            )
            result = await self.move_invoke("move_room_playback", request)
            self.assertEqual(result["outcome"], "unknown")
            self.assertFalse(result["accepted_room_move"])
            self.assertEqual(
                await self.move_invoke("move_room_playback", request),
                result,
            )
            await self.move_cleanup()
        self.assertEqual(len(self.move_commits()), 5)
        self.assertEqual(self._writes(), [])

    async def test_move_cancel_after_dispatch_seals_unknown_even_when_late_ack_completes(
        self,
    ):
        request = self.move_request()
        self.assertTrue(
            (await self.move_invoke("prepare_room_move_playback", request))["ok"]
        )
        self.move_commit_hold = True
        task = self._task(self.move_invoke("move_room_playback", request))
        await asyncio.wait_for(self.move_commit_entered.wait(), 2)
        task.cancel()
        with self.assertRaises(asyncio.CancelledError):
            await task
        result = await self.move_invoke("move_room_playback", request)
        self.assertEqual(result["outcome"], "unknown")
        self.move_commit_release.set()
        self.assertEqual(await self.move_invoke("move_room_playback", request), result)
        self.assertEqual(len(self.move_commits()), 1)
        await self.move_cleanup()

    async def test_move_concurrent_same_commit_waits_for_original_receipt_without_replay(
        self,
    ):
        request = self.move_request()
        self.assertTrue(
            (await self.move_invoke("prepare_room_move_playback", request))["ok"]
        )
        self.move_commit_hold = True
        first = self._task(self.move_invoke("move_room_playback", request))
        await asyncio.wait_for(self.move_commit_entered.wait(), 2)
        second = self._task(self.move_invoke("move_room_playback", request))
        await asyncio.sleep(0)
        self.assertFalse(second.done())
        self.move_commit_release.set()
        self.assertEqual(await first, await second)
        self.assertEqual(len(self.move_commits()), 1)

    async def test_move_explicit_cancel_during_commit_seals_identical_unknown(self):
        request = self.move_request()
        self.assertTrue(
            (await self.move_invoke("prepare_room_move_playback", request))["ok"]
        )
        self.move_commit_hold = True
        task = self._task(self.move_invoke("move_room_playback", request))
        await asyncio.wait_for(self.move_commit_entered.wait(), 2)
        cancelled = await self.move_invoke(
            "cancel_room_move_preparation", self.cancel_args(request)
        )
        self.assertEqual(cancelled["state"], "unknown")
        first_unknown = await self.move_invoke("move_room_playback", request)
        self.assertEqual(first_unknown["outcome"], "unknown")
        self.move_commit_release.set()
        self.assertEqual(await task, first_unknown)
        self.assertEqual(
            await self.move_invoke("move_room_playback", request),
            first_unknown,
        )
        self.assertEqual(len(self.move_commits()), 1)

    async def test_move_cancel_does_not_rewrite_published_preparation_unknown(self):
        self.move_prepare_status = 502
        request = self.move_request()
        original = await self.move_invoke("prepare_room_move_playback", request)
        self.assertEqual(original["state"], "unknown")
        await self.move_invoke(
            "cancel_room_move_preparation", self.cancel_args(request)
        )
        self.assertEqual(
            await self.move_invoke("prepare_room_move_playback", request),
            original,
        )
        self.assertEqual(self.move_commits(), [])
        await self.move_cleanup()

    async def test_move_transport_does_not_apply_short_legacy_timeout_to_commit(
        self,
    ):
        request = self.move_request()
        self.assertTrue(
            (await self.move_invoke("prepare_room_move_playback", request))["ok"]
        )
        self.move_commit_hold = True
        task = self._task(self.move_invoke("move_room_playback", request))
        await asyncio.wait_for(self.move_commit_entered.wait(), 2)
        await asyncio.sleep(2.1)
        self.assertFalse(task.done())
        self.move_commit_release.set()
        self.assertEqual((await task)["outcome"], "accepted")
        self.assertEqual(len(self.move_commits()), 1)

    async def test_move_transport_preserves_stage_budget_for_held_initial_reply(
        self,
    ):
        request = self.move_request()
        self.move_hold = True
        task = self._task(self.move_invoke("prepare_room_move_playback", request))
        await asyncio.wait_for(self.move_entered.wait(), 2)
        await asyncio.sleep(1.1)
        self.assertFalse(task.done())
        self.assertFalse(self.move_commands.busy)
        self.move_release.set()
        self.assertEqual((await task)["state"], "staged")
        self.assertEqual(self.move_commits(), [])
        await self.move_cleanup()

    async def test_move_complete_requires_bound_old_manual_base_not_target_or_fresh_policy(
        self,
    ):
        for index, wrong in enumerate((60, 35)):
            self.move_offer_id = f"{index + 100:032x}"
            self.move_commit_corrupt = lambda result, percent=wrong: result.update(
                session_volume_percent=percent
            )
            request = self.move_request()
            self.assertTrue(
                (await self.move_invoke("prepare_room_move_playback", request))["ok"]
            )
            result = await self.move_invoke("move_room_playback", request)
            self.assertEqual(result["outcome"], "unknown")
            self.assertFalse(result["accepted_room_move"])
            self.assertIsNone(result["session_volume_percent"])
            self.assertEqual(
                await self.move_invoke("move_room_playback", request), result
            )
            await self.move_cleanup()
        self.assertEqual(len(self.move_commits()), 2)
        self.assertEqual(self._writes(), [])

    async def test_move_and_selected_content_share_one_canonical_highwater_and_retirement(
        self,
    ):
        move = self.move_request()
        self.assertTrue(
            (await self.move_invoke("prepare_room_move_playback", move))["ok"]
        )
        content = self.content_request()
        self.assertEqual(content["command_sequence"], move["command_sequence"] + 1)
        self.assertTrue(
            (await self.content_invoke("prepare_room_selected_content", content))["ok"]
        )
        self.assertEqual(
            (await self.move_invoke("move_room_playback", move))["outcome"], "rejected"
        )
        newer_move = self.move_request()
        self.assertEqual(
            newer_move["command_sequence"], content["command_sequence"] + 1
        )
        self.assertTrue(
            (await self.move_invoke("prepare_room_move_playback", newer_move))["ok"]
        )
        self.assertEqual(
            (await self.content_invoke("start_room_selected_content", content))[
                "outcome"
            ],
            "rejected",
        )
        self.assertEqual(self.move_commits(), [])
        self.assertEqual(self.content_commits(), [])
        self.assertEqual(
            self.move_commands.command_sequence, newer_move["command_sequence"]
        )
        await self.move_cleanup()
        await self.content_cleanup()

    async def test_move_expired_staged_record_and_evicted_receipt_never_replay(self):
        request = self.move_request()
        self.assertTrue(
            (await self.move_invoke("prepare_room_move_playback", request))["ok"]
        )
        record = self.move_commands.room_move.records[request["command_sequence"]]
        record["staged_at"] -= 6
        self.assertEqual(
            (await self.move_invoke("move_room_playback", request))["error_kind"],
            "content_expired",
        )
        self.move_commands.receipts.clear()
        self.assertEqual(
            (await self.move_invoke("move_room_playback", request))["error_kind"],
            "stale_command",
        )
        self.assertEqual(self.move_commits(), [])
        await self.move_cleanup()


def load_tests(loader, tests, pattern):
    return unittest.TestSuite(
        RoomMoveFrameworkTests(name)
        for name in loader.getTestCaseNames(RoomMoveFrameworkTests)
        if name.startswith("test_move_")
    )
