# PodConnect — build-ready plan (next wave)

_Research-validated, decomposed for parallel agents. **Multi-account is deliberately out of scope**
(deferred). Every design decision below is backed by source-cited research (HA core, go-librespot,
OwnTone) — the citations live in the workstream notes._

## Decisions that change the approach (read first)

1. **No MQTT `media_player`.** HA's MQTT integration has **no media_player platform** (confirmed:
   absent from `mqtt/const.py` `SUPPORTED_COMPONENTS`/`ENTITY_PLATFORMS`; MQTT can only create
   button/switch/sensor/number/…). So the account-agnostic speaker is exposed via a **small
   companion custom integration** (`podconnect_speakers`) that wraps the add-on's HTTP API as a real
   `media_player`. Bonus: there is **no `HassMediaStop` intent** — "stop" is an alias of
   `HassMediaPause`, needing only the `PAUSE` feature, so "stop the kitchen" works natively.
2. **Buffer-flush is a config line, not API orchestration.** OwnTone exposes **no pipe-flush
   endpoint**; the 2–4 s lag is the fixed `start_buffer_ms = 2250` output pre-buffer. The
   maintainer documents reducing it to **`start_buffer_ms = 500`** ("perceptually instant"). That is
   the fix; an event-driven `stop`+autostart is only an optional extra.
3. **Push-state via go-librespot `/events`:** detect track change on the **`metadata`** event keyed
   on `data.uri`; volume is **0..100**; **no state replay** on connect (GET `/status` first); **no
   server heartbeat** (client must ping + reconnect); rapid-skip bug #300 → **debounce** any action.
4. **Multi-room = the manager forks/supervises children via `os/exec`**, NOT dynamic s6 services
   (s6-rc compiles at boot; runtime service creation mutates `/etc` and is fragile). OwnTone
   multi-instance is officially supported (per-instance config/db/ports/name).

---

## Workstreams

### WS-A — `podconnect_speakers` companion integration (account-agnostic voice)
**Goal:** "stop / pause the kitchen" (any account) + speaker play/idle/released state + volume, by
voice and on dashboards, in the right Area.

**Why a 2nd integration:** the existing `podconnect` domain is the cloud/per-account Spotify one.
The physical speaker is local + account-agnostic → its own `local_polling` integration backed by the
add-on's `/api/*`. (HA MQTT can't do media_player; see Decision 1.)

**New files** — `custom_components/podconnect_speakers/`:
- `manifest.json` (`domain: podconnect_speakers`, `config_flow: true`, `iot_class: local_polling`,
  `integration_type: device`, `version: 0.1.0`).
- `config_flow.py` — one text step: `base_url` (suggest `http://homeassistant.local:8099`), validate
  by `GET {base_url}/api/state`; unique_id from host. **Host gotcha:** with `host_network: true` the
  add-on has no hassio DNS name — the reliable URL is the **HA host LAN IP:8099**; let the user
  confirm/override it in the flow.
- `coordinator.py` — `DataUpdateCoordinator`, poll `GET {base_url}/api/state` ~5 s (aiohttp).
- `media_player.py` — one entity per room: `supported_features = PAUSE | PLAY` (+ `VOLUME_SET` once
  the manager exposes volume, see WS-D2). `state`: `released`→`IDLE`, `playing`→`PLAYING`, else
  `IDLE`. `async_media_pause`/`async_media_stop` → `POST /api/stop`; `async_media_play` →
  `POST /api/play` (add to manager: go-librespot `resume`); `media_title` from `now_playing`.
  `DeviceInfo(identifiers={(DOMAIN, room_id)}, name=speaker, manufacturer="PodConnect")`.
- `button.py` — "Release HomePod" → `POST /api/release` (release has no media_player verb).
- HACS: this repo already ships `podconnect` via HACS; document adding the 2nd integration (or a
  `hacs.json` note). Expose-to-Assist + Area = user step (already in `AREAS-AND-ASSIST.md`).

**Manager dependency (small, in WS-D2):** add `POST /api/play` (resume) and `GET/PUT` volume so the
media_player can offer play + `VOLUME_SET`. Ship A without volume first if needed.

**Tests:** config_flow happy/err path; coordinator maps state; `media_stop`→stop called. Manual:
expose entity, assign Area, say "stop the kitchen" via Assist (stock + Gemini).

**Isolation:** entirely new directory → **no conflict** with manager work. Best parallel candidate.

---

### WS-B — Push-state from go-librespot `/events` (manager)
**Goal:** replace ~200 ms `/status` polling in the bridge with the event stream; lower latency, less
churn; emit a track-change signal (feeds WS-D2).

**Design (cited):** connect `ws://localhost:<glPort>/events`; **GET `/status` on every (re)connect**
to seed (no replay). Map events: `volume`→`data.value` (0..100); `playing`(check `resume`)/`paused`/
`stopped`/`not_playing`/`active`/`inactive`→transport; **`metadata`→ if `data.uri` changed, fire
onTrackChange**. Client-side ping + reconnect-with-backoff (no server heartbeat; 10 s write timeout).
Rate-limit downstream actions (bug #300 rapid-skip storm). Keep a slow `/status` reconcile as backstop.

**Files:** `manager/events.go` (new: ws client, event structs from the catalog, `onTrackChange`,
`onVolume`, `onTransport` callbacks); `manager/main.go` `roomBridge` consumes pushed state instead of
polling each tick (keep the canonical `decideVolume`/`transState` reconcilers — they stay pure/tested).

**Tests:** unit-test the event JSON → state mapping; keep the existing `decideVolume`/`transState`
tests. Manual: `websocat` against the live add-on to confirm payloads, then verify HA reflects
play/volume faster.

**Conflict:** touches `manager/main.go` heavily (the bridge). **Conflicts with WS-C.** Sequence after
WS-C's refactor (or integrate into it).

---

### WS-C — Multi-room ("Add speaker → pick HomePod") ★ the big one
**Goal:** N HomePods, each its own (go-librespot + OwnTone) pair, added live from the panel, no
add-on restart.

**Decision (cited):** the **manager forks & supervises** each room's two child processes via
`os/exec`; **drop the s6 services** `go-librespot`, `owntone`, `gl-watchdog`, `select-homepod` (fold
their logic into the manager). Keep s6 for `dbus`, `avahi`, `init-podconnect` (global setup only),
`manager`. OwnTone multi-instance is supported via per-instance `-c <conf>` with unique
db/cache/log/ports/`library.name`.

**Port ranges (avoid the naïve +10·idx collision):** go-librespot `3700+idx`, OwnTone API
`3800+idx`, OwnTone ws `3900+idx`, mpd disabled. Room 0 may keep legacy 3678/3689/3688 for back-compat
(HA `watchdog: tcp://[HOST]:3689` keeps watching room 0).

**Persistence:** `/data/rooms.json` (`rooms[]` with `id, idx, name, homepod_name, device_id,
released`; `next_idx` monotonic — never reuse). Per-room tree `/data/rooms/<id>/{go-librespot,
owntone}` + pipes `/srv/media/rooms/<id>/spotify[.metadata]`.

**New files** (`manager/`): `rooms.go` (Room extended: `ID, Idx, Pipe, ConfigDir, Librespot, OwnTone`
URLs; `rooms()` reads rooms.json; port allocator; load/save w/ mutex), `supervisor.go` (`roomManager`,
`roomRuntime{glCmd, otCmd, …}`, `ensureRunning`, `superviseLoop` restart + folded go-librespot hang
watchdog, `removeRoom`, process-group kill via `SysProcAttr`+`Pdeathsig`, reconcile on manager
restart), `render.go` (Go text/template for `config.yml` + `owntone.conf` — move heredocs out of
bash; `mkfifo`/`chown` helpers; **set `start_buffer_ms = 500` here**, ties WS-D1), `select.go` (per-room
HomePod selection ported from the `select-homepod` jq script). **Make per-room** what's global today:
`tonePlaying` (map by id), `playTestTone(pipe)`, `releasedPath(room)`, `glConfigPath(room)`,
`restartLibrespot(room)`.

**HTTP:** `GET /api/discover` (live AirPlay scan minus already-claimed HomePods), `GET /api/rooms`,
`POST /api/rooms {homepod_name,name?}` (validate name uniqueness; allocate; render; spawn; select),
`DELETE /api/rooms/<id>` (deselect; kill PG; stop goroutines; drop from rooms.json). Panel: room list +
"Add speaker" flow.

**s6/rootfs:** `init-podconnect` → global setup only (dirs, avahi interface restriction); **delete**
the 4 service dirs + their `user/contents.d` entries; first-boot migrate legacy `selected_output.json`
+ `speaker_name` → a room-0 entry in rooms.json.

**Enforce:** one HomePod per room (AirPlay = one sender); unique room/device names (Connect + mDNS).

**Resource (RK3566/4 GB):** provision 4–6 rooms fine (~1.2–1.8 GB worst case); ~2–3 *concurrent
streams* comfortable; idle rooms ≈ free. Consider an OwnTone mem cap.

**Tests:** unit-test port allocator, rooms.json round-trip, render output; supervisor restart logic.
Manual: add 2 rooms, both appear in Spotify, independent playback/volume; delete a room cleanly.

**Risks:** mDNS/Connect name collisions (guard at add), double-claimed HomePod (hide claimed in
picker), manager-as-init orphaning (process groups + reconcile), port overlap (use the ranges above),
CPU saturation (expectations), OwnTone version drift (AirPlay 2 in-process is a property of pinned
29.2 — re-verify before any bump).

---

### WS-D — Snappy skips + manager volume/resume endpoints
**D1 — `start_buffer_ms = 500`** in the rendered `owntone.conf` (in `init-podconnect` now, or
`render.go` under WS-C). One line; attacks the real 2250 ms pre-buffer. Test underruns on the wired
Green (floor ~250 ms; 500 ms maintainer-blessed; bump to 700–1000 ms if dropouts). **Optional D1b:**
on a WS-B track-change event, `PUT /api/player/stop` (pipe_autostart re-buffers) for a harder cut —
debounced, only on real skips. Ship D1 first; D1b only if residual lag annoys on the Green.

**D2 — manager endpoints** for WS-A: `POST /api/play` (go-librespot `resume`), `GET/PUT /api/volume`
(reuse `owntoneOutputVolume`/`setLibrespotVolumePct`), and add `volume` to `/api/state`. Small,
isolated additions.

**Conflict:** D1 is in the config render; D2 is small manager handlers. Low-conflict, but D2 touches
`main.go` — coordinate with WS-C (or do D2 inside WS-C's HTTP section).

---

## Parallel execution map

> **Wave 1 ✅ DONE** — WS-A (`podconnect_speakers` 0.1.0) + WS-D1 (start_buffer_ms=500) + WS-D2
> (`/api/play`, `/api/volume`, `volume` in state) shipped as Speakers add-on **0.8.0** + the new
> companion integration. Distribution caveat: HACS = one integration per repo, so
> `podconnect_speakers` installs manually for now (a dedicated repo is the follow-up).
> **Next: Wave 2 (WS-C multi-room).**

```
Wave 1 (fully parallel — disjoint files)   ✅ done
├─ Agent 1 ▶ WS-A  custom_components/podconnect_speakers/**         (new dir, zero manager overlap)
└─ Agent 2 ▶ WS-D1 owntone.conf start_buffer_ms=500                 (config render only)

Wave 2 (the manager core — single owner to avoid main.go conflicts)
└─ Agent 3 ▶ WS-C  multi-room refactor (rooms.go/supervisor.go/render.go/select.go + s6 prune + panel)
              … folds in WS-D1 into render.go, and adds WS-D2 endpoints in the new HTTP section

Wave 3 (on top of the WS-C structure)
└─ Agent 4 ▶ WS-B  push-state events.go + bridge consumes events; wire WS-D1b track-change→flush
```

**Why these waves:** WS-A and WS-D1 touch disjoint files from everything else → safe to run together
immediately. WS-B and WS-C both rewrite the bridge in `manager/main.go`, so they must **not** run in
parallel — land the WS-C refactor first, then WS-B on the new structure. WS-D2's tiny endpoints ride
with WS-C. If you'd rather ship value fast: **Wave 1 alone** delivers voice stop/release (WS-A) +
snappy skips (WS-D1) without touching the multi-room refactor at all.

**Worktrees:** give Agent 3 (WS-C) its own git worktree (large refactor); Agents 1/2 can share since
their paths are disjoint.

## Release lines
- WS-A → **PodConnect Speakers integration `podconnect_speakers` 0.1.0** (new HACS entry).
- WS-D1 (+D2) → **Speakers add-on 0.8.0** (snappy skips + volume/resume API).
- WS-C → **Speakers add-on 0.9.0** (multi-room).
- WS-B → **Speakers add-on 0.10.0** (push-state).
- Update `CHANGELOG.md`, `README.md`, `docs/TODO.md`, `docs/control-plan.md` per release
  (see memory: keep-docs-in-sync-on-release).

## Out of scope (by request)
- **Multi-account** (one Control entry per family member) — deferred.
