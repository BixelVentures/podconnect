"""Bounded local Speakers requests, separate from the Spotify OAuth client."""

from __future__ import annotations

import asyncio
import json
from urllib.parse import urlsplit, urlunsplit

from aiohttp import ClientError, ClientTimeout


class SpeakersError(Exception):
    """Local contract is unavailable, unknown, or rejected."""


def speakers_url(value: str) -> str:
    """Accept one explicit HTTP base URL without credentials or query tokens."""
    value = value.strip()
    if any(char.isspace() or ord(char) < 32 for char in value):
        raise ValueError("Invalid Speakers URL")
    parts = urlsplit(value)
    if (
        parts.scheme not in ("http", "https")
        or not parts.hostname
        or parts.username is not None
        or parts.password is not None
        or parts.query
        or parts.fragment
    ):
        raise ValueError("Invalid Speakers URL")
    _ = parts.port  # Validate malformed ports before storing the option.
    return urlunsplit((parts.scheme, parts.netloc, parts.path.rstrip("/"), "", ""))


def _text(value) -> bool:
    return isinstance(value, str) and bool(value) and len(value) <= 1024


class SpeakersClient:
    """Use HA's shared HTTP session; own neither OAuth nor a background worker."""

    def __init__(self, web, base: str) -> None:
        self._web = web
        self._base = speakers_url(base)

    async def _request(self, method: str, path: str, body=None):
        try:
            async with asyncio.timeout(5):
                async with self._web.request(
                    method,
                    self._base + path,
                    json=body,
                    headers={"Accept": "application/json"},
                    allow_redirects=False,
                    timeout=ClientTimeout(total=4),
                ) as response:
                    if response.status != 200:
                        raise SpeakersError(f"Speakers returned HTTP {response.status}")
                    raw = bytearray()
                    async for chunk in response.content.iter_chunked(8192):
                        raw.extend(chunk)
                        if len(raw) > 65536:
                            raise SpeakersError("Speakers response exceeds budget")

                    def pairs(items):
                        result = {}
                        for key, value in items:
                            if key in result:
                                raise ValueError("Duplicate JSON key")
                            result[key] = value
                        return result

                    return json.loads(raw, object_pairs_hook=pairs)
        except (ClientError, TimeoutError, ValueError) as err:
            # No response body, URL, or credential material enters the public error.
            raise SpeakersError("Speakers request failed; outcome unknown") from err

    async def aliases(self) -> dict:
        data = await self._request("GET", "/api/room-switch")
        if not isinstance(data, dict):
            raise SpeakersError("Invalid alias catalog")
        binding = data.get("binding")
        rows = data.get("aliases")
        if (
            not isinstance(binding, dict)
            or binding.get("ready") is not True
            or not all(_text(binding.get(k)) for k in ("incarnation", "registry", "room_id"))
            or not isinstance(rows, list)
            or not rows
        ):
            raise SpeakersError("Alias owner unavailable")
        ids, rooms = set(), set()
        for row in rows:
            if (
                not isinstance(row, dict)
                or type(row.get("id")) is not int
                or not 1 <= row["id"] <= 4294967295
                or not _text(row.get("room_id"))
                or not _text(row.get("name"))
                or row["id"] in ids
                or row["room_id"] in rooms
            ):
                raise SpeakersError("Ambiguous alias catalog")
            ids.add(row["id"])
            rooms.add(row["room_id"])
        if binding["room_id"] not in rooms:
            raise SpeakersError("Current alias missing from registry")
        return {
            "binding": {k: binding[k] for k in ("ready", "incarnation", "registry", "room_id")},
            "aliases": [{k: r[k] for k in ("id", "room_id", "name")} for r in rows],
        }

    async def move(self, target: str, catalog: dict) -> dict:
        matches = [row for row in catalog["aliases"] if row["room_id"] == target]
        if len(matches) != 1:
            raise SpeakersError("Configured alias is unavailable")
        binding = catalog["binding"]
        body = {
            "room_id": target,
            "expected_incarnation": binding["incarnation"],
            "expected_registry": binding["registry"],
        }
        reply = await self._request("POST", "/api/room-switch", body)
        if (
            not isinstance(reply, dict)
            or reply.get("accepted_local") is not True
            or reply.get("binding") != {**binding, "room_id": target}
        ):
            raise SpeakersError("Local selection outcome unknown")
        return {"accepted_local": True, "binding": reply["binding"]}

    async def rooms(self) -> list:
        data = await self._request("GET", "/api/rooms")
        rows = data.get("rooms") if isinstance(data, dict) else None
        if not isinstance(rows, list):
            raise SpeakersError("Invalid configured rooms")
        seen = set()
        for row in rows:
            if (
                not isinstance(row, dict)
                or not _text(row.get("id"))
                or row["id"] in seen
                or not isinstance(row.get("homepod_id"), str)
            ):
                raise SpeakersError("Invalid configured rooms")
            seen.add(row["id"])
        return rows

    async def outputs(self) -> dict:
        data = await self._request("GET", "/api/state")
        if not isinstance(data, dict) or type(data.get("owntone_up")) is not bool:
            raise SpeakersError("Invalid observed output state")
        rows = data.get("devices")
        if not isinstance(rows, list):
            raise SpeakersError("Invalid observed outputs")
        seen = set()
        for row in rows:
            if (
                not isinstance(row, dict)
                or not _text(row.get("id"))
                or not _text(row.get("name"))
                or type(row.get("selected")) is not bool
                or type(row.get("needs_auth")) is not bool
                or row["id"] in seen
            ):
                raise SpeakersError("Ambiguous observed outputs")
            seen.add(row["id"])
        return data
