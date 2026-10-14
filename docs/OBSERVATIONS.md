# Account-owned playback and queue observations

These additive Control services are intended for server consumers such as PodVoice.
They use Home Assistant service authentication and the exact loaded PodConnect
`config_entry_id`. They do not accept a viewer, room name or default account as
an account selector. Browser clients should continue to use the PodVoice owner.
Neither service plays, transfers or changes music. OAuth credentials stay inside
Control's existing account client. Installation and physical playback acceptance
remain separate from source and fixture verification.

Call either service with `return_response` (for REST, append
`?return_response` to `/api/services/podconnect/<service>`). Responses below are
the `service_response` payload. Unknown, unloaded or replaced accounts raise a
sanitized HA service error. Provider failures use `ok: false` and `error_kind`;
raw bodies, exception text and credentials are not returned.

## `get_playback_snapshot`

Request: `{"config_entry_id":"account-entry-id"}`. No provider request is made.
The response projects the last successful existing coordinator poll, never
optimistic `media_player` state. A failed poll returns `unavailable`; an
observation older than 15 seconds returns `stale`. It does not refresh on demand.

```json
{
  "ok": true,
  "schema": "podconnect.playback.v1",
  "config_entry_id": "account-entry-id",
  "owner_epoch": "opaque-owner-incarnation",
  "poll_sequence": 42,
  "observation": {
    "status": "fresh",
    "observed_at": "2026-10-14T12:00:00+00:00",
    "age_ms": 1000,
    "max_age_ms": 15000,
    "source": "spotify_web_api"
  },
  "playback": {
    "state": "playing",
    "target": {"kind":"spotify_device","target_id":"provider-id","name":"Kitchen"},
    "item": {"type":"track","uri":"spotify:track:example","name":"Song","artists":["Artist"],"duration_ms":123000,"artwork":{"kind":"album_cover","provider":"spotify","url":"https://i.scdn.co/image/ab67616d0000b2730123456789abcdef"}},
    "progress_ms": 1000,
    "provider_changed_at_ms": 1791979200000,
    "shuffle": false,
    "repeat": "off"
  },
  "physical_result_verified": false
}
```

`state` is `playing`, `paused` or `no_active_playback`. With no active playback,
`playback` contains only `state`, null `target` and null `item`. With an active
response, missing device ID produces null `target`; item, progress, provider
change time, shuffle and repeat can be null. Repeat is `off`, `track` or `context`.
Track and episode items have the same closed base shape; episode `artists`
contains the show name when provided. A track may include optional `artwork`
when Spotify returned a valid album image: `{kind:"album_cover",provider:"spotify",url:"https://i.scdn.co/image/<id>"}`.
The URL is limited to the exact HTTPS Spotify image host and path; query strings,
fragments, credentials, other hosts and malformed values are omitted. Episode art
is not inferred. Unsupported or malformed items return `malformed_response`
instead of optimistic data.

`owner_epoch` changes whenever the account coordinator is recreated. A successful
poll's sequence can skip numbers after failed attempts; compare sequences only
within the same account and owner epoch. The timestamp records receipt of playback,
before the device-list request. This is Spotify-observed state, not audible proof,
and a Spotify target ID is not a permanent room identity or action permission.

## `get_queue`

Request: `{"config_entry_id":"account-entry-id","limit":20}`. Limit defaults to
20 and must be an integer from 1 through 20 (booleans are rejected).

```json
{
  "ok": true,
  "schema": "podconnect.queue.v1",
  "config_entry_id": "account-entry-id",
  "owner_epoch": "opaque-owner-incarnation",
  "snapshot_id": "opaque-owner-incarnation:queue:1",
  "observed_at": "2026-10-14T12:00:00+00:00",
  "scope": "account",
  "coverage": "provider_response",
  "relation_to_playback": "not_atomic",
  "currently_playing": null,
  "items": [
    {"option_id":"opaque-owner-incarnation:queue:1:0","type":"track","uri":"spotify:track:example","name":"Song","artists":["Artist"],"duration_ms":123000,"artwork":{"kind":"album_cover","provider":"spotify","url":"https://i.scdn.co/image/ab67616d0000b2730123456789abcdef"}}
  ],
  "returned_count": 1,
  "truncated": false
}
```

Queue is account-wide and independently observed. It is not tied to a requested
speaker and is not atomic with the playback snapshot. Provider ordering and
repeated URIs are preserved; `option_id` identifies an occurrence only within
that queue snapshot. A cached response retains those IDs. A new fetch creates a
new snapshot. These IDs grant no authority and there is no queue mutation service.
`currently_playing` is a nullable projected item. An empty queue is a successful
observation, not an error. `truncated` indicates omitted rows; the response makes
no claim to represent an entire playlist or future autoplay choices.

The existing OAuth owner performs one GET `/me/player/queue`. Existing playback
read scopes suffice; no new credentials or scopes are requested. The call has a
6-second total deadline, a 256 KiB response-body limit, and a 64 KiB public reply
limit. Only the first 20 rows are projected. One request per account can be in
flight; competing calls receive `busy`. Successful and failed replies are cached
for 10 seconds. A cancelled call retains its admission cooldown but leaves no
background task. HTTP 429 uses the provider's integer `Retry-After`, bounded to
10–3600 seconds, or 60 seconds for a missing/invalid header. That negative reply
is cached for the delay. Account ownership is checked again after awaits and
before publishing any cache result; unload/replacement cannot publish a late read.

Queue `error_kind` values are `busy`, `unavailable`, `authentication_required`,
`forbidden`, `rate_limited` and `malformed_response`. Cached observations keep their
original `observed_at`; consumers must not replace it with retrieval time.

Provider contracts:
[playback state](https://developer.spotify.com/documentation/web-api/reference/get-information-about-the-users-current-playback),
[queue](https://developer.spotify.com/documentation/web-api/reference/get-queue).

## Library reads: `top_tracks`, `recently_played`, `liked`

These response-only services accept an optional `config_entry_id`. Supplying it
binds the read to that exact loaded PodConnect account. For backwards
compatibility, omission is accepted when exactly one active PodConnect account
exists; with zero or multiple active accounts it fails and asks the caller to
select one. The owner identity is checked again after the Spotify request so an
unloaded or replaced account cannot publish a late result. The response echoes
the actual selected `config_entry_id`. Returned tracks have the closed
`{name,artist,uri}` projection and may add the same validated optional
`artwork` object used in playback and queue. No provider image metadata or
credential fields are forwarded.

## `search_tracks`

`search_tracks` is a separate read-only Spotify catalog query. It requires an
exact `config_entry_id`, a non-empty trimmed query of at most 160 characters,
and an exact integer `limit` from 1 through 20 (default 3; booleans and numeric
strings are rejected). It calls the existing Spotify Web API search with the
`track` type only; it does not use media-player search/play helpers. Its closed
response echoes the selected account and normalized query, identifies
`source: "spotify_web_api"` and `coverage: "spotify_search_sample"`, and includes
`source_sample_count`, `returned_count`, `source_has_more`, and at most `limit`
tracks. A track contains only `name`, `artist`, `uri`, and optional validated
Spotify artwork. The response is limited to 64 KiB. These are search candidates,
not a complete catalog, recommendation, or permission to play.

## Verification and release boundary

`tests/ha_framework/test_observations.py` exercises actual HA service schemas,
HTTP response transport, OAuth client and account lifecycle against inert loopback
fixtures. `tests/test_control_poll_truth.py` preserves command/poll semantics.
No fixture contacts Spotify, a real HA installation or a speaker.

Before release, obtain independent source review and the repository's official
`Verify HA service and client boundaries` CI (Python 3.12 boundary suite and
pinned HA 2026.8.2 on amd64). Local arm64 fixture success is a separate receipt.
Follow `docs/releasing.md`: bump Control's manifest and changelog, then publish
the matching HACS release after approval. Speakers has no version change here.
