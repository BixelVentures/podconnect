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
- **Pick your HomePod with no typing:** a sidebar panel shows a live network scan — click and save.
  The speaker can **auto-name itself** after the HomePod you pick.
- **Bidirectional volume sync** — the Spotify/HA slider and the HomePod's hardware buttons move
  together (per-output AirPlay level), and a fresh session never starts at full blast.
- **Transport sync** — a Spotify pause stops the HomePod instantly (beating the AirPlay buffer);
  a HomePod top-tap pauses/resumes Spotify. Flicker-free, rapid-tap-safe.
- **Sharing ("deling"):** **⏹ Stop** pauses whoever is playing (any account, local); **⏏ Release**
  frees the HomePod for other AirPlay apps (Mofibo, Apple Music). Auto-release after an idle grace
  period, auto-reclaim on resume.
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
- State via the Spotify Web API, polled ~10s. Installed via **HACS** (custom repository).

See [`CHANGELOG.md`](CHANGELOG.md) for the current version of each half and what changed.

## Install

- **Speakers (add-on):** see [`podconnect/DOCS.md`](podconnect/DOCS.md).
- **Control (integration):** add this repo as a **HACS custom repository** (type: *Integration*)
  → install **PodConnect Control** → restart HA → **Settings → Devices & Services → Add
  Integration → PodConnect Control** → enter your Spotify Client ID/Secret and sign in.
- **Rooms + voice:** see [`docs/AREAS-AND-ASSIST.md`](docs/AREAS-AND-ASSIST.md).

**Multi-room** (add-on 0.9.0): several HomePods, each its own speaker, added live from the panel
("Add speaker → pick HomePod"); the manager forks & supervises each room's engine. _New — validate
on hardware before relying on it._

**Account-agnostic voice control** (`podconnect_speakers` companion integration): each physical
speaker as a real `media_player` (+ Release button), so "stop the kitchen" works by voice
regardless of whose Spotify is playing. (Install notes in `CHANGELOG.md`.)

## Roadmap (planned — not yet built)

- **Multi-account** (whole family): one Control config entry per person.
- **Instant push state** for HomePods (live go-librespot events instead of ~10s polling).
- Polish: multi-room on-device hardening; a dedicated repo so the companion integration installs via
  HACS; picker visual refresh.

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
