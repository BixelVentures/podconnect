# PodConnect — TODO

_Updated 2026-06-19, after full end-to-end success (HA → Spotify → HomePod)._

## ✅ Done — the product does its core job
- **Speakers** add-on `0.2.5`: HomePod discovery (`AirPlay 2` fix), no-typing Ingress picker,
  named 🔊 test tone, stable device-id, mDNS interface restriction, go-librespot watchdog.
- **Control** integration `0.2.0`: OAuth (own Spotify app), media_player per Connect device,
  Browse & Play.
- **Proven end-to-end:** PodConnect Control plays Spotify on the mapped HomePod. Audio out of the
  HomePod confirmed (test tone + real playback). HA-driven control held where the Spotify app's
  Connect picker dropped on the VM.

---

## 🎯 Tomorrow — top priorities

### P1 — Volume-sync (the last rough edge) ★ start here
One slider should rule them all. Today three volumes multiply (Spotify app × OwnTone master ×
per-output), which is why levels feel random.
- go-librespot `external_volume: true` (stop double-scaling the PCM).
- Manager subscribes to go-librespot `/events`; on `volume`, relay to OwnTone
  `PUT /api/player/volume` (0–100 mapping).
- Result: the **Spotify slider** controls HomePod loudness; HA reflects it.
- Testable on the VM (audio already works there) — no Green required.

### P1 — Deploy to the wired HA Green + verify E2E
Move off the VM to the real target.
- Install Speakers + Control on the Green (Ethernet).
- Confirm: discovery is instant, the **Spotify-app Connect picker holds** (the VM's only genuine
  network failure), playback stable over time.
- This is the "is it production-real" checkpoint. Can run in parallel with volume-sync.

---

## 📋 Backlog — after the above

### Robustness / UX
- `CHANGELOG.md` for the add-on (kills the "No changelog found" + clarifies updates).
- Batch changes into fewer releases to reduce the HA store-cache dance.
- Live "▶ playing here" marker in the picker that updates as the active output changes.
- Re-check for ghost duplicate Connect devices after the Green move; auto-clean stale ones if needed.

### Features (roadmap)
- Control: deeper **browse/search** (track-level, search box), not just playlists.
- **Multi-room "Add speaker"** — N× (go-librespot + OwnTone) pairs, dynamic spawn/supervise,
  per-room picker. (The manager binary is the seed for this.)
- **Multi-account** (one HA config entry per family member).

### Architecture / compatibility
- `docs/CONTRACT.md` — stable contract between Control and the Speakers manager
  (`/podconnect/info`, events WS, volume) so the integration never binds directly to
  go-librespot:3678 / OwnTone:3689. HA Repairs card for version mismatches.

---

## Notes
- Volume-sync and the Green move are independent — do both tomorrow (one is code, one is hardware).
- Everything below the line is "make it nice / scale it," not "make it work" — that part is done.
