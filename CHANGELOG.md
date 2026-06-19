# Changelog

All notable changes to PodConnect. Two components version independently:
**Speakers** (HA add-on, `podconnect/config.yaml`) and **Control** (HACS integration,
`custom_components/podconnect/manifest.json`).

Format loosely follows [Keep a Changelog](https://keepachangelog.com/).

---

## Control 0.7.0 — 2026-06-19
- **Companion folded into Control.** The separate `podconnect_speakers` integration is retired —
  set the **add-on manager URL** in Control's options (Settings → Devices & Services → PodConnect
  Control → Configure; the HA host's LAN IP on `:8099`) and the account-agnostic local controls
  appear: a **local speaker `media_player`** (pause / play / volume) and a **Release HomePod**
  button **per room** (multi-room aware via `/api/rooms`, with a single-room fallback for older
  add-ons). These are distinct devices from the cloud Spotify Connect entities for the same HomePod.
- **Two things to install, not three:** add-on + Control. Control still works **fully without** the
  add-on URL — it stays a pure Spotify controller, no add-on dependency, no errors when the URL is
  unset or unreachable. Adding/clearing the URL reloads the entry to spin local speakers up/down.

## Control 0.6.1 — 2026-06-19
- **Search picks the version people mean.** When several results share the exact title (e.g. the
  many "Den Danske Sommer" songs), ties are now broken by Spotify **popularity** — so the iconic hit
  wins over a niche cover, instead of whichever Spotify returned first. (Gemini just passes the query;
  this is our ranking, not the prompt.)

## Control 0.6.0 — 2026-06-19
- **Audiobooks / podcasts / episodes in search.** Search now includes `audiobook,show,episode`, so
  "play <a children's bedtime story / audiobook / podcast>" resolves to the real audiobook instead of
  a random same-named song. `play_media` routes episodes as track URIs, shows/audiobooks as contexts.
- Pairs with `docs/gemini-system-prompt.md` (Assist confirms *what* it played + says when it can't)
  and the Gemini "Block none" safety fix for the `PROHIBITED_CONTENT` errors.

## Control 0.5.0 — 2026-06-19
- **Profile insights in Browse + Assist.** Browse Spotify by category: Playlists, **Top
  Artists**, **Top Tracks**, **Recently Played**, **Liked Songs** — each item plays directly.
- Adds OAuth scopes `user-top-read`, `user-read-recently-played`, `user-library-read`.
  **Requires a one-time re-authorization** (reauth, or remove + re-add the integration).
- Browse leaves are play targets (no dead-end drill-in).

## Control 0.4.0 — 2026-06-19
- **Spotify search → HA Assist can choose music.** `SEARCH_MEDIA` + `async_search_media`
  backed by `/search`; the built-in media-search intent plays the top hit on the targeted
  Connect device. Results ranked by name match (exact > prefix > substring). No new scope.

## Control 0.3.x — 2026-06-19
- **Shuffle** and **repeat** support, with **optimistic UI** (instant play/pause/shuffle/
  repeat icons; the 10s poll confirms).

## Control 0.2.0 — 2026-06-18
- media_player per Connect device: transport, volume, browse (playlists), play_media,
  select_source (transfer session).

## Control 0.1.x — 2026-06-18
- Initial integration: Application-Credentials OAuth, device discovery, playback state poll.

---

## Speakers 0.9.0 — 2026-06-19  (multi-room — needs on-device validation)
- **Multi-room.** N HomePods, each its own (go-librespot + OwnTone) pair, added live from the panel
  ("Add speaker → pick HomePod") with no add-on restart. Per-room volume/transport/grace all
  generalize (one goroutine each).
- **Architecture change:** the Go manager now **forks & supervises** each room's two child processes
  (os/exec; restart-on-exit + the folded go-librespot hang-watchdog + per-room HomePod selection).
  The s6 services `go-librespot`, `owntone`, `gl-watchdog`, `select-homepod` are **removed** — the
  manager owns them. Children run in their own process group (die-with-parent via Pdeathsig).
- **Back-compat:** on first boot the existing single speaker is migrated to room `r0`, keeping its
  **legacy paths + ports** (`/data/go-librespot` creds + device_id, `/data/owntone` library,
  `/srv/media/spotify`, go-librespot 3678 / OwnTone 3689/3688 — HA's watchdog stays on 3689). New
  rooms (idx ≥ 1) get `/data/rooms/<id>` + ports 37xx/38xx/39xx. Room ids/idx are monotonic.
- New API: `GET /api/rooms`, `POST /api/rooms`, `DELETE /api/rooms/<id>`, `GET /api/discover`;
  `/api/state` stays as the room-0 back-compat view. New unit tests: port allocator, rooms.json
  round-trip, add-room uniqueness, device-id stability.
- ⚠️ **Test on the VM first.** This rips out the s6 audio services in favour of manager supervision;
  the Go compiles + unit-tests green, but process-spawning / mDNS / two-rooms-at-once are only
  verifiable on real hardware. See `docs/GREEN-TESTING.md`.

## Speakers 0.8.0 — 2026-06-19
- **Snappy skips:** OwnTone `start_buffer_ms = 500` (was the default 2250 ms output pre-buffer —
  the cause of the ~2–4 s AirPlay skip lag). Maintainer-blessed floor; raise toward 700–1000 ms if
  underruns appear on a weak network.
- **Manager API for the new companion integration:** `POST /api/play` (resume), `PUT /api/volume`
  `{"volume":0..100}`, and a `volume` field added to `GET /api/state`.

## New: PodConnect Speakers integration 0.1.0 — 2026-06-19
- A second, local custom integration (`custom_components/podconnect_speakers/`) that wraps the
  add-on's HTTP API as a real **`media_player`** (+ a "Release HomePod" button), so the
  **account-agnostic Stop/Pause works via HA Assist voice** ("stop the kitchen") and dashboards, in
  the right Area — independent of which Spotify account is playing. `media_pause`/`media_stop` →
  `/api/stop`, `media_play` → `/api/play`, volume → `/api/volume`.
- Distribution note: HACS allows one integration per repo (that's the Spotify `podconnect`
  Control), so install this one **manually** for now (copy the folder to `config/custom_components/`)
  — a dedicated repo / HACS path is a follow-up. Needs Speakers add-on ≥ 0.8.0 for play/volume.

## Speakers 0.7.0 — 2026-06-19
- **Stop button (account-agnostic).** Panel "⏹ Stop music" + `/api/stop` pause go-librespot
  *locally*, so they stop whoever is playing — including a family member's Spotify the Web API
  can't reach — without giving the HomePod away (distinct from Release).
- **HomePod-name forwarding.** Leave `speaker_name` empty → the Connect speaker + HA entity
  auto-name after the HomePod you pick (e.g. "Køkkenalrum"); applied live (go-librespot bounce).
  Device id is now persisted independently of the name, so renaming never spawns a ghost device.
- **Configurable grace-release** via `grace_minutes` (default 3; 0 = free as soon as idle).
- **Picker now-playing line:** shows ▶ playing (with track) / ⏏ released / ⏸ idle.
- CI: `manager/**` now triggers the image build (the manager compiles into the image).

## Speakers 0.6.0 — 2026-06-19
- **Grace-release ("deling").** Hold the HomePod through brief Siri/notification
  interruptions; free it after 3 min idle for other AirPlay apps; reclaim on resume.
  Manual **"⏏ Release HomePod"** button (local pause + free — works regardless of which
  account is playing).

## Speakers 0.5.x — 2026-06-19
- **Bidirectional volume sync** (Spotify/HA ⇄ HomePod buttons, per-output).
- **Transport sync** (play/pause), flicker-free and rapid-tap-safe via confirm-tracking.

## Speakers 0.4.0 — 2026-06-19
- `external_volume: true` + per-output volume + **initial volume cap** (never full blast).

## Speakers 0.2.x–0.3.x — 2026-06-19
- HomePod picker (no typing); **`AirPlay 2` type fix** (HomePods report type "AirPlay 2");
  night-friendly test tone that names its target HomePod.

## Speakers 0.1.x — 2026-06-18
- Initial add-on: go-librespot + per-room OwnTone, Ingress panel, stable device id.
