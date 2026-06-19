# Rooms / Areas + Voice (HA Assist) — setup from both sides

Goal: **"add a speaker to a room/area"** so you can say *"spil noget i køkkenet"* / *"pause i
køkkenet"* and HA targets the right HomePod. This is covered from **both** sides of PodConnect —
the Speakers add-on names the speaker, and the Control integration exposes it to HA + Assist.

> TL;DR: pick the HomePod → it auto-names itself after the room → assign that `media_player` to an
> HA **Area** → expose it to **Assist**. Then built-in voice intents (play/pause/next/volume) and
> search-and-play ("spil X") just work.

---

## 1. Speakers side — name the speaker after its room

PodConnect Speakers **0.7.0** auto-names the Connect speaker after the HomePod you pick
(HomePod-name forwarding):

1. Open the **PodConnect Speakers** panel in the HA sidebar.
2. Leave the `speaker_name` option **empty** (Settings → Add-ons → PodConnect Speakers →
   Configuration). Empty = auto-name mode.
3. **Pick your HomePod** in the panel. The Connect device + the HA entity now take the HomePod's
   name (e.g. *"Køkkenalrum"*).
   - ⚠️ Picking briefly restarts go-librespot, so the Connect device disappears/reappears in the
     Spotify app once. The device **id is stable** (persisted independently of the name), so this
     renames in place — no duplicate "ghost" device.
   - Prefer a custom name? Set `speaker_name` instead; it wins over auto-naming.

This is the only part that needs the add-on: it makes the entity *self-describe* its room, which is
what makes the next step (Area + `suggested_area`) sensible.

## 2. Control side — assign the entity to an Area

The **PodConnect Control** integration exposes one `media_player` per Connect device.

1. Settings → **Devices & Services** → **PodConnect** → open the device/entity for your speaker.
2. Assign it to an **Area** (e.g. *Køkken*). One click.
   - Areas are an HA-native, **user-assigned** concept — there is no integration-code API to force
     an area or set Assist aliases. PodConnect can only *suggest* (`suggested_area`) once the entity
     is room-named (step 1); you confirm it here.

## 3. Expose to Assist (+ optional aliases)

1. Settings → **Voice assistants** → your assistant (e.g. Gemini) → **Expose** → add the speaker's
   `media_player`.
2. (Optional) Add **aliases** on the entity ("køkkenhøjttaler", "højttaleren i køkkenet") so more
   phrasings resolve. Aliases are user data in HA's registry — set here, not in code.

## 4. What voice can do now

With the entity exposed and in an Area, these work via Assist:

| You say | What happens |
|---|---|
| *"spil Coldplay i køkkenet"* | Search (`SEARCH_MEDIA`) → play top hit on the kitchen speaker |
| *"pause i køkkenet"* / *"næste"* | Built-in media intents on that `media_player` |
| *"skru op/ned i køkkenet"* | Volume (synced to Spotify + the HomePod) |
| *"spil min Discover Weekly"* | Resolves via search / profile browse |

A voice satellite **in** the room lets you drop "i køkkenet" — the default "play X" uses the
satellite's area.

---

## Honest limitations (so expectations match reality)

- **Stopping *another account's* music by voice is not wired yet.** The built-in media intents
  control **your** Spotify session (via Control / the Web API), which can't touch a family member's
  playback. To stop whoever is playing, use the panel **"⏹ Stop"** button (account-agnostic, local).
  The planned best-practice fix is to expose each physical speaker as a **`media_player` via MQTT
  discovery** from the add-on, so *"stop the kitchen"* hits a local, account-agnostic pause — see
  `docs/TODO.md` (P1). Until then: voice = your session; panel = anyone's.
- **Area assignment + aliases are manual** (HA UI), by design. The add-on's job is to name the
  speaker well; HA owns rooms and exposure.
