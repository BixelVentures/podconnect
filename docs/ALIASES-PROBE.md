# Device-aliases — how multi-room works (and how it was proven)

PodConnect shows **multiple selectable rooms in the Spotify Connect menu, on ONE account**, with clean
audio — pick a room and the audio moves to that HomePod. It uses Spotify's own **device aliases**
(multiroom zones). As of **0.25.0 this is the only mode** — it's on by default, zero config (the old
per-room multi-engine model and the `persistent_connect` / `experiment_aliases` experiments were
removed). This file documents how it works and how it was cracked.

## How it works
- A **single** go-librespot engine (the primary room) advertises every room as a Spotify device alias.
  The alias list auto-derives from your panel rooms (advanced override: the `connect_aliases` option,
  whose order must match your room order).
- Picking a room in the Spotify app sends a `transfer` whose **`target_alias_id`** sits at the dealer
  **RequestPayload top level** (not inside `command`/`command.options` — that wrong path is why early
  probes logged "NO target_alias_id"). The fork reads it → sets `DeviceInfo.SelectedAliasId` → pushes a
  `selected_alias` event on `/events` (and also exposes it on `/status` as a backstop).
- The manager's `routeAliasOutput` maps the alias id → that room → selects its HomePod on the single
  OwnTone output. The other rooms have **no engine of their own** (no contention).

## What you should see
- **Spotify app → Connect menu:** each room appears as its own device.
- **Add-on log when you pick a room:** `ALIAS-PROBE: ... selected alias id=N name="Stue"` then
  `alias-route: alias N -> room "Stue" -> HomePod "..."`. Audio follows (~1–2 s, AirPlay's switch).

## The key finding (the eSDK question, answered)
`device_aliases` is documented for Spotify's **certified eSDK**, so it was unknown whether a
non-certified client (go-librespot) would get the aliases rendered + the selection signal back. **Both
work** — proven on-device 2026-06-28: the aliases render, and the pick arrives as the payload-level
`target_alias_id`. It is **not** eSDK-gated. The implementation lives in
`podconnect/patches/aliases-v0.7.3.patch` (a fork of go-librespot v0.7.3, built into the image; a CI
job compile-guards the patch).

## Reverting (if ever needed)
There's no runtime switch — aliases are the architecture now. To revert you'd `git revert` the
device-aliases work and ship the prebuilt go-librespot again. See [`../CHANGELOG.md`](../CHANGELOG.md)
(0.23.0 → 0.25.0) for the full trail.
