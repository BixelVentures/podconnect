"""Actual HA entity and shared Control owners against inert loopback backends."""

import asyncio
import json
import unittest
from types import SimpleNamespace
from unittest.mock import patch

import test_room_content as fixtures
import test_room_move as move_fixtures
from aiohttp import web
from homeassistant.exceptions import HomeAssistantError
from homeassistant.helpers import entity_registry

from custom_components.podconnect.speakers import SpeakersError


class MediaPlayerNativeOwnerTests(fixtures.RoomContentFrameworkTests):
    async def asyncSetUp(self):
        self.native_id = "a" * 40
        self.external_id = "b" * 40
        self.alpha_output = "123"
        self.identity_status = 200
        self.identity_hold = False
        self.identity_entered = asyncio.Event()
        self.identity_release = asyncio.Event()
        self.native_state_reads = 0
        self.native_state_hold_at = None
        self.native_state_entered = asyncio.Event()
        self.native_state_release = asyncio.Event()
        self.search_hold = False
        self.search_entered = asyncio.Event()
        self.search_release = asyncio.Event()
        self.search_uri = "spotify:track:" + "Q" * 22
        self.library_rows = [{"uri": "spotify:track:0" + "A" * 21}]
        self.library_hold = False
        self.library_entered = asyncio.Event()
        self.library_release = asyncio.Event()
        self.library_status = 200
        self.content_actions = [
            "play_track",
            "play_context",
            "play_episode",
            "play_tracks",
        ]
        self.source_selection_fixture = False
        self.source_move_posts = []
        self.source_release_status = 200
        self.source_has_release = True
        self.source_idle_probe_status = 200
        self.source_idle_reads = 0
        self.source_idle_echo = True
        self.source_alias_names = ("Alpha", "Beta")
        self.source_external_name = "External"
        self.source_release_corrupt = None
        self.source_effects = []
        self.source_external_restricted = False
        self.source_current_target = None
        self.alias_reads = 0
        self.alias_hold_at = None
        self.alias_entered = asyncio.Event()
        self.alias_release = asyncio.Event()
        await super().asyncSetUp()
        self.content_target = self.native_id
        self.output_observation = True
        self.holds.extend(
            (
                self.identity_release,
                self.native_state_release,
                self.search_release,
                self.alias_release,
                self.library_release,
            )
        )
        self.alias_reads = 0
        self.data = self.entries[0].runtime_data
        registry = entity_registry.async_get(self.hass)
        self.entity_id = registry.async_get_entity_id(
            "media_player", "podconnect", "account-alpha_" + self.native_id
        )
        self.assertIsNotNone(self.entity_id)
        self.entity = self.hass.data["media_player"].get_entity(self.entity_id)
        self.assertIsNotNone(self.entity)
        self.calls.clear()

    def native(self):
        value = super().native()
        value["target_id"] = self.native_id
        return value

    def content_result(self, suffix, state):
        value = super().content_result(suffix, state)
        value.update(room_id="room-alpha", homepod_id="123")
        return value

    async def _backend(self, request):
        if self.source_selection_fixture:
            if (
                request.path == "/api/rooms"
                and request.query.get("include_release") == "1"
            ):
                return web.json_response(
                    {
                        "rooms": [
                            {
                                "id": "room-alpha",
                                "homepod_id": "123",
                                "release": {"owner_binding_id": "d" * 64}
                                if self.source_has_release
                                else None,
                            },
                            {"id": "room-beta", "homepod_id": "456"},
                        ]
                    }
                )
            if request.path == "/api/release":
                body = await request.json()
                self.source_effects.append((request.path, body))
                if self.source_release_status != 200:
                    return web.Response(status=self.source_release_status)
                result = {
                    "schema": "podconnect.release_result.v1",
                    "ok": True,
                    "prepare_id": "a" * 32,
                    "outcome": "retired",
                    "terminal": True,
                    "contexts_drained": True,
                    "retry_allowed": False,
                    "physical_result_verified": False,
                }
                if self.source_release_corrupt:
                    self.source_release_corrupt(result)
                return web.json_response(result)
            if request.path == "/api/room-switch" and request.method == "POST":
                body = await request.json()
                self.source_effects.append((request.path, body))
                native = self.native()
                return web.json_response(
                    {
                        "accepted_local": True,
                        "binding": {
                            "ready": True,
                            "incarnation": native["alias_incarnation"],
                            "registry": native["alias_registry"],
                            "room_id": body["room_id"],
                        },
                        **(
                            {"idle_only": True} if body.get("idle_only") is True else {}
                        ),
                    }
                )
            if request.path == "/spotify/me/player" and request.method == "PUT":
                body = await request.json()
                self.source_effects.append((request.path, body))
                self.calls.append((request.method, request.path, b"", True))
                return web.Response(status=204)
            if request.path == "/api/room-switch" and request.method == "GET":
                if request.query.get("idle_only") == "1":
                    self.source_idle_reads += 1
                    if self.source_idle_probe_status != 200:
                        return web.Response(status=self.source_idle_probe_status)
                self.alias_reads += 1
                self.alias_entered.set()
                if self.alias_reads == self.alias_hold_at:
                    await self.alias_release.wait()
                native = self.native()
                return web.json_response(
                    {
                        "binding": {
                            "ready": True,
                            "incarnation": native["alias_incarnation"],
                            "registry": native["alias_registry"],
                            "room_id": native["room_id"],
                        },
                        **(
                            {"idle_only": True}
                            if request.query.get("idle_only") == "1"
                            and self.source_idle_echo
                            else {}
                        ),
                        "aliases": [
                            {
                                "id": 1,
                                "room_id": "room-alpha",
                                "name": self.source_alias_names[0],
                            },
                            {
                                "id": 2,
                                "room_id": "room-beta",
                                "name": self.source_alias_names[1],
                            },
                        ],
                    }
                )
            if request.path == "/spotify/me/player/devices":
                return web.json_response(
                    {
                        "devices": [
                            {
                                "id": self.native_id,
                                "name": "Alpha",
                                "is_restricted": False,
                                "is_active": True,
                                "volume_percent": 30,
                                "supports_volume": True,
                            },
                            {
                                "id": self.external_id,
                                "name": self.source_external_name,
                                "is_restricted": self.source_external_restricted,
                                "is_active": False,
                                "volume_percent": 30,
                                "supports_volume": True,
                            },
                        ]
                    }
                )
            if request.path == "/api/room-move/state":
                self.move_offer_id = "a" * 32
                self.assertEqual(request.query["target_id"], self.native_id)
                return web.json_response(
                    {
                        "schema": "podconnect.room_move_state.v1",
                        "ok": True,
                        "move_offer": move_fixtures.RoomMoveFrameworkTests.move_offer_value(
                            self
                        ),
                        "actions": ["move_existing_session"],
                        "physical_result_verified": False,
                    }
                )
            if request.path.startswith("/api/room-move/"):
                body = await request.json()
                self.source_move_posts.append((request.path, body))
                if request.path.endswith("prepare"):
                    self.content_entered.set()
                    if self.content_hold:
                        await self.content_release.wait()
                    return web.json_response(
                        move_fixtures.RoomMoveFrameworkTests.move_result(
                            self, "prepare", "staged"
                        )
                    )
                if request.path.endswith("cancel"):
                    return web.json_response(
                        move_fixtures.RoomMoveFrameworkTests.move_result(
                            self, "cancel", "cancelled"
                        )
                    )
                self.assertEqual(request.path, "/api/room-move/commit")
                return web.json_response(
                    move_fixtures.RoomMoveFrameworkTests.move_result(
                        self, "commit", "complete"
                    )
                )
        if (
            request.path
            in {
                "/spotify/me/player/seek",
                "/spotify/me/player/shuffle",
                "/spotify/me/player/repeat",
                "/spotify/me/player/play",
            }
            and request.method == "PUT"
        ):
            self.assertEqual(
                request.headers["Authorization"], "Bearer inert-fixture-only"
            )
            self.calls.append(
                (request.method, request.path, await request.read(), True)
            )
            self.command_queries.append(dict(request.query))
            return web.Response(status=204)
        if request.path in {
            "/spotify/me/tracks",
            "/spotify/me/top/tracks",
            "/spotify/me/player/recently-played",
        }:
            self.library_entered.set()
            if self.library_hold:
                await self.library_release.wait()
            if self.library_status != 200:
                return web.Response(status=self.library_status)
            rows = self.library_rows
            if request.path != "/spotify/me/top/tracks":
                rows = [{"track": row} for row in rows]
            return web.json_response({"items": rows})
        if request.path == "/api/room-switch" and request.method == "GET":
            self.alias_reads += 1
            if self.alias_reads == self.alias_hold_at:
                self.alias_entered.set()
                await self.alias_release.wait()
        if request.path == "/spotify/search":
            self.search_entered.set()
            if self.search_hold:
                await self.search_release.wait()
            return web.json_response({"tracks": {"items": [{"uri": self.search_uri}]}})
        if request.path == "/api/start-volume/identity":
            self.identity_entered.set()
            if self.identity_hold:
                await self.identity_release.wait()
            if self.identity_status != 200:
                return web.Response(status=self.identity_status)
            return web.json_response(
                {
                    "schema": "podconnect.start_identity.v1",
                    "engine_device_id": self.native_id,
                }
            )
        if request.method == "GET" and request.path == "/spotify/me/player/devices":
            return web.json_response(
                {
                    "devices": [
                        {
                            "id": target,
                            "name": "SAME NAME",
                            "is_restricted": False,
                            "is_active": target == self.native_id,
                            "volume_percent": 30,
                            "supports_volume": True,
                        }
                        for target in (self.native_id, self.external_id)
                    ]
                }
            )
        if request.method == "GET" and request.path == "/spotify/me/player":
            return web.json_response(
                {
                    "is_playing": self.playing,
                    "device": {
                        "id": self.source_current_target or self.native_id,
                        "name": "SAME NAME",
                        "is_restricted": False,
                        "volume_percent": 30,
                        "supports_volume": True,
                    },
                    "actions": {"disallows": {}},
                    "item": None,
                    "progress_ms": 0,
                    "shuffle_state": False,
                    "repeat_state": "off",
                }
            )
        if request.path == "/api/room-transport/state":
            self.native_state_reads += 1
            if self.native_state_reads == self.native_state_hold_at:
                self.native_state_entered.set()
                await self.native_state_release.wait()
        return await super()._backend(request)

    async def library_call(self, source="liked", shuffle=False):
        return await self.hass.services.async_call(
            "podconnect",
            "play_from_library",
            {"entity_id": self.entity_id, "source": source, "shuffle": shuffle},
            blocking=True,
        )

    async def test_media_native_library_ordered_duplicates_use_one_content_owner(self):
        self.library_rows = [
            {"uri": "spotify:track:0" + value * 21} for value in ("A", "B", "A")
        ]
        try:
            await self.library_call(shuffle=True)
        except (HomeAssistantError, SpeakersError):
            # The old raw path can fail its later START guard after already
            # sending shuffle. Keep that first bypass as the causal assertion.
            self.assertEqual(self._writes(), [])
            raise
        self.assertEqual(self._writes(), [])
        self.assertEqual(len(self.prepares()), 1)
        self.assertEqual(self.prepares()[0]["action"], "play_tracks")
        self.assertEqual(
            self.prepares()[0]["args"],
            {
                "track_uris": [row["uri"] for row in self.library_rows],
                "shuffle": True,
                "library_source": "liked",
                "max_volume": 100,
            },
        )
        self.assertEqual(len(self.content_commits()), 1)
        self.assertIsNone(self.entity._optimistic_shuffle)

    async def test_media_native_library_source_kinds_preserve_handler_result(self):
        self.library_rows = [{"uri": "spotify:track:0" + ch * 21} for ch in "ABA"]
        for source in ("liked", "top_tracks", "recent"):
            await self.data.coordinator.async_refresh()
            await self.library_call(source)
            expected = (
                self.library_rows[:2] if source == "recent" else self.library_rows
            )
            self.assertEqual(
                self.prepares()[-1]["args"]["track_uris"],
                [row["uri"] for row in expected],
            )
            self.assertEqual(self.prepares()[-1]["args"]["library_source"], source)
        self.assertEqual(self._writes(), [])
        self.assertEqual(len(self.content_commits()), 3)

    async def test_media_native_library_keeps_existing_fifty_row_cap(self):
        self.library_rows = [
            {"uri": f"spotify:track:{index:022d}"} for index in range(51)
        ]
        await self.library_call()
        self.assertEqual(
            self.prepares()[0]["args"]["track_uris"],
            [row["uri"] for row in self.library_rows[:50]],
        )
        self.assertEqual(len(self.content_commits()), 1)
        self.assertEqual(self._writes(), [])

    async def test_media_native_library_empty_malformed_or_overflow_has_no_effect(self):
        for rows in (
            [],
            [{}],
            [{"uri": "spotify:episode:" + "0" * 22}],
            [{"uri": "spotify:track:" + "Z" * 22}],
        ):
            self.library_rows = rows
            with self.subTest(rows=rows), self.assertRaises(HomeAssistantError):
                await self.library_call()
        self.assertEqual(
            (self.prepares(), self.content_commits(), self._writes()), ([], [], [])
        )

    async def test_media_native_library_get_owner_replacement_denies(self):
        self.library_hold = True
        task = self._task(self.library_call())
        await asyncio.wait_for(self.library_entered.wait(), 2)
        self.hass.config_entries.async_update_entry(
            self.entries[0], unique_id="foreign-account"
        )
        self.library_release.set()
        with self.assertRaises(HomeAssistantError):
            await task
        self.assertEqual(
            (self.prepares(), self.content_commits(), self._writes()), ([], [], [])
        )

    async def test_media_native_library_staged_owner_replacement_cancels(self):
        self.content_hold = True
        task = self._task(self.library_call())
        await asyncio.wait_for(self.content_entered.wait(), 2)
        self.hass.config_entries.async_update_entry(
            self.entries[0], unique_id="foreign-account"
        )
        self.content_release.set()
        with self.assertRaises(HomeAssistantError):
            await task
        await self.content_cleanup()
        self.assertEqual(len(self.prepares()), 1)
        self.assertEqual((self.content_commits(), self._writes()), ([], []))

    async def test_media_native_library_source_failure_has_no_effect(self):
        self.library_status = 403
        with self.assertRaises(HomeAssistantError):
            await self.library_call()
        self.assertEqual(
            (self.prepares(), self.content_commits(), self._writes()), ([], [], [])
        )

    async def test_media_native_library_get_routine_poll_uses_fresh_admission(self):
        self.library_hold = True
        task = self._task(self.library_call())
        await asyncio.wait_for(self.library_entered.wait(), 2)
        await self.data.coordinator.async_refresh()
        self.library_release.set()
        await task
        self.assertEqual(len(self.content_commits()), 1)
        self.assertEqual(self._writes(), [])

    async def test_media_native_library_get_identity_replacement_never_falls_back(self):
        self.library_hold = True
        task = self._task(self.library_call())
        await asyncio.wait_for(self.library_entered.wait(), 2)
        self.native_id = "c" * 40
        self.library_release.set()
        with self.assertRaises(HomeAssistantError):
            await task
        self.assertEqual(
            (self.prepares(), self.content_commits(), self._writes()), ([], [], [])
        )

    async def test_media_native_library_get_epoch_replacement_denies(self):
        self.library_hold = True
        task = self._task(self.library_call())
        await asyncio.wait_for(self.library_entered.wait(), 2)
        self.data.coordinator.owner_epoch = "replacement"
        self.library_release.set()
        with self.assertRaises(HomeAssistantError):
            await task
        self.assertEqual(
            (self.prepares(), self.content_commits(), self._writes()), ([], [], [])
        )

    async def test_media_native_library_get_target_replacement_denies(self):
        self.library_hold = True
        task = self._task(self.library_call())
        await asyncio.wait_for(self.library_entered.wait(), 2)
        self.entity._device_id = self.external_id
        self.library_release.set()
        with self.assertRaises(HomeAssistantError):
            await task
        self.assertEqual(
            (self.prepares(), self.content_commits(), self._writes()), ([], [], [])
        )

    async def test_media_native_library_duplicate_unknown_never_falls_back(self):
        self.content_commit_hold = True
        task = self._task(self.library_call())
        await asyncio.wait_for(self.content_commit_entered.wait(), 2)
        with self.assertRaises(HomeAssistantError):
            await self.library_call()
        self.content_commit_status = 500
        self.content_commit_release.set()
        with self.assertRaises(HomeAssistantError):
            await task
        self.assertEqual(len(self.prepares()), 1)
        self.assertEqual(len(self.content_commits()), 1)
        self.assertEqual(self._writes(), [])
        self.assertEqual(self.data.commands.receipts[1][1]["outcome"], "unknown")

    async def test_media_native_library_cancelled_get_has_no_effect(self):
        self.library_hold = True
        task = self._task(self.library_call())
        await asyncio.wait_for(self.library_entered.wait(), 2)
        task.cancel()
        self.library_release.set()
        with self.assertRaises(asyncio.CancelledError):
            await task
        self.assertEqual(
            (self.prepares(), self.content_commits(), self._writes()), ([], [], [])
        )

    async def test_media_native_library_external_target_preserves_raw_adapter(self):
        self.entity_id = entity_registry.async_get(self.hass).async_get_entity_id(
            "media_player", "podconnect", "account-alpha_" + self.external_id
        )
        await self.library_call(shuffle=True)
        self.assertEqual(len(self._writes()), 2)
        self.assertEqual(self.command_queries[-1]["device_id"], self.external_id)
        self.assertEqual((self.prepares(), self.content_commits()), ([], []))

    async def test_media_native_selected_track_and_context_use_room_content(self):
        for uri, action, key in (
            ("spotify:track:" + "A" * 22, "play_track", "track_uri"),
            ("spotify:playlist:" + "B" * 22, "play_context", "context_uri"),
            ("spotify:album:" + "C" * 22, "play_context", "context_uri"),
            ("spotify:artist:" + "D" * 22, "play_context", "context_uri"),
            ("spotify:show:" + "E" * 22, "play_context", "context_uri"),
        ):
            await self.data.coordinator.async_refresh()
            self.entity._optimistic_playing = True
            self.entity._optimistic_shuffle = True
            self.entity._optimistic_repeat = "track"
            self.entity._optimistic_poll_floor = 44
            await self.native_call(
                "play_media", media_content_type="music", media_content_id=uri
            )
            sent = self.prepares()[-1]
            self.assertEqual(sent["action"], action)
            self.assertEqual(sent["args"], {key: uri, "max_volume": 100})
            self.assertEqual(
                (sent["scope"]["room_id"], sent["scope"]["homepod_id"]),
                ("room-alpha", "123"),
            )
        self.assertEqual(len(self.prepares()), 5)
        self.assertEqual(len(self.content_commits()), 5)
        self.assertEqual(self._writes(), [])
        self.assertIsNone(self.entity._optimistic_playing)
        self.assertIsNone(self.entity._optimistic_shuffle)
        self.assertIsNone(self.entity._optimistic_repeat)
        self.assertIsNone(self.entity._optimistic_poll_floor)
        self.assertEqual(self.data.commands.command_sequence, 5)

    async def test_media_native_selected_search_preserves_actual_uri_and_owner(self):
        self.no_playback = True
        await self.data.coordinator.async_refresh()
        await self.native_call(
            "play_media", media_content_type="music", media_content_id="chosen song"
        )
        self.assertEqual(self.prepares()[0]["args"]["track_uri"], self.search_uri)
        self.assertEqual(len(self.content_commits()), 1)
        self.assertEqual(self._writes(), [])

    async def test_media_native_routine_poll_during_staging_keeps_same_owner(self):
        self.content_hold = True
        task = self._task(
            self.native_call(
                "play_media",
                media_content_type="track",
                media_content_id="spotify:track:" + "A" * 22,
            )
        )
        await asyncio.wait_for(self.content_entered.wait(), 2)
        await self.data.coordinator.async_refresh()
        self.content_release.set()
        await task
        self.assertEqual((len(self.prepares()), len(self.content_commits())), (1, 1))
        self.assertEqual(self._writes(), [])
        self.assertEqual(self.data.commands.receipts[1][1]["outcome"], "accepted")

    async def test_media_native_search_owner_replacement_has_no_effect(self):
        self.search_hold = True
        task = self._task(
            self.native_call(
                "play_media", media_content_type="music", media_content_id="chosen song"
            )
        )
        await asyncio.wait_for(self.search_entered.wait(), 2)
        await self.data.coordinator.async_refresh()
        self.search_release.set()
        with self.assertRaises(HomeAssistantError):
            await task
        self.assertEqual(
            (self.prepares(), self.content_commits(), self._writes()), ([], [], [])
        )

    async def test_media_native_selected_double_during_commit_does_not_replay(self):
        self.content_commit_hold = True
        uri = "spotify:track:" + "A" * 22
        first = self._task(
            self.native_call(
                "play_media", media_content_type="track", media_content_id=uri
            )
        )
        await asyncio.wait_for(self.content_commit_entered.wait(), 2)
        with self.assertRaises(HomeAssistantError):
            await self.native_call(
                "play_media", media_content_type="track", media_content_id=uri
            )
        self.content_commit_release.set()
        await first
        self.assertEqual((len(self.prepares()), len(self.content_commits())), (1, 1))
        self.assertEqual(self._writes(), [])

    async def test_media_native_selected_cancelled_staging_cancels_without_send(self):
        self.content_hold = True
        task = self._task(
            self.native_call(
                "play_media",
                media_content_type="track",
                media_content_id="spotify:track:" + "A" * 22,
            )
        )
        await asyncio.wait_for(self.content_entered.wait(), 2)
        task.cancel()
        self.content_release.set()
        with self.assertRaises(asyncio.CancelledError):
            await task
        await self.content_cleanup()
        self.assertEqual(len(self.prepares()), 1)
        self.assertEqual(self.content_commits(), [])
        self.assertEqual(self._writes(), [])

    async def test_media_native_alias_changes_after_staging_cancels_before_commit(self):
        self.alias_hold_at = 2
        task = self._task(
            self.native_call(
                "play_media",
                media_content_type="track",
                media_content_id="spotify:track:" + "A" * 22,
            )
        )
        await asyncio.wait_for(self.alias_entered.wait(), 2)
        self.output_room = "room-beta"
        self.alias_release.set()
        with self.assertRaises(HomeAssistantError):
            await task
        self.assertEqual(len(self.prepares()), 1)
        self.assertEqual(self.content_commits(), [])
        self.assertEqual(
            len([path for path, _ in self.content_posts if path.endswith("cancel")]), 1
        )
        self.assertEqual(self._writes(), [])

    async def test_media_native_cancel_after_staging_retires_one_preparation(self):
        self.alias_hold_at = 2
        task = self._task(
            self.native_call(
                "play_media",
                media_content_type="track",
                media_content_id="spotify:track:" + "A" * 22,
            )
        )
        await asyncio.wait_for(self.alias_entered.wait(), 2)
        task.cancel()
        self.alias_release.set()
        with self.assertRaises(asyncio.CancelledError):
            await task
        self.assertEqual(len(self.prepares()), 1)
        self.assertEqual(self.content_commits(), [])
        self.assertEqual(
            len([path for path, _ in self.content_posts if path.endswith("cancel")]), 1
        )
        self.assertEqual(self._writes(), [])

    async def test_media_native_selected_unknown_has_no_generic_fallback(self):
        self.content_commit_status = 500
        with self.assertRaises(HomeAssistantError):
            await self.native_call(
                "play_media",
                media_content_type="track",
                media_content_id="spotify:track:" + "A" * 22,
            )
        self.assertEqual(len(self.content_commits()), 1)
        self.assertEqual(self._writes(), [])
        self.assertEqual(self.data.commands.receipts[1][1]["outcome"], "unknown")

    async def test_media_native_unsupported_content_does_not_use_generic_api(self):
        with self.assertRaises(HomeAssistantError):
            await self.native_call(
                "play_media",
                media_content_type="audiobook",
                media_content_id="spotify:audiobook:" + "A" * 22,
            )
        self.assertEqual(
            (self.prepares(), self.content_commits(), self._writes()), ([], [], [])
        )

    async def test_media_native_other_device_keeps_existing_play_media_path(self):
        registry = entity_registry.async_get(self.hass)
        other = registry.async_get_entity_id(
            "media_player", "podconnect", "account-alpha_" + self.external_id
        )
        await self.hass.services.async_call(
            "media_player",
            "play_media",
            {
                "entity_id": other,
                "media_content_type": "track",
                "media_content_id": "spotify:track:" + "A" * 22,
            },
            blocking=True,
        )
        self.assertEqual(len(self._writes()), 1)
        self.assertEqual((self.prepares(), self.content_commits()), ([], []))

    async def test_media_select_configured_alias_uses_room_move_preserving_pause(self):
        self.source_selection_fixture = True
        self.beta_output = "456"
        for playing in (True, False):
            self.playing = playing
            await self.data.coordinator.async_refresh()
            await self.native_call("select_source", source="Beta")
            prepare = self.source_move_posts[-2][1]
            self.assertEqual(prepare["action"], "move_existing_session")
            self.assertEqual(prepare["args"], {})
            self.assertEqual(prepare["scope"]["room_id"], "room-beta")
            self.assertEqual(prepare["scope"]["homepod_id"], "456")
            self.assertEqual(self.playing, playing)
        self.assertEqual(
            [path for path, _ in self.source_move_posts],
            ["/api/room-move/prepare", "/api/room-move/commit"] * 2,
        )
        self.assertEqual(self.data.commands.command_sequence, 2)
        self.assertEqual(self._writes(), [])

    async def test_media_select_source_list_reads_only_cached_aliases(self):
        from custom_components.podconnect.targets import configured_alias_targets

        self.source_selection_fixture = True
        await configured_alias_targets(self.data.speakers)
        reads = self.alias_reads
        for _ in range(3):
            self.assertIn("Beta", self.entity.source_list)
            self.assertIn("Alpha", self.entity.source_list)
        self.assertEqual(self.alias_reads, reads)
        self.assertEqual(
            (self.source_effects, self.source_move_posts, self._writes()), ([], [], [])
        )

    async def test_media_select_native_to_external_releases_once_before_transfer(self):
        self.source_selection_fixture = True
        self.playing = False
        await self.native_call("select_source", source="External")
        self.assertEqual(
            [path for path, _ in self.source_effects],
            ["/api/release", "/spotify/me/player"],
        )
        release = self.source_effects[0][1]
        self.assertEqual(set(release), {"room_id", "expected_binding_id", "request_id"})
        self.assertEqual(release["room_id"], "room-alpha")
        self.assertEqual(release["expected_binding_id"], "d" * 64)
        self.assertRegex(release["request_id"], r"^[0-9a-f]{32}$")
        self.assertEqual(
            self.source_effects[1][1], {"device_ids": [self.external_id], "play": False}
        )
        self.assertEqual(self.source_move_posts, [])

    async def test_media_select_native_unknown_release_cannot_transfer(self):
        self.source_selection_fixture = True
        self.source_release_corrupt = lambda value: value.update(contexts_drained=False)
        with self.assertRaises(HomeAssistantError):
            await self.native_call("select_source", source="External")
        self.assertEqual([path for path, _ in self.source_effects], ["/api/release"])
        self.assertEqual(self._writes(), [])

    async def test_media_select_external_source_uses_existing_adapter_without_release(
        self,
    ):
        self.source_selection_fixture = True
        self.source_has_release = False
        self.source_current_target = self.external_id
        self.playing = True
        await self.native_call("select_source", source="External")
        self.assertEqual(
            self.source_effects,
            [("/spotify/me/player", {"device_ids": [self.external_id], "play": True})],
        )
        self.assertEqual(self.source_move_posts, [])

    async def test_media_select_idle_native_uses_explicit_idle_guard(self):
        self.source_selection_fixture = True
        self.source_current_target = self.external_id
        await self.native_call("select_source", source="Beta")
        self.assertEqual(
            self.source_effects,
            [
                (
                    "/api/room-switch",
                    {
                        "room_id": "room-beta",
                        "expected_incarnation": "incarnation-a",
                        "expected_registry": "registry-a",
                        "idle_only": True,
                    },
                )
            ],
        )
        self.assertEqual((self.source_move_posts, self._writes()), ([], []))

    async def test_media_select_unknown_identity_and_owner_change_have_no_effect(self):
        self.source_selection_fixture = True
        self.identity_status = 503
        with self.assertRaises(HomeAssistantError):
            await self.native_call("select_source", source="External")
        self.identity_status = 200
        self.identity_hold = True
        self.identity_entered.clear()
        task = self._task(self.native_call("select_source", source="Beta"))
        await asyncio.wait_for(self.identity_entered.wait(), 2)
        await self.data.coordinator.async_refresh()
        self.identity_release.set()
        with self.assertRaises(HomeAssistantError):
            await task
        self.assertEqual(
            (self.source_effects, self.source_move_posts, self._writes()), ([], [], [])
        )

    async def test_media_select_typed_services_share_same_move_and_release_owners(self):
        self.source_selection_fixture = True
        self.beta_output = "456"
        alias = await self.hass.services.async_call(
            "podconnect",
            "move_playback",
            {
                "config_entry_id": self.entries[0].entry_id,
                "kind": "configured_alias",
                "target_id": "room-beta",
            },
            blocking=True,
            return_response=True,
        )
        self.assertEqual(alias["outcome"], "accepted")
        self.assertTrue(alias["accepted_room_move"])
        await self.data.coordinator.async_refresh()
        external = await self.hass.services.async_call(
            "podconnect",
            "move_playback",
            {
                "config_entry_id": self.entries[0].entry_id,
                "kind": "spotify_device",
                "target_id": self.external_id,
            },
            blocking=True,
            return_response=True,
        )
        self.assertTrue(external["provider_request_accepted"])
        self.assertEqual(
            [path for path, _ in self.source_effects],
            ["/api/release", "/spotify/me/player"],
        )
        self.assertEqual(len(self.source_move_posts), 2)
        self.assertEqual(self.data.commands.command_sequence, 1)

    async def test_media_select_release_then_restricted_target_never_transfers(self):
        self.source_selection_fixture = True
        self.source_release_corrupt = lambda value: setattr(
            self, "source_external_restricted", True
        )
        with self.assertRaises(HomeAssistantError):
            await self.native_call("select_source", source="External")
        self.assertEqual([path for path, _ in self.source_effects], ["/api/release"])
        self.assertEqual(self._writes(), [])

    async def test_media_select_release_does_not_require_retired_native_alias(self):
        self.source_selection_fixture = True
        self.source_release_corrupt = lambda value: setattr(
            self, "identity_status", 503
        )
        await self.native_call("select_source", source="External")
        self.assertEqual(
            [path for path, _ in self.source_effects],
            ["/api/release", "/spotify/me/player"],
        )

    async def test_media_select_provider_external_retained_writer_is_released(self):
        self.source_selection_fixture = True
        self.source_current_target = self.external_id
        await self.native_call("select_source", source="External")
        self.assertEqual(
            [path for path, _ in self.source_effects],
            ["/api/release", "/spotify/me/player"],
        )
        self.assertEqual(self.source_idle_reads, 1)

    async def test_media_select_null_release_unknown_custody_cannot_transfer(self):
        self.source_selection_fixture = True
        self.source_current_target = self.external_id
        self.source_has_release = False
        self.source_idle_probe_status = 409
        with self.assertRaises(HomeAssistantError):
            await self.native_call("select_source", source="External")
        self.assertEqual(self.source_idle_reads, 1)
        self.assertEqual((self.source_effects, self._writes()), ([], []))

    async def test_media_select_foreign_retained_writer_cannot_release_or_transfer(
        self,
    ):
        self.source_selection_fixture = True
        self.source_current_target = self.external_id
        self.state_corrupt = lambda state: state["native"].update(
            account_user_id="foreign-account"
        )
        with self.assertRaises(HomeAssistantError):
            await self.native_call("select_source", source="External")
        self.assertEqual((self.source_effects, self._writes()), ([], []))

    async def test_media_select_alias_name_collisions_have_no_effects(self):
        self.source_selection_fixture = True
        self.source_external_name = "Beta"
        with self.assertRaises(HomeAssistantError):
            await self.native_call("select_source", source="Beta")
        self.source_external_name = "External"
        self.source_alias_names = ("Beta", "Beta")
        with self.assertRaises(HomeAssistantError):
            await self.native_call("select_source", source="Beta")
        self.assertEqual(
            (self.source_effects, self.source_move_posts, self._writes()), ([], [], [])
        )

    async def test_media_select_old_manager_without_idle_echo_cannot_transfer(self):
        self.source_selection_fixture = True
        self.source_has_release = False
        self.source_idle_echo = False
        self.source_current_target = self.external_id
        with self.assertRaises(HomeAssistantError):
            await self.native_call("select_source", source="External")
        self.assertEqual((self.source_effects, self._writes()), ([], []))

    async def test_media_select_changed_destination_after_staging_cancels_move(self):
        self.source_selection_fixture = True
        self.beta_output = "456"
        self.content_hold = True
        task = self._task(self.native_call("select_source", source="Beta"))
        await asyncio.wait_for(self.content_entered.wait(), 2)
        self.beta_output = "789"
        self.content_release.set()
        with self.assertRaises(HomeAssistantError):
            await task
        cleanup = list(self.data.commands.room_move.cleanups)
        if cleanup:
            await asyncio.wait_for(asyncio.gather(*cleanup), 2)
        self.assertEqual(
            [path for path, _ in self.source_move_posts],
            ["/api/room-move/prepare", "/api/room-move/cancel"],
        )
        self.assertEqual((self.source_effects, self._writes()), ([], []))

    async def test_media_select_new_native_during_transfer_oauth_stops_external_send(
        self,
    ):
        self.source_selection_fixture = True
        entered, release = asyncio.Event(), asyncio.Event()
        self.holds.append(release)
        original = self.data.session.async_ensure_token_valid
        calls = 0

        async def held():
            nonlocal calls
            calls += 1
            await original()
            # Initial device/playback reads precede the transfer's own OAuth fence.
            if calls == 3:
                entered.set()
                await release.wait()

        with patch.object(self.data.session, "async_ensure_token_valid", held):
            task = self._task(self.native_call("select_source", source="External"))
            await asyncio.wait_for(entered.wait(), 2)
            self.assertEqual(
                [path for path, _ in self.source_effects], ["/api/release"]
            )
            # A new Native owner is active while OAuth is suspended. Its strict
            # idle read rejects; this operation must not release that newer owner.
            self.source_idle_probe_status = 409
            release.set()
            with self.assertRaises(HomeAssistantError):
                await task
        self.assertEqual([path for path, _ in self.source_effects], ["/api/release"])
        self.assertEqual(self.source_idle_reads, 1)
        self.assertEqual(self._writes(), [])

    async def test_media_select_current_alias_requires_observed_route_without_mutation(
        self,
    ):
        self.source_selection_fixture = True
        result = await self.hass.services.async_call(
            "podconnect",
            "move_playback",
            {
                "config_entry_id": self.entries[0].entry_id,
                "kind": "configured_alias",
                "target_id": "room-alpha",
            },
            blocking=True,
            return_response=True,
        )
        self.assertEqual(
            result,
            {
                "kind": "configured_alias",
                "target_id": "room-alpha",
                "observed_current": True,
                "physical_result_verified": False,
            },
        )
        self.assertNotIn("accepted_room_move", result)
        self.state_corrupt = lambda state: state["route"].update(device_id="999")
        with self.assertRaises(HomeAssistantError):
            await self.native_call("select_source", source="Alpha")
        self.assertEqual(
            (self.source_effects, self.source_move_posts, self._writes()), ([], [], [])
        )
        self.assertEqual(self.data.commands.command_sequence, 0)

    async def test_media_native_options_use_one_typed_owner_without_optimism(self):
        for service, arguments, action, field, expected in (
            ("shuffle_set", {"shuffle": True}, "shuffle", "shuffle_enabled", True),
            ("shuffle_set", {"shuffle": False}, "shuffle", "shuffle_enabled", False),
            ("repeat_set", {"repeat": "all"}, "repeat", "repeat_mode", "context"),
            ("repeat_set", {"repeat": "one"}, "repeat", "repeat_mode", "track"),
            ("repeat_set", {"repeat": "off"}, "repeat", "repeat_mode", "off"),
        ):
            await self.data.coordinator.async_refresh()
            with self.subTest(service=service, expected=expected):
                await self.native_call(service, **arguments)
                prepared = self.room_posts[-2][1]
                self.assertEqual(prepared["action"], action)
                self.assertEqual(prepared[field], expected)
                self.assertIs(type(prepared[field]), type(expected))
                self.assertEqual(
                    set(prepared),
                    {"scope", "native", "route", "action", "volume_percent", field},
                )
                self.assertIsNone(self.entity._optimistic_shuffle)
                self.assertIsNone(self.entity._optimistic_repeat)
        self.assertEqual(self._writes(), [])
        self.assertEqual(len(self.room_posts), 10)
        self.assertEqual(self.data.commands.command_sequence, 5)

    async def test_media_native_options_unknown_identity_and_changed_owner_are_closed(
        self,
    ):
        self.identity_status = 503
        with self.assertRaises(HomeAssistantError):
            await self.native_call("shuffle_set", shuffle=True)
        self.identity_status = 200
        self.identity_hold = True
        self.identity_entered.clear()
        task = self._task(self.native_call("repeat_set", repeat="all"))
        await asyncio.wait_for(self.identity_entered.wait(), 2)
        await self.data.coordinator.async_refresh()
        self.identity_release.set()
        with self.assertRaises(HomeAssistantError):
            await task
        self.assertEqual((self.room_posts, self._writes()), ([], []))

    async def test_media_native_options_changed_echo_and_unknown_commit_never_fallback(
        self,
    ):
        self.prepare_corrupt = lambda value: value.update(shuffle_enabled=1)
        with self.assertRaises(HomeAssistantError):
            await self.native_call("shuffle_set", shuffle=True)
        self.assertEqual(len(self.room_posts), 1)
        await self.data.coordinator.async_refresh()
        self.prepare_corrupt = None
        self.room_status = 500
        with self.assertRaises(HomeAssistantError):
            await self.native_call("repeat_set", repeat="all")
        self.assertEqual(len(self.room_posts), 3)
        self.assertEqual(self._writes(), [])
        self.assertEqual(self.data.commands.receipts[2][1]["outcome"], "unknown")

    async def test_media_native_options_reject_untyped_closed_arguments_before_prepare(
        self,
    ):
        for action, args in (
            ("shuffle", {}),
            ("shuffle", {"enabled": 1}),
            ("shuffle", {"enabled": "true"}),
            ("shuffle", {"enabled": True, "extra": 0}),
            ("repeat", {}),
            ("repeat", {"mode": True}),
            ("repeat", {"mode": "all"}),
            ("repeat", {"mode": "track", "extra": 0}),
        ):
            with self.subTest(action=action, args=args):
                result = await self.data.commands.execute(
                    self.hass,
                    self.room_request(action, target_id=self.native_id, args=args),
                    room_transport={
                        "room_id": "room-alpha",
                        "homepod_id": "123",
                        "native": self.native(),
                        "route": self.route(),
                    },
                )
                self.assertEqual(result["error_kind"], "invalid_arguments")
        self.assertEqual((self.room_posts, self._writes()), ([], []))
        self.assertEqual(self.data.commands.command_sequence, 0)

    async def test_media_native_options_restrictions_apply_only_to_changed_bits(self):
        raw = self.data.coordinator.data["playback"]
        raw["actions"]["disallows"].update(
            toggling_shuffle=True,
            toggling_repeat_context=True,
            toggling_repeat_track=True,
        )
        # Idempotent commands retain their exact no-change intent.
        await self.native_call("shuffle_set", shuffle=False)
        await self.data.coordinator.async_refresh()
        raw = self.data.coordinator.data["playback"]
        raw["actions"]["disallows"].update(
            toggling_repeat_context=True, toggling_repeat_track=True
        )
        await self.native_call("repeat_set", repeat="off")
        for service, arguments in (
            ("shuffle_set", {"shuffle": True}),
            ("repeat_set", {"repeat": "all"}),
            ("repeat_set", {"repeat": "one"}),
        ):
            await self.data.coordinator.async_refresh()
            self.data.coordinator.data["playback"]["actions"]["disallows"].update(
                toggling_shuffle=True,
                toggling_repeat_context=True,
                toggling_repeat_track=True,
            )
            with (
                self.subTest(service=service, arguments=arguments),
                self.assertRaises(HomeAssistantError),
            ):
                await self.native_call(service, **arguments)
        # context -> track changes only the single-track bit, so a context
        # restriction cannot reject it. The reverse transition obeys track's bit.
        await self.data.coordinator.async_refresh()
        raw = self.data.coordinator.data["playback"]
        raw["repeat_state"] = "context"
        raw["actions"]["disallows"].update(toggling_repeat_context=True)
        await self.native_call("repeat_set", repeat="one")
        self.assertEqual(len(self.room_posts), 6)
        self.assertEqual(self._writes(), [])

    async def test_media_native_repeat_restrictions_preserve_each_changed_bit(self):
        for actual, requested, restricted, accepted in (
            ("track", "all", "toggling_repeat_track", False),
            ("track", "off", "toggling_repeat_context", False),
            ("track", "off", "toggling_repeat_track", False),
            ("context", "off", "toggling_repeat_track", True),
            ("off", "all", "toggling_repeat_track", True),
            ("track", "one", "toggling_repeat_track", True),
        ):
            await self.data.coordinator.async_refresh()
            raw = self.data.coordinator.data["playback"]
            raw["repeat_state"] = actual
            raw["actions"]["disallows"][restricted] = True
            count = len(self.room_posts)
            with self.subTest(
                actual=actual, requested=requested, restricted=restricted
            ):
                if accepted:
                    await self.native_call("repeat_set", repeat=requested)
                else:
                    with self.assertRaises(HomeAssistantError):
                        await self.native_call("repeat_set", repeat=requested)
                self.assertEqual(len(self.room_posts), count + (2 if accepted else 0))
        self.assertEqual(self._writes(), [])

    async def test_media_native_options_service_keeps_native_context_required(self):
        for action, args in (
            ("shuffle", {"enabled": True}),
            ("repeat", {"mode": "track"}),
        ):
            await self.data.coordinator.async_refresh()
            request = self.room_request(action, target_id=self.native_id, args=args)
            result = await self.room_invoke(request)
            self.assertEqual(result["outcome"], "accepted")
            self.assertTrue(result["accepted_native"])
            self.assertFalse(result["provider_request_accepted"])
            direct = {
                key: value
                for key, value in request.items()
                if key not in {"room_id", "homepod_id", "native", "route"}
            }
            result = await self.invoke(direct)
            self.assertEqual(result["error_kind"], "native_room_required")
        self.assertEqual(len(self.room_posts), 4)
        self.assertEqual(self._writes(), [])

    async def test_media_native_option_restriction_changed_during_prepare_stops_commit(
        self,
    ):
        self.room_hold = True
        task = self._task(self.native_call("shuffle_set", shuffle=True))
        await asyncio.wait_for(self.room_entered.wait(), 2)
        self.data.coordinator.data["playback"]["actions"]["disallows"][
            "toggling_shuffle"
        ] = True
        self.room_release.set()
        with self.assertRaises(HomeAssistantError):
            await task
        self.assertEqual(len(self.room_posts), 1)
        self.assertEqual(self._writes(), [])
        self.assertEqual(self.data.commands.receipts[1][1]["outcome"], "rejected")

    async def test_media_external_options_keep_existing_spotify_adapter(self):
        other = entity_registry.async_get(self.hass).async_get_entity_id(
            "media_player", "podconnect", "account-alpha_" + self.external_id
        )
        for service, arguments in (
            ("shuffle_set", {"shuffle": True}),
            ("repeat_set", {"repeat": "one"}),
        ):
            await self.hass.services.async_call(
                "media_player",
                service,
                {"entity_id": other, **arguments},
                blocking=True,
            )
        self.assertEqual(
            [call[1] for call in self._writes()],
            ["/spotify/me/player/shuffle", "/spotify/me/player/repeat"],
        )
        self.assertEqual(
            self.command_queries,
            [
                {"state": "true", "device_id": self.external_id},
                {"state": "track", "device_id": self.external_id},
            ],
        )
        self.assertEqual(self.room_posts, [])
        self.assertEqual(self.data.commands.command_sequence, 0)

    async def test_media_native_options_leave_closed_v3_actions_unchanged(self):
        result = await self.hass.services.async_call(
            "podconnect",
            "get_playback_snapshot",
            {
                "config_entry_id": self.entries[0].entry_id,
                "include_output": True,
                "include_controls": True,
            },
            blocking=True,
            return_response=True,
        )
        self.assertEqual(result["controls"]["status"], "observed")
        self.assertTrue(
            set(result["controls"]["supported_actions"])
            <= {"pause", "resume", "next", "previous", "volume"}
        )
        controls = await self.data.commands.room_transport.read(
            self.hass, self.entries[0].entry_id, self.native_id, "room-alpha", "123"
        )
        self.assertTrue({"shuffle", "repeat"} <= set(controls["actions"]))
        self.assertEqual((self.room_posts, self._writes()), ([], []))

    async def test_media_native_seek_uses_exact_room_transport_owner(self):
        self.playing = True
        await self.data.coordinator.async_refresh()
        await self.native_call("media_seek", seek_position=12.345)
        self.assertEqual(self._writes(), [])
        self.assertEqual(
            [path for path, _ in self.room_posts],
            ["/api/room-transport/prepare", "/api/room-transport/commit"],
        )
        self.assertEqual(self.room_posts[0][1]["action"], "seek")
        self.assertEqual(self.room_posts[0][1]["position_ms"], 12345)
        self.assertIsNone(self.entity._optimistic_playing)

    async def test_media_native_seek_zero_and_paused_preserve_typed_intent(self):
        self.playing = False
        await self.data.coordinator.async_refresh()
        await self.native_call("media_seek", seek_position=0)
        self.assertEqual(self.room_posts[0][1]["position_ms"], 0)
        self.assertEqual(len(self.room_posts), 2)
        self.assertEqual(self._writes(), [])
        self.assertIsNone(self.entity._optimistic_playing)

    async def test_media_native_seek_invalid_seconds_have_no_dispatch(self):
        for position in (-1, float("nan"), float("inf"), 2**53):
            with self.subTest(position=position), self.assertRaises(HomeAssistantError):
                await self.entity.async_media_seek(position)
        self.assertEqual((self.room_posts, self._writes()), ([], []))

    async def test_media_native_seek_changed_prepared_position_has_no_commit(self):
        self.prepare_corrupt = lambda result: result.update(position_ms=True)
        with self.assertRaises(HomeAssistantError):
            await self.native_call("media_seek", seek_position=0.001)
        self.assertEqual(len(self.room_posts), 1)
        self.assertEqual(self._writes(), [])

    async def test_media_native_seek_duplicate_and_unknown_never_fallback(self):
        self.room_commit_hold = True
        first = self._task(self.native_call("media_seek", seek_position=1))
        await asyncio.wait_for(self.room_commit_entered.wait(), 2)
        with self.assertRaises(HomeAssistantError):
            await self.native_call("media_seek", seek_position=1)
        self.room_status = 500
        self.room_commit_release.set()
        with self.assertRaises(HomeAssistantError):
            await first
        self.assertEqual(len(self.room_posts), 2)
        self.assertEqual(self._writes(), [])
        receipt = self.data.commands.receipts[1][1]
        self.assertEqual(receipt["outcome"], "unknown")
        self.assertFalse(receipt["retry_allowed"])

    async def test_media_native_seek_cancelled_prepare_has_no_commit(self):
        self.room_hold = True
        task = self._task(self.native_call("media_seek", seek_position=1))
        await asyncio.wait_for(self.room_entered.wait(), 2)
        task.cancel()
        self.room_release.set()
        with self.assertRaises(asyncio.CancelledError):
            await task
        self.assertEqual(len(self.room_posts), 1)
        self.assertEqual(self._writes(), [])

    async def test_media_native_other_device_seek_keeps_existing_spotify_path(self):
        other = entity_registry.async_get(self.hass).async_get_entity_id(
            "media_player", "podconnect", "account-alpha_" + self.external_id
        )
        await self.hass.services.async_call(
            "media_player",
            "media_seek",
            {"entity_id": other, "seek_position": 2.5},
            blocking=True,
        )
        self.assertEqual(len(self._writes()), 1)
        self.assertEqual(self.command_queries[-1]["position_ms"], "2500")
        self.assertEqual(self.room_posts, [])

    async def test_media_native_seek_does_not_expand_closed_v3_observation(self):
        self.playing = True
        await self.data.coordinator.async_refresh()
        result = await self.hass.services.async_call(
            "podconnect",
            "get_playback_snapshot",
            {
                "config_entry_id": self.entries[0].entry_id,
                "include_output": True,
                "include_controls": True,
            },
            blocking=True,
            return_response=True,
        )
        self.assertEqual(result["schema"], "podconnect.playback.v3")
        self.assertEqual(result["controls"]["status"], "observed")
        self.assertNotIn("seek", result["controls"]["supported_actions"])
        self.assertTrue(
            set(result["controls"]["supported_actions"])
            <= {"pause", "resume", "next", "previous", "volume"}
        )
        admitted = await self.data.commands.room_transport.read(
            self.hass, self.entries[0].entry_id, self.native_id, "room-alpha", "123"
        )
        self.assertIn("seek", admitted["actions"])
        self.assertEqual(self.room_posts, [])
        print("SEEK_V3_DTO=" + json.dumps(result, separators=(",", ":")))

    async def test_media_native_pause_uses_shared_room_owner_not_spotify_mutation(self):
        self.playing = True
        await self.data.coordinator.async_refresh()
        await self.hass.services.async_call(
            "media_player", "media_pause", {"entity_id": self.entity_id}, blocking=True
        )
        self.assertEqual(self._writes(), [])
        self.assertEqual(
            [path for path, _ in self.room_posts],
            ["/api/room-transport/prepare", "/api/room-transport/commit"],
        )
        self.assertIsNone(self.entity._optimistic_playing)

    async def native_call(self, service, **kwargs):
        return await self.hass.services.async_call(
            "media_player",
            service,
            {"entity_id": self.entity_id, **kwargs},
            blocking=True,
        )

    async def test_media_native_all_five_operations_share_command_sequence(self):
        for service, arguments, playing in (
            ("media_play", {}, False),
            ("media_pause", {}, True),
            ("media_next_track", {}, True),
            ("media_previous_track", {}, True),
            ("volume_set", {"volume_level": 0.41}, True),
        ):
            self.playing = playing
            await self.data.coordinator.async_refresh()
            await self.native_call(service, **arguments)
            self.assertIsNone(self.entity._optimistic_playing)
        self.assertEqual(self._writes(), [])
        self.assertEqual(self.data.commands.command_sequence, 5)
        self.assertEqual(len(self.room_posts), 8)
        self.assertEqual(len(self.volume_posts), 2)
        self.assertEqual(self.prepared_percent, 41)
        self.assertTrue(
            all(
                receipt[1]["outcome"] == "accepted"
                for receipt in self.data.commands.receipts.values()
            )
        )

    async def test_media_native_touch_busy_cannot_bypass_into_spotify(self):
        self.playing = True
        await self.data.coordinator.async_refresh()
        self.room_commit_hold = True
        first = self._task(self.native_call("media_pause"))
        await asyncio.wait_for(self.room_commit_entered.wait(), 2)
        self.assertTrue(self.data.commands.busy)
        request = self.room_request("pause", target_id=self.native_id)
        result = await self.room_invoke(request)
        self.assertEqual(result["outcome"], "rejected")
        self.assertEqual(result["error_kind"], "busy")
        self.assertEqual(len(self.room_posts), 2)
        self.room_commit_release.set()
        await first
        self.assertEqual(self._writes(), [])

    async def test_media_native_busy_from_touch_refuses_entity_without_fallback(self):
        self.playing = True
        await self.data.coordinator.async_refresh()
        self.room_commit_hold = True
        first = self._task(
            self.room_invoke(self.room_request("pause", target_id=self.native_id))
        )
        await asyncio.wait_for(self.room_commit_entered.wait(), 2)
        with self.assertRaises(HomeAssistantError):
            await self.native_call("media_pause")
        self.assertEqual(len(self.room_posts), 2)
        self.assertEqual(self._writes(), [])
        self.room_commit_release.set()
        self.assertEqual((await first)["outcome"], "accepted")

    async def test_media_native_poll_change_during_classification_has_zero_effects(
        self,
    ):
        self.identity_hold = True
        task = self._task(self.native_call("media_pause"))
        await asyncio.wait_for(self.identity_entered.wait(), 2)
        await self.data.coordinator.async_refresh()
        self.identity_release.set()
        with self.assertRaises(HomeAssistantError):
            await task
        self.assertEqual(
            (self._writes(), self.room_posts, self.volume_posts), ([], [], [])
        )
        self.assertEqual(self.data.commands.command_sequence, 0)

    async def test_media_native_route_change_between_binding_and_offer_is_not_adopted(
        self,
    ):
        self.playing = True
        await self.data.coordinator.async_refresh()
        self.native_state_hold_at = 2
        task = self._task(self.native_call("media_pause"))
        await asyncio.wait_for(self.native_state_entered.wait(), 2)
        self.output_revision = 8
        self.native_state_release.set()
        with self.assertRaises(HomeAssistantError):
            await task
        self.assertEqual(
            (self._writes(), self.room_posts, self.volume_posts), ([], [], [])
        )

    async def test_media_native_unknown_commit_is_not_success_or_spotify_fallback(self):
        self.playing = True
        await self.data.coordinator.async_refresh()
        self.room_status = 500
        with self.assertRaises(HomeAssistantError):
            await self.native_call("media_pause")
        self.assertEqual(len(self.room_posts), 2)
        self.assertEqual(self._writes(), [])
        self.assertEqual(self.data.commands.receipts[1][1]["outcome"], "unknown")
        self.assertIsNone(self.entity._optimistic_playing)

    async def test_media_native_unknown_engine_identity_is_closed(self):
        self.identity_status = 503
        with self.assertRaises(HomeAssistantError):
            await self.native_call("media_pause")
        self.assertEqual(
            (self._writes(), self.room_posts, self.volume_posts), ([], [], [])
        )

    async def test_media_native_proven_other_device_retains_existing_api_path(self):
        registry = entity_registry.async_get(self.hass)
        entity_id = registry.async_get_entity_id(
            "media_player", "podconnect", "account-alpha_" + self.external_id
        )
        await self.hass.services.async_call(
            "media_player", "media_pause", {"entity_id": entity_id}, blocking=True
        )
        self.assertEqual(len(self._writes()), 1)
        self.assertEqual(self.command_queries, [{"device_id": self.external_id}])
        self.assertEqual((self.room_posts, self.volume_posts), ([], []))
        self.assertEqual(self.data.commands.command_sequence, 0)

    async def test_media_native_exhausted_sequence_is_closed_before_any_write(self):
        self.data.commands.command_sequence = 2**53 - 1
        with self.assertRaises(HomeAssistantError):
            await self.native_call("media_pause")
        self.assertEqual(
            (self._writes(), self.room_posts, self.volume_posts), ([], [], [])
        )

    async def test_media_native_expired_binding_cannot_be_adopted_after_await(self):
        from custom_components.podconnect import media_player

        self.playing = True
        await self.data.coordinator.async_refresh()
        self.native_state_hold_at = 2
        task = self._task(self.native_call("media_pause"))
        await asyncio.wait_for(self.native_state_entered.wait(), 2)
        now = media_player.time.time()
        with patch.object(media_player, "time", SimpleNamespace(time=lambda: now + 10)):
            self.native_state_release.set()
            with self.assertRaises(HomeAssistantError):
                await task
        self.assertEqual(
            (self._writes(), self.room_posts, self.volume_posts), ([], [], [])
        )

    async def test_media_native_advertised_episode_uses_same_selected_owner(self):
        self.content_actions = ["play_track", "play_context", "play_episode"]
        uri = "spotify:episode:" + "0" * 21 + "4"
        await self.data.coordinator.async_refresh()
        await self.native_call(
            "play_media", media_content_type="episode", media_content_id=uri
        )
        self.assertEqual(self.prepares()[0]["action"], "play_episode")
        self.assertEqual(
            self.prepares()[0]["args"], {"episode_uri": uri, "max_volume": 100}
        )
        self.assertEqual((len(self.prepares()), len(self.content_commits())), (1, 1))
        self.assertEqual(self._writes(), [])
        self.assertEqual(self.data.commands.receipts[1][1]["outcome"], "accepted")


def load_tests(loader, tests, pattern):
    return unittest.TestSuite(
        MediaPlayerNativeOwnerTests(name)
        for name in loader.getTestCaseNames(MediaPlayerNativeOwnerTests)
        if name.startswith("test_media_native_")
    )
