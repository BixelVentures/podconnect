# Multi-account — scope, what already belongs here, and the fix

> **Superseded + currently not supported (June 2026) — historical.** Multi-room is now PodConnect's
> **device-aliases** (default, zero setup): several selectable rooms in the Spotify Connect menu **on
> one account** — see [`ALIASES-PROBE.md`](ALIASES-PROBE.md) and [`../podconnect/DOCS.md`](../podconnect/DOCS.md) §6.
> That architecture is **single-engine** (one go-librespot for the whole household), so the scenario
> this doc describes — **different people on different Spotify accounts playing different rooms at the
> same time** — is **no longer possible**: it relied on the per-room multi-engine model, which was
> **removed in 0.25.0**. Re-enabling it would mean reverting to per-room engines (and the contention
> that caused). **Workaround for simultaneous:** a second person can **AirPlay from their iPhone
> directly** to a HomePod PodConnect isn't currently using (native iOS, outside PodConnect). Note: a
> "multi-account *via voice*" router was only ever a deferred plan — it was **never built**.
> Account-agnostic *stop* still works via the panel Stop / Siri. The rest below is kept as design history.

## THE one viable path to simultaneous multi-account/multi-room (research 2026-06-28)

> Outcome of a deep multi-agent research pass (16 ideas, adversarially verified, web-researched). If
> simultaneous "different people, different accounts, different rooms, at once" is ever wanted, **this
> is the only architecture that survives the hard constraints** — and it is gated on exactly ONE
> unproven thing. Captured so it isn't re-derived from scratch.

**It is structurally POSSIBLE, not blocked.** The physics (Spotify/AirPlay behaviour, branch-independent):
1. Spotify enforces **one stream per ACCOUNT**, server-side. Two *different* accounts = two independent
   streams → **no cross-account contention** (it's free).
2. **librespot #793 (flip-flop) is SAME-account-only.** It killed multiple engines *on one account*.
   Different-account multi-instance was never the trigger.
3. **AirPlay PTP port-319 exclusivity is a RECEIVER rule** (shairport-sync), **not a sender rule.**
   OwnTone-as-sender binds *ephemeral* timing/control ports per session → N OwnTone **senders** don't
   fight over 319.

**The only viable architecture — hybrid (alias + on-demand guest engine):** keep the shipped
single-engine **device-aliases** for the PRIMARY account (clean room-switching, as today); when a
**second account** claims a free HomePod, the manager lazily spawns a **dedicated (go-librespot +
OwnTone) pair** bound to that room + account (different account ⇒ no #793), and the alias router yields
that HomePod. Reap on idle; hard slot ceiling (2–3 rooms ≈ 150–350 MB on an HA Green). Arbitration to
build: one HomePod owned by *either* the alias-route or its guest engine (never both); a
one-account-per-engine guard so the same account never bids on both an alias and a standalone for one
room.

**Why this is reachable NOW (and wasn't before):** the three fixes that made the old per-room model
ugly — **graceful SIGTERM teardown** (no ghost Connect entries), **pinned avahi host-name**, **raised
dbus objects-per-client-max** — are **already on `main`** (0.24.x). So the churn/duplicate problems that
killed per-room are already neutralised; the hybrid starts on good ground.

**The ONE load-bearing unknown (everything hangs on it):** does **AirPlay-2 PTP sync hold across 2+
simultaneous OwnTone senders?** OwnTone's own multi-instance guidance needs **one shared `airptpd` /
`nqptp` started before any instance** — and it is **not in our image** (confirmed). This is the only
thing that can kill the hybrid.

**The decisive, cheapest experiment (gates the whole decision — zero manager refactor):** add `airptpd`
to the image; run **two hand-written OwnTone configs** (unique ws/library/mpd ports + db paths, each
fed a test tone via pipe); point A → one HomePod, B → another; run 10+ min and check both HomePods play
their own audio **in sync** with only `airptpd` bound to 319/320 (`ss -ulpn`). **Sync holds → build the
hybrid. Drift/dropouts → simultaneous multi-account isn't practical with OwnTone senders → iPhone-AirPlay
stays the answer.**

**Dead-ends (the constraint that kills each):** multiple engines on the *same* account (#793 flip-flop);
abandoning the alias model (loses clean single-account switching); one Spotify account per room
(defeats "pick from your own app"); Spotify Jam (shared queue, not different music per room).

**Until that bench is green, the honest status is unchanged:** simultaneous multi-account is a
*validated-but-unbuilt* option (not a blocked one); the workaround today is native iPhone-AirPlay to a
free HomePod. _(Note: a prior research pass mis-read a stale 0.14.0 worktree and reported the per-room
model as "already shipped" — it is not; `main` is alias-only. The physics above were re-verified
against `main` 0.25.2.)_

---

The trigger: a Spotify **Family plan = N independent accounts**, but the PodConnect speakers
(HomePods) are **shared hardware**. Each go-librespot Connect device can be played by *any* account on
the LAN (whoever picks it in their Spotify app authenticates go-librespot with their account). That
mismatch — shared speakers, per-account control — is what "multi-account" means here.

## The issues (what's genuinely a multi-account problem)
1. **Per-account control.** Control = one OAuth = one Spotify account. Another person's playback on a
   speaker is **invisible** to your Control (the Web API only sees *your* devices/session).
2. **Stopping / taking over another account** — "my wife is playing, I want to stop it." Can't via
   your own Web API (her session isn't yours). ← this is a multi-account problem, not a generic Stop.
3. **Voice has no identity.** HA Assist runs every command through **one** configured account, whoever
   speaks. "Play X" always uses that account's library/recs.
4. **New playback's account.** When HA/Assist starts playback, *whose* account (library, Discover
   Weekly, history) is used?
5. **Device visibility varies per account** (a zeroconf device surfaces in an account's Connect list
   after that account interacts with it).

## Already built that ACTUALLY belongs to multi-account (the "house" layer)
These exist *because of* multi-account and should be understood as the **account-neutral house
controls**, not generic features:
- **⏹ Stop** (`/api/stop`) and **⏏ Release** (`/api/release`) in the **add-on panel**. They talk to
  go-librespot/OwnTone **locally**, so they work **regardless of which account is playing** — that is
  precisely the answer to "stop my wife's music." (Single-account homes rarely need them; their
  unique value is cross-account.)
- The **add-on / audio layer is account-neutral** by design: accounts only matter at the
  Spotify-*control* layer (Control), never at the speaker/audio layer (the add-on).

Everything else recently shipped (search, audiobooks, browse, transport, volume sync, multi-room,
snappy skips) is **general** — it works the same with one account or five.

## The fix (deferred build — now scoped)
- **Control: multiple config entries** — one per family member, each its own Application-Credentials
  OAuth. The dev app's User Management covers the family (5 users in dev mode; more via a quota
  extension). Each entry surfaces *that person's* devices + playback as media_players. A speaker is
  then controlled by whoever's account is active on it.
- **House controls stay in the add-on** (Stop / Release / pick HomePod) — account-neutral, shared,
  the cross-account "stop/give-up the speaker" layer. No per-account duplication.
- **Voice routing:** unattributed Assist commands use a designated **primary** account; explicit
  naming routes to a specific one ("play X **from Mum's**"). Per-satellite → per-account mapping is
  the polish.
- **Don't break single-account:** multiple entries is purely additive — one entry = today's exact
  behavior.

## Reality check (HISTORICAL — no longer true under single-engine)
This section described the **old per-room model**: each room was its own go-librespot, so different
people could play different music to different rooms simultaneously for free. Under the current
**single-engine** device-aliases architecture there is **one** go-librespot for the household, so that
simultaneous multi-account playback is **not** available (everything routes through the one engine /
one active account). Account-agnostic *stop* still works via the panel Stop / Siri.

So the deferred "multi-account" build (multiple Control entries) adds **only one thing**: letting
**Home Assistant** see / control / automate *each person's* Spotify (a dashboard of who's playing
what, voice/automation routed to a specific account). For a **phone-first household** that's low
value and may be skipped indefinitely. Build it only if HA-level cross-account visibility/automation
is genuinely wanted. (Spotify **Jam** covers the "listen together / co-control" case socially: if one
starts a Jam and another joins, they share the session — and the joiner's HA Control then reflects
it, because they're in the session.)

## Why it's still deferred
It needs the engine to be solid first (multi-room validated on hardware, push-state). This doc is the
scoped plan so the multi-account build is ready to pick up without re-deciding the shape. The
account-neutral house controls (panel Stop/Release) already cover the urgent "stop the wife" case in
the meantime.
