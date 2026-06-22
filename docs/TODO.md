# PodConnect — TODO

_Updated 2026-06-20. Speakers add-on **0.14.0**, Control integration **0.7.1**. The engine roadmap
(multi-room · naming · per-room settings · never-loud · push-state · **voice-duck attention API**) and
the Control feature set (search/browse/shuffle/repeat) are all built. What's left is on-device
validation + a couple of polish items._

## ✅ Done (it works, end to end)
**Speakers (add-on, 0.12.0):** HomePod picker (no-typing, `AirPlay 2` fix), test-sound, stable
device-id, mDNS interface restriction, go-librespot watchdog. **Multi-room** — N HomePods, each its
own (go-librespot + OwnTone) pair; manager forks/supervises children; rooms.json + r0 legacy
migration; Add / Remove / Rename / **⚙ per-room Settings** (grace + bitrate) in the panel, live with
no restart. **Self-healing naming** — bound by stable OwnTone output id; an Apple-Home rename syncs
the Connect device + HA entity automatically (unless the room name is pinned). **Bidirectional
volume sync** (per-output) + **proactive never-loud cap** (re-armed per session, so even a second
account's new session can't start loud). **Transport sync** (flicker-free, rapid-tap-safe).
**Snappy skips** (`start_buffer_ms = 500`). **Push-state** (go-librespot `/events` websocket, stdlib
client, with `/status` poll fallback + 3 s re-seed). **Grace-release** (configurable `grace_minutes`,
reclaim on resume, + manual "⏏ Release"). **Stop button** (`/api/stop`, account-agnostic local pause).
**Voice-duck Attention API** (`/api/attention`, 0.14.0) — external voice gatekeeper ducks a room with
an owner+deadline heartbeat; duck wins over the relay; auto-releases if the agent stops. Contract in
[`ATTENTION-API.md`](ATTENTION-API.md); the voice agent itself is a **separate project**.
**Control (integration, 0.7.1):** media_player per Connect device (transport/volume/play/source),
**shuffle**, **repeat**, **optimistic UI**, **search** (`SEARCH_MEDIA` → `/search`, popularity-ranked,
incl. **audiobooks / shows / episodes**) and **profile browse** (Playlists / Top Artists / Top Tracks
/ Recently Played / Liked Songs) — HA Assist "spil X i køkkenet" works. **One entity per HomePod**
(the brief 0.7.0 local-speaker player was reverted in 0.7.1). (Profile browse needs a one-time re-auth
for the extra scopes.)
**Proven on the VM:** HA + Spotify app + HomePod all control & reflect. (Every "VM" bug was code.)

---

## 🎯 Next — what actually remains

> History / rationale: [`docs/BUILD-PLAN.md`](BUILD-PLAN.md) (the engine waves — now essentially
> complete) and [`docs/UX-PLAN.md`](UX-PLAN.md) (the 2-part UX, panel-owns-everything). Assist prompt:
> [`gemini-system-prompt.md`](gemini-system-prompt.md).

### On-device validation (the main open item)
Multi-room ships as code but needs the VM/Green to prove process spawning, mDNS, and two rooms at
once. Push-state likewise needs the real ws connect + event payloads confirmed (poll fallback keeps
it working regardless). See [`GREEN-TESTING.md`](GREEN-TESTING.md) and [`TEST-CHECKLIST.md`](TEST-CHECKLIST.md).

**Attention/duck API (0.14.0):** the duck state machine (`att.tick`) and the HTTP layer are
unit/httptest-covered, but the **`roomBridge` loop integration** — forcing the level, *skipping the
reconcile* while held, restore, and the never-loud latch — is **not loop-tested** (consistent with
`roomBridge` never having had a loop test) and needs Green/VM validation. Steps in
[`TEST-CHECKLIST.md`](TEST-CHECKLIST.md) §G.

### Track-change buffer-flush (Green-deferred)
Unblocked by push-state's track-change signal (`metadata.uri`); still built + tuned on the wired
Green to avoid underruns. Full spec in [`GREEN-TESTING.md`](GREEN-TESTING.md) §D.

### (Optional) Multi-account — likely SKIPPABLE → see [`MULTI-ACCOUNT.md`](MULTI-ACCOUNT.md)
**Reality check:** multi-account *playback* already works for free — each person plays from their own
Spotify app, and because each room is its own go-librespot, **different people can play different
music to different rooms at the same time, today, no extra setup.** The deferred build (multiple
Control entries) only adds **HA-level** cross-account visibility/control/automation — low value for a
phone-first household. Account-agnostic "stop the wife's music" is already solved by the panel **⏹
Stop** (+ Siri). Build the rest only if HA cross-account dashboards/voice-routing are wanted.

### (Optional) Synchronized groups
Multi-room = **independent** speakers, not synchronized groups. Playing the *same* music in sync
across rooms is a separate, not-yet-built feature.

### HA Assist + Areas (honest scope)
Built-in media intents (pause/next/volume) work once the entity is exposed. Area assignment and
Assist **aliases are user data in HA's registry** — set in the UI, not by integration code. The
code lever we *do* have is HomePod-name-forwarding (done), so the entity self-names to its room and
`suggested_area` becomes meaningful. ✅ Setup documented in
[`docs/AREAS-AND-ASSIST.md`](AREAS-AND-ASSIST.md).

---

## 📋 Polish / quality
- ✅ **Configurable grace-release** + **per-room** grace/bitrate in the panel (Speakers 0.11.0).
- ✅ **Picker now-playing / released / idle** line. Remaining: a fuller visual polish.
- Keep batching releases → fewer HA store-cache dances; keep `CHANGELOG.md` current for both halves.
- ~~`docs/CONTRACT.md` (Speakers↔Control facade)~~ — **moot.** Control and Speakers are fully
  decoupled (the local-entity fold was reverted in 0.7.1), so there's no facade to stabilize.

## 🚫 Investigated dead-ends (don't re-attempt)
- HomePod **double/triple-tap (next/prev)** — gesture doesn't reach OwnTone; pipe next = stop+clear.
- iOS **system/native volume → Connect** — Apple killed it (iOS 17.3-17.6) for ALL Connect apps.

## Environment
- All proven on the **VM**. The wired **Green** is still the right home for smooth audio (no
  underruns/session-flap) and for safely testing the buffer-flush.
