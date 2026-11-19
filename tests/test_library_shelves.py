"""Find musik shelf projections: closed, bounded, nothing invented (no HA needed)."""

import importlib.util
import json
import re
import unittest

from test_control_errors import HomeAssistantError
from test_search_library_truth import ROOT, methods

IMAGE = "https://i.scdn.co/image/ab67616d0000b2730123456789abcdef"


def shelves():
    spec = importlib.util.spec_from_file_location("shelf_artwork", ROOT / "artwork.py")
    artwork = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(artwork)
    names = [
        "_malformed",
        "_name",
        "_object",
        "_artist_text",
        "_page_rows",
        "_bounded",
        "_envelope",
        "_unique",
        "project_top_artists",
        "project_saved_albums",
        "project_saved_shows",
        "recent_context_candidates",
        "resolved_context",
        "project_recent_contexts",
    ]
    return methods(
        "library_shelves.py",
        names,
        {
            "spotify_image_artwork": artwork.spotify_image_artwork,
            "spotify_playlist_artwork": artwork.spotify_playlist_artwork,
            "REPLY_MAX_BYTES": 65536,
            "RECENT_PLAYS_READ": 50,
            "CONTEXT_KINDS": ("playlist", "album", "artist", "show"),
            "_ID": re.compile(r"^[A-Za-z0-9]{22}$"),
            "_PLAYED_AT": re.compile(
                r"^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:\.\d{1,6})?Z$"
            ),
        },
    )


def obj(kind, index, **extra):
    ident = str(index).zfill(22)
    return {
        "type": kind,
        "id": ident,
        "uri": f"spotify:{kind}:{ident}",
        "name": f"{kind} {index}",
        "images": [{"url": IMAGE, "token": "SECRET"}],
        "access_token": "SECRET",
        **extra,
    }


def page(rows, next_page=None):
    return {
        "items": rows,
        "offset": 0,
        "limit": 20,
        "total": len(rows),
        "next": next_page,
    }


class LibraryShelfProjectionTests(unittest.TestCase):
    def setUp(self):
        self.ns = shelves()

    def test_top_artists_closed_with_optional_artist_image(self):
        rows = [obj("artist", 1), obj("artist", 2, images=[{"url": "http://x/y"}])]
        reply = self.ns["project_top_artists"](page(rows), "e", "ep", 20, "long_term")
        self.assertEqual(reply["schema"], "podconnect.top_artists.v1")
        self.assertEqual(set(reply["artists"][0]), {"id", "uri", "name", "artwork"})
        self.assertEqual(reply["artists"][0]["artwork"]["kind"], "artist_image")
        self.assertNotIn("artwork", reply["artists"][1])
        self.assertNotIn("SECRET", json.dumps(reply))
        for bad in (
            page([obj("track", 1)]),
            page([{**obj("artist", 1), "uri": "spotify:artist:" + "Z" * 22}]),
            page([obj("artist", 1), obj("artist", 1)]),
            page([obj("artist", i) for i in range(21)]),
            {"items": None},
        ):
            with self.assertRaises(HomeAssistantError):
                self.ns["project_top_artists"](bad, "e", "ep", 20, "medium_term")

    def test_saved_albums_need_real_artists_and_report_more(self):
        album = obj("album", 3, artists=[{"name": "A"}, {"name": "B"}])
        reply = self.ns["project_saved_albums"](
            page(
                [{"album": album}, None],
                next_page="https://api.spotify.com/v1/me/albums?offset=20",
            ),
            "e",
            "ep",
            20,
        )
        self.assertEqual(reply["albums"][0]["artist"], "A, B")
        self.assertEqual(reply["albums"][0]["artwork"]["kind"], "album_cover")
        self.assertEqual((reply["returned_count"], reply["source_has_more"]), (1, True))
        with self.assertRaises(HomeAssistantError):
            self.ns["project_saved_albums"](
                page([{"album": obj("album", 3, artists=[])}]), "e", "ep", 20
            )

    def test_saved_shows_publisher_is_null_unless_sent(self):
        reply = self.ns["project_saved_shows"](
            page([{"show": obj("show", 1)}, {"show": obj("show", 2, publisher="P")}]),
            "e",
            "ep",
            20,
        )
        self.assertEqual([s["publisher"] for s in reply["shows"]], [None, "P"])
        self.assertEqual(reply["shows"][0]["artwork"]["kind"], "show_cover")

    def test_recent_candidates_distinct_bounded_and_skip_contextless(self):
        def play(kind, index):
            return {
                "played_at": "2026-11-19T07:00:00Z",
                "context": {"type": kind, "uri": obj(kind, index)["uri"]},
            }

        rows = [
            play("playlist", 1),
            {"context": None},
            play("playlist", 1),
            play("album", 2),
            {"context": {"type": "collection", "uri": "spotify:user:x:collection"}},
            {"context": {"type": "album", "uri": "spotify:album:short"}},
            play("artist", 3),
            play("show", 4),
        ]
        candidates, scanned = self.ns["recent_context_candidates"]({"items": rows}, 3)
        self.assertEqual(scanned, 8)
        self.assertEqual(
            [c["type"] for c in candidates], ["playlist", "album", "artist"]
        )
        with self.assertRaises(HomeAssistantError):
            self.ns["recent_context_candidates"]({"items": [{}] * 51}, 12)
        candidate = candidates[2]
        self.assertIsNone(self.ns["resolved_context"](candidate, obj("artist", 9)))
        found = self.ns["resolved_context"](candidate, obj("artist", 3))
        self.assertEqual(
            set(found), {"type", "uri", "name", "artwork", "last_played_at"}
        )
        reply = self.ns["project_recent_contexts"]([found], "e", "ep", 3, 8, 2)
        self.assertEqual(reply["schema"], "podconnect.recent_contexts.v1")
        self.assertEqual((reply["returned_count"], reply["dropped_count"]), (1, 2))


if __name__ == "__main__":
    unittest.main()
