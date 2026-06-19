"""Button platform for PodConnect — release a HomePod room for other AirPlay apps.

Only set up when the optional add-on manager URL is configured (a speakers coordinator
exists on the entry's runtime data). One Release button per room, sharing the room's
local-speaker device.
"""

from __future__ import annotations

from homeassistant.components.button import ButtonEntity
from homeassistant.core import HomeAssistant, callback
from homeassistant.helpers.device_registry import DeviceInfo
from homeassistant.helpers.entity_platform import AddEntitiesCallback
from homeassistant.helpers.update_coordinator import CoordinatorEntity

from . import PodConnectConfigEntry
from .const import DOMAIN
from .speakers_coordinator import SpeakersCoordinator


async def async_setup_entry(
    hass: HomeAssistant,
    entry: PodConnectConfigEntry,
    async_add_entities: AddEntitiesCallback,
) -> None:
    """Create a Release button per room, when the add-on manager is configured."""
    coordinator = getattr(entry.runtime_data, "speakers_coordinator", None)
    if coordinator is None:
        # No add-on URL configured -> Control is Spotify-only; nothing to add.
        return

    known: set[str] = set()

    @callback
    def _discover() -> None:
        new: list[PodConnectReleaseButton] = []
        for room in coordinator.data or []:
            room_id = room.get("id")
            if room_id and room_id not in known:
                known.add(room_id)
                new.append(
                    PodConnectReleaseButton(coordinator, entry.entry_id, room_id)
                )
        if new:
            async_add_entities(new)

    _discover()
    entry.async_on_unload(coordinator.async_add_listener(_discover))


class PodConnectReleaseButton(
    CoordinatorEntity[SpeakersCoordinator], ButtonEntity
):
    """Frees a room's HomePod so other AirPlay apps can take it over."""

    _attr_has_entity_name = True
    _attr_name = "Release HomePod"

    def __init__(
        self, coordinator: SpeakersCoordinator, entry_id: str, room_id: str
    ) -> None:
        """Initialise the entity (shares the room's local-speaker device)."""
        super().__init__(coordinator)
        self._room_id = room_id
        self._attr_unique_id = f"{entry_id}_release_{room_id}"
        room_name = self._room.get("name") if self._room else None
        self._attr_device_info = DeviceInfo(
            identifiers={(DOMAIN, f"speaker_{room_id}")},
            name=room_name or f"PodConnect {room_id}",
            manufacturer="PodConnect",
            model="HomePod (account-agnostic)",
        )

    @property
    def _room(self) -> dict | None:
        for room in self.coordinator.data or []:
            if room.get("id") == self._room_id:
                return room
        return None

    async def async_press(self) -> None:
        """Release this room's HomePod, then refresh state."""
        await self.coordinator.api.release(self._room_id)
        await self.coordinator.async_request_refresh()
