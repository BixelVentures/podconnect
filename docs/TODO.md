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

### P1 — Multi-room ("Add speaker") ★ the big one
N HomePods, each its own (go-librespot + OwnTone) pair with unique ports/db/mDNS name. The
manager already takes a `Room` + `rooms()` (the seed) — multi-room = build the real list +
spawn/supervise per room + an "Add speaker → pick HomePod → name it" flow in the panel. Per-room
volume/transport/grace-release already generalize (N goroutines).

### ✅ HomePod name forwarding (done — Speakers 0.7.0)
Empty `speaker_name` → speaker auto-names after the picked HomePod; live `device_name` update +
go-librespot bounce; device id persisted independently (no ghost on rename).

### P1 — Speaker as a HA entity → voice "stop/release/take over" (MQTT)
The chosen "best practice" for account-agnostic voice control (deferred for now — panel only).
Publish each physical speaker from the add-on as a real **`media_player`** (+ a "release"
`button`) via **MQTT discovery**. Then "stop the music in the kitchen" hits the built-in pause
intent → `/api/stop` (local, any account), in the right **Area**, no custom sentences. Needs an
MQTT broker + a discovery publisher in the Go manager. Alt for no-broker setups: a documented
`rest_command` + `script` snippet calling `/api/stop` & `/api/release`.

### P2 — Multi-account
One Control (HACS) config entry per family member (each its own Spotify OAuth). Control is already
device-list-driven; multi-account = allow multiple config entries + per-entry coordinator. The
"stop another account's playback" problem is **already solved at the speaker level** (Stop button +
`/api/stop`, local pause — Speakers 0.7.0); per-account *play* control stays in each person's Control.

### P2 — HA Assist + Areas (honest scope)
Built-in media intents (pause/next/volume) work once the entity is exposed. Area assignment and
Assist **aliases are user data in HA's registry** — set in the UI, not by integration code. The
code lever we *do* have is HomePod-name-forwarding (done), so the entity self-names to its room and
`suggested_area` becomes meaningful. Remaining: document the assign-to-area + expose-to-Assist flow.

---

## 📋 Polish / quality
- **Track-change buffer-flush** (next/skip latency): the ~2-4s AirPlay buffer means a skip is heard
  late; flushing OwnTone on a go-librespot track change would make it instant — but risks a glitch
  (and worse underruns on the VM). **Build & test on the wired Green**, not the VM. (Variable
  fast/slow is Spotify prefetch — not ours.)
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
