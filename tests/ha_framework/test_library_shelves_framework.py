"""Find musik shelves: actual HA response-only services; inert loopback provider only."""

import asyncio
import unittest
from unittest.mock import patch

import test_targets as fixture
from aiohttp import web
from homeassistant.core import SupportsResponse
from homeassistant.exceptions import HomeAssistantError

IMAGE = "https://i.scdn.co/image/ab6761610000e5eb0123456789abcdef"
SERVICES = ("top_artists", "saved_albums", "saved_shows", "recent_contexts")


def sid(char, index=0):
    return char * 21 + str(index)


def artist(index, *, image=True):
    return {
        "type": "artist",
        "id": sid("R", index),
        "uri": "spotify:artist:" + sid("R", index),
        "name": f"Artist {index}",
        "images": [{"url": IMAGE, "height": 640}] if image else [],
        "genres": ["PRIVATE"],
        "href": "https://api.spotify.com/v1/artists/PRIVATE",
    }


def album(index):
    return {
        "type": "album",
        "id": sid("A", index),
        "uri": "spotify:album:" + sid("A", index),
        "name": f"Album {index}",
        "artists": [{"name": "First"}, {"name": "Second"}],
        "images": [{"url": IMAGE}],
        "label": "PRIVATE",
    }


def show(index, *, publisher=None):
    value = {
        "type": "show",
        "id": sid("S", index),
        "uri": "spotify:show:" + sid("S", index),
        "name": f"Podcast {index}",
        "images": [{"url": "https://attacker.example/image/x"}, {"url": IMAGE}],
        "description": "PRIVATE",
    }
    if publisher is not None:
        value["publisher"] = publisher
    return value


def playlist(index):
    return {
        "type": "playlist",
        "id": sid("P", index),
        "uri": "spotify:playlist:" + sid("P", index),
        "name": f"Playlist {index}",
        "images": [{"url": "https://i.scdn.co/image/playlist"}],
    }


def play(context, track=None, played_at="2026-11-19T07:00:00.000Z"):
    return {
        "played_at": played_at,
        "context": context,
        "track": track or {"type": "track", "album": album(9)},
    }


def page(rows, limit=20, next_page=None):
    return {
        "items": rows,
        "limit": limit,
        "offset": 0,
        "total": len(rows),
        "next": next_page,
    }


class LibraryShelvesFrameworkTests(unittest.IsolatedAsyncioTestCase):
    engine_identity = fixture.TargetFrameworkTests.engine_identity
    asyncSetUp = fixture.TargetFrameworkTests.asyncSetUp
    _cleanup = fixture.TargetFrameworkTests._cleanup
    _task = fixture.TargetFrameworkTests._task
    _writes = fixture.TargetFrameworkTests._writes
    _service = fixture.TargetFrameworkTests._service

    def provider(self):
        recent = [
            play({"type": "playlist", "uri": playlist(1)["uri"]}),
            play(
                {"type": "album", "uri": album(2)["uri"]},
                {"type": "track", "album": album(2)},
            ),
            play({"type": "playlist", "uri": playlist(1)["uri"]}),
            play(None),
            play({"type": "artist", "uri": artist(3)["uri"]}),
            play({"type": "show", "uri": show(4)["uri"]}),
            play({"type": "playlist", "uri": playlist(5)["uri"]}),
            play({"type": "collection", "uri": "spotify:user:PRIVATE:collection"}),
            play({"type": "album", "uri": album(6)["uri"]}),
        ]
        return {
            "/spotify/me/top/artists": page([artist(0), artist(1, image=False)]),
            "/spotify/me/albums": page(
                [{"added_at": "PRIVATE", "album": album(0)}, None],
                next_page="https://api.spotify.com/v1/me/albums?offset=20&limit=20",
            ),
            "/spotify/me/shows": page(
                [
                    {"added_at": "PRIVATE", "show": show(0)},
                    {"added_at": "PRIVATE", "show": show(1, publisher="Real Radio")},
                ]
            ),
            "/spotify/me/player/recently-played": {
                "items": recent,
                "cursors": {"after": "PRIVATE"},
                "limit": 50,
            },
            "/spotify/playlists/" + sid("P", 1): playlist(1),
            "/spotify/artists/" + sid("R", 3): artist(3),
            "/spotify/shows/" + sid("S", 4): show(4),
            "/spotify/albums/" + sid("A", 6): album(6),
        }

    async def _backend(self, request):
        if (
            not request.path.startswith("/spotify/me/")
            and request.path != "/spotify/search"
            and not any(
                request.path.startswith("/spotify/" + kind + "/")
                for kind in ("albums", "artists", "playlists", "shows")
            )
        ):
            return await fixture.TargetFrameworkTests._backend(self, request)
        if request.path in ("/spotify/me/player", "/spotify/me/player/devices"):
            return await fixture.TargetFrameworkTests._backend(self, request)
        self.assertEqual(request.method, "GET")
        self.assertEqual(
            request.headers.get("Authorization"), "Bearer inert-fixture-only"
        )
        self.calls.append((request.method, request.path, dict(request.query), True))
        if self.hold_path == request.path:
            self.entered.set()
            await self.release.wait()
        status = getattr(self, "statuses", {}).get(request.path, 200)
        if status != 200:
            return web.Response(
                status=status,
                text="PRIVATE provider body",
                headers={"Retry-After": "1"},
            )
        override = getattr(self, "overrides", {}).get(request.path)
        if override is not None:
            return web.json_response(override)
        body = self.provider().get(request.path)
        if body is None:
            return web.Response(status=404, text="PRIVATE")
        return web.json_response(body)

    def reads(self):
        return [
            (path, query)
            for _, path, query, _ in self.calls
            if path.startswith("/spotify/")
            and path not in ("/spotify/me/player", "/spotify/me/player/devices")
        ]

    async def call(self, service, **data):
        return await self.hass.services.async_call(
            "podconnect",
            service,
            {"config_entry_id": "account-alpha", **data},
            blocking=True,
            return_response=True,
        )

    async def test_registration_is_response_only_with_closed_fields(self):
        for service in SERVICES:
            self.assertIs(
                self.hass.services.supports_response("podconnect", service),
                SupportsResponse.ONLY,
            )
        response = await self.client.get("/api/services")
        services = next(
            row["services"]
            for row in await response.json()
            if row["domain"] == "podconnect"
        )
        self.assertEqual(
            set(services["top_artists"]["fields"]),
            {"config_entry_id", "limit", "time_range"},
        )
        for service in ("saved_albums", "saved_shows", "recent_contexts"):
            self.assertEqual(
                set(services[service]["fields"]), {"config_entry_id", "limit"}
            )
        self.calls.clear()
        for service, invalid in (
            ("top_artists", {}),
            ("top_artists", {"config_entry_id": "account-alpha", "limit": 21}),
            ("top_artists", {"config_entry_id": "account-alpha", "limit": True}),
            ("top_artists", {"config_entry_id": "account-alpha", "time_range": "x"}),
            ("saved_albums", {"config_entry_id": "account-alpha", "limit": 0}),
            ("saved_shows", {"config_entry_id": "account-alpha", "offset": 5}),
            ("recent_contexts", {"config_entry_id": "account-alpha", "limit": 13}),
        ):
            with self.subTest(service=service, invalid=invalid):
                self.assertEqual((await self._service(service, invalid))[0], 400)
        for service in SERVICES:
            self.assertEqual(
                (
                    await self._service(
                        service, {"config_entry_id": "account-alpha"}, response=False
                    )
                )[0],
                400,
            )
        self.assertEqual(self.reads(), [])

    async def test_top_artists_exact_account_epoch_and_bounded_projection(self):
        epoch = self.entries[1].runtime_data.coordinator.owner_epoch
        self.calls.clear()
        status, body = await self._service(
            "top_artists", {"config_entry_id": "account-beta"}
        )
        self.assertEqual(status, 200)
        reply = body["service_response"]
        self.assertEqual(
            reply,
            {
                "ok": True,
                "schema": "podconnect.top_artists.v1",
                "config_entry_id": "account-beta",
                "owner_epoch": epoch,
                "source": "spotify_web_api",
                "coverage": "first_page",
                "time_range": "medium_term",
                "limit": 20,
                "returned_count": 2,
                "source_has_more": False,
                "artists": [
                    {
                        "id": sid("R", 0),
                        "uri": "spotify:artist:" + sid("R", 0),
                        "name": "Artist 0",
                        "artwork": {
                            "kind": "artist_image",
                            "provider": "spotify",
                            "url": IMAGE,
                        },
                    },
                    {
                        "id": sid("R", 1),
                        "uri": "spotify:artist:" + sid("R", 1),
                        "name": "Artist 1",
                    },
                ],
            },
        )
        self.assertEqual(
            self.reads(),
            [
                (
                    "/spotify/me/top/artists",
                    {"limit": "20", "offset": "0", "time_range": "medium_term"},
                )
            ],
        )
        self.calls.clear()
        reply = await self.call("top_artists", limit=5, time_range="short_term")
        self.assertEqual(reply["time_range"], "short_term")
        self.assertEqual(
            self.reads(),
            [
                (
                    "/spotify/me/top/artists",
                    {"limit": "5", "offset": "0", "time_range": "short_term"},
                )
            ],
        )
        self.assertEqual(self._writes(), [])

    async def test_saved_albums_and_shows_never_invent_fields(self):
        self.calls.clear()
        albums = await self.call("saved_albums")
        self.assertEqual(albums["schema"], "podconnect.saved_albums.v1")
        self.assertEqual(
            albums["albums"],
            [
                {
                    "id": sid("A", 0),
                    "uri": "spotify:album:" + sid("A", 0),
                    "name": "Album 0",
                    "artist": "First, Second",
                    "artwork": {
                        "kind": "album_cover",
                        "provider": "spotify",
                        "url": IMAGE,
                    },
                }
            ],
        )
        self.assertTrue(albums["source_has_more"])
        shows = await self.call("saved_shows", limit=20)
        self.assertEqual(shows["schema"], "podconnect.saved_shows.v1")
        self.assertEqual(
            [(s["name"], s["publisher"]) for s in shows["shows"]],
            [("Podcast 0", None), ("Podcast 1", "Real Radio")],
        )
        self.assertEqual(shows["shows"][0]["artwork"]["kind"], "show_cover")
        self.assertEqual(shows["shows"][0]["artwork"]["url"], IMAGE)
        self.assertEqual(
            [path for path, _ in self.reads()],
            ["/spotify/me/albums", "/spotify/me/shows"],
        )
        self.assertNotIn("PRIVATE", str(albums) + str(shows))
        self.assertEqual(self._writes(), [])

    async def test_recent_contexts_distinct_newest_first_and_drop_unresolved(self):
        self.calls.clear()
        reply = await self.call("recent_contexts")
        self.assertEqual(reply["schema"], "podconnect.recent_contexts.v1")
        self.assertEqual(
            [(c["type"], c["uri"], c["name"]) for c in reply["contexts"]],
            [
                ("playlist", "spotify:playlist:" + sid("P", 1), "Playlist 1"),
                ("album", "spotify:album:" + sid("A", 2), "Album 2"),
                ("artist", "spotify:artist:" + sid("R", 3), "Artist 3"),
                ("show", "spotify:show:" + sid("S", 4), "Podcast 4"),
                ("album", "spotify:album:" + sid("A", 6), "Album 6"),
            ],
        )
        self.assertEqual(
            [c["artwork"]["kind"] for c in reply["contexts"]],
            [
                "playlist_cover",
                "album_cover",
                "artist_image",
                "show_cover",
                "album_cover",
            ],
        )
        self.assertEqual(
            reply["contexts"][0]["last_played_at"], "2026-11-19T07:00:00.000Z"
        )
        self.assertEqual(
            (
                reply["limit"],
                reply["scanned_play_count"],
                reply["returned_count"],
                reply["dropped_count"],
            ),
            (12, 9, 5, 1),
        )
        # One history read, one lookup per unresolved context; album 2 came free.
        self.assertEqual(
            self.reads(),
            [
                ("/spotify/me/player/recently-played", {"limit": "50"}),
                (
                    "/spotify/playlists/" + sid("P", 1),
                    {"fields": "type,id,uri,name,images"},
                ),
                ("/spotify/artists/" + sid("R", 3), {}),
                ("/spotify/shows/" + sid("S", 4), {}),
                (
                    "/spotify/playlists/" + sid("P", 5),
                    {"fields": "type,id,uri,name,images"},
                ),
                ("/spotify/albums/" + sid("A", 6), {}),
            ],
        )
        self.assertNotIn("PRIVATE", str(reply))
        self.calls.clear()
        reply = await self.call("recent_contexts", limit=2)
        self.assertEqual(len(reply["contexts"]), 2)
        self.assertEqual(len(self.reads()), 2)

    async def test_resolved_object_must_be_the_played_context(self):
        self.overrides = {"/spotify/artists/" + sid("R", 3): artist(7)}
        reply = await self.call("recent_contexts")
        self.assertNotIn("artist", [context["type"] for context in reply["contexts"]])
        self.assertEqual(reply["dropped_count"], 2)

    async def test_rate_limit_is_one_honest_failure_without_retries(self):
        cases = {
            "top_artists": "/spotify/me/top/artists",
            "saved_albums": "/spotify/me/albums",
            "saved_shows": "/spotify/me/shows",
            "recent_contexts": "/spotify/me/player/recently-played",
        }
        for service, path in cases.items():
            with self.subTest(service=service):
                fixture.expire_spotify_backoff(self.entries)
                self.calls.clear()
                self.statuses = {path: 429}
                with self.assertRaisesRegex(HomeAssistantError, "rate limited") as err:
                    await self.call(service)
                self.assertNotIn("PRIVATE", str(err.exception))
                self.assertEqual(len(self.reads()), 1)
        # A rate limit while resolving stops every later lookup.
        fixture.expire_spotify_backoff(self.entries)
        self.calls.clear()
        self.statuses = {"/spotify/artists/" + sid("R", 3): 429}
        with self.assertRaisesRegex(HomeAssistantError, "rate limited"):
            await self.call("recent_contexts")
        self.assertEqual(
            [path for path, _ in self.reads()][-1], "/spotify/artists/" + sid("R", 3)
        )
        self.assertEqual(len(self.reads()), 3)
        self.assertEqual(self._writes(), [])

    async def test_provider_errors_malformed_and_oversized_fail_closed(self):
        for status in (302, 401, 403, 500):
            with self.subTest(status=status):
                self.calls.clear()
                self.statuses = {"/spotify/me/albums": status}
                with self.assertRaisesRegex(HomeAssistantError, "unavailable") as err:
                    await self.call("saved_albums")
                self.assertNotIn("PRIVATE", str(err.exception))
                self.assertEqual(len(self.reads()), 1)
        self.statuses = {}
        for path, service, body in (
            ("/spotify/me/top/artists", "top_artists", page([{"type": "track"}])),
            ("/spotify/me/top/artists", "top_artists", page([artist(0)] * 2)),
            ("/spotify/me/albums", "saved_albums", {"items": "x"}),
            ("/spotify/me/shows", "saved_shows", page([{"show": {"type": "show"}}])),
            ("/spotify/me/top/artists", "top_artists", page([artist(0)] * 21)),
            ("/spotify/me/shows", "saved_shows", {"oversized": "x" * 262145}),
            ("/spotify/me/player/recently-played", "recent_contexts", {"items": {}}),
        ):
            with self.subTest(service=service, body=str(body)[:40]):
                self.overrides = {path: body}
                with self.assertRaises(HomeAssistantError):
                    await self.call(service)
        self.assertEqual(self._writes(), [])

    async def test_owner_epoch_change_during_read_rejects(self):
        owner = self.entries[0].runtime_data
        for service, path in (
            ("top_artists", "/spotify/me/top/artists"),
            ("recent_contexts", "/spotify/artists/" + sid("R", 3)),
        ):
            with self.subTest(service=service):
                original = owner.coordinator.owner_epoch
                self.entered, self.release = asyncio.Event(), asyncio.Event()
                self.holds.append(self.release)
                self.hold_path = path
                pending = self._task(self.call(service))
                await self.entered.wait()
                owner.coordinator.owner_epoch = "replaced-owner"
                self.release.set()
                with self.assertRaisesRegex(HomeAssistantError, "owner replaced"):
                    await pending
                owner.coordinator.owner_epoch = original
                self.hold_path = None
        # A change before the first send means nothing is sent at all.
        self.calls.clear()

        async def replace_after_oauth():
            owner.coordinator.owner_epoch = "new-oauth-owner"

        with (
            patch.object(
                owner.api._session, "async_ensure_token_valid", replace_after_oauth
            ),
            self.assertRaises(HomeAssistantError),
        ):
            await self.call("saved_shows")
        self.assertEqual(self.reads(), [])

    async def test_search_never_asks_spotify_for_more_than_ten(self):
        self.overrides = {
            "/spotify/search": {"tracks": {"items": [], "total": 0}},
        }
        self.calls.clear()
        await self.hass.services.async_call(
            "podconnect",
            "search_tracks",
            {"config_entry_id": "account-alpha", "query": "jazz", "limit": 20},
            blocking=True,
            return_response=True,
        )
        self.overrides = {
            "/spotify/search": {"playlists": {"items": []}},
        }
        await self.hass.services.async_call(
            "podconnect",
            "search_playlists",
            {"config_entry_id": "account-alpha", "query": "jazz", "limit": 15},
            blocking=True,
            return_response=True,
        )
        self.assertEqual(
            [
                query["limit"]
                for path, query in self.reads()
                if path == "/spotify/search"
            ],
            ["10", "10"],
        )
