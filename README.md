# PodConnect

Turn each Apple **HomePod** into its own **Spotify Connect speaker** — packaged as a
**Home Assistant add-on** for the HA Green.

The full product: open the PodConnect panel in Home Assistant, click **"Add speaker"**, pick
a HomePod from a dropdown of devices found on your network, and it shows up in the Spotify
app. Start music from the **Spotify app or Home Assistant Assist**, with **volume synced**
across Spotify, Home Assistant, and the HomePod.

```
Spotify app / HA Assist ─► go-librespot (Connect identity) ─► pipe ─► OwnTone ─► AirPlay 2 ─► HomePod
                                          (one pair per HomePod, managed by PodConnect)
```

## Status

Early development. A single-room **test slice** is working (one HomePod as a Spotify Connect
speaker); the multi-room manager + "Add speaker" UI come next.

## Getting started

- **Install & test on your HA Green:** [`podconnect/DOCS.md`](podconnect/DOCS.md) — add the
  repository, install the add-on, and try it in your house.
- **Full design:** [`docs/PLAN.md`](docs/PLAN.md) — architecture, stack, build phases.

## Repository layout

```
podconnect/                 HA add-on (go-librespot + OwnTone in one image; the Connect speaker)
custom_components/podconnect HA integration (Spotify OAuth + Web API control + media_player entities)
hacs.json                   makes this repo a HACS-installable integration
docs/PLAN.md · docs/control-plan.md   architecture & roadmap
repository.yaml             lets you add this repo as a Home Assistant add-on repository
.github/workflows/          CI that builds & publishes the add-on image to GHCR
```

The **add-on** makes each HomePod a Spotify Connect speaker; the **integration** gives Home
Assistant full Spotify control (play/pause/skip/volume + voice) of those speakers via your own
Spotify developer app. See [`docs/control-plan.md`](docs/control-plan.md).

## Built on

[go-librespot](https://github.com/devgianlu/go-librespot) (Spotify Connect) ·
[OwnTone](https://github.com/owntone/owntone-server) (AirPlay 2) ·
[Home Assistant add-ons](https://developers.home-assistant.io/docs/add-ons)
