"""Native revision hints advance the actual HA playback owner, with inert Spotify I/O."""

import asyncio
import os
import unittest
from unittest.mock import AsyncMock, patch

import test_targets as fixture
from aiohttp import ClientSession, web
from homeassistant.core import callback


class PlaybackRevisionFrameworkTests(unittest.IsolatedAsyncioTestCase):
    _cleanup = fixture.TargetFrameworkTests._cleanup
    _task = fixture.TargetFrameworkTests._task
    _service = fixture.TargetFrameworkTests._service
    enable_polling = True
    revision_header = "X-Podconnect-Playback-Revision"

    async def asyncSetUp(self):
        self.playing = None
        self.title = "Old title"
        self.target = "a123456789012345678901234567890123456789"
        self.provider_reads = 0
        self.provider_changed_at = 1_790_000_000_000
        self.position = 0
        self.engine_reads = 0
        self.initial_engine_mismatch = (
            self._testMethodName
            == "test_held_poll_coalesces_hints_and_falls_back_on_failure"
        )
        self.writes = []
        self.native_replies = asyncio.Queue()
        self.native_requests = asyncio.Queue()
        self.read_entered = asyncio.Event()
        self.release_read = asyncio.Event()
        self.hold_read = False
        self.generation = "a" * 32
        binary = os.environ.get("PODCONNECT_PLAYBACK_REVISION_BINARY")
        if binary:
            self.account_overrides = {"account-alpha": "native-user"}
            await self.start_manager(binary)
        await fixture.TargetFrameworkTests.asyncSetUp(self)
        self.holds.append(self.release_read)
        await self.hass.config_entries.async_unload(self.entries[1].entry_id)
        self.entry = self.entries[0]
        self.coordinator = self.entry.runtime_data.coordinator
        if self.initial_engine_mismatch:
            self.coordinator._NATIVE_ENGINE_RECHECK_SECONDS = 0.05
        self.playing = True
        await self.coordinator.async_refresh()
        await self.coordinator.async_refresh()

    async def start_manager(self, binary):
        process = await asyncio.create_subprocess_exec(
            binary,
            "-test.run",
            "^TestPlaybackRevisionHTTPBridge$",
            env={**os.environ, "PODCONNECT_PLAYBACK_REVISION_FIXTURE": "1"},
            stdout=asyncio.subprocess.PIPE,
            stderr=asyncio.subprocess.STDOUT,
        )
        session = ClientSession()

        async def close():
            try:
                async with asyncio.timeout(4):
                    if hasattr(self, "speakers_base"):
                        async with session.post(
                            self.speakers_base + "/fixture/close"
                        ) as response:
                            self.assertEqual(response.status, 204)
                    self.assertEqual(
                        await process.wait(), 0, (await process.stdout.read()).decode()
                    )
            finally:
                if process.returncode is None:
                    process.kill()
                    await process.wait()
                await session.close()

        self.addAsyncCleanup(close)
        async with asyncio.timeout(3):
            self.speakers_base = (await process.stdout.readline()).decode().strip()
        self.assertTrue(self.speakers_base.startswith("http://127.0.0.1:"))
        self.manager = session

    async def _backend(self, request):
        if request.method != "GET":
            self.writes.append((request.method, request.path))
        if request.path == "/api/start-volume/identity":
            self.engine_reads += 1
            return web.json_response(
                {
                    "schema": "podconnect.start_identity.v1",
                    "engine_device_id": "b" * 40
                    if self.initial_engine_mismatch and self.engine_reads == 1
                    else self.target,
                }
            )
        if request.path == "/api/state" and self.revision_header in request.headers:
            self.assertEqual(request.method, "GET")
            self.assertEqual(
                dict(request.query),
                {"account_user_id": "account-alpha", "target_id": self.target},
            )
            self.assertNotIn("Authorization", request.headers)
            cursor = request.headers[self.revision_header]
            if cursor == "seed":
                return self.reply(0, changed=False)
            await self.native_requests.put(cursor)
            try:
                async with asyncio.timeout(2):
                    reply = await self.native_replies.get()
            except TimeoutError:
                return web.Response(status=204)
            if reply is None:
                return web.Response(status=409)
            revision, changed = reply
            return self.reply(revision, changed=changed)
        if request.path == "/spotify/me/player":
            self.assertEqual(
                request.headers.get("Authorization"), "Bearer inert-fixture-only"
            )
            self.provider_reads += 1
            if self.playing is None:
                return web.Response(status=204)
            title = self.title
            playing = self.playing
            changed_at = self.provider_changed_at
            position = self.position
            if self.hold_read:
                self.read_entered.set()
                await self.release_read.wait()
            return web.json_response(
                {
                    "is_playing": playing,
                    "device": {"id": self.target},
                    "item": {"name": title, "uri": f"spotify:track:{title}"},
                    "progress_ms": position,
                    "timestamp": changed_at,
                    "repeat_state": "off",
                    "shuffle_state": False,
                }
            )
        return await fixture.TargetFrameworkTests._backend(self, request)

    def reply(self, revision, *, changed):
        return web.json_response(
            {
                "schema": "podconnect.playback_revision.v1",
                "cursor": f"{self.generation}:{revision}",
                "changed": changed,
            }
        )

    async def next_request(self):
        async with asyncio.timeout(1.25):
            return await self.native_requests.get()

    async def wait_for_manager(self):
        async with asyncio.timeout(1.25):
            while True:
                async with self.manager.get(
                    self.speakers_base + "/fixture/waiters"
                ) as response:
                    if (await response.json())["waiters"] == 1:
                        return
                await asyncio.sleep(0.01)

    async def test_native_change_advances_normal_poll(self):
        manager = getattr(self, "manager", None)
        if manager is None:
            self.assertEqual(await self.next_request(), f"{self.generation}:0")
        changed = asyncio.Event()

        @callback
        def observe(event):
            if event.data.get("config_entry_id") == self.entry.entry_id:
                changed.set()

        remove = self.hass.bus.async_listen("podconnect_playback_changed", observe)
        self.addCleanup(remove)
        for revision, (effect, title, playing, position) in enumerate(
            (
                ("track", "New title", True, 0),
                ("pause", "New title", False, 3000),
                ("resume", "New title", True, 3000),
                ("seek", "New title", True, 63000),
            ),
            start=1,
        ):
            if manager is not None:
                await self.wait_for_manager()
            reads = self.provider_reads
            self.title, self.playing, self.position = title, playing, position
            self.provider_changed_at += 1000
            changed.clear()
            async with asyncio.timeout(1.4):
                if manager is not None:
                    async with manager.post(
                        self.speakers_base + f"/fixture/{effect}"
                    ) as response:
                        self.assertEqual(response.status, 204)
                else:
                    await self.native_replies.put((revision, True))
                await changed.wait()
            playback = self.coordinator.data["playback"]
            self.assertEqual(
                (
                    playback["item"]["name"],
                    playback["is_playing"],
                    playback["progress_ms"],
                ),
                (title, playing, position),
            )
            self.assertEqual(self.provider_reads, reads + 1)
            if manager is None:
                self.assertEqual(
                    await self.next_request(), f"{self.generation}:{revision}"
                )
        self.position += 25
        changed.clear()
        await self.coordinator.async_refresh()
        self.assertFalse(changed.is_set(), "routine progress is not a new seek")
        self.assertEqual(self.writes, [])

    @unittest.skipIf(
        os.environ.get("PODCONNECT_PLAYBACK_REVISION_BINARY"),
        "uses fault-injected replies",
    )
    async def test_held_poll_coalesces_hints_and_falls_back_on_failure(self):
        self.assertEqual(await self.next_request(), f"{self.generation}:0")
        self.assertEqual(self.engine_reads, 2)
        reads = self.provider_reads
        self.hold_read = True
        held = self._task(self.coordinator.async_refresh())
        async with asyncio.timeout(1.25):
            await self.read_entered.wait()
        self.title = "Newest title"

        # Several accepted hints arrive while the actual Spotify reply is held.
        for revision in (1, 2, 3):
            await self.native_replies.put((revision, True))
            self.assertEqual(await self.next_request(), f"{self.generation}:{revision}")
        await self.native_replies.put((3, True))
        self.assertEqual(await self.next_request(), f"{self.generation}:3")
        self.generation = "b" * 32
        await self.native_replies.put((4, True))
        self.assertEqual(await self.next_request(), "a" * 32 + ":3")
        self.assertEqual(self.provider_reads, reads + 1)

        self.hold_read = False
        self.release_read.set()
        async with asyncio.timeout(1.25):
            await held
            while self.coordinator.data["playback"]["item"]["name"] != self.title:
                await asyncio.sleep(0.01)
        self.assertEqual(self.provider_reads, reads + 2)
        await asyncio.sleep(0.3)
        self.assertEqual(self.provider_reads, reads + 2)

        # Lose the optional native path; the ordinary active poll must still run.
        self.title = "Polling fallback"
        await self.native_replies.put(None)
        async with asyncio.timeout(4):
            while self.coordinator.data["playback"]["item"]["name"] != self.title:
                await asyncio.sleep(0.02)
        self.assertEqual(self.provider_reads, reads + 3)

        # Cancellation of a running demand clears HA's previous timer. A rejected
        # platform unload must restore the polling fallback for the loaded owner.
        self.assertEqual(await self.next_request(), "a" * 32 + ":3")
        await self.native_replies.put((0, False))
        self.assertEqual(await self.next_request(), "b" * 32 + ":0")
        self.hold_read = True
        self.read_entered.clear()
        self.release_read.clear()
        await self.native_replies.put((1, True))
        async with asyncio.timeout(1.25):
            await self.read_entered.wait()
        self.assertIsNone(self.coordinator._unsub_refresh)
        with patch.object(
            self.hass.config_entries,
            "async_unload_platforms",
            AsyncMock(return_value=False),
        ):
            self.assertFalse(
                await fixture.podconnect.async_unload_entry(self.hass, self.entry)
            )
        self.hold_read = False
        self.release_read.set()
        self.assertIsNotNone(self.coordinator._unsub_refresh)
        self.assertTrue(self.entry.runtime_data.active)
        self.assertEqual(self.writes, [])
