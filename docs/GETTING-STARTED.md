# PodConnect — getting started

PodConnect is **two parts**, installed once:

1. **PodConnect Speakers** — a Home Assistant **add-on** (the audio engine + the speaker panel).
2. **PodConnect Control** — a **HACS integration** (Spotify control + media_player entities).

You need: **Spotify Premium**, your HomePod(s) on the **same network** as HA, and (for Control) your
own **Spotify developer app**.

---

## 1. Install the Speakers add-on
1. **Settings → Add-ons → Add-on Store → ⋮ → Repositories** → add
   `https://github.com/BixelVentures/podconnect` → **Add**.
2. Open **PodConnect Speakers** → **Install** (pulls a prebuilt image) → **Start**.
3. A **PodConnect Speakers** item appears in the sidebar.

## 2. Add your HomePods (the panel — no typing)
Open the **PodConnect Speakers** panel:
- The **primary speaker**: pick its HomePod from the live list → **Save**. It auto-names itself after
  the HomePod (e.g. "Køkkenalrum HomePod").
- **More rooms:** **+ Add speaker** → pick another HomePod → it becomes its own Spotify Connect
  speaker, named after that HomePod. No restart.
- Per room you get **Test** (a tone to prove audio), **Stop**, **Remove**, **✎ Rename**, and
  **⚙ Settings** (per-room grace + bitrate; empty = inherit the global default).
- **⏏ Release HomePod** frees a HomePod for other AirPlay apps (Mofibo, Apple Music); **⏹ Stop music**
  stops whatever's playing regardless of account.

## 3. Install Control (Spotify) via HACS
1. **HACS → ⋮ → Custom repositories** → add the repo (type **Integration**) → install
   **PodConnect Control** → restart HA.
2. **Settings → Devices & Services → Add Integration → PodConnect Control** → enter your Spotify
   **Client ID/Secret** (your developer app) and sign in.
3. You get a `media_player` per Spotify Connect device, plus **search / browse / shuffle / repeat**.

## 4. Use it
- **Spotify app:** open the Connect picker → choose your speaker → play. Volume stays in sync.
- **Home Assistant:** control the media_player; **Assist** ("spil X i køkkenet") searches + plays
  (see [`AREAS-AND-ASSIST.md`](AREAS-AND-ASSIST.md) to expose entities + assign Areas, and
  [`gemini-system-prompt.md`](gemini-system-prompt.md) for the assistant prompt).

## Multi-room — what to expect
Two modes:
- **Device-aliases (recommended, one account):** set `experiment_aliases: true` in the add-on's
  Configuration tab + restart. Every room then shows up as its **own selectable device in the Spotify
  Connect menu on your single account** — pick a room and the audio follows there. One stream, clean
  audio. See [`../podconnect/DOCS.md`](../podconnect/DOCS.md) §6 and [`ALIASES-PROBE.md`](ALIASES-PROBE.md).
- **Per-room (default):** each HomePod is its own independent Connect speaker; one account plays to one
  room at a time. **Different music in different rooms at the same time** needs one Spotify account per
  room — see [`MULTI-ACCOUNT.md`](MULTI-ACCOUNT.md).

Synchronized *same* music across rooms (groups) is a separate, not-yet-built feature.

## Updating
- **Speakers:** the Add-on Store shows **Update** when a new version ships. No reconfig.
- **Control:** HACS shows **Update** on each release.

## More
Install detail: [`../podconnect/DOCS.md`](../podconnect/DOCS.md) · Test checklist:
[`TEST-CHECKLIST.md`](TEST-CHECKLIST.md) · Versions: [`../CHANGELOG.md`](../CHANGELOG.md).
