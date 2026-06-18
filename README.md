# PodConnect

Turn each Apple **HomePod** into its own **Spotify Connect speaker** — packaged as a
**Home Assistant add-on** for the HA Green.

Open the PodConnect panel in Home Assistant, click **"Add speaker"**, name it, pick a
HomePod from a dropdown of devices found on your network, and it shows up in the Spotify
app. Start music from the **Spotify app or Home Assistant Assist**, with **volume synced**
across Spotify, Home Assistant, and the HomePod.

```
Spotify app / HA Assist ─► go-librespot (Connect identity) ─► pipe ─► OwnTone ─► AirPlay 2 ─► HomePod
                                          (one pair per HomePod, managed by PodConnect)
```

## Status

Early development. Validating the architecture with a single-room test slice before
building the add-on and its management UI.

## Getting started

- **Test it on your HA Green:** [`podconnect/DOCS.md`](podconnect/DOCS.md) — install the
  single-room test add-on (one HomePod as a Spotify Connect speaker) and try it in your house.
- **Full design:** [`docs/PLAN.md`](docs/PLAN.md) — architecture, stack, build phases.
- **Docker-only harness (any Linux box):** [`dev/README.md`](dev/README.md) — the same pipeline
  via docker-compose, for iterating outside Home Assistant.

## Repository layout

```
podconnect/        HA add-on: single-room test slice (go-librespot + OwnTone, auto-selects your HomePod)
dev/               docker-compose version of the same pipeline (Linux box)
docs/PLAN.md       full architecture & roadmap
repository.yaml    lets you add this repo as a Home Assistant add-on repository
```

## Built on

[go-librespot](https://github.com/devgianlu/go-librespot) (Spotify Connect) ·
[OwnTone](https://github.com/owntone/owntone-server) (AirPlay 2) ·
[Home Assistant add-ons](https://developers.home-assistant.io/docs/add-ons)
