# PodConnect — outstanding test checklist

Everything shipped but not yet validated on hardware. Tick as you go. **Do multi-room (C) on the VM
first** — it replaced the s6 audio services with manager supervision.

## A. Update / install
- [ ] Add-on → **0.14.0** (Add-on Store). Control → **0.7.1** (HACS).
- [ ] **Re-authorize Control once** (profile scopes from 0.5.0 — else Top/Recent/Liked are empty).

## B. Speakers — single room (r0 must still work after the multi-room migration)
- [ ] Audio plays on the HomePod; **migration kept it** (no Spotify re-login, no duplicate device).
- [ ] **Volume sync** both ways (Spotify/HA slider ⇄ HomePod buttons); never starts at full blast.
- [ ] **Play/pause sync**; HomePod top-tap pauses/resumes; no flicker.
- [ ] **⏹ Stop** (panel) stops playback, keeps the HomePod.
- [ ] **⏏ Release** frees it for AirPlay apps; auto-release after `grace_minutes` idle; reclaim on play.
- [ ] **Auto-name:** blank `speaker_name` → pick a HomePod → Connect device renames to it.
- [ ] **Snappy skips:** a skip lands ~0.5 s (not 2–4 s); listen for dropouts (raise `start_buffer_ms` if any).
- [ ] **Push-state (0.12.0):** volume/transport reflect promptly; force a ws drop → falls back to polling and still tracks.
- [ ] Picker shows ▶ now-playing / ⏸ idle / ⏏ released.

## C. Speakers — multi-room (0.9.0 — VM FIRST)
- [ ] **Add speaker** → pick a 2nd HomePod → it appears in Spotify with its own name, no restart.
- [ ] Both rooms **play independently**; per-room volume + play/pause work.
- [ ] **Remove** a room → its HomePod is freed, no leftover/ghost, r0 unaffected.
- [ ] Reboot the add-on → both rooms come back (rooms.json persisted).
- [ ] **Rename a HomePod in Apple Home** → the speaker name follows everywhere (Connect + HA entity), no re-pick. Then ✎ **Rename** a room in the panel → that custom name sticks (auto-sync won't overwrite it).

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

## G. Attention / voice-duck API (0.14.0 — the only part not loop-tested)
> **Coverage note.** The duck state machine is **unit-tested** (`att.tick` engage/heartbeat/expiry/
> release, TTL clamp) and the HTTP layer is **httptest-covered** (engage/GET/release, auth, 404/503/
> 405). What is **not** unit-tested is the `roomBridge` *loop integration* — the actual I/O of forcing
> the HomePod level, **skipping the reconcile** while held, restoring on release, and the **never-loud
> latch** (a fresh session that starts mid-duck). `roomBridge` has never had a loop test (it's a
> live-I/O 200 ms loop), so this gap is on par with existing coverage and lands here, on hardware.
- [ ] **Duck:** `curl -XPOST <host>:8099/api/attention -d '{"room":"r0","level":5,"ttl_ms":2000}'`
      while music plays → HomePod drops to ~5% within a tick; **music keeps playing** (not paused).
- [ ] **Heartbeat holds:** re-POST every ~0.5 s → stays ducked; a HomePod button press does **not**
      pull it back up while held (the duck wins).
- [ ] **Auto-release:** stop POSTing → within ~`ttl_ms` the volume **restores** to the pre-duck level
      on its own (no release call needed).
- [ ] **Explicit release:** `POST /api/attention/release {"room":"r0"}` → restores immediately.
- [ ] **Lounge step:** `level:35` after the AI "finishes" → fades to ~35% and holds; release → back up.
- [ ] **Never-loud across a duck:** while ducked, have a *second* account grab the HomePod → on
      release the volume must **not** jump to a remembered 100% (the fresh-session cap still fires).
- [ ] **Auth:** set `attention_token` → requests without `X-PodConnect-Token` get **401**.

---
_Reference: `docs/ATTENTION-API.md` (the duck contract) · `docs/GREEN-TESTING.md` (deeper bring-up + the deferred buffer-flush §D)._
