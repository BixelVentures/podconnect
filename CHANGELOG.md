# Changelog

All notable changes to PodConnect. Two components version independently:
**Speakers** (HA add-on, `podconnect/config.yaml`) and **Control** (HACS integration,
`custom_components/podconnect/manifest.json`).

Format loosely follows [Keep a Changelog](https://keepachangelog.com/).

---

## Speakers 0.16.0 — 2026-06-22  (Never-loud: cap the claim moment, not just steady state)
- **A freshly-claimed speaker no longer blasts at 100%.** Selecting a HomePod output sets no volume,
  so the first audio of a claim played at whatever level the HomePod/another account remembered (often
  100%) for the window before the reconcile caught it. With `external_volume:true` the loudness *is*
  the OwnTone AirPlay output level, so that window was audible full blast.
- **Cap now lands at selection time, at the source.** New `capFreshClaim` lowers a just-(re)selected
  output to the never-loud ceiling (35%) **before** audio flows, in every claim path: reclaim from a
  released HomePod, an explicit panel re-pick (`/api/select`), and the heal/initial selection tick.
  Lower-only — a session already under the cap is never raised. The bridge's steady-state cap stays
  as the backstop.

## Speakers 0.15.0 — 2026-06-22  (Panel cleanup — one primary card, no second picker)
- **The top speaker card *is* the primary.** The separate "Primary speaker — pick its HomePod"
  section is gone. The top card now carries a subtle accent tint and a small `main` pill, so the
  primary reads as primary at a glance — no radio list to choose "which one is main".
- **Re-pointing the primary's HomePod moved into its ⚙ Settings drawer.** A "Plays to HomePod"
  picker (the same live OwnTone AirPlay scan) sits alongside grace/bitrate; **picking applies
  immediately** (no separate Save click), and the drawer stays open across the 5 s refresh.
- **⏏ Release for other apps** lives in that same drawer. The old duplicate global **Test / Stop /
  Release** buttons are removed — per-card 🔊 Test / ⏹ Stop already cover it.
- Dropped the redundant `Primary speaker: …` subline, the play-state banner, and now-dead CSS. Pure
  UI/template change — no API, data-model, or behavior change.

## Speakers 0.14.0 — 2026-06-20  (Attention/duck API — Wave 4)
- **New `/api/attention` duck primitive** so an external agent (a voice-assistant gatekeeper) can
  dip a room's music while it talks, then let it back up — without ever fighting the volume relay.
  `POST {room, level, owner?, ttl_ms?}` engages or **heartbeat-extends** a duck; `POST
  /api/attention/release {room}` ends it; `GET` returns per-room state. Absolute, idempotent target
  with an **owner + deadline**.
- **Auto-release watchdog, built into the API.** A duck holds only as long as the agent keeps
  re-POSTing (heartbeat). Stop — crash, Wi-Fi drop, killed session — and it **auto-releases at the
  deadline** (default 2 s), restoring the pre-duck level. The agent never has to clean up; the music
  can't get stuck quiet.
- **The duck wins, locally.** While active, the per-room bridge **holds the HomePod at the target and
  suspends the volume reconcile**, so a HomePod button or the Spotify mirror can't undo it. Transport
  keeps running — the music plays quietly underneath (the "polite waiter"), exactly as the voice
  design intends. On release the pre-duck level is restored and the reconcile re-seeds.
- **Optional `attention_token`** option: when set, `/api/attention` requires an `X-PodConnect-Token`
  header (this is the one endpoint built for *external* control). Empty = open on a trusted LAN.
- **`ttl_ms` clamped to a 15 s ceiling** so a single large-TTL request + crash can't pin the music
  quiet and defeat the auto-release — the model is "hold only as long as you heartbeat".
- **Never-loud survives a duck.** A fresh session that starts *while* a room is ducked (another
  account grabbing the HomePod mid-conversation) is still capped on release — it can't escape to a
  remembered 100%.
- v1 is instant (no fade); `fade_ms` is accepted and reserved. Contract documented in
  [`docs/ATTENTION-API.md`](docs/ATTENTION-API.md) — the only binding between PodConnect and a
  separate voice project.

## Control 0.7.1 — 2026-06-19  (reverts the 0.7.0 local entities)
- **No duplicate entities.** 0.7.0 surfaced a *second* media_player (+ Release button) per HomePod
  alongside the Spotify Connect one — two players per speaker, which is the wrong UX. Reverted: Control
  is **pure Spotify control again, one entity per Connect device**. The local-speaker plumbing
  (options URL, speakers_api/coordinator, button) is removed.
- **Account-agnostic Stop/Release stays where it belongs:** the add-on **panel** buttons (+ Siri).
  And "stop music" / "pause the kitchen" in HA Assist already works on your *own* playback via the
  Spotify entity — no extra entity needed. (Cross-account voice-stop was deprioritized by design.)
- Keeps 0.6.x search (audiobooks + popularity ranking).

## Control 0.7.0 — 2026-06-19  (superseded by 0.7.1 — reverted)
- **Companion folded into Control.** The separate `podconnect_speakers` integration is retired —
  set the **add-on manager URL** in Control's options (Settings → Devices & Services → PodConnect
  Control → Configure; the HA host's LAN IP on `:8099`) and the account-agnostic local controls
  appear: a **local speaker `media_player`** (pause / play / volume) and a **Release HomePod**
  button **per room** (multi-room aware via `/api/rooms`, with a single-room fallback for older
  add-ons). These are distinct devices from the cloud Spotify Connect entities for the same HomePod.
- **Two things to install, not three:** add-on + Control. Control still works **fully without** the
  add-on URL — it stays a pure Spotify controller, no add-on dependency, no errors when the URL is
  unset or unreachable. Adding/clearing the URL reloads the entry to spin local speakers up/down.

## Control 0.6.1 — 2026-06-19
- **Search picks the version people mean.** When several results share the exact title (e.g. the
  many "Den Danske Sommer" songs), ties are now broken by Spotify **popularity** — so the iconic hit
  wins over a niche cover, instead of whichever Spotify returned first. (Gemini just passes the query;
  this is our ranking, not the prompt.)

## Control 0.6.0 — 2026-06-19
- **Audiobooks / podcasts / episodes in search.** Search now includes `audiobook,show,episode`, so
  "play <a children's bedtime story / audiobook / podcast>" resolves to the real audiobook instead of
  a random same-named song. `play_media` routes episodes as track URIs, shows/audiobooks as contexts.
- Pairs with `docs/gemini-system-prompt.md` (Assist confirms *what* it played + says when it can't)
  and the Gemini "Block none" safety fix for the `PROHIBITED_CONTENT` errors.

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

## Speakers 0.13.0 — 2026-06-20  (cleanup + panel redesign)
- **Panel redesign (Ingress).** The "pick a HomePod" sidebar panel is rebuilt for a clean,
  Home-Assistant-native look — all functionality and behavior unchanged. Card-based speaker list
  with subtle borders/elevation and a clearer name → "HomePod: …" hierarchy; **color-coded status
  pills** (playing → green, idle → grey, released → blue, starting… → amber, needs-verification →
  red) that read correctly in light *and* dark; consistent primary / ghost / destructive button
  styling; selectable HomePod picker rows with a "playing here" indicator; a tidier per-room
  ⚙ Settings drawer; and a responsive layout that holds up in a narrow (~360px) Ingress pane. Still
  a single self-contained HTML string — no external fonts/CSS/JS/images, system font stack only.
- **Dead-code removal.** Dropped the unused `roomStore.setHomePod` (the name-only store setter
  superseded by `setHomePodBinding` / `setName` / `setNameManual` — no callers anywhere) and a stale
  refactor-leftover comment. No behavior change; all tests still pass.

## Speakers 0.12.0 — 2026-06-20  (Wave 3: push-state)
- **go-librespot `/events` websocket instead of per-200ms `/status` polling** in the bridge — cuts
  the multi-room polling churn ~15x and reacts to volume/transport/track changes as they happen.
  Implemented as a **minimal stdlib RFC 6455 client** (no new dependency).
- **Safe + additive:** `/status` seeds on every (re)connect; on any ws error it **falls back to
  polling**; and a **/status re-seed every ~3s while connected** bounds staleness if go-librespot's
  event payloads ever differ from what we map. The bridge's reconcilers + the "never start loud"
  volume cap are unchanged — only the per-tick state SOURCE moved to in-memory pushed state.
- Lays the **track-change signal** (metadata.uri) for the future buffer-flush. New unit tests:
  event→state mapping, ws frame parser, RFC accept-key vector.
- ⚠️ **On-device:** verify the real ws connect + event payload shapes on the VM (the poll fallback +
  3s re-seed keep it working regardless, but confirm the fast-path actually fires).

## Speakers 0.11.1 — 2026-06-19  (never-loud safety fix)
- **A fresh session can no longer start at full blast.** The volume cap was a one-time-per-boot flag,
  so a *second* account's new session (e.g. a family member's Spotify, with a remembered 100%) slipped
  through loud — and the cap was reactive (after audio started). Now: (1) while no session is active
  the HomePod output is held at/under the cap (so the FIRST audio of any new session can't exceed it),
  and (2) EVERY inactive→active transition caps both go-librespot and the HomePod. Re-armed per
  session, proactive, per room. `initial_volume`/cap = 35%%.

## Speakers 0.11.0 — 2026-06-19  (UX-1b: per-room settings)
- **Per-room grace + bitrate, editable in the panel** (⚙ Settings per speaker). Stored in rooms.json;
  empty = inherit the global add-on option (which stays the default). Grace takes effect live; a
  bitrate change re-renders + restarts that room's go-librespot.
- New endpoint `POST /api/rooms/<id>/settings`; `/api/rooms` rows expose effective values + override
  flags. **No config.yaml fields removed** — `speaker_name`/`homepod_name`/`bitrate`/
  `network_interface`/`grace_minutes` all kept (bitrate/grace are now the global defaults).

## Speakers 0.10.2 — 2026-06-19  (panel owns naming)
- **Picking a HomePod now names the speaker after it — for every room, including the primary.** The
  legacy `speaker_name` option no longer overrides an explicit pick (it was only the migration seed);
  only a panel **Rename** pins a custom name. Fixes the primary staying "PodConnect Test" after you
  picked "Køkkenalrum HomePod". Re-Save the selection once on 0.10.2 to apply it to an already-picked
  primary.

## Speakers 0.10.1 — 2026-06-19  (multi-room port fix)
- **Fix: each room's OwnTone now binds its own HTTP/DAAP port.** `renderOTConfig` set
  `websocket_port` but not the library `port`, so every OwnTone instance fell back to the default
  3689. Single-room (r0) happened to match; a **second room** would collide on 3689 and its OwnTone
  would be unreachable. Now `port = <OTPort>` is written per room (r0 still 3689; new rooms 38xx).
- No effect on the working single-room setup.

## Speakers 0.10.0 — 2026-06-19  (UX-1: self-healing naming)
- **Rename a HomePod in Apple Home → it syncs everywhere, automatically.** A room is now bound to its
  HomePod by the **stable OwnTone output id** (not the name), and the selection loop **self-heals**:
  on a rename it persists the new name and (unless you pinned the room name) follows it on the Connect
  device + HA entity, re-rendering + bouncing that room's go-librespot. No re-pick needed.
- **Picking captures the id** (`/api/select` + `POST /api/rooms`); migrated r0 self-populates its id on
  the first tick (matches by name once, then by id forever).
- **Per-room rename in the panel** (`POST /api/rooms/<id>/rename`) — a "✎ Rename" pins a custom name
  (`name_manual`) that the auto-sync won't overwrite.
- New unit tests: `matchOutput` (id-first / name-fallback / sole-device), rooms.json round-trip with
  the new fields.
- **UX-1b (still to do):** per-room grace/bitrate in the panel; shrink the add-on Configuration tab.

## Speakers 0.9.0 — 2026-06-19  (multi-room — needs on-device validation)
- **Multi-room.** N HomePods, each its own (go-librespot + OwnTone) pair, added live from the panel
  ("Add speaker → pick HomePod") with no add-on restart. Per-room volume/transport/grace all
  generalize (one goroutine each).
- **Architecture change:** the Go manager now **forks & supervises** each room's two child processes
  (os/exec; restart-on-exit + the folded go-librespot hang-watchdog + per-room HomePod selection).
  The s6 services `go-librespot`, `owntone`, `gl-watchdog`, `select-homepod` are **removed** — the
  manager owns them. Children run in their own process group (die-with-parent via Pdeathsig).
- **Back-compat:** on first boot the existing single speaker is migrated to room `r0`, keeping its
  **legacy paths + ports** (`/data/go-librespot` creds + device_id, `/data/owntone` library,
  `/srv/media/spotify`, go-librespot 3678 / OwnTone 3689/3688 — HA's watchdog stays on 3689). New
  rooms (idx ≥ 1) get `/data/rooms/<id>` + ports 37xx/38xx/39xx. Room ids/idx are monotonic.
- New API: `GET /api/rooms`, `POST /api/rooms`, `DELETE /api/rooms/<id>`, `GET /api/discover`;
  `/api/state` stays as the room-0 back-compat view. New unit tests: port allocator, rooms.json
  round-trip, add-room uniqueness, device-id stability.
- ⚠️ **Test on the VM first.** This rips out the s6 audio services in favour of manager supervision;
  the Go compiles + unit-tests green, but process-spawning / mDNS / two-rooms-at-once are only
  verifiable on real hardware. See `docs/GREEN-TESTING.md`.

## Speakers 0.8.0 — 2026-06-19
- **Snappy skips:** OwnTone `start_buffer_ms = 500` (was the default 2250 ms output pre-buffer —
  the cause of the ~2–4 s AirPlay skip lag). Maintainer-blessed floor; raise toward 700–1000 ms if
  underruns appear on a weak network.
- **Manager API for the new companion integration:** `POST /api/play` (resume), `PUT /api/volume`
  `{"volume":0..100}`, and a `volume` field added to `GET /api/state`.

## New: PodConnect Speakers integration 0.1.0 — 2026-06-19
- A second, local custom integration (`custom_components/podconnect_speakers/`) that wraps the
  add-on's HTTP API as a real **`media_player`** (+ a "Release HomePod" button), so the
  **account-agnostic Stop/Pause works via HA Assist voice** ("stop the kitchen") and dashboards, in
  the right Area — independent of which Spotify account is playing. `media_pause`/`media_stop` →
  `/api/stop`, `media_play` → `/api/play`, volume → `/api/volume`.
- Distribution note: HACS allows one integration per repo (that's the Spotify `podconnect`
  Control), so install this one **manually** for now (copy the folder to `config/custom_components/`)
  — a dedicated repo / HACS path is a follow-up. Needs Speakers add-on ≥ 0.8.0 for play/volume.

## Speakers 0.7.0 — 2026-06-19
- **Stop button (account-agnostic).** Panel "⏹ Stop music" + `/api/stop` pause go-librespot
  *locally*, so they stop whoever is playing — including a family member's Spotify the Web API
  can't reach — without giving the HomePod away (distinct from Release).
- **HomePod-name forwarding.** Leave `speaker_name` empty → the Connect speaker + HA entity
  auto-name after the HomePod you pick (e.g. "Køkkenalrum"); applied live (go-librespot bounce).
  Device id is now persisted independently of the name, so renaming never spawns a ghost device.
- **Configurable grace-release** via `grace_minutes` (default 3; 0 = free as soon as idle).
- **Picker now-playing line:** shows ▶ playing (with track) / ⏏ released / ⏸ idle.
- CI: `manager/**` now triggers the image build (the manager compiles into the image).

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
