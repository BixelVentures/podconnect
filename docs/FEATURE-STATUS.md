# PodConnect — Feature Status (canonical)

## Current candidate — Speakers 0.26.5 (2026-10-08)

Delayed manager status/catalog replies and alias reclaim now preserve the latest
room selection. Normal manager tests, race checks, vet, Linux arm64/amd64 builds
and eight composed bridge regressions passed on this candidate. Installation and
physical acceptance are pending. This does not establish prompt Spotify visibility,
native playback/apply, voice-driven transfer or per-output duck restoration.
The dated feature claims below are historical evidence, not acceptance for this candidate.

Single source of truth for **what each feature does** and **whether it works**. Keep in sync with
[`CHANGELOG.md`](../CHANGELOG.md).

_Last updated: 2026-06-28 — Speakers add-on **0.24.11**, Control integration **0.10.0**._

**Legend:** ✅ working (verified on-device) · ⚪ should work per code, not re-tested this round · ⏳ planned.

---

## Headline (June 2026): multi-room on ONE account works — and it's the default

A **single** go-librespot engine advertises all your rooms as **separate selectable devices in the
Spotify Connect menu, on one account**. Pick a room in the Spotify app → the audio routes to that
HomePod (~1–2 s, AirPlay's switch). Proven on-device — the long-sought "several rooms, one account,
clean audio, switch from the Spotify app." This is now the **only** mode: the per-room multi-engine
model and the `persistent_connect` / `experiment_aliases` experiments were removed. See
[`ALIASES-PROBE.md`](ALIASES-PROBE.md).

**Limits (by design, single-engine):** it's **one stream — one room at a time** (picking a room *moves*
the music). PodConnect does **not** play two rooms at once, and a second person **cannot** play a
different room simultaneously via PodConnect (one engine = one account at a time; picking takes over).
Workaround for simultaneous: a second person AirPlays from their iPhone directly to a free HomePod
(native iOS, outside PodConnect). Different-music-per-room and synced same-music groups are not features;
"multi-account via voice" was never built. See [`MULTI-ACCOUNT.md`](MULTI-ACCOUNT.md).

It is **reachable** (not blocked) via a documented **hybrid** (alias for the primary account +
on-demand guest engine per 2nd account), gated on one bench test — does AirPlay-2 PTP sync hold across
two simultaneous OwnTone senders (needs a shared `airptpd`, not yet in the image). Full research + the
one decisive experiment: [`MULTI-ACCOUNT.md`](MULTI-ACCOUNT.md) § "THE one viable path".

---

## A. Audio bridge — volume
| Feature | Status |
|---|---|
| Bidirectional sync — Spotify/HA slider ↔ HomePod hardware buttons, one canonical value (±2% tolerance) | ✅ |
| Sane start — device advertises `initial_volume` (~35%) so the Spotify slider never shows 100% on a fresh claim | ✅ (0.24.4) |
| Never-loud cap — fresh / transferred / reclaimed sessions can't blast; re-arms across transfer + reclaim drift | ✅ (0.22.4–0.22.5) |
| `external_volume:true` — loudness lives in OwnTone's AirPlay output (no double-attenuation) | ✅ |

## B. Audio bridge — transport, grace, duck
| Feature | Status |
|---|---|
| Transport sync (play/pause), startup-aware, rapid-tap-safe | ✅ |
| Grace-release + reclaim on resume (restores your level; in alias mode re-routes to the selected room) | ✅ |
| Attention/duck API (`/api/attention`, heartbeat + auto-release) | ⚪ (unchanged since 0.14.0) |
| Next-track speed — floor is AirPlay's ~2 s; `buffer_ms` tunes the OwnTone re-buffer | ✅ (tunable) |

## C. Spotify Connect engine (go-librespot — our fork)
| Feature | Status |
|---|---|
| **Device-aliases (the only mode)** — one engine advertises N rooms as Connect-menu aliases; selection (`target_alias_id` at payload top level) routes output to that room; pushed instantly over `/events`. Single-engine; stable device_id | ✅ (0.24.6–0.25.0) |
| Fork built from source (multi-stage slim image); patch `podconnect/patches/aliases-v0.7.3.patch`; CI compile-guard | ✅ |
| Graceful restart (SIGTERM → withdraws zeroconf cleanly → no duplicate Connect entries) | ✅ (0.24.9) |
| avahi host-name pinned + `objects-per-client-max` raised (no rename churn / dbus flood) | ✅ (0.22.2 / 0.24.7) |
| Health-restart watchdog; `/events` push-state + `/status` poll fallback (1 s reseed, also carries alias) | ✅ |

## D. Room lifecycle & panel
| Feature | Status |
|---|---|
| Add / Remove / Rename / per-room ⚙ Settings (grace + bitrate), live, no restart | ✅ |
| Add-speaker in alias mode re-advertises on the primary (no rogue 2nd engine) | ✅ (0.24.8) |
| Panel shows alias rooms as `alias` (not a dead "starting…") | ✅ (0.24.2) |
| Self-healing naming (stable id; Apple-Home rename syncs) · Test tone · Stop / Release | ✅ |

## E. Control integration (HACS, Spotify Web API)
| Feature | Status |
|---|---|
| One `media_player` per Web-API Connect device; ~10 s poll; transport/volume/shuffle/repeat/transfer; optimistic UI | ⚪ |
| Search + Browse (Playlists/Top/Recent/Liked), provider-ordered within each type, spoken content | ⚪ |
| `media_player.play_media` accepts a free-text name → search + play top result (0.8.0) | ⚪ |
| `podconnect.play_from_library` (liked/top/recent, action) (0.9.0) | ⚪ |
| `podconnect.top_tracks` / `recently_played` / `liked` — response-returning data services for an AI assist (0.10.0) | ⚪ |

## Known noise / follow-ups
- ✅ `/events` ws reconnect log churn (`StatusNoStatusRcvd` ~every 31 s) — **fixed (0.25.2):** the ws
  reader now extends its read deadline on keepalive PONG/PING, so an idle stream no longer times out.
- ✅ Self-healing-on-rename dead code (`selectHomePod`/`healBinding`) — **removed (0.25.2).** `matchOutput`'s
  id-match survives most Apple-Home renames; if a rename ever breaks routing, re-add a heal-only path
  (must heal against the PRIMARY OwnTone for all rooms, since only one engine runs).
- Surface the alias rooms more clearly in the panel.
- Synchronized same-music groups across rooms — not built (OwnTone multi-output is the likely path).


## Active Control 0.10.2 decision — 2026-09-27

Lead approved bounded optimistic-state correction. Observed source defect: successful
Spotify API acceptance can be followed by dealer rejection or a contradictory poll,
but optimistic play/shuffle/repeat previously persisted until values matched. This
candidate must replace intent with a successful poll begun after command completion.
An already-running poll is not post-command evidence; a failed poll is not confirmation.
Use monotonic poll-start sequence and command generation, with no timeout tuning.
Non-goals: Spotify engine/discovery changes, transport replay, and physical playback proof.
Regression plan: contrary post-command truth, pre-command in-flight response, failed
poll, device transfer, overlapping commands and rejected commands. Roll back Control
only if HA integration validation fails. Not installed/released/test-ready yet.

Implemented candidate: poll-start sequence is stamped before network reads; a completed
command records the latest started poll. A successful later sequence replaces all
optimistic fields even when Spotify contradicts the command or changes active device.
Failed/pre-command polls do not confirm anything. Command generation prevents an older
completion from clearing a newer in-flight intent; network/API errors clear unconfirmed
intent. Thirteen Python 3.12 unit regressions passed on 2026-09-27, executing the shipped
methods with mocks (including the actual coordinator fetch method). No running-HA or
physical playback/discovery proof; independent review and integration checks pending.

Lead additionally approved source-order search repair and truthful library/search
read errors. Synthetic reproduction: a source-first requested artist's Halo (60)
was demoted below a different artist's Halo (99) by title-only/popularity reranking.
Preserve Spotify order within each result type; do not invent new relevance policy.
Failed library reads must raise service errors, not masquerade as empty tracks.
No prompt/tool-surface changes; regressions execute the actual service/search methods.

Final local evidence: 17 Python 3.12 tests passed after search/read additions and
cancelled-command cleanup. Search regressions preserve provider-first requested artist
against a more popular cover in both direct and Assist paths. Library tests distinguish
legitimate empty results from provider failure and missing account. These are shipped
method/service-registration tests with mocked boundaries, not running-HA integration
or physical audio/discovery evidence. Candidate remains uninstalled; independent review
pending. Speakers code/version is unchanged in this Control-only candidate.

Independent final review27/9: separate reviewer GO, no P0/P1. Reviewer independently
ran all17 Python tests and Node Stop-feedback regression successfully. Control0.10.2
manifest/changelog included in review. These are shipped-method tests with mocked
HA/provider boundaries, not physical discovery or room-behaviour proof.

Installation27/9: PR3 merged as43609d8d7300daa96dfb5bbc631472e8da86ba2f;
GitHub releasev0.10.2 published for that commit. HA update UI confirms installed
v0.10.2, then Core restart requested and browser reconnected. Speakers remains0.26.2.
Physical playback/search verification is not yet obtained. Voice PE separately lost
its native connection at12:33:58 and remained undiscoverable in inspected HA logs;
this Control update neither explains nor repairs that device/network condition.


## Connect disappearance investigation — 2026-09-27

Read-only investigation; no runtime/configuration change and no claimed fix.
Installed Speakers0.26.2 verified Running in HA; start-on-boot and watchdog both ON.
Panel retains primary Kitchen + Svend/Frida aliases. Primary released is AirPlay
idle-release state, not evidence that Spotify Connect was withdrawn.

Fresh HA log evidence (local time): startup11:52:21; Avahi registration11:52:22;
persisted credentials loaded and AP/Login5 authenticated; alias1 routed11:52:26.
At15:18:23 AP pong absent120s, connection closed, one AP connection refused,
then authenticated AP. At15:35:40 and15:45:13 peer reset followed by authenticated
AP. No manager health-restart is present in this displayed post-install excerpt.
AP authentication alone does not prove Dealer, Connect state publication or mobile
visibility. Startup also warns about another IPv4/IPv6 mDNS stack; this is a lead,
not causal proof. Logs shown cover the displayed interval, not all historical use.
At17:00:53 local DNS-SD browse sees Kitchen's _spotify-connect._tcp advertisement;
at17:01:14 resolve returns podconnect.local:37427, matching the startup log port.
Therefore advertisement is present now. Sibling rooms are cloud aliases of one
engine, not independent local mDNS advertisements; loss of that engine can affect
all aliases. Spotify mobile visibility is not independently observed.

Source findings in shipped upstream c191a43 (v0.7.3) + alias patch:
1. manager/supervisor.go glHealthy only checks local /status HTTP <500. A204
(no session), or200 from a stranded player, is healthy under that predicate.
It is a responsiveness watchdog, not a Spotify-session/discovery watchdog.
2. Dealer/AP reconnect eventually close receiver channels on exhausted retry.
daemon/player.go Run continues on closed AP/message/request channels instead of
ending the dead session. This permits a live local event loop without functioning
remote transport; status handling does not validate Dealer/AP. Code-supported
failure path, NOT reproduced in the field log above.
3. zeroconf/backend_avahi.go registers once and has no service-owner/state listener
to re-register after Avahi/D-Bus restart. A process that stays alive after advertiser
loss has no demonstrated advertisement recovery. Not observed happening today.
4. HA watchdog probes OwnTone TCP3689 only. It cannot establish Spotify readiness.
5. manager child exits are respawned; HTTP hangs trigger restart after warmup and
three30s checks. These existing protections do not cover every cloud-session failure.
6. Existing output-selection retry repairs downstream HomePod rejection; it cannot
repair a missing upstream Connect session. No playback command may be replayed on
unknown outcome. Whole-addon shutdown still hard-kills children whereas restartGL
uses graceful termination; review advertisement cleanup in controlled restart tests.

Upstream releases after the pinned version mention Avahi renaming hardening and
track audio-key failure recovery: https://github.com/devgianlu/go-librespot/releases .
Neither release note establishes a fix for this user's disappearance. Avoid a blind
version upgrade of the alias fork. Upstream issue300 describes Connect-state timeout
and unrecovered playback, but today's log does not show that causal chain.

Next bounded implementation decision: expose actual transport/session/registration
health, end exhausted sessions through their existing owner, and let supervision
recover only a confirmed failed generation. Verify healthy idle/no-user states are
not restarted, short disconnects recover without resets, sustained failure recovers
without replay, advertiser restart restores same identity, and next room selection
works. Then same-artifact HA reboot/network recovery and Spotify mobile visibility
are required before claiming disappearance fixed. No gain/buffer/Wi-Fi tuning is
justified by the evidence collected here.

## Active decision — exhausted transport and Avahi recovery — 2026-09-27

Owner: PodConnect reliability implementation, reviewed independently before release.
Code evidence above establishes terminal closed-channel spin and one-shot Avahi
registration; today's recovered AP resets do not establish either as the field cause.
Repair the pinned engine at these boundaries: terminal transport failure enters an
error-only API loop (no command replay), allowing the existing manager watchdog to
replace that process; ordinary reconnect stays upstream-owned. Periodic bounded Avahi
registration validation restores the same current name/port/TXT after advertiser loss.
Preserve healthy idle/no-credential sessions. No Spotify search, audio buffer, output
selection, alias identity or credential policy changes. Tests must cover failed API
truth/stop, healthy/no-user HTTP responses, advertisement loss/re-registration and
shutdown fencing. Independent review and same-artifact reboot/network/mobile discovery
proof remain required; this is not yet a released or physically proven fix.

Implementation: a separate recovery-v0.7.3.patch applies after the unchanged alias
patch. Exhausted AP/Dealer channels (and failed initial Dealer connection) enter an
error-only player loop; API commands fail and owner Stop remains accepted. Actual
/status maps failure to HTTP500. The existing three-check watchdog then gracefully
restarts the process; no new retry layer or command replay. Healthy /status200 and
unpaired204 remain accepted, other HTTP errors no longer count as healthy. Avahi uses
a private bus connection, serialized current registration state, bounded two-second
DBus calls/authentication, five-second revalidation, and a shutdown fence. Lost groups
are replaced with current identity/port/TXT; registering/established groups are retained.

Validation: all manager tests PASS using temporary Go1.25.5 and permitted localhost
listeners (first sandbox run could not bind, not a product failure). Linux ARM64 pinned
upstream plus existing alias patch and new recovery patch passed daemon+zeroconf tests,
including actual HTTP500 mapping, rejected play/resume/stop with no session execution,
closed API channel teardown, six Avahi entry-group states, daemon absence/retry, renamed
identity and shutdown fencing. Final Linux ARM64 composed-source rerun after bounded-connect addition PASS:
go test -race ./daemon ./zeroconf; go vet ./daemon ./zeroconf; go build ./cmd/daemon.
The manager complete test suite also passed. No release/install/physical proof.

Independent review: separate reviewer GO, no blocking finding in shipped zeroconf
path; manager tests independently passed and composed recovery source identity checked.
Speakers0.26.3 metadata prepared; Control remains0.10.2. Recovery watchdog is the
existing three30-second failed probes after upstream retries have exhausted (upstream
backoff may itself be lengthy); this does not promise90-second recovery from first drop.
Physical daemon restart, HA reboot and Spotify mobile rediscovery remain unproved.
