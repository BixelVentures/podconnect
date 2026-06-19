"""Config flow for PodConnect (Spotify OAuth2 via Application Credentials)."""

from __future__ import annotations

from collections.abc import Mapping
import logging
from typing import Any

import voluptuous as vol

from homeassistant.config_entries import (
    SOURCE_REAUTH,
    ConfigEntry,
    ConfigFlowResult,
    OptionsFlow,
)
from homeassistant.const import CONF_ACCESS_TOKEN, CONF_NAME, CONF_TOKEN
from homeassistant.core import callback
from homeassistant.helpers import config_entry_oauth2_flow
from homeassistant.helpers.aiohttp_client import async_get_clientsession

from .const import (
    CONF_BASE_URL,
    DEFAULT_BASE_URL,
    DOMAIN,
    LOGGER,
    SPOTIFY_API,
    SPOTIFY_SCOPES,
)
from .speakers_api import SpeakersApi, SpeakersApiError


class PodConnectFlowHandler(
    config_entry_oauth2_flow.AbstractOAuth2FlowHandler, domain=DOMAIN
):
    """Handle the PodConnect (Spotify) OAuth2 config flow."""

    DOMAIN = DOMAIN
    VERSION = 1

    @staticmethod
    @callback
    def async_get_options_flow(config_entry: ConfigEntry) -> OptionsFlow:
        """Return the options flow (configure the optional add-on manager URL)."""
        return OptionsFlowHandler()

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
            user_input={
                "implementation": self._get_reauth_entry().data["auth_implementation"]
            }
        )


class OptionsFlowHandler(OptionsFlow):
    """Options: configure the optional PodConnect add-on manager URL.

    Leave the URL empty for Spotify-only control (no add-on dependency). Set it to the
    add-on manager (the HA host's LAN IP on :8099) to surface account-agnostic local
    speaker media_players + a Release button per room.
    """

    async def async_step_init(
        self, user_input: dict[str, Any] | None = None
    ) -> ConfigFlowResult:
        """Single-step options: enter (or clear) the manager base URL and validate it."""
        errors: dict[str, str] = {}

        if user_input is not None:
            base_url = (user_input.get(CONF_BASE_URL) or "").strip().rstrip("/")
            if not base_url:
                # Empty = disable local speakers; reload tears them down.
                return self.async_create_entry(data={CONF_BASE_URL: ""})
            api = SpeakersApi(self.hass, base_url)
            try:
                state = await api.state()
            except SpeakersApiError as err:
                LOGGER.debug("Could not reach PodConnect manager: %s", err)
                errors["base"] = "cannot_connect"
            else:
                if "owntone_up" not in state and "speaker" not in state:
                    errors["base"] = "cannot_connect"
                else:
                    return self.async_create_entry(data={CONF_BASE_URL: base_url})

        current = self.config_entry.options.get(CONF_BASE_URL, "")
        return self.async_show_form(
            step_id="init",
            data_schema=vol.Schema(
                {
                    vol.Optional(
                        CONF_BASE_URL,
                        description={"suggested_value": current or DEFAULT_BASE_URL},
                    ): str
                }
            ),
            errors=errors,
        )
