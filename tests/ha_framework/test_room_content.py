"""Actual HA public split services; native/manager effects are inert HTTP fixtures."""

import asyncio
import copy
import hashlib
import json
import unittest
from unittest.mock import AsyncMock, patch

import voluptuous as vol
from aiohttp import web
from test_room_volume import RoomVolumeFrameworkTests

from custom_components.podconnect.room_content import (
    valid_selected_arguments,
    valid_selected_receipt,
)


class RoomContentFrameworkTests(RoomVolumeFrameworkTests):
    async def test_current_completed_content_requires_retained_receipt_and_fresh_route(
        self,
    ):
        request = self.content_request()
        self.assertEqual(
            (await self.content_invoke("prepare_room_selected_content", request))[
                "state"
            ],
            "staged",
        )
        receipt = await self.content_invoke("start_room_selected_content", request)
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
            "offer_id": request["start_offer"]["offer_id"],
            "binding_id": request["start_offer"]["binding_id"],
            "operation_id": request["start_offer"]["offer_id"],
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
                data.speakers, "room_content_current", AsyncMock(return_value=witness)
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
            self.assertEqual(
                (await read())["observation"]["prepare_id"], witness["offer_id"]
            )
            witness["route"] = {**route, "route_revision": "4"}
            self.assertEqual((await read())["status"], "unknown")
            witness["route"] = route
            data.commands.receipts.pop(request["command_sequence"])
            self.assertEqual((await read())["status"], "unknown")

    async def test_content_library_closed_arguments_and_full_list_replay(self):
        args = {
            "track_uris": ["spotify:track:0" + ch * 21 for ch in "ABA"],
            "shuffle": False,
            "library_source": "liked",
            "max_volume": 35,
        }
        self.assertTrue(valid_selected_arguments("play_tracks", args))
        for mutation in (
            {"track_uris": []},
            {"track_uris": args["track_uris"] * 17},
            {"track_uris": ["spotify:track:" + "Z" * 22]},
            {"shuffle": 0},
            {"shuffle": None},
            {"library_source": "playlist"},
            {"context_uri": ""},
            {"track_uri": args["track_uris"][0]},
        ):
            self.assertFalse(
                valid_selected_arguments("play_tracks", {**args, **mutation})
            )
        boundary = {**args, "track_uris": [args["track_uris"][0]] * 50}
        self.assertTrue(valid_selected_arguments("play_tracks", boundary))
        request = self.content_request(action="play_tracks", args=args)
        original = copy.deepcopy(request)
        result = await self.content_invoke("prepare_room_selected_content", request)
        self.assertEqual(result["state"], "staged")
        request["args"]["track_uris"][0:2] = reversed(
            request["args"]["track_uris"][0:2]
        )
        result = await self.content_invoke("prepare_room_selected_content", request)
        self.assertEqual(result["state"], "rejected")
        self.assertEqual(len(self.prepares()), 1)
        self.assertEqual(self.prepares()[0]["args"], original["args"])
        result = await self.content_invoke("start_room_selected_content", original)
        self.assertTrue(result["ok"])
        self.assertEqual(result["action"], "play_tracks")
        result = await self.content_invoke("start_room_selected_content", original)
        self.assertTrue(result["ok"])
        self.assertEqual(len(self.content_commits()), 1)

    def test_exact_context_kind_arguments_match_native_owner(self):
        for kind in ("playlist", "album", "artist", "show"):
            with self.subTest(kind=kind):
                self.assertTrue(
                    valid_selected_arguments(
                        "play_context",
                        {
                            "context_uri": "spotify:" + kind + ":" + "A" * 22,
                            "max_volume": 35,
                        },
                    )
                )
        for kind in ("episode", "audiobook", "station", "local"):
            with self.subTest(kind=kind):
                self.assertFalse(
                    valid_selected_arguments(
                        "play_context",
                        {
                            "context_uri": "spotify:" + kind + ":" + "A" * 22,
                            "max_volume": 35,
                        },
                    )
                )

    async def asyncSetUp(self):
        self.content_posts = []
        self.content_hold = False
        self.content_entered = asyncio.Event()
        self.content_release = asyncio.Event()
        self.content_commit_hold = False
        self.content_commit_entered = asyncio.Event()
        self.content_commit_release = asyncio.Event()
        self.content_prepare_status = 200
        self.content_commit_status = 200
        self.content_commit_corrupt = None
        self.content_offer_id = "a" * 32
        self.content_target = "native-owned-engine"
        await super().asyncSetUp()
        self.holds.extend((self.content_release, self.content_commit_release))
        self.content_commands = self.entries[0].runtime_data.commands

    def content_offer_value(self):
        return {
            "schema": "podconnect.room_content_offer.v1",
            "offer_id": self.content_offer_id,
            "binding_id": "b" * 64,
            "native_incarnation": "c" * 32,
            "policy_revision": "d" * 64,
            "start_volume_percent": 35,
            "expires_after_ms": 5000,
        }

    def content_result(self, suffix, state):
        complete = state == "complete"
        return {
            "schema": "podconnect.room_content_" + suffix + ".v1",
            "ok": complete,
            "offer_id": self.content_offer_id,
            "binding_id": "b" * 64,
            "state": state,
            "accepted_selected_content": complete,
            "start_operation_id": self.content_offer_id,
            "start_volume_percent": 35,
            "room_id": "room-beta",
            "homepod_id": "456",
            "physical_result_verified": False,
        }

    async def _backend(self, request):
        if request.path == "/api/room-content/state":
            if request.query.get("target_id") != self.content_target:
                return web.Response(status=409)
            return web.json_response(
                {
                    "schema": "podconnect.room_content_state.v1",
                    "ok": True,
                    "start_offer": self.content_offer_value(),
                    "actions": getattr(
                        self, "content_actions", ["play_track", "play_context"]
                    ),
                    "physical_result_verified": False,
                }
            )
        if request.path == "/api/room-content/result":
            return web.json_response(self.content_result("result", "staged"))
        if request.path.startswith("/api/room-content/"):
            body = await request.json()
            self.content_posts.append((request.path, copy.deepcopy(body)))
            if request.path.endswith("prepare"):
                self.content_entered.set()
                if self.content_hold:
                    await self.content_release.wait()
                if self.content_prepare_status != 200:
                    return web.Response(status=self.content_prepare_status)
                return web.json_response(self.content_result("prepare", "staged"))
            if request.path.endswith("cancel"):
                return web.json_response(self.content_result("cancel", "cancelled"))
            self.content_commit_entered.set()
            if self.content_commit_hold:
                await self.content_commit_release.wait()
            if self.content_commit_status != 200:
                return web.Response(status=self.content_commit_status)
            result = self.content_result("commit", "complete")
            if self.content_commit_corrupt:
                self.content_commit_corrupt(result)
            return web.json_response(result)
        return await super()._backend(request)

    def content_request(self, *, context=False, **overrides):
        return {
            **self.request("play_context" if context else "play_track"),
            "target_id": self.content_target,
            "room_id": "room-beta",
            "homepod_id": "456",
            "args": {
                "context_uri" if context else "track_uri": "spotify:playlist:"
                + "B" * 22
                if context
                else "spotify:track:" + "A" * 22,
                "max_volume": 35,
            },
            "start_offer": self.content_offer_value(),
            **overrides,
        }

    async def content_invoke(self, service, request):
        return await self.hass.services.async_call(
            "podconnect", service, request, blocking=True, return_response=True
        )

    def prepares(self):
        return [body for path, body in self.content_posts if path.endswith("prepare")]

    def content_commits(self):
        return [body for path, body in self.content_posts if path.endswith("commit")]

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
            "offer_id": request["start_offer"]["offer_id"],
            "binding_id": request["start_offer"]["binding_id"],
        }

    async def content_cleanup(self):
        tasks = list(self.content_commands.room_content.cleanups)
        if tasks:
            await asyncio.wait_for(asyncio.gather(*tasks), 2)

    async def test_content_read_targets_inactive_owned_engine_without_webapi_row_or_volume(
        self,
    ):
        data = self.entries[0].runtime_data
        data.coordinator.data["devices"] = []
        value = await self.content_invoke(
            "get_room_content_controls",
            {
                "config_entry_id": "account-alpha",
                "kind": "spotify_device",
                "target_id": self.content_target,
                "room_id": "room-beta",
                "homepod_id": "456",
            },
        )
        self.assertEqual(len(value), 14)
        self.assertEqual(
            value["target"],
            {"kind": "spotify_device", "target_id": self.content_target},
        )
        self.assertEqual(value["actions"], ["play_track", "play_context"])
        self.assertNotIn("volume_percent", value)
        self.assertNotIn("session_id", value)
        self.assertEqual(self.content_posts, [])
        response = await self.client.get("/api/services")
        services = next(
            row["services"]
            for row in await response.json()
            if row["domain"] == "podconnect"
        )
        for service in ("prepare_room_selected_content", "start_room_selected_content"):
            self.assertEqual(services[service]["response"], {"optional": False})
            self.assertEqual(
                set(services[service]["fields"]), set(self.content_request())
            )
        self.assertEqual(
            set(services["cancel_room_content_preparation"]["fields"]),
            set(self.cancel_args(self.content_request())),
        )

    async def test_content_stage_returns_to_caller_before_explicit_same_request_start(
        self,
    ):
        request = self.content_request(context=True)
        staged = await self.content_invoke("prepare_room_selected_content", request)
        self.assertEqual(len(staged), 13)
        self.assertEqual(staged["schema"], "podconnect.room_content_preparation.v1")
        self.assertEqual(staged["state"], "staged")
        self.assertEqual(self.content_commits(), [])
        # Actual trusted PV grant validation is a separate composed integration.
        result = await self.content_invoke("start_room_selected_content", request)
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
        self.assertTrue(valid_selected_receipt(result, identity))
        self.assertTrue(result["accepted_selected_content"])
        self.assertFalse(result["provider_request_accepted"])
        self.assertEqual(result["start_volume_percent"], 35)
        self.assertEqual(
            await self.content_invoke("start_room_selected_content", request), result
        )
        self.assertEqual(
            await self.content_invoke("prepare_room_selected_content", request), staged
        )
        self.assertEqual(len(self.prepares()), 1)
        self.assertEqual(
            self.content_commits(), [{"offer_id": "a" * 32, "binding_id": "b" * 64}]
        )
        self.assertEqual(self.prepares()[0]["args"], request["args"])
        self.assertEqual(self._writes(), [])
        self.assertEqual((self.native_prepares, self.native_commits), (0, 0))

    async def test_content_unprepared_changed_or_invalid_request_never_starts(self):
        request = self.content_request()
        self.assertEqual(
            (await self.content_invoke("start_room_selected_content", request))[
                "error_kind"
            ],
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
                (await self.content_invoke("prepare_room_selected_content", bad))["ok"]
            )
        with self.assertRaises(vol.Invalid):
            await self.content_invoke(
                "prepare_room_selected_content", {**request, "extra": True}
            )
        self.assertTrue(
            (await self.content_invoke("prepare_room_selected_content", request))["ok"]
        )
        changed = {
            **request,
            "args": {"track_uri": "spotify:track:" + "Z" * 22, "max_volume": 35},
        }
        self.assertEqual(
            (await self.content_invoke("start_room_selected_content", changed))[
                "error_kind"
            ],
            "request_reused",
        )
        self.assertEqual(self.content_commits(), [])

    async def test_content_delayed_stage_allows_genuine_advancing_poll_same_captured_owner(
        self,
    ):
        request = self.content_request()
        self.content_hold = True
        task = self._task(self.content_invoke("prepare_room_selected_content", request))
        await asyncio.wait_for(self.content_entered.wait(), 2)
        data = self.entries[0].runtime_data
        await data.coordinator.async_refresh()
        self.assertGreater(
            data.coordinator.data["poll_sequence"], request["expected_poll_sequence"]
        )
        self.assertEqual(self.content_commits(), [])
        self.content_release.set()
        self.assertTrue((await task)["ok"])
        result = await self.content_invoke("start_room_selected_content", request)
        self.assertEqual(result["outcome"], "accepted")
        self.assertEqual(len(self.content_commits()), 1)

    async def test_content_higher_intent_during_held_stage_is_not_blocked_and_retires_old(
        self,
    ):
        request = self.content_request()
        self.content_hold = True
        task = self._task(self.content_invoke("prepare_room_selected_content", request))
        await asyncio.wait_for(self.content_entered.wait(), 2)
        self.assertFalse(self.entries[0].runtime_data.commands.busy)
        higher = await self.invoke(self.request("next"))
        self.assertEqual(higher["outcome"], "accepted")
        self.content_release.set()
        self.assertFalse((await task)["ok"])
        rejected = await self.content_invoke("start_room_selected_content", request)
        self.assertEqual(rejected["outcome"], "rejected")
        self.assertEqual(self.content_commits(), [])
        await self.content_cleanup()

    async def test_content_concurrent_duplicate_prepares_coalesce_without_published_unknown(
        self,
    ):
        request = self.content_request()
        self.content_hold = True
        first = self._task(
            self.content_invoke("prepare_room_selected_content", request)
        )
        await asyncio.wait_for(self.content_entered.wait(), 2)
        second = self._task(
            self.content_invoke("prepare_room_selected_content", request)
        )
        await asyncio.sleep(0)
        self.assertFalse(second.done())
        self.content_release.set()
        self.assertEqual(await first, await second)
        self.assertEqual(len(self.prepares()), 1)
        self.assertEqual(self.content_commits(), [])

    async def test_content_replaced_owner_while_staging_denies_physical_commit(self):
        request = self.content_request()
        self.content_hold = True
        task = self._task(self.content_invoke("prepare_room_selected_content", request))
        await asyncio.wait_for(self.content_entered.wait(), 2)
        self.entries[0].runtime_data.coordinator.owner_epoch = "replacement"
        self.content_release.set()
        self.assertFalse((await task)["ok"])
        self.assertEqual(
            (await self.content_invoke("start_room_selected_content", request))[
                "outcome"
            ],
            "rejected",
        )
        self.assertEqual(self.content_commits(), [])
        await self.content_cleanup()

    async def test_content_revoked_task_cancels_after_stage_before_start(self):
        request = self.content_request()
        self.assertTrue(
            (await self.content_invoke("prepare_room_selected_content", request))["ok"]
        )
        cancelled = await self.content_invoke(
            "cancel_room_content_preparation", self.cancel_args(request)
        )
        self.assertTrue(cancelled["ok"])
        self.assertEqual(cancelled["state"], "cancelled")
        self.assertEqual(
            (await self.content_invoke("start_room_selected_content", request))[
                "outcome"
            ],
            "rejected",
        )
        self.assertEqual(self.content_commits(), [])

    async def test_content_prepare_unknown_cannot_be_upgraded_by_late_staging(self):
        self.content_prepare_status = 502
        request = self.content_request()
        unknown = await self.content_invoke("prepare_room_selected_content", request)
        self.assertEqual(unknown["state"], "unknown")
        self.content_prepare_status = 200
        self.assertEqual(
            await self.content_invoke("prepare_room_selected_content", request), unknown
        )
        self.assertEqual(
            (await self.content_invoke("start_room_selected_content", request))[
                "outcome"
            ],
            "rejected",
        )
        self.assertEqual(len(self.prepares()), 1)
        self.assertEqual(self.content_commits(), [])
        await self.content_cleanup()

    async def test_content_cancelled_prepare_never_auto_commits_on_late_response(self):
        request = self.content_request()
        self.content_hold = True
        task = self._task(self.content_invoke("prepare_room_selected_content", request))
        await asyncio.wait_for(self.content_entered.wait(), 2)
        task.cancel()
        with self.assertRaises(asyncio.CancelledError):
            await task
        self.content_release.set()
        replay = await self.content_invoke("prepare_room_selected_content", request)
        self.assertFalse(replay["ok"])
        self.assertEqual(replay["state"], "cancelled")
        self.assertEqual(self.content_commits(), [])
        await self.content_cleanup()

    async def test_content_missing_or_intermediate_ack_is_consumed_unknown_never_complete(
        self,
    ):
        for index, corrupt in enumerate(
            (
                lambda v: v.update(
                    state="staged", ok=False, accepted_selected_content=False
                ),
                lambda v: v.update(accepted_selected_content=False),
                lambda v: v.update(start_operation_id=None),
                lambda v: v.update(room_id="wrong"),
                lambda v: v.update(physical_result_verified=True),
            )
        ):
            self.content_offer_id = f"{index + 1:032x}"
            self.content_commit_corrupt = corrupt
            request = self.content_request()
            self.assertTrue(
                (await self.content_invoke("prepare_room_selected_content", request))[
                    "ok"
                ]
            )
            result = await self.content_invoke("start_room_selected_content", request)
            self.assertEqual(result["outcome"], "unknown")
            self.assertFalse(result["accepted_selected_content"])
            self.assertEqual(
                await self.content_invoke("start_room_selected_content", request),
                result,
            )
            await self.content_cleanup()
        self.assertEqual(len(self.content_commits()), 5)
        self.assertEqual(self._writes(), [])

    async def test_content_cancel_after_dispatch_seals_unknown_even_when_late_ack_completes(
        self,
    ):
        request = self.content_request()
        self.assertTrue(
            (await self.content_invoke("prepare_room_selected_content", request))["ok"]
        )
        self.content_commit_hold = True
        task = self._task(self.content_invoke("start_room_selected_content", request))
        await asyncio.wait_for(self.content_commit_entered.wait(), 2)
        task.cancel()
        with self.assertRaises(asyncio.CancelledError):
            await task
        result = await self.content_invoke("start_room_selected_content", request)
        self.assertEqual(result["outcome"], "unknown")
        self.content_commit_release.set()
        self.assertEqual(
            await self.content_invoke("start_room_selected_content", request), result
        )
        self.assertEqual(len(self.content_commits()), 1)
        await self.content_cleanup()

    async def test_content_concurrent_same_commit_waits_for_original_receipt_without_replay(
        self,
    ):
        request = self.content_request()
        self.assertTrue(
            (await self.content_invoke("prepare_room_selected_content", request))["ok"]
        )
        self.content_commit_hold = True
        first = self._task(self.content_invoke("start_room_selected_content", request))
        await asyncio.wait_for(self.content_commit_entered.wait(), 2)
        second = self._task(self.content_invoke("start_room_selected_content", request))
        await asyncio.sleep(0)
        self.assertFalse(second.done())
        self.content_commit_release.set()
        self.assertEqual(await first, await second)
        self.assertEqual(len(self.content_commits()), 1)

    async def test_content_explicit_cancel_during_commit_seals_identical_unknown(self):
        request = self.content_request()
        self.assertTrue(
            (await self.content_invoke("prepare_room_selected_content", request))["ok"]
        )
        self.content_commit_hold = True
        task = self._task(self.content_invoke("start_room_selected_content", request))
        await asyncio.wait_for(self.content_commit_entered.wait(), 2)
        cancelled = await self.content_invoke(
            "cancel_room_content_preparation", self.cancel_args(request)
        )
        self.assertEqual(cancelled["state"], "unknown")
        first_unknown = await self.content_invoke(
            "start_room_selected_content", request
        )
        self.assertEqual(first_unknown["outcome"], "unknown")
        self.content_commit_release.set()
        self.assertEqual(await task, first_unknown)
        self.assertEqual(
            await self.content_invoke("start_room_selected_content", request),
            first_unknown,
        )
        self.assertEqual(len(self.content_commits()), 1)

    async def test_content_cancel_does_not_rewrite_published_preparation_unknown(self):
        self.content_prepare_status = 502
        request = self.content_request()
        original = await self.content_invoke("prepare_room_selected_content", request)
        self.assertEqual(original["state"], "unknown")
        await self.content_invoke(
            "cancel_room_content_preparation", self.cancel_args(request)
        )
        self.assertEqual(
            await self.content_invoke("prepare_room_selected_content", request),
            original,
        )
        self.assertEqual(self.content_commits(), [])
        await self.content_cleanup()

    async def test_content_transport_does_not_apply_short_legacy_timeout_to_commit(
        self,
    ):
        request = self.content_request()
        self.assertTrue(
            (await self.content_invoke("prepare_room_selected_content", request))["ok"]
        )
        self.content_commit_hold = True
        task = self._task(self.content_invoke("start_room_selected_content", request))
        await asyncio.wait_for(self.content_commit_entered.wait(), 2)
        await asyncio.sleep(2.1)
        self.assertFalse(task.done())
        self.content_commit_release.set()
        self.assertEqual((await task)["outcome"], "accepted")
        self.assertEqual(len(self.content_commits()), 1)

    async def test_content_transport_preserves_stage_budget_for_held_initial_reply(
        self,
    ):
        request = self.content_request()
        self.content_hold = True
        task = self._task(self.content_invoke("prepare_room_selected_content", request))
        await asyncio.wait_for(self.content_entered.wait(), 2)
        await asyncio.sleep(1.1)
        self.assertFalse(task.done())
        self.assertFalse(self.content_commands.busy)
        self.content_release.set()
        self.assertEqual((await task)["state"], "staged")
        self.assertEqual(self.content_commits(), [])
        await self.content_cleanup()

    async def test_content_expired_staged_record_and_evicted_receipt_never_replay(self):
        request = self.content_request()
        self.assertTrue(
            (await self.content_invoke("prepare_room_selected_content", request))["ok"]
        )
        record = self.content_commands.room_content.records[request["command_sequence"]]
        record["staged_at"] -= 6
        self.assertEqual(
            (await self.content_invoke("start_room_selected_content", request))[
                "error_kind"
            ],
            "content_expired",
        )
        self.content_commands.receipts.clear()
        self.assertEqual(
            (await self.content_invoke("start_room_selected_content", request))[
                "error_kind"
            ],
            "stale_command",
        )
        self.assertEqual(self.content_commits(), [])
        await self.content_cleanup()

    async def test_content_episode_public_selected_service_preserves_exact_admitted_uri(
        self,
    ):
        self.content_actions = ["play_track", "play_context", "play_episode"]
        request = self.content_request(
            action="play_episode",
            args={"episode_uri": "spotify:episode:" + "0" * 21 + "4", "max_volume": 35},
        )
        staged = await self.content_invoke("prepare_room_selected_content", request)
        self.assertEqual(staged["state"], "staged")
        receipt = await self.content_invoke("start_room_selected_content", request)
        self.assertEqual(receipt["outcome"], "accepted")
        self.assertEqual(receipt["action"], "play_episode")
        self.assertEqual((len(self.prepares()), len(self.content_commits())), (1, 1))
        self.assertEqual(self.prepares()[0]["args"], request["args"])
        duplicate = await self.content_invoke("start_room_selected_content", request)
        self.assertEqual(duplicate, receipt)
        self.assertEqual(len(self.content_commits()), 1)

    def test_content_episode_closed_arguments_preserve_kind_and_ceiling(self):
        good = {"episode_uri": "spotify:episode:" + "0" * 21 + "4", "max_volume": 35}
        self.assertTrue(valid_selected_arguments("play_episode", good))
        for value in (
            {**good, "max_volume": 34},
            {**good, "max_volume": True},
            {**good, "track_uri": "spotify:track:" + "F" * 22},
            {**good, "episode_uri": "spotify:track:" + "F" * 22},
            {**good, "episode_uri": "spotify:audiobook:" + "F" * 22},
        ):
            self.assertFalse(valid_selected_arguments("play_episode", value))
        self.assertFalse(
            valid_selected_arguments(
                "play_context", {"context_uri": good["episode_uri"], "max_volume": 35}
            )
        )

    async def test_content_episode_replaced_owner_cannot_start(self):
        self.content_actions = ["play_track", "play_context", "play_episode"]
        request = self.content_request(
            action="play_episode",
            args={"episode_uri": "spotify:episode:" + "0" * 21 + "4", "max_volume": 35},
        )
        self.content_hold = True
        task = self._task(self.content_invoke("prepare_room_selected_content", request))
        await asyncio.wait_for(self.content_entered.wait(), 2)
        self.entries[0].runtime_data.coordinator.owner_epoch = "replacement"
        self.content_release.set()
        self.assertFalse((await task)["ok"])
        self.assertEqual(
            (await self.content_invoke("start_room_selected_content", request))[
                "outcome"
            ],
            "rejected",
        )
        self.assertEqual(self.content_commits(), [])
        await self.content_cleanup()

    async def test_content_episode_cancel_and_low_ceiling_never_start(self):
        self.content_actions = ["play_track", "play_context", "play_episode"]
        request = self.content_request(
            action="play_episode",
            args={"episode_uri": "spotify:episode:" + "0" * 21 + "4", "max_volume": 35},
        )
        bad = {**request, "args": {**request["args"], "max_volume": 34}}
        self.assertFalse(
            (await self.content_invoke("prepare_room_selected_content", bad))["ok"]
        )
        self.assertEqual(self.prepares(), [])
        self.assertTrue(
            (await self.content_invoke("prepare_room_selected_content", request))["ok"]
        )
        self.assertTrue(
            (
                await self.content_invoke(
                    "cancel_room_content_preparation", self.cancel_args(request)
                )
            )["ok"]
        )
        self.assertEqual(
            (await self.content_invoke("start_room_selected_content", request))[
                "outcome"
            ],
            "rejected",
        )
        self.assertEqual(self.content_commits(), [])
        await self.content_cleanup()


def load_tests(loader, tests, pattern):
    return unittest.TestSuite(
        RoomContentFrameworkTests(name)
        for name in loader.getTestCaseNames(RoomContentFrameworkTests)
        if name.startswith("test_content_")
    )
