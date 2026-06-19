"""Media player platform for PodConnect — one entity per Spotify Connect device."""

from __future__ import annotations

from homeassistant.components.media_player import (
    BrowseMedia,
    MediaClass,
    MediaPlayerEntity,
    MediaPlayerEntityFeature,
    MediaPlayerState,
    MediaType,
    RepeatMode,
)
from homeassistant.core import callback
from homeassistant.helpers.device_registry import DeviceInfo
from homeassistant.helpers.entity_platform import AddEntitiesCallback
from homeassistant.helpers.update_coordinator import CoordinatorEntity

from . import PodConnectConfigEntry
from .api import SpotifyApiError
from .const import DOMAIN, LOGGER
from .coordinator import PodConnectCoordinator

SUPPORTED = (
    MediaPlayerEntityFeature.PLAY
    | MediaPlayerEntityFeature.PAUSE
    | MediaPlayerEntityFeature.NEXT_TRACK
    | MediaPlayerEntityFeature.PREVIOUS_TRACK
    | MediaPlayerEntityFeature.SEEK
    | MediaPlayerEntityFeature.VOLUME_SET
    | MediaPlayerEntityFeature.VOLUME_STEP
    | MediaPlayerEntityFeature.PLAY_MEDIA
    | MediaPlayerEntityFeature.SELECT_SOURCE
    | MediaPlayerEntityFeature.BROWSE_MEDIA
    | MediaPlayerEntityFeature.SHUFFLE_SET
    | MediaPlayerEntityFeature.REPEAT_SET
)

# Spotify repeat_state <-> HA RepeatMode.
_SPOTIFY_TO_HA_REPEAT = {
    "off": RepeatMode.OFF,
    "track": RepeatMode.ONE,
    "context": RepeatMode.ALL,
}
_HA_TO_SPOTIFY_REPEAT = {v: k for k, v in _SPOTIFY_TO_HA_REPEAT.items()}


async def async_setup_entry(
    hass,
    entry: PodConnectConfigEntry,
    async_add_entities: AddEntitiesCallback,
) -> None:
    """Create a media_player for each Connect device, adding new ones as they appear."""
    coordinator = entry.runtime_data.coordinator
    known: set[str] = set()

    @callback
    def _discover() -> None:
        new: list[PodConnectMediaPlayer] = []
        for dev in (coordinator.data or {}).get("devices", []):
            device_id = dev.get("id")
            if device_id and device_id not in known:
                known.add(device_id)
                new.append(
                    PodConnectMediaPlayer(
                        coordinator, entry.entry_id, device_id, dev.get("name") or device_id
                    )
                )
        if new:
            async_add_entities(new)

    _discover()
    entry.async_on_unload(coordinator.async_add_listener(_discover))


class PodConnectMediaPlayer(CoordinatorEntity[PodConnectCoordinator], MediaPlayerEntity):
    """A Spotify Connect device exposed as a HA media_player."""

    _attr_has_entity_name = True
    _attr_name = None
    _attr_supported_features = SUPPORTED
    _attr_media_content_type = MediaType.MUSIC

    def __init__(
        self,
        coordinator: PodConnectCoordinator,
        entry_id: str,
        device_id: str,
        device_name: str,
    ) -> None:
        """Initialise the entity."""
        super().__init__(coordinator)
        self._device_id = device_id
        self._attr_unique_id = f"{entry_id}_{device_id}"
        self._attr_device_info = DeviceInfo(
            identifiers={(DOMAIN, device_id)},
            name=device_name,
            manufacturer="PodConnect",
            model="Spotify Connect device",
        )

    @property
    def _device(self) -> dict | None:
        for dev in (self.coordinator.data or {}).get("devices", []):
            if dev.get("id") == self._device_id:
                return dev
        return None

    @property
    def _playback(self) -> dict | None:
        return (self.coordinator.data or {}).get("playback")

    @property
    def _is_active(self) -> bool:
        pb = self._playback
        return bool(pb and (pb.get("device") or {}).get("id") == self._device_id)

    @property
    def _item(self) -> dict | None:
        return self._playback.get("item") if self._is_active and self._playback else None

    @property
    def state(self) -> MediaPlayerState:
        if not self._is_active:
            return MediaPlayerState.IDLE
        return (
            MediaPlayerState.PLAYING
            if self._playback.get("is_playing")
            else MediaPlayerState.PAUSED
        )

    @property
    def volume_level(self) -> float | None:
        dev = self._device
        vol = dev.get("volume_percent") if dev else None
        return vol / 100 if vol is not None else None

    @property
    def media_title(self) -> str | None:
        return self._item.get("name") if self._item else None

    @property
    def media_artist(self) -> str | None:
        if self._item and self._item.get("artists"):
            return ", ".join(a.get("name") for a in self._item["artists"] if a.get("name"))
        return None

    @property
    def media_album_name(self) -> str | None:
        return (self._item.get("album") or {}).get("name") if self._item else None

    @property
    def media_image_url(self) -> str | None:
        if self._item:
            images = (self._item.get("album") or {}).get("images") or []
            if images:
                return images[0].get("url")
        return None

    @property
    def media_duration(self) -> int | None:
        return self._item.get("duration_ms", 0) // 1000 if self._item else None

    @property
    def media_position(self) -> int | None:
        if self._is_active and self._playback.get("progress_ms") is not None:
            return self._playback["progress_ms"] // 1000
        return None

    @property
    def media_position_updated_at(self):
        return (self.coordinator.data or {}).get("fetched_at") if self._is_active else None

    @property
    def media_content_id(self) -> str | None:
        return self._item.get("uri") if self._item else None

    @property
    def source(self) -> str | None:
        """The Connect device the session is currently on."""
        pb = self._playback
        if pb and pb.get("device"):
            return pb["device"].get("name")
        return None

    @property
    def source_list(self) -> list[str]:
        """All available Connect devices — selecting one moves the session there."""
        return [
            dev["name"]
            for dev in (self.coordinator.data or {}).get("devices", [])
            if dev.get("name")
        ]

    @property
    def shuffle(self) -> bool | None:
        pb = self._playback
        return pb.get("shuffle_state") if (self._is_active and pb) else None

    @property
    def repeat(self) -> RepeatMode | None:
        pb = self._playback
        if not (self._is_active and pb):
            return None
        return _SPOTIFY_TO_HA_REPEAT.get(pb.get("repeat_state"), RepeatMode.OFF)

    # --- control (targets this device) ---
    async def _send(self, action) -> None:
        """Run a player command, tolerating Spotify's "restriction" rejections.

        Spotify returns 403 'Restriction violated' for a command that doesn't match the
        *current* playback state (e.g. pausing what's already paused) — common when our
        polled state is a few seconds stale. Honour the restriction: swallow it and resync
        rather than alarm the user; the end state is almost always what they intended.
        """
        try:
            await action
        except SpotifyApiError as err:
            LOGGER.debug("Spotify rejected a command (resyncing): %s", err)
        await self.coordinator.async_request_refresh()

    async def async_media_play(self) -> None:
        await self._send(self.coordinator.api.play(self._device_id))

    async def async_media_pause(self) -> None:
        await self._send(self.coordinator.api.pause(self._device_id))

    async def async_media_next_track(self) -> None:
        await self._send(self.coordinator.api.next(self._device_id))

    async def async_media_previous_track(self) -> None:
        await self._send(self.coordinator.api.previous(self._device_id))

    async def async_media_seek(self, position: float) -> None:
        await self._send(self.coordinator.api.seek(int(position * 1000), self._device_id))

    async def async_set_volume_level(self, volume: float) -> None:
        await self._send(self.coordinator.api.set_volume(round(volume * 100), self._device_id))

    async def async_set_shuffle(self, shuffle: bool) -> None:
        await self._send(self.coordinator.api.set_shuffle(shuffle, self._device_id))

    async def async_set_repeat(self, repeat: RepeatMode) -> None:
        state = _HA_TO_SPOTIFY_REPEAT.get(repeat, "off")
        await self._send(self.coordinator.api.set_repeat(state, self._device_id))

    async def async_play_media(self, media_type: str, media_id: str, **kwargs) -> None:
        """Play a Spotify URI (track -> uris; album/playlist/artist -> context_uri)."""
        if media_id.startswith("spotify:track:"):
            await self._send(self.coordinator.api.play(self._device_id, uris=[media_id]))
        else:
            await self._send(self.coordinator.api.play(self._device_id, context_uri=media_id))

    async def async_select_source(self, source: str) -> None:
        """"Connect to a device": transfer the session to `source`, keeping play/pause state."""
        device_id = next(
            (
                dev["id"]
                for dev in (self.coordinator.data or {}).get("devices", [])
                if dev.get("name") == source and dev.get("id")
            ),
            None,
        )
        if device_id is None:
            return
        is_playing = bool(self._playback and self._playback.get("is_playing"))
        await self._send(self.coordinator.api.transfer(device_id, play=is_playing))

    async def async_browse_media(
        self, media_content_type: str | None = None, media_content_id: str | None = None
    ) -> BrowseMedia:
        """Browse the user's Spotify playlists — pick one to play on this device."""
        try:
            playlists = await self.coordinator.api.playlists()
        except SpotifyApiError as err:
            LOGGER.warning("Could not load Spotify playlists: %s", err)
            playlists = []

        children = [
            BrowseMedia(
                title=pl.get("name") or "Playlist",
                media_class=MediaClass.PLAYLIST,
                media_content_type=MediaType.PLAYLIST,
                media_content_id=pl["uri"],
                can_play=True,
                can_expand=False,
                thumbnail=(pl.get("images") or [{}])[0].get("url"),
            )
            for pl in playlists
            if pl.get("uri")
        ]
        return BrowseMedia(
            title="Spotify playlists",
            media_class=MediaClass.DIRECTORY,
            media_content_type="playlists",
            media_content_id="root",
            can_play=False,
            can_expand=True,
            children=children,
            children_media_class=MediaClass.PLAYLIST,
        )
