"""Actual HA entity and shared Control owners against inert loopback backends."""

import asyncio
import unittest
from types import SimpleNamespace
from unittest.mock import patch

import test_room_content as fixtures
from aiohttp import web
from homeassistant.exceptions import HomeAssistantError
from homeassistant.helpers import entity_registry


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
            )
        )
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
                        "id": self.native_id,
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
                media_content_type="episode",
                media_content_id="spotify:episode:" + "A" * 22,
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


def load_tests(loader, tests, pattern):
    return unittest.TestSuite(
        MediaPlayerNativeOwnerTests(name)
        for name in loader.getTestCaseNames(MediaPlayerNativeOwnerTests)
        if name.startswith("test_media_native_")
    )
