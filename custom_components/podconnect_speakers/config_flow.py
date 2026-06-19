"""Config flow for PodConnect Speakers (local add-on manager HTTP API)."""

from __future__ import annotations

from typing import Any
from urllib.parse import urlparse

import voluptuous as vol

from homeassistant.config_entries import ConfigFlow, ConfigFlowResult

from .api import SpeakersApi, SpeakersApiError
from .const import CONF_BASE_URL, DEFAULT_BASE_URL, DOMAIN, LOGGER


class PodConnectSpeakersFlowHandler(ConfigFlow, domain=DOMAIN):
    """Handle the PodConnect Speakers config flow."""

    VERSION = 1

    async def async_step_user(
        self, user_input: dict[str, Any] | None = None
    ) -> ConfigFlowResult:
        """Single-step user flow: enter the manager base URL and validate it."""
        errors: dict[str, str] = {}

        if user_input is not None:
            base_url = user_input[CONF_BASE_URL].rstrip("/")
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
                    host = urlparse(base_url).hostname or base_url
                    await self.async_set_unique_id(host)
                    self._abort_if_unique_id_configured()
                    title = state.get("speaker") or "PodConnect Speakers"
                    return self.async_create_entry(
                        title=title, data={CONF_BASE_URL: base_url}
                    )

        return self.async_show_form(
            step_id="user",
            data_schema=vol.Schema(
                {
                    vol.Required(
                        CONF_BASE_URL,
                        default=(user_input or {}).get(CONF_BASE_URL, DEFAULT_BASE_URL),
                    ): str
                }
            ),
            errors=errors,
        )
