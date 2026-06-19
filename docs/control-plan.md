# PodConnect — Full Spotify Control (implementation spec)

## Goal
Full Spotify cloud control in Home Assistant — search, browse, play, transfer, next/prev,
volume — using **the user's own Spotify Developer app**. No SpotifyPlus, no HA Spotify
integration, no MQTT. PodConnect owns the whole experience.

## Two parts of PodConnect
- **Add-on** (exists): `go-librespot` + `OwnTone` per HomePod = Spotify Connect speakers + AirPlay 2.
- **PodConnect integration** (new, `custom_components/podconnect/`, distributed via **HACS custom repo**):
  OAuth + Spotify Web API control + `media_player` entities + media browsing.

## Status (current)
_Versions live in [`../CHANGELOG.md`](../CHANGELOG.md); this tracks what's built vs pending._

**Built & shipping:**
- **PodConnect Speakers** add-on — go-librespot + OwnTone, single HomePod; **no-typing picker** + live
  AirPlay scan; **`external_volume: true` + the volume relay** (bidirectional HomePod ⇄ Spotify/HA, per
  output, initial-volume cap); **transport sync** (play/pause, flicker-free, rapid-tap-safe); **sharing**
  (account-agnostic **Stop**, **Release**, configurable grace-release, auto-reclaim); **HomePod-name
  forwarding** (auto-name + ghost-free stable id); test tone; watchdog.
- **PodConnect Control** integration — Application-Credentials OAuth (own dev app); device-list-driven
  `media_player` per Connect device; play/pause/next/prev/seek/volume/**shuffle/repeat** + now-playing,
  **optimistic UI**; **"Connect to a device"** transfer; **search + browse** (playlists, Top Artists/Tracks,
  Recently Played, Liked Songs) so Assist can pick music; graceful "restriction" handling.
  **State is Web-API polling (~10s)** — the "push-first" design below is the *target*, not yet built.

**Not yet built:** go-librespot push state (still polling); voice stop/release via MQTT `media_player`;
multi-account (Phase 4); multi-room "Add speaker" UI (Phase 5); track-change buffer-flush (tune on Green).

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

## State updates (TARGET design — today it polls; push not yet built)
- **HomePods → go-librespot `/events` websocket (push):** real-time now-playing / play-pause / volume /
  position; **zero Web-API quota**. HA extrapolates the progress bar from position+timestamp.
- **Foreign devices → light Web-API polling:** one `DataUpdateCoordinator` per account (poll once, fan
  out), ~10s active / ~30s idle.
- **Commands (play/pause/next/volume/transfer/browse) → Web API:** occasional, uniform, cloud-consistent.
- Rationale: keeps us comfortably inside **dev-mode** rate limits and gives instant UI.

## Volume (✅ built — `external_volume: true` + bidirectional relay)
- go-librespot `external_volume: true` (no PCM scaling). The **manager** (in the add-on) reconciles
  go-librespot `/status` ↔ the OwnTone/AirPlay **per-output** volume on a tick, with canonical-value
  loop-protection. **Bidirectional:** HomePod hardware buttons move Spotify/HA and vice-versa; a fresh
  session is capped so it never starts at full blast. Lives in the add-on for locality + independence.

## Phases
0. **Add-on reliability — ✅ done:** avahi-readiness wait; single mDNS stack (`zeroconf_backend: avahi`);
   `.metadata` pipe; go-librespot watchdog.
1. **Integration core — ✅ done:** Application-Credentials OAuth (primary account); Web API client;
   device-list-driven `media_player`s; play/pause/next/prev/seek/volume/shuffle/repeat + now-playing +
   "Connect to a device" transfer + optimistic UI. **`external_volume: true` + the volume relay — ✅ done.**
   **Still pending:** push state (go-librespot events) — still ~10s polling.
2. **Browse & play — ✅ done:** `browse_media` (playlists + Top Artists/Tracks/Recently/Liked) + `search_media`
   + `play_media`.
3. **HA Assist — ✅ works:** entities exposed → voice transport/volume + search-and-play. Area/alias setup is
   user-side (documented in `AREAS-AND-ASSIST.md`). Account-agnostic voice stop/release via MQTT = pending.
4. **Multi-account:** per-family-member config entries; explicit "from <device/account>" routing. *(pending)*
5. **Multi-room "Add speaker" UI + manager.** *(pending)*

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
