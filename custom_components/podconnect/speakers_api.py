"""Minimal async client for the PodConnect add-on's manager HTTP API.

The add-on runs with host_network, so its Go manager is reachable at the HA host's
LAN IP on :8099. This client wraps the account-agnostic local controls (state, rooms,
stop, release, play, volume) the manager exposes. All transport/volume ops accept an
optional room id, appended as ``?room=<id>`` so a single room can be targeted (the
manager defaults to r0 for single ops, all rooms otherwise).
"""

from __future__ import annotations

from typing import Any

import aiohttp
from aiohttp import ClientSession

from homeassistant.core import HomeAssistant
from homeassistant.helpers.aiohttp_client import async_get_clientsession

# Short timeouts: this is a local LAN call. If the add-on is down we want to fail
# fast and surface UpdateFailed rather than stall the coordinator.
_TIMEOUT = aiohttp.ClientTimeout(total=10)


class SpeakersApiError(Exception):
    """Raised when a manager HTTP API request fails."""


class SpeakersApi:
    """Thin async client for the add-on manager API."""

    def __init__(self, hass: HomeAssistant, base_url: str) -> None:
        """Initialise with the manager base URL (e.g. http://host:8099)."""
        self._base_url = base_url.rstrip("/")
        self._web: ClientSession = async_get_clientsession(hass)

    async def _request(
        self,
        method: str,
        path: str,
        *,
        params: dict[str, Any] | None = None,
        json: dict[str, Any] | None = None,
    ) -> Any:
        """Make a request; return parsed JSON (or None) or raise on non-2xx."""
        url = f"{self._base_url}{path}"
        try:
            async with self._web.request(
                method, url, params=params or None, json=json, timeout=_TIMEOUT
            ) as resp:
                if resp.status < 200 or resp.status >= 300:
                    raise SpeakersApiError(
                        f"{method} {path} -> {resp.status}: {await resp.text()}"
                    )
                if resp.content_type == "application/json":
                    return await resp.json()
                return None
        except aiohttp.ClientError as err:
            raise SpeakersApiError(f"{method} {path} failed: {err}") from err

    @staticmethod
    def _room_params(room: str | None) -> dict[str, Any] | None:
        """Build the ``?room=<id>`` query param when a room is given."""
        return {"room": room} if room else None

    async def state(self) -> dict[str, Any]:
        """GET /api/state — current state of the primary room (r0)."""
        return await self._request("GET", "/api/state") or {}

    async def rooms(self) -> list[dict[str, Any]]:
        """GET /api/rooms — one status row per speaker.

        Falls back to wrapping ``state()`` as a single pseudo-room with id "r0" when an
        older add-on (pre multi-room) 404s on this endpoint.
        """
        try:
            data = await self._request("GET", "/api/rooms")
        except SpeakersApiError as err:
            if "-> 404" in str(err):
                state = await self.state()
                return [
                    {
                        "id": "r0",
                        "name": state.get("speaker") or "PodConnect Speaker",
                        "homepod_name": state.get("homepod_name"),
                        "owntone_up": state.get("owntone_up", False),
                        "playing": state.get("playing", False),
                        "released": state.get("released", False),
                        "now_playing": state.get("now_playing"),
                        "volume": state.get("volume", -1),
                    }
                ]
            raise
        return (data or {}).get("rooms", [])

    async def stop(self, room: str | None = None) -> None:
        """POST /api/stop — account-agnostic local pause (optionally one room)."""
        await self._request("POST", "/api/stop", params=self._room_params(room))

    async def release(self, room: str | None = None) -> None:
        """POST /api/release — free the HomePod for other AirPlay apps (one room)."""
        await self._request("POST", "/api/release", params=self._room_params(room))

    async def play(self, room: str | None = None) -> None:
        """POST /api/play — resume playback (optionally one room)."""
        await self._request("POST", "/api/play", params=self._room_params(room))

    async def set_volume(self, pct: int, room: str | None = None) -> None:
        """PUT /api/volume — set the speaker volume (0..100, optionally one room)."""
        await self._request(
            "PUT",
            "/api/volume",
            params=self._room_params(room),
            json={"volume": int(pct)},
        )
