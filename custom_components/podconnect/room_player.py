"""One media_player per PodConnect room.

The room's state is what Speakers reports its HomePod outputs (/api/rooms), whether the
source is Spotify or radio. Every control is one direct Speakers call; last command wins.
"""

from __future__ import annotations

from datetime import timedelta

from homeassistant.components.media_player import (
    MediaPlayerEntity,
    MediaPlayerEntityFeature,
    MediaPlayerState,
    MediaType,
)
from homeassistant.core import callback
from homeassistant.exceptions import HomeAssistantError
from homeassistant.helpers.device_registry import DeviceInfo
from homeassistant.helpers.update_coordinator import (
    CoordinatorEntity,
    DataUpdateCoordinator,
    UpdateFailed,
)

from .api import SpotifyApiError
from .const import DOMAIN, LOGGER
from .speakers import SpeakersError

ROOM_POLL = timedelta(seconds=2)

_STATES = {
    "playing": MediaPlayerState.PLAYING,
    "paused": MediaPlayerState.PAUSED,
    "connecting": MediaPlayerState.BUFFERING,
    "idle": MediaPlayerState.IDLE,
}
_SEARCH_TYPES = {
    "artist": "artist",
    "album": "album",
    "playlist": "playlist",
    "show": "show",
    "podcast": "show",
    "episode": "episode",
}


def room_unique_id(entry_id: str, room_id: str) -> str:
    return f"{entry_id}_room_{room_id}"


def room_device_key(room_id: str) -> str:
    return f"room_{room_id}"


class RoomStatus(DataUpdateCoordinator[dict[str, dict]]):
    """Speakers' room rows by room id: the one source of room state."""

    def __init__(self, hass, entry, speakers) -> None:
        super().__init__(
            hass,
            LOGGER,
            config_entry=entry,
            name="PodConnect rooms",
            update_interval=ROOM_POLL,
        )
        self.speakers = speakers

    async def _async_update_data(self) -> dict[str, dict]:
        try:
            rows = await self.speakers.rooms()
        except SpeakersError as err:
            raise UpdateFailed(str(err)) from err
        return {row["id"]: row for row in rows}


async def async_setup_rooms(hass, entry, async_add_entities) -> None:
    """Add a room entity for every configured room, now and when rooms are added."""
    status = RoomStatus(hass, entry, entry.runtime_data.speakers)
    await status.async_refresh()
    known: set[str] = set()

    @callback
    def _discover() -> None:
        new = [
            PodConnectRoomPlayer(status, entry, room_id, row.get("name") or room_id)
            for room_id, row in (status.data or {}).items()
            if room_id not in known
        ]
        known.update(player.room_id for player in new)
        if new:
            async_add_entities(new)

    _discover()
    entry.async_on_unload(status.async_add_listener(_discover))


class PodConnectRoomPlayer(CoordinatorEntity[RoomStatus], MediaPlayerEntity):
    """A PodConnect room: its HomePod, whatever plays on it."""

    _attr_has_entity_name = True
    _attr_name = None
    _attr_media_content_type = MediaType.MUSIC
    _attr_supported_features = (
        MediaPlayerEntityFeature.PLAY
        | MediaPlayerEntityFeature.PAUSE
        | MediaPlayerEntityFeature.NEXT_TRACK
        | MediaPlayerEntityFeature.PREVIOUS_TRACK
        | MediaPlayerEntityFeature.SEEK
        | MediaPlayerEntityFeature.VOLUME_SET
        | MediaPlayerEntityFeature.PLAY_MEDIA
    )

    def __init__(self, status: RoomStatus, entry, room_id: str, name: str) -> None:
        super().__init__(status)
        self._entry = entry
        self.room_id = room_id
        self._attr_unique_id = room_unique_id(entry.entry_id, room_id)
        self._attr_device_info = DeviceInfo(
            identifiers={(DOMAIN, room_device_key(room_id))},
            name=name,
            manufacturer="PodConnect",
            model="Connect room",
        )

    @property
    def _row(self) -> dict | None:
        return (self.coordinator.data or {}).get(self.room_id)

    @property
    def available(self) -> bool:
        return super().available and self._row is not None

    @property
    def state(self) -> MediaPlayerState | None:
        row = self._row
        if row is None:
            return None
        return _STATES.get(row.get("state"), MediaPlayerState.IDLE)

    @property
    def volume_level(self) -> float | None:
        volume = (self._row or {}).get("volume")
        if not isinstance(volume, int) or volume < 0:
            return None
        return min(volume, 100) / 100

    def _now_playing(self) -> tuple[str | None, str | None]:
        text = (self._row or {}).get("now_playing")
        if not isinstance(text, str) or not text:
            return None, None
        artist, sep, title = text.partition(" — ")
        return (title, artist) if sep else (text, None)

    @property
    def media_title(self) -> str | None:
        if (self._row or {}).get("source") == "radio":
            return "Radio"
        return self._now_playing()[0]

    @property
    def media_artist(self) -> str | None:
        return self._now_playing()[1]

    @property
    def extra_state_attributes(self) -> dict:
        return {"podconnect_source": (self._row or {}).get("source") or None}

    async def _control(self, action: str, **arguments) -> None:
        try:
            await self.coordinator.speakers.room_control(self.room_id, action, **arguments)
        except SpeakersError as err:
            raise HomeAssistantError(f"{self.name or self.room_id}: {action} failed") from err
        finally:
            await self.coordinator.async_refresh()

    async def async_media_play(self) -> None:
        await self._control("resume")

    async def async_media_pause(self) -> None:
        await self._control("pause")

    async def async_media_next_track(self) -> None:
        await self._control("next")

    async def async_media_previous_track(self) -> None:
        await self._control("previous")

    async def async_media_seek(self, position: float) -> None:
        await self._control("seek", position_ms=round(position * 1000))

    async def async_set_volume_level(self, volume: float) -> None:
        await self._control("volume", volume=round(volume * 100))

    async def async_play_media(self, media_type: str, media_id: str, **kwargs) -> None:
        """Start Spotify content in this room: select the room, then play on the speaker."""
        data = self._entry.runtime_data
        speakers, api = data.speakers, data.api
        try:
            uri = media_id if media_id.startswith("spotify:") else None
            if uri is None:
                uri = await _search_top_uri(api, media_id, media_type)
            catalog = await speakers.aliases()
            if (catalog.get("binding") or {}).get("room_id") != self.room_id:
                await speakers.move(self.room_id, catalog)
            engine = await speakers.start_identity()
            if uri.split(":")[1] in ("track", "episode"):
                await api.play(engine, uris=[uri])
            else:
                await api.play(engine, context_uri=uri)
        except (SpeakersError, SpotifyApiError) as err:
            raise HomeAssistantError(f"{self.name or self.room_id}: start failed") from err
        finally:
            await self.coordinator.async_refresh()

    async def async_play_from_library(self, source: str, shuffle: bool = False) -> None:
        raise HomeAssistantError("Library starts run through play_media on a room")


async def _search_top_uri(api, query: str, media_type: str | None) -> str:
    """The first playable Spotify search result for a spoken or typed request."""
    want = _SEARCH_TYPES.get((media_type or "").lower(), "track")
    data = await api.search(query, want)
    for item in (data.get(want + "s") or {}).get("items", []):
        if item and isinstance(item.get("uri"), str):
            return item["uri"]
    raise HomeAssistantError(f"Nothing on Spotify matches {query!r}")
