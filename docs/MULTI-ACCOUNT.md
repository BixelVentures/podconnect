# Multi-account — scope, what already belongs here, and the fix

> **Mostly superseded (June 2026).** You no longer need multiple accounts for multi-room: the
> **device-aliases mode** (`experiment_aliases: true`) gives several selectable rooms in the Spotify
> Connect menu **on one account**, with clean audio — see [`ALIASES-PROBE.md`](ALIASES-PROBE.md) and
> [`../podconnect/DOCS.md`](../podconnect/DOCS.md) §6. This doc remains relevant only if you genuinely
> want **different people on different Spotify accounts** controlling rooms, with HA-level visibility.

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

## Reality check: the valuable part already works for free
**Multi-account *playback* is NOT a feature we need to build — it's inherent to Spotify Connect.**
Every PodConnect speaker is a zeroconf Connect device, so any family member on the LAN can play to
any speaker from their own Spotify app, and — because each room is its own independent go-librespot —
**different people can play different music to different rooms simultaneously, today, with no extra
setup.** Stopping anyone is the account-neutral panel Stop / Siri.

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
