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
        self.queue_headers = []
        self.autoplay_headers = []
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
        if request.path == "/spotify/me/player/queue":
            self.assertEqual(request.method, "GET")
            self.assertEqual(request.headers.get("Authorization"), "Bearer inert-fixture-only")
            return web.json_response({"queue": []})
        if request.path == "/api/room-queue/state":
            self.queue_reads.append(dict(request.query))
            self.queue_headers.append(request.headers.get("X-Podconnect-Queue-Metadata"))
            self.autoplay_headers.append(request.headers.get("X-Podconnect-Queue-Autoplay"))
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

    async def queue_read(self, **options):
        return await self.hass.services.async_call(
            "podconnect",
            "get_room_queue_controls",
            {
                "config_entry_id": "account-alpha",
                "kind": "spotify_device",
                "target_id": "spotify-one",
                "room_id": "room-alpha",
                "homepod_id": "123",
                **options,
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

    async def test_autoplay_opt_in_preserves_native_action_authority(self):
        def add_preview(value):
            value["autoplay"] = {
                "kind": "preview",
                "display_ids": {f"{i:024x}": f"display-{i + 2:024x}" for i in range(2)},
            }

        legacy = await self.queue_read()
        self.assertTrue(legacy["ok"])
        self.assertNotIn("autoplay", legacy)
        self.queue_corrupt_state = add_preview
        preview = await self.queue_read(autoplay_preview=True)
        self.assertTrue(preview["ok"])
        self.assertEqual(preview["schema"], legacy["schema"])
        self.assertEqual(preview["autoplay"]["kind"], "preview")
        self.assertEqual(preview["actions"], ["play_queue_occurrence"])
        self.assertEqual(self.autoplay_headers, [None, "1"])
        self.assertEqual(self.queue_headers, ["1", "1"])
        self.assertEqual(self.queue_posts, [])

        body = self.queue_request()
        bad_body = copy.deepcopy(body)
        option = body["args"]["occurrence_id"]
        bad_body["args"]["occurrence_id"] = preview["autoplay"]["display_ids"][option]
        self.assertFalse((await self.queue_call("prepare_room_queue", bad_body))["ok"])
        self.assertEqual(self.queue_posts, [])
        self.assertTrue((await self.queue_call("prepare_room_queue", body))["ok"])
        self.assertEqual(len(self.queue_posts), 1)
        self.assertEqual(self._writes(), [])

    async def test_autoplay_sidecar_strict_opt_in_and_shape(self):
        def sidecar(kind, display_ids):
            self.queue_corrupt_state = lambda value: value.update(
                autoplay={"kind": kind, "display_ids": display_ids}
            )

        first, second, foreign = (f"{i:024x}" for i in range(3))
        first_display, second_display = f"display-{first}", f"display-{second}"
        sidecar("promotion", {first: first_display})
        self.assertFalse((await self.queue_read(autoplay_preview=False))["ok"])
        promoted = await self.queue_read(autoplay_preview=True)
        self.assertTrue(promoted["ok"])
        self.assertEqual(promoted["autoplay"]["display_ids"], {first: first_display})
        for kind, ids in (
            ("preview", {first: first_display}),
            ("preview", {first: first_display, second: first_display}),
            ("promotion", {foreign: second_display}),
            ("promotion", {first: first}),
            ("promotion", {}),
        ):
            with self.subTest(kind=kind, ids=ids):
                sidecar(kind, ids)
                self.assertFalse((await self.queue_read(autoplay_preview=True))["ok"])
        def display_only(value):
            value["autoplay"] = {
                "kind": "preview",
                "display_ids": {first: first_display, second: second_display},
            }
            value["actions"] = []

        self.queue_corrupt_state = display_only
        preview = await self.queue_read(autoplay_preview=True)
        self.assertTrue(preview["ok"])
        self.assertEqual(preview["actions"], [])
        self.assertFalse((await self.queue_call("prepare_room_queue", self.queue_request()))["ok"])
        self.assertEqual(self.queue_posts, [])
        self.assertEqual(self._writes(), [])

    async def test_autoplay_pending_is_empty_and_has_no_action_observation(self):
        def pending(value):
            value["autoplay"] = {"kind": "pending"}
            value["queue"]["items"] = []
            value["actions"] = []

        self.queue_corrupt_state = pending
        value = await self.queue_read(autoplay_preview=True)
        self.assertTrue(value["ok"])
        self.assertEqual(value["autoplay"], {"kind": "pending"})
        self.assertEqual(value["items"], [])
        self.assertEqual(value["actions"], [])
        owner = self.entries[0].runtime_data.commands.room_queue
        self.assertEqual(owner.observations, {})
        self.assertFalse((await self.queue_read())["ok"])

        def with_actions(value):
            pending(value)
            value["actions"] = ["play_queue_occurrence"]

        def with_rows(value):
            pending(value)
            value["queue"]["items"] = self.queue()["items"]

        def truncated(value):
            pending(value)
            value["queue"]["truncated"] = True

        for invalid in (with_actions, with_rows, truncated):
            with self.subTest(invalid=invalid.__name__):
                self.queue_corrupt_state = invalid
                self.assertFalse((await self.queue_read(autoplay_preview=True))["ok"])
        self.assertEqual(owner.observations, {})
        self.assertEqual(self.queue_posts, [])
        self.assertEqual(self._writes(), [])

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

    async def test_queue_routine_poll_during_stage_still_prepares_and_commits(self):
        # Issue #140: on Green a Spotify poll (every 3 s while playing, plus
        # Native revision refreshes) landed while Speakers staged the choice,
        # so prepare_room_queue answered not-ok and PodVoice logged
        # validate_preparation/invalid_response. Speakers fences the occurrence.
        self.queue_hold = True
        await self.queue_read()
        body = self.queue_request()
        running = self._task(self.queue_call("prepare_room_queue", body))
        await asyncio.wait_for(self.queue_entered.wait(), 2)
        await self.entries[0].runtime_data.coordinator.async_refresh()
        self.queue_release.set()
        prepared = await running
        self.assertTrue(prepared["ok"])
        self.assertEqual(prepared["schema"], "podconnect.room_queue_preparation.v1")
        self.assertEqual(len(self.queue_posts), 1)
        self.assertFalse(self.entries[0].runtime_data.commands.busy)
        self.assertEqual(self.entries[0].runtime_data.commands.command_sequence, 0)
        await self.entries[0].runtime_data.coordinator.async_refresh()
        result = await self.queue_call(
            "control_room_queue", body | {"prepare_id": prepared["prepare_id"]}
        )
        self.assertEqual(result["outcome"], "accepted")
        self.assertEqual(len(self.queue_posts), 2)
        self.assertEqual(self.entries[0].runtime_data.commands.command_sequence, 1)

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
        self.entries[0].runtime_data.coordinator.owner_epoch = "replaced"
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
        self.assertEqual([row["metadata_known"] for row in value["items"]], [True, True])
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
            self.assertEqual([row["metadata_known"] for row in value["items"]], [False, False])

    async def test_queue_late_metadata_is_structural_and_preserves_occurrences(self):
        data = self.entries[0].runtime_data
        data.observations._queue_cache = None
        cold = await self.queue_read()
        self.assertEqual([row["metadata_known"] for row in cold["items"]], [False, False])
        data.observations._queue_cache = {
            "ok": True,
            "config_entry_id": "account-alpha",
            "owner_epoch": data.coordinator.owner_epoch,
            "items": [{"uri": "spotify:track:" + "0" * 22, "name": "Nummer i køen", "artists": []}],
        }
        data.observations._queue_cached_at = time.monotonic()
        warm = await self.queue_read()
        self.assertEqual([row["metadata_known"] for row in warm["items"]], [True, True])
        self.assertEqual([row["name"] for row in warm["items"]], ["Nummer i køen"] * 2)
        for key in ("option_id", "index", "uri"):
            self.assertEqual([row[key] for row in cold["items"]], [row[key] for row in warm["items"]])
        self.assertEqual(cold["queue"]["queue_revision"], warm["queue"]["queue_revision"])
        self.assertEqual(self.queue_posts, [])

    async def test_cold_queue_enriches_exact_native_occurrences(self):
        data = self.entries[0].runtime_data
        data.observations._queue_cache = None
        uri = "spotify:track:" + "0" * 22
        self.queue_corrupt_state = lambda value: value["queue"].update(metadata={
            uri: {"name": "Faktisk køsang", "artist": "Faktisk kunstner", "artwork": "https://i.scdn.co/image/" + "a" * 40},
            "spotify:track:" + "1" * 22: {"name": "Foreign", "artist": "Other"},
        })
        value = await self.queue_read()
        self.assertEqual(self.queue_headers[-1], "1")
        self.assertEqual([row["name"] for row in value["items"]], ["Faktisk køsang"] * 2)
        self.assertEqual([row["metadata_known"] for row in value["items"]], [True, True])
        self.assertEqual([row["artist"] for row in value["items"]], ["Faktisk kunstner"] * 2)
        self.assertEqual([row["artwork"] for row in value["items"]], ["https://i.scdn.co/image/" + "a" * 40] * 2)
        self.assertNotEqual(value["items"][0]["option_id"], value["items"][1]["option_id"])

    async def test_room_read_warms_only_exact_account_labels_without_waiting(self):
        data = self.entries[0].runtime_data
        uri = "spotify:track:" + "0" * 22
        foreign_uri = "spotify:track:" + "1" * 22
        entered, release = asyncio.Event(), asyncio.Event()
        self.holds.append(release)
        requests = 0

        async def account_queue():
            nonlocal requests
            requests += 1
            entered.set()
            await release.wait()
            return {
                "queue": [
                    {
                        "type": "track",
                        "uri": foreign_uri,
                        "name": "Foreign",
                        "artists": [],
                    },
                    {
                        "type": "track",
                        "uri": uri,
                        "name": "Exact song",
                        "artists": [{"name": "Artist"}],
                    },
                ],
            }

        with patch.object(data.observations.api, "queue", account_queue):
            cold = await asyncio.wait_for(self.queue_read(), 1)
            self.assertEqual(
                [row["metadata_known"] for row in cold["items"]], [False, False]
            )
            await asyncio.wait_for(entered.wait(), 1)
            concurrent = await asyncio.wait_for(self.queue_read(), 1)
            self.assertEqual(
                [row["metadata_known"] for row in concurrent["items"]], [False, False]
            )
            self.assertEqual(requests, 1)
            release.set()
            await asyncio.wait_for(
                asyncio.shield(data.commands.room_queue._metadata_task), 1
            )

            warm = await self.queue_read()
            self.assertEqual([row["name"] for row in warm["items"]], ["Exact song"] * 2)
            self.assertEqual([row["artist"] for row in warm["items"]], ["Artist"] * 2)
            self.assertEqual(
                [row["metadata_known"] for row in warm["items"]], [True, True]
            )
            self.assertEqual(
                warm["queue"]["queue_revision"], cold["queue"]["queue_revision"]
            )
            for key in ("option_id", "index", "uri"):
                self.assertEqual(
                    [row[key] for row in warm["items"]],
                    [row[key] for row in cold["items"]],
                )
            self.assertEqual(requests, 1)
            data.observations._queue_cached_at -= 11
            data.observations._queue_next_read_at = 0
            expired = await self.queue_read()
            self.assertEqual(
                [row["metadata_known"] for row in expired["items"]], [False, False]
            )
            await asyncio.wait_for(
                asyncio.shield(data.commands.room_queue._metadata_task), 1
            )
            refreshed = await self.queue_read()
            self.assertEqual(
                [row["name"] for row in refreshed["items"]], ["Exact song"] * 2
            )
            self.assertEqual(requests, 2)
        self.assertEqual(self.queue_posts, [])

    async def test_room_metadata_warm_cannot_publish_after_replacement_or_cancel(self):
        entry = self.entries[0]
        data = entry.runtime_data
        reader = data.observations
        for mode in ("replacement", "cancel"):
            with self.subTest(mode=mode):
                entered, release = asyncio.Event(), asyncio.Event()
                self.holds.append(release)

                async def account_queue(entered=entered, release=release):
                    entered.set()
                    await release.wait()
                    return {"queue": []}

                reader._queue_next_read_at = 0
                with patch.object(reader.api, "queue", account_queue):
                    await self.queue_read()
                    await asyncio.wait_for(entered.wait(), 1)
                    task = data.commands.room_queue._metadata_task
                    if mode == "replacement":
                        entry.runtime_data = copy.copy(data)
                        try:
                            release.set()
                            await asyncio.wait_for(asyncio.shield(task), 1)
                        finally:
                            entry.runtime_data = data
                    else:
                        task.cancel()
                        with self.assertRaises(asyncio.CancelledError):
                            await asyncio.wait_for(asyncio.shield(task), 1)
                self.assertIsNone(reader._queue_cache)
                self.assertFalse(reader._queue_busy)
        self.assertEqual(self.queue_posts, [])

    async def test_new_control_accepts_old_manager_queue_shape(self):
        self.entries[0].runtime_data.observations._queue_cache = None
        value = await self.queue_read()
        self.assertEqual(self.queue_headers[-1], "1")
        self.assertEqual([row["name"] for row in value["items"]], ["Nummer i køen"] * 2)
        self.assertNotEqual(value["items"][0]["option_id"], value["items"][1]["option_id"])

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
