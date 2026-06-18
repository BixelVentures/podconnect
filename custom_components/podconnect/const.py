"""Constants for the PodConnect integration."""

from __future__ import annotations

import logging

DOMAIN = "podconnect"
LOGGER = logging.getLogger(__package__)

SPOTIFY_API = "https://api.spotify.com/v1"

# OAuth scopes needed to read playback state + control playback + read playlists.
# (Sent comma-joined in the authorize URL to match Spotify, see config_flow.)
SPOTIFY_SCOPES = [
    "user-read-playback-state",
    "user-modify-playback-state",
    "user-read-currently-playing",
    "user-read-private",
    "playlist-read-private",
    "playlist-read-collaborative",
]

# How often to poll Spotify's Web API for playback state + devices.
# Gentle on dev-mode rate limits; HomePod push-state (go-librespot events) lands in a later phase.
POLL_INTERVAL_SECONDS = 10
