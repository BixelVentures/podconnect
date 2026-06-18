# PodConnect (test slice) — install & test on your HA Green

This add-on makes **one HomePod** appear as a **Spotify Connect speaker**, running on your
Home Assistant Green. It's the first proof that the full PodConnect idea works on your
hardware. (Multi-room + the "Add speaker / pick HomePod" UI come later — see
[`docs/PLAN.md`](../docs/PLAN.md).)

## 1. Get the files onto the Green

1. Install the **Samba share** add-on (or **Advanced SSH & Web Terminal**) and start it.
2. Copy the whole **`podconnect/`** folder into the Green's **`/addons`** share, so you end
   up with `/addons/podconnect/config.yaml` (and `Dockerfile`, `build.yaml`, `rootfs/`).

## 2. Install the add-on

1. **Settings → Add-ons → Add-on Store**.
2. Top-right **⋮ → Check for updates**.
3. A **Local add-ons** section appears with **"PodConnect (test slice)"**. Open it → **Install**.

> ⏳ The first install **compiles OwnTone from source** on the Green — expect **~15–40 minutes**.
> This happens only once. Later starts are instant.

## 3. Configure

On the add-on's **Configuration** tab:

| Option | What to put |
|---|---|
| `speaker_name` | The name you want in Spotify, e.g. `Kitchen` |
| `homepod_name` | The **exact** HomePod name from the Apple **Home** app, e.g. `Living Room` |
| `default_volume` | Starting volume `0`–`100` (e.g. `35`) |
| `bitrate` | `320` |

Save.

## 4. Start & test

1. **Start** the add-on, then open the **Log** tab.
2. Watch for: `Waiting for HomePod '…' to be discovered…` followed by `Selected HomePod '…'`.
   - If it keeps saying *Waiting*, the `homepod_name` doesn't match exactly, or the HomePod
     is on a different network/VLAN than the Green.
3. Open **Spotify** (Premium) on the same network → **Connect to a device** → choose your
   **`speaker_name`** → press **Play**.
4. 🎉 Audio plays on the HomePod. The **Spotify volume slider** changes the volume.

## 5. What it proves

Spotify Connect → go-librespot → pipe → OwnTone → AirPlay 2 → HomePod, all on the Green,
with the HomePod auto-selected by name (no manual clicking). Credentials are saved, so the
speaker stays paired across restarts.

## Troubleshooting

- **"Waiting for HomePod…" forever** → wrong `homepod_name`, or HomePod not on the same
  network as the Green.
- **Speaker missing in Spotify** → confirm the add-on is *started*, you have Spotify
  Premium, and the phone is on the same network. Check the **Log**.
- **Selected but no sound** → check the Log for `go-librespot` / `owntone` errors and share them.
