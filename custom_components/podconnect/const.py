"""Constants for the PodConnect integration."""

from __future__ import annotations

import logging

DOMAIN = "podconnect"
LOGGER = logging.getLogger(__package__)

SPOTIFY_API = "https://api.spotify.com/v1"

# OAuth scopes: playback state + control + playlists, plus profile insights (top/recent/liked)
# so Assist and Browse can suggest music from the user's own taste.
# (Sent comma-joined in the authorize URL to match Spotify, see config_flow.)
# NOTE: adding scopes requires existing users to re-authorize (reauth / remove + re-add).
SPOTIFY_SCOPES = [
    "user-read-playback-state",
    "user-modify-playback-state",
    "user-read-currently-playing",
    "user-read-private",
    "playlist-read-private",
    "playlist-read-collaborative",
    "user-top-read",
    "user-read-recently-played",
    "user-library-read",
]

# How often to poll Spotify's Web API for playback state + devices.
# Gentle on dev-mode rate limits; HomePod push-state (go-librespot events) lands in a later phase.
POLL_INTERVAL_SECONDS = 10

# Optional local-speaker (add-on manager) integration. Set the manager base URL in the
# integration's options to surface account-agnostic local speaker entities + a Release button.
# Empty / unset = Control works purely as Spotify control with no add-on dependency.
CONF_BASE_URL = "base_url"
DEFAULT_BASE_URL = "http://homeassistant.local:8099"

# How often to poll the add-on's manager HTTP API for per-room speaker state.
# Local LAN call, so a tight interval keeps the dashboard / Assist state responsive.
SPEAKERS_POLL_INTERVAL_SECONDS = 5
