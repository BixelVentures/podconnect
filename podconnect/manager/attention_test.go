package main

import (
	"testing"
	"time"
)

func TestAttentionEngageHoldsAtLevel(t *testing.T) {
	t0 := time.Unix(0, 0)
	var a attention
	a.engage(5, 30, "voice", 2*time.Second, t0)

	hold, lvl, released, _ := a.tick(t0.Add(500 * time.Millisecond))
	if !hold || lvl != 5 || released {
		t.Fatalf("within ttl: want hold=true lvl=5 released=false, got hold=%v lvl=%d released=%v", hold, lvl, released)
	}
}

func TestAttentionClampsLevel(t *testing.T) {
	t0 := time.Unix(0, 0)
	var a attention
	a.engage(140, 30, "voice", time.Second, t0)
	if _, lvl, _, _ := a.tick(t0); lvl != 100 {
		t.Fatalf("level should clamp to 100, got %d", lvl)
	}
}

func TestAttentionAutoReleaseOnExpiry(t *testing.T) {
	t0 := time.Unix(0, 0)
	var a attention
	a.engage(5, 30, "voice", time.Second, t0)

	// Past the deadline: one release edge with the captured restore level, then quiet.
	hold, _, released, restoreTo := a.tick(t0.Add(2 * time.Second))
	if hold || !released || restoreTo != 30 {
		t.Fatalf("expiry: want hold=false released=true restoreTo=30, got hold=%v released=%v restoreTo=%d", hold, released, restoreTo)
	}
	if h, _, rel, _ := a.tick(t0.Add(3 * time.Second)); h || rel {
		t.Fatalf("after release edge: want hold=false released=false, got hold=%v released=%v", h, rel)
	}
}

func TestAttentionExplicitRelease(t *testing.T) {
	t0 := time.Unix(0, 0)
	var a attention
	a.engage(5, 42, "voice", time.Minute, t0)
	a.release()

	hold, _, released, restoreTo := a.tick(t0.Add(time.Second))
	if hold || !released || restoreTo != 42 {
		t.Fatalf("explicit release: want hold=false released=true restoreTo=42, got hold=%v released=%v restoreTo=%d", hold, released, restoreTo)
	}
}

func TestAttentionHeartbeatExtendsAndPreservesPrev(t *testing.T) {
	t0 := time.Unix(0, 0)
	var a attention
	a.engage(5, 30, "voice", time.Second, t0)

	// A heartbeat near expiry, captured from the ALREADY-DUCKED level (5) — must NOT overwrite the
	// real pre-duck level (30), and must push the deadline out.
	a.engage(5, 5, "voice", time.Second, t0.Add(900*time.Millisecond))

	// Still holding at the original deadline (extended past it).
	if hold, _, _, _ := a.tick(t0.Add(1100 * time.Millisecond)); !hold {
		t.Fatalf("heartbeat should have extended the deadline; expected still holding")
	}
	// Now let it expire — restore must be the original 30, not the heartbeat's 5.
	if _, _, released, restoreTo := a.tick(t0.Add(2200 * time.Millisecond)); !released || restoreTo != 30 {
		t.Fatalf("after heartbeat expiry: want released=true restoreTo=30, got released=%v restoreTo=%d", released, restoreTo)
	}
}

func TestClampAttentionTTL(t *testing.T) {
	cases := []struct {
		name string
		req  time.Duration
		want time.Duration
	}{
		{"zero falls back to default", 0, defaultAttentionTTL},
		{"negative falls back to default", -5 * time.Second, defaultAttentionTTL},
		{"in range passes through", 5 * time.Second, 5 * time.Second},
		{"exactly max passes through", maxAttentionTTL, maxAttentionTTL},
		{"over max clamps", time.Hour, maxAttentionTTL},
	}
	for _, c := range cases {
		if got := clampAttentionTTL(c.req); got != c.want {
			t.Fatalf("%s: clampAttentionTTL(%v) = %v, want %v", c.name, c.req, got, c.want)
		}
	}
}

func TestAttentionSnapshot(t *testing.T) {
	t0 := time.Unix(0, 0)
	var a attention
	if s := a.snapshot(t0); s.Active || s.RemainingMS != 0 {
		t.Fatalf("zero value should be inactive, got %+v", s)
	}
	a.engage(5, 30, "voice", 2*time.Second, t0)
	s := a.snapshot(t0.Add(500 * time.Millisecond))
	if !s.Active || s.Level != 5 || s.Owner != "voice" || s.RemainingMS != 1500 {
		t.Fatalf("active snapshot wrong: %+v", s)
	}
}
