"""Actual HA service/HTTP/OAuth boundaries; only inert loopback endpoints."""

import asyncio
import copy
import importlib
import json
import unittest
from unittest.mock import AsyncMock, patch

import test_targets as fixture
import voluptuous as vol
from aiohttp import web
from homeassistant.exceptions import HomeAssistantError
from homeassistant.helpers import entity_registry

commands = importlib.import_module("custom_components.podconnect.commands")
SpotifyApiError = importlib.import_module("custom_components.podconnect.api").SpotifyApiError
start_guards = importlib.import_module("custom_components.podconnect.native_start").start_guards


class CommandFrameworkTests(unittest.IsolatedAsyncioTestCase):
    _cleanup = fixture.TargetFrameworkTests._cleanup
    _task = fixture.TargetFrameworkTests._task
    _service = fixture.TargetFrameworkTests._service

    async def asyncSetUp(self):
        self.command_status = 204
        self.command_hold = False
        self.command_entered = asyncio.Event()
        self.command_release = asyncio.Event()
        self.command_queries = []
        self.playing = False
        self.no_playback = False
        self.native_prepares = 0
        self.native_commits = 0
        self.native_commit_status = 200
        self.native_commit_hold = False
        self.native_commit_entered = asyncio.Event()
        self.native_commit_release = asyncio.Event()
        self.native_revision = "b" * 64
        self.native_requested_room = "room-alpha"
        self.native_source_room = "room-alpha"
        self.native_output_selected = True
        self.native_output_volume = 35
        await fixture.TargetFrameworkTests.asyncSetUp(self)
        self.holds.append(self.command_release)
        self.holds.append(self.native_commit_release)
        self.calls.clear()

    async def _backend(self, request):
        if request.path == "/api/start-volume":
            proof = self._native_proof()
            if request.method == "GET":
                self.calls.append(("GET", request.path, None, False))
                return web.json_response(proof)
            body = await request.json()
            self.calls.append(("POST", request.path, body, False))
            self.native_prepares += 1
            prepared = copy.deepcopy(proof)
            prepared["alias_binding"]["room_id"] = prepared["room_id"]
            prepared["output_selected"] = True
            prepared["output_volume_percent"] = 35
            if prepared["mode"] == "fresh_start":
                self.native_output_volume = 35
            return web.json_response({
                "schema": "podconnect.start_lease.v1", "lease_id": "A" * 32,
                "expires_in_ms": 5000, "proof": prepared,
            })
        if request.path == "/api/start-volume/commit":
            body = await request.json()
            self.calls.append(("POST", request.path, body, False))
            self.native_commits += 1
            if self.native_commit_hold:
                self.native_commit_entered.set()
                await self.native_commit_release.wait()
            if body.get("max_volume") is not None and self.native_output_volume > body["max_volume"]:
                return web.Response(status=409)
            if self.native_commit_status != 200:
                return web.Response(status=self.native_commit_status)
            return web.json_response({
                "ok": True, "schema": "podconnect.start_commit.v1",
                "policy_revision": self.native_revision,
            })
        if request.path == "/spotify/me/player" and request.method == "GET":
            self.calls.append((request.method, request.path, None, True))
            if self.no_playback:
                return web.Response(status=204)
            return web.json_response(
                {
                    "is_playing": self.playing,
                    "device": {
                        "id": "spotify-one",
                        "name": "Actual source",
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
        if request.method in ("POST", "PUT") and request.path in {
            "/spotify/me/player/pause",
            "/spotify/me/player/play",
            "/spotify/me/player/next",
            "/spotify/me/player/previous",
            "/spotify/me/player/volume",
        }:
            self.assertEqual(
                request.headers["Authorization"], "Bearer inert-fixture-only"
            )
            self.calls.append(
                (request.method, request.path, await request.read(), True)
            )
            self.command_queries.append(dict(request.query))
            self.command_entered.set()
            if self.command_hold:
                await self.command_release.wait()
            return web.Response(
                status=self.command_status,
                text=None if self.command_status == 204 else "NEVER_PUBLIC",
            )
        return await fixture.TargetFrameworkTests._backend(self, request)

    def _writes(self):
        return [call for call in self.calls if call[0] in ("POST", "PUT", "DELETE") and call[1].startswith("/spotify/")]

    def _native_proof(self):
        return {
            "schema": "podconnect.start_volume.v1", "ready": True,
            "engine_device_id": "spotify-one", "room_id": self.native_requested_room,
            "homepod_id": "saved-output-alpha" if self.native_requested_room == "room-alpha" else "saved-output-beta", "policy_revision": self.native_revision,
            "start_volume_percent": 35, "output_volume_percent": self.native_output_volume,
            "output_selected": self.native_output_selected,
            "mode": "fresh_start" if self.no_playback else "same_session",
            "alias_binding": {"ready": True, "incarnation": "fixture-incarnation", "registry": "fixture-registry", "room_id": self.native_source_room},
        }

    def track_args(self, uri, max_volume=40):
        return {
            "track_uri": uri, "max_volume": max_volume, "room_id": self.native_requested_room,
            "homepod_id": "saved-output-alpha" if self.native_requested_room == "room-alpha" else "saved-output-beta", "start_policy_revision": "b" * 64,
            "alias_incarnation": "fixture-incarnation",
        }

    def request(self, action="resume", **overrides):
        data = self.entries[0].runtime_data
        coord = data.coordinator
        sequence = overrides.get("command_sequence", data.commands.command_sequence + 1)
        epoch = overrides.get("owner_epoch", coord.owner_epoch)
        return {
            "config_entry_id": "account-alpha",
            "kind": "spotify_device",
            "target_id": "spotify-one",
            "owner_epoch": coord.owner_epoch,
            "expected_poll_sequence": coord.data["poll_sequence"],
            "request_id": f"{epoch}:{sequence}",
            "command_sequence": sequence,
            "action": action,
            "args": {},
            **overrides,
        }

    async def invoke(self, request):
        return await self.hass.services.async_call(
            "podconnect",
            "control_playback",
            request,
            blocking=True,
            return_response=True,
        )

    async def controls(self):
        return await self.hass.services.async_call(
            "podconnect",
            "get_playback_controls",
            {
                k: v
                for k, v in self.request().items()
                if k in ("config_entry_id", "kind", "target_id")
            },
            blocking=True,
            return_response=True,
        )

    async def test_ordinary_media_start_commits_native_volume_before_one_provider_send(self):
        self.no_playback = True
        data = self.entries[0].runtime_data
        await data.coordinator.async_refresh()
        with patch.object(data.speakers, "start_identity", new_callable=AsyncMock, return_value="spotify-one"):
            guard, native_ready = start_guards(self.hass, "account-alpha", data, "spotify-one")
            await data.api.play("spotify-one", uris=["spotify:track:" + "A" * 22], execution_guard=guard, async_execution_guard=native_ready)
        self.assertEqual(self.native_prepares, 1)
        self.assertEqual(self.native_commits, 1)
        self.assertEqual(len(self._writes()), 1)
        self.assertEqual(self._writes()[0][1], "/spotify/me/player/play")

    async def test_ordinary_start_rejects_poll_replacement_during_native_commit(self):
        self.no_playback = True
        data = self.entries[0].runtime_data
        await data.coordinator.async_refresh()
        self.native_commit_hold = True
        with patch.object(data.speakers, "start_identity", new_callable=AsyncMock, return_value="spotify-one"):
            guard, native_ready = start_guards(self.hass, "account-alpha", data, "spotify-one")
            action = self._task(data.api.play("spotify-one", uris=["spotify:track:" + "B" * 22], execution_guard=guard, async_execution_guard=native_ready))
            await self.native_commit_entered.wait()
            await data.coordinator.async_refresh()
            self.native_commit_release.set()
            with self.assertRaises(SpotifyApiError):
                await action
        self.assertEqual(self.native_commits, 1)
        self.assertEqual(self._writes(), [])

    async def test_entity_play_media_uses_native_start_barrier(self):
        self.no_playback = True
        data = self.entries[0].runtime_data
        await data.coordinator.async_refresh()
        entity_id = entity_registry.async_get(self.hass).async_get_entity_id(
            "media_player", "podconnect", "account-alpha_spotify-one"
        )
        self.assertIsNotNone(entity_id)
        with patch.object(data.speakers, "start_identity", new_callable=AsyncMock, return_value="spotify-one"):
            await self.hass.services.async_call("media_player", "play_media", {
                "entity_id": entity_id, "media_content_type": "music",
                "media_content_id": "spotify:track:" + "C" * 22,
            }, blocking=True)
        self.assertEqual(self.native_prepares, 1)
        self.assertEqual(self.native_commits, 1)
        self.assertEqual(len(self._writes()), 1)

    async def test_playing_transfer_to_native_uses_start_barrier(self):
        self.playing = True
        data = self.entries[0].runtime_data
        await data.coordinator.async_refresh()
        with patch.object(data.speakers, "start_identity", new_callable=AsyncMock, return_value="spotify-one"):
            status, _ = await self._service("move_playback", {
                "config_entry_id": "account-alpha", "kind": "spotify_device", "target_id": "spotify-one",
            })
        self.assertEqual(status, 200)
        self.assertEqual(self.native_prepares, 1)
        self.assertEqual(self.native_commits, 1)
        self.assertEqual(len(self._writes()), 1)
        self.assertEqual(self._writes()[0][1], "/spotify/me/player")

    async def test_same_session_native_output_above_approved_max_refuses_track(self):
        self.native_output_volume = 90
        request = self.request("play_track", args=self.track_args("spotify:track:" + "D" * 22, max_volume=40))
        result = await self.invoke(request)
        self.assertEqual(result["outcome"], "rejected")
        self.assertEqual(self.native_prepares, 0)
        self.assertEqual(self._writes(), [])

    async def test_held_native_commit_rechecks_approved_output_max(self):
        self.native_commit_hold = True
        request = self.request("play_track", args=self.track_args("spotify:track:" + "E" * 22, max_volume=40))
        action = self._task(self.invoke(request))
        await self.native_commit_entered.wait()
        self.native_output_volume = 90
        self.native_commit_release.set()
        result = await action
        self.assertNotEqual(result["outcome"], "accepted")
        self.assertEqual(self.native_commits, 1)
        self.assertEqual(self._writes(), [])

    async def test_service_descriptor_response_only_and_observed_capabilities(self):
        response = await self.client.get("/api/services")
        rows = await response.json()
        services = next(
            row["services"] for row in rows if row["domain"] == "podconnect"
        )
        self.assertEqual(services["control_playback"]["response"], {"optional": False})
        self.assertEqual(
            set(services["control_playback"]["fields"]), set(self.request())
        )
        self.assertEqual(
            services["get_playback_controls"]["response"], {"optional": False}
        )
        result = await self.controls()
        self.assertEqual(result["volume_percent"], 30)
        self.assertEqual(result["actions"], ["resume", "next", "previous", "volume"])
        self.assertEqual(self.calls, [])
        self.assertNotIn("NEVER_PUBLIC", json.dumps(result))
        status, _body = await self._service(
            "control_playback", self.request(), response=False
        )
        self.assertNotEqual(status, 200)
        self.assertEqual(self._writes(), [])

    async def test_five_exact_commands_and_poll_floor_use_existing_client(self):
        coord = self.entries[0].runtime_data.coordinator
        for action in ("resume", "pause", "next", "previous", "volume"):
            self.playing = action != "resume"
            await coord.async_refresh()
            request = self.request(
                action, args={"percent": 42} if action == "volume" else {}
            )
            status, body = await self._service("control_playback", request)
            self.assertEqual(status, 200)
            result = body["service_response"]
            self.assertEqual(result["outcome"], "accepted")
            self.assertTrue(result["provider_request_accepted"])
            self.assertFalse(result["physical_result_verified"])
            self.assertFalse(result["retry_allowed"])
            self.assertEqual(self.command_queries[-1]["device_id"], "spotify-one")
            self.assertEqual((await self.controls())["status"], "awaiting_observation")
        self.assertEqual(len(self._writes()), 5)
        self.assertEqual(self.command_queries[-1]["volume_percent"], "42")

    async def test_selected_track_uses_exact_device_and_one_provider_send(self):
        uri = "spotify:track:" + "A" * 22
        request = self.request("play_track", args=self.track_args(uri))
        controls = await self.hass.services.async_call(
            "podconnect",
            "get_track_play_controls",
            {
                "config_entry_id": "account-alpha",
                "kind": "spotify_device",
                "target_id": "spotify-one",
                "room_id": "room-alpha",
            },
            blocking=True,
            return_response=True,
        )
        self.assertEqual(controls["actions"], ["play_track"])
        self.assertEqual(controls["volume_percent"], 30)
        receipt = await self.invoke(request)
        self.assertEqual(receipt["outcome"], "accepted")
        self.assertEqual(receipt["target"]["target_id"], "spotify-one")
        self.assertEqual(self.command_queries[-1]["device_id"], "spotify-one")
        self.assertEqual(json.loads(self._writes()[0][2]), {"uris": [uri]})
        sequence = [call[1] for call in self.calls if call[1] in (
            "/api/start-volume", "/api/start-volume/commit", "/spotify/me/player/play"
        )]
        self.assertEqual(sequence[-3:], [
            "/api/start-volume", "/api/start-volume/commit", "/spotify/me/player/play"
        ])
        self.assertEqual(self.native_prepares, 1)
        self.assertEqual(self.native_commits, 1)
        self.assertEqual(await self.invoke(request), receipt)
        self.assertEqual(len(self._writes()), 1)

    async def test_selected_track_rejects_raw_query_and_wrong_uri_before_send(self):
        for uri in ("jazz", "spotify:album:" + "A" * 22, "spotify:track:short"):
            result = await self.invoke(
                self.request("play_track", args=self.track_args(uri))
            )
            self.assertEqual(result["outcome"], "rejected")
        self.assertEqual(self._writes(), [])

    async def test_selected_track_rechecks_approved_volume_and_does_not_send(self):
        uri = "spotify:track:" + "A" * 22
        result = await self.invoke(
            self.request("play_track", args=self.track_args(uri, 20))
        )
        self.assertEqual(result["outcome"], "rejected")
        self.assertEqual(result["error_kind"], "volume_above_approved_limit")
        self.assertEqual(self._writes(), [])

    async def test_selected_track_requires_shared_start_floor_and_fresh_native_revision(self):
        uri = "spotify:track:" + "A" * 22
        floor = await self.invoke(self.request("play_track", args=self.track_args(uri, 34)))
        self.assertEqual(floor["error_kind"], "native_start_policy_changed")
        self.assertEqual(self.native_prepares, 0)
        self.assertEqual(self._writes(), [])
        self.native_revision = "c" * 64
        changed = await self.invoke(self.request("play_track", args=self.track_args(uri, 40)))
        self.assertEqual(changed["error_kind"], "native_start_policy_changed")
        self.assertEqual(self.native_prepares, 0)
        self.assertEqual(self._writes(), [])

    async def test_native_lease_rejection_after_oauth_never_sends_or_retries(self):
        uri = "spotify:track:" + "A" * 22
        self.native_commit_status = 409
        request = self.request("play_track", args=self.track_args(uri))
        receipt = await self.invoke(request)
        self.assertEqual(receipt["outcome"], "rejected")
        self.assertFalse(receipt["retry_allowed"])
        self.assertEqual(self.native_prepares, 1)
        self.assertEqual(self.native_commits, 1)
        self.assertEqual(self._writes(), [])
        self.assertEqual(await self.invoke(request), receipt)
        self.assertEqual(self.native_commits, 1)

    async def test_poll_replacement_while_native_commit_waits_never_sends(self):
        self.native_commit_hold = True
        uri = "spotify:track:" + "A" * 22
        request = self.request("play_track", args=self.track_args(uri))
        task = asyncio.create_task(self.invoke(request))
        await asyncio.wait_for(self.native_commit_entered.wait(), 5)
        await self.entries[0].runtime_data.coordinator.async_refresh()
        self.native_commit_release.set()
        receipt = await asyncio.wait_for(task, 5)
        self.assertEqual(receipt["outcome"], "rejected")
        self.assertEqual(receipt["error_kind"], "stale_observation")
        self.assertEqual(self._writes(), [])
        self.assertEqual(await self.invoke(request), receipt)
        self.assertEqual(self.native_commits, 1)

    async def test_selected_track_can_start_on_exact_inactive_idle_device(self):
        self.no_playback = True
        await self.entries[0].runtime_data.coordinator.async_refresh()
        self.entries[0].runtime_data.coordinator.data["devices"][0]["is_active"] = False
        uri = "spotify:track:" + "A" * 22
        controls = await self.hass.services.async_call(
            "podconnect",
            "get_track_play_controls",
            {
                "config_entry_id": "account-alpha",
                "kind": "spotify_device",
                "target_id": "spotify-one",
                "room_id": "room-alpha",
            },
            blocking=True,
            return_response=True,
        )
        self.assertEqual(controls["actions"], ["play_track"])
        self.assertIs(controls["target_is_active"], False)
        result = await self.invoke(
            self.request("play_track", args=self.track_args(uri))
        )
        self.assertEqual(result["outcome"], "accepted")
        self.assertEqual(self.command_queries[-1]["device_id"], "spotify-one")
        self.assertEqual(len(self._writes()), 1)

    async def test_idle_configured_second_room_uses_native_prepared_binding(self):
        self.no_playback = True
        self.native_requested_room = "room-beta"
        self.native_source_room = "room-alpha"
        self.native_output_selected = False
        await self.entries[0].runtime_data.coordinator.async_refresh()
        controls = await self.hass.services.async_call(
            "podconnect", "get_track_play_controls",
            {"config_entry_id":"account-alpha", "kind":"spotify_device", "target_id":"spotify-one", "room_id":"room-beta"},
            blocking=True, return_response=True,
        )
        self.assertEqual(controls["actions"], ["play_track"])
        self.assertEqual(controls["native_start"]["alias_binding"]["room_id"], "room-alpha")
        uri = "spotify:track:" + "A" * 22
        result = await self.invoke(self.request("play_track", args=self.track_args(uri)))
        self.assertEqual(result["outcome"], "accepted")
        self.assertEqual(self.native_prepares, 1)
        self.assertEqual(self.native_commits, 1)
        self.assertEqual(len(self._writes()), 1)

    async def test_selected_track_rejects_unknown_device_state_or_volume(self):
        data = self.entries[0].runtime_data.coordinator.data
        for change in (
            {"is_active": None},
            {"volume_percent": None},
            {"is_restricted": True},
            {"name": "A" * 1025},
        ):
            original = dict(data["devices"][0])
            data["devices"][0].update(change)
            result = await self.invoke(
                self.request(
                    "play_track",
                    args=self.track_args("spotify:track:" + "A" * 22),
                )
            )
            self.assertEqual(result["outcome"], "rejected")
            data["devices"][0].clear()
            data["devices"][0].update(original)
        self.assertEqual(self._writes(), [])

    async def test_duplicate_reuse_and_no_second_command_before_new_poll(self):
        request = self.request()
        accepted = await self.invoke(request)
        self.assertEqual(await self.invoke(request), accepted)
        self.assertEqual(
            (await self.invoke({**request, "action": "next"}))["error_kind"],
            "request_reused",
        )
        self.assertEqual(
            (await self.invoke(self.request()))["error_kind"],
            "awaiting_observation",
        )
        self.assertEqual(len(self._writes()), 1)
        # A consumer cannot mutate the cached command receipt.
        accepted["outcome"] = "tampered"
        self.assertEqual((await self.invoke(request))["outcome"], "accepted")

    async def test_invalid_account_epoch_target_revision_and_namespace_never_send(self):
        for change in (
            {"config_entry_id": "account-beta"},
            {"owner_epoch": "old"},
            {"target_id": "missing"},
            {"expected_poll_sequence": 999},
        ):
            result = await self.invoke(self.request(**change))
            self.assertEqual(result["outcome"], "rejected")
        for change in (
            {"kind": "configured_alias"},
            {"kind": "observed_output"},
            {"expected_poll_sequence": True},
            {"command_sequence": True},
            {"command_sequence": 0},
            {"command_sequence": 2**53},
            {"unexpected": "NEVER_PUBLIC"},
        ):
            with self.assertRaises((HomeAssistantError, vol.Invalid)):
                await self.invoke(self.request(**change))
        self.assertEqual(self._writes(), [])

    async def test_malformed_arguments_restrictions_capabilities_and_source_age(self):
        for args in (
            {"percent": True},
            {"percent": -1},
            {"percent": 101},
            {"percent": 1.5},
            {"percent": 20, "token": "NEVER_PUBLIC"},
        ):
            self.assertEqual(
                (await self.invoke(self.request("volume", args=args)))["outcome"],
                "rejected",
            )
        data = self.entries[0].runtime_data.coordinator.data
        baseline = copy.deepcopy(data)
        for mutate in (
            lambda: data["devices"][0].update(is_restricted=True),
            lambda: data["playback"]["device"].update(is_restricted=True),
            lambda: data["playback"]["actions"]["disallows"].update(resuming=True),
            lambda: data.update(playback_observed_monotonic=0),
            lambda: data["devices"].append(copy.deepcopy(data["devices"][0])),
        ):
            data.clear()
            data.update(copy.deepcopy(baseline))
            mutate()
            self.assertEqual(
                (await self.invoke(self.request()))["outcome"],
                "rejected",
            )
        self.assertEqual(self._writes(), [])

    async def test_unload_and_poll_replacement_during_oauth_revalidate_before_send(
        self,
    ):
        data = self.entries[0].runtime_data
        original = data.session.async_ensure_token_valid
        for mutation in ("unload", "poll"):
            entered, release = asyncio.Event(), asyncio.Event()
            self.holds.append(release)

            async def held(entered=entered, release=release):
                await original()
                entered.set()
                await release.wait()

            with patch.object(data.session, "async_ensure_token_valid", held):
                task = self._task(self.invoke(self.request()))
                await asyncio.wait_for(entered.wait(), 2)
                if mutation == "unload":
                    data.active = False
                else:
                    data.coordinator.data["poll_sequence"] += 1
                release.set()
                result = await task
                self.assertEqual(result["outcome"], "rejected")
                data.active = True
        self.assertEqual(self._writes(), [])

    async def test_timeout_after_send_retains_unknown_and_duplicate_never_retries(self):
        self.command_hold = True
        request = self.request("next")
        with patch.object(commands, "COMMAND_TIMEOUT_S", 0.1):
            task = self._task(self.invoke(request))
            await asyncio.wait_for(self.command_entered.wait(), 2)
            busy = await self.invoke(self.request("next"))
            self.assertEqual(busy["error_kind"], "busy")
            result = await task
        self.assertEqual(result["outcome"], "unknown")
        self.assertFalse(self.entries[0].runtime_data.commands.busy)
        self.assertEqual(await self.invoke(request), result)
        self.assertEqual(len(self._writes()), 1)
        self.command_release.set()

    async def test_owner_cancellation_retires_send_and_keeps_unknown_receipt(self):
        self.command_hold = True
        request = self.request("next")
        owner = self.entries[0].runtime_data.commands
        task = self._task(owner.execute(self.hass, request))
        await asyncio.wait_for(self.command_entered.wait(), 2)
        task.cancel()
        with self.assertRaises(asyncio.CancelledError):
            await task
        self.assertFalse(owner.busy)
        self.assertEqual((await self.invoke(request))["outcome"], "unknown")
        self.assertEqual(len(self._writes()), 1)
        self.command_release.set()

    async def test_provider_error_no_secrets_and_unknown_eviction_never_resends(self):
        self.command_status = 403
        request = self.request()
        result = await self.invoke(request)
        self.assertEqual(result["outcome"], "unknown")
        self.assertNotIn("NEVER_PUBLIC", json.dumps(result))
        self.command_status = 204
        with patch.object(commands, "MAX_RECEIPTS", 1):
            await self.entries[0].runtime_data.coordinator.async_refresh()
            self.assertEqual((await self.invoke(self.request()))["outcome"], "accepted")
            self.assertEqual(
                (await self.invoke(request))["error_kind"], "stale_command"
            )
        self.assertEqual(len(self._writes()), 2)

    async def test_success_remains_accepted_if_poll_unavailable_and_reload_rejects_old_epoch(
        self,
    ):
        request = self.request()
        self.assertEqual((await self.invoke(request))["outcome"], "accepted")
        self.entries[0].runtime_data.coordinator.last_update_success = False
        self.assertEqual((await self.invoke(request))["outcome"], "accepted")
        self.assertTrue(await self.hass.config_entries.async_reload("account-alpha"))
        self.assertNotEqual(
            self.entries[0].runtime_data.coordinator.owner_epoch, request["owner_epoch"]
        )
        self.assertEqual((await self.invoke(request))["outcome"], "rejected")
        self.assertEqual(len(self._writes()), 1)

    async def test_replaced_http_owner_and_cancel_before_oauth_never_send(self):
        data = self.entries[0].runtime_data
        original = data.session.async_ensure_token_valid
        for operation in ("client", "cancel", "owner"):
            entered, release = asyncio.Event(), asyncio.Event()
            self.holds.append(release)

            async def held(entered=entered, release=release):
                await original()
                entered.set()
                await release.wait()

            request = self.request()
            with patch.object(data.session, "async_ensure_token_valid", held):
                task = self._task(data.commands.execute(self.hass, request))
                await asyncio.wait_for(entered.wait(), 2)
                if operation == "cancel":
                    task.cancel()
                    with self.assertRaises(asyncio.CancelledError):
                        await task
                    self.assertEqual(
                        (await self.invoke(request))["outcome"], "rejected"
                    )
                elif operation == "owner":
                    with patch.object(data, "commands", object()):
                        release.set()
                        self.assertEqual((await task)["error_kind"], "owner_replaced")
                else:
                    with patch.object(data.api, "_web", object()):
                        release.set()
                        self.assertEqual((await task)["error_kind"], "owner_replaced")
            self.assertFalse(data.commands.busy)
        self.assertEqual(self._writes(), [])

    async def test_real_http_caller_cancellation_does_not_resend_or_orphan_owner(self):
        self.command_hold = True
        request = self.request("next")
        with patch.object(commands, "COMMAND_TIMEOUT_S", 0.1):
            task = self._task(self._service("control_playback", request))
            await asyncio.wait_for(self.command_entered.wait(), 2)
            task.cancel()
            with self.assertRaises(asyncio.CancelledError):
                await task
            async with asyncio.timeout(2):
                while self.entries[0].runtime_data.commands.busy:
                    await asyncio.sleep(0.01)
        self.command_release.set()
        self.assertEqual((await self.invoke(request))["outcome"], "unknown")
        self.assertEqual(len(self._writes()), 1)

    async def test_missing_volume_support_and_disallows_do_not_advertise_controls(self):
        data = self.entries[0].runtime_data.coordinator.data
        data["playback"]["device"].pop("supports_volume")
        data["playback"]["actions"]["disallows"].update(
            resuming=True, skipping_next=True, skipping_prev=True
        )
        self.assertEqual((await self.controls())["actions"], [])
        self.assertEqual(
            (await self.invoke(self.request("volume", args={"percent": 30})))[
                "error_kind"
            ],
            "unsupported_action",
        )
        self.assertEqual(self._writes(), [])

    async def test_nonacceptance_statuses_never_become_success_or_follow_redirect(self):
        coord = self.entries[0].runtime_data.coordinator
        for status in (200, 302, 403, 429, 500):
            self.command_status = status
            await coord.async_refresh()
            result = await self.invoke(self.request())
            self.assertEqual(result["outcome"], "unknown")
            self.assertFalse(result["provider_request_accepted"])
        self.assertEqual(len(self._writes()), 5)

    async def test_more_than_128_commands_keep_bounded_receipts_and_permanent_replay_floor(
        self,
    ):
        data = self.entries[0].runtime_data
        first = self.request()
        for index in range(132):
            await data.coordinator.async_refresh()
            request = self.request()
            result = await self.invoke(request)
            self.assertEqual(result["outcome"], "accepted")
            self.assertEqual(result["command_sequence"], index + 1)
            if index == 0:
                first = request
        self.assertEqual(len(data.commands.receipts), 128)
        self.assertEqual(data.commands.command_sequence, 132)
        await data.coordinator.async_refresh()
        observed = await self.controls()
        self.assertEqual(observed["status"], "ready")
        self.assertEqual(observed["next_command_sequence"], 133)
        self.assertEqual((await self.invoke(first))["error_kind"], "stale_command")
        changed = {
            **first,
            "expected_poll_sequence": observed["poll_sequence"],
            "action": "next",
        }
        self.assertEqual((await self.invoke(changed))["error_kind"], "stale_command")
        self.assertEqual(
            (await self.invoke({**changed, "request_id": "different"}))["error_kind"],
            "invalid_request_identity",
        )
        self.assertEqual(len(self._writes()), 132)

    async def test_sequence_gaps_identity_and_reload_namespace_fail_closed(self):
        data = self.entries[0].runtime_data
        self.assertEqual((await self.controls())["next_command_sequence"], 1)
        self.assertEqual(
            (await self.invoke(self.request(command_sequence=2)))["error_kind"],
            "command_sequence_gap",
        )
        self.assertEqual(
            (await self.invoke(self.request(request_id="browser-uuid")))["error_kind"],
            "invalid_request_identity",
        )
        self.assertEqual(data.commands.command_sequence, 0)
        with self.assertRaises(ValueError):
            commands.PlaybackCommands(data)
        request = self.request()
        self.assertEqual((await self.invoke(request))["outcome"], "accepted")
        self.assertTrue(await self.hass.config_entries.async_reload("account-alpha"))
        fresh = await self.controls()
        self.assertEqual(fresh["next_command_sequence"], 1)
        self.assertNotEqual(fresh["owner_epoch"], request["owner_epoch"])
        self.assertEqual((await self.invoke(request))["error_kind"], "stale_owner")
        self.assertEqual((await self.controls())["next_command_sequence"], 1)
        self.assertEqual((await self.invoke(self.request()))["outcome"], "accepted")
        self.assertEqual(len(self._writes()), 2)

    async def test_playlist_context_keeps_native_prepare_commit_and_existing_context_uri(self):
        uri = 'spotify:playlist:' + 'P' * 22
        args = self.track_args(uri)
        args['context_uri'] = args.pop('track_uri')
        controls = await self.hass.services.async_call('podconnect', 'get_context_play_controls',
            {'config_entry_id': 'account-alpha', 'kind': 'spotify_device',
             'target_id': 'spotify-one', 'room_id': 'room-alpha'}, blocking=True, return_response=True)
        self.assertEqual(controls['actions'], ['play_context'])
        self.assertTrue(controls['context_play_supported'])
        request = self.request('play_context', args=args)
        result = await self.invoke(request)
        self.assertEqual(result['outcome'], 'accepted')
        self.assertEqual(json.loads(self._writes()[0][2]), {'context_uri': uri})
        self.assertEqual((self.native_prepares, self.native_commits), (1, 1))
        self.assertEqual(await self.invoke(request), result)
        self.assertEqual(len(self._writes()), 1)
        self.assertEqual(self.command_queries[-1]['device_id'], 'spotify-one')

    async def test_playlist_context_rejects_track_uri_and_stale_native_proof(self):
        args = self.track_args('spotify:track:' + 'A' * 22)
        args['context_uri'] = args.pop('track_uri')
        self.assertEqual((await self.invoke(self.request('play_context', args=args)))['outcome'], 'rejected')
        args['context_uri'] = 'spotify:playlist:' + 'P' * 22
        args['start_policy_revision'] = 'c' * 64
        self.assertEqual((await self.invoke(self.request('play_context', args=args)))['outcome'], 'rejected')
        self.assertEqual(self._writes(), [])

    async def test_playlist_context_unknown_native_commit_never_sends_or_retries(self):
        self.native_commit_status = 500
        args = self.track_args('spotify:playlist:' + 'P' * 22)
        args['context_uri'] = args.pop('track_uri')
        request = self.request('play_context', args=args)
        first = await self.invoke(request)
        self.assertEqual(first['outcome'], 'rejected')
        self.assertEqual(await self.invoke(request), first)
        self.assertEqual(self._writes(), [])
        self.assertEqual(self.native_commits, 1)
