package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func announceConnect(t *testing.T, f *releasedStartFixture, ordinal float64) glSource {
	t.Helper()
	l := &f.rt.live
	raw, _ := json.Marshal(l.Get().AliasBinding)
	var b map[string]any
	_ = json.Unmarshal(raw, &b)
	source := glSource{run: l.runEpoch, phase: l.phaseEpoch}
	l.applySourceEvent(source, "connect_transfer", map[string]any{"id": float64(1), "ordinal": ordinal, "alias_binding": b})
	return source
}
func TestExplicitConnectRecoveryOneShot(t *testing.T) {
	f, _, _, _ := releasedRoomRouteFixture(t)
	f.selected = false
	old := http.DefaultTransport
	http.DefaultTransport = startTransport(func(r *http.Request) (*http.Response, error) {
		if r.Method == "PUT" && r.URL.Query().Get("recovery") != "connect" {
			t.Fatal("unguarded recovery mutation")
		}
		return old.RoundTrip(r)
	})
	announceConnect(t, f, 1)
	if !tryExplicitConnectRecovery(f.room, &f.rt.live, nil) || !f.selected || f.volume != 35 || fileExists(releasedPath(f.room)) {
		t.Fatal("fresh recovery failed", f.effects)
	}
	n := len(f.effects)
	_ = os.WriteFile(releasedPath(f.room), []byte("1"), 0600)
	announceConnect(t, f, 1)
	if tryExplicitConnectRecovery(f.room, &f.rt.live, nil) || len(f.effects) != n {
		t.Fatal("replayed transfer recovered")
	}
}
func TestExplicitConnectRecoveryInvalidations(t *testing.T) {
	for _, mode := range []string{"no_event", "poll", "old_source", "reseed_changed", "paused", "expired", "failed_prime", "superseded_during_read", "superseded_during_prime", "attention"} {
		t.Run(mode, func(t *testing.T) {
			f, _, _, _ := releasedRoomRouteFixture(t)
			f.selected = false
			l := &f.rt.live
			if mode != "no_event" && mode != "poll" {
				announceConnect(t, f, 1)
			}
			switch mode {
			case "poll":
				source := glSource{run: l.runEpoch, phase: l.phaseEpoch}
				r, _ := l.beginStatus(source)
				l.acceptStatus(r, l.Get())
			case "old_source":
				l.retirePhase(glSource{run: l.runEpoch, phase: l.phaseEpoch})
			case "reseed_changed":
				s := l.Get()
				s.AliasBinding = copyAliasBinding(s.AliasBinding)
				s.AliasBinding.Ready = false
				source := glSource{run: l.runEpoch, phase: l.phaseEpoch}
				r, _ := l.beginStatus(source)
				l.acceptStatus(r, s)
			case "paused":
				l.applyEvent("paused", nil)
			case "expired":
				l.connectRecovery.until = time.Now().Add(-time.Second)
			case "failed_prime":
				f.failPrime = true
			case "superseded_during_read":
				f.onRead = func() { l.applyEvent("paused", nil) }
			case "superseded_during_prime":
				f.onPrime = func() { l.applyEvent("paused", nil) }
			}
			var att *attention
			if mode == "attention" {
				att = &attention{nativeBusy: true}
			}
			tryExplicitConnectRecovery(f.room, l, att)
			if !fileExists(releasedPath(f.room)) || f.selected {
				t.Fatal("invalid recovery selected or cleared marker", f.effects)
			}
			allowed := 0
			if mode == "failed_prime" || mode == "superseded_during_prime" {
				allowed = 1
			}
			if len(f.effects) != allowed {
				t.Fatalf("unexpected effects %v", f.effects)
			}
			n := len(f.effects)
			tryExplicitConnectRecovery(f.room, l, att)
			if len(f.effects) != n {
				t.Fatal("automatic replay")
			}
		})
	}
}
func TestConnectRecoveryConsumedTransportOwnerCannotChange(t *testing.T) {
	f, _, _, _ := releasedRoomRouteFixture(t)
	l := &f.rt.live
	announceConnect(t, f, 1)
	credit, ok := l.consumeConnectRecovery()
	if !ok {
		t.Fatal("missing credit")
	}
	l.applyEvent("paused", nil)
	if l.startIntentCurrent(credit.transport) {
		t.Fatal("consume captured later owner")
	}
	if len(f.effects) != 0 {
		t.Fatal("effect")
	}
}

func interceptRecoveryPrime(t *testing.T, reject func(*http.Request) *http.Response) *int {
	t.Helper()
	previous := http.DefaultTransport
	calls := new(int)
	http.DefaultTransport = startTransport(func(r *http.Request) (*http.Response, error) {
		if r.Method == "PUT" && r.URL.Path == "/api/player/volume" {
			*calls++
			if response := reject(r); response != nil {
				return response, nil
			}
		}
		return previous.RoundTrip(r)
	})
	t.Cleanup(func() { http.DefaultTransport = previous })
	return calls
}
func temporaryBusyReply() *http.Response {
	return &http.Response{StatusCode: http.StatusConflict, Header: http.Header{"X-Podconnect-Recovery": []string{"temporary-busy-no-effect-v1"}}, Body: io.NopCloser(strings.NewReader(""))}
}
func TestExplicitConnectRecoveryBusyKeepsSameCreditUntilDrain(t *testing.T) {
	f, _, _, _ := releasedRoomRouteFixture(t)
	f.selected = false
	announceConnect(t, f, 1)
	original := f.rt.live.connectRecovery
	busy := true
	calls := interceptRecoveryPrime(t, func(_ *http.Request) *http.Response {
		if busy {
			return temporaryBusyReply()
		}
		return nil
	})
	if !tryExplicitConnectRecovery(f.room, &f.rt.live, nil) || f.rt.live.connectRecovery != original || len(f.effects) != 0 || !fileExists(releasedPath(f.room)) {
		t.Fatal("busy credit changed or effected output")
	}
	deadline, intent := original.until, original.transport
	tryExplicitConnectRecovery(f.room, &f.rt.live, nil)
	if original.until != deadline || original.transport != intent || *calls != 2 || len(f.effects) != 0 {
		t.Fatal("pending poll changed authority")
	}
	busy = false
	if !tryExplicitConnectRecovery(f.room, &f.rt.live, nil) || !f.selected || f.volume != 35 || fileExists(releasedPath(f.room)) {
		t.Fatal("drained owner did not route", f.effects)
	}
	if strings.Join(f.effects, ",") != "prime,select,prime" {
		t.Fatal("duplicate effects", f.effects)
	}
	tryExplicitConnectRecovery(f.room, &f.rt.live, nil)
	if *calls != 4 || len(f.effects) != 3 {
		t.Fatal("completed credit replayed")
	}
}
func TestExplicitConnectRecoveryBusyInvalidatedBeforeNextTick(t *testing.T) {
	for _, mode := range []string{"expiry", "paused", "source", "alias", "transport", "attention", "released"} {
		t.Run(mode, func(t *testing.T) {
			f, _, _, _ := releasedRoomRouteFixture(t)
			f.selected = false
			announceConnect(t, f, 1)
			calls := interceptRecoveryPrime(t, func(_ *http.Request) *http.Response { return temporaryBusyReply() })
			tryExplicitConnectRecovery(f.room, &f.rt.live, nil)
			l := &f.rt.live
			switch mode {
			case "expiry":
				l.connectRecovery.until = time.Now().Add(-time.Second)
			case "paused":
				l.applyEvent("paused", nil)
			case "source":
				l.retirePhase(glSource{run: l.runEpoch, phase: l.phaseEpoch})
			case "alias":
				l.applyEvent("selected_alias", nil)
			case "transport":
				l.mu.Lock()
				l.transportRevision++
				l.mu.Unlock()
			case "released":
				_ = os.Remove(releasedPath(f.room))
			}
			var att *attention
			if mode == "attention" {
				att = &attention{nativeBusy: true}
			}
			tryExplicitConnectRecovery(f.room, l, att)
			if mode == "released" {
				_ = os.WriteFile(releasedPath(f.room), []byte("1"), 0600)
				tryExplicitConnectRecovery(f.room, l, nil)
			}
			if *calls != 1 || len(f.effects) != 0 || f.selected || !fileExists(releasedPath(f.room)) {
				t.Fatal("stale busy replay", *calls, f.effects)
			}
		})
	}
}
func TestExplicitConnectRecoveryOnlyExactBusyResponseMayRemainPending(t *testing.T) {
	for _, mode := range []string{"500", "untyped409", "wrongheader", "duplicateheader", "timeout_after_send", "start_owner"} {
		t.Run(mode, func(t *testing.T) {
			f, _, _, _ := releasedRoomRouteFixture(t)
			f.selected = false
			announceConnect(t, f, 1)
			previous := http.DefaultTransport
			calls := 0
			http.DefaultTransport = startTransport(func(r *http.Request) (*http.Response, error) {
				if r.Method == "PUT" && r.URL.Path == "/api/player/volume" {
					calls++
					if mode == "timeout_after_send" {
						return nil, errors.New("unknown delivery")
					}
					reply := temporaryBusyReply()
					switch mode {
					case "500", "start_owner":
						reply.StatusCode = 500
					case "untyped409":
						reply.Header = nil
					case "wrongheader":
						reply.Header.Set("X-PodConnect-Recovery", "other")
					case "duplicateheader":
						reply.Header.Add("X-PodConnect-Recovery", "temporary-busy-no-effect-v1")
					}
					return reply, nil
				}
				return previous.RoundTrip(r)
			})
			t.Cleanup(func() { http.DefaultTransport = previous })
			tryExplicitConnectRecovery(f.room, &f.rt.live, nil)
			tryExplicitConnectRecovery(f.room, &f.rt.live, nil)
			if calls != 1 || f.rt.live.connectRecovery != nil || len(f.effects) != 0 {
				t.Fatal("unknown/terminal refusal replayed", calls, f.effects)
			}
		})
	}
}
func TestExplicitConnectRecoveryNewTransferCannotRestoreOldCredit(t *testing.T) {
	f, _, _, _ := releasedRoomRouteFixture(t)
	f.selected = false
	announceConnect(t, f, 1)
	old := f.rt.live.connectRecovery
	calls := interceptRecoveryPrime(t, func(_ *http.Request) *http.Response { announceConnect(t, f, 2); return temporaryBusyReply() })
	tryExplicitConnectRecovery(f.room, &f.rt.live, nil)
	if *calls != 1 || f.rt.live.connectRecovery == nil || f.rt.live.connectRecovery == old || f.rt.live.connectRecovery.ordinal != 2 {
		t.Fatal("old credit overwrote newer transfer")
	}
	if len(f.effects) != 0 {
		t.Fatal("busy effect")
	}
}

// Same bounded child-process adapter as the existing released bridge tests.
// This invokes roomBridge itself so a following legacy tick cannot evade credit ownership.
func TestActualConnectRecoveryBridgeNeverReplaysSpentTransfer(t *testing.T) {
	mode := os.Getenv("PC33_CONNECT_BRIDGE_CHILD")
	if mode == "" {
		for _, m := range []string{"unknown", "expired_busy", "pause_resume", "fresh_ordinal", "busy_drain"} {
			t.Run(m, func(t *testing.T) {
				ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
				defer cancel()
				cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestActualConnectRecoveryBridgeNeverReplaysSpentTransfer$", "-test.timeout=5s")
				cmd.Env = append(os.Environ(), "PC33_CONNECT_BRIDGE_CHILD="+m)
				if output, err := cmd.CombinedOutput(); err != nil {
					t.Fatalf("actual Connect bridge %s: %v\n%s", m, err, output)
				}
			})
		}
		return
	}
	f, _, owner, route := releasedRoomRouteFixture(t)
	f.selected = false
	status := f.rt.live.Get()
	status.VolPct = 35
	f.rt.live.set(status)
	announceConnect(t, f, 1)
	var primes, selections atomic.Int32
	var permit atomic.Bool
	previous := http.DefaultTransport
	http.DefaultTransport = startTransport(func(r *http.Request) (*http.Response, error) {
		if r.Method == "PUT" && r.URL.Path == "/api/player/volume" {
			primes.Add(1)
			if !permit.Load() {
				if mode == "busy_drain" || mode == "expired_busy" {
					if mode == "expired_busy" {
						// Same bridge goroutine: expire during the known no-effect reply,
						// before its post-await guard; no concurrent fixture time mutation.
						f.rt.live.connectRecoveryFence.until = time.Now().Add(-time.Second)
					}
					return temporaryBusyReply(), nil
				}
				return nil, errors.New("unknown after send")
			}
		}
		if r.Method == "PUT" && r.URL.Path == "/api/outputs/set" {
			selections.Add(1)
		}
		// Ordinary post-success reconciliation reads actual fixture status.
		// No fake extra route grant or provider request is introduced.
		if r.Method == "GET" && r.URL.Path == "/status" {
			raw, _ := json.Marshal(map[string]any{"username": "native-user", "device_id": f.id, "volume": 35, "volume_steps": 100, "paused": false, "stopped": false, "selected_alias_id": 1, "alias_binding": f.rt.live.Get().AliasBinding})
			return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(string(raw)))}, nil
		}
		if r.Method == "GET" && r.URL.Path == "/api/player" {
			return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"state":"play"}`))}, nil
		}
		return previous.RoundTrip(r)
	})
	// Deliberately report clean absence: the old following legacy bridge tick
	// would admit unguarded prime/selection here after the original credit vanished.
	roomTransportRPC = func(_ context.Context, _, _, path string, _ []byte) ([]byte, error) {
		switch {
		case path == "/status":
			return []byte(fmt.Sprintf(`{"username":"native-user","device_id":%q}`, f.id)), nil
		case strings.HasPrefix(path, "/player/room_transport/bound-state?"):
			return json.Marshal(receiverTransportState{"podconnect.native_room_transport_state.v2", true, *owner, []string{"pause"}, false})
		case path == "/api/outputs/42/transport/bound-state":
			return json.Marshal(outputTransportState{"podconnect.output_transport_state.v2", true, *route, false, []string{"pause"}, false, false, true})
		default:
			return nil, fmt.Errorf("unexpected native endpoint %s", path)
		}
	}
	go roomBridge(f.room, &boolFlag{}, &f.rt.live, &attention{})
	deadline := time.Now().Add(2 * time.Second)
	for primes.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if primes.Load() == 0 {
		t.Fatal("bridge never attempted accepted transfer")
	}
	switch mode {
	case "pause_resume":
		f.rt.live.applyEvent("paused", nil)
		f.rt.live.applyEvent("playing", nil)
	case "fresh_ordinal":
		permit.Store(true)
		announceConnect(t, f, 2)
	case "busy_drain":
		permit.Store(true)
	}
	if mode == "fresh_ordinal" || mode == "busy_drain" {
		deadline = time.Now().Add(2 * time.Second)
		for selections.Load() == 0 && time.Now().Before(deadline) {
			time.Sleep(5 * time.Millisecond)
		}
		if selections.Load() != 1 {
			t.Fatalf("current explicit transfer did not route exactly once: %d", selections.Load())
		}
	}
	time.Sleep(650 * time.Millisecond) // more than three actual existing bridge ticks
	if mode == "unknown" || mode == "pause_resume" {
		if primes.Load() != 1 || selections.Load() != 0 {
			t.Fatalf("spent UNKNOWN replayed by later bridge: prime=%d select=%d", primes.Load(), selections.Load())
		}
	} else if mode == "expired_busy" {
		if primes.Load() != 1 || selections.Load() != 0 {
			t.Fatalf("expired pending transfer replayed by later bridge: prime=%d select=%d", primes.Load(), selections.Load())
		}
	} else if selections.Load() != 1 {
		t.Fatal("completed transfer selected again")
	}
	os.Exit(0)
}

func TestExplicitConnectRecoveryAttentionDuringAwaitCannotSelectOrRemainPending(t *testing.T) {
	for _, phase := range []string{"read", "prime", "typed_busy"} {
		t.Run(phase, func(t *testing.T) {
			f, _, _, _ := releasedRoomRouteFixture(t)
			f.selected = false
			announceConnect(t, f, 1)
			att := &attention{}
			block := func() { att.mu.Lock(); att.nativePending = true; att.mu.Unlock() }
			switch phase {
			case "read":
				f.onRead = block
			case "prime":
				f.onPrime = block
			case "typed_busy":
				interceptRecoveryPrime(t, func(_ *http.Request) *http.Response { block(); return temporaryBusyReply() })
			}
			tryExplicitConnectRecovery(f.room, &f.rt.live, att)
			tryExplicitConnectRecovery(f.room, &f.rt.live, att)
			allowed := 0
			if phase == "prime" {
				allowed = 1
			}
			if len(f.effects) != allowed || f.selected || f.rt.live.connectRecovery != nil || !fileExists(releasedPath(f.room)) {
				t.Fatal("await crossed attention owner", phase, f.effects)
			}
		})
	}
}
func TestExplicitConnectRecoverySuccessfulRemovalRetiresOnlyItsOwnFence(t *testing.T) {
	f, _, _, _ := releasedRoomRouteFixture(t)
	f.selected = false
	announceConnect(t, f, 1)
	handled, done := tryExplicitConnectRecoveryOutcome(f.room, &f.rt.live, nil)
	if !handled || done == nil || done.ordinal != 1 || f.rt.live.connectRecoveryFence != nil || fileExists(releasedPath(f.room)) {
		t.Fatal("success missing exact receipt or retained spent fence")
	}
	// The bookkeeping receipt remains the alias actually routed, not a new event.
	announceConnect(t, f, 2)
	if done.ordinal != 1 || f.rt.live.connectRecoveryFence == done || f.rt.live.connectRecoveryFence.ordinal != 2 {
		t.Fatal("old success receipt adopted or cleared newer credit")
	}
}
