# PodConnect

Turn Apple **HomePods** into **Spotify Connect speakers** and control Spotify from
**Home Assistant**, using your own Spotify developer app.

```
Spotify app / Home Assistant ─► go-librespot (Spotify Connect) ─► OwnTone ─► AirPlay 2 ─► HomePod
```

PodConnect has two parts, installed from this one repository:

| Part | What it does | Install |
|---|---|---|
| **Speakers** (add-on, `podconnect/`) | Each HomePod appears as a room in the Spotify Connect menu. Volume and play/pause stay in sync with the HomePod's own buttons. | Home Assistant Add-on Store |
| **Control** (integration, `custom_components/podconnect/`) | A `media_player` per Spotify Connect device, search and browse, and services for voice assistants and dashboards. | HACS |

## Features

- **Multi-room on one Spotify account.** Every HomePod is its own Connect device; pick a room and
  the music moves there in 1–2 seconds. One stream plays at a time.
- **Room manager panel** in the Home Assistant sidebar: add, rename, remove and test speakers
  from a live network scan. Renaming a HomePod in Apple Home follows automatically.
- **Never too loud.** New sessions start at a sane volume, and a per-session cap survives transfers.
- **Instant transport.** Pause on Spotify stops the HomePod at once; a tap on the HomePod
  pauses Spotify.
- **Share the speaker.** *Stop* pauses whoever is playing, any account. *Release* frees the
  HomePod for other AirPlay apps.
- **Voice ducking API** lets a voice assistant lower the music while it talks.
- **Home Assistant control:** play, pause, next, previous, seek, volume, shuffle, repeat,
  transfer playback, search and browse (playlists, top tracks, recently played, liked songs,
  audiobooks and podcasts), plus data services for assistants.

## Requirements

Spotify Premium, Home Assistant (tested on Home Assistant Green), and HomePods on the same network.

## Install

1. **Speakers:** Settings → Add-on Store → ⋮ → Repositories → add
   `https://github.com/BixelVentures/podconnect`, then install **PodConnect Speakers**.
   Open the **PodConnect Speakers** panel and add your HomePods. Details: [podconnect/DOCS.md](podconnect/DOCS.md).
2. **Control:** in HACS add this repository as a custom repository (type *Integration*), install
   **PodConnect Control**, restart, then add the integration and sign in with your Spotify
   Client ID and Secret.
3. Optional: [rooms and voice with Assist](docs/AREAS-AND-ASSIST.md), [getting started](docs/GETTING-STARTED.md).

## Documentation

[Commands and services](docs/COMMANDS.md) · [Observations](docs/OBSERVATIONS.md) ·
[Attention API](docs/ATTENTION-API.md) · [Multiple accounts](docs/MULTI-ACCOUNT.md) ·
[Releasing](docs/releasing.md) · [Changelog](CHANGELOG.md)

## Contributing

See [AGENTS.md](AGENTS.md): describe the user outcome, write a failing test, fix it, keep it green.

## Built on

[go-librespot](https://github.com/devgianlu/go-librespot) ·
[OwnTone](https://github.com/owntone/owntone-server) ·
[Home Assistant](https://www.home-assistant.io/)
