# PodConnect (test slice) — install & test on your HA Green

This add-on makes **one HomePod** appear as a **Spotify Connect speaker**, running on your
Home Assistant Green. It's the first proof that the full PodConnect idea works on your
hardware. (Multi-room + the "Add speaker / pick HomePod" UI come later — see
[`docs/PLAN.md`](../docs/PLAN.md).)

## 1. Copy the files onto the Green (via Samba)

1. On your Mac, open **Finder** → press **⌘K** (menu: Go → Connect to Server).
2. Enter **`smb://homeassistant.local`** (or `smb://<your-Green-IP>`) → **Connect** → log in
   with your Home Assistant username/password.
3. A list of shared folders appears. Open the **`addons`** folder.
4. From your computer, copy the **`podconnect`** folder **into** `addons`.
   - ✅ Correct: `addons/podconnect/config.yaml`
   - ❌ Wrong: `addons/PodConnect/podconnect/config.yaml` (nested) — the folder you drop into
     `addons` must contain `config.yaml` **directly**.

## 2. Install the add-on

1. In Home Assistant, click **Settings** (the ⚙️ gear, bottom of the left sidebar).
2. Click **Add-ons** (puzzle-piece icon).
3. Bottom-right, click the blue **ADD-ON STORE** button.
4. Top-right **⋮ (three dots) → Check for updates**, then refresh the browser page.
5. Scroll to the top — a **"Local add-ons"** section shows **"PodConnect (test slice)"**.
   Click it → **INSTALL**.

> ⏳ The first install **compiles OwnTone** on the Green — **~15–40 minutes**, one time only.
> (Once the prebuilt image is published, this becomes a ~2-minute pull instead.)

**Don't see "Local add-ons" / "PodConnect"?**
- Re-check step 1.4: `addons/podconnect/config.yaml` must exist with that exact nesting.
- Redo **⋮ → Check for updates** and hard-refresh the browser.
- No **Add-ons** entry under Settings? Enable **Advanced Mode** in your HA user profile
  (click your name, bottom-left → toggle *Advanced Mode*).

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
