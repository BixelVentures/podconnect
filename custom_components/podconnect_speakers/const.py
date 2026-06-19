"""Constants for the PodConnect Speakers integration."""

from __future__ import annotations

import logging

DOMAIN = "podconnect_speakers"
LOGGER = logging.getLogger(__package__)

CONF_BASE_URL = "base_url"
DEFAULT_BASE_URL = "http://homeassistant.local:8099"

# How often to poll the add-on's manager HTTP API for speaker state.
# Local LAN call, so a tight interval keeps the dashboard / Assist state responsive.
POLL_INTERVAL_SECONDS = 5
