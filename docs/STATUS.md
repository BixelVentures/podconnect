# Active lead decision · Speakers0.26.21 / Control0.10.14 final bundle

Lead root. Exact published base6f8ca53661d25b52bfdc4234ee2def36132466ea/tree6c432ec00f7f8b88b3b4cb1732a9d47e141efbe6; all120 public blobs matched before edits. Actual native20 enginefcd is current but absent from the Control13 account device list; existing invalid_target is intentional. Engine/room identity alone is no account association.

Integrate bounded opt-in native account-observation and Control read-only diagnostic: exact actual canonical identifier hash bound to engine/run/current connection and5-second expiry; compare only to existing exact Control account identifier without new provider/token/credential reads. Ordinary identity GET stays compatible with zero extra I/O. Unknown/mismatched identifiers never authorize a target, start, transfer, restart or credential replacement.

Implement native one-use queue occurrence tokens on the existing serialized player owner, including manual queue and duplicates, with closed manager/Control adapters and caller fences. Reject changed session/list/connection/ABA/consumed/unknown outcomes without URI/Next fallback or retry. Preserve exact-account ordinary Spotify guards and existing autoplay/native start-volume/alias/lease owners.

Runtime changes require causal tests and independent adversarial review plus coupled consumers. Build immutable21 architecture images from the final reviewed subtree before main; publish Control14 from its exact manifest. Sole Green writer holds one installation with PodVoice32 and verified backup126b6c8b; rollback never restores spent quota. Source/CI/provider evidence/physical acceptance remain separate. No new version is released or installed.


Actual final source composition: reviewed opt-in native/Control account diagnostic plus exact native queue occurrence endpoints/adapters/tests, Speakers21 and Control14 metadata. Native cancellation review found and repaired synchronous request-parent propagation before effect; cross-layer artwork projection and aged provider-source expiry were repaired before final freeze. Retained native autoplay/start-volume owners are unchanged. Independent native, manager and final Control metadata/expiry reviews have source GO on final queuepatch586b957d. Composed actual HA70/70 and boundary72/72 pass; full manager race/vet pass. The three-line legacy AST fixture was updated to observe the newly registered diagnostic service, without runtime changes. No source test, image or provider acknowledgement is physical acceptance.

---

# Active decision: released paused URI start — Speakers 0.26.20 candidate

Source implementation in progress, unpublished and uninstalled. Actual released
Speakers 0.26.19 can identify its supervised engine but rejects exact-room URI
start readiness after idle release when /status still reports a logged-in paused
session. Active derives from username/volume, not a selected native output. A
read-only intercepted-handler reproducer confirms this causal source defect; the
currently observed Spotify device is a MacBook, not evidence of Kitchen readiness.

Decision: admit only exact-current-alias Active + Paused + release marker as a
fresh output claim. Preserve ordinary selected paused same-session manual volume;
reject unselected alone, playing released routes, unknown owners and cross-room
active starts. Prime the exact configured start level before output selection,
recheck source intent/binding and readback, retain release marker through the
one-use native lease until existing observed-playing reclaim clears it. No new
provider endpoint, schema, room fallback or PodView-specific volume rule.

Change only native start barrier/tests and Speakers version to 0.26.20; retain
Control 0.10.13 playlist candidate below. Required evidence: causal positive and
negative/race/order/replay tests, existing native race/vet checks, independent
review, coupled contract checks, immutable architecture prebuilds, then coordinated
installation and physical acceptance. None are implied by this decision record.
Validation of the current native repair: all manager tests pass with the race
detector (17.608s); go vet and diff checks pass under Go 1.25 with dependency
network access disabled. Full native tests used scoped local loopback permission;
new causal tests intercept HTTP and require no listening sockets. They cover
released selected/unselected starts, pre-prime/select order, ordinary manual 64%
pause, rejected unselected-without-release and playing-with-release, changed
marker/owner, native failures, stale or deselected-output commit and one-use consumption. Existing
idle alias and local resume tests also pass. No provider/device action performed.
Independent review identified a deselected-output commit gap; commit now explicitly requires the exact prepared output still selected, and a rejection consumes its lease. Independent rereview, composed consumer tests, images and Green acceptance remain.

Rollback before publication is this native candidate; device rollback follows
existing docs/releasing.md. Unknown starts are never automatically retried.

---

# Active decision: bounded native playlist context — Control0.10.13 candidate

Implemented source candidate; unpublished and uninstalled. Base is actual merged
Control0.10.12/native source commit `d4d914e80cd0047286d196bb5012f77664330f99`,
exact tree `05be2aec21fe1f7906a4ea2f78e74c3a9d5694d1`. The independently scoped
playlist patch from the separate PodView follow-on applies cleanly to those bytes.

Existing evidence: the original owner accepted only exact track starts; SpotifyApi
already has the native `context_uri` form. Hypothesis: extending that same command
owner and source read contract can start real selected playlist contexts without
new playback, autoplay or volume policy. Changed chain: exact-account search read
→ owner epoch/source-bound selection in PodVoice → explicit context grant/native
readiness → existing prepare/commit → guarded Spotify context_uri send → existing
observation/no-replay receipt. Wrong account/epoch/source and stale native proof,
commit failure and duplicate commands are the relevant rejection boundaries.

Changes: bounded exact-account/epoch playlist catalogue service; context-specific
native proof read; `play_context` in the existing PlaybackCommands namespace;
Control manifest0.10.13 only. No Speakers files/version, provider credentials,
room policy, grants or model allowance changed. Existing native autoplay and
same-session/fresh-start volume semantics are retained.

Validation on this actual composed0.10.13 source:61 pinned HA2026.8.2 framework
tests pass in19.870s, with network disabled, no image pull and read-only source.
They include five playlist search/context/native-guard regressions.59 boundary
tests pass; changed Control modules pass syntax checks, versions are bounded to
Control0.10.13/Speakers0.26.19 and diff checks pass. The playlist runtime delta is
unchanged from separately reviewed patchdca0e3e0; final exact-tree review and CI
remain. Source tests do not establish provider or physical proof.
Rollback is this Control delta and prior integration files; deployment remains the
coupled explicit rollout/rollback process in docs/releasing.md, not an automatic
replay or installation. No publication or device action is performed here.

---

# Historical native-start preparation record (2026-10-15)

Status: candidate implementation in an isolated checkout. Released Speakers 0.26.18 and Control 0.10.11 remain unchanged; no Home Assistant installation or physical acceptance is implied.

The existing Speakers owner advertises `initial_volume: 35` to its Spotify engine, but its bridge independently uses a one-shot 35% ceiling. Control's new-track action currently checks only that the observed Spotify volume is below a caller max and can send `/me/player/play` before Speakers has proven the output and start level. The requested behavior is one fixed native start level for every fresh session/transfer/reclaim, followed by unrestricted normal manual changes within the configured control policy.

Decision: define the start percentage once in Speakers and render the engine setting from it. Speakers will expose a bounded, source-proven exact device/room/output readiness record tied to its room/alias incarnation and policy revision. Control may send a track only for that exact native device and fresh owner/readiness lease, with a floor of the shared start percentage; an unknown or stale state rejects. The ordinary five Spotify controls keep their existing admission path. Never join by visible name, HA area, alias guess, or observed Spotify volume alone. Do not create a second PodView volume setting.

Scope: local source and tests only. The native owner must protect the output before first audio, including transfer and reclaim, and must avoid resetting later user volume on same-session pause/resume. The source can prove dispatch and owner/readiness guards; a room recording and exact installed versions remain required for physical acceptance.

Candidate wire contract: `GET /api/start-volume/identity` reports the configured, live supervised engine ID for generic HA play paths. `GET /api/start-volume?room_id=<configured room>&target_id=<exact Spotify device>` returns `podconnect.start_volume.v1` with that persisted primary engine ID, desired room, saved native output ID, fixed start percentage, policy revision, current alias binding, selected-output state, and `fresh_start` or `same_session`. Idle requests may name another configured room; the binding still names the current room until preparation. `POST /api/start-volume` primes the future output before an idle alias move, waits for the engine's new binding, selects the exact output, reads the fixed level back and issues one 5-second lease. `POST /api/start-volume/commit` consumes that lease once after the caller's OAuth wait and rejects changed owner, room, output, policy, alias, source revision or output level. An active different-room request is refused; there is no name-based fallback or claim of atomic external Spotify/AirPlay completion.

Control `get_track_play_controls` requires `room_id` and returns the native proof in `native_start`. `play_track` adds server-stored `room_id`, `homepod_id`, `start_policy_revision` and `alias_incarnation` to its existing exact track URI and `max_volume`; the provider target must equal the native engine ID. The native fixed start is a floor for `max_volume`, not a separate PodView maximum. The five existing manual Spotify actions retain their existing path. PodVoice must be upgraded together with Control; the released PodVoice 2.0.28 does not yet supply these fields.

The ordinary HA `media_player.play`, `play_media`, `play_from_library`, and playing transfers use the same native readiness/commit when their exact target is this engine; another Spotify device retains its own provider path. If a configured Speakers owner cannot identify its engine, those starts fail closed. The local Speakers `/api/play` remains a resume-only command: a held same-session pause retains the user's volume, while a released output is primed and selected at the shared start level before the local resume. A direct Spotify app can bypass Control entirely; the engine's rendered initial volume and native bridge protection still apply, but proving the first audible frame at the output needs a physical room test.

The native bridge primes the exact saved output before each automatic alias selection/reclaim, checks it again after selection, and re-arms engine/output start handling on an alias transfer. Fresh leases require the native readback within the existing quantized start band at mint and commit; both high and low drift reject. A selected same-session output may retain manually adjusted volume, but `play_track` compares the native output against the approved maximum at execution and native one-use commit. An observed OwnTone volume and a provider ACK are still not audible-frame proof; selection itself can have hardware timing that only an instrumented room test can settle.

Rollback: revert this candidate before publication; released versions continue to operate. Unknown provider-send outcomes remain one-use and must not be retried automatically.
