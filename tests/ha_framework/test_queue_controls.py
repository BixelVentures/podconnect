"""Real HA services + account lifecycle + bounded HTTP; native replies are inert fixtures."""
import asyncio
import copy
import unittest

from aiohttp import web
from test_commands import CommandFrameworkTests


class QueueFrameworkTests(CommandFrameworkTests):
    async def asyncSetUp(self):
        self.queue_posts = []
        self.queue_status = 200
        self.queue_hold = False
        self.queue_entered = asyncio.Event()
        self.queue_release = asyncio.Event()
        self.queue_offer_id = "a" * 32
        self.native_user = "alpha"
        await super().asyncSetUp()
        self.native_user = self.entries[0].unique_id
        self.holds.append(self.queue_release)

    async def _backend(self, request):
        if request.path == "/spotify/me/player/queue":
            item = {"type": "track", "uri": "spotify:track:" + "A" * 22, "name": "Real cached label", "artists": [{"name": "Cached artist"}], "duration_ms": 120000, "album": {"images": [{"url": "https://i.scdn.co/image/real-cover"}]}}
            return web.json_response({"currently_playing": None, "queue": [item, item]})
        if request.path == "/api/queue-occurrences":
            if request.query.get("account_user_id") != self.native_user:
                return web.Response(status=409)
            self.queue_entered.set()
            if self.queue_hold:
                await self.queue_release.wait()
            row = {"uri": "spotify:track:" + "A" * 22, "queued": True}
            return web.json_response({"schema": "podconnect.native_queue.v1", "target_id": "spotify-one", "room_id": "room-alpha", "homepod_id": "saved-output-alpha", "account_user_id": self.native_user,
                "connection_id": "connection-a", "session_id": "session-a", "alias_incarnation": "fixture-incarnation", "observation_id": self.queue_offer_id, "queue_revision": 7, "expires_after_ms": 10000,
                "items": [{**row, "id": "1" * 24, "position": 0}, {**row, "id": "2" * 24, "position": 1}], "truncated": False})
        if request.path == "/api/queue-occurrence":
            body = await request.json()
            self.queue_posts.append(copy.deepcopy(body))
            self.queue_entered.set()
            if self.queue_hold:
                await self.queue_release.wait()
            if self.queue_status != 200:
                return web.Response(status=self.queue_status)
            return web.json_response({"ok": True, "schema": "podconnect.native_queue_commit.v1", "observation_id": body["observation_id"], "occurrence_id": body["occurrence_id"], "physical_result_verified": False})
        return await super()._backend(request)

    async def offer(self):
        return await self.hass.services.async_call("podconnect", "get_queue_controls", {"config_entry_id": "account-alpha", "target_id": "spotify-one", "room_id": "room-alpha"}, blocking=True, return_response=True)

    def queue_args(self, occurrence="2" * 24):
        return {"room_id": "room-alpha", "observation_id": self.queue_offer_id, "session_id": "session-a", "queue_revision": 7, "occurrence_id": occurrence}

    async def test_queue_exact_duplicate_occurrence_uses_native_once_and_no_spotify_write(self):
        offer = await self.offer()
        self.assertTrue(offer["ok"])
        self.assertEqual([r["option_id"] for r in offer["items"]], ["1" * 24, "2" * 24])
        self.assertEqual(offer["native"]["homepod_id"], "saved-output-alpha")
        request = self.request("play_queue_occurrence", args=self.queue_args())
        accepted = await self.invoke(request)
        replay = await self.invoke(request)
        self.assertTrue(accepted["ok"])
        self.assertEqual(replay, accepted)
        self.assertEqual(len(self.queue_posts), 1)
        self.assertEqual(self.queue_posts[0]["occurrence_id"], "2" * 24)
        self.assertNotIn("uri", self.queue_posts[0])
        self.assertEqual(self._writes(), [])
        self.assertEqual((self.native_prepares, self.native_commits), (0, 0))

    async def test_queue_real_rest_metadata_is_display_only_and_matches_adapter_schema(self):
        cached = await self.hass.services.async_call("podconnect", "get_queue", {"config_entry_id": "account-alpha", "limit": 20}, blocking=True, return_response=True)
        self.assertTrue(cached["ok"])
        offered = await self.offer()
        self.assertTrue(offered["ok"])
        self.assertEqual(offered["items"][0]["name"], "Real cached label")
        self.assertEqual(offered["items"][0]["artist"], "Cached artist")
        self.assertEqual(offered["items"][0]["artwork"], "https://i.scdn.co/image/real-cover")
        self.assertNotEqual(offered["items"][0]["option_id"], cached["items"][0]["option_id"])
        self.assertEqual([r["option_id"] for r in offered["items"]], ["1" * 24, "2" * 24])
        self.assertEqual(self.queue_posts, [])

    async def test_queue_foreign_account_and_replaced_poll_never_send(self):
        self.native_user = "different-native-account"
        self.assertFalse((await self.offer())["ok"])
        self.native_user = self.entries[0].unique_id
        self.assertTrue((await self.offer())["ok"])
        await self.entries[0].runtime_data.coordinator.async_refresh()
        result = await self.invoke(self.request("play_queue_occurrence", args=self.queue_args()))
        self.assertEqual(result["outcome"], "rejected")
        self.assertEqual(self.queue_posts, [])

    async def test_queue_missing_occurrence_consumes_offer_without_fallback(self):
        self.assertTrue((await self.offer())["ok"])
        result = await self.invoke(self.request("play_queue_occurrence", args=self.queue_args("3" * 24)))
        self.assertEqual(result["outcome"], "rejected")
        result = await self.invoke(self.request("play_queue_occurrence", args=self.queue_args()))
        self.assertEqual(result["outcome"], "rejected")
        self.assertEqual(self.queue_posts, [])
        self.assertEqual(self._writes(), [])

    async def test_queue_held_read_is_revoked_by_owner_replacement(self):
        self.queue_hold = True
        task = self._task(self.offer())
        await asyncio.wait_for(self.queue_entered.wait(), 2)
        self.entries[0].runtime_data.coordinator.owner_epoch = "replacement"
        self.queue_release.set()
        result = await task
        self.assertFalse(result["ok"])
        self.assertIsNone(self.entries[0].runtime_data.commands.queue_controls.offer)

    async def test_queue_unknown_commit_is_never_retried(self):
        self.assertTrue((await self.offer())["ok"])
        self.queue_status = 502
        request = self.request("play_queue_occurrence", args=self.queue_args())
        result = await self.invoke(request)
        self.assertEqual(result["outcome"], "unknown")
        self.assertFalse(result["retry_allowed"])
        self.assertEqual(await self.invoke(request), result)
        self.assertEqual(len(self.queue_posts), 1)
        self.assertEqual(self._writes(), [])


def load_tests(loader, tests, pattern):
    return unittest.TestSuite(QueueFrameworkTests(name) for name in loader.getTestCaseNames(QueueFrameworkTests) if name.startswith("test_queue_"))
