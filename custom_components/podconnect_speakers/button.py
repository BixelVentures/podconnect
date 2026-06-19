"""Button platform for PodConnect Speakers — release the HomePod for other AirPlay apps."""

from __future__ import annotations

from homeassistant.components.button import ButtonEntity
from homeassistant.core import HomeAssistant
from homeassistant.helpers.device_registry import DeviceInfo
from homeassistant.helpers.entity_platform import AddEntitiesCallback
from homeassistant.helpers.update_coordinator import CoordinatorEntity

from . import PodConnectSpeakersConfigEntry
from .const import DOMAIN
from .coordinator import PodConnectSpeakersCoordinator


async def async_setup_entry(
    hass: HomeAssistant,
    entry: PodConnectSpeakersConfigEntry,
    async_add_entities: AddEntitiesCallback,
) -> None:
    """Set up the Release HomePod button for this config entry."""
    coordinator = entry.runtime_data.coordinator
    async_add_entities([PodConnectReleaseButton(coordinator, entry.entry_id)])


class PodConnectReleaseButton(
    CoordinatorEntity[PodConnectSpeakersCoordinator], ButtonEntity
):
    """Frees the HomePod so other AirPlay apps can take it over."""

    _attr_has_entity_name = True
    _attr_name = "Release HomePod"

    def __init__(
        self, coordinator: PodConnectSpeakersCoordinator, entry_id: str
    ) -> None:
        """Initialise the entity (shares the speaker's device)."""
        super().__init__(coordinator)
        self._attr_unique_id = f"{entry_id}_release"
        speaker = (coordinator.data or {}).get("speaker") or "PodConnect Speaker"
        self._attr_device_info = DeviceInfo(
            identifiers={(DOMAIN, entry_id)},
            name=speaker,
            manufacturer="PodConnect",
            model="HomePod (AirPlay)",
        )

    async def async_press(self) -> None:
        """Release the HomePod, then refresh state."""
        await self.coordinator.api.release()
        await self.coordinator.async_request_refresh()
