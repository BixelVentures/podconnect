# HA Green — bring-up + test notes

Everything below has been built; the single-room path is **VM-validated**, while **multi-room**
(Speakers 0.9.0+) and **push-state** (0.12.0) still need on-device confirmation. The wired **HA
Green** is the real home (no Wi-Fi flap, fewer audio underruns) and the right place to test the
audio-timing items — especially the **buffer-flush** (still deferred; see bottom).

> Ground rule we hold: don't blame "the VM". Every bug so far has been code. The Green is for the
> few genuinely environment-sensitive things (underruns, session flap) and for safely testing
> changes that risk a glitch.

---

## A. Bring-up checklist

1. **Install Speakers add-on** — it PULLS the prebuilt GHCR image (`ghcr.io/bixelventures/
   {arch}-podconnect`, aarch64 on the Green). Confirm version **0.12.0**.
2. **Install Control** via HACS (GitHub release **0.7.1**). Set up **Application Credentials** with
   your Spotify app (client id/secret), then OAuth-authorize. Profile browse needs the scopes added
   in 0.5.0 → if you authorized before that, **re-authorize** (reauth / remove + re-add).
3. **Pick the HomePod** in the panel; confirm it **auto-names** to the HomePod (and renames once in
   the Spotify app). Add a second room (**+ Add speaker**) to exercise multi-room.
4. **Verify the audio path:** panel → **🔊 Test** → you should hear a soft tone on the HomePod (proves
   OwnTone→AirPlay→HomePod with zero Spotify dependency).
5. **Play from Spotify** to the Connect device; confirm audio.

## B. Test matrix (what to verify on the Green)

| Area | Test | Expected |
|---|---|---|
| Volume | Move HomePod hardware buttons; move Spotify/HA slider | Both directions sync; **no session starts at full blast** (even a second account's new session is capped) |
| Transport | Play/pause from Spotify; top-tap the HomePod | Syncs both ways; no flicker; rapid taps OK |
| **Push-state** (0.12.0) | Watch volume/transport reflect in HA while playing | Reacts promptly (ws `/events`); on a forced ws drop it falls back to polling and still tracks |
| **Multi-room** (0.9.0+) | **+ Add speaker** → 2nd HomePod; play to each independently | Both appear in Spotify; independent volume/transport; remove one cleanly; both survive an add-on reboot |
| **Stop** | Press **⏹ Stop** in the panel while playing | Music stops; HomePod **not** given away. Ideally test with a **second account** playing → it still stops |
| **Grace-release** | Stop playback; wait `grace_minutes` | HomePod frees; Mofibo/Apple Music can grab it; pressing play in Spotify **reclaims** it |
| `grace_minutes` | Set to 0 and to e.g. 1 | 0 = frees ~immediately on idle; 1 = ~1 min |
| **Name-forward** | Pick a different HomePod | Connect device renames in place (brief disappear/reappear); **no ghost** device piles up |
| Picker status | Watch the panel while playing/idle/released | Shows ▶ now-playing / ⏸ idle / ⏏ released |
| Search (Assist) | *"spil <kunstner>"* to a room | Plays the top hit on that speaker |
| Profile browse | Browse media on the entity | Playlists / Top Artists / Top Tracks / Recently Played / Liked Songs (after re-auth) |
| Areas + Assist | Assign Area, expose to Assist, *"pause i køkkenet"* | Targets the right speaker (see `docs/AREAS-AND-ASSIST.md`) |

## C. Environment-sensitive things the Green should improve

- **Underruns / dropouts** — the wired path should be cleaner than the VM's bridged Wi-Fi.
- **Session flap / reconnects** — fewer go-librespot reconnects (the watchdog is the safety net).
- If any of these still happen on the Green, treat it as **code** (buffer sizing, reconnect
  handling), not "the network" — capture the add-on log.

---

## D. Deferred task to build & test HERE: track-change buffer-flush

**Why deferred:** glitch/underrun risk. The VM's audio path made underruns worse, so this is built
and tuned on the wired Green, not the VM.

- **Problem:** AirPlay buffers ~2–4 s, so a **next/skip** is heard late (the old track keeps playing
  from the buffer for a moment). (The *variable* fast/slow you saw is Spotify prefetch — not ours.)
- **Approach:** on a go-librespot **track-change** event, flush OwnTone's pipe/queue so the new
  track starts promptly instead of after the buffered tail.
- **Risk:** flushing too aggressively → an audible glitch or an underrun on weaker audio paths.
- **Acceptance:** skip latency drops to **< ~1 s** with **no audible glitch/underrun** across, say,
  20 consecutive skips and a few full songs. If it can't hit both, keep the buffer (correctness over
  snappiness).
- **Where:** the manager already subscribes to go-librespot `/events` and **already emits a
  track-change signal** (`metadata.uri` change, added with push-state in 0.12.0). The remaining work
  is the OwnTone flush call on that signal, behind a tunable threshold. Build on the Green, measure,
  then ship.
