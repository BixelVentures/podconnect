# PodConnect — TODO

_Updated 2026-06-19. Volume + transport + sharing all built & largely VM-validated._

## ✅ Done (it works, end to end)
**Speakers (add-on, 0.7.0):** HomePod picker (no-typing, `AirPlay 2` fix), test-sound, stable
device-id, mDNS interface restriction, go-librespot watchdog. **Bidirectional volume sync**
(Spotify/HA ⇄ HomePod buttons, per-output). **Transport sync** (play/pause, flicker-free,
rapid-tap-safe via confirm-tracking). **initialVolumeCap** (never full blast). **Grace-release**
("deling": configurable `grace_minutes`, reclaim on resume, + manual "⏏ Release" button).
**Stop button** (`/api/stop`, account-agnostic local pause). **HomePod-name forwarding**
(auto-name the speaker; ghost-free). **Picker now-playing/released/idle** line.
**Control (integration, 0.5.0):** media_player per Connect device (transport/volume/play/source),
**shuffle**, **repeat**, **optimistic UI**, **search** (`SEARCH_MEDIA` → `/search`, ranked) and
**profile browse** (Playlists/Top Artists/Top Tracks/Recently Played/Liked Songs) — HA Assist
"spil X i køkkenet" works. (Profile browse needs a one-time re-auth for the extra scopes.)
**Proven on the VM:** HA + Spotify app + HomePod all control & reflect. (Every "VM" bug was code.)

---

## 🎯 Next — prioritized

> **Build-ready, research-validated, parallel-agent decomposition:**
> [`docs/BUILD-PLAN.md`](BUILD-PLAN.md) (companion integration · push-state · multi-room · snappy
> skips). Multi-account deferred by request.
>
> **Unified UX / install plan (2 parts, panel owns everything):**
> [`docs/UX-PLAN.md`](UX-PLAN.md) — fold the companion media_player into Control (retire the 3rd
> piece), panel-owns-all-setup, zero-typing naming. Assist: [`gemini-system-prompt.md`](gemini-system-prompt.md).

### ✅ Multi-room ("Add speaker") — built (Speakers 0.9.0), needs on-device validation
N HomePods, each its own (go-librespot + OwnTone) pair. Manager forks/supervises children; s6 audio
services removed; rooms.json + r0 legacy migration; `/api/rooms` + `/api/discover` + Add-speaker
panel. **Validate on the VM/Green** (process spawning / mDNS / two rooms) — see `GREEN-TESTING.md`.

### ✅ HomePod name forwarding (done — Speakers 0.7.0)
Empty `speaker_name` → speaker auto-names after the picked HomePod; live `device_name` update +
go-librespot bounce; device id persisted independently (no ghost on rename).

### ✅ Speaker as a HA media_player → voice "stop/release" (done — `podconnect_speakers` 0.1.0)
NOT MQTT (HA has no MQTT media_player) — a companion custom integration wraps `/api/*` as a real
`media_player` (+ Release button). "stop the kitchen" → `HassMediaPause` → `/api/stop`, account-
agnostic. Distribution: installs manually for now (HACS = one integration per repo); a dedicated
repo is the follow-up.

### P2 — Multi-account  → scoped in [`MULTI-ACCOUNT.md`](MULTI-ACCOUNT.md)
Separated out: the account-agnostic **panel Stop/Release are the "house" controls** (the
"stop my wife's music" answer — account-neutral, already shipped). The real fix = **multiple Control
entries (one per person)** + voice account-routing; deferred until the engine is validated.

### P2 — Multi-account (old notes)
One Control (HACS) config entry per family member (each its own Spotify OAuth). Control is already
device-list-driven; multi-account = allow multiple config entries + per-entry coordinator. The
"stop another account's playback" problem is **already solved at the speaker level** (Stop button +
`/api/stop`, local pause — Speakers 0.7.0); per-account *play* control stays in each person's Control.

### P2 — HA Assist + Areas (honest scope)
Built-in media intents (pause/next/volume) work once the entity is exposed. Area assignment and
Assist **aliases are user data in HA's registry** — set in the UI, not by integration code. The
code lever we *do* have is HomePod-name-forwarding (done), so the entity self-names to its room and
`suggested_area` becomes meaningful. ✅ Setup documented in
[`docs/AREAS-AND-ASSIST.md`](AREAS-AND-ASSIST.md).

---

## 📋 Polish / quality
- **Track-change buffer-flush** (next/skip latency): **build & test on the wired Green** — full
  spec + acceptance criteria in [`docs/GREEN-TESTING.md`](GREEN-TESTING.md) §D.
- ✅ **Configurable grace-release** (`grace_minutes`, Speakers 0.7.0).
- ✅ **Picker now-playing / released / idle** line (Speakers 0.7.0). Remaining: a fuller visual polish.
- ✅ `CHANGELOG.md` (both components). Keep batching releases → fewer HA store-cache dances.
- `docs/CONTRACT.md` — the stable Speakers↔Control facade (so Control never binds to :3678/:3689).

## 🚫 Investigated dead-ends (don't re-attempt)
- HomePod **double/triple-tap (next/prev)** — gesture doesn't reach OwnTone; pipe next = stop+clear.
- iOS **system/native volume → Connect** — Apple killed it (iOS 17.3-17.6) for ALL Connect apps.

## Environment
- All proven on the **VM**. The wired **Green** is still the right home for smooth audio (no
  underruns/session-flap) and for safely testing the buffer-flush.
