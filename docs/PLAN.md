# PodConnect — Plan

## Overview

PodConnect is a **Home Assistant add-on** for the **HA Green** (HA OS, ARM64) that turns
each Apple HomePod into its **own independent Spotify Connect speaker**.

The experience: open the PodConnect panel in Home Assistant, click **"Add speaker"**, name
it, **pick a HomePod from a dropdown of devices found on your network**, and it appears in
the Spotify app as a Connect device. Playback can start **from the Spotify app or from HA
Assist** (voice/automation), and **volume stays in sync** across the Spotify app, Home
Assistant, and the HomePod.

> **Status (2026-06): this document is the target roadmap.** Built today: the single-HomePod
> add-on (**PodConnect Speakers** 0.1.4) and the control integration (**PodConnect Control**
> 0.1.2 — Spotify control of all your Connect devices, polling-based). The multi-room manager,
> the "Add speaker" Ingress UI, push state, and HomePod volume sync described below are **not
> yet built**. See `docs/control-plan.md` for the integration's current status.

## Architecture

```
                 ┌──────────────────── PodConnect add-on (one container, host network) ────────────────────┐
                 │                                                                                           │
  HA sidebar ◄───┤  Ingress Web UI ── "Add speaker" · HomePod dropdown (mDNS) · per-room status & volume     │
                 │        │                                                                                  │
  HA Assist ────►│   Manager service (Go)                                                                   │
  (rest_command) │     • mDNS-browses _airplay._tcp → live list of HomePods                                 │
                 │     • CRUD speakers; persists config + per-room Spotify credentials on /data             │
                 │     • spawns/supervises one (go-librespot + OwnTone) pair per speaker; allocates ports   │
                 │     • volume relay: each go-librespot /events  →  that room's OwnTone /api/player/volume  │
                 │        │                                                                                  │
                 │   ┌────┴── speaker "Kitchen" ───────────────────────────────────────────────────────┐   │
   Spotify app ──┼──►│  go-librespot (Connect "PodConnect Kitchen")                                      │   │
   (Connect)     │   │        └─ PCM pipe (s16le/44100/stereo) ─► OwnTone (pipe input)                   │   │
                 │   │                                              └─ AirPlay 2 ─► Kitchen HomePod       │   │
                 │   └──────────────────────────────────────────────────────────────────────────────────┘   │
                 │   (repeat per speaker: Bedroom, Living Room, …)                                          │
                 └───────────────────────────────────────────────────────────────────────────────────────┘
```

One OwnTone instance plays one queue to one set of synchronized speakers, so independent
simultaneous rooms use **one (go-librespot + OwnTone) pair per HomePod**, each with unique
ports, database, and mDNS name.

## Tech stack

| Layer | Choice | Docs |
|---|---|---|
| Spotify Connect receiver (per room) | **go-librespot** (Go, no JVM) | [repo](https://github.com/devgianlu/go-librespot) · [API.md](https://github.com/devgianlu/go-librespot/blob/master/API.md) |
| AirPlay 2 sender (per room) | **OwnTone** (internal AirPlay-2 timing) | [docs](https://owntone.github.io/owntone-server/) · [JSON API](https://owntone.github.io/owntone-server/json-api/) · [multi-instance](https://owntone.github.io/owntone-server/advanced/multiple-instances/) |
| **Manager + Ingress UI** (the product) | **custom, Go single binary** | [HA add-ons](https://developers.home-assistant.io/docs/add-ons) · [Ingress](https://developers.home-assistant.io/docs/add-ons/presentation/#ingress) |
| Container supervision | s6-overlay (supervises manager + avahi/dbus) | [s6-overlay](https://github.com/just-containers/s6-overlay) |
| Discovery | avahi + dbus in-container, host networking | — |
| Device | HA Green — RK3566 quad A55, 4 GB, aarch64 | [HA Green](https://www.home-assistant.io/green/) |

## The product: manager + Ingress UI

The audio pipeline is off-the-shelf; the **manager** is what we build:

1. **Discover HomePods** — browse mDNS `_airplay._tcp` → live list for the dropdown.
2. **CRUD speakers** — add (name + HomePod), rename, remove; persisted to `/data`.
3. **Lifecycle** — on add, render that room's go-librespot + OwnTone configs with unique
   ports/paths/names, create the FIFO, and spawn the pair live (no full add-on restart).
4. **Volume relay** — subscribe each go-librespot `/events`; on a `volume` event, `PUT` that
   room's OwnTone master volume.
5. **Status + control** — per-room paired/playing/volume; a small HTTP surface for HA.
6. **Persistence** — per-room go-librespot credentials + OwnTone database on `/data`, so
   identities stay paired and the chosen HomePod persists across restarts.

A custom UI is required because add-on option forms are static — a dropdown can only hold
hardcoded values, never network-discovered devices. **Ingress** lets the add-on serve its
own web UI with the live HomePod dropdown and add/remove flow.

## Volume sync (bidirectional — go-librespot is the source of truth)

- go-librespot runs with `external_volume: true` so it does **not** scale the PCM; OwnTone /
  AirPlay applies the actual level (no double-attenuation). go-librespot tracks and reports
  the volume number only.
- **Spotify app → everywhere:** app volume change → go-librespot `/events` `volume` event →
  manager `PUT /api/player/volume` on that room's OwnTone → HomePod follows.
- **HA Assist → everywhere:** HA sets volume via go-librespot `POST /player/set-volume` (the
  source of truth), which reports back to the Spotify app (Connect protocol) and fires the
  same event → OwnTone. HA never sets OwnTone volume directly.
- **HA reflects state** by reading go-librespot `/events`/status.

## Dual-sided start (both reach the same per-room go-librespot)

- **Spotify app:** pick the room in the Connect picker → go-librespot → pipe → OwnTone → HomePod.
- **HA Assist / automation:** `POST /player/load` with `uri=spotify:…&play=true` (plus
  `/player/pause|resume|next|prev|seek`, `/player/current`, `/player/set-volume`), wrapped as
  HA `rest_command`s or behind the manager facade.

## Repository layout

```
README.md
docs/PLAN.md                         # this document
repository.yaml                      # lets you add this repo as an HA add-on repository
.github/workflows/publish.yaml       # CI: build & publish the add-on image to GHCR
podconnect/                          # the HA add-on (single-room test slice today; manager/UI later)
  config.yaml                        # manifest: image, host_network, options, watchdog
  build.yaml                         # base image per arch (Debian)
  Dockerfile                         # builds OwnTone (from source) + go-librespot into one image
  rootfs/etc/s6-overlay/             # s6 services: dbus, avahi, go-librespot, owntone, select-homepod
  DOCS.md
  (later) manager/                   # Go: mDNS discovery, speaker CRUD, lifecycle, volume relay, Ingress UI
```

## Build phases

1. **Vertical slice (current — `podconnect/` add-on):** one go-librespot + one OwnTone,
   HomePod auto-selected by name; prove Spotify app → HomePod audio + persistence on the Green.
2. **Manager core:** speaker CRUD, dynamic spawn/supervise, per-room port allocation,
   credential/db persistence, volume relay for N rooms.
3. **Ingress UI:** mDNS HomePod discovery → dropdown; add/rename/remove; per-room status & volume.
4. **HA integration:** `rest_command`s / facade for Assist play + volume; target a room by name.
5. **Package as add-on:** `config.yaml` + Dockerfile + s6; multi-arch (aarch64 first); publish
   as an installable HA add-on repository.

## Verification (on HA Green or a Linux host on the HomePods' network)

1. Add a speaker via the UI; the HomePod dropdown is populated from mDNS.
2. The Connect device appears in the Spotify app within seconds; play → audio on the HomePod.
3. Volume from the Spotify app and from HA Assist both move the HomePod and stay in sync.
4. HA Assist can start playback to a room with the app closed.
5. A second room plays different music simultaneously (independent instances).
6. Restart the add-on → speakers still paired, HomePods still bound.
7. Killing a daemon → manager respawns it; killing the manager → s6 restarts it.

## Risks / open questions

- **Capacity:** ~4–6 rooms estimated on HA Green; measure per-room memory + AirPlay CPU on RK3566.
- **Volume in `external_volume` mode:** confirm `volume` events still fire; finalize the
  0–65536 ↔ 0–100 mapping.
- **Dynamic instances:** the manager (not s6) supervises per-room daemons for add/remove without
  restart — needs robust spawn/kill/respawn + port reclamation.
- **mDNS scale:** N go-librespot + N OwnTone advertising on host network — enforce unique names.
- **Ingress + host_network:** confirm the exact `config.yaml` shape against current add-on docs.
- **HomePod AirPlay quirks** on some firmware — keep `airplay2_disable` / IPv6 escape hatches.
- **go-librespot REST verbs:** pin exact paths from the OpenAPI spec (`/api-spec.yml`) when wiring.
