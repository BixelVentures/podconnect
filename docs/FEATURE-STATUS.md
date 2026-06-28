# PodConnect — Feature Status (canonical)

Single source of truth for **what each feature does** and **whether it works**. Keep in sync with
[`CHANGELOG.md`](../CHANGELOG.md).

_Last updated: 2026-06-28 — Speakers add-on **0.24.11**, Control integration **0.10.0**._

**Legend:** ✅ working (verified on-device) · ⚪ should work per code, not re-tested this round ·
🧪 experiment (works, behind a flag) · ⏳ planned.

---

## Headline (June 2026): multi-room on ONE account works

Device-aliases mode (`experiment_aliases: true`) makes a **single** go-librespot engine advertise all
your rooms as **separate selectable devices in the Spotify Connect menu, on one account**. Pick a room
in the Spotify app → the audio routes to that HomePod (~1–2 s, AirPlay's switch). Proven on-device —
the long-sought "several rooms, one account, clean audio, switch from the Spotify app." See
[`ALIASES-PROBE.md`](ALIASES-PROBE.md).

---

## A. Audio bridge — volume
| Feature | Status |
|---|---|
| Bidirectional sync — Spotify/HA slider ↔ HomePod hardware buttons, one canonical value (±2% tolerance) | ✅ |
| Sane start — device advertises `initial_volume` (~35%) so the Spotify slider never shows 100% on a fresh claim | ✅ (0.24.4) |
| Never-loud cap — fresh / transferred / reclaimed sessions can't blast; re-arms across transfer + reclaim drift | ✅ (0.22.4–0.22.5) |
| `external_volume:true` — loudness lives in OwnTone's AirPlay output (no double-attenuation) | ✅ |

## B. Audio bridge — transport, grace, duck
| Feature | Status |
|---|---|
| Transport sync (play/pause), startup-aware, rapid-tap-safe | ✅ |
| Grace-release + reclaim on resume (restores your level; in alias mode re-routes to the selected room) | ✅ |
| Attention/duck API (`/api/attention`, heartbeat + auto-release) | ⚪ (unchanged since 0.14.0) |
| Next-track speed — floor is AirPlay's ~2 s; `buffer_ms` tunes the OwnTone re-buffer | ✅ (tunable) |

## C. Spotify Connect engine (go-librespot — our fork)
| Feature | Status |
|---|---|
| Per-room engine (default): one go-librespot+OwnTone per HomePod, stable device_id | ✅ |
| **Device-aliases** — one engine advertises N rooms as Connect-menu aliases; selection (`target_alias_id` at payload top level) routes output to that room; pushed instantly over `/events` | 🧪✅ (0.24.6–0.24.11) |
| Fork built from source (multi-stage slim image); patch `podconnect/patches/aliases-v0.7.3.patch`; CI compile-guard | ✅ |
| Graceful restart (SIGTERM → withdraws zeroconf cleanly → no duplicate Connect entries) | ✅ (0.24.9) |
| avahi host-name pinned + `objects-per-client-max` raised (no rename churn / dbus flood) | ✅ (0.22.2 / 0.24.7) |
| Health-restart watchdog; `/events` push-state + `/status` poll fallback (1 s reseed, also carries alias) | ✅ |

## D. Room lifecycle & panel
| Feature | Status |
|---|---|
| Add / Remove / Rename / per-room ⚙ Settings (grace + bitrate), live, no restart | ✅ |
| Add-speaker in alias mode re-advertises on the primary (no rogue 2nd engine) | ✅ (0.24.8) |
| Panel shows alias rooms as `alias` (not a dead "starting…") | ✅ (0.24.2) |
| Self-healing naming (stable id; Apple-Home rename syncs) · Test tone · Stop / Release | ✅ |

## E. Control integration (HACS, Spotify Web API)
| Feature | Status |
|---|---|
| One `media_player` per Web-API Connect device; ~10 s poll; transport/volume/shuffle/repeat/transfer; optimistic UI | ⚪ |
| Search + Browse (Playlists/Top/Recent/Liked), popularity-ranked, spoken content | ⚪ |
| `media_player.play_media` accepts a free-text name → search + play top result (0.8.0) | ⚪ |
| `podconnect.play_from_library` (liked/top/recent, action) (0.9.0) | ⚪ |
| `podconnect.top_tracks` / `recently_played` / `liked` — response-returning data services for an AI assist (0.10.0) | ⚪ |

## Known noise / follow-ups
- Recurring `/events` ws reconnect log lines (`StatusNoStatusRcvd`) — pre-existing; poll fallback keeps state fresh.
- Promote device-aliases out of the `experiment_` flag after more mileage; surface alias rooms better in the panel.
- Synchronized same-music groups across rooms — not built (OwnTone multi-output is the likely path).
