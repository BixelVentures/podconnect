"""Complete first-page projection: no invented playlists or partial valid prefix."""

import importlib.util
import json
import re
import unittest
from urllib.parse import parse_qs, urlsplit

from test_control_errors import HomeAssistantError
from test_search_library_truth import ROOT, methods


def page(count=1, *, limit=20, more=False):
    return {
        "items": [
            {
                "type": "playlist",
                "uri": "spotify:playlist:" + str(i).zfill(22),
                "name": f"Real playlist {i}",
                "images": [{"url": "https://i.scdn.co/image/real"}],
                "owner": {"id": "PRIVATE"},
                "access_token": "SECRET",
            }
            for i in range(count)
        ],
        "offset": 0,
        "limit": limit,
        "total": count + int(more),
        "next": f"https://api.spotify.com/v1/me/playlists?offset={limit}&limit={limit}"
        if more
        else None,
    }


def projector():
    spec = importlib.util.spec_from_file_location(
        "playlist_artwork", ROOT / "artwork.py"
    )
    artwork = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(artwork)
    return methods(
        "playlist_catalogue.py",
        ["project_user_playlists", "_user_playlist_limit"],
        {
            "PLAYLIST": re.compile(r"^spotify:playlist:[A-Za-z0-9]{22}$"),
            "spotify_playlist_artwork": artwork.spotify_playlist_artwork,
            "urlsplit": urlsplit,
            "parse_qs": parse_qs,
        },
    )


class UserPlaylistProjectionTests(unittest.TestCase):
    def test_real_first_page_empty_and_more_are_closed(self):
        project = projector()["project_user_playlists"]
        for raw in (page(0), page(1), page(20, more=True)):
            reply = project(raw, "entry-a", "epoch-a", 20)
            self.assertEqual(
                set(reply),
                {
                    "ok",
                    "schema",
                    "config_entry_id",
                    "owner_epoch",
                    "source",
                    "coverage",
                    "limit",
                    "returned_count",
                    "source_has_more",
                    "playlists",
                },
            )
            self.assertEqual(reply["schema"], "podconnect.user_playlists.v1")
            self.assertEqual(reply["coverage"], "first_page")
            self.assertEqual(reply["source_has_more"], raw["next"] is not None)
            self.assertEqual(reply["returned_count"], len(raw["items"]))
            self.assertNotIn("SECRET", json.dumps(reply))
            self.assertNotIn("PRIVATE", json.dumps(reply))
            for row in reply["playlists"]:
                self.assertEqual(set(row), {"uri", "name", "artwork"})
                self.assertEqual(row["artwork"]["kind"], "playlist_cover")

    def test_invalid_final_row_and_paging_fail_whole_page(self):
        project = projector()["project_user_playlists"]
        variants = [page(21), page(1, more=True)]
        for key, value in (
            ("total", True),
            ("total", -1),
            ("offset", True),
            ("offset", 1),
            ("limit", 19),
            ("next", "https://evil.invalid/?secret=x"),
        ):
            raw = page()
            raw[key] = value
            variants.append(raw)
        for bad in (
            None,
            {},
            {"type": "playlist", "uri": "spotify:track:" + "A" * 22, "name": "Bad"},
        ):
            raw = page(20)
            raw["items"][-1] = bad
            variants.append(raw)
        raw = page(2)
        raw["items"][-1] = raw["items"][0]
        variants.append(raw)
        for raw in variants:
            with self.subTest(raw=raw), self.assertRaises(HomeAssistantError):
                project(raw, "entry-a", "epoch-a", 20)

    def test_limit_rejects_coercion_and_invalid_artwork_is_omitted(self):
        ns = projector()
        for limit in (True, False, 0, 21, "20", None):
            with self.assertRaises(ValueError):
                ns["_user_playlist_limit"](limit)
        raw = page()
        raw["items"][0]["images"] = [
            {"url": "https://i.scdn.co/image/real?token=SECRET"}
        ]
        reply = ns["project_user_playlists"](raw, "entry-a", "epoch-a", 20)
        self.assertNotIn("artwork", reply["playlists"][0])
