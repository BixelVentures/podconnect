"""Minimal async Spotify Web API client for PodConnect.

Self-contained (no third-party Spotify library): backed by HA's OAuth2Session for
token handling, talks to the Web API with the shared aiohttp client session.
"""

from __future__ import annotations

import json as json_module
from collections.abc import Awaitable, Callable
from typing import Any

from aiohttp import ClientSession
from homeassistant.const import CONF_ACCESS_TOKEN
from homeassistant.core import HomeAssistant
from homeassistant.helpers.aiohttp_client import async_get_clientsession
from homeassistant.helpers.config_entry_oauth2_flow import OAuth2Session

from .const import SPOTIFY_API


class SpotifyApiError(Exception):
    """Raised when a Spotify Web API request fails."""

    def __init__(
        self, message: str, *, status: int | None = None, retry_after: int | None = None
    ):
        super().__init__(message)
        self.status = status
        self.retry_after = retry_after


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
        max_bytes: int | None = None,
        playback_response: bool = False,
        execution_guard: Callable[[], bool] | None = None,
        async_execution_guard: Callable[[], Awaitable[None]] | None = None,
        on_send: Callable[[], None] | None = None,
    ) -> Any:
        """Make an authenticated request; returns parsed JSON or None."""
        await self._session.async_ensure_token_valid()
        token = self._session.token[CONF_ACCESS_TOKEN]
        # Drop None-valued query params (Spotify rejects empty device_id).
        if params:
            params = {k: v for k, v in params.items() if v is not None}
        if execution_guard is not None and not execution_guard():
            raise SpotifyApiError("Command admission expired")
        if async_execution_guard is not None:
            await async_execution_guard()
        if execution_guard is not None and not execution_guard():
            raise SpotifyApiError("Command admission expired")
        if on_send is not None:
            on_send()
        async with self._web.request(
            method,
            f"{SPOTIFY_API}{path}",
            headers={"Authorization": f"Bearer {token}"},
            params=params or None,
            json=json,
            **(
                {"allow_redirects": False}
                if execution_guard is not None or async_execution_guard is not None
                else {}
            ),
        ) as resp:
            if execution_guard is not None and resp.status != 204:
                raise SpotifyApiError(
                    "Spotify command did not return acceptance", status=resp.status
                )
            if max_bytes is not None:
                if resp.status != 200:
                    retry_after = None
                    if resp.status == 429:
                        header = resp.headers.get("Retry-After", "")
                        retry_after = (
                            max(10, min(3600, int(header)))
                            if header.isascii()
                            and header.isdigit()
                            and len(header) <= 6
                            else 60
                        )
                    raise SpotifyApiError(
                        "Spotify queue read failed",
                        status=resp.status,
                        retry_after=retry_after,
                    )
                if resp.content_type != "application/json":
                    raise SpotifyApiError("Invalid queue content type")
                raw = bytearray()
                async for chunk in resp.content.iter_chunked(8192):
                    raw.extend(chunk)
                    if len(raw) > max_bytes:
                        raise SpotifyApiError("Queue response exceeds budget")
                return json_module.loads(raw)
            if resp.status == 204:
                return None
            if resp.status == 429:
                raise SpotifyApiError(
                    f"rate limited (retry-after={resp.headers.get('Retry-After')}s)"
                )
            if resp.status >= 400:
                raise SpotifyApiError(
                    f"{method} {path} -> {resp.status}: {await resp.text()}"
                )
            if playback_response:
                if resp.status != 200 or resp.content_type != "application/json":
                    raise SpotifyApiError("Invalid playback response")
                try:
                    playback = await resp.json()
                except (ValueError, UnicodeDecodeError) as err:
                    raise SpotifyApiError("Invalid playback response") from err
                if not isinstance(playback, dict):
                    raise SpotifyApiError("Invalid playback response")
                return playback
            if resp.content_type == "application/json":
                return await resp.json()
            return None

    # --- read ---
    async def current_user(self) -> dict[str, Any]:
        """Bounded GET /me through this entry's existing OAuth session."""
        return await self._request("GET", "/me", max_bytes=16384)

    async def playback_state(self) -> dict[str, Any] | None:
        """GET /me/player (None when nothing is playing)."""
        return await self._request("GET", "/me/player", playback_response=True)

    async def queue(self) -> dict[str, Any]:
        """Bounded account-wide queue read; credentials never leave this owner."""
        return await self._request("GET", "/me/player/queue", max_bytes=262144)

    async def devices(self) -> list[dict[str, Any]]:
        """GET /me/player/devices."""
        data = await self._request("GET", "/me/player/devices")
        return (data or {}).get("devices", [])

    async def playlists(self, limit: int = 50) -> list[dict[str, Any]]:
        """GET /me/playlists — the user's playlists."""
        data = await self._request("GET", "/me/playlists", params={"limit": limit})
        return (data or {}).get("items", [])

    async def user_playlists(
        self,
        limit: int,
        *,
        owner_guard: Callable[[], Awaitable[None]],
    ) -> dict[str, Any]:
        """Read one bounded first page through this entry's existing OAuth owner."""
        if type(limit) is not int or not 1 <= limit <= 20:
            raise SpotifyApiError("Invalid playlist limit")
        return await self._request(
            "GET",
            "/me/playlists",
            params={"limit": limit, "offset": 0},
            max_bytes=262144,
            async_execution_guard=owner_guard,
        )

    async def search(
        self,
        query: str,
        types: str = "track,artist,album,playlist,show,episode,audiobook",
        limit: int = 8,
    ) -> dict[str, Any]:
        """GET /search — returns {tracks,artists,albums,playlists: {items:[...]}}."""
        return (
            await self._request(
                "GET", "/search", params={"q": query, "type": types, "limit": limit}
            )
            or {}
        )

    async def top_artists(self, limit: int = 20) -> list[dict[str, Any]]:
        """GET /me/top/artists (needs user-top-read)."""
        data = await self._request("GET", "/me/top/artists", params={"limit": limit})
        return (data or {}).get("items", [])

    async def top_tracks(self, limit: int = 20) -> list[dict[str, Any]]:
        """GET /me/top/tracks (needs user-top-read)."""
        data = await self._request("GET", "/me/top/tracks", params={"limit": limit})
        return (data or {}).get("items", [])

    async def recently_played(self, limit: int = 25) -> list[dict[str, Any]]:
        """GET /me/player/recently-played → de-duplicated track objects (needs user-read-recently-played)."""
        data = await self._request(
            "GET", "/me/player/recently-played", params={"limit": limit}
        )
        out: list[dict[str, Any]] = []
        seen: set[str] = set()
        for entry in (data or {}).get("items", []):
            track = entry.get("track") or {}
            uri = track.get("uri")
            if uri and uri not in seen:
                seen.add(uri)
                out.append(track)
        return out

    async def saved_tracks(self, limit: int = 50) -> list[dict[str, Any]]:
        """GET /me/tracks → the user's Liked Songs as track objects (needs user-library-read)."""
        data = await self._request("GET", "/me/tracks", params={"limit": limit})
        return [entry.get("track") or {} for entry in (data or {}).get("items", [])]

    # --- control (device_id targets a specific Connect device) ---
    async def play(
        self,
        device_id: str | None = None,
        *,
        context_uri: str | None = None,
        uris: list[str] | None = None,
        position_ms: int | None = None,
        execution_guard: Callable[[], bool] | None = None,
        async_execution_guard: Callable[[], Awaitable[None]] | None = None,
        on_send: Callable[[], None] | None = None,
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
            "PUT",
            "/me/player/play",
            params={"device_id": device_id},
            json=body or None,
            **(
                {
                    "execution_guard": execution_guard,
                    "async_execution_guard": async_execution_guard,
                    "on_send": on_send,
                }
                if execution_guard is not None
                else {}
            ),
        )

    async def pause(
        self,
        device_id: str | None = None,
        *,
        execution_guard: Callable[[], bool] | None = None,
        on_send: Callable[[], None] | None = None,
    ) -> None:
        """PUT /me/player/pause."""
        await self._request(
            "PUT",
            "/me/player/pause",
            params={"device_id": device_id},
            **(
                {"execution_guard": execution_guard, "on_send": on_send}
                if execution_guard is not None
                else {}
            ),
        )

    async def next(
        self,
        device_id: str | None = None,
        *,
        execution_guard: Callable[[], bool] | None = None,
        on_send: Callable[[], None] | None = None,
    ) -> None:
        """POST /me/player/next."""
        await self._request(
            "POST",
            "/me/player/next",
            params={"device_id": device_id},
            **(
                {"execution_guard": execution_guard, "on_send": on_send}
                if execution_guard is not None
                else {}
            ),
        )

    async def previous(
        self,
        device_id: str | None = None,
        *,
        execution_guard: Callable[[], bool] | None = None,
        on_send: Callable[[], None] | None = None,
    ) -> None:
        """POST /me/player/previous."""
        await self._request(
            "POST",
            "/me/player/previous",
            params={"device_id": device_id},
            **(
                {"execution_guard": execution_guard, "on_send": on_send}
                if execution_guard is not None
                else {}
            ),
        )

    async def seek(self, position_ms: int, device_id: str | None = None) -> None:
        """PUT /me/player/seek."""
        await self._request(
            "PUT",
            "/me/player/seek",
            params={"position_ms": int(position_ms), "device_id": device_id},
        )

    async def set_volume(
        self,
        volume_percent: int,
        device_id: str | None = None,
        *,
        execution_guard: Callable[[], bool] | None = None,
        on_send: Callable[[], None] | None = None,
    ) -> None:
        """PUT /me/player/volume (0-100)."""
        await self._request(
            "PUT",
            "/me/player/volume",
            params={"volume_percent": int(volume_percent), "device_id": device_id},
            **(
                {"execution_guard": execution_guard, "on_send": on_send}
                if execution_guard is not None
                else {}
            ),
        )

    async def set_repeat(self, state: str, device_id: str | None = None) -> None:
        """PUT /me/player/repeat (state = track | context | off)."""
        await self._request(
            "PUT", "/me/player/repeat", params={"state": state, "device_id": device_id}
        )

    async def set_shuffle(self, state: bool, device_id: str | None = None) -> None:
        """PUT /me/player/shuffle (state = true | false)."""
        await self._request(
            "PUT",
            "/me/player/shuffle",
            params={"state": "true" if state else "false", "device_id": device_id},
        )

    async def transfer(
        self,
        device_id: str,
        play: bool = True,
        *,
        execution_guard: Callable[[], bool] | None = None,
        async_execution_guard: Callable[[], Awaitable[None]] | None = None,
    ) -> None:
        """PUT /me/player — move playback to a device."""
        await self._request(
            "PUT",
            "/me/player",
            json={"device_ids": [device_id], "play": play},
            **(
                {
                    "execution_guard": execution_guard,
                    "async_execution_guard": async_execution_guard,
                }
                if execution_guard is not None
                else {}
            ),
        )
