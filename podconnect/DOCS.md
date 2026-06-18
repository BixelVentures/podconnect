# PodConnect Speakers — install on Home Assistant

This add-on makes **one HomePod** appear as a **Spotify Connect speaker**, running on your
Home Assistant Green. It's the first proof that the full PodConnect idea works on your
hardware. (Multi-room + the "Add speaker / pick HomePod" UI come later — see
[`docs/PLAN.md`](../docs/PLAN.md).)

**You need:** Spotify **Premium**, and the HomePod on the same network as the Green.

## 1. Add the repository

1. In Home Assistant: **Settings → Apps → Add-on Store**.
2. Top-right **⋮ → Repositories**.
3. Paste **`https://github.com/BixelVentures/podconnect`** → **Add** → close.

## 2. Install (downloads a prebuilt image — ~2 min, no compiling)

1. Refresh the store → open **PodConnect Speakers** → **INSTALL**.
2. It **downloads a ready-made image** for your Green — no on-device build.

## 3. Configure

| Option | What to put |
|---|---|
| `speaker_name` | The name shown in Spotify, e.g. `Kitchen` |
| `homepod_name` | Which HomePod to play to. **Leave blank** if you have just one HomePod (it's auto-selected). With several, set the Apple **Home** name — the add-on **Log** prints `AirPlay devices found: …` so you can copy the exact name. Matching is case-insensitive. |
| `bitrate` | `320` |
| `network_interface` | *(advanced)* leave blank to auto-detect your LAN interface |

Save.

## 4. Start & test

1. **Start** the add-on → open the **Log** tab.
2. Watch for `Selected HomePod '…'` (it found and locked onto your HomePod).
   - Stuck on `Waiting for HomePod '…'`? The `homepod_name` doesn't match exactly, or the
     HomePod is on a different network/VLAN than the Green.
   - `… needs AirPlay verification`? In the Apple **Home** app, set this HomePod's
     **"Allow Speaker & Display Access"** to **"Anyone on the Same Network"**.
3. Open **Spotify** (Premium) → **Connect to a device** → choose your **`speaker_name`** → **Play**.
4. 🎉 Audio plays on the HomePod; the Spotify volume slider changes the volume.

## How it works

Spotify Connect → go-librespot → pipe → OwnTone → AirPlay 2 → HomePod, all inside this
add-on, with the HomePod auto-selected by name. It uses standard **Spotify Connect** (no
Spotify developer keys needed) and does **not** use the Apple TV integration. Credentials
are saved, so the speaker stays paired across restarts.

## Troubleshooting

- **Install can't pull the image** → the GHCR package must be **public**:
  github.com/BixelVentures → **Packages** → `aarch64-podconnect` → **Package settings** →
  **Change visibility → Public**.
- **"Waiting for HomePod…" forever** → wrong `homepod_name`, or HomePod on a different network.
- **Speaker missing in Spotify** → add-on started? Spotify Premium? Same network? Check the Log.
- **Selected but no sound** → check the Log for `go-librespot` / `owntone` errors and share them.
