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
- **Multi-room:** several HomePods, each its own independent Connect speaker. The sidebar **panel**
  is the room manager — **Add / Remove / Rename** speakers and **⚙ per-room Settings**, all live with
  no add-on restart. The manager forks & supervises each room's own (go-librespot + OwnTone) pair.
- **Pick your HomePod with no typing:** the panel shows a live network scan — click and save. Each
  speaker **auto-names itself** after its HomePod.
- **Self-healing naming:** a room is bound to its HomePod by a stable id, so renaming the HomePod in
  Apple Home **syncs everywhere automatically** (Connect device + HA entity) — no re-pick.
- **Per-room grace + bitrate** in the panel (empty = inherit the global add-on default).
- **Bidirectional volume sync** — the Spotify/HA slider and the HomePod's hardware buttons move
  together (per-output AirPlay level), and **no fresh session can start at full blast** (the cap is
  proactive and re-armed per session, so even a second account's new session stays capped).
- **Transport sync** — a Spotify pause stops the HomePod instantly (beating the AirPlay buffer);
  a HomePod top-tap pauses/resumes Spotify. Flicker-free, rapid-tap-safe.
- **Snappy skips** (`start_buffer_ms = 500`) and **instant push-state** — the bridge reads
  go-librespot's `/events` websocket (with a `/status` poll fallback), so volume/transport/track
  changes register as they happen.
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

## Roadmap (planned — not yet built)

- **On-device validation** of multi-room on real hardware (process spawning / mDNS / two rooms at
  once) — the code ships; the Green is the proving ground.
- **Track-change buffer-flush** (sub-second skips) — unblocked by push-state's track-change signal,
  still tuned on the wired Green to avoid underruns.
- **Multi-account** (optional): multi-account *playback* already works for free via Spotify Connect;
  the deferred build only adds HA-level cross-account visibility/control. **Synchronized same-music
  groups** across rooms are a separate, not-yet-built feature.
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
