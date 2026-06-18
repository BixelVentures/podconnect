# PodConnect — Vertical Test Slice (start here)

This is a tiny test to **prove the idea works**: play Spotify and have it come out of
**one HomePod**, using one `go-librespot` + one `OwnTone` container.

If this works, the full product (the "add a speaker, pick a HomePod" app) is viable.

---

## ⚠️ Read this first — where to run it

Run this on a **Linux** computer that is on the **same Wi-Fi/network as your HomePod**:

- a spare PC or laptop running Linux, **or**
- a Raspberry Pi, **or**
- your Home Assistant Green (advanced).

**It will NOT work on a Mac or Windows PC.** Spotify and AirPlay need a special
"host networking" mode that Mac/Windows Docker can't provide. (That's a limitation of
Docker on those systems, not of PodConnect.)

You also need:
- **Docker** installed (`docker --version` should work).
- **Spotify Premium** (Spotify Connect requires it).

---

## Steps

### 1. Copy this `dev/` folder onto the Linux machine
Put it anywhere, e.g. `~/podconnect-test`. Open a terminal **inside the folder**
(the one containing `docker-compose.yaml`).

### 2. (Optional) change the speaker name
The speaker shows up in Spotify as **"PodConnect Test"**. To rename it, edit
`go-librespot/config.yml` and change the `device_name` line. Skip this if you don't care.

### 3. Start it
```bash
docker compose up -d
```
First run downloads the images (a few minutes). Wait ~30 seconds after it finishes.

Check it's alive:
```bash
docker compose ps          # both containers should be "running"
docker compose logs -f     # watch the logs (Ctrl-C to stop watching)
```

### 4. Tell OwnTone which HomePod to use (one time)
Open a browser to **`http://<LINUX-MACHINE-IP>:3689`** (e.g. `http://192.168.1.50:3689`).
This is the OwnTone web page. In the **Outputs / Speakers** menu (the speaker icon),
**tick your HomePod** so it's selected. Leave this page open for now.

> Don't know the machine's IP? Run `hostname -I` on it and use the first address.

### 5. Play from Spotify
- Open the **Spotify app** on your phone or computer (same network).
- Tap **"Connect to a device"** and pick **"PodConnect Test"**.
- Press **Play** on any song.

🎉 **Audio should now come out of your HomePod.**

### 6. Quick checks
- **Volume:** move the volume slider in Spotify — the HomePod should get louder/quieter.
- **It stays around:** close Spotify, wait ~15 minutes, reopen — "PodConnect Test"
  should still be in the device list (no re-pairing).

---

## If something doesn't work

- **"PodConnect Test" never shows up in Spotify** → you're probably not on a Linux host
  with host networking, or the machine isn't on the same network as your phone. Re-check
  the "where to run it" box above. Also `docker compose logs go-librespot`.
- **It shows up, but no sound on the HomePod** → make sure you ticked the HomePod in the
  OwnTone web page (step 4). Check `docker compose logs owntone`.
- **The HomePod isn't listed in OwnTone** → the Linux machine may not see it via the
  network (different Wi-Fi/VLAN). HomePod and the machine must be on the same network.

## Stop / clean up
```bash
docker compose down          # stop
docker compose down -v       # stop and also wipe the OwnTone database volume
```

---

## What this proves (and what it isn't yet)

This slice is **one room, set up by hand**. The real PodConnect product wraps all of this
in a Home Assistant add-on with a UI where you click **"Add speaker"**, pick a HomePod from
a **dropdown**, and it does everything above automatically — for as many HomePods as you
want, each its own Spotify Connect device, with full volume sync and Home Assistant voice
control. See [`../docs/PLAN.md`](../docs/PLAN.md).
