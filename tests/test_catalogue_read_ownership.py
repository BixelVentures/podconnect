"""Catalogue task custody against the existing shipped-handler fixture."""

import asyncio
import unittest
from types import SimpleNamespace
from unittest.mock import AsyncMock, patch

from test_playback_targets import (
    HAError,
    S,
    SpotifyError,
    catalog,
    device,
    invoke,
    services,
)


def speakers():
    return SimpleNamespace(
        aliases=AsyncMock(return_value=catalog()),
        rooms=AsyncMock(
            return_value=[
                {"id": "A", "homepod_id": "native-A"},
                {"id": "B", "homepod_id": "native-B"},
            ]
        ),
        outputs=AsyncMock(
            return_value={
                "owntone_up": True,
                "devices": [
                    {
                        "id": "observed",
                        "name": "Observed",
                        "selected": False,
                        "needs_auth": False,
                    }
                ],
            }
        ),
    )


class CatalogueReadOwnershipTests(unittest.IsolatedAsyncioTestCase):
    async def test_one_unavailable_kind_preserves_complete_other_kinds_and_no_writes(
        self,
    ):
        for failed in ("spotify_device", "configured_alias", "observed_output"):
            with self.subTest(kind=failed):
                local = speakers()
                api = SimpleNamespace(
                    devices=AsyncMock(return_value=[device()]), transfer=AsyncMock()
                )
                if failed == "spotify_device":
                    api.devices.side_effect = SpotifyError("inert unavailable")
                elif failed == "configured_alias":
                    local.aliases.side_effect = S["SpeakersError"]("inert unavailable")
                else:
                    local.outputs.side_effect = S["SpeakersError"]("inert unavailable")
                _, _, _, reg = services(api, local)
                reply = await invoke(reg, "get_targets_with_room_context")
                self.assertEqual(reply["errors"], {failed: "unavailable"})
                expected = [
                    ("spotify_device", "device-B"),
                    ("configured_alias", "A"),
                    ("configured_alias", "B"),
                    ("observed_output", "observed"),
                ]
                self.assertEqual(
                    [(r["kind"], r["target_id"]) for r in reply["targets"]],
                    [pair for pair in expected if pair[0] != failed],
                )
                api.transfer.assert_not_called()
                if failed == "configured_alias":
                    local.rooms.assert_not_called()  # Alias identity prerequisite remains required.

    async def test_missing_speakers_is_explicit_not_configured(self):
        _, _, _, reg = services()
        reply = await invoke(reg, "get_targets_with_room_context")
        self.assertEqual(
            reply["errors"],
            {
                "configured_alias": "not_configured",
                "observed_output": "not_configured",
            },
        )
        self.assertEqual([r["target_id"] for r in reply["targets"]], ["device-B"])

    async def test_parent_cancellation_joins_each_owned_read_and_propagates(self):
        entered = [asyncio.Event() for _ in range(3)]
        joined = [asyncio.Event() for _ in range(3)]
        hold = asyncio.Event()

        async def held(index):
            entered[index].set()
            try:
                await hold.wait()
            finally:
                joined[index].set()

        local = speakers()

        # AsyncMock returns a coroutine unchanged; use actual async functions here.
        async def aliases():
            return await held(1)

        async def outputs():
            return await held(2)

        async def devices():
            return await held(0)

        local.aliases = aliases
        local.outputs = outputs
        _, _, _, reg = services(SimpleNamespace(devices=devices), local)
        call = asyncio.create_task(invoke(reg, "get_targets_with_room_context"))
        try:
            await asyncio.gather(*(event.wait() for event in entered))
            call.cancel()
            with self.assertRaises(asyncio.CancelledError):
                await call
            self.assertTrue(all(event.is_set() for event in joined))
            self.assertFalse(
                any(
                    t.get_name().startswith("podconnect-catalogue-")
                    for t in asyncio.all_tasks()
                    if not t.done()
                )
            )
        finally:
            hold.set()
            call.cancel()
            await asyncio.gather(call, return_exceptions=True)

    async def test_deadline_late_completion_is_joined_but_not_credited_and_reload_denies(
        self,
    ):
        for reload in (False, True):
            with self.subTest(reload=reload):
                entered, cancelled, release = (asyncio.Event() for _ in range(3))
                local = speakers()

                async def devices(
                    entered=entered, cancelled=cancelled, release=release
                ):
                    entered.set()
                    try:
                        await asyncio.Event().wait()
                    except asyncio.CancelledError:
                        cancelled.set()
                        await release.wait()
                        return [device("late", "Late cancelled result")]

                _, data, _, reg = services(SimpleNamespace(devices=devices), local)
                original_wait = asyncio.wait

                async def boundary(
                    reads, *, timeout, entered=entered, original_wait=original_wait
                ):
                    self.assertEqual(
                        timeout, 7.5
                    )  # Declared production response boundary.
                    await entered.wait()
                    # Observe both independent local branches actually completed.
                    while not all(
                        t.done() for t in reads if "spotify" not in t.get_name()
                    ):
                        await asyncio.sleep(0)
                    return await original_wait(reads, timeout=0)

                with patch.object(asyncio, "wait", boundary):
                    call = asyncio.create_task(
                        invoke(reg, "get_targets_with_room_context")
                    )
                    try:
                        await cancelled.wait()
                        self.assertFalse(
                            call.done()
                        )  # Own cleanup is joined, not orphaned.
                        if reload:
                            data.active = False
                        release.set()
                        if reload:
                            with self.assertRaises(HAError):
                                await call
                        else:
                            reply = await call
                            self.assertEqual(
                                reply["errors"], {"spotify_device": "unavailable"}
                            )
                            self.assertEqual(
                                [r["target_id"] for r in reply["targets"]],
                                ["A", "B", "observed"],
                            )
                    finally:
                        release.set()
                        call.cancel()
                        await asyncio.gather(call, return_exceptions=True)
