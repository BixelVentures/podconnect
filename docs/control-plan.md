# PodConnect — Full Spotify Control (implementation spec)

## Goal
Full Spotify cloud control in Home Assistant — search, browse, play, transfer, next/prev,
volume — using **the user's own Spotify Developer app**. No SpotifyPlus, no HA Spotify
integration, no MQTT. PodConnect owns the whole experience.

## Two parts of PodConnect
- **Add-on** (exists): `go-librespot` + `OwnTone` per HomePod = Spotify Connect speakers + AirPlay 2.
- **PodConnect integration** (new, `custom_components/podconnect/`, distributed via **HACS custom repo**):
  OAuth + Spotify Web API control + `media_player` entities + media browsing.

## Account model (Family plan)
- Spotify **Web API is per-account**; a Family plan = separate independent accounts.
- "**One account owns a speaker at a time**" is standard Spotify Connect behavior — not our limitation.
- Each family member authorizes **separately** (HA multiple config entries; dev app User Management
  allows 5). Each account is surfaced separately; whoever owns a speaker controls it.
- **Phasing:** single (primary) account first; architect for multiple config entries from day one.

## Voice / Assist account model
- HA Assist has **no per-voice identity** → voice runs through a configurable **primary account**.
- Multi-account later: **explicit naming routes the account**, e.g. "play in the living room **from
  the iPad**" → maps the named source to that Spotify account. The account is addressable in the command.

## Device model (device-list-driven)
- Read `/me/player/devices` → all of the account's Connect devices.
- **PodConnect HomePods = first-class managed `media_player` entities** (+ AirPlay volume relay).
- **Other Connect devices** (MacBook, phone, …) = available **playback targets** → "find Connect in HA" free.

## State updates (push-first, NOT poll-first)
- **HomePods → go-librespot `/events` websocket (push):** real-time now-playing / play-pause / volume /
  position; **zero Web-API quota**. HA extrapolates the progress bar from position+timestamp.
- **Foreign devices → light Web-API polling:** one `DataUpdateCoordinator` per account (poll once, fan
  out), ~10s active / ~30s idle.
- **Commands (play/pause/next/volume/transfer/browse) → Web API:** occasional, uniform, cloud-consistent.
- Rationale: keeps us comfortably inside **dev-mode** rate limits and gives instant UI.

## Volume
- go-librespot `external_volume: true` (no PCM scaling). **Volume-relay in the add-on** watches
  go-librespot `/events`/local API → sets OwnTone/AirPlay output volume → HomePod's real level follows.
  Lives in the add-on for locality + independence from HA. Bidirectional (OwnTone→go-librespot) = stretch.

## Phases
0. **Add-on reliability (0.1.3) — current:** wait for avahi ready before OwnTone/go-librespot; single
   mDNS stack (`zeroconf_backend: avahi`); create `.metadata` pipe. *Volume stays as-is (external_volume
   false) — not touched here.*
1. **Integration core:** Application-Credentials OAuth (primary account); Web API client; device-list-driven
   `media_player`(s) — HomePods managed (push state via go-librespot events), others as targets;
   play/pause/next/prev/seek/volume + now-playing. **Ships `external_volume: true` + the volume relay.**
2. **Browse & play:** `browse_media` (playlists/albums/search) + `play_media`.
3. **HA Assist:** expose entities → voice transport/volume via primary account + "play [content] on [device]".
4. **Multi-account:** per-family-member config entries; explicit "from <device/account>" routing.
5. **Multi-room "Add speaker" UI + manager** (later).

## Repo / distribution
```
custom_components/podconnect/   manifest.json · config_flow.py · application_credentials.py
                                __init__.py · media_player.py · api.py · const.py
hacs.json                       makes the repo a HACS-installable integration
podconnect/                     add-on changes (mDNS/avahi fixes; later external_volume + volume-relay)
```
Install: add this GitHub repo as a **HACS custom repository** → install the PodConnect integration.

## Verify at implementation time (knowledge may be stale)
- HA **Application Credentials** + `config_entry_oauth2_flow` current API.
- Spotify **Web API** endpoints/scopes (`user-read-playback-state`, `user-modify-playback-state`,
  `playlist-read-private`, …), device-transfer + volume params, dev-mode rate limits.
- go-librespot `/events` payloads (metadata fields, volume scale `0..max`), token refresh via HA `OAuth2Session`.
