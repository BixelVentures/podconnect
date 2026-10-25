"""Actual HA service/command owner; inert loopback native/C staging only."""

import asyncio
import copy
import time
import unittest
from datetime import datetime
from types import SimpleNamespace
from unittest.mock import patch

import voluptuous as vol
from aiohttp import web
from test_room_transport import RoomTransportFrameworkTests


class RoomQueueFrameworkTests(RoomTransportFrameworkTests):
    async def asyncSetUp(self):
        self.queue_reads = []
        self.queue_posts = []
        self.queue_hold = False
        self.queue_entered = asyncio.Event()
        self.queue_release = asyncio.Event()
        self.queue_commit_hold = False
        self.queue_commit_entered = asyncio.Event()
        self.queue_commit_release = asyncio.Event()
        self.queue_corrupt_state = None
        self.queue_corrupt_prepare = None
        self.queue_corrupt_ack = None
        await super().asyncSetUp()
        self.holds.extend((self.queue_release, self.queue_commit_release))

    def native(self):
        value = super().native()
        value.update(alias_incarnation="a" * 32, alias_registry="b" * 64)
        return value

    def queue(self):
        return {
            "observation_id": "c" * 32,
            "queue_revision": 12,
            "expires_after_ms": 9000,
            "items": [
                {
                    "id": f"{i:024x}",
                    "position": i,
                    "uri": "spotify:track:" + "0" * 22,
                    "queued": i == 0,
                }
                for i in range(2)
            ],
            "truncated": False,
        }

    def writer(self):
        return {
            "native_process": {
                "boot_id": "12345678-1234-4234-8234-123456789abc",
                "pid": "17",
                "proc_start_ticks": "21",
            },
            "writer_fd": "0",
            "pipe_identity": {"device": "1", "inode": "2", "type": "fifo"},
            "source_epoch": "0",
            "open_generation": "1",
            "input_path_digest": self.route()["input_path_digest"],
        }

    async def _backend(self, request):
        if request.path == "/api/room-queue/state":
            self.queue_reads.append(dict(request.query))
            value = {
                "schema": "podconnect.room_queue_state.v1",
                "ok": True,
                "purpose": "queue_occurrence_v1",
                "native": self.native(),
                "route": self.route(),
                "queue": self.queue(),
                "actions": ["play_queue_occurrence"],
                "physical_result_verified": False,
            }
            if self.queue_corrupt_state:
                self.queue_corrupt_state(value)
            return web.json_response(value)
        if request.path in {"/api/room-queue/prepare", "/api/room-queue/commit"}:
            body = await request.json()
            self.queue_posts.append((request.path, copy.deepcopy(body)))
            if request.path.endswith("prepare"):
                self.queue_entered.set()
                if self.queue_hold:
                    await self.queue_release.wait()
                value = {
                    "schema": "podconnect.room_queue_prepare.v1",
                    "ok": True,
                    "purpose": "queue_occurrence_v1",
                    "prepare_id": "d" * 32,
                    "native": self.native(),
                    "route": self.route(),
                    "queue": body["queue"],
                    "writer_proof": self.writer(),
                    "expires_after_ms": 4000,
                    "physical_result_verified": False,
                }
                if self.queue_corrupt_prepare:
                    self.queue_corrupt_prepare(value)
                return web.json_response(value)
            self.queue_commit_entered.set()
            if self.queue_commit_hold:
                await self.queue_commit_release.wait()
            value = {
                "schema": "podconnect.room_queue_commit.v1",
                "ok": True,
                "prepare_id": body["prepare_id"],
                "outcome": "accepted",
                "accepted_native": True,
                "retry_allowed": False,
                "physical_result_verified": False,
            }
            if self.queue_corrupt_ack:
                self.queue_corrupt_ack(value)
            return web.json_response(value)
        return await super()._backend(request)

    async def queue_read(self):
        return await self.hass.services.async_call(
            "podconnect",
            "get_room_queue_controls",
            {
                "config_entry_id": "account-alpha",
                "kind": "spotify_device",
                "target_id": "spotify-one",
                "room_id": "room-alpha",
                "homepod_id": "123",
            },
            blocking=True,
            return_response=True,
        )

    def queue_request(self):
        return self.request(
            "play_queue_occurrence",
            args={
                "observation_id": "c" * 32,
                "session_id": "session-a",
                "queue_revision": 12,
                "occurrence_id": f"{1:024x}",
            },
            room_id="room-alpha",
            homepod_id="123",
            native=self.native(),
            route=self.route(),
            purpose="queue_occurrence_v1",
        )

    async def queue_call(self, name, body):
        return await self.hass.services.async_call(
            "podconnect", name, body, blocking=True, return_response=True
        )

    async def stage(self):
        value = await self.queue_read()
        self.assertTrue(value["ok"])
        body = self.queue_request()
        prepared = await self.queue_call("prepare_room_queue", body)
        return body, prepared

    async def _queue_with_processing_delay(self):
        from custom_components.podconnect import room_queue

        owner = self.entries[0].runtime_data.commands
        observed = owner.controls("account-alpha", "spotify-one")["observation"]
        source_until = (
            datetime.fromisoformat(observed["observed_at"]).timestamp()
            + observed["max_age_ms"] / 1000
        )
        clock = {"wall": source_until - 2, "mono": 1000.0}
        queue = self.queue()
        queue["items"] = [
            {**queue["items"][0], "id": f"{index:024x}", "position": index}
            for index in range(20)
        ]
        real_copy = copy.deepcopy
        real_artwork = room_queue.validate_spotify_artwork

        def delayed_copy(value):
            if (
                isinstance(value, dict)
                and value.get("schema") == "podconnect.room_queue_state.v1"
            ):
                clock["wall"] += 0.25
                clock["mono"] += 0.25
            return real_copy(value)

        def delayed_artwork(value):
            clock["wall"] += 0.025
            clock["mono"] += 0.025
            return real_artwork(value)

        with (
            patch.object(self, "queue", return_value=queue),
            patch.object(
                room_queue,
                "time",
                SimpleNamespace(
                    time=lambda: clock["wall"], monotonic=lambda: clock["mono"]
                ),
            ),
            patch.object(room_queue, "copy", SimpleNamespace(deepcopy=delayed_copy)),
            patch.object(room_queue, "validate_spotify_artwork", delayed_artwork),
        ):
            result = await self.queue_read()
        self.assertTrue(result["ok"])
        self.assertEqual(len(result["items"]), 20)
        self.assertEqual(self.queue_posts, [])
        self.assertEqual(self._writes(), [])
        saved = owner.room_queue.observations["c" * 32]
        return result, saved, source_until

    async def test_queue_metadata_processing_never_extends_source_deadline(self):
        result, _, source_until = await self._queue_with_processing_delay()
        self.assertLessEqual(result["valid_until"], source_until)

    async def test_queue_copy_processing_never_extends_native_deadline(self):
        _, saved, _ = await self._queue_with_processing_delay()
        self.assertLessEqual(saved["deadline"], 1002.0)

    async def test_queue_distinct_prepare_no_effect_no_sequence_then_commit_once(self):
        data = self.entries[0].runtime_data
        self.native_output_volume = 27
        body, prepared = await self.stage()
        self.assertTrue(prepared["ok"])
        self.assertEqual(data.commands.command_sequence, 0)
        self.assertEqual(self._writes(), [])
        self.assertEqual(len(self.queue_posts), 1)
        self.assertLessEqual(prepared["expires_after_ms"], 4000)
        sent = body | {"prepare_id": prepared["prepare_id"]}
        result = await self.queue_call("control_room_queue", sent)
        self.assertEqual(result["outcome"], "accepted")
        self.assertTrue(result["accepted_native"])
        self.assertFalse(result["provider_request_accepted"])
        self.assertFalse(result["physical_result_verified"])
        self.assertEqual(await self.queue_call("control_room_queue", sent), result)
        self.assertEqual(len(self.queue_posts), 2)
        self.assertEqual(data.commands.command_sequence, 1)
        self.assertEqual(self.queue_posts[-1][1], {"prepare_id": "d" * 32})
        self.assertEqual(self.native_output_volume, 27)
        self.assertEqual(self._writes(), [])

    async def test_queue_duplicate_uri_distinct_ids_and_manual_context_projection(self):
        value = await self.queue_read()
        self.assertEqual(value["items"][0]["uri"], value["items"][1]["uri"])
        self.assertNotEqual(
            value["items"][0]["option_id"], value["items"][1]["option_id"]
        )
        self.assertTrue(all("queued" not in row for row in value["items"]))
        original = self.entries[0].runtime_data.commands.room_queue.observations[
            "c" * 32
        ]["value"]["queue"]["items"]
        self.assertEqual([row["queued"] for row in original], [True, False])
        self.assertEqual(value["purpose"], "queue_occurrence_v1")
        self.assertEqual(value["native"], self.native())
        self.assertEqual(value["route"], self.route())

    async def test_queue_old_or_foreign_state_and_duplicate_ids_deny_without_stage(
        self,
    ):
        for mutate in [
            lambda v: v.update(schema="podconnect.queue_controls.v1"),
            lambda v: v.update(purpose="next"),
            lambda v: v["native"].update(homepod_id="999"),
            lambda v: v["queue"]["items"][1].update(id="0" * 24),
            lambda v: v.update(unexpected=True),
        ]:
            self.queue_corrupt_state = mutate
            self.assertFalse((await self.queue_read())["ok"])
        self.assertEqual(self.queue_posts, [])

    async def test_queue_prepared_wrong_writer_route_selection_and_old_schema_never_commit(
        self,
    ):
        for mutate in [
            lambda v: v["writer_proof"].update(input_path_digest="e" * 64),
            lambda v: v["writer_proof"].update(writer_fd="-1"),
            lambda v: v["route"].update(selection_revision="99"),
            lambda v: v["queue"].update(occurrence_id="f" * 24),
            lambda v: v.update(schema="podconnect.room_transport_prepare.v1"),
            lambda v: v.update(expires_after_ms=0),
        ]:
            self.queue_corrupt_prepare = mutate
            _, prepared = await self.stage()
            self.assertFalse(prepared["ok"])
        self.assertTrue(all(path.endswith("prepare") for path, _ in self.queue_posts))
        self.assertEqual(self.entries[0].runtime_data.commands.command_sequence, 0)

    async def test_queue_stage_poll_replaced_never_commit(self):
        self.queue_hold = True
        await self.queue_read()
        running = self._task(
            self.queue_call("prepare_room_queue", self.queue_request())
        )
        await asyncio.wait_for(self.queue_entered.wait(), 2)
        await self.entries[0].runtime_data.coordinator.async_refresh()
        self.queue_release.set()
        result = await running
        self.assertFalse(result["ok"])
        self.assertEqual(len(self.queue_posts), 1)
        self.assertFalse(self.entries[0].runtime_data.commands.busy)
        self.assertEqual(self.entries[0].runtime_data.commands.command_sequence, 0)

    async def test_queue_cancel_stage_no_effect_no_ledger_and_no_restage(self):
        self.queue_hold = True
        await self.queue_read()
        body = self.queue_request()
        running = self._task(self.queue_call("prepare_room_queue", body))
        await asyncio.wait_for(self.queue_entered.wait(), 2)
        running.cancel()
        with self.assertRaises(asyncio.CancelledError):
            await running
        data = self.entries[0].runtime_data
        self.assertFalse(data.commands.busy)
        self.assertEqual(data.commands.command_sequence, 0)
        self.assertFalse((await self.queue_call("prepare_room_queue", body))["ok"])
        self.assertEqual(len(self.queue_posts), 1)

    async def test_queue_commit_owner_replaced_after_stage_rejects_before_send(self):
        body, prepared = await self.stage()
        await self.entries[0].runtime_data.coordinator.async_refresh()
        result = await self.queue_call(
            "control_room_queue", body | {"prepare_id": prepared["prepare_id"]}
        )
        self.assertEqual(result["outcome"], "rejected")
        self.assertEqual(len(self.queue_posts), 1)

    async def test_queue_unknown_consumed_no_retry_or_changed_choice(self):
        body, prepared = await self.stage()
        self.queue_corrupt_ack = lambda v: v.update(
            outcome="unknown", ok=False, accepted_native=False
        )
        sent = body | {"prepare_id": prepared["prepare_id"]}
        result = await self.queue_call("control_room_queue", sent)
        self.assertEqual(result["outcome"], "unknown")
        self.assertFalse(result["retry_allowed"])
        self.assertEqual(await self.queue_call("control_room_queue", sent), result)
        changed = copy.deepcopy(sent)
        changed["args"]["occurrence_id"] = "0" * 24
        self.assertEqual(
            (await self.queue_call("control_room_queue", changed))["outcome"],
            "rejected",
        )
        self.assertEqual(len(self.queue_posts), 2)

    async def test_queue_services_closed_no_mix_or_uri_position_authority(self):
        await self.queue_read()
        body = self.queue_request()
        for bad in [
            body | {"unexpected": True},
            body | {"purpose": "next"},
            body | {"action": "next"},
        ]:
            with self.assertRaises(vol.Invalid):
                await self.queue_call("prepare_room_queue", bad)
        for args in [
            body["args"] | {"uri": "spotify:track:" + "0" * 22},
            body["args"] | {"position": 1},
        ]:
            self.assertFalse(
                (await self.queue_call("prepare_room_queue", body | {"args": args}))[
                    "ok"
                ]
            )
        self.assertEqual(self.queue_posts, [])

    async def test_queue_fresh_same_account_metadata_flows_without_changing_occurrences(
        self,
    ):
        data = self.entries[0].runtime_data
        data.observations._queue_cache = {
            "ok": True,
            "config_entry_id": "account-alpha",
            "owner_epoch": data.coordinator.owner_epoch,
            "items": [
                {
                    "uri": "spotify:track:" + "0" * 22,
                    "name": "Faktisk køsang",
                    "artists": ["Faktisk kunstner"],
                    "artwork": None,
                }
            ],
        }
        data.observations._queue_cached_at = time.monotonic()
        value = await self.queue_read()
        self.assertEqual(
            [row["name"] for row in value["items"]], ["Faktisk køsang"] * 2
        )
        self.assertEqual(
            [row["artist"] for row in value["items"]], ["Faktisk kunstner"] * 2
        )
        self.assertNotEqual(
            value["items"][0]["option_id"], value["items"][1]["option_id"]
        )
        for corrupt in ("account", "epoch", "stale"):
            cache = data.observations._queue_cache
            if corrupt == "account":
                cache["config_entry_id"] = "foreign"
            if corrupt == "epoch":
                cache["config_entry_id"] = "account-alpha"
                cache["owner_epoch"] = "foreign"
            if corrupt == "stale":
                cache["owner_epoch"] = data.coordinator.owner_epoch
                data.observations._queue_cached_at = time.monotonic() - 11
            value = await self.queue_read()
            self.assertEqual(
                [row["name"] for row in value["items"]], ["Nummer i køen"] * 2
            )

    async def test_queue_prepared_deadline_expired_before_commit_no_send(self):
        body, prepared = await self.stage()
        self.entries[0].runtime_data.commands.room_queue.preparations[
            prepared["prepare_id"]
        ]["deadline"] = 0
        result = await self.queue_call(
            "control_room_queue", body | {"prepare_id": prepared["prepare_id"]}
        )
        self.assertEqual(result["outcome"], "rejected")
        self.assertEqual(len(self.queue_posts), 1)

    async def test_queue_post_send_cancel_is_unknown_consumed_and_not_replayed(self):
        body, prepared = await self.stage()
        sent = body | {"prepare_id": prepared["prepare_id"]}
        self.queue_commit_hold = True
        running = self._task(self.queue_call("control_room_queue", sent))
        await asyncio.wait_for(self.queue_commit_entered.wait(), 2)
        running.cancel()
        with self.assertRaises(asyncio.CancelledError):
            await running
        self.assertEqual(
            (await self.queue_call("control_room_queue", sent))["outcome"], "unknown"
        )
        self.assertEqual(len(self.queue_posts), 2)

    async def test_queue_stage_epoch_replacement_denied_before_commit(self):
        self.queue_hold = True
        await self.queue_read()
        running = self._task(
            self.queue_call("prepare_room_queue", self.queue_request())
        )
        await asyncio.wait_for(self.queue_entered.wait(), 2)
        self.entries[0].runtime_data.coordinator.owner_epoch = "replaced"
        self.queue_release.set()
        self.assertFalse((await running)["ok"])
        self.assertEqual(len(self.queue_posts), 1)
        self.assertFalse(self.entries[0].runtime_data.commands.busy)

    async def test_queue_stage_account_replacement_denied_before_commit(self):
        # HA may unload this entry after unique_id changes. Keep the original
        # in-flight owner so cleanup is checked even after runtime_data is removed.
        owner = self.entries[0].runtime_data.commands
        self.queue_hold = True
        await self.queue_read()
        running = self._task(
            self.queue_call("prepare_room_queue", self.queue_request())
        )
        await asyncio.wait_for(self.queue_entered.wait(), 2)
        self.hass.config_entries.async_update_entry(
            self.entries[0], unique_id="foreign-account"
        )
        self.queue_release.set()
        self.assertFalse((await running)["ok"])
        self.assertEqual(len(self.queue_posts), 1)
        self.assertFalse(owner.busy)
        self.assertEqual(owner.command_sequence, 0)
        self.assertEqual(self._writes(), [])


if __name__ == "__main__":
    unittest.main()
