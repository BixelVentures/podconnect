"""Causal catalogue deadline regression using the existing real HA fixture.

No provider, playback, production timeout change, or alternative catalogue owner.
"""

import asyncio
import time
import unittest
from unittest.mock import patch

import httpx
import test_targets as target_fixture


class CatalogueDeadlineTests(unittest.IsolatedAsyncioTestCase):
    # Reuse the existing real-HA fixture without collecting its old tests twice.
    asyncSetUp = target_fixture.TargetFrameworkTests.asyncSetUp
    _cleanup = target_fixture.TargetFrameworkTests._cleanup
    _task = target_fixture.TargetFrameworkTests._task
    _backend = target_fixture.TargetFrameworkTests._backend
    _writes = target_fixture.TargetFrameworkTests._writes

    def _assert_fresh_catalogue(self, reply, *, spotify):
        self.assertEqual(set(reply), {"config_entry_id", "targets", "errors"})
        self.assertEqual(reply["config_entry_id"], "account-alpha")
        self.assertEqual(
            reply["errors"], {} if spotify else {"spotify_device": "unavailable"}
        )
        expected = ["room-alpha", "room-beta", "output-one"]
        if spotify:
            expected.insert(0, "spotify-one")
        self.assertEqual([r["target_id"] for r in reply["targets"]], expected)
        self.assertEqual(
            [r["kind"] for r in reply["targets"]],
            (["spotify_device"] if spotify else [])
            + ["configured_alias", "configured_alias", "observed_output"],
        )
        for row in reply["targets"]:
            if row["kind"] == "configured_alias":
                self.assertEqual(
                    set(row), {"kind", "target_id", "name", "homepod_id", "binding"}
                )
                self.assertEqual(
                    row["binding"],
                    {
                        "ready": True,
                        "incarnation": "fixture-incarnation",
                        "registry": "fixture-registry",
                        "room_id": "room-alpha",
                    },
                )
                self.assertEqual(
                    row["homepod_id"],
                    "saved-output-" + row["target_id"].removeprefix("room-"),
                )
                self.assertNotIn("ha_area", row)  # No invented saved area association.
        self.assertTrue(reply["targets"][-1]["read_only"])
        self.assertTrue(self.entries[0].runtime_data.active)
        self.assertEqual(self._writes(), [])

    async def test_catalogue_slow_spotify_returns_complete_local_rows_before_actual_caller(
        self,
    ):
        """Actual HA cancels/joins the held branch before the unchanged 8s caller."""
        async with asyncio.timeout(12):
            self.calls.clear()
            self.hold_path = "/spotify/me/player/devices"
            api = self.entries[0].runtime_data.api
            original = api.devices
            branch_cancelled = asyncio.Event()
            branch_joined = asyncio.Event()

            async def observe_read():
                try:
                    return await original()
                except asyncio.CancelledError:
                    branch_cancelled.set()
                    raise
                finally:
                    branch_joined.set()

            with patch.object(api, "devices", observe_read):
                async with httpx.AsyncClient(
                    timeout=httpx.Timeout(8.0, connect=3.0),
                    headers=dict(self.client.session.headers),
                ) as caller:
                    started = time.monotonic()
                    call = self._task(
                        caller.post(
                            str(
                                self.client.make_url(
                                    "/api/services/podconnect/get_targets_with_room_context?return_response"
                                )
                            ),
                            json={"config_entry_id": "account-alpha"},
                        )
                    )
                    await self.entered.wait()
                    response = await call
                    elapsed = time.monotonic() - started
            self.assertEqual(response.status_code, 200)
            payload = response.json()
            self.assertEqual(set(payload), {"changed_states", "service_response"})
            self._assert_fresh_catalogue(payload["service_response"], spotify=False)
            self.assertTrue(branch_cancelled.is_set())
            self.assertTrue(branch_joined.is_set())
            self.assertLess(elapsed, 8.0)
            self.assertEqual(
                {c[1] for c in self.calls},
                {
                    self.hold_path,
                    "/api/room-switch",
                    "/api/rooms",
                    "/api/state",
                },
            )
            self.assertEqual(len(self.calls), 4)
            self.assertTrue(all(c[0] == "GET" for c in self.calls))
            self.assertFalse(
                any(
                    t.get_name().startswith("podconnect-catalogue-")
                    for t in asyncio.all_tasks()
                    if not t.done()
                )
            )
            # A delayed backend response cannot resurrect rows after joined cancellation.
            self.release.set()
            await self.hass.async_block_till_done()
            self._assert_fresh_catalogue(payload["service_response"], spotify=False)
            print(
                f"GREEN catalogue HTTP200 seconds={elapsed:.3f} "
                "spotify_cancelled_joined=true complete_local_rows=3 effects=0"
            )

    async def test_catalogue_healthy_actual_caller_has_complete_fresh_v3_reply(self):
        async with asyncio.timeout(15):
            self.calls.clear()
            async with httpx.AsyncClient(
                timeout=httpx.Timeout(8.0, connect=3.0),
                headers=dict(self.client.session.headers),
            ) as caller:
                response = await caller.post(
                    str(
                        self.client.make_url(
                            "/api/services/podconnect/get_targets_with_room_context?return_response"
                        )
                    ),
                    json={"config_entry_id": "account-alpha"},
                )
            self.assertEqual(response.status_code, 200)
            payload = response.json()
            self.assertEqual(set(payload), {"changed_states", "service_response"})
            self._assert_fresh_catalogue(payload["service_response"], spotify=True)
            self.assertFalse(payload["service_response"]["targets"][0]["restricted"])
            self.assertEqual(
                {c[1] for c in self.calls},
                {
                    "/spotify/me/player/devices",
                    "/api/room-switch",
                    "/api/rooms",
                    "/api/state",
                },
            )
            self.assertEqual(len(self.calls), 4)
            self.assertTrue(all(c[0] == "GET" for c in self.calls))
            print("CONTROL catalogue HTTP200 complete_fresh_rows=4 errors=0 effects=0")
