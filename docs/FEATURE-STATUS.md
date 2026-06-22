# PodConnect — Feature Status (canonical)

Single source of truth for **what each feature is supposed to do** (per the code) and **whether it
currently works**. Update the Status column as we verify/fix on real hardware. Keep this in sync with
[`CHANGELOG.md`](../CHANGELOG.md).

**Status legend**
- ✅ **Working** — verified behaving as expected (note where/when).
- 🔴 **Regressed** — user-confirmed broken right now.
- 🟡 **Suspect** — likely affected by recent changes; needs on-device check.
- ⚪ **Unverified** — should work per code, not tested on-device this round (no local Go build here).

> ⚠️ Verification limit: this environment has **no Go toolchain and no access to the live HA/HomePods**.
> CI only confirms the image *compiles*. Every "Working" claim for runtime behavior must be checked on
> the device. Where I can't verify, it's marked ⚪ — not ✅.

---

## 🔥 Known regressions — 2026-06-22 (after 0.15.0 → 0.17.0)

User report: *"Højt, skift af lydstyrke, miste forbindelse til connect-højtaleren i app … alt det der virkede er nu weird."* (Loud; volume changes by itself; loses the Connect speaker in the Spotify app.)

| # | Symptom | Most likely cause (suspect) | Where |
|---|---------|------------------------------|-------|
| R1 | Volume still goes **loud** on a fresh claim | The held-window cap (0.17.0) fights Spotify re-asserting its level; `lastGoodVol` may have latched a high value during steady playback, so the "cap" target is high | `main.go:680-710` |
| R2 | Volume **jumps/oscillates** by itself (e.g. 90→25→12→17) | Cap block sets `volCanon=-1` every tick during the 8 s window → reconcile re-seeds each tick while the cap re-applies → ping-pong with `decideVolume` | `main.go:680-710`, `decideVolume` `main.go:439-462` |
| R3 | **Loses the Connect speaker** in the Spotify app | go-librespot is **restarted** on name-forward, which now fires from the panel's auto-apply-on-pick (0.15.0) and from any HomePod re-bind; each restart drops the Connect session | `/api/select` `main.go:996-1051`, `select.go:82-90` |
| R4 | General "weird" instability | Recurring `/events` websocket errors (`StatusNoStatusRcvd`) every ~30 s → manager keeps reconnecting to go-librespot; combined with restarts above | `events.go:139-256` |

**Recommended next step (decision needed):** the volume + connection core was stable at **0.14.0**.
The cleanest path is to **revert the manager volume/never-loud + auto-apply changes to the 0.14.0
baseline** (ship as 0.18.0), confirm stability on-device, then re-introduce never-loud *carefully with
on-device testing before shipping*. Keep the panel cleanup (0.15.0 UI) and in-panel changelog, which
are unrelated to the audio path. See "Revert plan" at the bottom.

---

## A. Audio bridge — volume

| Feature | Expected behavior | Where | Status |
|---|---|---|---|
| Bidirectional volume sync | One canonical %; side drifting >2% becomes canon (Spotify wins ties); pushed to the other side; tolerance prevents echo | `main.go:439-462` (decideVolume), loop `main.go:700-719` | 🟡 (core logic sound; destabilized by cap interaction) |
| `external_volume:true` model | go-librespot reports volume only; **OwnTone applies actual loudness** at the AirPlay output | `render.go:52` | ⚪ |
| Never-loud — idle prearm | While no session active, hold the HomePod output ≤35% (every 2 s) so the first audio can't blast | `main.go:671-677` | 🟡 |
| Never-loud — fresh-session window | On claim/reclaim, clamp both sides ≤`lastGoodVol` for 8 s, re-applied each tick; hard-clamp the pushed value | `main.go:680-710` | 🔴 (R1, R2) |
| Never-loud — `lastGoodVol` target | Track the room's last steady level so your **own** resume returns to it (not a hard 35%) | `main.go:579,710` | 🔴 (R1 — can latch high) |
| `capFreshClaim` (select-time guard) | Lower-only: cap the just-selected output ≤35% before the first reconcile | `main.go:272-277`; callers `main.go:101,557,1025`, `select.go:101` | 🟡 |
| Manual volume set | `PUT /api/volume {volume,room?}` → go-librespot; bridge mirrors to HomePod | `main.go:1142-1164` | ⚪ |

## B. Audio bridge — transport, grace, duck

| Feature | Expected behavior | Where | Status |
|---|---|---|---|
| Transport sync (play/pause) | Forward (Spotify→HomePod) instant; back (HomePod tap→Spotify) only after OwnTone confirms, to mask startup lag | `main.go:479-511`, loop `main.go:722-734` | ⚪ |
| Grace-release | After `grace_minutes` idle, deselect outputs + write `released` flag (free HomePod for other apps); grace read live every ~10 s | `main.go:534-542,641-664`; `releasedPath` `main.go:528` | ⚪ |
| Reclaim on resume | Playback resumes → clear flag, re-select HomePod, re-arm never-loud | `main.go:546-559,646-654` | 🟡 (re-arm interacts with R1/R2) |
| Attention/duck API | `POST /api/attention` ducks a room to a level with heartbeat + auto-release (≤15 s); duck wins over reconcile; restores pre-duck level on release | `attention.go`, bridge `main.go:617-636`, HTTP `main.go:1184-1268` | ⚪ (unchanged since 0.14.0) |

## C. Spotify Connect device (go-librespot)

| Feature | Expected behavior | Where | Status |
|---|---|---|---|
| One Connect device per room | Each room runs go-librespot named after its HomePod, stable `device_id` (no ghost on rename) | `render.go:32-61`, `rooms.go:429-439` | ⚪ |
| Zeroconf claim model | Device advertises via mDNS; appears in the **Web API / HA** only after the **Control account** claims it (plays to it once) | `render.go:43-48`; see [MULTI-ACCOUNT.md](MULTI-ACCOUNT.md) | ⚪ (by design, not a bug) |
| Name-forward on HomePod pick | Picking a HomePod auto-renames the speaker + **restarts go-librespot** (drops Connect session briefly) | `main.go:1003-1010,1041-1048` | 🔴 (R3 — now auto-fires on picker change) |
| Health restart guard | 3 failed `/status` polls in ~90 s → restart go-librespot | `supervisor.go:123-170` | ⚪ |
| `/events` push-state + fallback | ws `/events` for live state; falls back to `/status` polling on error | `events.go:139-256` | 🟡 (R4 — frequent reconnect errors in logs) |

## D. Room lifecycle & API

| Feature | Expected behavior | Where | Status |
|---|---|---|---|
| Add speaker | Validate uniqueness, allocate ports/paths, render configs, spawn, select | `rooms.go:182-221`, `main.go:1284-1344` | ⚪ |
| Remove speaker (not r0) | Deselect HomePod, kill child group, drop from store | `rooms.go:226-254`, `main.go:1329-1340` | ⚪ |
| Rename speaker | Pin manual name, rewrite device_name, restart go-librespot | `main.go:1346-1384` | ⚪ (restart drops Connect session — by design) |
| Per-room grace/bitrate | grace live (no restart); bitrate re-renders + restarts | `main.go:1386-1453` | ⚪ |
| Stop / Play / Release | Account-agnostic pause / resume / free-HomePod across target rooms | `main.go:1095-1138` | ⚪ |
| Test tone | Soft 330 Hz tone at 13% to prove the OwnTone→AirPlay→HomePod path | `main.go:749-785,1053-1093` | ⚪ |

## E. Speakers panel (web UI)

| Feature | Expected behavior | Where | Status |
|---|---|---|---|
| "Your speakers" cards | Per-room Test/Stop/Rename/Settings/(Remove); 5 s refresh | `main.go:1666-1794` | ✅ (renders; 0.15.0) |
| Primary "main" card | Top card tinted + "main" pill; no separate picker section | `main.go:1673-1684` | ✅ |
| HomePod re-pick in Settings | Radio list inside primary's ⚙ Settings; **auto-applies on pick** → `/api/select` | `main.go:1749-1764,1843-1875` | 🟡 (auto-apply triggers R3 restarts) |
| Release in Settings | "⏏ Release for other apps" → `POST /api/release` | `main.go:1759-1763` | ⚪ |
| Add-speaker flow | Discover free HomePods → add live | `main.go:1797-1839` | ⚪ |
| In-panel "What's new" | Collapsible changelog per release (parity with PodVoice) | `main.go:1636-1657` | ✅ (0.17.0) |

## F. Control integration (HACS, Spotify Web API)

| Feature | Expected behavior | Where | Status |
|---|---|---|---|
| Device → media_player | One entity per Web-API Connect device; **dynamically added** as devices appear | `media_player.py:82-107` | ⚪ |
| Poll loop | `/me/player` + `/me/player/devices` every 10 s | `coordinator.py:30-41`, `const.py:30` | ⚪ |
| Transport/volume/shuffle/repeat | Standard media_player controls with **optimistic UI** | `media_player.py:281-311` | ⚪ |
| Select source (transfer) | Move the session to another Connect device, keep play state | `media_player.py:320-333` | ⚪ |
| Browse | Playlists / Top Artists / Top Tracks / Recently Played / Liked | `media_player.py:335-403` | ⚪ |
| Search | Name-relevance ranked, popularity tie-break; spoken content included | `media_player.py:427-480` | ⚪ |
| OAuth / scopes | App Credentials OAuth; scope mismatch forces re-auth | `__init__.py:38-61`, `const.py:16-26` | ⚪ |
| Per-account visibility | Only devices claimed by **this** account appear (why HomePods may be missing) | n/a (Web API) | ⚪ (by design) |

---

## Revert plan (if approved)

1. `git revert`/restore `podconnect/manager/main.go` + `select.go` volume/never-loud + auto-apply
   blocks to the **0.14.0** baseline (commit `6400315`), **keeping**:
   - the 0.15.0 panel redesign (single primary card),
   - the in-panel changelog (0.17.0),
   - the attention/duck API (0.14.0).
2. Bump `config.yaml` → `0.18.0`, changelog entry "revert volume regressions to the stable baseline".
3. Ship, update add-on, **verify on-device**: claim a fresh speaker (no blast?), adjust volume (no
   oscillation?), confirm the Connect device stays in the app.
4. Only then re-design never-loud — minimal, one guard, tested live before release.

_Last updated: 2026-06-22 (0.17.0 shipped; regressions R1–R4 open)._
