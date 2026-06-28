# PodConnect — Full Spotify Control (implementation spec)

> **Current state (2026-06-28).** This Control spec is largely as-built (OAuth + Web API + media_player
> + search/browse). Two notes: the **Speakers** add-on's multi-room is now **device-aliases only** (one
> engine, rooms in the Spotify Connect menu on one account — not the per-HomePod-engine model implied
> below), and the AI music tools shipped — `media_player.play_media` (free-text → search+play),
> `podconnect.play_from_library`, and the data services `top_tracks`/`recently_played`/`liked`. The
> multi-account Phase 4 stays deferred (now only the different-music-per-room-simultaneously niche).
> Current truth: [`FEATURE-STATUS.md`](FEATURE-STATUS.md) · [`../CHANGELOG.md`](../CHANGELOG.md).

## Goal
Full Spotify cloud control in Home Assistant — search, browse, play, transfer, next/prev,
volume — using **the user's own Spotify Developer app**. No SpotifyPlus, no HA Spotify
integration, no MQTT. PodConnect owns the whole experience.

## Two parts of PodConnect
- **Add-on** (PodConnect Speakers): `go-librespot` + `OwnTone` per HomePod = multi-room Spotify
  Connect speakers + AirPlay 2, with a panel that owns all speaker setup.
- **PodConnect Control** integration (`custom_components/podconnect/`, distributed via **HACS custom
  repo**): OAuth + Spotify Web API control + `media_player` entities + search/browse.

## Status (current)
_Versions live in [`../CHANGELOG.md`](../CHANGELOG.md); this tracks what's built vs pending._

**Built & shipping:**
- **PodConnect Speakers** add-on — go-librespot + OwnTone, single HomePod; **no-typing picker** + live
  AirPlay scan; **`external_volume: true` + the volume relay** (bidirectional HomePod ⇄ Spotify/HA, per
  output, initial-volume cap); **transport sync** (play/pause, flicker-free, rapid-tap-safe); **sharing**
  (account-agnostic **Stop**, **Release**, configurable grace-release, auto-reclaim); **HomePod-name
  forwarding** (auto-name + ghost-free stable id); test tone; watchdog.
- **PodConnect Control** integration (0.7.1) — Application-Credentials OAuth (own dev app);
  device-list-driven `media_player` per Connect device; play/pause/next/prev/seek/volume/**shuffle/
  repeat** + now-playing, **optimistic UI**; **"Connect to a device"** transfer; **search** (incl.
  audiobooks/shows/episodes, popularity-ranked) **+ browse** (playlists, Top Artists/Tracks, Recently
  Played, Liked Songs) so Assist can pick music; graceful "restriction" handling. **One entity per
  HomePod** (the brief 0.7.0 local-speaker player was reverted in 0.7.1). **Control's own state is
  Web-API polling (~10s)** and stays so — see the note below.
- **Multi-room** ("Add speaker" UI + manager) and **push-state** (go-librespot `/events`) **shipped in
  the add-on** (Speakers 0.9.0 / 0.12.0). Push-state lives in the add-on's go-librespot↔OwnTone
  bridge; it makes the *speaker* react instantly, but it does not change how Control polls the cloud.

**Not yet built:** multi-account (Phase 4, optional); track-change buffer-flush (tune on Green).
**Decoupled by design:** Control and Speakers are fully independent — there is no Speakers↔Control
facade to stabilize (the local-entity fold was reverted), so the old `docs/CONTRACT.md` idea is moot.

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

## State updates
- **Inside the add-on (✅ shipped, Speakers 0.12.0):** the go-librespot↔OwnTone **bridge** consumes
  go-librespot's `/events` websocket (with a `/status` poll fallback + 3 s re-seed) so the *speaker*
  reacts to volume/transport/track changes in real time, at zero Web-API quota.
- **Control's HA entities (today):** one `DataUpdateCoordinator` per account, **light Web-API polling
  ~10s** active. This is intentionally kept — Control is the cloud controller; its optimistic UI makes
  commands feel instant, and polling keeps it comfortably inside **dev-mode** rate limits.
- **Commands (play/pause/next/volume/transfer/browse) → Web API:** occasional, uniform, cloud-consistent.
- (A future enhancement could let Control read the add-on's pushed state for HomePod entities, but it's
  not built — and not required: the add-on already reacts instantly on its own.)

## Volume (✅ built — `external_volume: true` + bidirectional reconcile, since 0.21.0)
- go-librespot `external_volume: true` (no PCM scaling — avoids double-attenuation; loudness lives in
  the OwnTone/AirPlay output). The **manager** runs `decideVolume`: go-librespot `/status` ↔ the OwnTone
  per-output volume on one **canonical value** with ±2% tolerance (no echo). **Bidirectional:** the
  Spotify/HA slider moves the HomePod, and a **HomePod hardware button moves Spotify/HA back**. One
  edge-safe one-shot never-loud cap stops a brand-new session starting at full blast.
- **History:** the never-loud *held-window* (0.16–0.17) caused oscillation/blast (R1/R2); 0.20.0
  over-corrected to a one-directional mirror (lost physical volume); **0.21.0** restored the
  bidirectional reconcile while keeping only the simple one-shot cap — physical volume both ways,
  no oscillation, no blast.

## Voice ducking — the Attention API (✅ built, Speakers 0.14.0)
- The add-on exposes **`/api/attention`** so an *external* voice-assistant gatekeeper can dip a room's
  music while it talks, then release it — an **absolute, idempotent, owner+deadline** duck the volume
  relay respects (the duck wins; the reconcile is suspended while held; transport keeps playing
  underneath). A **heartbeat** (re-POST) holds it; stop and it **auto-releases** on the deadline, so a
  crashed agent can't leave the music stuck quiet. Optional `attention_token` guards it.
- **Decoupled by design — the voice agent is a separate project.** PodConnect owns audio + volume;
  the gatekeeper (Gemini Live, Voice PE, VAD, tool-calling) lives in its own repo and consumes only
  this one endpoint. Contract: [`ATTENTION-API.md`](ATTENTION-API.md). Same separation rationale as
  Control ↔ Speakers — different language, runtime, and failure domain; a voice hiccup must never take
  playback down.

## Phases
0. **Add-on reliability — ✅ done:** avahi-readiness wait; single mDNS stack (`zeroconf_backend: avahi`);
   `.metadata` pipe; go-librespot watchdog.
1. **Integration core — ✅ done:** Application-Credentials OAuth (primary account); Web API client;
   device-list-driven `media_player`s; play/pause/next/prev/seek/volume/shuffle/repeat + now-playing +
   "Connect to a device" transfer + optimistic UI. **`external_volume: true` + the volume relay — ✅ done.**
   Control's own state stays ~10s Web-API polling (by design; push-state shipped in the add-on bridge).
2. **Browse & play — ✅ done:** `browse_media` (playlists + Top Artists/Tracks/Recently/Liked) + `search_media`
   (incl. audiobooks/shows/episodes, popularity-ranked) + `play_media`.
3. **HA Assist — ✅ works:** entities exposed → voice transport/volume + search-and-play. Area/alias setup is
   user-side (documented in `AREAS-AND-ASSIST.md`). Account-agnostic stop/release lives in the **add-on
   panel** (+ Siri) — no extra HA entity (the local-entity attempt was reverted in 0.7.1).
4. **Multi-account *(deferred / not built)*:** per-family-member config entries; explicit "from
   <device/account>" routing. NOTE: simultaneous multi-account playback is **not** available under the
   current single-engine alias architecture (it needed the removed per-room engines) — see
   `MULTI-ACCOUNT.md`.
5. **Multi-room "Add speaker" UI + manager — ✅ done in the add-on** (Speakers 0.9.0+); needs on-device
   validation.

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
