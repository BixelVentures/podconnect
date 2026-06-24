# Stage A — device-aliases probe (runbook)

Goal: prove whether a non-certified go-librespot can make ONE Spotify session show **multiple
selectable rooms** in the Spotify app's Connect menu (Spotify "device aliases" / multiroom zones),
with clean audio and no flip-flop. This is the only architecture that can keep multiple PodConnect
rooms visible in the Connect menu on ONE account (see memory: device-aliases path).

The implementation is **dormant by default** — the released image ships the prebuilt go-librespot and
the runtime flags are off. Nothing below affects a normal install until you opt in.

## What ships

- `podconnect/patches/aliases-v0.7.3.patch` — the verified fork of go-librespot v0.7.3 (8 touchpoints:
  emit `aliases` in zeroconf getInfo with empty `remoteName`, populate `DeviceInfo.device_aliases` +
  `selected_alias_id`, read `target_alias_id` on the transfer command, log + echo the selection).
- Dockerfile `GL_ALIASES` build-arg (default `0` = prebuilt binary, unchanged). `1` = build the fork.
- Runtime options `experiment_aliases` (bool) + `connect_aliases` (list of room names).
- CI `verify-aliases-patch` job — compile-guards the patch on every change.

## Running the probe

1. **Build the fork image:** GitHub → Actions → "Publish add-on image" → Run workflow →
   tick **"Build the device-aliases fork"** → run. This rebuilds the current version tag with the fork
   binary (behaviourally identical to stock until the runtime flags below are set).
2. **Update the add-on** in HA → it pulls the fork image → restart.
3. **Configuration tab:** set
   ```yaml
   experiment_aliases: true
   connect_aliases: ["Køkken", "Stue", "Soveværelse"]
   ```
   (Use the rooms you want to see.) Save → restart the add-on.
4. **Open the Spotify app** → Connect/device menu.

## Reading the result (go/no-go)

- **GREEN** — both must hold:
  - (a) the rooms appear as **separate devices** in the Spotify Connect menu;
  - (b) tapping a room logs `ALIAS-PROBE: ... selected alias id=N name="Stue"` in the add-on log, over
    the one session, with **no flip-flop** (play → switch → wait 60 s → no auto-switch-back).
  → The mechanism works. Next: wire the manager to map alias→room and move the OwnTone output on
  select (Stage B integration).
- **RED** — aliases never render, or selection arrives with `NO target_alias_id`:
  → non-certified clients are gated. Fall back to the N-session-in-one-process probe, else architecture
  A (room switch in our panel/voice).

## Reverting

Set `experiment_aliases: false` (instant), and/or re-run Publish without the fork tick (back to the
prebuilt binary). The patch and flags are inert when off.
