# Device-aliases — one-switch test (and how it works)

Goal: ONE Spotify session that shows **multiple selectable rooms** in the Spotify Connect menu (Spotify
"device aliases" / multiroom zones), on ONE account, clean audio, no flip-flop — and picking a room
**moves the audio** to that HomePod. This is the only architecture that keeps multiple PodConnect rooms
visible in the Connect menu on one account (see memory: device-aliases path).

As of Speakers 0.24.0 this is a **real feature behind one switch**, not a manual probe. The released
image already contains the device-aliases fork of go-librespot; it is dormant and behaviourally
identical to stock until you flip the switch.

## Turn it on (no GitHub steps)

1. Update the **PodConnect Speakers** add-on to 0.24.0+ → restart.
2. Configuration tab:
   ```yaml
   experiment_aliases: true
   ```
   That's it — aliases auto-derive from your configured rooms. (Optional advanced override:
   `connect_aliases: ["Køkken","Stue"]` — order must match your room order.)
3. Save → restart the add-on.

What happens when it's on:
- only the **primary** room's engine runs; the other rooms become **aliases** on it (one session, no
  contention);
- the per-room HomePod pin is suppressed; the **alias router** moves the single OwnTone output to the
  chosen room's HomePod;
- pick a room in Spotify's Connect menu → audio follows.

## What to look for

- **Spotify app → Connect menu:** do your rooms show up as separate devices?
- **Add-on log when you tap a room:** `ALIAS-PROBE: ... selected alias id=N name="Stue"` then
  `alias-route: alias N -> room "Stue" -> HomePod "..."`.
- Pick a different room → audio should move there, and not auto-switch-back within ~60 s.

## The one thing nobody can predict

`device_aliases` is documented for Spotify's **certified eSDK**. Whether a non-certified client
(go-librespot) gets the aliases rendered + the selection signal is the single unknown — visible the
moment you open the Connect menu. If the rooms appear and the log shows the route, it works. If they
never appear, non-certified clients are gated → fall back to architecture A (room switch in our panel /
voice). Routing maps the selection from `/status selected_alias_id`; if the live log shows the signal
arriving differently, it's a one-line tweak in `routeAliasOutput`.

## Rollback

Set `experiment_aliases: false` → restart. Back to today's per-room behaviour, identical. (To also drop
the fork binary: re-run Publish with `gl_prebuilt: true`.)
