# PodConnect — outstanding test checklist

Everything shipped but not yet validated on hardware. Tick as you go. **Do multi-room (C) on the VM
first** — it replaced the s6 audio services with manager supervision.

## A. Update / install
- [ ] Add-on → **0.9.0** (Add-on Store). Control → **0.7.0** (HACS).
- [ ] **Re-authorize Control once** (profile scopes from 0.5.0 — else Top/Recent/Liked are empty).

## B. Speakers — single room (r0 must still work after the multi-room migration)
- [ ] Audio plays on the HomePod; **migration kept it** (no Spotify re-login, no duplicate device).
- [ ] **Volume sync** both ways (Spotify/HA slider ⇄ HomePod buttons); never starts at full blast.
- [ ] **Play/pause sync**; HomePod top-tap pauses/resumes; no flicker.
- [ ] **⏹ Stop** (panel) stops playback, keeps the HomePod.
- [ ] **⏏ Release** frees it for AirPlay apps; auto-release after `grace_minutes` idle; reclaim on play.
- [ ] **Auto-name:** blank `speaker_name` → pick a HomePod → Connect device renames to it.
- [ ] **Snappy skips:** a skip lands ~0.5 s (not 2–4 s); listen for dropouts (raise `start_buffer_ms` if any).
- [ ] Picker shows ▶ now-playing / ⏸ idle / ⏏ released.

## C. Speakers — multi-room (0.9.0 — VM FIRST)
- [ ] **Add speaker** → pick a 2nd HomePod → it appears in Spotify with its own name, no restart.
- [ ] Both rooms **play independently**; per-room volume + play/pause work.
- [ ] **Remove** a room → its HomePod is freed, no leftover/ghost, r0 unaffected.
- [ ] Reboot the add-on → both rooms come back (rooms.json persisted).

## D. Control — Spotify
- [ ] media_player per Connect device: play/pause/next/seek/volume/**shuffle/repeat**.
- [ ] **Browse**: Playlists / Top Artists / Top Tracks / Recently Played / Liked (after re-auth).
- [ ] **Search**: "spil <kunstner/sang>" picks the *right/popular* version; "spil <lydbog>" finds the **audiobook** (not a random song).
- [ ] **One entity per HomePod** — no duplicate "account-agnostic" player (removed in 0.7.1).

## E. Assist (Gemini)
- [ ] Gemini safety → **Block none** (no more `PROHIBITED_CONTENT` on normal input).
- [ ] System prompt pasted (`docs/gemini-system-prompt.md`) → it **confirms what it played** and **says when it can't**.
- [ ] Entities **exposed** + assigned an **Area** → "pause/stop i <rum>" hits the right speaker.
- [ ] "spil X i <rum>" searches + plays on that room's speaker.

## F. Cross-account (the wife scenario)
- [ ] Another account plays on a HomePod → **⏹ Stop** in the add-on **panel** (or "Hey Siri, stop") → it stops.

---
_Reference: `docs/GREEN-TESTING.md` (deeper bring-up + the deferred buffer-flush §D)._
