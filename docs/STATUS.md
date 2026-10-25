## Active candidate52 — FIFO metadata ownership, 25 October 2026

Lead /root; mechanical composition /root/sol61_release75_review. The lead reports
three failed physical music attempts; admitted Native playback, FIFO/metadata
ownership and actual speaker audio remain separate claims. Baseline is published
Speakers0.26.51 / Control source0.10.21, commit4b9740bb1fcb8272ab00cc9415c0fead61b84591,
tree80a4e4e0c64c0d1cb292146a0405c1c5a62033ad. No successful sound acceptance is inferred.

Reviewed correction: metadata watches are created/read/retired on the existing pipe
input thread and bound to the exact setup generation and source identity; a late
stop for a reused DB id/path cannot retire its successor. Speakers0.26.52 adds one
reviewed C overlay after unchanged C20. Native159 production files, manager,
Control source0.10.21, authority, retries, timeouts, volume and route policy are unchanged.

Independent review74 source/composition GO covers the seven-path source preparation
receipt ea8d115c7054f2b7c2fcf0ade9c15cbfef33cdc371236fe7fd1c6c818b5fd7cd.
Its portability-only fixture adaptations preserve both existing Gcrypt initialization
calls and omit generated Makefile.in; existing autoreconf regenerates that file from
the reviewed Makefile.am flag. C21 production patch e42518bb and composed pipe.c
7d4ea19f match the source owner. The cumulative existing CI-only overlay is f6ae54ae;
Docker and official CI apply production C21 in the same position. Official CI invokes
--metadata-pipe-owner exactly once. Clean21-source/5-fixture replay, YAML and whitespace
checks pass. Existing ARM OwnTone/harness and actual metadata-FIFO successor results
are retained; no broad local gate was repeated.

The stale-retirement counterfactual is causal source evidence, not reproduction of
the field evbuffer crash or proof of HomePod sound. The separately reviewed private
Native→FIFO→C→AP2 RTP evidence is retained externally: packet emission at a private
peer, not decrypted PCM content, sub-poll device timing, provider behavior or physical
HomePod audibility. The large RTP Native fixture and its C observer are deliberately
excluded from52; no new Native fixture or production artifact is adopted.

This is the frozen local52 candidate for root-owned official CI. Publication,
installation and physical acceptance are pending. Root alone owns remote mutation
and delivery; no network/device action occurred during composition. Rollback removes
C21 and its metadata build/fixture wiring. All incumbent gates remain in force.

## Active field incident — ordinary Connect pause and volume observation, 24 October 2026

Lead: /root. Installed Speakers0.26.44, Control0.10.21 and PodVoice2.0.80. First ordinary Spotify Connect transfer at11:01:53 reached route_accepted at11:01:56, primed35%. Same-process passive C snapshots show selected Kitchen, session1/route1, base/effective35; no physical sound acceptance is recorded. User reports volume control disabling/re-enabling and displaying100%, plus a non-live timeline. A displayed100% is not evidence of physical100%.

At11:05:47 the shipped log records bound-pause-confirm409, followed by repeated ordinary player/pause500 until11:05:51 and source starvation. Root issued no pause/volume command. Initial request origin and exact rejected confirmation identity are unresolved. Source review identifies a legacy reconcile path retrying pause after Native paused/C playing disagreement; this is a causal lead, not yet an approved runtime fix. Control playback.v2 remains an observed Kitchen binding, while one exact-room volume read returns room_volume_unavailable, retry_allowed:false. No command was retried.

Candidate is NOT ready for the remaining three-cycle acceptance. Hold switches, further audio tests, volume writes and restarts. Preserve current process evidence. Separate owners: native_idle_owner analyses pause/confirm/reconcile chain; weather_backend_review analyses exact-room volume availability; PodView owns display/timeline and must not substitute generic/cloud100 for unavailable exact volume. Root integrates only a demonstrated owner correction, causal regression and independent adversarial review. No timeout, retry, guard bypass, alternate engine or speculative volume restoration. Next: correlate first purpose-bound pause receipt and prove the failing ordinary Connect identity boundary before implementation. Existing installed artifact remains unchanged; rollback boundary will be the narrowly reviewed successor diff. All three full acceptance cycles remain incomplete.

### Lead decision — combined46 restart and first-transfer candidate

Lead root; integration weather_backend_integration. The current field evidence is
installed45 FAIL, superseding the older44 attribution above without rewriting it.
Actual45 boot shows rejected volume/output reconciliation before the sole explicit
Spotify transfer. The requested track subsequently loads unpaused, then pipe autostart
and output selection fail. Private evidence remains in
/private/tmp/speakers45-green-boot-log.txt and
/private/tmp/pc45-first-transfer-failure-live-log.txt. Passive observations disagree
across the new C process and Native writer state; the target output has no selected
current owner. These observations do not prove audible success. Root issued no
additional Play, pause, volume write or restart after the sole Connect selection.

Full chain under review: add-on/s6 stop -> manager SIGTERM -> Native App.Close and
all original writer/decoder joins -> exact C termination -> supervisor restart ->
fresh Native transfer credit -> prepared initial stream bootstrap -> current C FIFO
reader -> bounded35% priming/current-room selection -> actual PCM/output observation.
Pair replacement must stop the captured Native owner before its paired C process and
must not let late old callbacks affect a replacement. Normal successful exit gains no
added wait. The manager's existing bounded Native12s+12s+2s cleanup must fit a55s
shutdown context, manager s6 timeout-kill60000ms and outer HA timeout70s. These stop
budgets do not change speech, media waits, policy, retries or normal playback timing.

Falsifiable hypothesis: source-owned first-transfer credit and initial prepared
stream bootstrap, plus completed writer joins before paired C teardown, remove the
proved pre-transfer reconcile writes and unsafe restart ordering. This is not proof
that they explain the inaccessible old C journal: FileEditor cannot read its DB;
the protected preupdate44 backup is metadata-only, so journal class stays UNKNOWN.
Strict kernel/PID namespace mismatch DENY remains mandatory; a source/harness
namespace finding must not be attributed to the field or used for admission bypass.
No journal deletion, unbound release, auto Play, new engine, scope widening, timeout
masking or new C runtime is authorized. Control0.10.21 and first32 stable Native
patches remain unchanged; any C20 change is its namespace-boundary fixture only.

Lead extends the same candidate dependency before the Native semantic identity edit:
source TrySeek currently swallows a later-page404 and moveStart can succeed on the
first cached entry, replacing the requested transferred track with an unrelated item.
This is a proved source contract defect, not an attribution of the Green45 failure:
inbound CurrentTrack identity was unavailable in that field trace. The assigned
Native owner will make the transfer seek strict and prove exact requested identity
before commit; a failed later-page fetch preserves the prior state. Existing cached
first-page/later404 causal fixtures must prove no unrelated first-entry fallback,
publication or old-owner teardown. No global fallback, refetch/reload or new endpoint
is authorized; this is the same fresh-transfer path and rollback boundary.

Source ownership: reviewed manager fresh-credit packet connect_recovery/room_transport;
manager ordered SIGTERM/pair teardown main/supervisor/proc by native_idle_owner;
Native prepared-stream bootstrap by C resume owner; Native App.Close JOIN by release75.
Integration owns one cumulative unpublished Native overlay, exact checksums/Docker/CI
composition only after final independent source GOs, plus this version/stop metadata.
Regression plan uses existing manager focused tests, real Native harness shutdown and
first-stream causal cases, strict namespace opposite cases and exact overlay replay/
composition checks. Reuse unchanged passed source gates; broad official CI only on
one frozen combined candidate. Rollback is this isolated successor diff, never DB or
owner-marker reset. No46 version publication/installation or physical acceptance is
claimed; root alone owns eventual combined install/readback and a bounded field test.


Actual bounded integration result: reviewed manager shutdown packet37cc5949 is
adopted with all three source hashes exact. The actual SIGTERM path closes admission
and respawn, signals the captured Native process, joins its sole keepAliveWait while
C stays alive, then signals/joins C; pair replacement uses the same ordering and
barrier. The55s forced-exit boundary reports UNKNOWN; Pdeathsig and the C namespace
fence remain unchanged. Baseline real-child SIGKILL causal RED is retained; final
local race, actual Linux process cases, held-old-C respawn barrier, existing removeRoom,
vet and formatting are reported GREEN by the source owner and independently reviewed.
Final source composition: independently reviewed Native shutdown a0b80 and prepared
START/strict-seek3772 are one Native33 overlay after the unchanged32. Clean replay
matches all285 composed source files; all33 checksums and changed Go formatting pass.
The manager now accepts only an exact current positive OWNED START fact and atomic
receipt, seeds that already-primed bridge state without selection/recoveryprime, and
suppresses six generic write groups while preserving observations/manual/attention.
C absence is never inferred as positive ownership; fresh credit keeps its existing
atomic C recovery path. Production source review is GO. Fourteen focused cases,
seven actual bridge modes (final all-non-GET observer8.754s), and four existing
no-credit modes pass; the strengthened observer includes Native pause/resume/volume.
C20 production remains unchanged; only the existing CI fixture adds modeled prior-boot
admission and same-boot changed-namespace denial. This is not actual reboot or current
field-journal evidence. Control21 stays unchanged. Exact45 baseline, Native overlay,
Docker/CI wiring and55/60/70s stop dependency were reviewed together. Official final
CI/publication/installation and a bounded physical acceptance series remain pending;
no provider, C/AP2 output or acoustic success is claimed. All earlier candidate
partial/pending notes are superseded by this source result, not by field acceptance.


Lead continuation — exact-transfer47, planned build. Delivered46 is now the
software baseline: merge261c31ddbf0f099b8f5b5e47eff8a996ba790b6c,
source treec8c2a042f5265856036186bc9c8e539d612c9713. Official PR/main CI and
publisher passed; ARM64 source identity and Green46 RUNNING were read back by root.
Startup showed no generic writes and initial revision state. These results do not
establish audible or full lifecycle acceptance.

The first paused transfer selected a different output through root UI error; no Play
was issued. The observed request was rejected before START by a later radio-page404.
Two Spotify auto-requests followed one click; this does not prove two human actions or
classify the C journal. Private field receipts remain root-owned. The remaining
station/context eligibility and exact transfer identity are the next causal boundary.

Affected chain: Spotify supplied current track/context -> Native transfer parse ->
strict lazy context seek -> exact supplied-track eligibility -> existing prepared START
and writer/current-owner fences -> manager receipt -> C route/output. Hypothesis:
when upstream pagination is unavailable but the transfer already supplies a valid exact
current-track identity, a narrowly eligible supplied-track fallback can preserve that
identity without selecting an unrelated cached item. The source owner must define and
prove eligibility; this planned hypothesis is not an implemented or field-proven fix.
Failure must preserve prior state. No broad context fallback, automatic Play, manager/C/
Control/PodView/UI changes, journal reset, permission widening or new engine.

Native owner PC13 owns the bounded upstream source change and causal requested-track /
lazy404 / malformed-or-ineligible / stale-owner regressions; independent74 review is
required. Integration prepares Speakers47 metadata and one Native34 overlay after
unchanged33 only once the immutable reviewed packet arrives. Reuse unchanged46 checks;
run affected source/composition/static checks, then root owns one final official CI and
publication/install flow. Rollback boundary is this isolated successor diff. Final source result: independent74 SOURCE/PIN/EVIDENCE GO covers Native34 patch
142870ef and receiptc9deb128. Exact7 base/tip pins, clean delivered46 apply, actual
seven-file delta, unchanged first33 checksums, all34 checksum values and changed Go
formatting/whitespace pass. Source-owner tests cover actual paused and playing dealer
transfer through existing prepared START/FIFO (9.081s), affected tracks race (1.020s),
queue/shuffle (5.085s), with prior affected daemon17.536s retained. Upstream artifact
and all seven evidence logs were independently verified. Supplied exact-track handling
does not establish station continuation: later radio/context continuation remains
incomplete and is not claimed fixed. Manager/C/Control/PV remain unchanged. Candidate
is frozen source pending official CI/publication/installation and bounded physical
acceptance; no provider/device actions by integration or audible success claimed.


Lead continuation — bootstrap predecessor49, planned build. Speakers48 is published
for both architectures and installed on Green according to root readback; exact merge
cef68d2b4d9137ed2905d4873ec84b5d3ba01a4a/treebbd8cf05b342adcf2fdf1d45fee20f4406d1cc61
is the unchanged baseline. A subsequent correctly targeted paused transfer passes the
prior radio-page boundary but receives C prepare409. This does not establish output
or lifecycle success and does not change the unknown historical journal classification.

Affected chain: supplied transfer -> Native initial prepared stream -> predecessor
request identity -> existing C prepare/commit owner checks -> writer/FIFO/current-route
receipt. Source owner PC13 is investigating the proven bootstrap self-predecessor
request boundary; C owner independently reviews the request contract and real-C causal
regression. The minimal hypothesis is a Native request correction, not relaxed C
admission. Exact successor/current-owner, paused transfer and negative stale/foreign
cases must remain fenced. No Control, manager, C runtime, UI, permission, timeout or
journal changes are planned. Integration owns only the reviewed Native overlay and
existing Docker/checksum/CI binding plus49 release metadata once the frozen GO packet
arrives. No speculative runtime edit, freeze, publication or device action is allowed.
Final independent Native+C SOURCE/PIN/EVIDENCE GO covers f613d0c9 and C fixture
5eff69bb. Unchanged C20 HTTP admission reproduces ready=true with semantic self-
predecessor409 versus absent physical predecessor200; live custody, UNKNOWN, pending
writer and stale process still return409. Native fresh bootstrap proof requires
initial generation, no opened owner and no START/MOVE/unknown/pending/permit/queue
custody, then omits only the private C predecessor. Semantic session/alias/source
revalidation and late-generation CAS remain unchanged. Offline cached Linux
TestPreparedDealer race passes21.326s, including paused/playing FIFO bootstrap,
default predecessor preservation, eight extant-owner negatives and late generation.

Integration folds this exact two-file delta into existing Native34 (ae6fa242), cleanly
replayed from33 to all final source bytes; first33 stay exact. Existing Docker/CI path
is unchanged; all34 checksums, affected formatting and whitespace pass. Existing C CI
overlay permanently covers the HTTP regression; clean replay preserves all229 runtime
C/H files. Manager/Control/PV are unchanged. Prior passed checks remain evidence for
unchanged bytes. Candidate source is frozen; root owns final official CI, publication,
install/readback and bounded physical acceptance. Station continuation remains open;
no live audio, persisted field-journal recovery or general lifecycle success claimed.


Lead continuation — final custody50, planned build, NOT accepted. Speakers49 is
installed RUNNING, exact mergebd436182cf261e4bacbaef4b842b4e0a5c69165f /
tree6edfb8425a03a74ab28f806966790a1c1da1f81c. The field flow reaches C complete then
fails final custody with owner_changed; the result remains UNKNOWN. Private evidence:
/private/tmp/pc49-first-transfer-live-log.txt and
/private/tmp/pc49-after-transfer-state.txt. No additional Play, retry, reset or guard
bypass is authorized; startup/RUNNING and C complete do not prove final ownership,
audible playback or lifecycle acceptance.

Affected chain: transfer -> prepared stream/current writer -> C complete -> existing
Native final custody verification -> manager observation. A normal paused Native+C
composite passes; the current playing fixture SIGSEGV is a fixture repair in progress,
not product failure/success evidence. Normal actual-C playing proof and a frozen
independently reviewed source packet are required before freeze. The authorized scope
is finite diagnostics at existing final verification and a separately proven transient
buffering-position timestamp correction. That separate timestamp bug is not an audio
fix or explanation of field owner_changed. No ownership guard, timeout, C runtime,
manager, Control or UI change; UNKNOWN must remain closed.

PC13 source owner supplies causal finite-verifier/position regressions and actual-C
playing proof; independent review covers changed bits and adjacent stale/current-owner
boundaries. Integration owns this one existing STATUS continuation, version50 metadata
and composition into the existing cumulative Native34 path after final GO. First33 and
all unchanged runtime evidence are retained. No new application path, framework,
publication or installation during prep. Root owns final official CI and delivery;
physical acceptance remains open. Final source5-file patch2b4bd889 has independent
SOURCE/PIN GO. The separate public-position regression retains RED10808489ms versus
raw8487ms and GREEN8487ms with fresh timestamp; final verifier raw-position behavior
is unchanged. Affected daemon race passes1.036s and three existing player verifier
race cases pass1.052s. All five files pass formatting and exact49 patch checks.
Normal actual49 Native+C paused/playing/EOF cases pass controlled protocol checks;
the earlier playing SIGSEGV was corrected only in the existing fixture initialization.
This does not explain the field UNKNOWN or prove audio. Existing C CI fixture retains
that real protocol reproducer with no C runtime/workflow change.

Integration folds the exact reviewed5-file Native delta into existing cumulative34;
first33 and Docker/CI application path remain unchanged. Native and composed fixture
clean replays/formatting pass. Candidate source is frozen for official PR CI; merge
remains explicitly held until the rebuilt50 Native binary passes all three actual-C
cases and independent final packaging review. Publication/install and physical
acceptance remain pending. Root may separately require one kernel reboot through the
existing prior-boot guard after idle checks; this is not DB reset or integration
permission to perform it.


## Active lead decision — preserve paused Native ownership and reject unsafe release, 24 October 2026

Lead: /root; implementation: weather_budget. Direct source evidence: releaseHomePod ignores empty-output PUT failure then writes released; idle and manual release report unconditional success. Field released=true with selected=true is not physical release proof. Earlier acknowledgement-only draft is withdrawn: even atomic current-owner/marker commit cannot stop an unbound output PUT deselecting a successor during the request.

Causal chain: ordinary Connect/pause retains Native writer, decoder context and position → manager idle reconcile must not mutate physical custody → real Native owner replacement uses existing prepare → writer close/join → complete → exact C terminal retirement. Manual Release has no successor-bound Native facade today. Invariants: one physical owner; no unbound outputs[], Stop or Close substituted for owner retirement; no UNKNOWN replay or new endpoints/engine/worker recreation. Non-goals: C/native changes, historical marker recovery/admission changes, route guards, provider/audio effects or installation.

Root approves smallest three-caller correction: remove idle physical release and false success; existing configured manual release route returns typed HTTP409 release_unsupported before pause/effects; removeRoom drops global deselect but preserves its existing exact-runtime teardown. Legacy released flags remain legacy compatibility state and MUST NOT be read as physical proof; aggregate Native/C observations own actual output truth. User-visible consequence: pause/idle keeps the current Native context for normal resume; automatic grace release no longer runs; manual Release is temporarily unavailable. Existing ordinary pause/Stop controls remain unchanged. Actual sender replacement and Native/C terminal retirement are a separate combined owner change.

Focused causal plan: real HTTP handler refusal before any outbound request, delayed/successor request cannot pause/deselect/write marker or change live owner context; exact runtime removal still stops only captured runtime without empty-output PUT; static actual idle source forbids release calls and marker creation. Gofmt and affected Go regressions only, independent final source review required. Source-only, no version/release/install/physical acceptance claimed; rollback this manager/test diff only.

Actual source result: the unbound releaseHomePod helper and all three effect call sites are gone. /api/release is preserved exactly and returns typed409 before any pause or outbound request. Idle no longer runs grace physical release or writes a marker; ordinary paused Native state remains owned. removeRoom retains its pre-existing captured runtime stop/join/kill but sends no global output mutation. Historical released-file recovery/admission behavior and status fields are unchanged as explicitly directed by root; no legacy flag is physical release evidence.

Focused Go tests PASS with fixed Go1.22/cache and external linking: actual handler preserves paused owner and delayed successor with zero HTTP effects/marker/runtime stops; method405 unchanged; removable alias teardown targets only its captured runtime and has zero HTTP; AST idle safety; existing TestTransState regressions PASS. Gofmt and diff whitespace PASS. One initial alias-removal fixture incorrectly used nonremovable primary r0; corrected to actual removable alias and reran the invalidated focused cohort. Independent revised-manager source GO covers exact main1ed8971175006b9f3dc4a921de8159b06e5182845feb143ec210235b79b2d25e, supervisor2f9b62f23f09ce8f67bec9787cdb972f24e5e87fffc82f994eb50cd6bcced537 and testef352da979424ffa6a1191df87bb1fb3b4225b56a38fe363846088077a525941. Earlier ACK-only draft remains withdrawn. No release/install/provider/household/audio action or physical proof; combined Native/C retirement delivery remains root-owned.


## Combined integration — 24 October 2026

Lead/root has integrated one planned build, Speakers0.26.44 and Control0.10.21, onto exact remote main36b77e894f8a740fed679c876a4061c5653477e3/tree12995f16f4d34a996ee7e5c4d8bb6d6f1af37c9f. The current Control20 playlist services, publisher and Native32 engine stay unchanged. The combined diff contains only C19 closed-owner retirement and its existing fixtures/wiring, the optional Control v2 passive producer, the narrow manager idle/manual-release correction, versions/changelog and these existing decision posts. The untracked release_ack_test.go is part of the frozen source and must be exported with the manager changes.

Independent source GO is complete: weather_backend_review approved C production/fixtures41b05f55/edf0b048 and final Control patch7325181b02e0cbcaa6ad419cfa7e172b4daf751d9985871491bafe292eee7255, including the corrected awaited-error fallback, and manager main1ed89711/supervisor2f9b62f2/testef352da9. sol61_release75_review approved exact C Docker/CI wiring. The official C job explicitly executes --start-retire-normal-reconnect-wire after applying the checksum-bound CI-only fixture overlay. Production Docker and official CI apply the identical19-overlay C source stack; no CI fixture enters the runtime image.

Retained checks cover the same reviewed bits: exact C final make-check3/3, same-process protectedA retirement→normalB route after simulated8h transient expiry, zero output before prime with autoselect enabled, late old cancel/join and existing >5s actual FIFO watcher/route cases; pinned HA20 affected room cases plus two v1 neighbors and two corrected held-error/expiry cases; narrow manager real-handler/alias-removal/AST idle and transport-state tests. Reuse these successful source checks without a new broad local gate. Final composition statics verify changed Python formatting/lint with the documented existing observations TRY004 baseline exception, Go formatting, YAML/shell, exact patch/checksum/source pins and unchanged runtime scope. Fixed Ruff0.16.2 exposed five legacy-formatting hunks in the touched observations.py; a separate formatter-only delta changes its hash35d6a3d5→8ea3e1bc with identical parsed AST (excluding source locations). The earlier reviewed Control semantics and executed regressions remain unchanged. weather_backend_review independently confirmed the identical AST and five layout-only hunks and approved the format-only delta; no source regression was invalidated. No full integration mypy PASS is claimed; Control retains its existing baseline diagnostics.

The manager now truthfully preserves live paused ownership instead of sending an unbound idle release; manual Release is explicitly unsupported before effects, rather than falsely acknowledged. Exact Native owner replacement still uses prepare→writer close/join→complete→C retirement. This candidate does not create a new idle API, close a live authenticated session, relax command claims, replay UNKNOWN outcomes or confer playback authority through passive metadata. Coupled consumer/UI contract and final exact-tree composition review remain separate from these source GOs. Official final CI, immutable Speakers images, matching Control release/tag, root-only installation/version readback and coordinated external Spotify→HomePod sound plus UI acceptance remain pending. The earlier simple audible attempt is physical partial evidence, not acceptance of all music flows or attribution of the08:35 failure. No publication, installation, provider or household action was performed by the packaging owner.

## Active lead decision — Control canonical passive playback/output observation ·24October2026

Lead/root; isolated Control owner sol61_review74_final; independent review remains required. Current Control0.10.20 get_playback_snapshot truth is cached account Spotify metadata only; a shared Spotify device can represent several native room aliases, so configured target/default-room inference can misjoin actual output. No new field/audio probe was run. Root authorizes optional include_output:true with a closed podconnect.playback.v2 aggregate, preserving default closed8-key v1 exactly.

Chain: unchanged successful cached account poll → current actual Native selected alias and exactly one selected C output → existing passive room-transport Native/C fact join → same Control owner/poll/body/deadline recheck → output observation only. Separate command offers remain actions-strict. Invariants: no provider poll, prepare/commit/mutation, default-room/name inference, new engine/endpoint or playback authority; actionless valid facts remain observable; failed provider source keeps its existing error DTO; fresh no-active playback has unavailable route, never a physical release claim. Binding SHA64 describes stable Control owner plus complete Native/C route identity, excluding account polling cadence/progress; freshness is separately capped by original account TTL and one existing5s Speakers observation budget captured before awaits, with monotonic checks. Owner/poll/body change fails closed without mixed publication; output-source mismatch is owner_changed. Plan focused existing HA fixture tests for exact v1/v2, alias selection, ambiguous/absent output, actionless facts versus command rejection, route identity changes, held awaits/account replacement, stale/idle/error, stable binding and zero writes/provider polls. Only Control source/tests/service descriptor and this post change; rollback removes optional aggregate only. No version/release/Green/provider/audio acceptance is claimed.


Actual isolated result: the optional service field produces exact9-key playback.v2 only when requested; its eight existing metadata fields retain the original captured projection. Default and explicit-false v1 issue no Speakers reads. One shared passive fact validator admits valid actionless Native/C state, while the existing command read still requires actions. Actual current Native alias room plus one selected C output are joined by the existing manager proof, with no configured-room/default/name inference. The stable binding SHA excludes poll/progress; original owner/poll/body and account/5s observation deadlines fence publication after every await. Idle/error/ambiguous/unavailable paths publish no output binding and no physical-release claim.

Focused pinned HA2026.8.2 inert-loopback evidence: all20 affected room-transport cases pass, including9 new v2/source/deadline cases and11 retained command neighbors; two original v1 playback-source cases pass. Restoring only the old action-required predicate in an isolated copy fails the actionless observation case (causal RED). An initial test placement/load_tests selector attempt ran zero cases and is not PASS; only the corrected executed results count. Formatter/lint on changed code, compile and whitespace checks pass; observations.py retains its pre-existing unrelated TRY004 invalid-queue ValueError under a CLI-only baseline exception, with no configuration or runtime change. Source scope is Control observation/room-transport/service descriptor, its existing HA fixture and this active post only. Optional services.yaml change must apply as a hunk onto actual remote Control20, preserving its newer user-playlists service. Independent adversarial source/final-pin review and lead integration remain pending. No live provider/HA/Green/audio/version/release work occurred; no field/physical acceptance is inherited.

Independent review HOLD: a Speakers error/timeout after an awaited passive read could take the unavailable-output fallback without rechecking the original account owner/poll/body/deadline. That could publish the older otherwise-valid metadata after replacement or expiry. Root unfreezes only this resolver error boundary and its held-await regression: route errors must run the same current-source guard before any v2 fallback; changed/unavailable account source returns the existing error DTO. Unchanged current source may still report route_unavailable. Preserve all earlier executed checks on unchanged paths; add held actual Speakers-error/account replacement and held TimeoutError/source-expiry negatives before refreshed source review.

Review correction result: both new held-error cases reproduce old ok:true playback.v2 metadata publication on pre-fix source (RED). The resolver now runs its original current-source/deadline guard on both generic passive failures and converted room-read failures before returning an unavailable-output aggregate; an unavailable/replaced/expired account source returns the existing unavailable error DTO. Unchanged valid source plus route error still reports route_unavailable. The actual inert HA Speakers500/account-runtime replacement and held TimeoutError/source-expiry cases both pass on the corrected function; all prior22 relevant successful checks are retained on unchanged paths. The first corrected-source attempt used an invalid bare-object runtime replacement; the fixture now copies a valid current runtime object, and no production change was made from that fixture error. Updated source/pins await independent changed-boundary review; earlier source packet is superseded for this function/tests/post only.




## Active lead decision — repeated ordinary Connect after physical first sound, 24 October

Lead/root authorizes isolated source/regression owner sol61_connect_c_resume; independent adversarial review precedes any release. Installed Speakers43 physically produced sound in one simple user trial last night. Its saved trace then shows a generic pause, idle release and transfer away; this morning a new authenticated Connect transfer loads playing PCM, but actual pipe autostart fails and distinguished Connect volume prime returns terminal transport_busy/HTTP500, so no route is committed. This is a failed repeated-transfer chain, not passive metadata or complete physical acceptance. Later passive state is the same C process with a removed session and selected flag; that later snapshot is not atomic with the failure and cannot prove its owner.

Whole affected chain: authenticated Native transfer/actual FIFO writer → C watcher/by-ID autostart → input and START/journal guard → manager volume-prime35/selected route → AP2 session; prior pause/reader closure/idle deselection/transfer-away and delayed callbacks must not retain stale authority or release a newer owner. Falsifiable hypothesis is an ordinary pause/EOF/release lifecycle retains one C ownership guard across a genuinely new writer; exact guard is currently UNKNOWN. Preserve every busy, journal, process/FIFO, context-join and current-owner refusal. No retry, timeout, startup bypass, journal deletion, unconditional cleanup or automatic device action is authorized.

Reuse existing actual AP2/FIFO owner fixture for one bounded ordinary success→pause→release/stop→new writer/autostart/prime/selection regression, and compare the prior protected START terminal path. Pin composed C18 production bytes before any edit. A new guard exception is not justified unless that test proves a specific lifecycle defect and retirement can be exact. No current endpoint exposes all six internal busy predicates; the old aggregate log cannot retrospectively identify one. A content-free diagnostic at the existing rejection site is a separate bounded proposal if the causal regression remains green. Rollback is only a subsequently reviewed new overlay; current43, Control and frozen PV79 display patch remain unchanged. No live Home Assistant/provider/device/audio calls; installation and physical acceptance remain parent-owned and open.


Bounded causal result: ordinary exact C18 watcher/AP2 fixture reaches normal playing route, then actual public Pause→empty selected-set→Stop with busy0/gate0/source_owned0/undrained0. The attempted second ordinary writer enters actual autostart and prime successfully but aborts in an unclassified metadata-worker/evbuffer observation; it is not evidence of the field transport_busy and triggers no runtime fix. Preserve that failed investigation log and fixture separately; no further worker/framework expansion.

Protected predecessor causal RED: existing mode18 constructs an actual completed protected START, performs exact retirement prepare→real writer close→complete with reader epoch advance, drained AP2 context and canonical staged fresh reader, then opens a real new FIFO writer. Actual playback_start_id returns-1 with gate1/start_active1/source_owned0 and Connect prime status-1; required normal reconnect assertion exits134. This proves a same-process terminal-slot handoff defect independently of the still-unclassified field predecessor. Manager independently also falsely marks idle release after a rejected empty selected-set write; another owner handles truthful acknowledgments. The field's last-night entrypoint remains unknown, so this is not yet causal attribution to the household trace.

Lead approves the bounded C correction after terminal FULL save, exact closed old writer, joined old reader/AP requests and current canonical staged reader: revoke only the exact predecessor selected routing under saved device incarnation/route and closed gate, then detach the old active/scope/deadline references while retaining historical slot/token/result and durable journal tombstone. Any foreign/new selected route, UNKNOWN/PENDING, failed persistence or unjoined writer remains fenced, and failed detachment withdraws staged generic-admission readiness. Never add a selected flag or allow new room PCM to activate the old target. Delayed old cancellation/result reads must not affect a later ordinary or protected successor. Existing dynamic retirement results must not borrow global successor stage/gate/reader as a new historical witness. Native actor retirement and manager truthful release are separate owner boundaries of the same planned candidate; C detachment alone is not a complete idle-release/Resume solution. No production bytes changed before this decision.


Final C source result (PC58 successor, not released): exact old known-terminal FULL retirement now retains its historical slot/token/journal but revokes only its proved predecessor selected route/custody and detaches runtime START/scope/timer references after real closed-writer, joined reader/AP and canonical staged-reader proof. The saved live predecessor transport route and post-session-removal selection revision are private in-memory retirement witnesses, not a new API or durable authority. Foreign selected/session/route/custody/attention, reopened real writer, UNKNOWN/PENDING and persistence failures remain fenced and withdraw stage readiness. The no-runtime-slot path rejects stale selected/session/foreign resources without guessing reboot ownership. Retired normal reader admission skips both output start and autoselect; only the current manager prime/selection creates a new route. All ordinary nonretired input behavior is preserved.

Actual causal RED on unchanged43: completed protected START→exact prepare/realwriterclose/join/complete→staged terminal reader→new normal public entry fails with retained active slot and terminal busy. Final same-process regression passes after controlled eight-hour expiry of old transient deadlines: old query reports its original expired UNKNOWN/notok without changing current C state; new normal public by-ID starts only the fresh source, deterministic pre-prime barrier sees zero AP session/selected flags/receiver peer/UDP with autoselect enabled, then prime35/selection connects one real private AP2 room B. The old room A stays unselected with no session; late old cancel/complete/result cannot change B gate/session/epoch/route/input generation or acquire a successor retirement witness. Existing protected successor regression also verifies late old cancel/join cannot change the new protected route. Existing >5.2s zeroPCM watcher→EV_READ→by-ID/prime/selection case passes; seven neighboring START/MOVE closed-writer/expiry cases pass. Final sole existing make-check passes3/3 on exact frozen production in cached Linux with network disabled. Source2+fixture2 patches apply exactly against C18; whitespace validation passes. Independent adversarial reviewer weather_backend_review gives exact final SOURCE+fixture GO; integration/release/field acceptance excluded.

Frozen production overlay connect-retired-owner-detach-owntone.patch SHA25641b05f552f8ef01c88b6e53cf9cdb861465d2dbb5dca4e60453413264c5b7bb6; CI fixture overlay SHA256edf0b0480ebb68be0c534592c57689361ba76aa5130437383e5708f269abbebc. C18 manifest0f8796b66eca13e5706f627e0e9df3661477853569e40d9b608eacce4866b4fa → C19 manifest6c745d3fe66ab7023dc059ff0f050936c7dbed65e4b72c2d06f06bc86201226e. Exact packet and logs: /private/tmp/pc44-reconnect-owner-20261024/retired-owner-detach-receipt.json. Root owns combined Native/manager/C composition, official CI and one coordinated external Spotify→actual HomePod sound plus UI physical series on the same installed process. Current43 evidence is preserved, predecessor field classification remains open, and this C software result alone does not prove all music flows or truthful idle release.


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

PC58 second official C gate result22October: corrected run39746912157 again passed Native, but OwnTone job119717553598 reached `make check` and failed compiling `tests/airplay_callback_owner.c`: the fixture undefines `db_speaker_save` before including `player.c`, while the guarded `db.h` declaration had already been included under its inert fixture name. This leaves `player.c:4220` with an undeclared `db_speaker_save`; no C owner mode ran in that official job. The checksum-bound fixture-only patch in `.github/fixtures/` moves that undef immediately after the `player.c` include and is applied by CI after all production overlays. It changes no OwnTone production source, Dockerfile, addon version or already published 0.26.40 runtime image; publisher39746906565 completed separately. In the existing cached offline Linux C builder, the previously failing `airplay_callback_owner` target compiled and five affected queue/closed-prior owner modes exited successfully after only this fixture patch. This is local focused evidence; corrected official CI, installation and audible playback remain open.

PC58 third official C gate result22October: run39748185495 at f963c14df2317f5093a512e2c461cc7e33845128 passed Native, applied and compiled the OwnTone stack, then failed `make check` in the default `airplay_callback_owner` mode11. Its real AP constructor logged generic “Out of memory for verification setup context” before pair setup 1 and the fixture hit its existing five-second owner deadline; this log alone does not establish allocation exhaustion or a product timeout defect. The fixture called `gcry_check_version` but omitted production `main.c`'s `GCRYCTL_DISABLE_SECMEM` and `GCRYCTL_INITIALIZATION_FINISHED`; `pair_setup_new` checks the finished predicate before constructing the HomeKit client. A standalone probe in the existing Debian C builder confirmed that `gcry_check_version` alone leaves that predicate false. The external fixture-only patch now performs the two production initialization calls, with no runtime/image/version change. In the cached offline C builder the exact `make check` suite passed 3/3, including default `airplay_callback_owner`; a subsequent focused default invocation passed mode11 and the full fixture. One earlier focused invocation exited134 without a preserved diagnostic log, so its cause remains unknown and cannot be counted as a product finding or erased by the passing run. Official Ubuntu CI, Green installation and audible playback remain open.

PC58 no-output transport result22October: causal dealer resume after failed output-open was RED on prior27 source. Only three worker no-output branches now return errors for Play/Pause/Seek, so existing daemon state publication does not run; NewStream positioning, valid paused FIFO controls, Stop idempotence and existing output-loss/stale-owner flow remain. Focused actual dealer commands, real FIFO controls and prior failure neighbors GREEN; vet/gofmt, exact prior27 forward/reverse three-file byte match and manifest/Docker/CI binding PASS. Independent voice_closure_fix final GO on overlay28 SHA256d83009c9c16df5b3e88739564dd018b72801027cbea9316ad9fc638ddbf073da. This fixes a truthful-outcome defect, not output reopening or proven field cause. Speakers39 was published, but its official C build failed and it is not installed/audio-accepted; corrected Speakers40 remains a planned build.

Lead release-routing decision22October: user requires GitHub-plugin-only. Current plugin cannot dispatch workflow; browser is signed out and must not be used for GitHub. Preserve required exact-source/both-architecture prebuild before main advertisement by adding only the existing reviewed candidate branch codex/pc51-paused-track to publish push branches. The identical existing build/promotion jobs, path restrictions, package permissions, concurrency, immutable version and architecture/source labels remain. No fork/PR-target trigger, token access, automatic merge, CI waiver, new publisher or unknown publication replay. Independent source review before push; inspect existing runs/digests if outcome is unknown. Root owns combined release; this route starts prebuild only, not Green installation. Rollback removes only branch trigger/docs.


**PC58 main-gate token-fixture decision — 22 October 2026.** Lead failover root; bounded fixture owner fast_ai61; independent registry_review61 and v2.X reads agree. Main211d7384d92b5799772ec26db33641ef8ae26709 has exact reviewed tree5482041c37192b40e3509f83eb88da46255efc25 and unchanged production subtreefab69dfb1e8529fa283494b7bb09207f0c0f2141. Official run39757113594 OwnTone job119751807356 passed make check3/3, then mode27 `--start-retire-closed-no-session-wire` aborted at start35_wire_owner.inc.c:557: the negative request assigns first token digit `f`, which is a no-op for a legitimate random token already starting with `f`. Actual start_find uses exact token equality; accepting that unchanged valid request is not evidence of a product owner violation. Change only the checksum-bound CI fixture overlay to choose a guaranteed different valid hex digit and explicitly assert inequality before retaining the existing refusal assertion. All production guards, modes, deadlines and existing negative cases remain. The earlier focused exit134 without diagnostic remains UNKNOWN; the newly identified mechanism is compatible but does not retrospectively prove its cause. Existing cached Linux builder is currently unavailable because its Docker socket is absent; no daemon or new builder will be started. Deterministic all16-digit mutation checks, exact patch application and independent source review precede the required new official C execution; installation remains blocked until that gate passes. No version, runtime image, provider or Green action is included.

## Active lead decision — PC58 advancing Spotify / silent HomePod, 23 October 2026

Lead /root. Green app info directly confirms installed/running Speakers0.26.40; PodVoice2.0.74 git3f50a1c/buildb44aa593 remains unchanged. User reports PodView Play followed by Spotify position advancing without sound, then mobile Connect hangs. This is physical QA FAIL, not the earlier stalled-timeline acceptance case. Source authority is PC63 merged211d7384 production subtreefab69dfb1e8529fa283494b7bb09207f0c0f2141, identical to current local650e87b production subtree; PC64 changes fixtures only. Exact running binary/image digest is not yet independently read.

Existing Green log retained privately at /private/tmp/pc58-green40-20261023-speakers-log-ax.txt:12:07:16 source starvation and transfer away;12:07:25 authenticated mobile transfer accepts alias1 and loads unpaused track, but OwnTone pipe autostart fails. Manager Connect volume prime receives C transport_busy/500 and route_not_committed. Three native health failures cause automatic native restart12:08:41. No root playback, restart, reclaim or replay was triggered. Later passive rooms read shows r0 playing=true/volume35/released=true; this is Spotify state, not PCM or audible proof.

Chain under investigation: authenticated Spotify transfer → exact alias/native owner → FIFO writer/watch/autostart → C route/volume custody → HomePod output → native commands/retirement → reconnect. Hypothesis to falsify: full FIFO with live watcher but no consuming source blocks native writer while holding output mutex, preventing Pause/Close and subsequent API health replies. Separate unresolved boundary: which C ownership condition denies autostart/recovery. Invariants: exact current owner/generation, no concurrent writer, no reclaim on UNKNOWN, no stale cleanup of newer owner, truthful playback result. Non-goals: UI redesign, lowering timeouts, provider/VAD/prompt changes, automatic retries or speculative rollback to17.

Next: reuse current composed pipe regression to reproduce blocked write and command liveness; inspect C START/transport guards one step before/after failure. Minimal proven fix only, with independent adversarial owner/race review, focused current native+C integration and official candidate CI. Rollback is bounded new patch; no release/test-ready claim before these results. Earlier audible17 is reference evidence, not proof that downgrade is safe with current contracts. Physical audio remains failed/open.

Independent liveness result: exact current40 28-overlay composition reproduces full named FIFO with live reader/no consumption: actual outputLoop blocks in syscall.Write while holding output mutex; Pause and Close return only after controlled external drain. Four existing Close/start-fence cases still pass. Receipt /private/tmp/pc58-pipe-liveness-20261023/red.log + composition.json. This is a deterministic Darwin standard-library adapter using exact composed driver/helper bytes, not a Linux/native/codec suite or original-field-cause proof. Review agent sol61_review74_final owns immutable RED; implementation sol61_release75_review owns isolated native correction. Proposed smallest direction: interruptible pollable FIFO write with explicit control cancellation and real writer join, retaining partial bytes/fence/current owner and avoiding generic busy bypass. Initial silent START remains separately unresolved; watchdog liveness correction cannot be labelled a complete audio fix. C500 confirms failure outside typed clean temporary-busy409; generic autostart must remain denied while custody is unresolved. GitHub58 is In Progress / physical QA failed, retaining earlier software receipts.

Actual Linux reproduction now confirmed: cached glfork image Go1.25.11, network disabled, exact28-overlay40 native source and same focused regression, no adapter. Both Pause/Close block on actual FIFO syscall.Write until external drain; four existing neighboring cases PASS. Exit1 preserved at /private/tmp/pc58-pipe-liveness-20261023/red-linux.log. This supersedes the Darwin-only execution limitation for this narrow mechanism. No physical/audio PASS inferred. Passive HA snapshot confirms current Spotify target is the user's Mac after transfer-away; exact HomePod room controls correctly return target_not_current, so current route state cannot retrospectively prove the failed owner's state.

C adjacent-chain control: existing mode18 extended privately through actual open-gate FIFO/timer starvation → input-thread buffer_full callback → existing synchronous player command dispatch/bottom-half → PLAY_PLAYING. Exact owner/source/FIFO/route continuity passes; no manual resume invocation, fake callback or production change. Exact261-file40 source binding e5eb44d9882801ef1644df19f1f0d14008e8e30b0f05130ea22376ccf5a2ea69 matches upstream+16overlays; offline cached Linux execution exit0. Receipt /private/tmp/pc58-c-resume-20261023/resume-auto-receipt.json. Generic new-writer autostart/recovery remains correctly refused for retained START ownership. This finds no C automatic same-owner resume defect; it does not validate authenticated Native replacement or initial silent START/acoustics. C investigation stops here; native liveness correction and its source-mutation preservation remain the bounded implementation.

Native correction planned as Speakers0.26.41 (compatible unchanged PodVoice74/Control19): pipe keeps pollable nonblocking FIFO; explicit Pause/Resume/Drop/Close cancellation joins the same writer. Ordinary Pause retains only unwritten PCM, while exact source mutation and Seek discard old pending bytes under the same join, preserving prior pause state. START fences discard stale suffix only under existing owner join; non-pipe behavior stays unchanged. No C guard, timeout policy, authority, provider, retry or UI change. Final overlay0064d599768c36421ae68dd1e928bd5e5547fef31f917521d0ddbf2e3530fc22 applies after connect-no-output-transport-native.patch. Independent adversarial source and actual Linux/race review on identical production bytes is GO:22 focused output/player/daemon owner regressions passed, including full FIFO Pause/Close, partial PCM, START/control race, new-source8192PCM, playing/paused seek and real EPIPE/stale failure. Last delta corrects only a test's blocking drain observer; final fixture-only review is GO on0064d599 with three affected Linux/race subcases passing; unchanged results retained. Earlier invalidated observer failures are retained, not runtime evidence. Official frozen-candidate CI, publication, installation and audio acceptance remain outstanding. Initial silent START attribution remains unresolved and this fix is not a full audio PASS.

## Active lead decision — PC58 false playing after stream-admission failure ·23October

Before merge of41, separate actual40 daemon/dealer/player regression reproduces three failures: NewStream metadata refusal with no output; replacement refusal leaving the previous worker FIFO output alive after AppPlayer primary is cleared; and prefetched SetPrimaryStream output-admission refusal with actor primary but no worker output. Each publishes IsPlaying=true/IsBuffering=true before admission, leaves those flags after failure and advances position; next Resume correctly rejects missing primary/output. Private exact receipt /private/tmp/pc58-false-playing-40-20261023/false-playing-red2.log. Initial Green attribution remains unknown, but this is now a concrete software fault matching false playback, not a UI hypothesis.

Lead/root holds41 merge. Chain: explicit dealer/context selection → new actor context publication → metadata/decoder source preparation → current worker output admission → terminal failure cleanup/publication → next explicit command. Preserve exact source/output generation, no stale cleanup of newer owner, no retry/automatic start, bounded actual retirement proof and truthful final inactive state. Hypothesis: existing terminal Stop/reset can safely retire exactly the failed actor/output instead of leaving premature playing state. Must prove whole caller context and surviving previous worker retirement; resetting flags or manager filtering alone is insufficient. Non-goals: no C guard/timing/provider/UI redesign, no old-context transactional rollback framework. Implementation owner sol61_connect_c_resume; independent adversarial reviewer sol61_release75_review; existing pipe overlay0064d599 unchanged. Root will integrate only proven minimal failure cleanup and causal next-command regression before final officialCI/publication/install. Rollback boundary is the new final overlay alone;41 is not merge/test-ready until review and current-source controls pass.

First official41 CI39940873769: OwnTone PASS; daemon/output/tracks/zeroconf/spclient/vorbis PASS; player fails with10-minute timeout in existing TestPipeEPIPEFailureClaimFencesOldOwnerAndRejectsStaleEvent/current_failure. Stack proves first pipe output (paused=true/drop=false request) began held source.Read before worker could Pause it; not a generic process delay or grounds to tune timeout. Correct initial-output ordering: every new pipe uses existing paused factory before SetPrimary and explicit requested Pause/Resume. Author sol61_release75_review owns minimal condition correction; independent sol61_review74_final reproduces/runs failed case plus affected neighbours. Earlier focused GO does not override official failure;41 stays not merge-ready. C and unrelated passed packages are retained; next official run is on final combined correction, not a manual rerun of unchanged head.

Final combined41 source result: liveness overlayfa07060f5896fdef44473ee078ed79e186b9947ef95be688790f2c9c27258f54 now creates every first pipe paused; deterministic old-condition RED fails immediately and entire changed Player Linux/race package passes27tests/22subcases in1.260s, vet/format/apply/pins pass. Separate final load overlay2582413970e6399ccbbcc01cc6be0db13f88ed255e7f1f328134400068c1d739 seals only captured failed actor, existing Stop/reset/inactive/logout joins output before prepared decoder close, and rejects stale ordinary events. Actual three premature-state failures now terminate with frozen stopped state and fresh actor can open real FIFO; existing first typed restricted/unsupported skip to valid prefetched source and terminal all-unplayable case pass. Whole changed daemon Linux/race package passes in12.052s, vet/format pass. Independent sol61_release75_review source GO on exact loadhash; sol61_review74_final independent GO on exact pipehash. Eight-file native overlay plus two-file daemon overlay are the only engine changes; C production unchanged. Thirty patch hashes and Docker/official-CI order bind both overlays to the source. Candidate now ready for final official combined CI; first failed CI stays failed. Invalid URI/type pre-defer cases and initial Green silent START attribution are outside these proven repairs; no audible/software-delivery PASS is claimed before publication/install/readback. Physical issue58 remains open.


### PC58 active lead decision — persisted C START owner across fresh41 startup, 23 October 2026

Lead/release owner: /root; isolated C source/reproduction owner: sol61_connect_c_resume. Fresh Green41 normal log proves first observed transfer after C/native startup13:22:10: transfer_received13:31:45 → alias1 → unpaused Daydreamin' load13:31:46 → FIFO autostart refused → accepted Connect route prime refused by C transport_busy/HTTP500 → route_not_committed. Passive13:36 output read shows all six AirPlay outputs unselected; intended Kitchen is session_epoch0/route_revision0/lease0/restoringfalse in the current C process. User reports quick Connect selection and advancing Spotify without sound. This is physical FAIL; no automatic Play, restart, reclaim, deletion or replay occurred. The41 Native repairs and successful software delivery do not establish initial audible routing.

Affected chain: durable C START/MOVE journal → fresh C startup/quarantine → actual FIFO watcher/reader → authenticated current Native transfer/writer → manager exact alias prime/selection → C route/session → PCM emission. Strongest source hypothesis: a retained nonterminal or malformed journal closes the startup emission gate and sets the START ownership latch; the ordinary Connect path cannot retire an orphaned previous-process obligation and is therefore denied before route construction. Autostart refusal and volume-prime refusal can share this earlier owner; neither log identifies its exact class. Existing permanent C successor fixture explicitly retains quarantine even after the old Native child process is kernel-proven dead. Actual Green journal contents remain unknown; empty guard, attention/custody or an ambiguous process certificate would falsify or narrow this attribution.

Invariants: one exact current owner/source/FIFO/generation; no old PCM or output session crosses restart; native boot-ID/PID/start-ticks and PID-namespace proof must distinguish absent/reused from live/ambiguous; output/attention/custody joins precede old reader drain; conditional FULL journal persistence must reject a concurrent successor; stage a fresh configured FIFO reader behind a closed gate; only actual current reader/writer and route readiness may open emission; UNKNOWN stays terminal with no replay. No change to C generic busy guards, attention leases, provider behavior, volume, gain, buffer or deadlines; no journal deletion or restoration of old task credit.

Next: run/extend the existing file-backed C successor fixture to join the observed generic Connect refusal to the proven-dead-process boundary. Inspect an available read-only content-free journal projection if Green access permits. Propose only an existing-contract orphan retirement: exact old-process retirement plus drained current C sessions/input, conditional durable retired-known witness, closed-gate configured-reader staging, then fresh current-owner admission. If process/namespace/journal/FD/route joins cannot be proven, keep quarantine and name the precise missing proof. Required regressions include live old process, dead/reused process, namespace/boot mismatch, malformed/unknown journal, delayed old callback/new successor, persistence failure and fresh FIFO-to-route continuation. Independent adversarial review and existing official C/Native gates precede any merge/release; rollback is the eventual bounded overlay. No42 candidate/version or implementation is yet approved by this decision record.


PC58 successor reproduction result: exact unchanged40/41 C production source in the existing cached Linux owner suite reproduces both boundaries. Existing file-backed successor proof still recognizes the actually exited old Native child yet keeps START ownership/gate closed; added generic Connect assertion returns terminal -1, never typed temporary -2. `/private/tmp/pc58-c-startup-20261023/c-startup-owner.log` exits0. Separately the actual AP2/FIFO mode17 terminal-retirement path reaches a canonical RETIRED_KNOWN journal, real C restart, exact staged reader and real replacement writer, but the real `playback_start_id` entry returns -1 while `outputs_start_retired_normal_ready` is true; `/private/tmp/pc58-c-startup-20261023/c-terminal-autostart-red.log` exits134 at that explicit regression assertion. The prior fixture called `pb_session_start` directly and therefore did not test this production caller. No production C bytes changed. This is a concrete safe-terminal continuation defect; attribution to Green requires its journal class, which is still unknown.

Bounded correction recommendation: carry the existing exact retired-reader permission through normal by-ID entry, then the current fresh-input/route-prime join; do not call a retained terminal record clean absence merely by enum, bypass unrelated custody/attention pins, or relax unknown startup quarantine. A consumed retired reopen still leaves a durable journal, so current `start_route_clean_absent` will separately deny Connect prime even after source admission; full regression must follow ordinary autostart through the manager/current owner and one real output route. Nonterminal orphan retirement remains a separate proof boundary: existing old-process proof accepts changed host boot or, only in the identical recorded PID namespace, absent/reused old PID/start-ticks. A different container PID namespace is ambiguous and cannot be treated as absence. Existing terminal writer/input retirement proofs cannot be manufactured from a fresh local input epoch or a different FIFO inode; no old task outcome may be upgraded. No cleanup/delete/runtime patch or release has been made from this investigation.

Root authorizes an isolated minimal terminal-only production draft after the preserved real-caller RED. Scope: exact registered by-ID PIPE admission using the existing closed staged-reader permission; classify a canonical RETIRED_KNOWN+consumed record as historical absence only after gate/ownership/quarantine/pending-stage checks and matching retired runtime slot. The durable journal remains intact; no old task outcome, writer, process or route credit is restored. The existing private AP2/FIFO fixture must execute ordinary by-ID command/bottom half → exact Connect prime35 → one selected connected output, plus nonterminal/unknown/wrong-input and unrelated-custody refusal. This is a software correction proposal, not Green attribution or release authorization. Green backup/journal classification remains pending the user approval requested by root; no alternate read channel is authorized.

Final terminal-only C correction is frozen privately for root review/integration: production overlay `/private/tmp/pc58-c-terminal-fix-20261023/connect-retired-autostart-owntone.patch` SHA256 d3e32a87fc1f91a132fa3ad0dd6874612fc5b0b45dc595adeaf7f2b37a1691ae; existing-fixture overlay `connect-retired-autostart-ci.patch` SHA256 a1212439b02cf83096c75dee239b208932310fa607f13c6ae96137271cfc3468. Three production files only: by-ID verifies the requested registered configured PIPE against the existing exact staged reader before queue mutation; the new autostart permission requires no retained runtime START slot and no transport custody; clean absence accepts only a canonical RETIRED_KNOWN+consumed historical record with no active slot/scope/latch/gate/fault/quarantine/pending stage. Journal stays intact and no process/task/route credit is restored. Production changes apply after the current16 C overlays; fixture changes apply after the existing CI fixture overlay. Exact261-file baseline manifest e5eb44d9882801ef1644df19f1f0d14008e8e30b0f05130ea22376ccf5a2ea69 → final7ffa4c06f54eb5e4b21197ba35373babc5dce970a429fbb1a087d94aa8420fb5. Receipt `/private/tmp/pc58-c-terminal-fix-20261023/terminal-autostart-receipt.json`.

Causal controls retained: unchanged41 by-ID returns -1 with no prime/selection; by-ID-only repair then returns prime35=-1/start_not_clean_absent. Exact final software chain executes public by-ID API on the real player command owner/bottom half → PLAY_PLAYING → Connect prime35 → public Connect selection → one real private AP2 session CONNECTED and exactly one volume35 wire write. Missing library ID and live unrelated custody refuse before queue mutation. Fresh-runtime journal reload is exercised with loss of the active slot; this is not an OS process-reexec fixture. Cached offline Linux make-check passes3/3; nine relevant existing/extended terminal/unknown/expiry/starvation/closed-writer cases pass final4 logs. Git apply/check and whitespace checks pass; all five final files match reapplied overlays. No acoustic or provider acceptance is inferred.

Independent review found that permitting a retained same-process completed MOVE slot would let a delayed old cancel close the gate and retire the new normal route. An explicit private control reproduces that failure against superseded6073; final lead scope instead requires no retained slot before either new permission. Same-process retired slot now refuses ordinary by-ID/prime before source, selection, volume or journal effects; original nonretired postcomplete MOVE cancel quarantine remains PASS. No slot detachment, broad PLAYING→retirement→stop change or recovery architecture was added. The exact Green journal branch remains unclassified while root awaits the requested backup approval; no alternate read channel, production mutation or publication is authorized. Initial41 silent START and physical issue58 remain open.

Approved field-branch evidence, 23October: the user-approved existing13:20 pre41 app backup is now downloaded through normal Chrome UI. Only its `songs3.db` singleton `native_start_guard_v1` START row was inspected read-only/immutable; no other rows, credentials or raw identities were output. `/private/tmp/pc58-approved-start-read-20261023/START-READINESS.json` confirms all production canonical terminal validation constraints, including real json-c plain-byte serialization/58keys, column/payload identities, retirement/native writer/source/FIFO joins and advanced retired input epoch. Phase10 is OUTPUT_START_COMPLETED; schema is START_v1, move_existing_session=false/source_paused=false/cancelled=false/writer_pending=false, reader_ready_closed=true, retired_known, reopen_consumed=false, configured FIFO path/digest match. This is terminal closed-source evidence, not an unknown/orphan obligation and not a new emission grant. No current process/FD/FIFO proof was invented.

For this persisted row, exact40/41 startup predicts journal_fault=false, terminal=true, retired_reopen_pending=true, owned_latch=false, startup_quarantine=false, gate_closed=true and no runtime active slot. The first logged fresh41 transfer13:31 reaches generic watcher by-ID, whose unconditional busy refusal prevents existing exact staged normal admission; prime then remains blocked and passive output session_epoch0/all-unselected agrees with failure before route creation. This is a high-confidence causal match to the already-proved terminal continuation defect. Earlier persisted-nonterminal/quarantine hypothesis is superseded for this approved snapshot; source-owned latch is not the predicted blocker here, the closed terminal reopen gate is. The backup predates41 startup and has no WAL, so it is not a direct post-failure live-row proof and cannot exclude an unseen later mutation on its own. START and MOVE terminal schemas share the same startup/staging/admit/consume paths; frozen full command regression uses terminal MOVE, while unchanged START closed-writer cases preserve canonical staging. No source/fixture bits were changed after freeze. Root owns official42 CI/review/delivery; physical sound remains FAIL/open until one fresh explicit Connect transfer proves current selected route and audible output.


## Active lead decision — PC58 paused Connect handoff cancels its own route credit ·23October

Lead /root; isolated implementation sol61_review74_final; independent reviewer sol61_release75_review. Supplied42 trial log records paused:true load → accepted connect_transfer → event_cancelled; fresh passive state remains playingfalse/all outputs unselected. The retained trial does not prove a later explicit Resume. Source proves the queued initial worker Pause follows the accepted event, manager cancels every Pause, and the persistent accepted-transfer fence then blocks fallback routing. Later Resume remaining stranded is source-derived and covered by regression, not a claimed field observation. Physical sound remains FAIL/open.

Affected chain: explicit dealer transfer/current selected alias → successful actual worker paused admission → initial worker observation → existing one-use manager credit → explicit current-owner Resume → guarded Connect prime/selection. Adjacent boundaries: queued older Play/Resume, genuine Pause before initial-event delivery, duplicate/older/malformed/newer receipts, retired reader/source, changed alias/binding, expiry, UNKNOWN and busy. Invariants: exact source/load provenance; no autoplay; genuine Pause always cancels; no replay after spent/unknown/expired credit; unchanged5s window and all route/account/attention/START guards; preserve worker acknowledgement/event FIFO and both output adapters. Non-goals: inferring the first Pause or URI, arbitrary event draining, retries/timeouts, RestorePaused tuning, provider/Green/release actions. The pre-code plan was to correct only this observation-versus-intent boundary with the existing dealer/worker/FIFO and actual roomBridge harnesses, preserving causal RED and independent review.

The first serial-publication draft78a9 was withdrawn before adoption: ignoring the queued initial Pause after synchronous publication lets an older queued Play/Resume revert a newer paused source. An event-drain alternative was rejected because arbitrary older callbacks can nest new loads or interact with a full queue. No withdrawn source was copied into the release clone.

Final isolated implementation uses one per-AppPlayer load receipt bounded to JSON's exact integer range. Its sole production source setter passes that receipt to the worker; only that successful initial Play/Pause carries it. Genuine control events remain untagged. Accepted transfer includes its expected receipt and actual initial paused state. The manager's original credit starts at the same accepted-event time and cannot be consumed until its exact initial observation arrives; only that matching initial Pause preserves it. Every ordinary Pause cancels, including before the matching receipt. While the unused credit is pending, older initial observations and already-observed duplicates are inert; malformed/newer/wrong-state observations discard credit. After credit consumption, a late duplicate follows the existing fail-closed transport handling and may abort a route; global duplicate immunity or replay is not claimed. Existing source/alias/expiry/UNKNOWN/busy/attention/START fences and worker acknowledgement/event order are unchanged. The legacy SetPrimaryStream method and absent-receipt wire behavior remain compatible.

Causal evidence on exact final production: existing Native explicit dealer/real worker/named FIFO fixture produces actual JSON in four branches (older queued Play+Resume present/absent × genuine Pause command queued before initial-event delivery present/absent). The same untouched JSON and actual Native binding seed the existing manager status owner and actual roomBridge fixture; no event or identity rewrite. While awaiting the exact initial receipt, older Play/Resume cannot route. Initial paused observation still causes zero effects through multiple bridge ticks; prompt explicit Resume permits exactly one pre-selection Connect volume proof, one selection and the existing post-selection volume proof. Genuine Pause command cancels and Resume cannot revive it. Restoring only the old unconditional Pause cancellation against those same Native JSON bytes yields accepted→event_cancelled→no bridge attempt (RED); the fatal observer's existing background-bridge cleanup also reports races and is retained, not product evidence.

Final manager Linux race checks pass all7 actual bridge modes,9 receipt-bound cases and existing cancellation/one-shot cases (14.707s). Original UNKNOWN pause/resume, expired busy, fresh ordinal and busy-drain cases remain intact. Native four-branch causal race check, affected Dealer/PC58/Connect tests, six worker paused/source/START/MOVE neighbors and daemon/player/manager vet pass; final gofmt/whitespace, exact baseline patch application and full affected-scope checks pass in the frozen receipt. Earlier fixture setup/count errors are retained as invalidated observations; no runtime patch was made from them. Independent adversarial source review is GO on Native046c9e935d846cd3747f13315165db1744059caad014d5fcf19c7e4232c23472 and manager07c9da632fb07ea575db1807f2f1d096ec1df9ea1310c876105bb525fedb276a; previous78a9 review remains withdrawn.

Product boundary: pinned upstreamc191a43 already computes paused as Playback.IsPaused && RestorePaused != "resume"; this patch preserves it. The5s eligibility window still starts at accepted connect_transfer, never at initial observation or Resume. No explicit Resume in the actual42 trial is proven; this patch proves only a paused accepted handoff followed by a prompt explicit Resume inside that existing window. A later Resume after expiry does not create a new transfer credit, and the persistent fence still refuses fallback. Whether that later explicit user action should receive separately proven authority is an open product/contract decision outside this patch. No automatic playback, release/version, installation or physical audio acceptance is claimed. Root owns subsequent integration/delivery decisions.

Speakers43 isolated integration: root authorizes this single combined candidate; integration owner sol61_connect_c_resume. Approved Native046c9e935d846cd3747f13315165db1744059caad014d5fcf19c7e4232c23472 applies after the reviewed30 Native overlays; approved manager07c9da632fb07ea575db1807f2f1d096ec1df9ea1310c876105bb525fedb276a supplies both production receipt handling and existing regressions. Withdrawn78a9 is absent. Exact reviewed42 subtree30e667c8d888c64069eaf935ff1755eb5e1f3abf is the baseline; C17 overlays/fixtures and Control0.10.19 remain byte-identical. This is planned43 build only: independent source GO and reported focused causal checks are retained; integration static checks and exact-tree/pins are recorded outside the repo for lead review. Official43 CI, publication, Green installation and the physical sound gate remain pending. A paused transfer followed only by Resume after five seconds remains outside this correction and cannot create fresh route authority.


PC58 active lead extension — complete paused Connect staging, 23October. Lead/root authorizes the isolated C staging correction; C implementation sol61_connect_c_resume, manager integration sol61_review74_final, independent review sol61_release75_review. Installed42 physical trial remains NO SOUND: paused load → accepted transfer → manager cancellation, no selected output; the later C starvation log proves its emission gate was open at that time but cannot prove a HomePod session. The provisional43 receipt-order candidate repairs only prompt Resume inside the existing5s window and is not approved to ship as the complete flow.

Whole chain/hypothesis: authenticated paused transfer carries exact load observation/one-use manager credit → immediate guarded Connect prime35 and selection must stage only the already-canonical retired-known fresh-C reader while PCM is absent → manager retains that selected staged disposition without autoplay or ordinary pause/cleanup interference → later explicit same-owner Resume produces actual PCM → real pipe watcher EV_READ → public by-ID → existing42 exact current-reader/CAS consume → gate opens → selected real AP2 route activates. Current C Connect admission equates every closed gate with live/unknown busy, so it refuses this already-proven staging contract before PCM; treating staging as clean absence or opening the gate early would violate ownership.

Invariants: no retained in-memory START slot; canonical retired-known journal and configured exact FIFO/staged reader; drained old source/output contexts; no unrelated transport/attention/output busy or custody; volume35 and selected flags only before PCM, no AP2 session/emission and no journal consume; actual later reader identity and conditional durable reopen consumption own gate opening; unknown/nonterminal/live slots remain refused. Non-goals: provider/paused semantics, timeout/window tuning, auto-Play, journal deletion, orphan recovery, generic busy bypass, restored old task credit or release actions. Reuse existing private AP2/FIFO/actual watcher harness; causal RED is paused zero-PCM staging refusal, GREEN must preserve a closed unconsumed gate with selection through more than5s, then actual EV_READ/by-ID/normal admission to a real connected route; retain wrong input, custody, attention, unknown, active-slot and stale-callback controls. Rollback is the new final C overlay alone, after42's17-stack; provisional43 remains held for combined source review/gates and later physical acceptance.

PC58 lead decision update — staging path WITHDRAWN before adoption. Independent whole-chain review identifies stale A selected flags followed by newer B PCM as an audible-wrong-room risk; a manager pointer alone cannot fence C's delayed watcher. Preserve the isolated staged proof as research, but ship no staged writes, stage success header or gate/selection authority from it. Lead/root chooses the smaller existing contract: a fresh authenticated dealer Resume provides Native/manager one-use current-owner credit with the original bounded deadline and fences; C classifies only exact fresh canonical retired-normal-autostart-ready closed input as existing temporary pre-effect busy, without any flags/base/effects. Actual PCM → existing pipe EV_READ watcher →42 by-ID reader/CAS admission opens gate, then the retained explicit Resume credit may prime35/select the current correct room. Status polls create no authority; unknown/nonterminal/live retained slots remain terminal refusals.

Whole regression chain now follows explicit dealer Resume → actual worker observation/ACK and PCM → real C watcher/autostart → typed prePCM busy → guarded retry prime35/selection → real AP2 route, with Pause/teardown, newer source/alias, stale/duplicate callback, attention and unknown/expiry controls. C deadline/window/provider behavior and selected flags stay unchanged. Implementation scopes are C classification only (sol61_connect_c_resume) and Native/manager explicit Resume credit (sol61_review74_final), independent review sol61_release75_review; rollback only the new combined diff. No release43 until combined source GO and complete audible-flow decision; physical issue58 remains FAIL/open.

Final narrow C result (no staging): new overlay `/private/tmp/pc58-c-resume-prime-20261023/connect-resume-prepcm-busy-owntone.patch` SHA256 cc4dc086fc6fef835e942f60ae8a8a05da40ff8714ebfaf32900eb315d6126a3 changes only `src/outputs.c`; fixture overlay `connect-resume-prepcm-busy-ci.patch` SHA256 e296a1875392910b276022b9a456c84c0a9653f4339096bf246bc3236d8c267c extends only existing AP2/FIFO files. Exact fresh no-active-slot canonical retired reader may return the existing typed temporary -2/HTTP409 before effects; attention/outputbusy, any actual session, unrelated custody, live/unknown/nonterminal/malformed guard remain terminal. No new stage/selected flag, successful prePCM response, schema, header, API signature or deadline exists. The existing header is `X-PodConnect-Recovery: temporary-busy-no-effect-v1`.

Unchanged42 causal control fails after>5s with actual public prePCM prime temporary=false; final-fixture42 control likewise fails expected -2. Exact final C18 source passes all3 existing make-check suites and10 focused cases. New actual watcher regression holds zero PCM for>5.2s with unchanged selected/base/route/session/input generation/journal, gets typed no-effect busy, writes real FIFO PCM, executes production watcher EV_READ→public by-ID42→reader/CAS gate-open, then explicit prime35/selection retry connects one actual private AP2 route; watcher/input teardown joins and exits0. Negative controls include output/attention, malformed/fault/quarantine, source latch, retained old slot and unrelated custody. Source/fixture patches replay exactly; source1/fixture2 scope and whitespace pass. The C regression models the Resume control/producer edge; it does not itself execute Native authenticated dealer/ACK or manager identity. Their joined regression/review and full physical sound gate remain required. Receipt `/private/tmp/pc58-c-resume-prime-20261023/resume-prepcm-busy-receipt.json`. Independent adversarial review is requested; no release/Green/CI action was taken.


Speakers43 final isolated integration supersedes the provisional prompt-only43 limitation above. Native stack32 applies initial paused receipt046c9e9 followed by explicit Resume7c7c71fa; manager production/test applies initial07c9da63 then explicit Resume4f586ca4. OwnTone stack18 adds only exact prePCM busycc4dc086; official CI applies fixturee296a187 after the existing42 fixture and explicitly runs --move-retire-resume-prime-wire. Docker preserves the existing separation of production overlays from CI-only fixtures. Control stays byte-identical. Independent source GO received on both frozen Native/manager overlays and narrow C/fixture. Three owner-boundary regressions provide Native authenticated Resume/worker ACK, manager exact receipt/fence and actual C FIFO-watcher/AP2 evidence; they are not one executed end-to-end provider/composite test. Final exact-source static composition is recorded in the integration receipt; official frozen-candidate CI, publication, installation and physical sound remain separate pending gates. Rollback is this43 diff; no staged room flags or new timeout policy ships.


Active lead decision — read-only exact-account user playlists, 23October. Lead /root authorizes isolated next-software work by sol61_connect_c_resume; frozen43, Green and playback are unchanged. Existing SpotifyApi.playlists returns only items and discards first-page coverage; no response-only exact-account service exists for PodView M04/M09–M12. Implement get_user_playlists through the existing OAuth/config-entry owner, max20 firstpage offset0, total8s and raw256KiB, no429 retry/new scopes/auth. Capture existing /me-established unique account binding and fresh runtime epoch, recheck current entry/owner/account before and after external awaits and after OAuth before provider send. Whole response validates before any projection; output only real playlist URI/name/optional canonical playlist cover and explicit first-page coverage/source_has_more. Never infer DailyMix/daylist, playback authority or native-account equivalence. Regression: real service registration/response-only schema, selected account, empty/more pages, malformed tail/paging, size/time/rate limits, OAuth and HTTP owner replacement, cancellation, sanitized errors/metadata. Formatter/lint and appropriate existing Python/HA inert checks precede review; rollback is this isolated Control/service diff only. No provider/live/release/install or physical acceptance is claimed.

Read-only playlist result: get_user_playlists now registers through the existing catalogue setup hook before search's independent idempotency return. The response-only closed podconnect.user_playlists.v1 DTO contains config_entry_id/owner_epoch, spotify_web_api source, first_page coverage, requested limit1..20, returned_count/source_has_more and actual playlist URI/name/optional canonical playlist cover. Exact request offset0 is enforced; no next URL, total, private owner metadata or credentials escape. All rows/paging validate before projection. Existing unique account binding/runtime/epoch are captured and rechecked before OAuth, immediately after token validation before GET and after the complete await; guarded reads also refuse redirects. Legacy browse/search/command behavior stays intact.

Final focused evidence: all88 existing+new inert Python boundary tests PASS0.190s-class; actual pinned HA2026.8.2 service/client suite5 PASS8.486s, including exact second account/default20/schema/response-only, OAuth+HTTP epoch changes, actual runtime/account binding replacement,403/429/500 and256KiB, redirect refusal, actual eight-second deadline and caller cancellation. The final catalogue-hook-only change separately passes actual HA registration/idempotency with search already registered/user absent then both present/no duplicate replacement (0.155s); unchanged service/client case results are retained. Changed Python formatter/lint, services YAML and whitespace pass. Full integration mypy retains exactly the43 baseline's71 errors in11files after line-number normalization; no added diagnostics, but no typecheck PASS is claimed. No live account contents, provider scope proof, release, installation or physical acceptance were exercised. Independent source review is pending; this Control-only draft does not modify frozen Speakers43, Native/C or version metadata.


Control0.10.20 packaging decision — root/sole release installer combines reviewed PodConnect playlist source with PodVoice77/UI. Packaging owner sol61_connect_c_resume; independent reviewer sol61_release75_review after PV binding. Reviewed source branch codex/account-playlist-catalogue86e20c27c095e09eca65c44f7c9290f602bca65d builds on actual main43 commit6a35d5e0f6ef0527c8eaced07417a326d7b0cdb5/tree569906cdb6825a55e8a2fc4e8d37331b7225779d. This isolated step changes only Control manifest19→20, changelog and this append. Existing get_user_playlists.v1 service source/DTO/owner guards and its focused evidence stay byte-identical. No Speakers source/version, Native/C overlay, workflow, scopes, policy grants or runtime deadlines change. Minimum HACS HA version stays2024.1.0 because this passive service adds no newer dependency; actual framework evidence is specifically HA2026.8.2, not a new minimum-version compatibility claim.

Release admission follows docs/releasing.md: official Verify HA service and client boundaries CI on the final candidate; exact Control manifest0.10.20 and matching published GitHub Release/tag; source-pinned HACS content; then sole root installer stages Control20 with PodVoice77/UI and verifies installed versions/service availability/current account and owner epoch. Publishing a tag alone is insufficient; no Speakers image build or release is part of this Control-only diff. Existing publish.yaml path filters exclude these packaging files; existing HA CI includes manifest and reviewed service/test paths without new wiring. Reuse5 actual HA service/client cases, final real registration/idempotency case and88 boundary tests. Full mypy remains exactly71 baseline errors in11files, not a full PASS. Final packaging statics/source hashes and PV contract binding must receive independent GO; publication, Green installation, actual Spotify catalogue/provider scopes and visible UI acceptance remain pending. Rollback only Control files/version and coupled PV77/UI; never music replay or auth/scope changes.


Control20 final source composition: reviewed publisher workflow20f5004873edd05b4ffb0b38417ad84770046ff9cbc57ab4a41f788512a1fb5f, script ec55a1d44cd751f2493114a2c31a7447c5a625d16fb2411cc2849e40c5bc326c and test43fcd559c14991203742fa2079fa9ff0e108f181db4f942da12c4df269701977 are copied exactly after independent high-risk source GO from weather_backend_review. Redirect HOLD was corrected in the reviewed source: the authenticated GitHub API client refuses redirects; the retained ten focused publisher tests include actual inert stdlib redirect handling. The new publish-control job runs only on push/main after both mandatory same-SHA boundary and pinned-HA jobs succeed, with contents:write confined to that job. It publishes only a validated changed Control manifest version at github.sha, reuses an existing exact-commit release/tag, and fails closed on mismatch/ambiguous outcomes without retagging or overwriting. No GitHub API, publication or provider action was executed locally.

The reviewed playlist service/test source, five real-HA inert cases plus final idempotency case and88 boundary checks are retained unchanged. Final composed Python formatter/lint, workflow/services YAML, shell syntax, manifest20, complete source inventory and exact tested-source pins pass; full mypy remains71 unchanged baseline errors rather than a full PASS. Speakers0.26.43 entire subtree32147754f43263b7ac9756fc0a390eaa9d30c65f, Native/C, scopes and HACS minimum are unchanged. Candidate is frozen only for exact-hash composition review and paired PV77 binding; official final CI, HACS Release/tag publication, sole-root Green installation/version/service readback, real account catalogue and visible UI acceptance remain pending. GitHub/HA/provider/installer actions remain with root under existing authorization.


## Active lead decision — stop legacy pause feedback for current bound-contract capability, 24 October 2026

Lead /root; manager owner weather_budget; C ACK split separately native_idle_owner. Field11:05:47 records accepted exact Native pause followed by C confirmation409; generic manager reconcile sees Native paused and C PCM lifecycle still play, sends generic C pause every200ms and receives500. C PCM lifecycle is not Native logical timeline. Chain: current Native source event → existing bound Native/C capability observation → manager C player read → legacy decide/admission → generic effect/log; next cycle repeats. Adjacent boundaries: run/phase/alias replacement, observation late/stale, unowned external transport, unsupported C source and Native acceptance/UNKNOWN.

Lead authorizes minimal suppression using existing current-run/binding cached capability only, never as effect permission or physical owner proof. No new poll/cache/owner-receipt framework. Retain unowned/external legacy transport, all exact Native commands, source/admission fences and existing cadence/deadlines. Hardware pause remains existing exact Native/C event ownership; absence is an explicit behavior gap, not authority for global fallback. Plan: reproduce in existing actual-loop child fixture, then gate generic legacy transport admission for current matching cached bound-contract support, invalidating stale run/phase/binding observations; focused causal/ordinary/stale tests and independent review. No effects, shipping or physical acceptance.


## Native START cancellation fixture follow-up — 24 October 2026

Status: **ingen version aftalt**; source-only fixture successor on published main
cc96fe33c2a94bccf1c5c7e5f8c7f7c868e0be04/tree aed22932e7d3ad93223211fa061a91e28694a479.
Lead root; fixture owner native_idle_owner; independent reviewer weather_backend_review.

Speakers0.26.44 ARM publication remains immutable and separately verified. The amd64
publisher run40077936370/job120825256433 remains FAIL/partial publication: its
protocol-only START fixture observed the original asynchronous C cancellation after
C COMPLETE, while an old assertion expected only prepare/commit/barrier/open/complete.
The fixture deliberately has no real writer, so Native correctly records
writer_current/writer_missing/UNKNOWN and cancels C. Replay is already fenced.

Only the existing room-start-diagnostics-native.patch test hunks and Native checksum
entry change. The fixture joins the original cancel, requires exactly one cancel and
the existing missing-writer diagnostic, then proves terminal commit/cancel replay
adds no requests. No runtime guard, callback, timeout, provider behavior, version or
publication changes. All13 subcases of the exact sequence test passed once in cached
Linux arm64 Go1.25.11 with networking disabled; source review confirms fixture-only
causal closure. Complete32-patch composition and byte comparison preserve every
non-test source file. This is no claim that amd64 publication or a complete publisher
has passed. No new release, merge, publication, HA, installation or physical action
was performed by this follow-up. Root owns any later draft PR and delivery decision.


## Active lead decision — ordinary bound Pause confirmation, 24October.
 Lead/root; isolated owner native_idle_owner; independent reviewer weather_backend_review. Installed Speakers44 log at11:05:47 records bound-pause-confirm409 followed by repeated rejected legacy player/pause500 every200ms and normal PCM starvation. Initial command origin and exact live409 branch remain unknown. Source proves a separate falsifiable contract defect: bound-state offers Pause for a clean-absent ordinary current C route; Native applies its guarded pause and emits the observation; C confirmation then requires protected START ownership and rejects that ordinary effect. Candidate is not test-ready; no field acceptance is inferred.

Affected chain: current Native source/timeline → exact bound C prepare/commit/custody → guarded Native pause → C post-effect confirmation → normal FIFO starvation/suspend/delayed output stop → explicit same-source Resume/PCM → existing C input-buffer resume/output route. Adjacent cases: completed START retention, missing/changed token/prepare/revision/route, expiry, competing custody, duplicate/late confirmation and unknown START/journal. Preserve one-use consumed capabilities, exact Native/C route custody and actual FIFO source; ordinary ACK grants no persistent START retention or physical result. Proposed correction only separates exact effect acknowledgement from completed-START resource retention, keeping ordinary pause/resume mechanics unchanged. Regression plan: causal ordinary confirmation RED, exact ordinary ACK GREEN with adverse fences, existing real completed-START pause retention and actual ordinary delayed-stop/resume route; unchanged Native real-worker pause/resume evidence. Legacy manager resend mechanism is independently known and remains separately scoped until proven. No versions, release, installation, provider/device actions or timeout policy change; rollback is this source-only successor diff.

Minimal successor result: exact C19 plus ordinary-pause-ack final overlay now acknowledges only a consumed successful bound Pause/paused-next/previous effect whose token/prepare/revision, unexpired custody and actual current C route still match. Clean-absent ordinary route ACK installs no START retention; completed START retains the unchanged real writer/reader/session checks and resource retention. Existing actual private AP2/FIFO queue-connect fixture first reproduces the ordinary confirmation failure (RED), then passes with duplicate, successor custody, changed session epoch, expired token and unknown START rejection/no revival. Exact final existing start-retire fixture also passes, including completed START pause retention and changed-session/stale-callback controls. Both overlays apply cleanly to exact C19; the composed inventory changes only outputs.c and the existing start35 wire fixture.

Required ordinary delayed-stop→resume remains UNPROVEN before release. A separate attempted extension reaches actual PCM starvation/pb_suspend then the original private fixture bound fails; the failure log and attempted test source are retained outside the shipping patch. Its no-PCM initial setup has a CONNECTED rather than STREAMING session, so the ordinary flush returns a no-op and no delayed-stop timer is armed; this is a fixture diagnosis, not production evidence. C fixture owner sol61_connect_c_resume owns the minimum actual streaming/peer adapter check in a separate copy. No runtime patch follows that timeout. Manager repeated legacy pause correction is separately owned by weather_budget. Independent review and whole causal pause/resume gate remain required; installed44 field branch/origin and volume availability report remain unresolved. No version/publication/installation or physical acceptance is claimed.


## Combined45 source integration draft — 24 October 2026

Lead/root; packaging owner sol61_release75_review. Isolated composition copies exact shipped44 production (published cc96fe33c2a94bccf1c5c7e5f8c7f7c868e0be04/treeaed22932e7d3ad93223211fa061a91e28694a479); only the newer active field STATUS differs in the input copy and is preserved. Planned Speakers0.26.45 retains Control0.10.21, existing Native32 production and C19, then appends the frozen ordinary-pause ACK overlay after C19 in the identical Docker/official CI production order. The CI-only ACK fixture follows its prior four fixture overlays and is exercised by the existing explicit queue-connect-wire invocation, without adding a new runner or runtime fixture.

Five pinned manager files preserve Native bound-contract play/pause ownership, suppress the separate legacy feedback mechanism only for the current capability and acknowledge a legacy HTTP send only after success. The previously reviewed Native correction changes only the existing cancellation fixture and its checksum entry; all other31 Native patch hashes and production hunks remain unchanged. The current ACK fixture is the frozen source receipt, not the unfrozen delayed-stop/resume investigation adapter. Source composition/static checks are separate from the existing causal test receipts and independent review.

This draft is NOT frozen or test-ready for release: exact ordinary delayed-stop/resume proof and independent ACK/combined composition review remain pending. Root owns any final official CI, merge/publication and one coordinated installation/physical series. No provider, HA, playback, device, CI dispatch or remote mutation occurred in packaging; current44 field origin and remaining sound/volume/timeline acceptance remain open.


PC45 final fixture integration update: the separate peer-only delta c486d501f3730953ff008d424ef32afcc7281e59cdf8ab3e0e1c11dbbb69ee84 now closes the previously UNPROVEN ordinary delayed-stop/resume software boundary. Its retained actual Linux command ran existing --move-retire-wire/mode17, not queue-connect-wire/mode25. It proves real FIFO PCM reaches AP2 STREAMING/UDP, natural PCM deficit drives strict encrypted matching-CSeq FLUSH, the real armed10s STOP and custody expiry occur, then same-writer PCM reconstructs AP2 STREAMING/UDP with the original FIFO reader epoch and loaded source. The exact retained peer-final-resume.log SHA20442796754985d1c4d30376c67f9a3e78dc1f6a7ce2449fd990b6ad5ed7c6de is software receiver evidence; no Native actor executes inside this C fixture and no physical HomePod or audible result is claimed. Original failed setup/observer attempts remain preserved in the owner receipt and did not trigger runtime edits.

The complete CI-only overlay is regenerated from C19 to final two fixture files (airplay_callback_owner fc99b5f1ede1712878006ad67ca429cd6a9dd584de419c2882c23d6237f2f130; start35_wire_owner c17acb3189fc4c9593e714d17219041277d62be10ad6c41e16a9ed8e245677f9). ACK adverses still execute via the existing queue-connect-wire; whole ordinary pause/resume executes via the existing explicit move-retire-wire. Both official Linux invocations occur once; no extra harness or production timeout change is added. C runtime outputs.c stays8f8f05f88e5d01bbff2366a5d204184b7effe650e45b0fca1bf7e224341b082c and every production source pin is unchanged. Exact20 Docker/CI patch order, five CI fixture overlays, manager frozen pins, Native32 checksum composition and changed-file static checks pass. The initial integration HOLD above is superseded only for that measured software boundary; independent final C/manager/composition review, required official CI and root-owned publication/install/physical acceptance remain required. No further broad local gate, remote operation or device action occurred.

Lead HOLD before final45 freeze: independent review identifies a possible admitted bound Pause after completed MOVE whose retention predicate may still reject it. The C owner is checking actual bound-state advertisement. Because the same-candidate switch/pause series includes this path, do not export or release until it is disproved or the smallest owner correction, causal regression and independent review close it. No second version or speculative patch is introduced.


PC45 cumulative C20 source update (same unreleased version): root supplies independent source GO for the frozen ACK09c638, completed-MOVE836955 and paused-volume358b1f components. The packaging owner composes those sequential production deltas from unchanged C19 into one ordinary-pause-ack-owntone overlay, and composes their three CI fixture deltas plus the frozen ordinary peer c486d501 into one ordinary-pause-ack-ci overlay. There are still20 production overlays and five CI-only overlays; no C21/C22 layer or new state/API/timer is introduced. The four final runtime pins are outputs.c=98060254579e43da3b7b171a2130dcff165af122a24fa6edf636c3a33b449c49; outputs_transport_custody.inc.c=c267a57c75baa0621a7255163d279d6231e09a3a0d26f8cc4739218a507887fc; outputs_room_volume.inc.c=46577942932188e6be123bb0564668bc20a3e0b5a85e4016e21fef6e0aa6f0a1; outputs_start.inc.c=48c17fd1d469805fc9d34fb3905147f8b2c13372187d49de84e01d10f9bda872. Exact component and cumulative apply checks reconstruct the same four runtime files; all other C19 production source pins remain unchanged.

The volume owner retains causal RED/GREEN in the two existing START/MOVE fixtures, actual matching-CSeq volume acknowledgement, held armed-STOP race, terminal UNKNOWN/no replay and drained/foreign-context negatives. Official Linux make-check already runs default modes18 and24 through TESTS=check_PROGRAMS: START volume is mode18/confirmed_pause_custody_case; MOVE volume is mode24/completed_move_pause_confirmation_case. The incumbent explicit queue-wire repeats mode24; no new duplicate invocation is added. Mode17 ordinary real delayed-stop/resume remains the existing explicit move-retire-wire. The real completed-MOVE ten-second retention/resume gate is still active with the C fixture owner; no final export/freeze follows until that receipt and final independent composition review arrive. All manager and Native fixture pins remain unchanged. No publication, CI dispatch or device/provider action has occurred.


PC45 final real-MOVE fixture staging update: the frozen158cd6543a2e4ba12784489a6505a6e8cac53b82cbd8d880591a52acc30d6d6d packet supersedes the earlier ordinary-only peer delta as one cumulative adapter. It preserves ordinary mode17 and keeps queue-wire/default mode24 owner-negative checks unchanged; only the explicit new move-pause-resume-wire flag selects the timed completed-MOVE branch. The existing official Linux C job now invokes that flag once with a25s process bound, after the same checksum-bound CI overlay; no new program or runtime timer is added. Exact cumulative C19 apply reconstructs both frozen fixture pins08230646/af5c67f4 while the four runtime source pins and261 production manifest stay unchanged. The retained real ten-second MOVE trace includes same-owner volume acknowledgement, actual STOP rearm and same-writer PCM/UDP; it does not close the newly isolated explicit bound Resume-ACK→armed STOP→delayed actual PCM boundary. Root keeps final freeze/export on HOLD until the minimal Resume runtime/fixture correction, causal RED/GREEN and independent source/composition review arrive. No CI dispatch, provider/household/device or release operation occurred.


## Combined45 final source and export closure — 24 October 2026

Root supplies final scoped independent source/fixture GO from sol61_review74_final. The minimal final bound-Resume component0461bd4af90ef96562c3f1b12012068896309c75bca8b31feaa4a02f53455c6e preserves only the proved same owner/current route across the successful Resume custody update until actual PCM owns timer cancellation. The causal old980602 source sends actual TEARDOWN at the injected already-armed STOP after Resume ACK and before PCM (RED); final outputs.c=34cbff88d9d339388802a2eb8fca46b96e02ded85a8946b8f4fe08ac0cb012af retains that same AP2 source/epoch/route through the real STOP boundary and then reaches actual PCM STREAMING/UDP (GREEN). Foreign owner, changed route, lost reader, consumed stale proof, restored-condition ABA and real writer close remain denied in the existing direct negative helper. The other three reviewed C runtime files are unchanged.

The frozen merged082ea95f5a2901877e1f798f6d9588eafdc4174cd4607dac69918dd0ca249f5b peer fixture applies after the owner-reviewed direct92e61b helper; final fixture pins are airplay_callback_owner.c=082306460e1c22de1c36b8f980c4576d6b7405345a4df4cd24bdf3050d4331f3 and start35_wire_owner.inc.c=00babc0fe6babcc95da66576be8e280f9697656b109a1ed28df5ce7c588be34f. Exact final real-flow receipt retains RED10593d8d7b8ec02f036c02f0db1187fe843b3cd228f002f41c72c6b0a691dbe3 and GREEN5e8c0fe7c45aa7bc92f258e74f7e19636829b2ec8eb969661c5f427fd8e2bafa. Direct negative-helper execution and unchanged-body evidence are retained separately; no new broad test run is claimed. This supersedes the earlier software HOLDs in this source draft; the installed44 field observations and failed attempts remain preserved and are not reclassified as physical PASS.

All four production components are composed into ONE cumulative C19→C20 ordinary-pause-ack-owntone overlay19f28d56816c56c99d992ec70826605f9c9d00d552169cb0ef0dcf7ea18b5bae. Four direct CI components plus the final merged peer fixture compose into ONE CI overlayf1f3ee4b39d05e800882471cdb08d90ab2140066b9e74050d7bc50caccdc7968. Docker and official Linux CI retain identical20 production-overlay order after unchanged19 predecessors and five CI-only overlays; the new move-pause-resume-wire runs once with timeout25. All261 C production source pins reproduce manifestf1ace337091ddd5fac3ae7dd3e28d692f7a5f4e302517d84b616f70426e46204; only the reviewed four C files differ from C19. Native32 production and31 predecessor patch hashes are unchanged; the sole changed Native patch contains the previously reviewed cancellation-fixture correction. Frozen manager five-file pins, Control0.10.21 and every unrelated source/config byte are retained.

Planned Speakers0.26.45 is now ready only for independent exact export/composition closure and the mandatory official candidate CI. No remote mutation, manual CI dispatch, provider/HA/device command, restart or installation occurred in packaging. Root owns the combined PodVoice81/Speakers45 delivery; physical sound, volume, timeline and all three acceptance cycles remain open on the installed candidate. Source, CI, publication, installation and physical acceptance stay separate.


Lead continuation — failed START retirement51, planned build, NOT accepted.
Lead root; Native source/test owner native_idle_owner; repo integration owner
sol61_connect_c_resume; independent adversarial source and composition review required.
The exact base is main32d09ee6340a263f493cd0a1b90eb8c210a70622 /
treefefe7c4014b1810b9bb5ca0fcf9b7d30ec8420ea, including the50 late-HTTP fixture
teardown fence and per-architecture publisher cache. Installed50 startup recovery is
separate: its replacement Native actor answered one passive status read while stopped.
That read is no Connect, output or acoustic acceptance.

Direct controlled evidence on exact50 shows a finite source EOF after C COMPLETE:
C's strict HTTP/player/FIFO/AP2 transaction completes, then Native correctly denies
initial playback credit because the actual output is paused. The failed Native
operation remains UNKNOWN. The existing load-failure defer seals this exact actor
and calls stopPlayback -> beginStopRetirement before closing its writer, but normal
retirement currently requires Native semantic State=complete. It therefore rejects
that failed actor despite its retained strict C-completed physical receipt. This
cleanup coupling is a concrete source fault; attribution of the installed49 final
custody failure remains unproven.

Authorized chain: failed exact published START -> teardown-only current custody
check -> existing C retirement prepare/FULL closed gate -> actual writer/decoder
close and join -> existing C terminal completion -> exact old custody release ->
later authenticated owner admission. Preserve semantic UNKNOWN/no initial-load
credit, generic UNKNOWN refusal and all C admission. Bind the failed operation,
closing actor/player/session, registered writer/pipe and generation; recheck after
awaits so delayed cleanup cannot touch a successor. Missing/unknown C completion,
foreign custody, changed writer/generation, prepare failure and incomplete terminal
proof remain fenced. The existing closed-prior path must independently prove an
already closed old writer, not infer death from an alias or an elapsed timer.

One falsifiable hypothesis: the semantic-completion requirement prevents a valid,
retirement-only physical cleanup and strands future Connect admission after a
legitimate failed load. Reuse the existing real Native+C fixture and actual load
caller: EOF -> final verification refusal -> failure defer -> retirement prepare
before Stop -> close/join -> terminal retirement, then a fresh owner; add the nearby
stale/foreign/UNKNOWN/persist-failure negatives. Reuse passed unchanged checks.
Non-goals: EOF playback allowance, automatic next/play/retry, timeout tuning, a new
protocol/worker/endpoint, startup-auth recovery, C production/admission, manager,
Control or UI changes. No field retry, journal reset or device/provider effect.

Final source and controlled evidence: independent reviewer sol61_review74_final
supplies SOURCE and evidence GO for production3511cc139213b879ecb390608a0e6d510bb7384583639ac927830a1ba98f9af3
and full six-file Native packetb6f68030991f5b28a070c49f22df19396f5e5206b5903a32bd8090319d1b3bd7.
The correction admits only retirement cleanup of the exact failed published
C-completed START with current player/session/registered writer and captured
writer generation; it does not change semantic UNKNOWN or playback credit.
Original RED is retained. Focused race regression PASS1.062s covers UNKNOWN,
malformed/foreign custody, worker/generation drift, unjoined writers and delayed
prepare/completion against a successor. Exact actual Native+C outer-load-failure
regression PASS Native0/C0 in2.21s: C pending/FULL precedes Stop/Close and join;
terminal completion/FULL advances the input epoch, then the same authenticated App
opens a paused successor writer at generation+1. Delayed old completion has no
effect and the next proper C START state is ready. This proves a guarded writer
successor, not a second authenticated Spotify transfer or acoustics. Normal paused
and playing real-C proofs are reused because playback guards are unchanged.
Evidence receipt472edb86e0ed47f7f88cff48ca4a75d9ff49fbdb71e8d2d678be559bd88847c8
binds the source, race binary and actual logs. Installed49 failure attribution
remains unknown; no field EOF diagnosis is claimed.

Exact packaging folds this source into existing Native34 cumulative overlay
c72790365d270fc32bdbc174f53d2e2d611360305f005c5ccb30ddb2a3b06fba;
all220 Native C/Go/header source files byte-match the final owner source after replay
and all33 predecessor overlays remain unchanged. Existing C CI overlay becomes
820599e04df0737e8f54669d95397de7af303051dbcd27f16f4487db24301dbc
by applying reviewed fixture-only4ad108de2d8a894ff22d5024f42052ee6a9c0a82539a7fa18d850e745b2bf117
after exact final50 complete CI fixture. Earlier tests and the late-HTTP teardown
fence are preserved. All229 C production files, manager, Control21, Docker build
inputs and the per-architecture cache workflow are unchanged. No patch slot or
protocol is added. Cumulative Native and C fixture replay, source pins and diff
whitespace checks pass; no broad local gate is rerun.

Speakers0.26.51 is frozen for final independent composition/hash closure and the
mandatory official CI. Rollback is this Native successor overlay, its inert fixture
and release metadata. No remote mutation, provider/HA/device action, publication,
installation or host reboot occurs in packaging. Root owns one combined PV82/PC51
delivery; official CI, installation and physical acceptance stay separate and open.
