# PodConnect — Feature Status (canonical)

Single source of truth for **what each feature does** and **whether it works**. Keep in sync with
[`CHANGELOG.md`](../CHANGELOG.md).

_Last updated: 2026-06-28 — Speakers add-on **0.24.11**, Control integration **0.10.0**._

**Legend:** ✅ working (verified on-device) · ⚪ should work per code, not re-tested this round · ⏳ planned.

---

## Headline (June 2026): multi-room on ONE account works — and it's the default

A **single** go-librespot engine advertises all your rooms as **separate selectable devices in the
Spotify Connect menu, on one account**. Pick a room in the Spotify app → the audio routes to that
HomePod (~1–2 s, AirPlay's switch). Proven on-device — the long-sought "several rooms, one account,
clean audio, switch from the Spotify app." This is now the **only** mode: the per-room multi-engine
model and the `persistent_connect` / `experiment_aliases` experiments were removed. See
[`ALIASES-PROBE.md`](ALIASES-PROBE.md).

**Limits (by design, single-engine):** it's **one stream — one room at a time** (picking a room *moves*
the music). PodConnect does **not** play two rooms at once, and a second person **cannot** play a
different room simultaneously via PodConnect (one engine = one account at a time; picking takes over).
Workaround for simultaneous: a second person AirPlays from their iPhone directly to a free HomePod
(native iOS, outside PodConnect). Different-music-per-room and synced same-music groups are not features;
"multi-account via voice" was never built. See [`MULTI-ACCOUNT.md`](MULTI-ACCOUNT.md).

It is **reachable** (not blocked) via a documented **hybrid** (alias for the primary account +
on-demand guest engine per 2nd account), gated on one bench test — does AirPlay-2 PTP sync hold across
two simultaneous OwnTone senders (needs a shared `airptpd`, not yet in the image). Full research + the
one decisive experiment: [`MULTI-ACCOUNT.md`](MULTI-ACCOUNT.md) § "THE one viable path".

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
| **Device-aliases (the only mode)** — one engine advertises N rooms as Connect-menu aliases; selection (`target_alias_id` at payload top level) routes output to that room; pushed instantly over `/events`. Single-engine; stable device_id | ✅ (0.24.6–0.25.0) |
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
- ✅ `/events` ws reconnect log churn (`StatusNoStatusRcvd` ~every 31 s) — **fixed (0.25.2):** the ws
  reader now extends its read deadline on keepalive PONG/PING, so an idle stream no longer times out.
- ✅ Self-healing-on-rename dead code (`selectHomePod`/`healBinding`) — **removed (0.25.2).** `matchOutput`'s
  id-match survives most Apple-Home renames; if a rename ever breaks routing, re-add a heal-only path
  (must heal against the PRIMARY OwnTone for all rooms, since only one engine runs).
- Surface the alias rooms more clearly in the panel.
- Synchronized same-music groups across rooms — not built (OwnTone multi-output is the likely path).


## Active Control 0.10.2 decision — 2026-09-27

Lead approved bounded optimistic-state correction. Observed source defect: successful
Spotify API acceptance can be followed by dealer rejection or a contradictory poll,
but optimistic play/shuffle/repeat previously persisted until values matched. This
candidate must replace intent with a successful poll begun after command completion.
An already-running poll is not post-command evidence; a failed poll is not confirmation.
Use monotonic poll-start sequence and command generation, with no timeout tuning.
Non-goals: Spotify engine/discovery changes, transport replay, and physical playback proof.
Regression plan: contrary post-command truth, pre-command in-flight response, failed
poll, device transfer, overlapping commands and rejected commands. Roll back Control
only if HA integration validation fails. Not installed/released/test-ready yet.

Implemented candidate: poll-start sequence is stamped before network reads; a completed
command records the latest started poll. A successful later sequence replaces all
optimistic fields even when Spotify contradicts the command or changes active device.
Failed/pre-command polls do not confirm anything. Command generation prevents an older
completion from clearing a newer in-flight intent; network/API errors clear unconfirmed
intent. Thirteen Python 3.12 unit regressions passed on 2026-09-27, executing the shipped
methods with mocks (including the actual coordinator fetch method). No running-HA or
physical playback/discovery proof; independent review and integration checks pending.

Lead additionally approved source-order search repair and truthful library/search
read errors. Synthetic reproduction: a source-first requested artist's Halo (60)
was demoted below a different artist's Halo (99) by title-only/popularity reranking.
Preserve Spotify order within each result type; do not invent new relevance policy.
Failed library reads must raise service errors, not masquerade as empty tracks.
No prompt/tool-surface changes; regressions execute the actual service/search methods.

Final local evidence: 17 Python 3.12 tests passed after search/read additions and
cancelled-command cleanup. Search regressions preserve provider-first requested artist
against a more popular cover in both direct and Assist paths. Library tests distinguish
legitimate empty results from provider failure and missing account. These are shipped
method/service-registration tests with mocked boundaries, not running-HA integration
or physical audio/discovery evidence. Candidate remains uninstalled; independent review
pending. Speakers code/version is unchanged in this Control-only candidate.

Independent final review27/9: separate reviewer GO, no P0/P1. Reviewer independently
ran all17 Python tests and Node Stop-feedback regression successfully. Control0.10.2
manifest/changelog included in review. These are shipped-method tests with mocked
HA/provider boundaries, not physical discovery or room-behaviour proof.
