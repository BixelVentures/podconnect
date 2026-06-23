# PodConnect Speakers — install on Home Assistant

This add-on makes your Apple **HomePods** appear as **Spotify Connect speakers**, running on your
Home Assistant Green. It's **multi-room** — add several HomePods from the sidebar panel, each its own
independent Connect speaker.

**You need:** Spotify **Premium**, and the HomePod(s) on the same network as the Green.

## 1. Add the repository

1. In Home Assistant: **Settings → Apps → Add-on Store**.
2. Top-right **⋮ → Repositories**.
3. Paste **`https://github.com/BixelVentures/podconnect`** → **Add** → close.

## 2. Install (downloads a prebuilt image — ~2 min, no compiling)

1. Refresh the store → open **PodConnect Speakers** → **INSTALL**.
2. It **downloads a ready-made image** for your Green — no on-device build.

## 3. Set up your HomePods in the panel (no typing — this is the main way)

After starting the add-on, a **PodConnect Speakers** item appears in the Home Assistant **sidebar**.
**The panel owns all speaker setup** — you normally never touch the Configuration tab.

Open the panel to see a **live network scan** of every AirPlay device found (the same scan Spotify
Connect uses). From here you:

- The **top card is your primary speaker** — subtly highlighted with a `main` pill. It **auto-names
  itself** after its HomePod (e.g. "Køkkenalrum HomePod").
- **+ Add speaker** → pick another HomePod → it becomes its own Connect speaker, live, **no restart**.
- Per room: **🔊 Test** (a soft tone — proves the AirPlay path, no Spotify needed), **⏹ Stop** (pauses
  whatever is playing, *regardless of which account*, without giving the HomePod away), **✎ Rename**
  (pins a custom name the auto-sync won't overwrite), **Remove**, and **⚙ Settings** (per-room
  **grace** + **bitrate**; empty = inherit the global default).
- The **primary's ⚙ Settings** also holds a **"Plays to HomePod"** picker (switch which HomePod it
  plays to — applies on pick) and **⏏ Release** (frees the HomePod so another AirPlay app — Mofibo,
  Apple Music… — can use it; press play in Spotify to take it back).
- A **now-playing / released / idle** status pill per room.

**Self-healing naming:** a room is bound to its HomePod by a stable id, so **renaming the HomePod in
Apple Home syncs everywhere automatically** (Connect device + HA entity) — no re-pick needed (unless
you pinned a custom name with ✎ Rename).

Your choices persist across restarts (`rooms.json`). The panel is also reachable directly at
`http://<your-HA-IP>:8099`.

> **Not seeing a speaker in Home Assistant (PodConnect Control / `media_player`)?** A Spotify Connect
> device only appears in the Web API — and therefore in HA — **after your Spotify account has played to
> it once**. Open the Spotify app (same account as Control), pick the speaker under *Spotify Connect*
> (not its AirPlay entry) and press play for a moment; it then shows in HA and persists across restarts.

> **Stopping playback regardless of which account is playing.** The panel's per-speaker **⏹ Stop** (and
> Siri) pause a HomePod no matter whose Spotify owns the session — this is the **account-agnostic** stop.
> The HA `media_player` (Control) entity's pause only stops *your own* session (Spotify Web API). To get
> the account-agnostic stop from a HA automation or voice, point a `rest_command` at the add-on
> (`room` ids come from `GET /api/rooms`; `/api/stop` needs no token):
>
> ```yaml
> rest_command:
>   podconnect_stop_kitchen:
>     url: "http://<your-HA-IP>:8099/api/stop?room=r0"
>     method: POST
> ```

## 4. (Advanced) Configuration tab options

You normally don't need these — the panel covers everything. The tab keeps a few global defaults/
fallbacks:

| Option | What it does |
|---|---|
| `bitrate` | **Default** bitrate (`320`); a per-room ⚙ Settings value overrides it. |
| `grace_minutes` | **Default** hold time before freeing an idle HomePod (`3`; `0` = free as soon as idle); per-room ⚙ Settings overrides it. |
| `network_interface` | *(advanced)* leave blank to auto-detect your LAN interface. |
| `attention_token` | *(advanced)* optional shared secret guarding the `/api/attention` **duck API** (for a voice-assistant gatekeeper). When set, those requests must send `X-PodConnect-Token`. Blank = open on the LAN. See [`docs/ATTENTION-API.md`](../docs/ATTENTION-API.md). |

## 5. Test it

1. Open the **Log** tab and watch for `Selected HomePod '…'` (it locked onto the HomePod you picked).
   - `… needs AirPlay verification`? In the Apple **Home** app, set this HomePod's
     **"Allow Speaker & Display Access"** to **"Anyone on the Same Network"**.
   - Nothing found? The HomePod may be on a different network/VLAN than the Green.
2. Open **Spotify** (Premium) → **Connect to a device** → choose the speaker (named after its
   HomePod) → **Play**.
3. 🎉 Audio plays on the HomePod; the Spotify volume slider changes the volume.

## How it works

For **each room**, the manager forks & supervises its own pair:
Spotify Connect → go-librespot → pipe → OwnTone → AirPlay 2 → HomePod — all inside this add-on, with
the HomePod bound by a stable id. It uses standard **Spotify Connect** (no Spotify developer keys
needed) and does **not** use the Apple TV integration. Credentials + room config are saved, so
speakers stay paired across restarts.

## Troubleshooting

- **Install can't pull the image** → the GHCR package must be **public**:
  github.com/BixelVentures → **Packages** → `aarch64-podconnect` → **Package settings** →
  **Change visibility → Public**.
- **HomePod not in the panel's scan** → on a different network/VLAN than the Green, or AirPlay access
  isn't "Anyone on the Same Network" (Apple Home app).
- **Speaker missing in Spotify** → add-on started? Spotify Premium? Same network? Check the Log.
- **Selected but no sound** → check the Log for `go-librespot` / `owntone` errors and share them.
