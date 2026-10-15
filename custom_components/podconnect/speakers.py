"""Bounded local Speakers requests, separate from the Spotify OAuth client."""

from __future__ import annotations

import asyncio
import json
from urllib.parse import urlencode, urlsplit, urlunsplit

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

    async def _request(self, method: str, path: str, body=None, *, budget: int = 5):
        try:
            async with asyncio.timeout(budget):
                async with self._web.request(
                    method,
                    self._base + path,
                    json=body,
                    headers={"Accept": "application/json"},
                    allow_redirects=False,
                    timeout=ClientTimeout(total=budget - 1),
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

    async def start_readiness(self, room_id: str, target_id: str) -> dict:
        """Read the native owner's exact engine, room, output and start policy."""
        if not _text(room_id) or not _text(target_id):
            raise SpeakersError("Invalid native start identity")
        path = "/api/start-volume?" + urlencode({"room_id": room_id, "target_id": target_id})
        proof = await self._request("GET", path)
        if not _start_proof(proof, room_id, target_id):
            raise SpeakersError("Native start owner unavailable")
        return proof

    async def account_identity(self) -> dict:
        """One explicit bounded native diagnostic read; never forwards OAuth."""
        return await self._request("GET", "/api/start-volume/identity?account=1", budget=5)

    async def start_identity(self) -> str:
        """The configured native Spotify ID, independent of a browser/device name."""
        reply = await self._request("GET", "/api/start-volume/identity")
        if (
            not isinstance(reply, dict)
            or reply.get("schema") != "podconnect.start_identity.v1"
            or not isinstance(reply.get("engine_device_id"), str)
            or len(reply["engine_device_id"]) != 40
            or any(ch not in "0123456789abcdef" for ch in reply["engine_device_id"])
        ):
            raise SpeakersError("Native engine identity unavailable")
        return reply["engine_device_id"]

    async def prepare_start(self, proof: dict) -> dict:
        """Prime the exact output; the owner issues a short one-use lease."""
        if not isinstance(proof, dict) or not _start_proof(proof, proof.get("room_id"), proof.get("engine_device_id")):
            raise SpeakersError("Invalid native start proof")
        reply = await self._request("POST", "/api/start-volume", {
            "room_id": proof["room_id"],
            "target_id": proof["engine_device_id"],
            "policy_revision": proof["policy_revision"],
            "alias_incarnation": proof["alias_binding"]["incarnation"],
        }, budget=10)
        if (
            not isinstance(reply, dict)
            or reply.get("schema") != "podconnect.start_lease.v1"
            or not isinstance(reply.get("lease_id"), str)
            or len(reply["lease_id"]) != 32
            or reply.get("expires_in_ms") != 5000
            or not _start_proof(reply.get("proof"), proof["room_id"], proof["engine_device_id"])
            or any(reply["proof"].get(key) != proof.get(key) for key in (
                "engine_device_id", "room_id", "homepod_id", "policy_revision", "start_volume_percent", "mode"
            ))
            or any(reply["proof"]["alias_binding"].get(key) != proof["alias_binding"].get(key)
                   for key in ("incarnation", "registry"))
            or reply["proof"]["alias_binding"]["room_id"] != proof["room_id"]
        ):
            raise SpeakersError("Native start lease outcome unknown")
        return reply

    async def commit_start(self, lease_id: str, policy_revision: str, max_volume: int | None = None) -> None:
        """Consume readiness immediately before the one provider send."""
        if not isinstance(lease_id, str) or len(lease_id) != 32:
            raise SpeakersError("Invalid native start lease")
        if max_volume is not None and (type(max_volume) is not int or not 0 <= max_volume <= 100):
            raise SpeakersError("Invalid approved volume limit")
        body = {"lease_id": lease_id}
        if max_volume is not None:
            body["max_volume"] = max_volume
        reply = await self._request("POST", "/api/start-volume/commit", body)
        if (
            not isinstance(reply, dict)
            or reply.get("ok") is not True
            or reply.get("schema") != "podconnect.start_commit.v1"
            or reply.get("policy_revision") != policy_revision
        ):
            raise SpeakersError("Native start lease outcome unknown")


    async def queue_occurrences(self, room_id, target_id, account_user_id):
        query = urlencode({"room_id": room_id, "target_id": target_id, "account_user_id": account_user_id})
        return await self._request("GET", "/api/queue-occurrences?" + query, budget=13)

    async def play_queue_occurrence(self, target_id, account_user_id, args):
        return await self._request("POST", "/api/queue-occurrence", {**args, "target_id": target_id, "account_user_id": account_user_id}, budget=7)


def _start_proof(value, room_id, target_id) -> bool:
    if not isinstance(value, dict) or value.get("schema") != "podconnect.start_volume.v1":
        return False
    binding = value.get("alias_binding")
    return (
        value.get("ready") is True
        and value.get("engine_device_id") == target_id
        and value.get("room_id") == room_id
        and _text(value.get("homepod_id"))
        and isinstance(value.get("policy_revision"), str)
        and len(value["policy_revision"]) == 64
        and type(value.get("start_volume_percent")) is int
        and 0 <= value["start_volume_percent"] <= 100
        and value.get("mode") in ("fresh_start", "same_session")
        and type(value.get("output_volume_percent")) is int
        and 0 <= value["output_volume_percent"] <= 100
        and type(value.get("output_selected")) is bool
        and (value["mode"] != "same_session" or value["output_selected"])
        and isinstance(binding, dict)
        and binding.get("ready") is True
        and _text(binding.get("room_id"))
        and (value["mode"] != "same_session" or binding["room_id"] == room_id)
        and all(_text(binding.get(key)) for key in ("incarnation", "registry"))
    )

