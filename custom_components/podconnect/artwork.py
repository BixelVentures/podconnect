"""Strict, optional Spotify album-art projection for passive read contracts."""

from __future__ import annotations

import re
from urllib.parse import urlsplit

ARTWORK_HOST = "i.scdn.co"
ARTWORK_MAX_URL_LENGTH = 512
_ARTWORK_PATH = re.compile(r"^/image/[A-Za-z0-9_-]{1,128}$")


def _allowed_spotify_image_url(value: object, *, canonical: bool = False) -> str | None:
    if not isinstance(value, str) or not 1 <= len(value) <= ARTWORK_MAX_URL_LENGTH:
        return None
    try:
        parsed = urlsplit(value)
        if (
            parsed.scheme != "https"
            or parsed.hostname != ARTWORK_HOST
            or parsed.username is not None
            or parsed.password is not None
            or parsed.port is not None
            or parsed.query
            or parsed.fragment
            or not _ARTWORK_PATH.fullmatch(parsed.path)
            or (canonical and value != f"https://{ARTWORK_HOST}{parsed.path}")
        ):
            return None
    except ValueError:
        return None
    return value


def validate_spotify_artwork(value: object) -> dict | None:
    """Validate the small public artwork value, never forwarding provider objects."""
    if not isinstance(value, dict) or set(value) != {"kind", "provider", "url"}:
        return None
    if value.get("kind") != "album_cover" or value.get("provider") != "spotify":
        return None
    url = _allowed_spotify_image_url(value.get("url"))
    if url is None:
        return None
    return {"kind": "album_cover", "provider": "spotify", "url": url}


def spotify_album_artwork(track: object) -> dict | None:
    """Project one valid first Spotify album image from a raw track object."""
    if not isinstance(track, dict):
        return None
    album = track.get("album")
    images = album.get("images") if isinstance(album, dict) else None
    if not isinstance(images, list) or len(images) > 20:
        return None
    for image in images:
        if not isinstance(image, dict):
            continue
        artwork = validate_spotify_artwork(
            {"kind": "album_cover", "provider": "spotify", "url": image.get("url")}
        )
        if artwork is not None:
            return artwork
    return None


def spotify_playlist_artwork(playlist: object) -> dict | None:
    """Project the first canonical Spotify playlist image, omitting provider metadata."""
    if not isinstance(playlist, dict):
        return None
    images = playlist.get("images")
    if not isinstance(images, list) or len(images) > 20:
        return None
    for image in images:
        if not isinstance(image, dict):
            continue
        url = _allowed_spotify_image_url(image.get("url"), canonical=True)
        if url is not None:
            return {"kind": "playlist_cover", "provider": "spotify", "url": url}
    return None
