"""Config flow for PodConnect (Spotify OAuth2 via Application Credentials)."""

from __future__ import annotations

from collections.abc import Mapping
import logging
from typing import Any

import voluptuous as vol

from homeassistant.config_entries import SOURCE_REAUTH, ConfigFlowResult, OptionsFlow
from homeassistant.core import callback
from homeassistant.const import CONF_ACCESS_TOKEN, CONF_NAME, CONF_TOKEN
from homeassistant.helpers import area_registry as ar, config_entry_oauth2_flow
from homeassistant.helpers.selector import AreaSelector
from homeassistant.helpers.aiohttp_client import async_get_clientsession

from .const import CONF_ROOM_AREAS, CONF_SPEAKERS_URL, DOMAIN, LOGGER, SPOTIFY_API, SPOTIFY_SCOPES
from .speakers import SpeakersClient, SpeakersError, speakers_url
from .targets import configured_alias_targets


class PodConnectFlowHandler(config_entry_oauth2_flow.AbstractOAuth2FlowHandler, domain=DOMAIN):
    """Handle the PodConnect (Spotify) OAuth2 config flow."""

    DOMAIN = DOMAIN
    VERSION = 1

    @staticmethod
    @callback
    def async_get_options_flow(config_entry):
        return PodConnectOptionsFlow(config_entry)

    @property
    def logger(self) -> logging.Logger:
        """Return the logger."""
        return LOGGER

    @property
    def extra_authorize_data(self) -> dict[str, Any]:
        """Append the Spotify scopes to the authorize URL.

        Spotify (as used by HA core) takes comma-joined scopes here.
        """
        return {"scope": ",".join(SPOTIFY_SCOPES)}

    async def async_oauth_create_entry(self, data: dict[str, Any]) -> ConfigFlowResult:
        """Create or update the entry, keyed on the Spotify account."""
        token = data[CONF_TOKEN][CONF_ACCESS_TOKEN]
        web = async_get_clientsession(self.hass)
        try:
            async with web.get(
                f"{SPOTIFY_API}/me", headers={"Authorization": f"Bearer {token}"}
            ) as resp:
                if resp.status != 200:
                    return self.async_abort(reason="connection_error")
                me = await resp.json()
        except Exception:  # noqa: BLE001
            self.logger.exception("Error connecting to Spotify")
            return self.async_abort(reason="connection_error")

        name = me.get("display_name") or me["id"]
        await self.async_set_unique_id(me["id"])

        if self.source == SOURCE_REAUTH:
            self._abort_if_unique_id_mismatch(reason="reauth_account_mismatch")
            return self.async_update_reload_and_abort(
                self._get_reauth_entry(), title=name, data=data
            )

        self._abort_if_unique_id_configured()
        return self.async_create_entry(title=name, data={**data, CONF_NAME: name})

    async def async_step_reauth(self, entry_data: Mapping[str, Any]) -> ConfigFlowResult:
        """Start reauth."""
        return await self.async_step_reauth_confirm()

    async def async_step_reauth_confirm(
        self, user_input: dict[str, Any] | None = None
    ) -> ConfigFlowResult:
        """Confirm reauth, then re-run OAuth with the stored implementation."""
        if user_input is None:
            return self.async_show_form(step_id="reauth_confirm")
        return await self.async_step_pick_implementation(
            user_input={"implementation": self._get_reauth_entry().data["auth_implementation"]}
        )


class PodConnectOptionsFlow(OptionsFlow):
    """Account connection and explicit room language metadata for existing targets."""

    def __init__(self, entry) -> None:
        self._entry = entry
        self._original_options = dict(entry.options)
        self._options = dict(entry.options)
        self._targets = []
        self._index = 0

    async def _targets_now(self):
        value = self._options.get(CONF_SPEAKERS_URL)
        if not value:
            return []
        client = SpeakersClient(async_get_clientsession(self.hass), value)
        return [t for t in await configured_alias_targets(client) if t["homepod_id"]]

    def _unchanged(self):
        return dict(self._entry.options) == self._original_options

    async def async_step_init(self, user_input=None):
        errors = {}
        if user_input is not None:
            value = user_input.get(CONF_SPEAKERS_URL, "").strip()
            try:
                value = speakers_url(value) if value else ""
            except ValueError:
                errors[CONF_SPEAKERS_URL] = "invalid_url"
            else:
                if not self._unchanged():
                    return self.async_abort(reason="configuration_changed")
                self._options.pop(CONF_SPEAKERS_URL, None)
                if value:
                    self._options[CONF_SPEAKERS_URL] = value
                try:
                    self._targets = await self._targets_now()
                except SpeakersError:
                    # Keep the existing connection-only save available while offline.
                    self._targets = []
                if not self._unchanged():
                    return self.async_abort(reason="configuration_changed")
                if not self._targets:
                    return self.async_create_entry(title="", data=self._options)
                return await self.async_step_room()
        return self.async_show_form(
            step_id="init",
            data_schema=vol.Schema(
                {
                    vol.Optional(
                        CONF_SPEAKERS_URL, default=self._options.get(CONF_SPEAKERS_URL, "")
                    ): str
                }
            ),
            errors=errors,
        )

    async def async_step_room(self, user_input=None):
        target = self._targets[self._index]
        errors = {}
        if user_input is not None:
            if not self._unchanged():
                return self.async_abort(reason="configuration_changed")
            try:
                fresh = await self._targets_now()
            except SpeakersError:
                errors["base"] = "speakers_unavailable"
            else:
                if not self._unchanged():
                    return self.async_abort(reason="configuration_changed")
                matches = [
                    t
                    for t in fresh
                    if t["target_id"] == target["target_id"]
                    and t["homepod_id"] == target["homepod_id"]
                ]
                if len(matches) != 1:
                    return self.async_abort(reason="speaker_changed")
                area_id = user_input.get("area_id")
                if area_id and (
                    not isinstance(area_id, str)
                    or ar.async_get(self.hass).async_get_area(area_id) is None
                ):
                    errors["area_id"] = "area_unavailable"
                else:
                    existing = self._options.get(CONF_ROOM_AREAS, {})
                    associations = dict(existing) if isinstance(existing, dict) else {}
                    associations.pop(target["target_id"], None)
                    if area_id:
                        associations[target["target_id"]] = {
                            "homepod_id": target["homepod_id"],
                            "area_id": area_id,
                        }
                    self._options.pop(CONF_ROOM_AREAS, None)
                    if associations:
                        self._options[CONF_ROOM_AREAS] = associations
                    self._index += 1
                    if self._index == len(self._targets):
                        identities = {(t["target_id"], t["homepod_id"]) for t in fresh}
                        if any(
                            (t["target_id"], t["homepod_id"]) not in identities
                            for t in self._targets
                        ):
                            return self.async_abort(reason="speaker_changed")
                        # Areas chosen on an earlier form can also be deleted meanwhile.
                        if any(
                            ar.async_get(self.hass).async_get_area(a["area_id"]) is None
                            for key, a in associations.items()
                            if key in {t["target_id"] for t in self._targets}
                            and isinstance(a, dict)
                            and "area_id" in a
                        ):
                            return self.async_abort(reason="configuration_changed")
                        return self.async_create_entry(title="", data=self._options)
                    return await self.async_step_room()
        saved = self._options.get(CONF_ROOM_AREAS, {})
        saved = saved.get(target["target_id"]) if isinstance(saved, dict) else None
        suggested = {}
        if (
            isinstance(saved, dict)
            and saved.get("homepod_id") == target["homepod_id"]
            and isinstance(saved.get("area_id"), str)
            and ar.async_get(self.hass).async_get_area(saved["area_id"]) is not None
        ):
            suggested = {"suggested_value": saved["area_id"]}
        return self.async_show_form(
            step_id="room",
            data_schema=vol.Schema(
                {vol.Optional("area_id", description=suggested): AreaSelector()}
            ),
            description_placeholders={"speaker": target["name"]},
            errors=errors,
        )
