"""Room entities: state is Speakers' room status; each control is one direct call."""

import unittest

import test_targets as fixture
from aiohttp import web
from homeassistant.exceptions import HomeAssistantError
from homeassistant.helpers import device_registry, entity_registry


class RoomPlayerFrameworkTests(unittest.IsolatedAsyncioTestCase):
    engine_identity = "spotify-one"
    _cleanup = fixture.TargetFrameworkTests._cleanup
    _task = fixture.TargetFrameworkTests._task
    _writes = fixture.TargetFrameworkTests._writes

    async def asyncSetUp(self):
        self.room_rows = {
            "room-alpha": {
                "name": "Alpha",
                "state": "playing",
                "source": "spotify",
                "playing": True,
                "now_playing": "Artist — Song",
                "volume": 30,
                "track": {
                    "uri": "spotify:track:" + "A" * 22,
                    "title": "Song",
                    "artists": ["Artist", "Guest"],
                    "album": "Album",
                    "artwork_url": "https://i.scdn.co/image/x",
                    "position_ms": 61000,
                    "duration_ms": 200000,
                },
            },
            "room-beta": {
                "name": "Beta",
                "state": "idle",
                "source": "",
                "playing": False,
                "now_playing": "",
                "volume": 12,
            },
        }
        self.control_status = 200
        await fixture.TargetFrameworkTests.asyncSetUp(self)
        self.calls.clear()

    async def _backend(self, request):
        if request.path == "/api/rooms":
            self.calls.append((request.method, request.path, None, False))
            rows = [
                {"id": room_id, "homepod_id": "saved-output-" + room_id[5:], **row}
                for room_id, row in self.room_rows.items()
            ]
            return web.json_response({"rooms": rows})
        if request.path == "/api/room-control":
            body = await request.json()
            self.calls.append((request.method, request.path, body, False))
            if self.control_status != 200:
                return web.json_response(
                    {"ok": False, "error": "room_not_playing"}, status=self.control_status
                )
            return web.json_response({"ok": True, "source": "spotify"})
        if request.method == "PUT" and request.path == "/spotify/me/player/play":
            body = await request.json()
            self.calls.append(
                (request.method, request.path, {"query": dict(request.query), **body}, True)
            )
            return web.Response(status=204)
        return await fixture.TargetFrameworkTests._backend(self, request)

    def entity_id(self, room_id, entry="account-alpha"):
        return entity_registry.async_get(self.hass).async_get_entity_id(
            "media_player", "podconnect", f"{entry}_room_{room_id}"
        )

    async def call(self, service, room_id, **data):
        await self.hass.services.async_call(
            "media_player",
            service,
            {"entity_id": self.entity_id(room_id), **data},
            blocking=True,
        )

    def controls(self):
        return [body for method, path, body, _ in self.calls if path == "/api/room-control"]

    async def test_every_configured_room_is_one_entity(self):
        for room_id in ("room-alpha", "room-beta"):
            self.assertIsNotNone(self.entity_id(room_id))
        registry = entity_registry.async_get(self.hass)
        engine = registry.async_get(
            registry.async_get_entity_id("media_player", "podconnect", "account-alpha_spotify-one")
        )
        self.assertIs(engine.hidden_by, entity_registry.RegistryEntryHider.INTEGRATION)

    async def test_reload_removes_old_devices_without_deprecated_registry_calls(self):
        # Testnoter 13. nov.: HA 2026.11 warned that podconnect removes devices
        # through async_update_device(remove_config_entry_id=...).
        entry = self.hass.config_entries.async_get_entry("account-alpha")
        devices = device_registry.async_get(self.hass)
        old = devices.async_get_or_create(
            config_entry_id=entry.entry_id, identifiers={("podconnect", "c" * 40)}
        )
        legacy = []
        real_update = devices.async_update_device

        def update_device(device_id, **changes):
            if {"add_config_entry_id", "remove_config_entry_id"} & changes.keys():
                legacy.append(sorted(changes))
            return real_update(device_id, **changes)

        devices.async_update_device = update_device
        try:
            self.assertTrue(await self.hass.config_entries.async_reload(entry.entry_id))
            await self.hass.async_block_till_done()
        finally:
            devices.async_update_device = real_update
        self.assertEqual(legacy, [])
        self.assertIsNone(devices.async_get(old.id))

    async def test_room_state_is_what_its_homepod_outputs(self):
        alpha = self.hass.states.get(self.entity_id("room-alpha"))
        self.assertEqual(alpha.state, "playing")
        self.assertEqual(alpha.attributes["media_title"], "Song")
        self.assertEqual(alpha.attributes["media_artist"], "Artist, Guest")
        self.assertEqual(alpha.attributes["media_album_name"], "Album")
        self.assertEqual(alpha.attributes["media_content_id"], "spotify:track:" + "A" * 22)
        self.assertEqual(alpha.attributes["entity_picture"][:1], "/")  # HA proxies the artwork
        self.assertEqual(alpha.attributes["media_duration"], 200.0)
        self.assertEqual(alpha.attributes["media_position"], 61.0)
        self.assertIn("media_position_updated_at", alpha.attributes)
        self.assertEqual(alpha.attributes["volume_level"], 0.3)
        self.assertEqual(alpha.attributes["podconnect_source"], "spotify")
        self.assertEqual(self.hass.states.get(self.entity_id("room-beta")).state, "idle")

    async def test_silent_homepod_is_buffering_and_radio_is_named(self):
        self.room_rows["room-alpha"].update(state="connecting", playing=False)
        self.room_rows["room-beta"].update(state="playing", source="radio", playing=True)
        await self.call("media_pause", "room-beta")
        alpha = self.hass.states.get(self.entity_id("room-alpha"))
        beta = self.hass.states.get(self.entity_id("room-beta"))
        self.assertEqual(alpha.state, "buffering")
        self.assertEqual(beta.state, "playing")
        self.assertEqual(beta.attributes["media_title"], "Radio")
        self.assertEqual(beta.attributes["podconnect_source"], "radio")

    async def test_controls_are_one_direct_room_call_each(self):
        await self.call("media_pause", "room-alpha")
        await self.call("media_play", "room-alpha")
        await self.call("media_next_track", "room-alpha")
        await self.call("media_previous_track", "room-alpha")
        await self.call("media_seek", "room-alpha", seek_position=2.5)
        await self.call("volume_set", "room-alpha", volume_level=0.25)
        self.assertEqual(
            self.controls(),
            [
                {"room_id": "room-alpha", "action": "pause"},
                {"room_id": "room-alpha", "action": "resume"},
                {"room_id": "room-alpha", "action": "next"},
                {"room_id": "room-alpha", "action": "previous"},
                {"room_id": "room-alpha", "action": "seek", "position_ms": 2500},
                {"room_id": "room-alpha", "action": "volume", "volume": 25},
            ],
        )

    async def test_a_rejected_control_is_an_error_not_a_retry(self):
        self.control_status = 409
        with self.assertRaises(HomeAssistantError):
            await self.call("media_pause", "room-beta")
        self.assertEqual(self.controls(), [{"room_id": "room-beta", "action": "pause"}])

    async def test_play_media_selects_the_room_and_plays_on_the_speaker(self):
        await self.call(
            "play_media",
            "room-beta",
            media_content_type="music",
            media_content_id="spotify:track:" + "A" * 22,
        )
        writes = [(method, path, body) for method, path, body, _ in self._writes()]
        self.assertEqual(
            writes,
            [
                (
                    "POST",
                    "/api/room-switch",
                    {
                        "room_id": "room-beta",
                        "expected_incarnation": "fixture-incarnation",
                        "expected_registry": "fixture-registry",
                    },
                ),
                (
                    "PUT",
                    "/spotify/me/player/play",
                    {
                        "query": {"device_id": "spotify-one"},
                        "uris": ["spotify:track:" + "A" * 22],
                    },
                ),
            ],
        )

    async def test_play_media_in_the_selected_room_does_not_move(self):
        await self.call(
            "play_media",
            "room-alpha",
            media_content_type="playlist",
            media_content_id="spotify:playlist:" + "B" * 22,
        )
        writes = [(method, path) for method, path, _, _ in self._writes()]
        self.assertEqual(writes, [("PUT", "/spotify/me/player/play")])


if __name__ == "__main__":
    unittest.main()
