"""Minimal async client for the PodConnect add-on's manager HTTP API.

The add-on runs with host_network, so its Go manager is reachable at the HA host's
LAN IP on :8099. This client wraps the account-agnostic local controls (state, stop,
release, play, volume) the manager exposes.
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
        json: dict[str, Any] | None = None,
    ) -> Any:
        """Make a request; return parsed JSON (or None) or raise on non-2xx."""
        url = f"{self._base_url}{path}"
        try:
            async with self._web.request(
                method, url, json=json, timeout=_TIMEOUT
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

    async def state(self) -> dict[str, Any]:
        """GET /api/state — current speaker state."""
        return await self._request("GET", "/api/state") or {}

    async def stop(self) -> None:
        """POST /api/stop — account-agnostic local pause."""
        await self._request("POST", "/api/stop")

    async def release(self) -> None:
        """POST /api/release — free the HomePod for other AirPlay apps."""
        await self._request("POST", "/api/release")

    async def play(self) -> None:
        """POST /api/play — resume playback."""
        await self._request("POST", "/api/play")

    async def set_volume(self, pct: int) -> None:
        """PUT /api/volume — set the speaker volume (0..100)."""
        await self._request("PUT", "/api/volume", json={"volume": int(pct)})
