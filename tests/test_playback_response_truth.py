"""Exercise shipped playback HTTP, poll and observation bodies with inert boundaries."""

import asyncio
import json
import sys
import time
import unittest
from datetime import UTC, datetime
from types import SimpleNamespace
from unittest.mock import AsyncMock

from test_playback_targets import PKG, load_body


def load_isolated(file, namespace, names):
    name = PKG + "." + file.removesuffix(".py")
    previous = sys.modules.get(name)
    try:
        return load_body(file, namespace, names)
    finally:
        if previous is None:
            sys.modules.pop(name, None)
        else:
            sys.modules[name] = previous


API = load_isolated(
    "api.py",
    {
        "SPOTIFY_API": "https://example.invalid",
        "CONF_ACCESS_TOKEN": "access_token",
        "json_module": json,
    },
    {"SpotifyApiError", "SpotifyApi"},
)
OBS = load_isolated(
    "observations.py",
    {
        "time": time,
        "datetime": datetime,
        "PLAYBACK_MAX_AGE_S": 15.0,
        "QUEUE_CACHE_S": 10.0,
        "PUBLIC_MAX_BYTES": 65536,
        "spotify_album_artwork": lambda _: None,
    },
    {"_error", "_text", "_number", "_item", "_bounded", "ObservationReader"},
)


class CoordinatorBase:
    @classmethod
    def __class_getitem__(cls, _):
        return cls


COORD = load_isolated(
    "coordinator.py",
    {
        "asyncio": asyncio,
        "time": time,
        "SpotifyApiError": API["SpotifyApiError"],
        "UpdateFailed": RuntimeError,
        "dt_util": SimpleNamespace(utcnow=lambda: datetime.now(UTC)),
        "DataUpdateCoordinator": CoordinatorBase,
        "Any": object,
    },
    {"PodConnectCoordinator"},
)


class Response:
    def __init__(self, status, content_type="application/json", body=None):
        self.status, self.content_type, self.body, self.headers = (
            status,
            content_type,
            body,
            {},
        )

    async def __aenter__(self):
        return self

    async def __aexit__(self, *args):
        pass

    async def json(self):
        return self.body

    async def text(self):
        return "error"


class PlaybackResponseTruth(unittest.IsolatedAsyncioTestCase):
    def rig(self, response):
        api = API["SpotifyApi"].__new__(API["SpotifyApi"])
        api._session = SimpleNamespace(
            token={"access_token": "inert"}, async_ensure_token_valid=AsyncMock()
        )
        api._web = SimpleNamespace(request=lambda *a, **kw: response)
        coordinator = SimpleNamespace(
            poll_sequence=4,
            command_owner_guard=None,
            owner_epoch="epoch",
            api=SimpleNamespace(
                playback_state=api.playback_state, devices=AsyncMock(return_value=[])
            ),
        )
        coordinator._check_command_owner = lambda: COORD[
            "PodConnectCoordinator"
        ]._check_command_owner(coordinator)
        return coordinator

    async def poll(self, response):
        coordinator = self.rig(response)
        data = await COORD["PodConnectCoordinator"]._async_update_data(coordinator)
        coordinator.data, coordinator.last_update_success = data, True
        return coordinator, OBS["ObservationReader"](coordinator, None).playback(
            "account"
        )

    async def test_real_204_becomes_fresh_empty(self):
        coordinator, result = await self.poll(Response(204))
        self.assertEqual(coordinator.poll_sequence, 5)
        self.assertTrue(result["ok"])
        self.assertEqual(result["playback"]["state"], "no_active_playback")

    async def test_bad_non204_never_becomes_fresh_empty(self):
        for response in (
            Response(200, "text/html"),
            Response(200, body=None),
            Response(201, body={}),
        ):
            with self.subTest(
                status=response.status, content_type=response.content_type
            ):
                coordinator = self.rig(response)
                with self.assertRaises(RuntimeError):
                    await COORD["PodConnectCoordinator"]._async_update_data(coordinator)
                self.assertEqual(coordinator.poll_sequence, 5)
                coordinator.last_update_success = False
                coordinator.data = {"poll_sequence": 4, "playback": None}
                self.assertEqual(
                    OBS["ObservationReader"](coordinator, None).playback("account"),
                    {"ok": False, "error_kind": "unavailable"},
                )

    async def test_valid_paused_is_retained(self):
        playback = {
            "is_playing": False,
            "device": {"id": "device", "name": "room"},
            "item": None,
            "progress_ms": 1000,
            "timestamp": None,
            "shuffle_state": False,
            "repeat_state": "off",
        }
        coordinator, result = await self.poll(Response(200, body=playback))
        self.assertEqual(coordinator.poll_sequence, 5)
        self.assertEqual(result["playback"]["state"], "paused")
        self.assertEqual(result["playback"]["progress_ms"], 1000)

    async def test_existing_error_and_cancellation_propagate(self):
        for status in (429, 500):
            with self.subTest(status=status), self.assertRaises(RuntimeError):
                await COORD["PodConnectCoordinator"]._async_update_data(
                    self.rig(Response(status))
                )
        response = Response(200, body={})
        response.json = AsyncMock(side_effect=ValueError("bad JSON"))
        with self.assertRaises(RuntimeError):
            await COORD["PodConnectCoordinator"]._async_update_data(self.rig(response))
        response = Response(200, body={})
        response.json = AsyncMock(side_effect=asyncio.CancelledError())
        with self.assertRaises(asyncio.CancelledError):
            await COORD["PodConnectCoordinator"]._async_update_data(self.rig(response))

    async def test_existing_non_playback_contracts_remain_unchanged(self):
        coordinator = self.rig(Response(200, "text/html"))
        api = coordinator.api.playback_state.__self__
        self.assertIsNone(await api._request("GET", "/other"))
        with self.assertRaises(API["SpotifyApiError"]):
            await api._request("POST", "/command", execution_guard=lambda: True)
        with self.assertRaises(API["SpotifyApiError"]):
            await api._request("GET", "/queue", max_bytes=64)
        api._web = SimpleNamespace(request=lambda *a, **kw: Response(204))
        self.assertIsNone(
            await api._request("POST", "/command", execution_guard=lambda: True)
        )
