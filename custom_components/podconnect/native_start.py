"""A shared native start barrier for ordinary HA media-player start paths.

The caller owns the exact Spotify account and chosen target. Speakers owns the
configured engine ID, alias/output route, and fixed fresh-session volume.
"""

from __future__ import annotations

from homeassistant.exceptions import HomeAssistantError

from .api import SpotifyApiError
from .observations import current


def start_guards(hass, entry_id: str, data, target_id: str, *, expected_api=None):
    """Return guards run after OAuth refresh and immediately before one send.

    A failed/unknown local commit consumes its lease. No caller retries it.
    When Speakers is configured but its exact engine identity cannot be read,
    starts fail closed, including starts aimed at other Spotify devices.
    """
    coordinator = data.coordinator
    owner_epoch = coordinator.owner_epoch
    poll_sequence = coordinator.poll_sequence
    api = data.api
    speakers = data.speakers
    if expected_api is None:
        expected_api = api

    def owned() -> bool:
        try:
            current(hass, entry_id, data)
        except HomeAssistantError:
            return False
        return (
            data.active
            and data.api is api
            and api is expected_api
            and data.coordinator is coordinator
            and data.speakers is speakers
            and coordinator.owner_epoch == owner_epoch
            and coordinator.poll_sequence == poll_sequence
        )

    async def native_ready() -> None:
        if not owned():
            raise SpotifyApiError("Command admission expired")
        if speakers is None:
            return
        native_id = await speakers.start_identity()
        if not owned():
            raise SpotifyApiError("Command admission expired")
        if target_id != native_id:
            return
        catalog = await speakers.aliases()
        if not owned():
            raise SpotifyApiError("Command admission expired")
        proof = await speakers.start_readiness(catalog["binding"]["room_id"], target_id)
        if not owned() or proof["alias_binding"] != catalog["binding"]:
            raise SpotifyApiError("Native start owner changed")
        if proof["mode"] == "same_session":
            observed = data.observations.playback(entry_id)
            target = observed.get("playback", {}).get("target") if observed.get("ok") else None
            if not isinstance(target, dict) or target.get("target_id") != target_id:
                raise SpotifyApiError("Native account playback unavailable")
        lease = await speakers.prepare_start(proof)
        if not owned():
            raise SpotifyApiError("Command admission expired")
        await speakers.commit_start(lease["lease_id"], proof["policy_revision"])
        if not owned():
            raise SpotifyApiError("Command admission expired")

    return owned, native_ready
