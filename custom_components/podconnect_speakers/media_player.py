"""Media player platform for PodConnect Speakers — one entity per HomePod room."""

from __future__ import annotations

from homeassistant.components.media_player import (
    MediaPlayerEntity,
    MediaPlayerEntityFeature,
    MediaPlayerState,
)
from homeassistant.core import HomeAssistant
from homeassistant.helpers.device_registry import DeviceInfo
from homeassistant.helpers.entity_platform import AddEntitiesCallback
from homeassistant.helpers.update_coordinator import CoordinatorEntity

from . import PodConnectSpeakersConfigEntry
from .const import DOMAIN
from .coordinator import PodConnectSpeakersCoordinator

SUPPORTED = (
    MediaPlayerEntityFeature.PAUSE
    | MediaPlayerEntityFeature.PLAY
    | MediaPlayerEntityFeature.VOLUME_SET
)


async def async_setup_entry(
    hass: HomeAssistant,
    entry: PodConnectSpeakersConfigEntry,
    async_add_entities: AddEntitiesCallback,
) -> None:
    """Set up the single speaker media_player for this config entry."""
    coordinator = entry.runtime_data.coordinator
    async_add_entities([PodConnectSpeaker(coordinator, entry.entry_id)])


class PodConnectSpeaker(
    CoordinatorEntity[PodConnectSpeakersCoordinator], MediaPlayerEntity
):
    """A PodConnect HomePod room exposed as a HA media_player."""

    _attr_has_entity_name = True
    _attr_name = None
    _attr_supported_features = SUPPORTED

    def __init__(
        self, coordinator: PodConnectSpeakersCoordinator, entry_id: str
    ) -> None:
        """Initialise the entity."""
        super().__init__(coordinator)
        self._attr_unique_id = f"{entry_id}_speaker"
        speaker = (coordinator.data or {}).get("speaker") or "PodConnect Speaker"
        self._attr_device_info = DeviceInfo(
            identifiers={(DOMAIN, entry_id)},
            name=speaker,
            manufacturer="PodConnect",
            model="HomePod (AirPlay)",
        )

    @property
    def _data(self) -> dict:
        return self.coordinator.data or {}

    @property
    def state(self) -> MediaPlayerState:
        data = self._data
        if data.get("released"):
            return MediaPlayerState.IDLE
        if data.get("playing"):
            return MediaPlayerState.PLAYING
        return MediaPlayerState.IDLE

    @property
    def volume_level(self) -> float | None:
        vol = self._data.get("volume")
        if vol is not None and vol >= 0:
            return vol / 100
        return None

    @property
    def media_title(self) -> str | None:
        return self._data.get("now_playing") or None

    async def async_media_pause(self) -> None:
        """Account-agnostic local pause (maps to HassMediaPause / "stop")."""
        await self.coordinator.api.stop()
        await self.coordinator.async_request_refresh()

    async def async_media_stop(self) -> None:
        """Stop -> same as pause for the local engine."""
        await self.coordinator.api.stop()
        await self.coordinator.async_request_refresh()

    async def async_media_play(self) -> None:
        """Resume playback."""
        await self.coordinator.api.play()
        await self.coordinator.async_request_refresh()

    async def async_set_volume_level(self, volume: float) -> None:
        """Set the speaker volume (0.0..1.0 -> 0..100)."""
        await self.coordinator.api.set_volume(round(volume * 100))
        await self.coordinator.async_request_refresh()
