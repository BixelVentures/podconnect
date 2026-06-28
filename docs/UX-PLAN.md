# PodConnect — unified UX, install & update plan

> **Current state (2026-06-28).** The 2-part split + panel-owns-setup direction below is still how it
> works. Two updates: the `speaker_name`/`homepod_name` config seeds were removed, and multi-room is now
> **device-aliases only** (one engine, rooms in the Spotify Connect menu on one account) — so
> "multi-account later" is moot for multi-room. Current truth: [`FEATURE-STATUS.md`](FEATURE-STATUS.md).

Goal (user's words): **least effort, one coherent experience, don't break what works.** Decided
direction: **TWO installable parts** (not three), the **sidebar panel owns all speaker setup**, and
naming/selection is **zero-typing**.

## The 3 problems today
1. **Config is split.** Speaker setup lives in *both* the add-on's deep **Configuration tab**
   (`speaker_name`, `homepod_name`, `bitrate`, `grace_minutes`) *and* the **panel** (pick HomePod).
   Auto-naming was expected in the panel but `speaker_name` sits in the config tab → confusing.
2. **Three installable pieces.** Add-on + Control + the new `podconnect_speakers` companion. The 3rd
   (account-agnostic voice `media_player`) is an awkward manual install.
3. **Multi-room naming** isn't streamlined yet — `speaker_name` is a single global field, but with N
   rooms each should self-name from its HomePod.

## Target architecture — 2 parts
```
1. PodConnect Speakers   (add-on, Add-on Store)   — audio engine + THE control panel (rooms)
2. PodConnect Control    (integration, HACS)      — all HA entities (Spotify + the local speakers)
```
No third thing. The companion `media_player` folds into Control (below).

---

## Workstreams

### UX-1 — ✅ DONE (Speakers 0.10.0 + 0.11.0): self-healing naming + per-room settings
Bound to the HomePod by **stable OwnTone output id**, self-heals on rename → Apple-Home rename syncs
the Connect device + HA entity automatically (unless the room name is user-pinned via the panel's
✎ Rename). Migrated r0 self-populates its id. **UX-1b ✅ (0.11.0):** per-room grace + bitrate are now
editable in the panel (⚙ Settings; empty = inherit the global default). The legacy config-tab options
are kept as defaults/fallback (not yet shrunk to advanced-only — minor cosmetic follow-up).
(Original spec below.)

#### UX-1 — The panel becomes the single control surface (add-on)
- The panel is the room manager: **list rooms**, **"Add speaker → pick HomePod"** (auto-named, zero
  typing), **rename / remove**, and **per-room settings** (grace, bitrate) inline.
- **Auto-naming lives here.** Picking a HomePod names the speaker (Connect device + HA entity). There
  is no global `speaker_name` in multi-room — each room self-names.
- **Name auto-sync (robustness fix).** Today selection + naming key off the HomePod's *name*, so
  renaming the HomePod in Apple Home would break the match and lose the speaker. Switch to selecting
  by the AirPlay output's **stable id**, and auto-update the stored room name when the HomePod's name
  changes → renaming the HomePod in Apple Home propagates everywhere automatically, no re-pick.
- **Shrink the add-on Configuration tab** to *advanced-only* (`network_interface`). Keep
  `speaker_name`/`homepod_name`/`grace_minutes` readable for one release as a deprecated fallback
  (migrated into rooms.json), then drop from the schema. The user should never need the config tab.
- The multi-room panel already exists (0.9.0); this is polish: settings inline, clearer naming, a
  "this is the only place you need" layout.

### UX-2 — ✅ RESOLVED: no local entities (companion retired; reverted in 0.7.1)
Decision: **never show two media_players per HomePod.** The companion `podconnect_speakers` is retired
(deleted), AND the brief 0.7.0 attempt to re-expose a local speaker media_player inside Control was
**reverted in 0.7.1** — Control is one Spotify entity per Connect device, period. The account-agnostic
Stop/Release lives **only in the add-on panel** (+ Siri); HA Assist "stop music" already pauses your
own playback via the Spotify entity. So: **two installs (add-on + Control), one entity per HomePod.**
(Original fold spec kept below for history.)

#### UX-2 — Fold the companion media_player into Control (retire the 3rd piece)
- Move the account-agnostic speaker `media_player` (+ Release button) **into Control**, created
  **only when** an optional **"PodConnect Speakers add-on URL"** is set in Control's options
  (default-suggest `http://<HA-IP>:8099`, validated against `/api/state`).
- **Preserves the core principle** (`docs/releasing.md` #2): Control still works **without** the
  add-on — the local speaker entities simply don't appear until you give it the URL. No hard coupling.
- Result: one HACS integration exposes *both* the per-account Spotify Connect `media_player`s **and**
  the per-physical-speaker account-agnostic one ("stop the kitchen" by voice). The separate
  `custom_components/podconnect_speakers/` is **retired** (code kept in git history; remove from the
  install path) — so the user installs **two** things, period.
- With multi-room, Control reads `GET /api/rooms` and makes one local `media_player` per room.

### UX-3 — Install & update story (two clicks, auto-updates)
- **Speakers:** Add-on Store → Install → opens the panel. Updates: the Store shows "Update" when
  `config.yaml` version bumps (CI builds the image first). No re-config.
- **Control:** HACS → Install → add the integration → Spotify sign-in; optionally paste the add-on
  URL for local speaker entities. Updates: HACS shows "Update" on each GitHub Release.
- Document both in one short "Getting started" in the README; the panel + Control config flow carry
  the rest. No YAML, no manual file copying.

### UX-4 — Assist polish
- ✅ **Audiobooks/shows/episodes in search** (Control 0.6.0) — bedtime stories resolve to the real
  audiobook, not a same-named song.
- ✅ **System prompt** for a real-assistant feel (`docs/gemini-system-prompt.md`) — confirms what it
  actually played, says when it couldn't, picks audiobook vs song vs playlist sensibly.
- **User-side (documented):** set Gemini safety filters to "Block none" (fixes
  `PROHIBITED_CONTENT`); expose entities + assign Areas (`docs/AREAS-AND-ASSIST.md`).

---

## Don't break what works (migration & safety)
- 0.9.0's rooms.json migration stays; UX-1's config-tab shrink keeps reading the legacy options for
  one release (migrated, not dropped abruptly).
- UX-2 is additive to Control: the new local entities are gated behind the optional URL, so existing
  Control installs are unaffected until the user opts in. Retiring `podconnect_speakers` only matters
  to anyone who manually installed it (just this user so far) — document the switch.
- Each step ships behind a version bump with the CHANGELOG/README/TODO updated (see memory:
  keep-docs-in-sync-on-release).

## Sequencing (all landed)
1. ✅ **Assist fixes** (Control 0.6.0) — immediate value, zero risk.
2. ✅ **UX-2** — resolved by *not* folding: the local entity was tried (0.7.0) and reverted (0.7.1).
   Two installs, one entity per HomePod; account-agnostic Stop/Release stays in the add-on panel.
3. ✅ **UX-1 panel consolidation** — self-healing naming (0.10.0) + per-room settings (0.11.0).
4. ✅ **UX-3 docs** — `GETTING-STARTED.md` covers the two-part install.
5. ✅ **Wave 3 push-state** (0.12.0). Remaining: on-device validation; multi-account later (optional).
