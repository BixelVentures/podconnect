// Attention API (Wave 4): an external agent — e.g. a voice assistant gatekeeper — can "duck" a
// room's music to a quiet level while it talks, then release it. The duck is an ABSOLUTE, idempotent
// target with an OWNER and a DEADLINE:
//
//   - re-POSTing the same room extends the deadline (a heartbeat), so an open conversation just keeps
//     refreshing it;
//   - if the heartbeat stops (agent crash, Wi-Fi drop, killed session) the duck AUTO-RELEASES at the
//     deadline — the music can never get stuck quiet, and the agent never has to "clean up";
//   - while a duck is active the roomBridge volume reconcile is SUSPENDED (the duck WINS): the HomePod
//     is held at the target and a HomePod button or the Spotify mirror can't fight it, but transport
//     keeps running so the music plays quietly underneath;
//   - on release/expiry the pre-duck HomePod level (captured at first engage) is restored, and the
//     reconcile re-seeds so it re-converges on the live truth.
//
// No fade in v1: the duck-down is instant (you want the music to drop the moment you speak) and the
// restore is instant too. fade_ms is accepted by the API and reserved for a later smooth-fade pass.
package main

import (
	"sync"
	"time"
)

// defaultAttentionTTL is the auto-release window when a request omits ttl_ms. Short on purpose: an
// agent must heartbeat (re-POST) to hold the duck, so a dead agent's duck clears within ~2s.
const defaultAttentionTTL = 2 * time.Second

// maxAttentionTTL caps any requested ttl_ms. The whole watchdog rests on "hold only as long as you
// heartbeat" — without a ceiling, a single large-TTL request followed by a crash would pin the music
// quiet for that whole window, defeating the auto-release. 15s comfortably covers the longest
// intended window (the lounge layer's ~8s) while keeping a dead agent's worst case bounded.
const maxAttentionTTL = 15 * time.Second

// clampAttentionTTL resolves a requested ttl (from ttl_ms) into the actual hold window: <=0 falls back
// to the default, and anything over the ceiling is clamped to it.
func clampAttentionTTL(req time.Duration) time.Duration {
	if req <= 0 {
		return defaultAttentionTTL
	}
	if req > maxAttentionTTL {
		return maxAttentionTTL
	}
	return req
}

// attention is one room's duck state. The zero value is inactive (the detached fallback for an
// unsupervised room). All access is mutex-guarded, like glLive.
type attention struct {
	mu             sync.Mutex
	active         bool
	pendingRelease bool      // a release/expiry the bridge hasn't restored from yet (consumed by tick)
	level          int       // target % held on the HomePod while active (e.g. 5 = ducked, 35 = lounge)
	prevLevel      int       // HomePod level captured at first engage; restored on release (-1 = unknown)
	owner          string    // who holds the duck ("voice"); informational, surfaced in the snapshot
	deadline       time.Time // auto-release time; extended by every engage (heartbeat)
}

// attSnapshot is a lock-free view of a room's duck state for the HTTP layer.
type attSnapshot struct {
	Active      bool   `json:"active"`
	Level       int    `json:"level"`
	Owner       string `json:"owner"`
	RemainingMS int    `json:"remaining_ms"` // ms until auto-release (0 when inactive)
}

// engage sets or refreshes the duck. capturePrev is the current HomePod level to restore later; it's
// recorded ONLY on the first engage (so a stream of heartbeats can't overwrite the real pre-duck
// level with an already-ducked one).
func (a *attention) engage(level, capturePrev int, owner string, ttl time.Duration, now time.Time) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if !a.active {
		a.prevLevel = capturePrev
	}
	a.active = true
	a.pendingRelease = false
	a.level = clampPct(level)
	a.owner = owner
	a.deadline = now.Add(ttl)
}

// release ends the duck now. The next tick reports the release so the bridge restores the pre-duck
// level. A no-op if not currently active.
func (a *attention) release() {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.active {
		a.active = false
		a.pendingRelease = true
	}
}

// tick reports the duck's effect for one bridge cycle at time now, folding in auto-expiry. It's the
// single point the bridge consults each loop:
//
//	hold      - duck active: force the HomePod to `level` and skip the reconcile this tick.
//	level     - target % while holding.
//	released  - the duck just ended this tick (explicit release or deadline expiry); fired ONCE.
//	restoreTo - the pre-duck HomePod level to restore on release (-1 = unknown, leave as-is).
//
// Pure given (state, now) — unit-tested without I/O.
func (a *attention) tick(now time.Time) (hold bool, level int, released bool, restoreTo int) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.active && now.After(a.deadline) {
		a.active = false
		a.pendingRelease = true
	}
	if a.active {
		return true, a.level, false, 0
	}
	if a.pendingRelease {
		a.pendingRelease = false
		return false, 0, true, a.prevLevel
	}
	return false, 0, false, 0
}

// snapshot returns the current state for the API (and the panel later).
func (a *attention) snapshot(now time.Time) attSnapshot {
	a.mu.Lock()
	defer a.mu.Unlock()
	s := attSnapshot{Active: a.active, Level: a.level, Owner: a.owner}
	if a.active {
		if d := a.deadline.Sub(now); d > 0 {
			s.RemainingMS = int(d / time.Millisecond)
		}
	}
	return s
}
