# PodConnect — build-ready plan (engine waves)

> **Status (2026-06-20): the wave plan is essentially COMPLETE.** Wave 1 (companion + snappy skips),
> Wave 2 (multi-room), and **Wave 3 (push-state)** all shipped. The companion integration (WS-A) was
> later **retired** — the local-speaker entity it provided was folded into Control, then reverted, so
> account-agnostic stop/release lives only in the add-on panel. This doc is kept as history /
> rationale; the live remaining work is in [`TODO.md`](TODO.md) (on-device validation, buffer-flush).

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

### WS-A — `podconnect_speakers` companion integration (account-agnostic voice) — ⚠️ RETIRED
> **Outcome:** shipped as 0.1.0, then **retired.** Decision: never show two media_players per HomePod.
> The local-speaker entity was folded into Control (0.7.0) and then **reverted (0.7.1)**. Account-
> agnostic Stop/Release lives **only in the add-on panel** (+ Siri). Kept below for history.

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

> **Wave 1 ✅ DONE** — WS-D1 (start_buffer_ms=500) + WS-D2 (`/api/play`, `/api/volume`, `volume` in
> state) shipped as Speakers add-on **0.8.0**. WS-A (`podconnect_speakers` 0.1.0) also shipped but was
> **later retired** — its account-agnostic media_player was folded into Control then reverted (0.7.1);
> Stop/Release now lives only in the add-on panel + Siri.
>
> **Wave 2 ✅ DONE (code) — WS-C multi-room shipped as Speakers add-on 0.9.0.** Manager forks/
> supervises children; s6 audio services removed; rooms.json + migration (r0 legacy back-compat);
> `/api/rooms` + `/api/discover` + Add-speaker panel. (On-device validation still pending.) Polished
> further in 0.10–0.11 (self-healing naming, per-room settings, never-loud).
>
> **Wave 3 ✅ DONE — WS-B push-state shipped as Speakers add-on 0.12.0.** The bridge consumes
> go-librespot's `/events` websocket (stdlib RFC 6455 client) instead of per-200 ms `/status` polling,
> with a `/status` seed on connect, a poll **fallback** on any ws error, and a 3 s re-seed. It also
> lays the **track-change signal** (`metadata.uri`) that the still-deferred buffer-flush (WS-D1b)
> will consume. On-device: confirm the real ws connect + payload shapes on the VM.

```
Wave 1 ✅ done
├─ WS-A  podconnect_speakers companion media_player   (later retired — see status note above)
└─ WS-D1 owntone.conf start_buffer_ms=500             (config render only)

Wave 2 ✅ done  (the manager core)
└─ WS-C  multi-room refactor (rooms.go/supervisor.go/render.go/select.go + s6 prune + panel)
          … folds WS-D1 into render.go, adds WS-D2 endpoints in the new HTTP section

Wave 3 ✅ done  (on top of the WS-C structure)
└─ WS-B  push-state events.go + bridge consumes events
          (WS-D1b track-change→flush still Green-deferred — see GREEN-TESTING.md §D)
```

**Why these waves:** WS-A and WS-D1 touch disjoint files from everything else → safe to run together
immediately. WS-B and WS-C both rewrite the bridge in `manager/main.go`, so they must **not** run in
parallel — land the WS-C refactor first, then WS-B on the new structure. WS-D2's tiny endpoints ride
with WS-C. If you'd rather ship value fast: **Wave 1 alone** delivers voice stop/release (WS-A) +
snappy skips (WS-D1) without touching the multi-room refactor at all.

**Worktrees:** give Agent 3 (WS-C) its own git worktree (large refactor); Agents 1/2 can share since
their paths are disjoint.

## Release lines (as shipped)
- WS-A → `podconnect_speakers` 0.1.0 — **later retired** (folded into Control then reverted in 0.7.1).
- WS-D1 (+D2) → **Speakers add-on 0.8.0** (snappy skips + volume/resume API).
- WS-C → **Speakers add-on 0.9.0** (multi-room); polished in **0.10–0.11** (naming, per-room
  settings, never-loud).
- WS-B → **Speakers add-on 0.12.0** (push-state).
- Update `CHANGELOG.md`, `README.md`, `docs/TODO.md`, `docs/control-plan.md` per release
  (see memory: keep-docs-in-sync-on-release).

## Out of scope (by request)
- **Multi-account** (one Control entry per family member) — deferred.
