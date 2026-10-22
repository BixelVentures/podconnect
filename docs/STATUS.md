## Active lead decision — PC58 content-free START failure boundary, 19 October 2026

Lead: /root; isolated implementation: led_finish_patch. Baseline is installed Speakers0.26.35/Control0.10.19 main59254ab9b7eb70c796a1f722579e2d5fb3aeccfb, tree624c196fc489e9e37157f5a05c5864efab84387f. Existing downloaded PodVoice trace places the second fresh content preparation at22:24:41.291 and its HTTP200 service response at22:24:43.087; existing Speakers log records source starvation at22:24:42/53 and bound-state409 from22:25:05. No new EPIPE or SIGSEGV follows35 startup. The failed C/Native phase and audible outcome remain unknown; PC58 stays open.


Morning field decision — 20October2026, lead/root, source owner pc36_diag_recovery: installed/running Speakers0.26.37 is verified. One bounded actual flow showed RoligAften→TakeFive in UI, Next→SoWhat, then one fresh DinnerMusic START at08:35 returned UNKNOWN and emptied the UI. Retained normal HA log `/private/tmp/podvoice-field-20261020/pc37-live-log-0836.txt` records OwnTone starvation at08:35:18 (deficit266364/264600), then at08:35:25 `content_start_failed stage=c_barrier last_c_phase=unknown error_class=c_step_failed outcome=unknown`, followed by one native health miss at08:35:27. No replay or audible PASS is claimed. The prior35 FIFO keeper repair and37 logger do not close PC58.

The proven chain has reached C gate_closed_reader_ready, Native custody supersede and successful quiesce, then fails during C barrier before publish/open/resume. Last validated UNKNOWN preceded Native cleanup; c_step_failed includes a valid terminal C receipt. Falsifiable cause hypothesis: the gated replacement loses C exact input or asynchronous route readiness and expires its existing pin before route_ready_closed. Source investigation must distinguish input-thread source/FD/drain proof, target-route callback/session proof and expiry/cancellation, including starvation one step before and stale callbacks/cleanup one step after. A full retained field log/protocol phase would discriminate these branches; the visible tail alone does not. Invariants: one exact Native/C owner, same FIFO identity/generation and source, quiesce before drain, closed output gate until exact readiness, existing deadlines, UNKNOWN/no replay, one cleanup/custody result. Non-goals: no retry, timeout/gain/provider/UI tuning, legacy path or speculative runtime fix. Next: trace shipped source and existing regression harness, reproduce the demonstrated boundary with the smallest existing causal fixture; only then choose a minimal owner correction and independent review. No candidate version is agreed. Rollback boundary is the eventual bounded source/test diff; no release/install is authorized by this investigation record.


Morning source result (not released/test-ready): exact shipped37 outputs.c, airplay.c and player.c were recomposed from pinned upstreamfa607a02768d9e29cd87065ac30ac66ea673f1ae through all12 current OwnTone overlays and matched the source used for investigation. An exact-body local callback adapter proves a concrete owner defect: outputs_device_flush registers device_flush_cb (replacing nonexact start_connected) before the AP backend notices the connecting session is not STREAMING and returns a no-op. The same current session completion is then delivered to flushcb; no route35 readiness callback runs. This is source hazard proof, not proof of the field cause.

Root approved the minimal ordinary-starvation owner guard after existing source_read/EOF/error/drain and before read-deficit recovery: while the START gate is closed, clear the intentional deficit and do not let legacy suspend/flush/resume take session ownership. The earlier genuine host-timer overrun branch remains unchanged fail-closed safety; this change is not blanket callback protection or timeout tuning. Existing mode18 now drives its fresh successor through actual FIFO barrier, AP constructor/receiver/pairing, exact35 route/open/completion after a real empty-FIFO timer deficit tick, then checks ordinary open-gate starvation still pauses; the prior real-FD keeper regression remains. Independent adversarial source review is GO for overlay e2811725562df4e31ef011b848ae2e1f46c58b7239a6106773d026b9955e326c after correcting the fixture to read the successor's original deadline. Source whitespace passes; actual composed Linux mode18/full gate remains required. Root authorized one planned-build Speakers0.26.38 source candidate on actual37 baseeafb12214b762337f08d12aed82e015106ce9519; Control19 is unchanged. This exact overlay is appended after FIFO35 in the existing checksum/Docker/official Linux application chain. Packaging review and mandatory actual Linux execution remain pending; no merge/publication/installation or corrected-field PASS is claimed. Owner/root must review the frozen composition before delivery; rollback is this two-file OwnTone overlay.

Causal chain: current selected offer → one C prepare/commit → Native reservation/quiesce → C barrier/route → publish/open/resume/complete → exact writer/custody completion. Every failure currently collapses into a terminal outcome without naming the owner step; cancellation may overwrite the retained C receipt. Hypothesis: one fixed-stage, validated-last-phase, finite-error-class terminal log at the existing failure owner will distinguish tomorrow's exact failed boundary without changing behavior. Invariants: exact owner/source/grant/generation, existing locks and deadlines, one terminal result, UNKNOWN/no replay, no private IDs/content/raw errors. Non-goals: no cause claim, playback correction, endpoint/schema, retry, timer, provider call or audible action.

Plan: change only the existing Native commit failure closure/call sites and its existing sequence fixture. Snapshot validated phase under existing op.mu before cancellation can overwrite it, preserve the original cancel/finish order (including native consume cancel-first), and emit outside locks after cancel/finish; keep all transitions/calls/deadlines/discard intact. Extend post-commit open-fails plus failed-exchange and native-consume cases to assert finite logging, zero resume/complete/replay and preserved custody. Reuse existing native gate, formatting and independent adversarial review. No live action or audible test before Tuesday20October08:00Europe/Copenhagen. Rollback is the appended diagnostic patch/gate wiring only; source proof, publication, installation and field acceptance remain separate.


Frozen-source record (candidate37; delivery below): the existing failure owner now emits finite stage/validated pre-cancel C phase/error class/outcome outside locks after the original cancel/finish sequence. Native consume snapshots its diagnostic class before its existing cancellation so cleanup does not relabel an actor failure; its original cancelled outcome remains unchanged. C step failures are conservative boundary observations, not root-cause diagnoses; narrower critical deadlines can remain generic step failures. The existing sequence fixture adds exchange failure and native-consume failure cases with no resume/complete/replay and retained custody/outcome assertions. Exact overlay SHA256 is 3d4b34ae199ce78802bb8830f27bba389c8656dd1c6482249768b28531f4dfe8. Prior independent production-source review was GO conditional on Linux execution; candidate37 fixture/composition review remains pending; gofmt and native diff whitespace pass. Overlay is appended to the existing checksum manifest, Docker application chain and official Native race/vet/build workflow; no new harness or dependencies. Candidate36 source commit05e2db531492505239dcb8f45df9e56fcd9aa3c5 is superseded and not released on main: its new consume-fails mode retained the required cancelled outcome but conflicted with the sequence fixture's old shared UNKNOWN-only assertion. Candidate37 changes only that fixture exception and release metadata; production diagnostic bytes are unchanged. Independent review must trace all newly added modes through shared assertions. The first candidate37 Linux test step remained active; a temporary standard-library-only reproduction proved the newly introduced synthetic deadline handler could block httptest.Close on an unread POST body. Lead removed only that unreleased experimental mode and its custom context machinery to preserve the bounded causal diagnostic scope. All shipped baseline35 regressions remain; open-fails/open-exchange-fails and native-consume failure regressions remain. The temporary RED/GREEN seam evidence is retained but is not a C-backed suite pass or a production timeout failure. Production deadline behavior and diagnostic bytes are unchanged. At freeze, final composition review and official Linux execution remained pending; no publication, installation or physical acceptance was claimed. Subsequent delivery evidence is recorded below. PC58 remains open; Control0.10.19 is unchanged.


Delivery evidence / overnight handoff — Speakers0.26.37 released, Green installation pending: exact source head9920ca690f1cb1a2ca9652a83bef6d93e3fa448c passed official PR Native race/vet/build/smoke and OwnTone C gates (run39323842429). PR60 merged normally as260ded32c93a421ecee6815547d2e5bbbb4ceee0 with identical reviewed tree5a5397c4d49d973671b3999c8d2aa573ff722591; automatic main source gates39324208185 also passed. Publisher39324207961 completed successfully for both architectures. Independent anonymous public ARM readback verified manifest sha256:30335758cff995d1301183986ecbe9541130ec3d71d8b20083f5c70ecc7523e2, Linux/arm64/aarch64 and source-tree f294370217d5e8e21056241ddf0de7918b7f9b0f, matching the independently recomputed reviewed Speakers subtree. Registry metadata has no commit label; the commit association is the exact GitHub merge/CI/publisher evidence. Control remains0.10.19.

Root started the normal Green update35→37 with backup enabled. Last actual HA observation: **Installerer98%**. Native Chrome subsequently reported the Mac locked, so final installed37 version and running state remain **UNKNOWN**, not failed; do not repeat/restart the update. Root owns the next action: after normal access returns, read the final installed version and running state. No audio, provider/household test or fresh playback probe occurred; none is authorized before20October08:00Europe/Copenhagen. The existing one-off08:00 follow-up will resume this thread; no recurring monitor is required. PC58 field replacement cause/acceptance remains open, and published software is not installation or physical proof.

## Active lead decision — PC58 live FIFO reader replacement, 19 October 2026

Lead: /root; isolated implementation/reproduction: led_review_current. Baseline is installed Speakers0.26.34/Control0.10.19 main a0a7d1e3d704220382593e70269ded6a3a22ac1f, tree015eedb38adc775a2f714e897b6e173c766ef0d8. Actual normal Green evidence after Play Take Five and Next So What: one fresh Italian-playlist START returned UNKNOWN, with native FIFO EPIPE at21:53:30 and OwnTone insufficient data at21:53:32. No new SIGSEGV/restart shown. No replay was sent. The exact failing C phase is not retained in the public result DTO; field attribution remains unknown.

Causal source hazard: Native consume reserves without pausing, then C commit installs its reader before Native quiesce. The input-thread replacement stops the old reader before setup opens the successor. Renderer pipe_autostart=true normally adds a watcher FD, but input stop schedules watcher reset, itself close→open. A controlled overlap can therefore remove every reader while the old writer is still active. The output gate is not writer-quiescence proof. Existing retired-stage reader does not cover every active replacement.

Plan: extend the existing START35 mode18 actual C/player/input/FIFO harness with a scheduled watcher-reset close at the real old-input close and a real existing writer write; retain all source/route/epoch/cancellation guards. First reproduce the exact production input command with real FD boundaries, then propose only a local same-FIFO reader hold across replacement if causal evidence supports it. No new endpoint, automatic replay, timeout change, provider call, unrelated tuning or native/account authority change. Any local extracted-body probe is lower evidence than the full Linux harness and does not prove the Green cause. Independent adversarial review and existing official Linux gates remain mandatory. Rollback boundary is the isolated FIFO-handoff repair; installed34 and PC56 evidence stay preserved.

Independent review found that the draft's old-reader fifo_barrier(false) proof disables the old pipe's autostop before later pre-stop failures. The first draft is NOT approved. Refine only this proof boundary: optional private-backend fifo_hold duplicates the actual pipe read FD without mutation or reading; input owner checks its fstat identity against the named FIFO and eventual successor, then closes it. This is an internal input-definition hook, not an endpoint or authority grant. Trace every initializer/caller and preserve failed old-source state.

Source result: the exact shipped input_fifo_start_exact body, compiled with inert DB/event seams and real FIFO read/write/close boundaries, returned success on the old code while the scheduled old-input plus watcher-reset close made the existing writer fail with EPIPE32 (red-body-probe.json). This proves a source-level permitted hazard, not the cause of Green21:53:30. The local same-inode read-only keeper produces a successful write in the same schedule. Ten focused exact-body cases pass: success, expired/wrong-path requests, missing named path, keeper/named identity mismatch, unavailable hold, setup failure, successor-barrier failure, changed successor identity and late expiry. The probe also compiles the exact private fifo_hold body and asserts the old autostart flag is unchanged on pre-stop rejection and at the old stop. Actual original-FIFO FD counts confirm keeper cleanup on success and all injected failures. No keeper is registered with an event/read owner and no bytes are read by it. The keeper spans only this synchronous input command, cannot authorize output or adopt a source, and protects only same-path/same-inode replacement. Existing setup, drain, epoch and deadline rules remain; changed/new identity or late failure rejects and closes the unproved successor. Different old-FIFO replacement is explicitly outside this repair.

The existing START35 mode18 fixture now drives the real input command and schedules the independent watcher-reset close at the actual old-input close through a test-only close linker wrapper, then writes through the real existing FIFO writer. It also retains expiry/path rejection and the existing explicit drain/epoch transition assertions. This permanent full C harness has not been run locally because its established Linux dependencies are unavailable; it is mandatory in existing official make-check before merge. Local exact-function C compilation and ten cases, source diff whitespace, checksum/patch application and packaging syntax checks are the available lower-layer evidence. No Native retry or protocol/schema/endpoint change is introduced. Planned metadata35 leaves Control0.10.19 unchanged; no release, install or acoustic proof is claimed. Post-stop failure is terminal: the keeper closes and the original writer may have no reader; it is not a successful handoff, rollback or replay authority. Independent final source review remains pending.

## Active lead decision — PC56 Vorbis retirement crash, 19 October 2026

Lead: /root; isolated implementation: led_review_current. Baseline is released Speakers0.26.33/Control0.10.19 main e94ecf19d5d8b4169d540817c54b7d1c5164a016, tree751a211212aeca0a7e205384970fed3e8792a426. Existing full downloaded normal Speakers log (SHA256 aefdfa91bf963c5a818f9e9b0dc418a6d8a7b9bc1f3f67ab491e687de2503ccf) records SIGSEGV at19:01:32 in vorbis_dsp_clear → Decoder.decoderStateCleanup → Close → ownedAudioSource.Close → Player.applyStart35 line195 while retiring a displaced source. The earlier accessibility tail omitted the crashing goroutine; no new playback was required.

Causal chain: current guarded START installs the new owned source → displaced old source is retired once → decoder Close holds its mutex and prevents later Read → C codec resources are cleared → new source remains independently owned. The old decoder currently clears/frees Info before DSP and Block. Xiph libvorbis block.c stores DSP.vi and dereferences it during dsp_clear; its reference decoder clears Block then DSP and calls info_clear last. Hypothesis: the observed old-source retirement dereferences freed codec Info. Preserve exact source/START/route/account ownership, partial-initialization guards, Close idempotence, source read cancellation and UNKNOWN no-replay. No new API, version, timeout, codec, queue or UI behavior is planned.

Plan: change only dependency cleanup order (Block → DSP → Info), add an actual libvorbis initialized decoder regression covering close before/after lazy block initialization, repeated Close and malformed-header cleanup, and run that package in existing native test/vet gates. Existing source retirement regressions remain authoritative. Independent adversarial source/partial-init review is required. Local codec execution requires existing C libraries; any unavailable local build is not a passed test and official Linux execution remains pending. Rollback boundary is this appended native cleanup patch and its existing-gate wiring; no release/install or household proof is claimed.

Source result: exact released Native base c191a43 plus all21 current patches was reconstructed before editing. The only decoder behavior change is dependency-safe BlockClear/Free → DspClear/Free → InfoClear/Free under the original Ref guards; locking, closed state and displaced-source selection are unchanged. The appended regression uses actual libvorbis allocations, including close before/after Block initialization, release of C references, sequential repeated Close, Read after Close, partial initialization without DSP and malformed-header constructor cleanup. It is a resource-lifetime fixture, not an encoded-track or audible proof. Existing official Native race/vet and Docker test commands now include ./vorbis and apply the same checksum-pinned patch after the existing stack. Gofmt, native diff whitespace, new-patch application against exact composed33, all22 manifest entries and package metadata checks pass. The nested patch's legitimate context whitespace is checked at native-apply level rather than treated as outer-source indentation. Local go test was blocked before compilation by missing pkg-config/libvorbis; no C-backed RED/GREEN or runtime pass is claimed. The existing actual crash is direct failure evidence; Linux C-backed test/build and independent final-pin review remain required before release. No version bump, account-claim branch, provider request, installation or household action is part of this source draft.

## Active lead decision — fresh profile ID diagnostic, 19 October 2026

Lead: /root, implementation public64_readback. Actual Green Control0.10.18 read get_room_content_controls returns native_start_unavailable before playback. Existing identifier diagnostic returns identifiers_different, but compares stored config-entry /me.id with fresh native AP canonical-username bytes; namespace and human-account relation remain unknown. Stored profile age is not retained. No relink or identity rewrite is justified.

Causal chain: existing operator-only read → exact live config-entry/API/coordinator owner → same OAuth session bounded fresh GET /me → current native identity read → content-free identifier comparisons → unchanged owner proof. Hypothesis: a fresh same-entry profile can distinguish stale stored Web ID from an unchanged mismatch without changing playback authority. Do not compare AP to account_id, emit profile/IDs/digests/tokens, add routes, migrate unique_id, alter policies/grants, authenticate Native, reset or replay playback.

Plan: extend only the existing diagnostic response with separate stored-vs-fresh /me.id and native-vs-fresh /me.id enum comparisons, preserving identity_relation=unknown and the old stored-vs-native comparison. Reuse existing API.current_user, five-second service deadline and OAuth owner. Bound profile bytes; fail closed on malformed profile/native reply, provider error, cancellation or owner replacement. Existing focused service/pure tests with stale-stored/fresh-native match, genuinely differing bytes, malformed/account_id-only/error and changed-owner cases; independent review before freeze. Rollback is this diagnostic extension only. Source result: existing service regression was RED before implementation and is GREEN (7 tests); existing pure diagnostic regression is GREEN (8 tests). All five changed Python files pass Ruff lint and format checks. The real HA framework fixture is updated for the extra same-session GET /me; official pinned Linux HA execution remains pending. Independent source review GO verifies patch 89c2096eca3adfc4597aac17c9bde9a7b0f66e826d8ee4d1d87922facd9594c0 and all six integration/test pins; byte comparison never establishes AP/Web API account equivalence. API.current_user had no runtime callers before this service, and config-flow direct /me requests are unchanged. The profile read is bounded to 16 KiB inside the existing five-second deadline. No provider or home action occurred; current Green failure is not claimed fixed. Planned integration-only Control0.10.19 changes no Speakers0.26.33 files; no publication or installation is claimed.

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

## Active PC51 paused track controls — 21 October 2026

Lead/root; Native/C source owner voice_closure_fix; manager root; independent reviewer voice_music_fix. Current source is main83c933d/Speakers38, unchanged installed version. Observed source defect: a real confirmed paused START retains Resume, but manager intentionally denies Next/Previous despite the issue's required controls. The current composed Native helper forces Next playing; replacing stream and accepting competing C transport retires pause retention. Guard removal alone is invalid.

Hypothesis: existing guarded Native preparation/effect can preserve paused state for a paused track switch, and the existing one-use post-effect C confirmation can retain only the still-exact current route with matching token/native revision, source/writer/session and epoch. Compose the actual shipped patch stack first; do not infer final source from an overwritten intermediate overlay. Admit manager Next/Previous only with this Native+C contract implemented and causally checked. Invariants: exact current account/room/alias and source/route/generation, one guarded effect, no automatic Resume, UNKNOWN/no replay, unchanged deadlines and playing flow. Chain: paused current owner read → Native staged track → C bound prepare/commit → guarded paused stream replacement → actual post-effect confirmation → fresh route/control read; old tokens, output timer and late completion cannot retain/retire a successor. Non-goals: UI, provider, gain/timers, new protocol framework, implicit route authority or live music. Regression: both paused track directions, no playback, fresh route retained, stale owner/source/session/token and UNKNOWN cleanup, plus unchanged playing/Resume neighbors. Rollback removes only the new source/manager delta; no release until the combined candidate is ready. Draft not test-ready; actual Linux Native/C execution and independent review remain required.

PC51 draft result: manager now offers/adopts Next/Previous only through the existing joined Native/C actions and exact current START owner, retaining after-await/commit checks and UNKNOWN/no replay. Native preserves actual paused state on same-session track switches, rejects terminal-autoplay session changes before effect, and uses the existing one-use post-effect C confirmation; C rechecks the same live custody, exact token/prepare/revision, current source/writer/route and session. Playing Next and actual external Pause/Resume generation invalidation remain unchanged. The manager causal cases fail on baseline, pass after the two admission changes; focused neighbouring route/replay checks with race detection and Go vet pass. Actual composed Native Linux Go1.25 focused cases and vet pass. Actual composed C outputs-owner suite and final `airplay_callback_owner --start-retire-wire` pass, including real local FIFO/reader/encrypted AP2 route and paused track custody. The initial invalid-hex C fixture failure is retained as test setup, not a product cause; corrected inputs were recompiled, temporary diagnostic removed, and final same-case execution passed. Existing image/caches were reused without provider/home access. No unchanged broad suite repeated.

Independent adversarial review /root/voice_music_fix gives final software GO on SHA256 manager d3f80ad84da129da440ed860b3d14ac062512f7a7e56dcb74515da94aca4e115, manager regression c13b8576608efa5b3756317c0d28effb40e1363b5ff1a5acda76dcdac92cff07, Native overlay 2f38ff353d33a8c774854f7c2a546e3ba7106f613f04c7f172fc1a7fa9f1597c, C overlay 752b4c26022a409ac61e0d4c608c0b6e52b73c213fca38100a23ced4c9b046f0. Docker/official-CI stack wiring and checksums match; overlays are generated from the exact composed source diffs. Status **planlagt samlet build**, no version assigned, no merge/publication/Green installation and no physical/audible claim. Final official candidate CI and matching installed acceptance remain required. No further PC51 source change is justified by current evidence; preserve the deliberate fail-closed terminal-session boundary rather than inventing a cross-session owner. Next sequential work follows Connect→Voice PE→remainder; no parallel issues or partial release.


## Active PC58 direct Connect no-start — 21 October 2026

Lead root; source owner voice_music_fix. Fresh Green38 log at20:54–20:56: authenticated transfer → explicit alias selected → failed setting stream, generic ErrAliasConflict; user confirms Spotify timeline stands still. This differs from prior content START starvation. Actual withNewOutput can refuse queue custody, unknown custody, pending retirement, retained START or failed MOVE retirement. Retained completed START is a reproduced-by-source obstruction to a fresh writer, but the15:48 retirement-prepare409 means field quarantine is also plausible; do not choose a field root cause without evidence. Existing exact reader/writer/native/C retirement safeguards stay intact. No blind custody clear/replay, false native_close_done, forced output, policy or deadline change. Minimal immediate source scope: finite content-free refusal reason in existing output admission plus real Player.SetPrimaryStream regression proving failed create, no output/side-effect and preserved owner. This diagnoses the concrete unresolved branch and is not a functional repair. In parallel within this issue, trace whether the existing canonical START handoff can safely handle the explicit authenticated dealer transfer; no new architecture without a bounded reviewed contract. No home actions or release. Whole chain required: dealer transfer/load → alias owner → staged track → output/source custody → exact C route → new writer → actual start acknowledgement; stale/unknown and subsequent normal-track paths must remain fenced. Rollback reverts only new diagnostic delta.

PC58 bounded source result: fixed content-free error classes now distinguish output admission queue_busy/custody_unknown/retirement_pending/start_custody/move_retirement_unproven while preserving the exact previous admission order, ErrAliasConflict via wrapping, and zero creator calls on rejection. Actual Player.SetPrimaryStream causal regression covers unknown, pending and completed START; focused ownership cases and daemon vet pass in cached offline Linux builder. Independent root review verifies no broadened creator permission and errors.Is remains intact; existing direct sentinel comparisons do not consume this boundary. This is NOT the functional repair and not field-cause proof. A lost FIFO can strand completed START; existing retirement requires original descriptor open and therefore cannot be bypassed after loss. A safe closed-prior-owner recovery would require a new exact Native/C orphan-retirement contract, not a one-line retry; it is not speculatively implemented without determining the actual guard. Include finite classification in the eventual combined candidate, preserve the current failed attempt and unknown outcome. No audio, restart, home action or partial release. Final source overlay SHA256a93e7e6b5899ee999f3e26b6e4c7780a1a648c28b804fb75964ef6ef470ff83f is appended after reviewed PC51 in Docker, official CI and native-stack checksum.

Final diagnostic review also includes fixed writer_generation_exhausted error classification and the truthful generic output-admission test name; no guard or output behavior changed. Earlier overlay hash ea7fcbef is superseded before commit/wiring.


## Active PC58 functional correction decision — closed prior owner only

Lead root; Native voice_music_fix; C voice_closure_fix; root independently reviews integration. Source demonstrates a concrete missing recovery: original player closes its FIFO (normal output failure/Stop), old C prepare may refuse because the writer is already closed, AppPlayer.Close joins the decoder but retains quarantined START custody, and a DIFFERENT authenticated player can never open a new writer. Fresh20:54 failed-transfer trace is consistent but does not prove this exact guard. We will correct this reproducible closed-prior-owner gap without claiming it is the selected field cause. No same-active-owner or arbitrary UNKNOWN recovery.

Hypothesis: the existing retirement protocol can safely attest an already-closed prior writer by a narrow optional prepare mode, with independently proven exact current completed START token/journal/process/pipe, no writers, no foreign current source/output/session, no pins or concurrent operation. Native invokes only after validated explicit authenticated dealer TransferState/context and matching nonzero alias, before alias/state commit. Prior owner must differ and its AppPlayer plus decoder Close must be joined. Existing synchronous dealer actor calls C directly, never itself through manager API. App admission remains blocked across awaits; one captured old START and pending operation prevents a later owner crossing.

Private wire: normal eight-field prepare remains byte-identical. Ninth field writer_already_closed:true is accepted only for START and proves no live writer on exact original process/dev/inode (FD reuse alone is not proof). Because original reader may already be gone, this branch may not fabricate a pending reader-ready receipt: it must durably gate, drain old exact input or independently empty owner, retire only owned/no-foreign C contexts and stage fresh configured reader, then return existing terminal retired+drained+durable+fresh-reader witness with prepare schema. Native may atomically clear ONLY the captured old quarantine/custody on this matching terminal and unchanged source snapshot, then continue the current fresh user transfer once. Any missing/changed/stale/busy/unknown result stays quarantined, zero new writer and no retry. Old output callbacks cannot cross newer session/alias/registry or input/source generation.

Regression: actual closedoldworker→newexplicittransfer can admit one replacement after terminal witness; live/sameowner/pending/foreign/stale/wrongpipe/newwriter/lostreply never clears. Actual C FIFO/AP lifecycle covers closedwriter vs oldlivewriter, empty vs foreigninput, currentvsnewerjournal, freshreaderbeforeterminal. Reuse normal retirement and PC51 neighbors; final exactstack/adversarialreview required before combined publication. Rollback removes optional branch/helper and preserves durable evidence; existing38 installed unchanged. No speculative route/audio PASS, home action or release.

PC58 functional draft result: the closed-prior-owner recovery is implemented in existing private retirement and direct authenticated transfer paths. Native waits for old AppPlayer+decoder Close, validates explicit current transfer/alias/context, obtains one exact C terminal retired witness, rechecks writer generation/pins/ownership, clears only captured old START, then admits the fresh standard transfer. No synthetic queue/session/podcast conversion: ordinary dealer TransferState keeps its existing session/queue/position/options flow. C independently proves all old FIFO writers absent, original current completed token/process/journal/pipe and no foreign route/session/input; old live reader or independently empty input is drained, durable terminal saved and fresh reader staged before acknowledgement. Same-current-player output loss, C UNKNOWN/cancelled/newerowner and unproved field predicates are NOT cured or accepted.

Actual focused evidence: Native TestClosedPriorSTART causal cases and normal dealer/queue/pause/retirement neighbors PASS; positive case opens a real successor FIFO via Player.SetPrimaryStream and preserves kernel FD reuse. daemon vet PASS; root targeted Native race suite session12122 PASS. Actual C final2 session68402 exits0 for closed writer26, no-session/live-reader27, empty-input28 and original paused/retirement18. Original normal prepare fails after real writer close; new branch returns terminal only on exact proof. The earlier mode27 foreign-pointer fixture assertion FAIL is retained; the invalid fixture mutated only an irrelevant saved pointer while no live session existed, and was corrected without product workaround. Independent source findings resolved: UNKNOWN-with-witness rejected by new Native callsite, caller deadline respected, postawait queue/permit fenced, no-input route/selection fenced, real FD identity verified.

Root and independent peer source review GO on final Native overlay SHA256501152138ab0d3d9bba7b5495f185428a8adf9f58de7d0f9d94d7c6283d1e8e0 and C overlay SHA25607aa2da3080662074acb2c38727f499199059b3f29a495a94c4318f8defca5a4. Exact Docker stack replay found a duplicated PC51 test hunk in the first C serialization (2ae9a1d1); regenerated only overlay against actual PC51 stack, without changing any compiled/tested source. Final all26 Native overlays reproduce103 changed tested files; all15 C overlays reproduce45 changed tested files exactly. Both reverse-apply checks and diff-check PASS; Docker/CI checksums and three new actual C modes are wired. Status planlagt samlet build, no version/release/install; original field guard and physical audio remain unproven. No reason to rerun unchanged gates before the final combined candidate.


**PC58 adjacent active-owner output-loss decision (same issue).** Root observed actual worker outErr closes its FIFO then emits generic Stop; daemon Stop only emits status and does not reset active provider state or retire the failed AppPlayer. That leaves the same failed worker unable to enter the new closed-prior recovery. Correct the whole affected flow: introduce a distinct output-failure event only for real nonnil output error, bound to a monotonic worker output epoch; normal manual Stop/EOF semantics and existing event values stay unchanged. Stale event must do nothing before any state/generation invalidation. Genuine current failure must retire current alias/admission and use existing inactive/Stop/logout/Close; next explicit authenticated user transfer then follows the already reviewed closed-prior C terminal proof. No replay, automatic Play, provider retry or new loop.

Risk: queued old failure can arrive after a new same-worker stream or async START, so acceptance and alias/admission retirement must be linearized against actual output install without reversing source/alias lock order. A mere earlier epoch read is insufficient if a new output can install before teardown. Native owner must demonstrate guard and real EPIPE→failed-session teardown→fresh-writer recovery, stale failure after new writer, quarantine and non-Zeroconf behavior. Non-Zeroconf has no logout restorer; its existing buffered stop permits joined Close/Run exit and existing supervisor restart only if that chain is proven; no untested claim. C protocol remains unchanged from reviewed orphan branch. Separate overlay after frozen closed-prior code; same single active PC58. Independent adversarial review and focused regression required; no partial release.

PC58 output-loss draft review found two concrete adjacent races before freeze: queued source EOF fanout can saturate the worker event channel while the sole daemon Run loop waits for its serialized failure claim/Stop; and default pipe construction can read the retained old source before the replacement source is installed. Scope stays the failed-output path: consume obsolete EOFs while no replacement exists, drain them at the replacement boundary, and use the existing paused pipe constructor only for that recovery before installing/resuming the new source. Preserve ordinary EOF/manual Stop and normal output construction. Bounded regressions must prove claim/Close liveness, stale failure no-op and no old-source read across replacement; these are requirements, not completed results.

PC58 active-output-loss software result: real nonnil output failure now carries a worker output epoch. A serialized current-failure claim seals alias/admission, and existing Stop/inactive/logout/Close joins the failed owner; non-Zeroconf joins inline and signals the existing buffered Run exit. A stale/duplicate failure causes no owner or generation mutation. No automatic Play/retry. The worker drains only failed-output obsolete EOFs, preserves ordinary Stop, and uses its existing paused pipe constructor only for failed-output replacement so old PCM cannot leak before SetPrimary. Normal output construction and C protocol unchanged.

Final focused four-case player/daemon command (`TestPipeEPIPEFailureClaimFencesOldOwnerAndRejectsStaleEvent|TestCurrentOutputFailureRetiresOwnerBeforeFreshExplicitTransfer|TestStaleOutputFailureDoesNotRetireFreshOutput|TestNonZeroconfOutputFailureClosesItsOwnRunOwner`, count3) PASS in cached offline Linux builder: real EPIPE, 256 pending EOFs without command deadlock, no old-source reread, stale failure after new output, actual inactive HTTP/logout/join and fresh authenticated-owner FIFO after exact terminal proof, plus non-Zeroconf Close/exit. go vet player+daemon PASS; gofmt all five changed files empty. Final overlay265e7ba87df8b97ce7c47086f0da34b1914091bd78dd6aed43969cdbf5016f5d reverse-apply PASS; exact27-overlay Docker replay matches all106 changed tested files (Native tree d717de98b0a49546ba0dc3df8f6d4ae78564bd25). Earlier a8d hash superseded solely by deterministic EOF-test synchronization, with production bytes unchanged and affected focused check rerun. Planned combined build only, no release/install/home action or audible PASS. Installed field refusal remains unclassified until this diagnostic/correction batch is installed; these tests prove the bounded software defect and recovery, not that every generic ErrAliasConflict has the same cause.

Independent adversarial reviewer /root/voice_closure_fix gives final source GO for overlay265e7ba87df8b97ce7c47086f0da34b1914091bd78dd6aed43969cdbf5016f5d, including both resolved liveness/source races, epoch/current claim, conditional Stop drain, sealed commands, daemon teardown and non-Zeroconf Close. Reviewer verified exact final test blobs/reverse apply and reused reported focused/vet results. Lead wiring Docker/official CI/checksum and exact composition PASS. No remaining source blocker in this bounded slice; field/audio/Green installation remain open for the combined candidate.

**PC58 follow-on false transport admission decision — 22 October 2026.** User observes Spotify selected on the HomePod while pressing Play leaves the timeline stationary. Installed38 log already proves transfer failed at `SetPrimaryStream` with generic alias conflict, before a successful output load; it does not identify the exact custody predicate or confirm the later command was `resume`. In source, a failed output open leaves a prepared `primaryStream` while the player has no output. Daemon resume/pause/seek check only that stream, while worker Play/Pause/Seek currently return success with no output, allowing unperformed transport effects to be published. Hypothesis: fail closed at those three worker commands when no output exists; preserve NewStream's separate pre-output positioning, real paused-output transport and Stop cleanup. Regression: rejected output creation followed by actual dealer resume/pause/seek must return failure without state/position publication; a valid paused FIFO must seek, resume and pause normally, and output-loss/stale guards stay intact. No retry, forced output, timeout, semantic audio claim or change to prior overlays. Rollback removes only these three no-output checks; Green38 remains installed and exact field predicate/physical audio remain open.

**PC58 official C build compatibility — 22 October 2026.** PR63 pinned-stacks run39745466700 at commit2d44e31b50ddee8f958aed25937d7ed011ce7358 passed the Native fork job, including its patch stack and tests. OwnTone job119712683318 passed exact patch application, then failed compiling `src/outputs.o` before any C owner fixture: the runner's C headers do not define `ATOMIC_VAR_INIT`, so three historical START static atomic declarations fail as implicit calls/nonconstant initializers. These declarations came from `room-move-owntone.patch`, not the new PC58 retirement or transport changes. Correct only the three C static initializers in a separate last C overlay using their same constant values (`false`, `1`, `false`); retain atomic types, loads/stores, START ownership and every previous overlay. Validate the supported direct-initializer form, focused OwnTone compile and affected owner modes, then exact stack binding and independent review. The failed C job is a tooling/compiler compatibility blocker, not field playback evidence. Previously passing Native job and unrelated checks remain valid; no CI rerun, release or Green action has happened from this finding.

PC58 C build correction result22October: last C overlay `connect-atomic-init-owntone.patch` SHA256 `801e00044729d2b9a880ad3198d8c001feb9c7e019c6303ebef1b13ee8c55a9a` changes only the three static initializer spellings; atomic types, values and accesses are unchanged. Existing Docker and official C workflow apply the checksum-bound overlay after the closed-prior patch. Reverse application and exact forward byte match passed, as did local Clang C11/C17/C23 initializer checks and diff whitespace; local full OwnTone Linux compilation was unavailable because the Docker daemon was stopped. Independent source review GO is conditional on corrected official Linux compile and owner modes. Publisher run39745455453 completed successfully for the old 0.26.39 source, creating immutable 0.26.39 artifacts. The correction therefore advances the Speakers candidate to 0.26.40 while Control stays 0.10.19; no overwrite of 0.26.39, new official C pass, installation or physical audio proof is claimed.

PC58 no-output transport result22October: causal dealer resume after failed output-open was RED on prior27 source. Only three worker no-output branches now return errors for Play/Pause/Seek, so existing daemon state publication does not run; NewStream positioning, valid paused FIFO controls, Stop idempotence and existing output-loss/stale-owner flow remain. Focused actual dealer commands, real FIFO controls and prior failure neighbors GREEN; vet/gofmt, exact prior27 forward/reverse three-file byte match and manifest/Docker/CI binding PASS. Independent voice_closure_fix final GO on overlay28 SHA256d83009c9c16df5b3e88739564dd018b72801027cbea9316ad9fc638ddbf073da. This fixes a truthful-outcome defect, not output reopening or proven field cause. Speakers39 was published, but its official C build failed and it is not installed/audio-accepted; corrected Speakers40 remains a planned build.

Lead release-routing decision22October: user requires GitHub-plugin-only. Current plugin cannot dispatch workflow; browser is signed out and must not be used for GitHub. Preserve required exact-source/both-architecture prebuild before main advertisement by adding only the existing reviewed candidate branch codex/pc51-paused-track to publish push branches. The identical existing build/promotion jobs, path restrictions, package permissions, concurrency, immutable version and architecture/source labels remain. No fork/PR-target trigger, token access, automatic merge, CI waiver, new publisher or unknown publication replay. Independent source review before push; inspect existing runs/digests if outcome is unknown. Root owns combined release; this route starts prebuild only, not Green installation. Rollback removes only branch trigger/docs.
