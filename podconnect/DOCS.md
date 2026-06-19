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
| `speaker_name` | The name shown in Spotify, e.g. `Kitchen`. **Leave blank to auto-name** the speaker after the HomePod you pick in the panel. |
| `homepod_name` | *(optional fallback)* **Leave blank** and use the **PodConnect panel** instead (see below) — it lets you **pick** the HomePod from a live list, no typing. If you'd rather type, set the Apple **Home** name (case-insensitive). |
| `bitrate` | `320` |
| `network_interface` | *(advanced)* leave blank to auto-detect your LAN interface |
| `grace_minutes` | How long to hold the HomePod after playback stops before freeing it for other apps (default `3`; `0` = free as soon as idle). |

Save.

### Pick your HomePod — no typing (recommended)

After starting the add-on, a **PodConnect Speakers** item appears in the Home Assistant **sidebar**.
Open it to see a **live network scan** of every AirPlay device found (the same scan Spotify Connect
uses) — just **click your HomePod and Save**. Your choice is remembered across restarts and
overrides the `homepod_name` box. (The panel is also reachable directly at `http://<your-HA-IP>:8099`.)

The panel also has:
- **🔊 Play test sound** — sends a soft tone straight to the HomePod (proves the AirPlay path, no
  Spotify needed).
- **⏹ Stop music** — pauses whatever is playing, *regardless of which account* — without giving the
  HomePod away.
- **⏏ Release HomePod** — frees the HomePod so another AirPlay app (Mofibo, Apple Music…) can use
  it; press play in Spotify to take it back.
- A **now-playing / released / idle** status line.

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
