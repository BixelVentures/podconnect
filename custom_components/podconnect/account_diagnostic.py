"""Read-only identifier comparison; never account authority or a play admission."""

from __future__ import annotations

import hashlib
import hmac
import re

_HEX40 = re.compile(r"[a-f0-9]{40}\Z")
_HEX64 = re.compile(r"[a-f0-9]{64}\Z")
_GENERATION = re.compile(r"[1-9][0-9]{0,19}\Z")
_FIELDS = {
    "schema", "method", "domain", "identifier_sha256", "engine_device_id",
    "run_generation", "connection_generation", "observation_sequence",
    "observed_at_unix_ms", "expires_at_unix_ms",
}


def compare_account_identifiers(identity, stored_me_id, expected_engine_id, *, now_ms):
    """Compare a fresh native AP identifier with the entry's stored /me.id.

    Control's config flow established stored_me_id from Spotify /me.id. Native
    provides APWelcome.CanonicalUsername. Their cross-protocol namespace relation
    is not independently established here: even equal bytes grant no authority.
    Caller retains its existing entry/owner fence; this function performs no I/O.
    """
    result = {"comparison": "unavailable", "identity_relation": "unknown"}
    if (
        not isinstance(stored_me_id, str)
        or not stored_me_id
        or len(stored_me_id) > 1024
        or any(ch.isspace() or ord(ch) < 32 or ord(ch) == 127 for ch in stored_me_id)
        or not isinstance(expected_engine_id, str)
        or _HEX40.fullmatch(expected_engine_id) is None
        or type(now_ms) is not int
        or not 0 < now_ms < 2**53
        or not isinstance(identity, dict)
        or identity.get("schema") != "podconnect.start_identity.v1"
        or identity.get("engine_device_id") != expected_engine_id
    ):
        return result
    try:
        encoded = stored_me_id.encode("utf-8", errors="strict")
    except UnicodeError:
        return result
    if len(encoded) > 1024:
        return result
    row = identity.get("account_observation")
    if not isinstance(row, dict) or set(row) != _FIELDS:
        return result
    if (
        row["schema"] != "podconnect.native_account_observation.v1"
        or row["method"] != "sha256"
        or row["domain"] != "spotify.ap.canonical_username"
        or row["engine_device_id"] != expected_engine_id
        or not isinstance(row["identifier_sha256"], str)
        or _HEX64.fullmatch(row["identifier_sha256"]) is None
        or any(not isinstance(row[key], str) or _GENERATION.fullmatch(row[key]) is None
               for key in ("run_generation", "connection_generation", "observation_sequence"))
    ):
        return result
    observed, expires = row["observed_at_unix_ms"], row["expires_at_unix_ms"]
    if (
        type(observed) is not int or type(expires) is not int
        or not 0 < observed <= now_ms < expires < 2**53
        or expires - observed != 5000
    ):
        return result
    equal = hmac.compare_digest(hashlib.sha256(encoded).hexdigest(), row["identifier_sha256"])
    return {"comparison": "identifiers_equal" if equal else "identifiers_different",
            "identity_relation": "unknown"}
