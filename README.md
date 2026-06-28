# PodConnect

Turn an Apple **HomePod** into a **Spotify Connect speaker**, and control your Spotify from
**Home Assistant** — using your own Spotify developer app. PodConnect is **two cooperating
halves**, both installed from this one repo.

```
Spotify app / Home Assistant ─► go-librespot (Spotify Connect) ─► pipe ─► OwnTone ─► AirPlay 2 ─► HomePod
```

## What works today

**PodConnect Speakers** — the Home Assistant **add-on** (`podconnect/`)
- Turns a HomePod into a **Spotify Connect speaker**: `go-librespot` (Connect receiver) → a
  named pipe → `OwnTone` (AirPlay 2 sender) → HomePod.
- **Multi-room on ONE Spotify account — zero setup.** A single engine advertises **all your rooms as
  separate selectable devices in the Spotify Connect menu, on your one account, with clean audio**.
  Pick a room in the Spotify app and the audio **moves** there (~1-2 s, just AirPlay's switch). This uses
  Spotify's own device-aliases (multiroom zones) — see [`docs/ALIASES-PROBE.md`](docs/ALIASES-PROBE.md).
  Add your HomePods in the panel and they each appear as a room; nothing to enable.
  *It's one stream — one room at a time (picking a room moves the music; PodConnect doesn't play two
  rooms, or two accounts, at once). Different-music-per-room and synced groups are not PodConnect
  features — see [`docs/MULTI-ACCOUNT.md`](docs/MULTI-ACCOUNT.md).*
- The sidebar **panel** is the room manager — **Add / Remove / Rename** speakers and **⚙ per-room
  Settings**, all live with no add-on restart.
- **Pick your HomePod with no typing:** the panel shows a live network scan — click and save. Each
  speaker **auto-names itself** after its HomePod.
- **Self-healing naming:** a room is bound to its HomePod by a stable id, so renaming the HomePod in
  Apple Home **syncs everywhere automatically** (Connect device + HA entity) — no re-pick.
- **Per-room grace + bitrate** in the panel (empty = inherit the global add-on default).
- **Bidirectional volume sync** — the Spotify/HA slider and the HomePod's hardware buttons move
  together (per-output AirPlay level). **No fresh session starts at full blast:** the device advertises
  a sane `initial_volume` (so the Spotify slider reads ~35%, not 100%, on a fresh claim) and a
  proactive per-session cap re-arms even across a transfer to another device.
- **Transport sync** — a Spotify pause stops the HomePod instantly (beating the AirPlay buffer);
  a HomePod top-tap pauses/resumes Spotify. Flicker-free, rapid-tap-safe.
- **Snappy skips** (tunable `buffer_ms`, default 500) and **instant push-state** — the bridge reads
  go-librespot's `/events` websocket (with a `/status` poll fallback), so volume/transport/track and
  **room-alias** changes register as they happen.
- **Sharing ("deling"):** **⏹ Stop** pauses whoever is playing (any account, local); **⏏ Release**
  frees the HomePod for other AirPlay apps (Mofibo, Apple Music). Auto-release after an idle grace
  period, auto-reclaim on resume.
- **Voice-duck Attention API** (`/api/attention`) — an external voice-assistant gatekeeper can dip a
  room's music while it talks and let it back up, without fighting the volume sync. The duck wins
  while held and **auto-releases** if the agent stops (heartbeat + deadline), so the music can't get
  stuck quiet. Optional shared-secret guard. Contract: [`docs/ATTENTION-API.md`](docs/ATTENTION-API.md).
- Built-in **test tone**, go-librespot watchdog. Installed via the HA **Add-on Store** (prebuilt
  image, aarch64 + amd64 — no on-device build).

**PodConnect Control** — the Home Assistant **integration** (`custom_components/podconnect/`)
- Sign in to **Spotify** with your **own developer app** (via HA Application Credentials) — no
  SpotifyPlus, no built-in Spotify integration.
- Creates a `media_player` **for each of your Spotify Connect devices** (the HomePod speaker,
  plus any other Connect device — a MacBook, a car, a phone).
- Controls: **play / pause / next / previous / seek / volume / shuffle / repeat / now-playing**,
  with **optimistic UI** (icons react instantly), plus **"Connect to a device"** (a real Spotify
  *Transfer Playback* handoff).
- **Search + Browse** your Spotify in HA — search, Playlists, Top Artists, Top Tracks, Recently
  Played, Liked Songs — so **HA Assist can pick music** ("spil noget afslappende i køkkenet").
  Search includes **audiobooks / shows / episodes** and breaks same-title ties by **popularity**.
- **One entity per HomePod** (pure Spotify control — no duplicate local-speaker player).
- **AI / voice music tools:** `media_player.play_media` accepts a free-text name (search + play top
  result); the `podconnect.play_from_library` service plays your Liked / Top / Recent; and the
  response-returning services `podconnect.top_tracks` / `recently_played` / `liked` let an assistant
  **fetch** your listening history as data (`{tracks:[{name,artist,uri}]}`) and choose a track —
  built on Control's own auth, callable over REST.
- State via the Spotify Web API, polled ~10s. Installed via **HACS** (custom repository).

See [`CHANGELOG.md`](CHANGELOG.md) for the current version of each half and what changed.

## Install

- **Speakers (add-on):** see [`podconnect/DOCS.md`](podconnect/DOCS.md).
- **Control (integration):** add this repo as a **HACS custom repository** (type: *Integration*)
  → install **PodConnect Control** → restart HA → **Settings → Devices & Services → Add
  Integration → PodConnect Control** → enter your Spotify Client ID/Secret and sign in.
- **Rooms + voice:** see [`docs/AREAS-AND-ASSIST.md`](docs/AREAS-AND-ASSIST.md) and the quick
  [`docs/GETTING-STARTED.md`](docs/GETTING-STARTED.md).

**Account-agnostic stop/release** lives in the **add-on panel** (⏹ Stop / ⏏ Release) and via Siri —
they pause whoever is playing, regardless of which Spotify account owns the speaker. HA Assist's
"stop music" / "pause the kitchen" already pauses *your own* session via the Spotify entity.

## Roadmap

**Done & working on real hardware (June 2026):**
- **Device-aliases multi-room** — multiple rooms in the Spotify Connect menu on **one account**, clean
  audio, instant room routing. Proven on-device; it's the **default** (the per-room multi-engine model
  and the `persistent_connect` experiment were removed — aliases are the one way).
- **Sane-start volume** (slider reads the real level, never 100%) and the never-loud cap.
- **Slim multi-stage image** + **graceful go-librespot restart** (no duplicate Connect entries) +
  avahi host-name pin (no mDNS rename churn).

**Planned / nice-to-have:**
- Surface the alias rooms more clearly in the panel; re-wire self-healing-on-rename for alias mode.
- **Track-change buffer-flush** (sub-second skips) — the floor is AirPlay's ~2 s; `buffer_ms` is the
  current knob.
- **Synchronized same-music groups** across rooms (one source → many HomePods at once) — a separate,
  not-yet-built feature (OwnTone multi-output is the likely path).
- Picker visual polish.

See [`docs/TODO.md`](docs/TODO.md) (living roadmap), [`docs/PLAN.md`](docs/PLAN.md) (architecture)
and [`docs/control-plan.md`](docs/control-plan.md) (integration spec).

## Repository layout

```
podconnect/                    add-on "PodConnect Speakers" (go-librespot + OwnTone + manager/panel)
custom_components/podconnect/  integration "PodConnect Control" (Spotify OAuth + Web API + entities)
hacs.json                      makes this repo a HACS-installable integration
repository.yaml                makes this repo a Home Assistant add-on repository
CHANGELOG.md                   per-half version history
docs/                          PLAN · TODO · control-plan · AREAS-AND-ASSIST · GREEN-TESTING · releasing
.github/workflows/             CI that builds & publishes the add-on image to GHCR
```

## Versioning & updates

Two halves, **independent versions, one repo** — you just click **Update** when HACS (Control)
or the Add-on Store (Speakers) shows one. Details in [`docs/releasing.md`](docs/releasing.md).

## Built on

[go-librespot](https://github.com/devgianlu/go-librespot) (Spotify Connect) ·
[OwnTone](https://github.com/owntone/owntone-server) (AirPlay 2) ·
[Home Assistant](https://www.home-assistant.io/)
