"""Media player platform for PodConnect — one entity per Spotify Connect device."""

from __future__ import annotations

import asyncio

import voluptuous as vol
from aiohttp import ClientError

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
from homeassistant.exceptions import HomeAssistantError
from homeassistant.helpers import config_validation as cv, entity_platform
from homeassistant.helpers.device_registry import DeviceInfo
from homeassistant.helpers.entity_platform import AddEntitiesCallback
from homeassistant.helpers.update_coordinator import CoordinatorEntity

from . import PodConnectConfigEntry
from .api import SpotifyApiError
from .const import DOMAIN, LOGGER
from .coordinator import PodConnectCoordinator
from .targets import spotify_transfer_target

# SEARCH_MEDIA + SearchMedia/SearchMediaQuery landed in HA 2025.5. Import defensively so the
# integration still loads on older cores (it just won't advertise search there).
try:
    from homeassistant.components.media_player import SearchMedia, SearchMediaQuery

    _HAS_SEARCH = True
except ImportError:  # pragma: no cover - depends on HA core version
    _HAS_SEARCH = False

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
if _HAS_SEARCH:
    SUPPORTED |= MediaPlayerEntityFeature.SEARCH_MEDIA

# Spotify search-result type -> (HA MediaClass, HA MediaType). Tracks are leaves; the rest are
# containers you can also play (an artist/album/playlist URI is a valid play context).
_SEARCH_KINDS = {
    "tracks": (MediaClass.TRACK, MediaType.TRACK),
    "artists": (MediaClass.ARTIST, MediaType.ARTIST),
    "albums": (MediaClass.ALBUM, MediaType.ALBUM),
    "playlists": (MediaClass.PLAYLIST, MediaType.PLAYLIST),
    # Spoken content so "play <a children's bedtime story / audiobook / podcast>" resolves to the
    # actual audiobook/show — not a random same-named song. (getattr keeps it loading on older HA.)
    "audiobooks": (getattr(MediaClass, "AUDIOBOOK", MediaClass.PODCAST), getattr(MediaType, "AUDIOBOOK", "audiobook")),
    "shows": (MediaClass.PODCAST, MediaType.PODCAST),
    "episodes": (MediaClass.EPISODE, MediaType.EPISODE),
}

# Browse root: (category id, display title, child MediaClass). Each expands to playable leaves.
_BROWSE_CATEGORIES = [
    ("playlists", "Playlists", MediaClass.PLAYLIST),
    ("top_artists", "Top Artists", MediaClass.ARTIST),
    ("top_tracks", "Top Tracks", MediaClass.TRACK),
    ("recent", "Recently Played", MediaClass.TRACK),
    ("liked", "Liked Songs", MediaClass.TRACK),
]

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

    # Taste/history "play something I like" tool for an AI assist (and the UI): play the user's
    # Liked Songs, top tracks, or recently played on a speaker. Additive — doesn't touch playback.
    entity_platform.async_get_current_platform().async_register_entity_service(
        "play_from_library",
        {
            vol.Required("source"): vol.In(["liked", "top_tracks", "recent"]),
            vol.Optional("shuffle", default=False): cv.boolean,
        },
        "async_play_from_library",
    )

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
        # Optimistic play/pause: HA's state comes from a 10s Spotify poll, so the card's
        # play/pause icon lags the (instant) audio. Show the user's intent immediately, then let
        # the poll confirm. None = no pending guess.
        self._optimistic_playing: bool | None = None
        self._optimistic_shuffle: bool | None = None
        self._optimistic_repeat: RepeatMode | None = None
        self._optimistic_poll_floor: int | None = None
        self._command_generation = 0
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
        playing = self._playback.get("is_playing")
        if self._optimistic_playing is not None:
            playing = self._optimistic_playing  # show intent until the poll confirms
        return MediaPlayerState.PLAYING if playing else MediaPlayerState.PAUSED

    @callback
    def _handle_coordinator_update(self) -> None:
        """Let a successful poll begun after the command replace optimistic intent."""
        sequence = (self.coordinator.data or {}).get("poll_sequence", 0)
        if (
            self.coordinator.last_update_success
            and self._optimistic_poll_floor is not None
            and sequence > self._optimistic_poll_floor
        ):
            self._optimistic_playing = None
            self._optimistic_shuffle = None
            self._optimistic_repeat = None
            self._optimistic_poll_floor = None
        super()._handle_coordinator_update()

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
        if self._optimistic_shuffle is not None:
            return self._optimistic_shuffle
        pb = self._playback
        return pb.get("shuffle_state") if (self._is_active and pb) else None

    @property
    def repeat(self) -> RepeatMode | None:
        if self._optimistic_repeat is not None:
            return self._optimistic_repeat
        pb = self._playback
        if not (self._is_active and pb):
            return None
        return _SPOTIFY_TO_HA_REPEAT.get(pb.get("repeat_state"), RepeatMode.OFF)

    # --- control (targets this device) ---
    async def _send(self, action) -> None:
        """Keep command intent only until newer authoritative playback data arrives."""
        self._command_generation += 1
        generation = self._command_generation
        self._optimistic_poll_floor = None  # command still pending
        try:
            await action
        except (Exception, asyncio.CancelledError) as err:
            if generation == self._command_generation:
                self._optimistic_playing = None
                self._optimistic_shuffle = None
                self._optimistic_repeat = None
                self.async_write_ha_state()
            if isinstance(err, asyncio.CancelledError):
                raise
            await self.coordinator.async_request_refresh()
            if isinstance(err, SpotifyApiError):
                raise HomeAssistantError(f"Spotify rejected the command: {err}") from err
            raise
        if generation == self._command_generation:
            self._optimistic_poll_floor = self.coordinator.poll_sequence
        await self.coordinator.async_request_refresh()

    async def async_media_play(self) -> None:
        self._optimistic_playing = True
        self.async_write_ha_state()
        await self._send(self.coordinator.api.play(self._device_id))

    async def async_media_pause(self) -> None:
        self._optimistic_playing = False
        self.async_write_ha_state()
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
        self._optimistic_shuffle = shuffle
        self.async_write_ha_state()
        await self._send(self.coordinator.api.set_shuffle(shuffle, self._device_id))

    async def async_set_repeat(self, repeat: RepeatMode) -> None:
        self._optimistic_repeat = repeat
        self.async_write_ha_state()
        await self._send(self.coordinator.api.set_repeat(_HA_TO_SPOTIFY_REPEAT.get(repeat, "off"), self._device_id))

    async def async_play_media(self, media_type: str, media_id: str, **kwargs) -> None:
        """Play a Spotify URI — or, when media_id is NOT a spotify: URI, treat it as a free-text
        QUERY and play the top-ranked match. The query path makes the *standard*
        media_player.play_media service "play by name" in one call (so a generic caller — HA Assist,
        a voice agent, a script — can do play_media music "Dua Lipa" without a separate search step)."""
        if not media_id.startswith("spotify:"):
            query = media_id
            media_id = await self._search_top_uri(query, media_type) or ""
            if not media_id:
                raise HomeAssistantError(f"No Spotify match for {query!r}")
        if media_id.startswith(("spotify:track:", "spotify:episode:")):
            await self._send(self.coordinator.api.play(self._device_id, uris=[media_id]))
        else:
            await self._send(self.coordinator.api.play(self._device_id, context_uri=media_id))

    async def async_play_from_library(self, source: str, shuffle: bool = False) -> None:
        """Play one of the user's own collections on THIS device — the AI tool for "play something
        I like" / "play my recent". source: liked | top_tracks | recent. Fetches the tracks and
        plays their URIs (up to 50); optional shuffle."""
        api = self.coordinator.api
        try:
            if source == "liked":
                items = await api.saved_tracks()
            elif source == "top_tracks":
                items = await api.top_tracks()
            else:  # "recent"
                items = await api.recently_played()
        except SpotifyApiError as err:
            raise HomeAssistantError(f"Could not read Spotify library: {err}") from err
        uris = [it["uri"] for it in items if it and it.get("uri")][:50]
        if not uris:
            raise HomeAssistantError(f"No playable tracks in Spotify library {source!r}")
        if shuffle:
            await self._send(self.coordinator.api.set_shuffle(True, self._device_id))
        await self._send(self.coordinator.api.play(self._device_id, uris=uris))

    async def _search_top_uri(self, query: str, media_type: str | None) -> str | None:
        """Use the first playable Spotify search result, preserving provider order."""
        type_map = {
            "track": "track", "music": "track", "song": "track",
            "artist": "artist", "album": "album", "playlist": "playlist",
            "show": "show", "podcast": "show", "episode": "episode", "audiobook": "audiobook",
        }
        want = type_map.get((media_type or "").lower(), "track")
        try:
            data = await self.coordinator.api.search(query, want)
        except SpotifyApiError as err:
            raise HomeAssistantError(f"Spotify search failed: {err}") from err
        for item in (data.get(want + "s") or {}).get("items", []):
            if item and item.get("uri"):
                return item["uri"]
        return None

    async def async_select_source(self, source: str) -> None:
        """ "Connect to a device": transfer the session to `source`, keeping play/pause state."""
        entry = self.coordinator.config_entry
        data = getattr(entry, "runtime_data", None)
        if (
            data is None
            or not data.active
            or data.coordinator is not self.coordinator
            or self.coordinator.hass.config_entries.async_get_entry(entry.entry_id) is not entry
        ):
            raise HomeAssistantError("PodConnect account entry unavailable")
        try:
            device_id, is_playing = await spotify_transfer_target(self.coordinator.api, name=source)
        except (SpotifyApiError, ClientError, TimeoutError) as err:
            raise HomeAssistantError("Spotify target lookup failed") from err
        if (
            getattr(entry, "runtime_data", None) is not data
            or not data.active
            or data.coordinator is not self.coordinator
            or self.coordinator.hass.config_entries.async_get_entry(entry.entry_id) is not entry
        ):
            raise HomeAssistantError("PodConnect account entry unavailable")
        await self._send(self.coordinator.api.transfer(device_id, play=is_playing))

    async def _browse_category(self, category: str) -> list[BrowseMedia]:
        """Fetch one profile category as playable leaves."""
        api = self.coordinator.api
        try:
            if category == "playlists":
                items = await api.playlists()
                kind = (MediaClass.PLAYLIST, MediaType.PLAYLIST)
            elif category == "top_artists":
                items = await api.top_artists()
                kind = (MediaClass.ARTIST, MediaType.ARTIST)
            elif category == "top_tracks":
                items = await api.top_tracks()
                kind = (MediaClass.TRACK, MediaType.TRACK)
            elif category == "recent":
                items = await api.recently_played()
                kind = (MediaClass.TRACK, MediaType.TRACK)
            elif category == "liked":
                items = await api.saved_tracks()
                kind = (MediaClass.TRACK, MediaType.TRACK)
            else:
                return []
        except SpotifyApiError as err:
            # 403 here usually means the token predates the profile scopes — re-auth needed.
            LOGGER.warning("Could not browse '%s' (re-auth may be needed): %s", category, err)
            return []
        return [self._result_item(it, *kind) for it in items if it and it.get("uri")]

    async def async_browse_media(
        self, media_content_type: str | None = None, media_content_id: str | None = None
    ) -> BrowseMedia:
        """Browse Spotify by category (playlists + your top/recent/liked) — pick one to play."""
        if media_content_id in (None, "root"):
            children = [
                BrowseMedia(
                    title=title,
                    media_class=MediaClass.DIRECTORY,
                    media_content_type=cid,
                    media_content_id=cid,
                    can_play=False,
                    can_expand=True,
                    children_media_class=child_class,
                )
                for cid, title, child_class in _BROWSE_CATEGORIES
            ]
            return BrowseMedia(
                title="Spotify",
                media_class=MediaClass.DIRECTORY,
                media_content_type="root",
                media_content_id="root",
                can_play=False,
                can_expand=True,
                children=children,
                children_media_class=MediaClass.DIRECTORY,
            )

        title = next((t for c, t, _ in _BROWSE_CATEGORIES if c == media_content_id), "Spotify")
        child_class = next(
            (cc for c, _, cc in _BROWSE_CATEGORIES if c == media_content_id), MediaClass.TRACK
        )
        return BrowseMedia(
            title=title,
            media_class=MediaClass.DIRECTORY,
            media_content_type=media_content_id or "root",
            media_content_id=media_content_id or "root",
            can_play=False,
            can_expand=True,
            children=await self._browse_category(media_content_id),
            children_media_class=child_class,
        )

    @staticmethod
    def _result_item(item: dict, media_class: MediaClass, media_type: MediaType) -> BrowseMedia:
        """Map one Spotify object to a playable BrowseMedia (its URI is the play target)."""
        title = item.get("name") or "?"
        if media_type == MediaType.TRACK and item.get("artists"):
            artists = ", ".join(a.get("name") for a in item["artists"] if a.get("name"))
            if artists:
                title = f"{title} — {artists}"
        # Tracks carry their cover on the album; artists/albums/playlists carry their own.
        images = item.get("images") or (item.get("album") or {}).get("images") or []
        return BrowseMedia(
            title=title,
            media_class=media_class,
            media_content_type=media_type,
            media_content_id=item["uri"],
            can_play=True,
            # Leaves are play targets, not folders: a playlist/album/artist URI is played
            # directly (context_uri), so don't offer a drill-in that we don't serve.
            can_expand=False,
            thumbnail=images[0].get("url") if images else None,
        )

    async def async_search_media(self, query: SearchMediaQuery) -> SearchMedia:
        """Search Spotify, preserving provider order within each requested result type."""
        # Include spoken content (audiobooks/shows/episodes) so bedtime stories etc. resolve to the
        # real audiobook, not a same-named song.
        types = "track,artist,album,playlist,show,episode,audiobook"
        if query.media_filter_classes:
            wanted = {
                MediaClass.TRACK: "track",
                MediaClass.ARTIST: "artist",
                MediaClass.ALBUM: "album",
                MediaClass.PLAYLIST: "playlist",
                MediaClass.PODCAST: "show",
                MediaClass.EPISODE: "episode",
            }
            sel = [wanted[c] for c in query.media_filter_classes if c in wanted]
            if sel:
                types = ",".join(sel)
        try:
            data = await self.coordinator.api.search(query.search_query, types)
        except SpotifyApiError as err:
            raise HomeAssistantError(f"Spotify search failed: {err}") from err

        results = []
        for key, (media_class, media_type) in _SEARCH_KINDS.items():
            for item in (data.get(key) or {}).get("items", []):
                if item and item.get("uri"):
                    results.append(self._result_item(item, media_class, media_type))
        return SearchMedia(result=results)
