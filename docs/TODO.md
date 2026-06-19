# PodConnect — TODO

_Updated 2026-06-19. Volume + transport + sharing all built & largely VM-validated._

## ✅ Done (it works, end to end)
**Speakers (add-on, 0.6.0):** HomePod picker (no-typing, `AirPlay 2` fix), test-sound, stable
device-id, mDNS interface restriction, go-librespot watchdog. **Bidirectional volume sync**
(Spotify/HA ⇄ HomePod buttons, per-output). **Transport sync** (play/pause, flicker-free,
rapid-tap-safe via confirm-tracking). **initialVolumeCap** (never full blast). **Grace-release**
("deling": hold through brief interruptions, free after 3min idle, reclaim on resume, + manual
"⏏ Release" button).
**Control (integration, 0.3.2):** media_player per Connect device (transport/volume/browse/play/
source), **shuffle**, **repeat**, **optimistic UI** (instant play/pause/shuffle/repeat).
**Proven on the VM:** HA + Spotify app + HomePod all control & reflect. (Every "VM" bug was code.)

---

## 🎯 Next — prioritized

### P1 — Multi-room ("Add speaker") ★ the big one
N HomePods, each its own (go-librespot + OwnTone) pair with unique ports/db/mDNS name. The
manager already takes a `Room` + `rooms()` (the seed) — multi-room = build the real list +
spawn/supervise per room + an "Add speaker → pick HomePod → name it" flow in the panel. Per-room
volume/transport/grace-release already generalize (N goroutines).

### P1 — HomePod name forwarding (nice, small)
Default the speaker name to the **picked HomePod's name** (e.g. "Køkkenalrum") instead of a manual
`speaker_name`, so the Connect device + HA entity auto-name sensibly. Needs a go-librespot
`device_name` update + restart on pick.

### P2 — Multi-account
One Control (HACS) config entry per family member (each its own Spotify OAuth). Control is already
device-list-driven; multi-account = allow multiple config entries + per-entry coordinator.

### P2 — HA Assist + Areas (mostly config, smooth it)
Works once the entity is exposed (built-in media intents: pause/next/volume). Polish: set a
**suggested_area** + **aliases** from the integration so "pause the kitchen" works with less setup;
document the room/area assignment.

---

## 📋 Polish / quality
- **Track-change buffer-flush** (next/skip latency): the ~2-4s AirPlay buffer means a skip is heard
  late; flushing OwnTone on a go-librespot track change would make it instant — but risks a glitch
  (and worse underruns on the VM). **Build & test on the wired Green**, not the VM. (Variable
  fast/slow is Spotify prefetch — not ours.)
- **Picker UI** — a more polished look; show current grace-release state; maybe a "now playing" line.
- **Configurable grace-release** period (3min default).
- `CHANGELOG.md` + batch releases → fewer HA store-cache dances.
- `docs/CONTRACT.md` — the stable Speakers↔Control facade (so Control never binds to :3678/:3689).

## 🚫 Investigated dead-ends (don't re-attempt)
- HomePod **double/triple-tap (next/prev)** — gesture doesn't reach OwnTone; pipe next = stop+clear.
- iOS **system/native volume → Connect** — Apple killed it (iOS 17.3-17.6) for ALL Connect apps.

## Environment
- All proven on the **VM**. The wired **Green** is still the right home for smooth audio (no
  underruns/session-flap) and for safely testing the buffer-flush.
