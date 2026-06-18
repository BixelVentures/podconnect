# PodConnect

Turn an Apple **HomePod** into a **Spotify Connect speaker**, and control your Spotify from
**Home Assistant** — using your own Spotify developer app. PodConnect is **two cooperating
halves**, both installed from this one repo.

```
Spotify app / Home Assistant ─► go-librespot (Spotify Connect) ─► pipe ─► OwnTone ─► AirPlay 2 ─► HomePod
```

## What works today

**PodConnect Speakers** — the Home Assistant **add-on** (`podconnect/`, v0.1.4)
- Turns a HomePod into a **Spotify Connect speaker**: `go-librespot` (Connect receiver) → a
  named pipe → `OwnTone` (AirPlay 2 sender) → HomePod.
- **Single room:** you set one HomePod's name; it auto-selects that speaker.
- Installed via the HA **Add-on Store** (prebuilt image, aarch64 + amd64 — no on-device build).

**PodConnect Control** — the Home Assistant **integration** (`custom_components/podconnect/`, v0.1.2)
- Sign in to **Spotify** with your **own developer app** (via HA Application Credentials) — no
  SpotifyPlus, no built-in Spotify integration.
- Creates a `media_player` **for each of your Spotify Connect devices** (the HomePod speaker,
  plus any other Connect device — a MacBook, a car, a phone).
- Controls: **play / pause / next / previous / seek / volume / now-playing**, plus
  **"Connect to a device"** (a real Spotify *Transfer Playback* handoff). Voice via HA Assist.
- State via the Spotify Web API, polled ~10s. Installed via **HACS** (custom repository).

## Install

- **Speakers (add-on):** see [`podconnect/DOCS.md`](podconnect/DOCS.md).
- **Control (integration):** add this repo as a **HACS custom repository** (type: *Integration*)
  → install **PodConnect Control** → restart HA → **Settings → Devices & Services → Add
  Integration → PodConnect Control** → enter your Spotify Client ID/Secret and sign in.

## Roadmap (planned — not yet built)

- **Multi-room:** several HomePods, each its own speaker, with an **"Add speaker → pick HomePod"** UI.
- **Instant push state** for HomePods (live updates from the speaker instead of ~10s polling).
- **Full HomePod volume sync** (the HomePod's real AirPlay level follows Spotify).
- **Browse & search** Spotify content in HA; **multi-account** (whole family).

See [`docs/PLAN.md`](docs/PLAN.md) (roadmap) and [`docs/control-plan.md`](docs/control-plan.md)
(integration spec + current status).

## Repository layout

```
podconnect/                   add-on "PodConnect Speakers" (go-librespot + OwnTone)
custom_components/podconnect/  integration "PodConnect Control" (Spotify OAuth + Web API + entities)
hacs.json                     makes this repo a HACS-installable integration
repository.yaml               makes this repo a Home Assistant add-on repository
docs/                         PLAN.md (roadmap) · control-plan.md (control spec) · releasing.md (versioning)
.github/workflows/            CI that builds & publishes the add-on image to GHCR
```

## Versioning & updates

Two halves, **independent versions, one repo** — you just click **Update** when HACS (Control)
or the Add-on Store (Speakers) shows one. Details in [`docs/releasing.md`](docs/releasing.md).

## Built on

[go-librespot](https://github.com/devgianlu/go-librespot) (Spotify Connect) ·
[OwnTone](https://github.com/owntone/owntone-server) (AirPlay 2) ·
[Home Assistant](https://www.home-assistant.io/)
