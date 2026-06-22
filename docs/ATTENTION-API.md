# PodConnect — Attention (duck) API

The **Attention API** lets an external agent — typically a voice-assistant gatekeeper — temporarily
**duck** a room's music to a quiet level while it talks, then release it. It is the *only* contract
surface a separate voice project needs: PodConnect owns the audio and the volume; the agent just asks
it to get quiet for a moment.

> Shipped in **Speakers 0.14.0**. Lives in the add-on's per-room volume bridge (the manager binary),
> alongside the bidirectional HomePod ⇄ Spotify volume relay.

## Why it exists (design)

The add-on already runs a **bidirectional volume reconcile** every ~200 ms (HomePod buttons ⇄
Spotify/HA slider). A naive "set volume to 5%" would be fought by that loop within a tick. The
Attention API is a first-class *override* the reconcile respects:

- **Absolute + idempotent.** You set a target level, not a delta. Re-sending is safe.
- **Owned + deadline-bound.** A duck has an `owner` and an auto-release `deadline`.
- **Heartbeat to hold.** Re-POST the same room to extend the deadline. Stop heartbeating and the duck
  **auto-releases** — a crashed/disconnected agent can never leave the music stuck quiet.
- **The duck wins.** While active the reconcile is suspended: the HomePod is held at the target and a
  HomePod button or the Spotify mirror can't undo it. **Transport keeps running** — the music plays
  quietly underneath, so a conversation can pause and resume without the room going silent.
- **Clean restore.** On release/expiry the pre-duck level (captured at first engage) is restored and
  the reconcile re-seeds on the live truth.

## Endpoints

Base URL: the add-on manager, `http://<ha-host>:8099` (also reachable on the LAN, not only via
Ingress). All endpoints are JSON.

### `POST /api/attention` — engage or heartbeat

```jsonc
{
  "room":   "r0",        // required — room id (see GET /api/rooms)
  "level":  5,           // target HomePod volume % while ducked (0..100)
  "owner":  "voice",     // optional — who holds the duck (informational); default "external"
  "ttl_ms": 2000,        // optional — auto-release window; default 2000, clamped to a 15000 max. Re-POST before it elapses to hold.
  "fade_ms": 0           // optional — RESERVED (v1 is instant); accepted, ignored
}
```

Engages the duck (or, if already active, extends the deadline — a **heartbeat**). The pre-duck level
is captured on the **first** engage only, so a stream of heartbeats can't overwrite it.

Returns the room's current state (see snapshot below). `404` if the room id is unknown; `503` if the
room exists but isn't currently supervised.

### `POST /api/attention/release` — release now

```jsonc
{ "room": "r0" }
```

Ends the duck immediately; the bridge restores the pre-duck level on its next tick. Always `{"ok":true}`.

### `GET /api/attention` — state

```jsonc
{
  "rooms": {
    "r0": { "active": true,  "level": 5, "owner": "voice", "remaining_ms": 1500 },
    "r1": { "active": false, "level": 0, "owner": "",      "remaining_ms": 0 }
  }
}
```

## Auth

If the **`attention_token`** add-on option is set, every `/api/attention*` request must carry it:

```
X-PodConnect-Token: <token>
```

Empty (the default) leaves the API open — fine on a trusted home LAN. This is the one endpoint built
for *external* control, hence the opt-in secret.

## Typical voice flow

| Conversation state | Call                                                              |
|--------------------|-------------------------------------------------------------------|
| LISTENING          | `POST /api/attention {room, level:5, owner:"voice", ttl_ms:2000}` |
| AI_SPEAKING        | keep heartbeating every ~500 ms (same POST)                       |
| LOUNGE_WINDOW      | `POST /api/attention {room, level:35, ttl_ms:8000}`               |
| IDLE / barge-in    | `POST /api/attention/release {room}`                              |
| agent crash / hang | *(stop heartbeating — auto-releases at the deadline)*             |

The agent never has to guarantee a release call: the deadline is the safety net. A heartbeat cadence
comfortably under `ttl_ms` (e.g. 500 ms heartbeat vs 2000 ms TTL) keeps the duck solid while making a
hang self-heal within ~2 s.

> **`ttl_ms` is clamped to a 15 s ceiling.** The whole watchdog rests on "hold only as long as you
> heartbeat" — without a cap, a single large-TTL request followed by a crash would pin the music quiet
> for that whole window. Keep TTLs short (2 s listening, ~8 s lounge) and rely on the heartbeat.

## Notes / limits (v1)

- **Instant, no fade.** The duck-down and the restore are immediate. `fade_ms` is reserved for a later
  smooth-fade pass (the bridge would step the level over the loop ticks).
- **A duck holds against manual changes** until release/expiry (the duck wins). Letting a large manual
  volume move cancel an active duck is a deliberate v2 candidate, not v1 behavior.
- **Never-loud survives a duck.** If a *fresh* Spotify session starts while a room is ducked (e.g.
  another account grabs the HomePod mid-conversation), it's still capped on release — it can't escape
  to a remembered 100%.
- **Per room.** Target one room per call. (A future "all rooms" selector could be added.)
- **Transport is untouched.** Attention only moves volume; the music keeps playing.
