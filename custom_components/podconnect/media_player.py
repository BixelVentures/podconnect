"""Media player platform for PodConnect — one entity per Spotify Connect device."""

from __future__ import annotations

import asyncio
import hashlib
import json
import math
import re
import time

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
from homeassistant.helpers import config_validation as cv
from homeassistant.helpers import entity_platform
from homeassistant.helpers.device_registry import DeviceInfo
from homeassistant.helpers.entity_platform import AddEntitiesCallback
from homeassistant.helpers.update_coordinator import CoordinatorEntity

from . import PodConnectConfigEntry
from .api import SpotifyApiError
from .const import DOMAIN, LOGGER
from .coordinator import PodConnectCoordinator
from .native_start import start_guards
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
    "audiobooks": (
        getattr(MediaClass, "AUDIOBOOK", MediaClass.PODCAST),
        getattr(MediaType, "AUDIOBOOK", "audiobook"),
    ),
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
                        coordinator,
                        entry.entry_id,
                        device_id,
                        dev.get("name") or device_id,
                    )
                )
        if new:
            async_add_entities(new)

    _discover()
    entry.async_on_unload(coordinator.async_add_listener(_discover))


class PodConnectMediaPlayer(
    CoordinatorEntity[PodConnectCoordinator], MediaPlayerEntity
):
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
        self._entry_id = entry_id
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
        return (
            self._playback.get("item") if self._is_active and self._playback else None
        )

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
            return ", ".join(
                a.get("name") for a in self._item["artists"] if a.get("name")
            )
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
        return (
            (self.coordinator.data or {}).get("fetched_at") if self._is_active else None
        )

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
                raise HomeAssistantError(
                    f"Spotify rejected the command: {err}"
                ) from err
            raise
        if generation == self._command_generation:
            self._optimistic_poll_floor = self.coordinator.poll_sequence
        await self.coordinator.async_request_refresh()

    async def _native_command(self, action: str, args: dict | None = None) -> bool:
        """Native HA commands enter the same account-owned Control admission once."""
        from .commands import CommandRejected
        from .observations import current
        from .speakers import SpeakersError

        hass = self.coordinator.hass
        data = current(hass, self._entry_id)
        coordinator, api, speakers, commands = (
            data.coordinator,
            data.api,
            data.speakers,
            data.commands,
        )
        entry, target_id = coordinator.config_entry, self._device_id
        epoch, poll = coordinator.owner_epoch, coordinator.poll_sequence
        binding_owner = (data.session, data.observations, api._session, api._web)

        def owned(*, same_poll=True):
            current(hass, self._entry_id, data)
            if (
                data.coordinator is not coordinator
                or coordinator is not self.coordinator
                or data.api is not api
                or coordinator.api is not api
                or data.speakers is not speakers
                or data.commands is not commands
                or hass.config_entries.async_get_entry(self._entry_id) is not entry
                or self._device_id != target_id
                or any(
                    now is not expected
                    for now, expected in zip(
                        (data.session, data.observations, api._session, api._web),
                        binding_owner,
                    )
                )
                or coordinator.owner_epoch != epoch
                or (same_poll and coordinator.poll_sequence != poll)
            ):
                raise HomeAssistantError("PodConnect command owner changed")

        owned()
        if speakers is None:
            return False
        try:
            native_id = await speakers.start_identity()
            owned()
            if self._device_id != native_id:
                return False
            if commands is None:
                raise HomeAssistantError("Native command owner unavailable")
            # Native observations/receipts, not an optimistic HA intent, own state.
            self._optimistic_playing = None
            self._optimistic_shuffle = None
            self._optimistic_repeat = None
            self._optimistic_poll_floor = None
            observed = data.observations.playback(self._entry_id)
            if not observed.get("ok"):
                raise HomeAssistantError("Native playback observation unavailable")
            actual = await commands.room_transport.playback_output(
                hass, self._entry_id, observed
            )
            owned()
            output = actual.get("output", {})
            binding = output.get("binding")
            if (
                actual.get("ok") is not True
                or output.get("status") != "observed"
                or not isinstance(binding, dict)
                or binding.get("target_id") != self._device_id
            ):
                raise HomeAssistantError("Native route unavailable")
            room, homepod = binding["native_room_id"], binding["homepod_id"]
            owner = (
                commands.room_volume if action == "volume" else commands.room_transport
            )
            controls = await owner.read(
                hass, self._entry_id, self._device_id, room, homepod
            )
            owned()
            identity = [self._entry_id, epoch, controls["native"], controls["route"]]
            binding_id = hashlib.sha256(
                json.dumps(
                    identity, sort_keys=True, separators=(",", ":"), allow_nan=False
                ).encode()
            ).hexdigest()
            if binding_id != binding["binding_id"]:
                raise HomeAssistantError("Native route changed")
            sequence = controls["next_command_sequence"]
            if (
                controls["status"] != "ready"
                or action not in controls["actions"]
                or type(sequence) is not int
                or controls["owner_epoch"] != epoch
                or controls["poll_sequence"] != poll
                or time.time() >= binding["valid_until"]
            ):
                raise HomeAssistantError("Native command admission unavailable")
            request = {
                "config_entry_id": self._entry_id,
                "kind": "spotify_device",
                "target_id": self._device_id,
                "owner_epoch": controls["owner_epoch"],
                "expected_poll_sequence": controls["poll_sequence"],
                "command_sequence": sequence,
                "request_id": f"{epoch}:{sequence}",
                "action": action,
                "args": args or {},
            }
            context = {
                key: controls[key]
                for key in ("room_id", "homepod_id", "native", "route")
            }
            if action == "volume":
                context["volume_state"] = controls["volume_state"]
            result = await commands.execute(
                hass,
                request,
                **{"room_volume" if action == "volume" else "room_transport": context},
            )
            # The owner may have completed its own refresh; only its poll may advance.
            owned(same_poll=False)
            if result.get("ok") is not True or result.get("outcome") != "accepted":
                raise HomeAssistantError("Native command rejected or outcome unknown")
            return True
        except (CommandRejected, SpeakersError) as err:
            raise HomeAssistantError("Native command unavailable") from err

    async def async_media_play(self) -> None:
        if await self._native_command("resume"):
            return
        self._optimistic_playing = True
        self.async_write_ha_state()
        guard, native_ready = start_guards(
            self.coordinator.hass,
            self._entry_id,
            self.coordinator.config_entry.runtime_data,
            self._device_id,
            expected_api=self.coordinator.api,
        )
        await self._send(
            self.coordinator.api.play(
                self._device_id,
                execution_guard=guard,
                async_execution_guard=native_ready,
            )
        )

    async def async_media_pause(self) -> None:
        if await self._native_command("pause"):
            return
        self._optimistic_playing = False
        self.async_write_ha_state()
        await self._send(self.coordinator.api.pause(self._device_id))

    async def async_media_next_track(self) -> None:
        if await self._native_command("next"):
            return
        await self._send(self.coordinator.api.next(self._device_id))

    async def async_media_previous_track(self) -> None:
        if await self._native_command("previous"):
            return
        await self._send(self.coordinator.api.previous(self._device_id))

    async def async_media_seek(self, position: float) -> None:
        if (
            type(position) not in (int, float)
            or not math.isfinite(position)
            or not 0 <= position * 1000 < 2**53
        ):
            raise HomeAssistantError("Invalid seek position")
        position_ms = int(position * 1000)
        if await self._native_command("seek", {"position_ms": position_ms}):
            return
        await self._send(self.coordinator.api.seek(position_ms, self._device_id))

    async def async_set_volume_level(self, volume: float) -> None:
        if await self._native_command("volume", {"percent": round(volume * 100)}):
            return
        await self._send(
            self.coordinator.api.set_volume(round(volume * 100), self._device_id)
        )

    async def async_set_shuffle(self, shuffle: bool) -> None:
        if type(shuffle) is not bool:
            raise HomeAssistantError("Invalid shuffle option")
        if await self._native_command("shuffle", {"enabled": shuffle}):
            return
        self._optimistic_shuffle = shuffle
        self.async_write_ha_state()
        await self._send(self.coordinator.api.set_shuffle(shuffle, self._device_id))

    async def async_set_repeat(self, repeat: RepeatMode) -> None:
        if repeat not in _HA_TO_SPOTIFY_REPEAT:
            raise HomeAssistantError("Invalid repeat option")
        mode = _HA_TO_SPOTIFY_REPEAT[repeat]
        if await self._native_command("repeat", {"mode": mode}):
            return
        self._optimistic_repeat = repeat
        self.async_write_ha_state()
        await self._send(
            self.coordinator.api.set_repeat(
                _HA_TO_SPOTIFY_REPEAT.get(repeat, "off"), self._device_id
            )
        )

    async def _native_selected_content(
        self,
        uri: str,
        source,
        *,
        track_uris=None,
        shuffle=False,
        library_source=None,
        library_account=None,
        native_identity=None,
    ) -> bool:
        """Admit selected tracks or contexts through the exact room-content owner."""
        from .commands import CommandRejected
        from .observations import current
        from .speakers import SpeakersError
        from .targets import configured_alias_targets

        hass = self.coordinator.hass
        (
            data,
            coordinator,
            api,
            speakers,
            commands,
            entry,
            target,
            epoch,
            poll,
            binding_owner,
        ) = source

        def owned(*, same_poll=True):
            current(hass, self._entry_id, data)
            if (
                data.coordinator is not coordinator
                or coordinator is not self.coordinator
                or data.api is not api
                or coordinator.api is not api
                or data.speakers is not speakers
                or data.commands is not commands
                or hass.config_entries.async_get_entry(self._entry_id) is not entry
                or (library_account is not None and entry.unique_id != library_account)
                or self._device_id != target
                or any(
                    now is not expected
                    for now, expected in zip(
                        (data.session, data.observations, api._session, api._web),
                        binding_owner,
                    )
                )
                or coordinator.owner_epoch != epoch
                or (same_poll and coordinator.poll_sequence != poll)
            ):
                raise HomeAssistantError("PodConnect content owner changed")

        owned()
        if speakers is None:
            return False
        try:
            native_id = await speakers.start_identity()
            owned()
            if native_identity is not None and native_id != native_identity:
                raise HomeAssistantError("Native library identity changed")
            if target != native_id:
                return False
            if commands is None:
                raise HomeAssistantError("Native content owner unavailable")
            self._optimistic_playing = None
            self._optimistic_shuffle = None
            self._optimistic_repeat = None
            self._optimistic_poll_floor = None
            if track_uris is not None:
                action = "play_tracks"
                content_args = {
                    "track_uris": list(track_uris),
                    "shuffle": shuffle,
                    "library_source": library_source,
                    "max_volume": 100,
                }
            elif re.fullmatch(r"spotify:track:[A-Za-z0-9]{22}", uri):
                action, key = "play_track", "track_uri"
                content_args = {key: uri, "max_volume": 100}
            elif re.fullmatch(r"spotify:episode:[A-Za-z0-9]{22}", uri):
                action, key = "play_episode", "episode_uri"
                content_args = {key: uri, "max_volume": 100}
            elif re.fullmatch(
                r"spotify:(?:playlist|album|artist|show):[A-Za-z0-9]{22}", uri
            ):
                action, key = "play_context", "context_uri"
                content_args = {key: uri, "max_volume": 100}
            else:
                raise HomeAssistantError(
                    "Selected content is unavailable on Native output"
                )

            def selected(rows):
                binding = rows[0]["binding"] if rows else None
                matches = (
                    [
                        row
                        for row in rows
                        if row["target_id"] == binding["room_id"] and row["homepod_id"]
                    ]
                    if binding
                    else []
                )
                if len(matches) != 1:
                    raise HomeAssistantError("Native room unavailable")
                return binding, matches[0]["homepod_id"]

            first = selected(await configured_alias_targets(speakers))
            owned()
            room, homepod = first[0]["room_id"], first[1]
            content = commands.room_content
            controls = await content.read(hass, self._entry_id, target, room, homepod)
            owned()
            sequence = controls["next_command_sequence"]
            if (
                controls["status"] != "ready"
                or action not in controls["actions"]
                or type(sequence) is not int
                or controls["owner_epoch"] != epoch
                or controls["poll_sequence"] != poll
                or controls["room_id"] != room
                or controls["homepod_id"] != homepod
            ):
                raise HomeAssistantError("Native content admission unavailable")
            request = {
                "config_entry_id": self._entry_id,
                "kind": "spotify_device",
                "target_id": target,
                "room_id": room,
                "homepod_id": homepod,
                "owner_epoch": epoch,
                "expected_poll_sequence": poll,
                "command_sequence": sequence,
                "request_id": f"{epoch}:{sequence}",
                "action": action,
                "args": content_args,
                "start_offer": controls["start_offer"],
            }
            prepared = False
            started = False
            try:
                preparation = await content.prepare(hass, request)
                prepared = preparation.get("state") == "staged"
                owned(same_poll=False)
                if not prepared:
                    raise HomeAssistantError("Native content preparation unavailable")
                if selected(await configured_alias_targets(speakers)) != first:
                    raise HomeAssistantError("Native room changed")
                owned(same_poll=False)
                started = True
                receipt = await content.start(hass, request)
                owned(same_poll=False)
                if (
                    receipt.get("ok") is not True
                    or receipt.get("outcome") != "accepted"
                ):
                    raise HomeAssistantError(
                        "Native content rejected or outcome unknown"
                    )
                return True
            finally:
                if prepared and not started:
                    await content.cancel(
                        hass,
                        {
                            "config_entry_id": self._entry_id,
                            "owner_epoch": epoch,
                            "command_sequence": sequence,
                            "request_id": request["request_id"],
                            "offer_id": request["start_offer"]["offer_id"],
                            "binding_id": request["start_offer"]["binding_id"],
                        },
                    )
        except (CommandRejected, SpeakersError) as err:
            raise HomeAssistantError("Native content unavailable") from err

    async def async_play_media(self, media_type: str, media_id: str, **kwargs) -> None:
        """Play a Spotify URI — or, when media_id is NOT a spotify: URI, treat it as a free-text
        QUERY and play the top-ranked match. The query path makes the *standard*
        media_player.play_media service "play by name" in one call (so a generic caller — HA Assist,
        a voice agent, a script — can do play_media music "Dua Lipa" without a separate search step)."""
        from .observations import current

        data = current(self.coordinator.hass, self._entry_id)
        coordinator = self.coordinator
        source = (
            data,
            coordinator,
            self.coordinator.api,
            data.speakers,
            data.commands,
            self.coordinator.config_entry,
            self._device_id,
            coordinator.owner_epoch,
            coordinator.poll_sequence,
            (data.session, data.observations, data.api._session, data.api._web),
        )
        if not media_id.startswith("spotify:"):
            query = media_id
            media_id = await self._search_top_uri(query, media_type) or ""
            if not media_id:
                raise HomeAssistantError(f"No Spotify match for {query!r}")
        if await self._native_selected_content(media_id, source):
            return
        if media_id.startswith(("spotify:track:", "spotify:episode:")):
            guard, native_ready = start_guards(
                self.coordinator.hass,
                self._entry_id,
                self.coordinator.config_entry.runtime_data,
                self._device_id,
                expected_api=self.coordinator.api,
            )
            await self._send(
                self.coordinator.api.play(
                    self._device_id,
                    uris=[media_id],
                    execution_guard=guard,
                    async_execution_guard=native_ready,
                )
            )
        else:
            guard, native_ready = start_guards(
                self.coordinator.hass,
                self._entry_id,
                self.coordinator.config_entry.runtime_data,
                self._device_id,
                expected_api=self.coordinator.api,
            )
            await self._send(
                self.coordinator.api.play(
                    self._device_id,
                    context_uri=media_id,
                    execution_guard=guard,
                    async_execution_guard=native_ready,
                )
            )

    async def async_play_from_library(self, source: str, shuffle: bool = False) -> None:
        """Play one of the user's own collections on THIS device — the AI tool for "play something
        I like" / "play my recent". source: liked | top_tracks | recent. Fetches the tracks and
        plays their URIs (up to 50); optional shuffle."""
        from .observations import current
        from .room_content import canonical_list_track
        from .speakers import SpeakersError

        if source not in {"liked", "top_tracks", "recent"} or type(shuffle) is not bool:
            raise HomeAssistantError("Invalid library selection")
        hass = self.coordinator.hass
        data = current(hass, self._entry_id)
        coordinator, api = self.coordinator, self.coordinator.api
        entry, target = coordinator.config_entry, self._device_id
        account, epoch, poll = (
            entry.unique_id,
            coordinator.owner_epoch,
            coordinator.poll_sequence,
        )
        speakers, commands = data.speakers, data.commands
        binding_owner = (data.session, data.observations, api._session, api._web)
        captured = (
            data,
            coordinator,
            api,
            speakers,
            commands,
            entry,
            target,
            epoch,
            poll,
            binding_owner,
        )

        def owned():
            current(hass, self._entry_id, data)
            if (
                data.coordinator is not coordinator
                or self.coordinator is not coordinator
                or data.api is not api
                or coordinator.api is not api
                or data.speakers is not speakers
                or data.commands is not commands
                or hass.config_entries.async_get_entry(self._entry_id) is not entry
                or entry.unique_id != account
                or not account
                or self._device_id != target
                or coordinator.owner_epoch != epoch
                or any(
                    now is not before
                    for now, before in zip(
                        (data.session, data.observations, api._session, api._web),
                        binding_owner,
                    )
                )
            ):
                raise HomeAssistantError("PodConnect library owner changed")

        owned()
        native_identity = None
        if speakers is not None:
            try:
                native_identity = await speakers.start_identity()
            except SpeakersError as err:
                raise HomeAssistantError("Native library identity unavailable") from err
            owned()
        try:
            if source == "liked":
                items = await api.saved_tracks()
            elif source == "top_tracks":
                items = await api.top_tracks()
            else:
                items = await api.recently_played()
        except SpotifyApiError as err:
            raise HomeAssistantError("Could not read Spotify library") from err
        owned()
        if not isinstance(items, list) or not items:
            raise HomeAssistantError("No playable tracks in Spotify library")
        selected = items[:50]
        if any(
            not isinstance(row, dict) or not canonical_list_track(row.get("uri"))
            for row in selected
        ):
            raise HomeAssistantError("Invalid Spotify library tracks")
        uris = [row["uri"] for row in selected]
        # The library response is metadata, not an execution offer. A routine
        # poll may advance while reading it; admit once against the current
        # observation only after the original account/owner checks above.
        captured = (*captured[:8], coordinator.poll_sequence, binding_owner)
        if await self._native_selected_content(
            "",
            captured,
            track_uris=uris,
            shuffle=shuffle,
            library_source=source,
            library_account=account,
            native_identity=native_identity,
        ):
            return
        owned()
        if shuffle:
            await self._send(api.set_shuffle(True, target))
            owned()
        guard, native_ready = start_guards(
            self.coordinator.hass,
            self._entry_id,
            self.coordinator.config_entry.runtime_data,
            self._device_id,
            expected_api=self.coordinator.api,
        )
        await self._send(
            api.play(
                target,
                uris=uris,
                execution_guard=guard,
                async_execution_guard=native_ready,
            )
        )

    async def _search_top_uri(self, query: str, media_type: str | None) -> str | None:
        """Use the first playable Spotify search result, preserving provider order."""
        type_map = {
            "track": "track",
            "music": "track",
            "song": "track",
            "artist": "artist",
            "album": "album",
            "playlist": "playlist",
            "show": "show",
            "podcast": "show",
            "episode": "episode",
            "audiobook": "audiobook",
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
            or self.coordinator.hass.config_entries.async_get_entry(entry.entry_id)
            is not entry
        ):
            raise HomeAssistantError("PodConnect account entry unavailable")
        try:
            device_id, is_playing = await spotify_transfer_target(
                self.coordinator.api, name=source
            )
        except (SpotifyApiError, ClientError, TimeoutError) as err:
            raise HomeAssistantError("Spotify target lookup failed") from err
        if (
            getattr(entry, "runtime_data", None) is not data
            or not data.active
            or data.coordinator is not self.coordinator
            or self.coordinator.hass.config_entries.async_get_entry(entry.entry_id)
            is not entry
        ):
            raise HomeAssistantError("PodConnect account entry unavailable")
        if is_playing:
            guard, native_ready = start_guards(
                self.coordinator.hass,
                self._entry_id,
                data,
                device_id,
                expected_api=self.coordinator.api,
            )
            await self._send(
                self.coordinator.api.transfer(
                    device_id,
                    play=True,
                    execution_guard=guard,
                    async_execution_guard=native_ready,
                )
            )
        else:
            await self._send(self.coordinator.api.transfer(device_id, play=False))

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
            LOGGER.warning(
                "Could not browse '%s' (re-auth may be needed): %s", category, err
            )
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

        title = next(
            (t for c, t, _ in _BROWSE_CATEGORIES if c == media_content_id), "Spotify"
        )
        child_class = next(
            (cc for c, _, cc in _BROWSE_CATEGORIES if c == media_content_id),
            MediaClass.TRACK,
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
    def _result_item(
        item: dict, media_class: MediaClass, media_type: MediaType
    ) -> BrowseMedia:
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
