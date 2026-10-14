# Direct Spotify playback controls — isolated candidate, 14 October 2026

Lead decision: add five bounded, response-only account commands through the existing
SpotifyApi owner. Exact direct `spotify_device` IDs only; configured aliases,
observed outputs, implicit active devices, transfer and arbitrary MCP routing are
excluded. The caller owns administrator room/action/volume scope; these HA services
require Home Assistant authentication and do not interpret viewer identity as authority.

Before any effect, bind the exact active account runtime, owner incarnation, fresh
successful poll and observed target; revalidate after OAuth refresh immediately
before the existing client sends. Reserve a bounded per-incarnation request receipt
before sending. Unknown outcomes are never retried; old epoch requests fail after
reload. Accepted provider responses remain accepted regardless of later polling.
Source observations are separate from optimistic entities and physical playback.

Continuation decision: replace the initial lifetime 128-command admission cap with
a server-owned monotonic command sequence. Retain only 128 recent receipts while a
permanent per-incarnation high-water mark rejects replay after eviction. Browser
request UUIDs remain in PodVoice; they are never accepted as owner command identity.

## Response-only services

Both services require `return_response`; unknown top-level fields are rejected.
Account and target IDs are nonempty strings of at most 1,024 characters. No service
chooses an account or target on the caller's behalf.

`podconnect.get_playback_controls` accepts:

```json
{"config_entry_id":"account-entry","kind":"spotify_device","target_id":"device-id"}
```

A successful response is a closed projection:

```json
{
  "ok":true,"schema":"podconnect.controls.v1",
  "config_entry_id":"account-entry","owner_epoch":"owner-incarnation",
  "poll_sequence":42,"next_command_sequence":1,
  "observation":{"status":"fresh","observed_at":"2026-10-14T12:00:00+00:00","age_ms":100,"max_age_ms":15000,"source":"spotify_web_api"},
  "target":{"kind":"spotify_device","target_id":"device-id","name":"Kitchen"},
  "actions":["resume","next","previous","volume"],"volume_percent":30,
  "status":"ready","physical_result_verified":false
}
```

The existing observation reader supplies source age and successful poll provenance.
This read never polls Spotify. The requested device must be the current observed
playback target and a unique unrestricted catalog device. Playback must explicitly
report `is_restricted: false` and an `actions.disallows` object containing booleans.
Pause/resume depend on observed playback state, and provider disallow flags remove
transport actions. Volume requires explicit `supports_volume: true` and a valid
observed integer percent. Missing volume yields `null` and no volume action.

`status` is `ready`, `awaiting_observation` or `sequence_exhausted`; the latter two
have empty `actions`. Closed admission errors return
`{"ok":false,"error_kind":"...","retry_allowed":false}`. Missing/unloaded
accounts produce the existing sanitized HA service error. Capability availability
is a current observation, not a permission grant or assurance of provider acceptance.

`podconnect.control_playback` accepts:

```json
{
  "config_entry_id":"account-entry","kind":"spotify_device","target_id":"device-id",
  "owner_epoch":"owner-incarnation","expected_poll_sequence":42,
  "request_id":"owner-incarnation:1","command_sequence":1,"action":"pause","args":{}
}
```

`action` is exactly `pause`, `resume`, `next`, `previous` or `volume`. Only volume
accepts arguments: exactly `{"percent":30}`, integer 0–100, excluding booleans.
All other actions require `{}`. Epoch is a nonempty string of at most 128 characters.
Both poll and command sequences are integers from 1 through `2**53 - 1`. Request ID
is exactly `<owner_epoch>:<command_sequence>` (maximum 145 characters), using the
next command sequence supplied by the owner. At safe-integer exhaustion, the read
returns `next_command_sequence: null`, no actions and `sequence_exhausted`.
The caller must derive account, target and action/volume limits from its own current
administrator scope and bind its independent touch task/revision before forwarding.
PodVoice must map its browser UUID to one canonical owner request and retain that
exact request for retries of the response lookup; browsers cannot choose sequences.

Terminal response example:

```json
{
  "ok":true,"schema":"podconnect.command.v1","config_entry_id":"account-entry",
  "owner_epoch":"owner-incarnation","request_id":"owner-incarnation:1",
  "command_sequence":1,
  "action":"pause","target":{"kind":"spotify_device","target_id":"device-id"},
  "outcome":"accepted","provider_request_accepted":true,
  "retry_allowed":false,"physical_result_verified":false,
  "observe_after_poll_sequence":42
}
```

Outcomes are `accepted`, `rejected` and `unknown`. Failures carry a closed
`error_kind`, with `ok` and `provider_request_accepted` false. Admission rejections
and an in-flight duplicate may omit `observe_after_poll_sequence`. Provider HTTP
204 is the only acceptance; redirects are disabled for guarded commands. No raw
provider error, credentials or tokens are returned or stored in request receipts.
`accepted` proves provider acceptance only, never physical or audible playback.

## Ownership, uncertainty and bounds

The exact active runtime, command owner, coordinator, observation owner, OAuth
session, API and underlying HTTP client must remain unchanged and the HTTP client
must remain open. Admission checks run before OAuth and again immediately before
the existing API request. The exact epoch, successful poll sequence, current target,
source freshness and provider capability must still match. Existing callers that
omit guard hooks retain their previous API behavior.

There is one in-flight command per account incarnation and a six-second deadline.
The exact next command sequence advances and its receipt is reserved synchronously
before the first await. Repeating a retained sequence with the same complete
request returns a copy of the receipt; a changed request rejects as `request_reused`.
An older evicted sequence rejects as `stale_command`; future gaps reject as
`command_sequence_gap`. Altered request IDs reject as `invalid_request_identity`.
No duplicate can resend. Cancellation or failure after a possible
send retains `unknown`; before that boundary it is `rejected`. No background
command task, retry, automatic transfer, optimistic update or post-command poll is
created. The existing HA service lifecycle can outlive a disconnected HTTP caller,
but the owner deadline still terminates the command and retains its receipt.

After any possible send, another command requires a successful poll whose sequence
is strictly greater than the coordinator's allocated poll sequence at command
completion. This excludes a poll already in flight before completion. The controls
read advertises no actions until this causal observation arrives. Poll failures do
not overwrite accepted receipts. Reload creates a new owner epoch and rejects old
epoch commands before effects.

At most 128 recent receipts are retained per owner incarnation, including rejected
and unknown reserved requests. Oldest receipts may be evicted because the monotonic
high-water mark permanently fences their command sequences. Even an evicted request
with changed action, fresh observation or a different ID cannot replay that sequence.
An evicted unknown receipt loses its detailed response, not its replay protection.
There is no daily-use count limit: the real framework regression accepts 132 commands
while retaining only 128 receipts. Constructing a replacement command owner on the
same integration runtime is rejected. A fresh integration runtime has a fresh
coordinator epoch; old-epoch requests reject before consuming its first sequence.

Pairing, browser CSRF, room authorization and persistent browser-request deduplication
remain the calling owner's responsibilities. A fresh canonical sequence after an
unknown outcome must not be treated as an automatic retry of the unknown action.

## Local evidence and remaining gates

Base: released Control 0.10.8, commit
`37e6cc8dd336dc20b0d6b4cf8c3453dafd4406cf`. Proposed Control version:0.10.9. Speakers version remains unchanged.

Local real Home Assistant framework proof used the installed arm64
`ghcr.io/home-assistant/home-assistant:2026.8.2` image, digest
`sha256:56690a89c79a0de98035e1719f8324a92d5859c1192ff45adb0230ea81cb42a5`.
The container ran with `--network none`, a read-only repository/root filesystem,
dropped capabilities and bounded resources. Provider effects were an inert
loopback HTTP fixture; there were no real HA, Spotify or speaker calls.

- Final monotonic command contract: `python -B -m unittest test_commands -v`:
  all 16 real-framework command tests passed in 1.542 seconds. Receipt:
  `/private/tmp/podconnect-controls-sequence.log`.
- Before the monotonic-identity continuation, 13 command plus 8 existing observation
  cases passed together in 7.582 seconds. Observation source remains unchanged.
- Existing `python -m unittest discover -s tests -p 'test_control*.py' -v`:
  14 tests passed in 0.053 seconds.
- Ruff passes for the new command module and framework tests; changed Python
  sources parse and the Git diff has no whitespace errors.

Tests exercise actual HA service registration/schema, authenticated REST response
contracts, the existing OAuth/API client and inert HTTP boundaries. Coverage
includes exact five paths/device arguments; stale/cross-account/replaced owner;
source/target/restriction/capability failures; OAuth-await revocation; in-flight poll
causality; cancellation/timeout; duplicate and mutated IDs; receipt immutability;
provider-error redaction; reload namespace; 132 accepted commands with bounded
receipts; replay after accepted/unknown receipt eviction; sequence gaps and canonical
identity; and accepted receipt preservation.

This is local arm64 source/test evidence. Independent exact-diff review and the
official pinned amd64 HA CI remain required before the coordinated integration-only
release or installation. No publication, installation or physical result
is claimed here.

Independent source review, 14 October 2026: reviewer
`/root/podvoice_bridge` returned GO for this source slice. The reviewer checked the
final monotonic command-sequence delta: high-water reservation precedes the first
await; receipt eviction cannot reopen an old sequence; canonical epoch/sequence
identity prevents relabeling; exact current-owner and provider guards still run
before the network send. The reviewer read and attributed the 16-test local
framework receipt above; they did not independently rerun those tests. This verdict
covers the isolated source slice, not official CI, release, installation, paired
PodVoice integration or physical playback.
