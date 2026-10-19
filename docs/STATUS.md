## Active lead decision — accepted Connect transfer lost on pre-effect busy, 19 October 2026

Lead: /root. Baseline is published Speakers0.26.32/Control0.10.18 source0d4e4226fe37dc6044ddaf4698830e00f75e16fa, independently verified public image. Actual Green log15:35:22: native transfer accepted → manager credit consumed → route/volume prime → OwnTone Connect recovery denied:transport_busy → HTTP500 → route_not_committed → attempt_finished. No retry was sent. Current engine ID matches existing policy; absence from the Spotify device list is separate unresolved evidence.

Causal owner chain: accepted native transfer and immutable credit → manager bridge tick/run/phase/intent → native output/START/transport/volume custody guard → prime → selection → observed output. The guard rejects before effect, but aggregates both temporary custody and persisted START ownership; actual busy class is not retained. A generic500 or timeout cannot prove no effect.

Hypothesis: a definitive temporary pre-effect refusal currently discards the same accepted transfer before its existing5s credit expires; retaining only that exact credit for existing bridge ticks can allow a later proven-free owner to route once. This does not claim it fixes the observed run if its owner was persisted START. First expose machine-readable definitive busy classification from the existing endpoint while preserving all guards. Unknown delivery must remain terminal, without replay. Persisted START/quarantine must never be stolen or cleared by retry.

Plan: minimal existing-owner C/manager response classification and pending-credit correction within the existing lifetime, no new timer/window, polling-generated credit, broad retry policy, attention bypass, account grant or reset. Revalidate exact run/phase/intent, alias/incarnation, attention and released state before a later request. Regressions: definitive busy→owner drain→one route; unknown post-send→zero retries; expiry/new transfer/alias/source change→zero replay; START/journal remains fenced. Focused native and manager harnesses; independent adversarial review and officialCI before release; publish/install only with the combined compatible candidate. Rollback boundary is this classification/pending path. Adversarial review found a possible later legacy bridge tick bypass after a spent/unknown transfer credit. Original helper-only negatives do not prove end-to-end no replay. Candidate is NOT release-ready. Extend causal boundary through the following ordinary bridge tick; require full-bridge regression proving no prime/selection after unknown, expiry or invalidated pending credit, while preserving explicit current start. No new authority, timer or reset. Resolve before final GO.

Full bridge regression also exposed a successful-recovery handoff gap: the following tick still had lastAlias=0 and could repeat alias prime/selection. Extend the same repair only to seed the bridge's existing alias bookkeeping after exact-current explicit recovery has actually removed the released marker. This creates no route authority and must not run after pending, rejected, unknown or stale outcomes.

Implementation result (planned Speakers0.26.33 / unchanged Control0.10.18): the existing distinguished recovery volume endpoint now returns a typed HTTP409 plus fixed no-effect header only for synchronous pre-effect temporary transport custody, after clean START absence and all other output/attention guards. A separate synchronous boolean carries that proof; negative errors from an already-attempted backend cannot impersonate it. START/journal/quarantine, ordinary failure, timeout and post-selection failure remain terminal. The manager retains only the same accepted transfer ordinal and original transport/deadline after that typed first-prime refusal. Existing bridge ticks revalidate it; no new timer, window, event, credit or account authority is created. Released-state loss consumes the pending credit.

Review follow-up implemented: an accepted-event fence (run/phase, selected alias and exact binding/incarnation) remains across credit expiry and pause/resume transport revisions, so the following ordinary bridge tick cannot turn clean absence into a replay. New explicit accepted ordinal can run ahead of the fence; actual source/alias replacement leaves the old fence inapplicable. Successful marker removal immediately retires only that same fence and returns its completed-credit bookkeeping receipt; the bridge seeds lastAlias/binding from that receipt instead of an older snapshot. Attention active/pending/unknown ownership is rechecked by every post-await recovery guard before any later send, not only at entry.

Full actual roomBridge child regressions pass for UNKNOWN→later clean-absent ticks with zero further writes, expired typed-busy credit→zero further writes, pause/resume revision changes→zero replay, fresh accepted ordinal→one selection and busy→drain→one selection. The fixture reports clean absence deliberately, exercising the previous legacy bypass. Existing ordinary clean-absence bridge tests remain passing. Final additional attention-during-read/prime/typed-busy and immediate success-fence retirement regressions pass. Focused Go race checks, full existing manager race suite and Go vet pass. The initial broad local attempt could not bind its existing fixture socket in the sandbox; the same suite then passed with local fixture socket access (36.420s). No provider/household calls were made. The complete actual published32 C patch stack plus the new overlay applies cleanly; a compiled extracted production owner→player→HTTP probe passes temporary custody, START/attention/prevent denial, backend-negative-code collision and drain cases. The permanent regression is in the existing complete outputs_attention_owner fixture; full Linux C fixture compilation/execution remains pending official CI, not claimed by the extracted probe.

Baseline correction before freeze: the earlier handoff archive was tree0cd61e4c, not the final published tree658b6ef3. Exact final32 index archive was generated and both differing paused-route patch/checksum files were restored; no authored runtime file overlapped. Composition/probe checks above use that corrected exact baseline. Independent review pending. This fixes the proven lost-pending temporary case, but does not establish which busy class caused Green15:35:22 or cure a persisted START owner. Actual Green playback remains unaccepted.

# Combined local candidate · Speakers0.26.32 / Control0.10.18 · 19 October 2026

Sole local assembly owner: this PodVoice delivery agent, coordinated with the root
lead. Fresh remote main remains `621b024ee3d4071655fd3017afcf14e01bd1d433`, tree
`19bb22b65b61cf10ac60829b5f59b8ecbb0e65b4`; fresh default-branch versions are
Speakers0.26.31 and Control0.10.17, and Control0.10.18 tag is absent. The extracted
baseline index independently reproduces the exact main tree before applying changes.

This local successor combines independently reviewed PC51 paused-route source with
the optional Control playlist-cover projection. Native/C source bytes retain their
reviewed patch hashes; Control projection preserves independent optional DTO behavior,
owner/config-entry/epoch fields and existing command admission. The existing
search service defaults include_artwork to false, preserving exact URI/name rows
for older consumers. Its descriptor explicitly advertises required:false and
default:false; newer consumers send the optional flag only after current HA service
discovery confirms that optional field. Invalid or absent images do not invalidate
playlists; no artwork is fetched here. Versions are planned
build32/18, not released. No remote branch, PR, tag, publication or installation is
performed by this assembly. Do not overwrite existing tags/images.

Next gates are official Linux manager/Native/C, HA boundary/framework CI on the exact
combined candidate, both-architecture immutable32 prebuild and source-label readback,
then same-subtree promotion; Control needs its own exact0.10.18 GitHub Release. One
coordinated Green update/normal-flow acceptance follows only after those gates. Mobile
and new product features are outside this local source assembly.

# Active PC51 paused-route decision · 19 October 2026

Actual installed Speakers0.26.31/PodVoice2.0.63 readback: one UI Play produced
Spotify playing on the intended target and native transport controls. One Pause
produced Spotify paused, followed by native output_proof_unavailable. Actual
OwnTone log reports source insufficiency/suspension at14:12:24 and real C
bound-state HTTP409 from14:12:36. This excludes manager-only proof loss; exact
retirement remains a supported causal hypothesis, not complete field attribution.
C pb_suspend's real flush callback schedules the existing ten-second output stop.
The current proof correctly requires a live exact session, FIFO reader and writer.

Lead chooses the smallest guarded retention owner instead of pretending a retired
session is current or adding broad fresh-START fallback. C commit alone is merely
admission: lasting retention may only follow the existing private Native actor's
actual successful guarded Pause effect and one exact consumed C-token confirmation.
Failed/no-effect Pause cannot confirm; confirmation ACK loss after actual successful
effect may retain the proven live owner while caller outcome remains unknown and
unreplayed. Confirmation is one-shot/unexpired, with exact prepare/revision/owner/
route; stale or changed source is rejected. Existing stop-timer ticks re-prove the
same live C session/START/durable FIFO-reader/writer/route, and loss clears retention
permanently before normal cleanup. Retention authorizes no command or alias grant:
Native-only alias revocation still denies fresh control, but physical resources may
remain paused until actual C retirement. Do not claim C can observe in-memory Native
alias changes. Competing admitted C transport clears retention; no new timeout, automatic resume, WebAPI fallback, volume/gain or permissions.

Planned causal tests: real suspend/flush/timer chain; successful native confirmation
retains exact live pause and allows truthful Resume; no confirmation on native
failure/unknown; wrong/replayed/expired/stale token/revision/source/owner rejected;
old paused completion/timer cannot affect replacement session; normal stop after
writer/source/revocation/competitor loss. Review actual Native+C handshake together.
This isolated successor does not modify frozen PodVoice64 or installed31. No
release/install/acoustic acceptance is claimed. Rollback is unchanged exact31.

Source result: the production stop-timer callback fails the focused expected hold
on the unchanged baseline and passes with confirmed-custody guarding. Controlled
production-body checks cover no admission-only hold, expired/wrong/uncommitted/
stale/replayed confirmation, source/route/competing custody loss, failed timer rearm,
and delayed stop after session-epoch replacement. They use inert dependency seams,
not real FIFO/RTSP proof. Existing real START/wire and HTTP owner fixtures now cover
the actual constructed START reader/writer/session, confirmation ordering, retained
stop timer, competing START projection and strict malformed/forged confirmation.
Full Linux C/Native fixture execution remains pending official CI: the local Docker
inventory command never returned and was terminated alone; no daemon restart or
new build environment was created. Local patch application/whitespace validation
passes. Native post-effect receipt source is being independently reviewed with the
same C endpoint. Neither source review nor these controlled checks establishes
actual paused HomePod Resume on Green.

# Active lead decision · Speakers0.26.31 candidate / unchanged Control0.10.17

Base: published/installed Speakers0.26.30, merge7c51ecb4c15c182c0fcdf3904cf5d18120d9cf93,
tree770abcc49afd5d4f472defcc8d4d6850833668ae. This source candidate is not published or installed.

Observed19October: one PodView2.0.60 card Play selected Shallow on the configured
Kitchen Spotify engine. A fresh provider observation showed that exact target
playing, while the current native room read returned receiver_proof_unavailable
and PodView retained its pending player. Actual identifier lengths and the exact
native predicate/HTTP failure are not exposed by the captured normal UI/logs;
field causality remains unconfirmed. No extra Play or receiver workaround follows
from this source finding.

Source defect: the Native bound-owner producer and OwnTone output owner accept
connection/session identifiers through255 bytes, but the manager alone capped
both at160. Real manager read fixtures reproduce rejection for161/255-byte native
owners while160 passes. Align only those two manager limits with the existing
255-byte contract. Keep exact account, target, room, HomePod, alias incarnation,
registry, currentness, C route and no-replay checks;256 bytes and wrong owners
still reject. The RELEASED marker is intentional and remains untouched.

Scope: one existing manager validator, one causal regression file, Speakers
version metadata and these changelog/status entries. Control0.10.17, Dockerfile,
Native/OwnTone patches and their manifests, rootfs, workflows and policies retain
exact30 bytes. Use the existing two-architecture branch prebuild and immutable
source-subtree promotion procedure before advertising31 on main. No new release
lane, service, fallback, admission, policy or integration release.

The focused source regressions and independent manager suite/vet passed. The
final candidate manager race suite passed (36.364s) and vet passed. Independent
packaging review, publication and installation remain separate gates.
Source review and tests do not establish actual Green playback, player recovery,
physical sound, current owner transfer or household acceptance.

---

# Historical lead decision · Speakers0.26.30 candidate / unchanged Control0.10.17

Lead: this chat's native delivery owner, coordinated with the PodView main lead.
Base: installed/published Speakers0.26.29, merge696d793e9bed5adef71b43c029e12768c9a2377f,
treef9414e010b40973f3a7c3cb7f50f46d6ef79cc5c. No new candidate is published or installed.

Observed field failure: installed PodVoice2.0.55 completed provider cleanup but its
original attention release remained unsettled after HTTP200 responses at13:58:02–19
CEST on2026-10-18. Speakers restarted later during its normal29 installation. That
later restart cannot explain the first timeout; the historical native response
bodies and concurrent released/paused state were not captured, so field causality
remains unknown. Direct evidence and limits are retained in the operator receipts.

Source hypothesis: roomBridge's released/inactive route hold precedes
nativeReconcile and can prevent an already admitted inactive original attention
lease from retiring. Reproduce this on the actual29 roomBridge owner before any
runtime patch. The chain is Voice PE Stop/model close → provider and heartbeat
retirement → original attention release → native lease/custody settlement → exact
release receipt → PodVoice teardown/rearm → next wake. Check one owner on either
side and concurrent engage, expiry, replacement, foreign output and restart paths.

Preserve these invariants: released rooms cannot be reclaimed by routing; only
the exact admitted original lease may retire; active/replacement owners and epochs
remain fenced inside the owner lock; custody retirement requires its existing
durable proof; unknown effects are never blindly replayed. Do not move the full
nativeReconcile ahead of the hold. The correction may only advance the narrowly
guarded inactive/original retirement path. No BEGIN/UPDATE, alias selection,
start/volume priming, released-marker removal, foreign claim, provider, firmware,
prompt, timeout, policy or UI changes. Existing29 diagnostics and28 patches remain.

Plan: reuse the existing joined-child actual roomBridge regression and inert
native HTTP fixture. Record exact29 RED, then focused GREEN for original release
without restart and rejection/currentness/unknown-effect cases. Run formatting and
the final manager race/vet checks once the reviewed diff settles. Independent
adversarial source review is required before any push or release. Rollback before
publication is the isolated candidate; installed55/29 remains the operational
baseline. Any future installation reopens the affected physical acceptance. A
regression or software fix does not establish the historical cause, actual sound,
fast closing, next wake or complete end-to-end acceptance.

Current result: the actual29 roomBridge regression reached causal RED: released
+paused remained pending after1.20s with zero native release PUTs and zero other
writes. The unreleased positive control sent one native release and reached
released; its assertion is being limited to attention settlement because the
ordinary post-settlement bridge may perform unrelated permitted work. An initial
loopback sandbox denial is infrastructure, not product evidence. The first release-only draft reached focused GREEN for the causal pair: exactly
one original native release, zero other writes, and the released marker preserved.
Initial independent review was HOLD on atomic command admission/direct effects
and missing negative cases. The revised actual roomBridge fixture now passes9/9
child modes (about2.0s), including original attention HTTP POST pending → GET
released, released paused/stopped/idle settlement, active and foreign owner
refusal, uncertainty without duplicate effects, and GET-time replacement, direct
effect and duplicate-release revision guards. The durable positive case was additionally run on an isolated exact29 baseline
with only the final regression file: RED after1.22s, pending with zero release
PUTs and zero other writes. On30 it proves persisted original output and session
retirement after journal reload, HTTP GET released, duplicate original POST
durably released, and exactly one native release PUT. Independent adversarial
rereview issued source GO for the four runtime files and this regression, closing
the prior HOLD findings. Runtime/test bytes stayed frozen through the final manager gates: the full
manager suite with the race detector passed (36.860s), and go vet passed. The
reviewed four runtime-file and regression hashes match the tested files. Native
patches, Dockerfile, workflows and Control remain unchanged against29; config
changes only the Speakers version to0.26.30. Final publication and installation
remain pending the main lead's combined delivery GO. The source GO and these gates
do not establish physical sound, fast closing or next-wake acceptance. This reproduction proves the software gap, not the historical onset.

---

# Historical lead decision · unpublished Speakers0.26.24 / unchanged Control0.10.16

The actual0.26.23 ARM publisher38945871257 failed in PC29 START_LOCKED: its fixture's BEGIN IMMEDIATE raced the real library initialization transaction before player admission. AMD23 has already published; immutable tags are preserved. Bump only the planned Speakers candidate to0.26.24 and amend the existing queue patch/checksum with a fixture-only library-worker readiness marker. The real library completes initscan before its command loop; the test marker therefore precedes fixture writer acquisition. The existing six-second observer alarm and actual held-lock/player-admission/cleanup assertions remain. No production source, timeout, Native/Control wire contract, grants or UI behavior changes. Control stays0.10.16; PodVoice2.0.36 accepts the unchanged queue_occurrence_v1 contract. Independent fixture/packaging review and actual compiled ARM publisher are required; prior failure is retained. Existing unchanged-source checks remain evidence only for those bytes. Green remains Speakers22/Control15/PV35 until the combined candidate is published and installed with backups and preserved policies. Physical music/robot/timer/provider acceptance remains open; rollback is the installed22/15/35 batch.

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
