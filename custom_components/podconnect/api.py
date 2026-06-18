"""Minimal async Spotify Web API client for PodConnect.

Self-contained (no third-party Spotify library): backed by HA's OAuth2Session for
token handling, talks to the Web API with the shared aiohttp client session.
"""

from __future__ import annotations

from typing import Any

from aiohttp import ClientSession

from homeassistant.const import CONF_ACCESS_TOKEN
from homeassistant.core import HomeAssistant
from homeassistant.helpers.aiohttp_client import async_get_clientsession
from homeassistant.helpers.config_entry_oauth2_flow import OAuth2Session

from .const import SPOTIFY_API


class SpotifyApiError(Exception):
    """Raised when a Spotify Web API request fails."""


class SpotifyApi:
    """Thin Spotify Web API client."""

    def __init__(self, hass: HomeAssistant, session: OAuth2Session) -> None:
        """Initialise with an OAuth2 session that owns token refresh."""
        self._session = session
        self._web: ClientSession = async_get_clientsession(hass)

    async def _request(
        self,
        method: str,
        path: str,
        *,
        params: dict[str, Any] | None = None,
        json: dict[str, Any] | None = None,
    ) -> Any:
        """Make an authenticated request; returns parsed JSON or None."""
        await self._session.async_ensure_token_valid()
        token = self._session.token[CONF_ACCESS_TOKEN]
        # Drop None-valued query params (Spotify rejects empty device_id).
        if params:
            params = {k: v for k, v in params.items() if v is not None}
        async with self._web.request(
            method,
            f"{SPOTIFY_API}{path}",
            headers={"Authorization": f"Bearer {token}"},
            params=params or None,
            json=json,
        ) as resp:
            if resp.status == 204:
                return None
            if resp.status == 429:
                raise SpotifyApiError(
                    f"rate limited (retry-after={resp.headers.get('Retry-After')}s)"
                )
            if resp.status >= 400:
                raise SpotifyApiError(f"{method} {path} -> {resp.status}: {await resp.text()}")
            if resp.content_type == "application/json":
                return await resp.json()
            return None

    # --- read ---
    async def current_user(self) -> dict[str, Any]:
        """GET /me."""
        return await self._request("GET", "/me")

    async def playback_state(self) -> dict[str, Any] | None:
        """GET /me/player (None when nothing is playing)."""
        return await self._request("GET", "/me/player")

    async def devices(self) -> list[dict[str, Any]]:
        """GET /me/player/devices."""
        data = await self._request("GET", "/me/player/devices")
        return (data or {}).get("devices", [])

    async def playlists(self, limit: int = 50) -> list[dict[str, Any]]:
        """GET /me/playlists — the user's playlists."""
        data = await self._request("GET", "/me/playlists", params={"limit": limit})
        return (data or {}).get("items", [])

    # --- control (device_id targets a specific Connect device) ---
    async def play(
        self,
        device_id: str | None = None,
        *,
        context_uri: str | None = None,
        uris: list[str] | None = None,
        position_ms: int | None = None,
    ) -> None:
        """PUT /me/player/play (resume, or start a context/uris)."""
        body: dict[str, Any] = {}
        if context_uri:
            body["context_uri"] = context_uri
        if uris:
            body["uris"] = uris
        if position_ms is not None:
            body["position_ms"] = position_ms
        await self._request(
            "PUT", "/me/player/play", params={"device_id": device_id}, json=body or None
        )

    async def pause(self, device_id: str | None = None) -> None:
        """PUT /me/player/pause."""
        await self._request("PUT", "/me/player/pause", params={"device_id": device_id})

    async def next(self, device_id: str | None = None) -> None:
        """POST /me/player/next."""
        await self._request("POST", "/me/player/next", params={"device_id": device_id})

    async def previous(self, device_id: str | None = None) -> None:
        """POST /me/player/previous."""
        await self._request("POST", "/me/player/previous", params={"device_id": device_id})

    async def seek(self, position_ms: int, device_id: str | None = None) -> None:
        """PUT /me/player/seek."""
        await self._request(
            "PUT",
            "/me/player/seek",
            params={"position_ms": int(position_ms), "device_id": device_id},
        )

    async def set_volume(self, volume_percent: int, device_id: str | None = None) -> None:
        """PUT /me/player/volume (0-100)."""
        await self._request(
            "PUT",
            "/me/player/volume",
            params={"volume_percent": int(volume_percent), "device_id": device_id},
        )

    async def transfer(self, device_id: str, play: bool = True) -> None:
        """PUT /me/player — move playback to a device."""
        await self._request(
            "PUT", "/me/player", json={"device_ids": [device_id], "play": play}
        )
