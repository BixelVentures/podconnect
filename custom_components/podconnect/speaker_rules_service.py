"""Admin services to read and set the family's speaker rules on an account entry."""

from __future__ import annotations

import voluptuous as vol
from homeassistant.core import HomeAssistant, ServiceCall, SupportsResponse
from homeassistant.exceptions import ServiceValidationError
from homeassistant.helpers.service import async_register_admin_service

from .const import DOMAIN
from .speaker_rules import (
    OPTION,
    RulesInvalid,
    from_podview_policy,
    normalize,
    rules_of,
)


def _entry(hass: HomeAssistant, entry_id: str):
    entry = hass.config_entries.async_get_entry(entry_id)
    if entry is None or entry.domain != DOMAIN:
        raise ServiceValidationError(
            translation_domain=DOMAIN,
            translation_key="unknown_account",
        )
    return entry


def register_speaker_rules_services(hass: HomeAssistant) -> None:
    if hass.services.has_service(DOMAIN, "set_speaker_rules"):
        return

    async def set_rules(call: ServiceCall):
        entry = _entry(hass, call.data["config_entry_id"])
        given = [k for k in ("podview_policy", "rules", "clear") if k in call.data]
        if len(given) != 1 or call.data.get("clear") is False:
            raise ServiceValidationError(
                translation_domain=DOMAIN,
                translation_key="invalid_speaker_rules",
                translation_placeholders={"reason": "one_of"},
            )
        ignored = 0
        try:
            if "podview_policy" in call.data:
                rules, ignored = from_podview_policy(
                    call.data["podview_policy"], entry.entry_id
                )
            elif "rules" in call.data:
                rules = normalize(call.data["rules"])
            else:
                rules = None
        except RulesInvalid as err:
            raise ServiceValidationError(
                translation_domain=DOMAIN,
                translation_key="invalid_speaker_rules",
                translation_placeholders={"reason": str(err)},
            ) from err
        options = {k: v for k, v in entry.options.items() if k != OPTION}
        if rules is not None:
            options[OPTION] = rules
        # Rules are read on every write; changing them never reloads the account.
        hass.config_entries.async_update_entry(entry, options=options)
        return {"rules": rules_of(entry), "ignored_rows": ignored}

    async def get_rules(call: ServiceCall):
        entry = _entry(hass, call.data["config_entry_id"])
        return {"rules": rules_of(entry)}

    entry_id = vol.All(str, vol.Length(min=1, max=1024))
    async_register_admin_service(
        hass,
        DOMAIN,
        "set_speaker_rules",
        set_rules,
        vol.Schema(
            {
                vol.Required("config_entry_id"): entry_id,
                vol.Optional("podview_policy"): vol.All(str, vol.Length(max=8192)),
                vol.Optional("rules"): dict,
                vol.Optional("clear"): bool,
            }
        ),
        SupportsResponse.OPTIONAL,
    )
    hass.services.async_register(
        DOMAIN,
        "get_speaker_rules",
        get_rules,
        schema=vol.Schema({vol.Required("config_entry_id"): entry_id}),
        supports_response=SupportsResponse.ONLY,
    )
