"""Spotify playlist covers stay optional, canonical, and provider-metadata free."""

import ast
import importlib.util
import json
import re
import unittest
from pathlib import Path

ROOT = Path(__file__).parents[1] / "custom_components/podconnect"
PLAYLIST_URI = "spotify:playlist:" + "A" * 22
VALID_URL = "https://i.scdn.co/image/cover-id"


def _artwork_module():
    spec = importlib.util.spec_from_file_location(
        "podconnect_playlist_artwork", ROOT / "artwork.py"
    )
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


def _project_playlists(artwork):
    source = (ROOT / "playlist_catalogue.py").read_text()
    tree = ast.parse(source)
    function = next(
        node
        for node in tree.body
        if isinstance(node, ast.FunctionDef) and node.name == "project_playlists"
    )
    module = ast.Module(body=[function], type_ignores=[])
    namespace = {
        "json": json,
        "PLAYLIST": re.compile(r"^spotify:playlist:[A-Za-z0-9]{22}$"),
        "HomeAssistantError": ValueError,
        "spotify_playlist_artwork": artwork.spotify_playlist_artwork,
    }
    exec(  # noqa: S102 - execute the production projection function in an isolated test namespace
        compile(
            ast.fix_missing_locations(module),
            str(ROOT / "playlist_catalogue.py"),
            "exec",
        ),
        namespace,
    )
    return namespace["project_playlists"]


def _raw_playlist(images):
    return {
        "playlists": {
            "items": [
                {
                    "type": "playlist",
                    "uri": PLAYLIST_URI,
                    "name": "Actual playlist",
                    "images": images,
                    "owner": {"display_name": "Private owner"},
                }
            ]
        }
    }


def test_default_projection_keeps_legacy_rows_for_podvoice64():
    project = _project_playlists(_artwork_module())
    result = project(
        _raw_playlist([{"url": VALID_URL}]), "entry-a", "epoch-a", "jazz", 3
    )
    assert result["playlists"] == [{"uri": PLAYLIST_URI, "name": "Actual playlist"}]


def test_home_assistant_service_schema_advertises_optional_opt_in():
    source = (ROOT / "services.yaml").read_text()
    block = source.split("search_playlists:", 1)[1].split("\n\n", 1)[0]
    assert "include_artwork:" in block
    assert "default: false" in block


def test_opted_in_projection_emits_first_valid_canonical_image_only():
    project = _project_playlists(_artwork_module())
    result = project(
        _raw_playlist(
            [
                {"url": "https://attacker.example/image/wrong"},
                {"url": VALID_URL, "width": 640, "token": "SECRET"},
                {"url": "https://i.scdn.co/image/second"},
            ]
        ),
        "entry-a",
        "epoch-a",
        "jazz",
        3,
        include_artwork=True,
    )

    assert result["playlists"] == [
        {
            "uri": PLAYLIST_URI,
            "name": "Actual playlist",
            "artwork": {
                "kind": "playlist_cover",
                "provider": "spotify",
                "url": VALID_URL,
            },
        }
    ]
    assert (
        result["config_entry_id"],
        result["owner_epoch"],
        result["query"],
        result["limit"],
    ) == ("entry-a", "epoch-a", "jazz", 3)
    assert result["returned_count"] == 1
    assert "SECRET" not in json.dumps(result)
    assert "Private owner" not in json.dumps(result)


def test_invalid_images_leave_playlist_valid_without_cover():
    project = _project_playlists(_artwork_module())
    for url in (
        "HTTPS://i.scdn.co/image/upper-scheme",
        "https://I.SCDN.CO/image/upper-host",
        "https://i.scdn.co:443/image/port",
        "https://user@i.scdn.co/image/credentials",
        "https://i.scdn.co/image/cover?token=secret",
        "https://i.scdn.co/image/../cover",
        "https://i.scdn.co/image/cover#fragment",
        "https://foreign.invalid/image/cover",
    ):
        result = project(
            _raw_playlist([{"url": url}]),
            "entry-a",
            "epoch-a",
            "jazz",
            3,
            include_artwork=True,
        )
        assert result["playlists"] == [{"uri": PLAYLIST_URI, "name": "Actual playlist"}]

    missing = project(
        _raw_playlist([]), "entry-a", "epoch-a", "jazz", 3, include_artwork=True
    )
    assert missing["playlists"] == [{"uri": PLAYLIST_URI, "name": "Actual playlist"}]

    oversized = project(
        _raw_playlist([{"url": VALID_URL}] * 21),
        "entry-a",
        "epoch-a",
        "jazz",
        3,
        include_artwork=True,
    )
    assert oversized["playlists"] == [{"uri": PLAYLIST_URI, "name": "Actual playlist"}]


class TestPlaylistCoverProjection(unittest.TestCase):
    def test_control_compatibility_contract(self):
        project = _project_playlists(_artwork_module())
        legacy = project(
            _raw_playlist([{"url": VALID_URL}]), "entry-a", "epoch-a", "jazz", 3
        )
        self.assertEqual(
            legacy["playlists"], [{"uri": PLAYLIST_URI, "name": "Actual playlist"}]
        )

        opted_in = project(
            _raw_playlist([{"url": VALID_URL}]),
            "entry-a",
            "epoch-a",
            "jazz",
            3,
            include_artwork=True,
        )
        self.assertEqual(
            opted_in["playlists"][0]["artwork"],
            {"kind": "playlist_cover", "provider": "spotify", "url": VALID_URL},
        )

        invalid = project(
            _raw_playlist([{"url": "https://i.scdn.co/image/cover?token=x"}]),
            "entry-a",
            "epoch-a",
            "jazz",
            3,
            include_artwork=True,
        )
        self.assertEqual(
            invalid["playlists"], [{"uri": PLAYLIST_URI, "name": "Actual playlist"}]
        )

        descriptor = (ROOT / "services.yaml").read_text()
        service = descriptor.split("search_playlists:", 1)[1].split("\n\n", 1)[0]
        self.assertIn("include_artwork:\n      required: false", service)
        self.assertIn("default: false", service)
