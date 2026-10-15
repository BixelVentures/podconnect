"""Explicit operator-only read service; no provider or playback authority."""

from __future__ import annotations

import asyncio
import time

import voluptuous as vol
from homeassistant.core import SupportsResponse
from homeassistant.exceptions import HomeAssistantError

from .account_diagnostic import compare_account_identifiers
from .const import DOMAIN


def register_account_diagnostic_service(hass):
    def current(entry_id, expected=None):
        entries = [e for e in hass.config_entries.async_entries(DOMAIN) if e.entry_id == entry_id]
        if len(entries) != 1:
            raise HomeAssistantError("Account diagnostic owner unavailable")
        entry = entries[0]
        data = getattr(entry, "runtime_data", None)
        if (data is None or not data.active or data.speakers is None
                or (expected is not None and (
                    entry is not expected[0] or data is not expected[1]
                    or data.api is not expected[2] or data.coordinator is not expected[3]
                    or data.speakers is not expected[4] or entry.unique_id != expected[5]
                    or data.coordinator.owner_epoch != expected[6]))):
            raise HomeAssistantError("Account diagnostic owner unavailable")
        return entry, data

    async def diagnostic(call):
        entry_id = call.data["config_entry_id"]
        entry, data = current(entry_id)
        stored_id = entry.unique_id
        owner = (entry, data, data.api, data.coordinator, data.speakers, stored_id, data.coordinator.owner_epoch)
        result = {"comparison": "unknown", "identity_relation": "unknown"}
        try:
            async with asyncio.timeout(5):
                response = await data.speakers.account_identity()
                current(entry_id, owner)
                result = compare_account_identifiers(response, stored_id, call.data["engine_device_id"],
                                                     now_ms=time.time_ns() // 1_000_000)
                if result["comparison"] == "unavailable":
                    result["comparison"] = "unknown"
        except HomeAssistantError:
            raise
        except Exception:
            # No native body, account identifier, digest, URL or exception text escapes.
            current(entry_id, owner)
        current(entry_id, owner)
        return result

    if not hass.services.has_service(DOMAIN, "get_account_identifier_diagnostic"):
        hass.services.async_register(
            DOMAIN, "get_account_identifier_diagnostic", diagnostic,
            schema=vol.Schema({
                vol.Required("config_entry_id"): vol.All(str, vol.Length(min=1, max=1024)),
                vol.Required("engine_device_id"): vol.Match(r"^[a-f0-9]{40}$"),
            }), supports_response=SupportsResponse.ONLY,
        )
