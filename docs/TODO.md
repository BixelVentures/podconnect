# PodConnect — TODO / roadmap

_Updated 2026-06-28. Speakers add-on **0.24.11**, Control integration **0.10.0**._

## ✅ Done (works end-to-end, on real hardware)

**The big one — multi-room on ONE account (device aliases), now the default.** A single go-librespot
engine advertises every room as a separate selectable device in the Spotify Connect menu, on one
account; picking a room routes the audio to that HomePod (selection pushed over `/events`, ~1–2 s =
AirPlay's switch only). Zero setup. The per-room multi-engine model and the `persistent_connect` /
`experiment_aliases` experiments were **removed** (cleanup, 0.25.0). Findings: [`ALIASES-PROBE.md`](ALIASES-PROBE.md).

**Speakers (add-on):**
- Per-room engines (default) **and** device-aliases mode (above); add-speaker in alias mode
  re-advertises on the primary (no rogue 2nd engine); panel shows alias rooms as `alias`.
- **Volume:** bidirectional sync; sane start (advertises `initial_volume`, slider never 100%);
  never-loud cap that survives transfer + reclaim-drift.
- **Stability:** slim multi-stage image (fork built from source); graceful restart → no duplicate
  Connect entries; avahi host-name pinned + object cap raised; `buffer_ms` tunable.
- Self-healing naming, test tone, transport sync, grace-release/reclaim, account-agnostic Stop,
  ⏏ Release, voice-duck **Attention API** ([`ATTENTION-API.md`](ATTENTION-API.md)), push-state.

**Control (integration):**
- media_player per Connect device (transport/volume/shuffle/repeat/transfer, optimistic UI),
  search + browse (Playlists/Top/Recent/Liked).
- `media_player.play_media` plays a free-text name (search + top result); `play_from_library`;
  and response-returning **data services** `top_tracks` / `recently_played` / `liked` for an AI assist.

## 🎯 Next
- **Re-wire self-healing-on-rename for alias mode** (`selectHomePod`/`healBinding` are currently
  unwired — they'd fight the router; routeAliasOutput's id-match covers most renames). Surface the
  alias rooms more clearly in the panel.
- **Sub-second skips** — the floor is AirPlay's ~2 s; `buffer_ms` is the current knob. A track-change
  buffer-flush could help, tuned on the Green to avoid underruns.
- **Synchronized same-music groups** (one source → many HomePods at once) — separate, not built;
  OwnTone multi-output is the likely path.
- **Multi-account** (optional) — see [`MULTI-ACCOUNT.md`](MULTI-ACCOUNT.md). Mostly moot now that
  device-aliases gives multi-room on one account; only adds HA-level cross-account dashboards.
- Clean up the recurring `/events` ws reconnect log noise (pre-existing; harmless).

## 🚫 Investigated dead-ends (don't re-attempt)
- 3× zeroconf or 3× persistent go-librespot on one account → flip-flop / contention / kaput audio.
  (Device-aliases — one engine, N aliases — is the correct path.)
- HomePod **double/triple-tap (next/prev)** — gesture doesn't reach OwnTone.
- iOS **system/native volume → Connect** — Apple removed it (iOS 17.3–17.6) for all Connect apps.
- Forcing the Spotify slider perfectly truthful via `external_volume:false` — breaks the physical
  HomePod button. Current model (advertise `initial_volume`) keeps both.

## More
[`PLAN.md`](PLAN.md) (architecture) · [`control-plan.md`](control-plan.md) (integration) ·
[`UX-PLAN.md`](UX-PLAN.md) · [`BUILD-PLAN.md`](BUILD-PLAN.md) (history) ·
[`GREEN-TESTING.md`](GREEN-TESTING.md) · [`releasing.md`](releasing.md).
