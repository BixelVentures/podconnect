# Multi-account — scope, what already belongs here, and the fix

> **Superseded + currently not supported (June 2026) — historical.** Multi-room is now PodConnect's
> **device-aliases** (default, zero setup): several selectable rooms in the Spotify Connect menu **on
> one account** — see [`ALIASES-PROBE.md`](ALIASES-PROBE.md) and [`../podconnect/DOCS.md`](../podconnect/DOCS.md) §6.
> That architecture is **single-engine** (one go-librespot for the whole household), so the scenario
> this doc describes — **different people on different Spotify accounts playing different rooms at the
> same time** — is **no longer possible**: it relied on the per-room multi-engine model, which was
> **removed in 0.25.0**. Re-enabling it would mean reverting to per-room engines (and the contention
> that caused). Account-agnostic *stop* still works via the panel Stop / Siri. The rest below is kept
> as design history.

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
