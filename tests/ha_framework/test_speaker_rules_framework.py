"""The family's speaker rules hold on every PodConnect write path, whoever calls.

Real HA 2026.8.2 service registry, entity platform and auth; Speakers and Spotify
are the inert loopback fixture. Calls are made as an ordinary (non-admin) user,
like the PodView HA panel and dashboards make them.
"""

import hashlib
import json
import unittest
from unittest.mock import AsyncMock

import test_room_player as rooms
from homeassistant.core import Context
from homeassistant.exceptions import ServiceValidationError, Unauthorized

ENTRY = "account-alpha"
TRACK = "spotify:track:" + "A" * 22
PLAYLIST = "spotify:playlist:" + "B" * 22

POLICY = json.dumps(
    {
        "roomId": "screen",
        "vacuum": [],
        "music": [
            {
                "accountId": ENTRY,
                "targetId": "spotify-one",
                "nativeRoomId": "room-alpha",
                "nativeHomepodId": "saved-output-alpha",
                "actions": ["pause", "resume", "volume", "play_track", "play_queue_occurrence"],
                "maxVolume": 40,
                "default": True,
            },
            {
                "accountId": ENTRY,
                "targetId": "spotify-one",
                "nativeRoomId": "room-beta",
                "nativeHomepodId": "saved-output-beta",
                "actions": ["play_context"],
                "maxVolume": 25,
            },
        ],
    }
)





class SpeakerRulesFrameworkTests(unittest.IsolatedAsyncioTestCase):
    # The room-player fixture, without re-running its own (rule-free) tests.
    engine_identity = rooms.RoomPlayerFrameworkTests.engine_identity
    _cleanup = rooms.RoomPlayerFrameworkTests._cleanup
    _task = rooms.RoomPlayerFrameworkTests._task
    _writes = rooms.RoomPlayerFrameworkTests._writes
    _backend = rooms.RoomPlayerFrameworkTests._backend
    entity_id = rooms.RoomPlayerFrameworkTests.entity_id
    controls = rooms.RoomPlayerFrameworkTests.controls

    async def asyncSetUp(self):
        await rooms.RoomPlayerFrameworkTests.asyncSetUp(self)
        auth = self.hass.auth
        self.admin = await auth.async_create_user("Parent", group_ids=["system-admin"])
        self.member = await auth.async_create_user("Child panel", group_ids=["system-users"])
        self.assertFalse(self.member.is_admin)
        self.entry = self.hass.config_entries.async_get_entry(ENTRY)
        self.runtime = self.entry.runtime_data
        reply = await self.hass.services.async_call(
            "podconnect",
            "set_speaker_rules",
            {"config_entry_id": ENTRY, "podview_policy": POLICY},
            blocking=True,
            return_response=True,
            context=Context(user_id=self.admin.id),
        )
        self.assertEqual(reply["ignored_rows"], 0)
        self.calls.clear()

    # --- helpers -----------------------------------------------------------------

    def user(self):
        return Context(user_id=self.member.id)

    async def call(self, service, room_id, **data):
        await self.hass.services.async_call(
            "media_player",
            service,
            {"entity_id": self.entity_id(room_id), **data},
            blocking=True,
            context=self.user(),
        )

    async def refused(self, key, awaitable):
        with self.assertRaises(ServiceValidationError) as caught:
            await awaitable
        self.assertEqual(caught.exception.translation_domain, "podconnect")
        self.assertEqual(caught.exception.translation_key, key)
        return caught.exception

    async def service(self, name, data):
        return await self.hass.services.async_call(
            "podconnect",
            name,
            {"config_entry_id": ENTRY, **data},
            blocking=True,
            return_response=True,
            context=self.user(),
        )

    def command(self, action, args, **room):
        return {
            "kind": "spotify_device",
            "target_id": "spotify-one",
            "owner_epoch": "e" * 32,
            "expected_poll_sequence": 1,
            "command_sequence": 1,
            "request_id": "e" * 32 + ":1",
            "action": action,
            "args": args,
            **room,
        }

    def room_context(self, room="room-alpha"):
        return {"room_id": room, "homepod_id": "saved-output-" + room[5:], "native": {}, "route": {}}

    def capture(self, owner, method):
        mock = AsyncMock(return_value={"ok": True})
        setattr(owner, method, mock)
        return mock

    async def another_account_plays(self, room="room-alpha"):
        self.room_rows[room]["account_sha256"] = hashlib.sha256(b"someone-else").hexdigest()
        await self.runtime.rooms.async_refresh()

    # --- configuration --------------------------------------------------------------

    async def test_rules_come_from_podvoice_policy_and_apply_without_reload(self):
        reply = await self.hass.services.async_call(
            "podconnect",
            "get_speaker_rules",
            {"config_entry_id": ENTRY},
            blocking=True,
            return_response=True,
            context=self.user(),
        )
        self.assertEqual(
            reply["rules"]["rooms"],
            {
                "room-alpha": {
                    "actions": [
                        "pause",
                        "play_queue_occurrence",
                        "play_track",
                        "resume",
                        "seek",
                        "volume",
                    ],
                    "max_volume": 40,
                },
                "room-beta": {"actions": ["play_context"], "max_volume": 25},
            },
        )
        # Setting rules never replaced the running account (no reload, no new owner).
        self.assertIs(self.entry.runtime_data, self.runtime)

    async def test_only_an_admin_may_change_the_rules(self):
        with self.assertRaises(Unauthorized):
            await self.hass.services.async_call(
                "podconnect",
                "set_speaker_rules",
                {"config_entry_id": ENTRY, "clear": True},
                blocking=True,
                context=self.user(),
            )
        self.assertIn("speaker_rules", self.entry.options)
        for bad in ({"rules": {"rooms": {"r": {"actions": ["fly"], "max_volume": 1}}}}, {}):
            await self.refused(
                "invalid_speaker_rules",
                self.hass.services.async_call(
                    "podconnect",
                    "set_speaker_rules",
                    {"config_entry_id": ENTRY, **bad},
                    blocking=True,
                    context=Context(user_id=self.admin.id),
                ),
            )

    async def test_clearing_the_rules_restores_unrestricted_control(self):
        await self.hass.services.async_call(
            "podconnect",
            "set_speaker_rules",
            {"config_entry_id": ENTRY, "clear": True},
            blocking=True,
            context=Context(user_id=self.admin.id),
        )
        self.assertNotIn("speaker_rules", self.entry.options)
        self.assertEqual(self.entry.options["speakers_url"], self.base)
        await self.call("volume_set", "room-beta", volume_level=0.9)
        self.assertEqual(self.controls(), [{"room_id": "room-beta", "action": "volume", "volume": 90}])

    # --- room media_player ----------------------------------------------------------

    async def test_room_volume_is_lowered_to_the_rooms_ceiling(self):
        await self.call("volume_set", "room-alpha", volume_level=0.9)
        await self.call("volume_set", "room-alpha", volume_level=0.2)
        await self.call("volume_up", "room-alpha")  # 30 % + HA's step, still within 40 %
        self.room_rows["room-alpha"]["volume"] = 40
        await self.runtime.rooms.async_refresh()
        await self.call("volume_up", "room-alpha")
        self.assertEqual(
            [body["volume"] for body in self.controls()],
            [40, 20, 40, 40],
        )

    async def test_room_without_the_permission_is_refused_before_any_send(self):
        error = await self.refused(
            "room_action_not_allowed", self.call("volume_set", "room-beta", volume_level=0.1)
        )
        self.assertEqual(error.translation_placeholders, {"room": "Beta", "action": "volume"})
        await self.refused("room_action_not_allowed", self.call("volume_up", "room-beta"))
        await self.refused("room_action_not_allowed", self.call("media_pause", "room-beta"))
        await self.refused("room_action_not_allowed", self.call("media_play", "room-beta"))
        await self.refused("room_action_not_allowed", self.call("media_next_track", "room-alpha"))
        await self.refused(
            "room_action_not_allowed", self.call("media_previous_track", "room-alpha")
        )
        await self.refused(
            "room_action_not_allowed", self.call("media_seek", "room-beta", seek_position=3)
        )
        self.assertEqual(self.controls(), [])

    async def test_granted_transport_goes_through(self):
        await self.call("media_pause", "room-alpha")
        await self.call("media_play", "room-alpha")
        await self.call("media_seek", "room-alpha", seek_position=2.5)
        self.assertEqual(
            [body["action"] for body in self.controls()], ["pause", "resume", "seek"]
        )

    async def test_play_media_needs_the_rooms_start_permission(self):
        await self.refused(
            "room_action_not_allowed",
            self.call("play_media", "room-alpha", media_content_type="playlist", media_content_id=PLAYLIST),
        )
        await self.refused(
            "room_action_not_allowed",
            self.call("play_media", "room-beta", media_content_type="music", media_content_id=TRACK),
        )
        self.assertEqual(self._writes(), [])
        await self.call(
            "play_media", "room-beta", media_content_type="playlist", media_content_id=PLAYLIST
        )
        self.assertEqual(
            [path for _, path, _, _ in self._writes()],
            ["/api/room-switch", "/spotify/me/player/play"],
        )

    async def test_another_account_is_never_steered_but_own_music_may_start(self):
        await self.another_account_plays()
        for service, data in (
            ("media_pause", {}),
            ("media_play", {}),
            ("media_seek", {"seek_position": 1}),
            ("volume_set", {"volume_level": 0.1}),
        ):
            with self.subTest(service):
                await self.refused("other_account_playing", self.call(service, "room-alpha", **data))
        self.assertEqual(self.controls(), [])
        await self.call("play_media", "room-alpha", media_content_type="music", media_content_id=TRACK)
        self.assertEqual([path for _, path, _, _ in self._writes()], ["/spotify/me/player/play"])

    # --- PodConnect services ----------------------------------------------------------

    async def test_control_room_volume_is_capped_or_refused(self):
        execute = self.capture(self.runtime.commands, "execute")
        await self.service(
            "control_room_volume",
            {**self.command("volume", {"percent": 80}), **self.room_context(), "volume_state": {}},
        )
        self.assertEqual(execute.await_args.args[1]["args"], {"percent": 40})
        await self.refused(
            "room_action_not_allowed",
            self.service(
                "control_room_volume",
                {
                    **self.command("volume", {"percent": 10}),
                    **self.room_context("room-beta"),
                    "volume_state": {},
                },
            ),
        )
        await self.another_account_plays()
        await self.refused(
            "other_account_playing",
            self.service(
                "control_room_volume",
                {**self.command("volume", {"percent": 10}), **self.room_context(), "volume_state": {}},
            ),
        )
        self.assertEqual(execute.await_count, 1)

    async def test_control_room_playback_follows_the_room_rule(self):
        execute = self.capture(self.runtime.commands, "execute")
        await self.service("control_room_playback", {**self.command("pause", {}), **self.room_context()})
        await self.refused(
            "room_action_not_allowed",
            self.service("control_room_playback", {**self.command("next", {}), **self.room_context()}),
        )
        await self.refused(
            "room_action_not_allowed",
            self.service(
                "control_room_playback",
                {**self.command("pause", {}), **self.room_context("room-beta")},
            ),
        )
        # Shuffle and repeat are not part of the rules today.
        await self.service(
            "control_room_playback",
            {**self.command("shuffle", {"enabled": True}), **self.room_context("room-beta")},
        )
        self.assertEqual(execute.await_count, 2)

    async def test_control_playback_uses_the_speakers_current_room(self):
        # The fixture's speaker currently plays in room-alpha.
        execute = self.capture(self.runtime.commands, "execute")
        await self.service("control_playback", self.command("volume", {"percent": 99}))
        self.assertEqual(execute.await_args.args[1]["args"], {"percent": 40})
        await self.refused(
            "room_action_not_allowed", self.service("control_playback", self.command("next", {}))
        )
        start = {
            "max_volume": 100,
            "room_id": "room-beta",
            "homepod_id": "saved-output-beta",
            "start_policy_revision": "f" * 64,
            "alias_incarnation": "fixture-incarnation",
        }
        await self.refused(
            "room_action_not_allowed",
            self.service("control_playback", self.command("play_track", {"track_uri": TRACK, **start})),
        )
        await self.service(
            "control_playback", self.command("play_context", {"context_uri": PLAYLIST, **start})
        )
        # A start never asks for more than the room's ceiling (PodVoice sends maxVolume).
        self.assertEqual(execute.await_args.args[1]["args"]["max_volume"], 25)
        self.assertEqual(execute.await_count, 2)

    async def test_room_queue_content_and_move_services(self):
        execute = self.capture(self.runtime.commands, "execute")
        content = self.runtime.commands.room_content
        move = self.runtime.commands.room_move
        prepare, start = self.capture(content, "prepare"), self.capture(content, "start")
        move_prepare, move_start = self.capture(move, "prepare"), self.capture(move, "start")
        queue = {
            **self.command("play_queue_occurrence", {}),
            **self.room_context(),
            "purpose": "queue_occurrence_v1",
        }
        await self.service("prepare_room_queue", queue)
        await self.service("control_room_queue", {**queue, "prepare_id": "p"})
        for name in ("prepare_room_queue", "control_room_queue"):
            await self.refused(
                "room_action_not_allowed",
                self.service(
                    name,
                    {**queue, **self.room_context("room-beta"), "prepare_id": "p"}
                    if name == "control_room_queue"
                    else {**queue, **self.room_context("room-beta")},
                ),
            )
        self.assertEqual(execute.await_count, 1)  # prepare_room_queue is not via execute
        selected = {
            **self.command("play_context", {"context_uri": PLAYLIST, "max_volume": 90}),
            "room_id": "room-beta",
            "homepod_id": "saved-output-beta",
            "start_offer": {},
        }
        for name, mock in (
            ("prepare_room_selected_content", prepare),
            ("start_room_selected_content", start),
        ):
            await self.service(name, selected)
            self.assertEqual(mock.await_args.args[1]["args"]["max_volume"], 25)
            await self.refused(
                "room_action_not_allowed",
                self.service(name, {**selected, "room_id": "room-alpha"}),
            )
        moved = {
            **self.command("move_existing_session", {}),
            "room_id": "room-alpha",
            "homepod_id": "saved-output-alpha",
            "move_offer": {},
        }
        for name, mock in (
            ("prepare_room_move_playback", move_prepare),
            ("move_room_playback", move_start),
        ):
            await self.service(name, moved)
            await self.refused(
                "room_action_not_allowed",
                self.service(name, {**moved, "room_id": "room-beta"}),
            )
            self.assertEqual(mock.await_count, 1)

    async def test_move_playback_into_a_room_follows_its_rule(self):
        await self.refused(
            "room_action_not_allowed",
            self.service("move_playback", {"kind": "configured_alias", "target_id": "room-beta"}),
        )
        self.assertEqual(self._writes(), [])

    # --- the hidden speaker media_player (Spotify engine) -----------------------------

    async def test_speaker_entity_is_governed_by_its_current_room(self):
        registry = self.hass.data["entity_components"]["media_player"]
        engine_id = rooms.entity_registry.async_get(self.hass).async_get_entity_id(
            "media_player", "podconnect", f"{ENTRY}_{self.engine_identity}"
        )
        engine = registry.get_entity(engine_id)
        engine._native_command = AsyncMock(return_value=True)

        async def call(service, **data):
            await self.hass.services.async_call(
                "media_player",
                service,
                {"entity_id": engine_id, **data},
                blocking=True,
                context=self.user(),
            )

        await call("volume_set", volume_level=0.75)
        self.assertEqual(engine._native_command.await_args.args, ("volume", {"percent": 40}))
        await self.refused("room_action_not_allowed", call("media_next_track"))
        await self.refused(
            "room_action_not_allowed",
            call("play_media", media_content_type="playlist", media_content_id=PLAYLIST),
        )
        await self.another_account_plays()
        await self.refused("other_account_playing", call("media_pause"))
        self.assertEqual(engine._native_command.await_count, 1)


if __name__ == "__main__":
    unittest.main()
