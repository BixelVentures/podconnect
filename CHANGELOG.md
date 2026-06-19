# Changelog

All notable changes to PodConnect. Two components version independently:
**Speakers** (HA add-on, `podconnect/config.yaml`) and **Control** (HACS integration,
`custom_components/podconnect/manifest.json`).

Format loosely follows [Keep a Changelog](https://keepachangelog.com/).

---

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
