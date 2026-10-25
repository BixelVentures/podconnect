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
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func announceConnect(t *testing.T, f *releasedStartFixture, ordinal float64, load ...uint64) glSource {
	t.Helper()
	l := &f.rt.live
	raw, _ := json.Marshal(l.Get().AliasBinding)
	var b map[string]any
	_ = json.Unmarshal(raw, &b)
	source := glSource{run: l.runEpoch, phase: l.phaseEpoch}
	data := map[string]any{"id": float64(1), "ordinal": ordinal, "alias_binding": b}
	if len(load) > 0 {
		data["initial_load_ordinal"] = float64(load[0])
		data["initial_paused"] = l.Get().Paused
	}
	l.applySourceEvent(source, "connect_transfer", data)
	return source
}

// Older recovery fixtures must supply the same owner-addressed read as production.
func installConnectRouteRead(t *testing.T, f *releasedStartFixture, owner *roomTransportIdentity, route *outputTransportIdentity) {
	t.Helper()
	old := roomTransportRPC
	t.Cleanup(func() { roomTransportRPC = old })
	roomTransportRPC = func(_ context.Context, _, method, path string, body []byte) ([]byte, error) {
		switch {
		case path == "/status":
			return []byte(fmt.Sprintf(`{"username":"native-user","device_id":%q}`, f.id)), nil
		case strings.HasPrefix(path, "/player/room_transport/bound-state?"):
			return json.Marshal(receiverTransportState{"podconnect.native_room_transport_state.v2", true, *owner, []string{"pause"}, nil, false})
		case path == "/api/outputs/42/transport/bound-state":
			var supplied struct {
				Owner roomTransportIdentity `json:"native_owner"`
			}
			if json.Unmarshal(body, &supplied) != nil || supplied.Owner != *owner {
				t.Fatal("unbound C state read")
			}
			return nil, fmt.Errorf("HTTP409: unselected ordinary output has no bound identity")
		}
		return nil, fmt.Errorf("unexpected read %s %s", method, path)
	}
}

func TestExplicitConnectRecoveryOneShot(t *testing.T) {
	f, _, owner, route := releasedRoomRouteFixture(t)
	installConnectRouteRead(t, f, owner, route)
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
			f, _, owner, route := releasedRoomRouteFixture(t)
			installConnectRouteRead(t, f, owner, route)
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
	f, _, owner, route := releasedRoomRouteFixture(t)
	installConnectRouteRead(t, f, owner, route)
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
	f, _, owner, route := releasedRoomRouteFixture(t)
	installConnectRouteRead(t, f, owner, route)
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
	for _, mode := range []string{"expiry", "paused", "source", "alias", "transport", "attention"} {
		t.Run(mode, func(t *testing.T) {
			f, _, owner, route := releasedRoomRouteFixture(t)
			installConnectRouteRead(t, f, owner, route)
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
			}
			var att *attention
			if mode == "attention" {
				att = &attention{nativeBusy: true}
			}
			tryExplicitConnectRecovery(f.room, l, att)
			if *calls != 1 || len(f.effects) != 0 || f.selected || !fileExists(releasedPath(f.room)) {
				t.Fatal("stale busy replay", *calls, f.effects)
			}
		})
	}
}
func TestExplicitConnectRecoveryOnlyExactBusyResponseMayRemainPending(t *testing.T) {
	for _, mode := range []string{"500", "untyped409", "wrongheader", "duplicateheader", "timeout_after_send", "start_owner"} {
		t.Run(mode, func(t *testing.T) {
			f, _, owner, route := releasedRoomRouteFixture(t)
			installConnectRouteRead(t, f, owner, route)
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
	f, _, owner, route := releasedRoomRouteFixture(t)
	installConnectRouteRead(t, f, owner, route)
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
		for _, m := range []string{"bound_selected_observation", "stopped_bound_transfer", "idle_bound_transfer", "completed_start", "unknown", "expired_busy", "pause_resume", "fresh_ordinal", "busy_drain", "paused_handoff", "paused_user_cancel", "resume_late", "resume_queued", "resume_cancel", "resume_unknown", "resume_busy"} {
			t.Run(m, func(t *testing.T) {
				limit := 6 * time.Second
				if strings.HasPrefix(m, "resume_") {
					limit = 13 * time.Second
				}
				ctx, cancel := context.WithTimeout(context.Background(), limit)
				defer cancel()
				cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestActualConnectRecoveryBridgeNeverReplaysSpentTransfer$", "-test.timeout="+fmt.Sprint(limit-time.Second))
				cmd.Env = append(os.Environ(), "PC33_CONNECT_BRIDGE_CHILD="+m)
				if output, err := cmd.CombinedOutput(); err != nil {
					t.Fatalf("actual Connect bridge %s: %v\n%s", m, err, output)
				}
			})
		}
		return
	}
	if strings.HasPrefix(mode, "resume_") {
		runExplicitResumeBridgeCase(t, mode)
		return
	}
	f, _, owner, route := releasedRoomRouteFixture(t)
	f.selected = mode == "completed_start" || mode == "bound_selected_observation"
	if mode == "completed_start" {
		f.volume = 35
	}
	status := f.rt.live.Get()
	status.VolPct = 35
	if mode == "idle_bound_transfer" || mode == "stopped_bound_transfer" {
		status.Active = mode == "stopped_bound_transfer"
		status.Stopped = mode == "stopped_bound_transfer"
		_ = os.Remove(releasedPath(f.room))
	}
	f.rt.live.set(status)
	if mode == "bound_selected_observation" {
		_ = os.Remove(releasedPath(f.room))
	}
	// Optional joined execution uses untouched JSON emitted by the real Native
	// dealer/worker/FIFO case in this same candidate; no event/identity rewrites.
	type wireEvent struct {
		Type string         `json:"type"`
		Data map[string]any `json:"data"`
	}
	var beforeInitial, initialAndPause []wireEvent
	var explicitResume *wireEvent
	var nativePrefix []wireEvent
	if dir := os.Getenv("PC58_CONNECT_TRACE_DIR"); dir != "" && (mode == "paused_handoff" || mode == "paused_user_cancel") {
		var trace struct {
			InitialStatus map[string]any `json:"initial_status"`
			Events        []wireEvent    `json:"events"`
		}
		raw, err := os.ReadFile(filepath.Join(dir, fmt.Sprintf("prior-true-cancel-%t.json", mode == "paused_user_cancel")))
		if err != nil {
			t.Fatal(err)
		}
		if err = json.Unmarshal(raw, &trace); err != nil {
			t.Fatal(err)
		}
		binding, present := eventAliasBinding(trace.InitialStatus)
		if !present || binding == nil || !binding.valid() || binding.Registry != status.AliasBinding.Registry || binding.RoomID != f.room.ID || trace.InitialStatus["paused"] != true || trace.InitialStatus["active"] != true || trace.InitialStatus["selected_alias_id"] != float64(1) {
			t.Fatal("real Native status cannot seed exact fixture owner", trace.InitialStatus, status.AliasBinding)
		}
		source := glSource{run: f.rt.live.runEpoch, phase: f.rt.live.phaseEpoch}
		request, ok := f.rt.live.beginStatus(source)
		if !ok {
			t.Fatal("status fence unavailable")
		}
		status.AliasBinding = binding
		status.Paused = true
		if !f.rt.live.acceptStatus(request, status) {
			t.Fatal("actual Native status rejected")
		}
		owner.Incarnation, owner.Registry = binding.Incarnation, binding.Registry
		creditAt := -1
		var expected float64
		for i, event := range trace.Events {
			if event.Type == "connect_transfer" {
				if creditAt >= 0 {
					t.Fatal("multiple Native transfer grants")
				}
				creditAt = i
				expected, _ = event.Data["initial_load_ordinal"].(float64)
			}
		}
		if creditAt < 0 || expected < 1 || len(trace.Events) <= creditAt+1 {
			t.Fatal("missing actual Native transfer/observation")
		}
		nativePrefix = trace.Events[:creditAt+1]
		last := trace.Events[len(trace.Events)-1]
		if last.Type != "playing" || last.Data["resume"] != true {
			t.Fatal("missing actual final explicit Resume")
		}
		explicitResume = &last
		matched := false
		for _, event := range trace.Events[creditAt+1 : len(trace.Events)-1] {
			if event.Type == "paused" && event.Data["initial_load_ordinal"] == expected {
				matched = true
			}
			if matched {
				initialAndPause = append(initialAndPause, event)
			} else {
				beforeInitial = append(beforeInitial, event)
			}
		}
		if !matched {
			t.Fatal("missing matching actual initial load observation")
		}
	}
	var statusPaused atomic.Bool
	if mode == "paused_handoff" || mode == "paused_user_cancel" {
		statusPaused.Store(true)
		if nativePrefix != nil {
			source := glSource{run: f.rt.live.runEpoch, phase: f.rt.live.phaseEpoch}
			for _, event := range nativePrefix {
				f.rt.live.applySourceEvent(source, event.Type, event.Data)
			}
			for _, event := range beforeInitial {
				f.rt.live.applySourceEvent(source, event.Type, event.Data)
			}
		} else {
			f.rt.live.applyEvent("paused", nil)
			announceConnect(t, f, 1, 2)
			f.rt.live.applyEvent("playing", map[string]any{"initial_load_ordinal": float64(1)})
			f.rt.live.applyEvent("playing", nil)
		}
	} else if mode != "idle_bound_transfer" && mode != "stopped_bound_transfer" && mode != "bound_selected_observation" {
		announceConnect(t, f, 1)
	}
	var primes, selections, connectPrimes, preselectionPrimes, allWrites atomic.Int32
	var permit atomic.Bool
	if mode == "paused_handoff" || mode == "paused_user_cancel" || (mode == "idle_bound_transfer" || mode == "stopped_bound_transfer") {
		permit.Store(true)
	}
	previous := http.DefaultTransport
	http.DefaultTransport = startTransport(func(r *http.Request) (*http.Response, error) {
		if r.Method != http.MethodGet {
			allWrites.Add(1)
		}
		if r.Method == "PUT" && r.URL.Path == "/api/player/volume" {
			primes.Add(1)
			if r.URL.Query().Get("recovery") == "connect" {
				connectPrimes.Add(1)
				if selections.Load() == 0 {
					preselectionPrimes.Add(1)
				}
			}
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
			raw, _ := json.Marshal(map[string]any{"username": "native-user", "device_id": f.id, "volume": 35, "volume_steps": 100, "paused": statusPaused.Load(), "stopped": false, "selected_alias_id": 1, "alias_binding": f.rt.live.Get().AliasBinding})
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
			return json.Marshal(receiverTransportState{"podconnect.native_room_transport_state.v2", true, *owner, []string{"pause"}, nil, false})
		case path == "/api/outputs/42/transport/bound-state":
			if mode != "completed_start" {
				return nil, fmt.Errorf("HTTP409: no current selected bound output")
			}
			return json.Marshal(outputTransportState{"podconnect.output_transport_state.v2", true, *route, false, []string{"pause"}, false, true, false})
		default:
			return nil, fmt.Errorf("unexpected native endpoint %s", path)
		}
	}
	go roomBridge(f.room, &boolFlag{}, &f.rt.live, &attention{})
	if mode == "bound_selected_observation" {
		time.Sleep(650 * time.Millisecond)
		if allWrites.Load() != 0 {
			t.Fatal("bound observed route issued a write", allWrites.Load())
		}
		os.Exit(0)
	}
	if mode == "idle_bound_transfer" || mode == "stopped_bound_transfer" {
		time.Sleep(650 * time.Millisecond)
		if allWrites.Load() != 0 {
			t.Fatal("idle bound alias issued a write", allWrites.Load())
		}
		active := f.rt.live.Get()
		active.Active = true
		active.Stopped = false
		f.rt.live.set(active)
		announceConnect(t, f, 1)
	}
	if mode == "paused_handoff" || mode == "paused_user_cancel" {
		time.Sleep(450 * time.Millisecond) // More than two actual bridge ticks.
		if primes.Load() != 0 || selections.Load() != 0 || !fileExists(releasedPath(f.room)) {
			t.Fatal("paused transfer dispatched route/autoplay", primes.Load(), selections.Load())
		}
		if nativePrefix != nil {
			source := glSource{run: f.rt.live.runEpoch, phase: f.rt.live.phaseEpoch}
			for _, event := range initialAndPause {
				f.rt.live.applySourceEvent(source, event.Type, event.Data)
			}
		} else {
			f.rt.live.applyEvent("paused", map[string]any{"initial_load_ordinal": float64(2)})
			if mode == "paused_user_cancel" {
				f.rt.live.applyEvent("paused", nil)
			}
		}
		time.Sleep(250 * time.Millisecond)
		if primes.Load() != 0 || selections.Load() != 0 {
			t.Fatal("initial paused receipt dispatched route")
		}
		statusPaused.Store(false)
		if explicitResume != nil {
			f.rt.live.applySourceEvent(glSource{run: f.rt.live.runEpoch, phase: f.rt.live.phaseEpoch}, explicitResume.Type, explicitResume.Data)
		} else {
			f.rt.live.applyEvent("playing", nil)
		} // Explicit Resume only.
		if mode == "paused_user_cancel" {
			time.Sleep(650 * time.Millisecond)
			if primes.Load() != 0 || selections.Load() != 0 || !f.rt.live.holdsConnectRecoveryFence() {
				t.Fatal("genuine Pause cancellation revived on Resume", primes.Load(), selections.Load())
			}
			os.Exit(0)
		}
	}
	if mode == "completed_start" {
		deadline := time.Now().Add(2 * time.Second)
		for fileExists(releasedPath(f.room)) && time.Now().Before(deadline) {
			time.Sleep(5 * time.Millisecond)
		}
		if fileExists(releasedPath(f.room)) {
			t.Fatal("completed START not observed")
		}
		time.Sleep(650 * time.Millisecond)
		if allWrites.Load() != 0 {
			t.Fatal("completed START next tick issued a write", allWrites.Load())
		}
		os.Exit(0)
	}
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
	if mode == "fresh_ordinal" || mode == "busy_drain" || mode == "paused_handoff" {
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
	if mode == "paused_handoff" && (preselectionPrimes.Load() != 1 || connectPrimes.Load() != 2) {
		// Existing routing primes before selection and re-proves volume after it.
		t.Fatal("paused handoff repeated or missed existing Connect volume proofs", preselectionPrimes.Load(), connectPrimes.Load())
	}
	os.Exit(0)
}

func TestExplicitConnectRecoveryAttentionDuringAwaitCannotSelectOrRemainPending(t *testing.T) {
	for _, phase := range []string{"read", "prime", "typed_busy"} {
		t.Run(phase, func(t *testing.T) {
			f, _, owner, route := releasedRoomRouteFixture(t)
			installConnectRouteRead(t, f, owner, route)
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
	f, _, owner, route := releasedRoomRouteFixture(t)
	installConnectRouteRead(t, f, owner, route)
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

func TestConnectRecoveryInitialReceiptBounds(t *testing.T) {
	for _, mode := range []string{"missing", "older", "genuine_pause_before", "genuine_pause_after", "wrong_state", "newer", "malformed", "expired", "duplicate_then_resume"} {
		t.Run(mode, func(t *testing.T) {
			f, _, owner, route := releasedRoomRouteFixture(t)
			installConnectRouteRead(t, f, owner, route)
			l := &f.rt.live
			l.applyEvent("paused", nil)
			announceConnect(t, f, 1, 2)
			credit := l.connectRecovery
			if credit == nil || !credit.awaitInitial {
				t.Fatal("missing exact initial observation fence")
			}
			until := credit.until
			l.applyEvent("playing", map[string]any{"initial_load_ordinal": float64(1)})
			l.applyEvent("playing", nil) // A queued older Resume cannot consume before the current receipt.
			if _, ok := l.consumeConnectRecovery(); ok {
				t.Fatal("unobserved load consumed credit")
			}
			switch mode {
			case "missing", "older":
				return
			case "genuine_pause_before":
				l.applyEvent("paused", nil)
			case "wrong_state":
				l.applyEvent("playing", map[string]any{"initial_load_ordinal": float64(2)})
			case "newer":
				l.applyEvent("paused", map[string]any{"initial_load_ordinal": float64(3)})
			case "malformed":
				l.applyEvent("paused", map[string]any{"initial_load_ordinal": "2"})
			case "expired":
				credit.until = time.Now().Add(-time.Second)
				l.consumeConnectRecovery()
			default:
				l.applyEvent("paused", map[string]any{"initial_load_ordinal": float64(2)})
				if l.connectRecovery != credit || credit.awaitInitial || !credit.until.Equal(until) || !l.Get().Paused {
					t.Fatal("initial observation cancelled/extended credit or lost paused truth")
				}
				if mode == "genuine_pause_after" {
					l.applyEvent("paused", nil)
				} else {
					l.applyEvent("playing", nil)
					l.applyEvent("paused", map[string]any{"initial_load_ordinal": float64(2)}) // Duplicate cannot undo Resume.
					if l.Get().Paused {
						t.Fatal("duplicate initial receipt reverted Resume")
					}
					if _, ok := l.consumeConnectRecovery(); !ok {
						t.Fatal("prompt explicit Resume lost current credit")
					}
					if _, ok := l.consumeConnectRecovery(); ok {
						t.Fatal("receipt replayed credit")
					}
					return
				}
			}
			l.applyEvent("playing", nil)
			l.applyEvent("paused", map[string]any{"initial_load_ordinal": float64(2)})
			l.applyEvent("playing", nil)
			if _, ok := l.consumeConnectRecovery(); ok || l.connectRecovery != nil || !l.holdsConnectRecoveryFence() {
				t.Fatal("cancelled/expired/malformed credit revived")
			}
		})
	}
}

// Uses the existing child-process roomBridge adapter and actual Native wire
// receipts when supplied; no extra command, provider or transport authority.
func runExplicitResumeBridgeCase(t *testing.T, mode string) {
	f, _, owner, route := releasedRoomRouteFixture(t)
	installConnectRouteRead(t, f, owner, route)
	f.selected = false
	status := f.rt.live.Get()
	status.VolPct, status.Paused = 35, true
	f.rt.live.set(status)
	type wireEvent struct {
		Type string         `json:"type"`
		Data map[string]any `json:"data"`
	}
	var grant wireEvent
	var observations []wireEvent
	if dir := os.Getenv("PC58_RESUME_TRACE_DIR"); dir != "" {
		name := "accepted"
		if mode == "resume_queued" {
			name = "queued_initial"
		}
		if mode == "resume_cancel" {
			name = "genuine_pause"
		}
		var trace struct {
			InitialStatus map[string]any `json:"initial_status"`
			Events        []wireEvent    `json:"events"`
		}
		raw, err := os.ReadFile(filepath.Join(dir, name+".json"))
		if err != nil {
			t.Fatal(err)
		}
		if err = json.Unmarshal(raw, &trace); err != nil {
			t.Fatal(err)
		}
		binding, present := eventAliasBinding(trace.InitialStatus)
		if !present || binding == nil || !binding.valid() || binding.Registry != status.AliasBinding.Registry || binding.RoomID != f.room.ID {
			t.Fatal("Native Resume owner differs from actual fixture")
		}
		source := glSource{run: f.rt.live.runEpoch, phase: f.rt.live.phaseEpoch}
		request, ok := f.rt.live.beginStatus(source)
		if !ok {
			t.Fatal("status fence unavailable")
		}
		status.AliasBinding = binding
		if !f.rt.live.acceptStatus(request, status) {
			t.Fatal("Native owner status rejected")
		}
		found := false
		for _, event := range trace.Events {
			if event.Type == "connect_transfer" && event.Data["kind"] == "resume" {
				if found {
					t.Fatal("more than one actual Resume grant")
				}
				grant, found = event, true
			} else if found {
				observations = append(observations, event)
			} else {
				f.rt.live.applySourceEvent(source, event.Type, event.Data)
			}
		}
		if !found {
			t.Fatal("actual Native Resume grant missing")
		}
	} else {
		announceConnect(t, f, 1, 1)
		f.rt.live.applyEvent("paused", map[string]any{"initial_load_ordinal": float64(1)})
		raw, _ := json.Marshal(status.AliasBinding)
		var binding map[string]any
		_ = json.Unmarshal(raw, &binding)
		grant = wireEvent{"connect_transfer", map[string]any{"kind": "resume", "id": float64(1), "ordinal": float64(2), "alias_binding": binding, "resume_ordinal": float64(2), "primary_load_ordinal": float64(1)}}
		if mode == "resume_queued" {
			observations = append(observations, wireEvent{"paused", map[string]any{"initial_load_ordinal": float64(1)}})
		}
		observations = append(observations, wireEvent{"playing", map[string]any{"resume": true, "resume_ordinal": float64(2)}})
		if mode == "resume_cancel" {
			observations = append(observations, wireEvent{"paused", nil})
		}
	}
	var primes, selections, transports atomic.Int32
	var permit atomic.Bool
	if mode != "resume_busy" {
		permit.Store(true)
	}
	previous := http.DefaultTransport
	http.DefaultTransport = startTransport(func(r *http.Request) (*http.Response, error) {
		if r.Method == "PUT" && r.URL.Path == "/api/player/volume" {
			primes.Add(1)
			if r.URL.Query().Get("recovery") != "connect" && selections.Load() == 0 {
				t.Error("unguarded preselection volume")
			}
			if mode == "resume_unknown" {
				return nil, errors.New("UNKNOWN after send")
			}
			if !permit.Load() {
				return temporaryBusyReply(), nil
			}
		}
		if r.Method == "PUT" && r.URL.Path == "/api/outputs/set" {
			selections.Add(1)
		}
		if strings.Contains(r.URL.Path, "/play") || strings.Contains(r.URL.Path, "/pause") || strings.Contains(r.URL.Path, "/resume") {
			transports.Add(1)
		}
		if r.Method == "GET" && r.URL.Path == "/status" {
			raw, _ := json.Marshal(map[string]any{"username": "native-user", "device_id": f.id, "volume": 35, "volume_steps": 100, "paused": f.rt.live.Get().Paused, "stopped": false, "selected_alias_id": 1, "alias_binding": f.rt.live.Get().AliasBinding})
			return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(string(raw)))}, nil
		}
		if r.Method == "GET" && r.URL.Path == "/api/player" {
			return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"state":"play"}`))}, nil
		}
		return previous.RoundTrip(r)
	})
	// Every preceding transfer is expired, never revived by this later command.
	if mode == "resume_late" {
		time.Sleep(connectRecoveryWindow + 50*time.Millisecond)
	} else {
		f.rt.live.mu.Lock()
		f.rt.live.connectRecovery.until = time.Now().Add(-time.Second)
		f.rt.live.mu.Unlock()
	}
	go roomBridge(f.room, &boolFlag{}, &f.rt.live, &attention{})
	time.Sleep(250 * time.Millisecond)
	if primes.Load() != 0 || selections.Load() != 0 || transports.Load() != 0 {
		t.Fatal("expired paused transfer caused effects")
	}
	f.rt.live.applySourceEvent(glSource{run: f.rt.live.runEpoch, phase: f.rt.live.phaseEpoch}, grant.Type, grant.Data)
	f.rt.live.mu.Lock()
	until := f.rt.live.connectRecovery.until
	f.rt.live.mu.Unlock()
	time.Sleep(250 * time.Millisecond)
	if primes.Load() != 0 || selections.Load() != 0 {
		t.Fatal("Resume ACK before exact observation routed")
	}
	for _, event := range observations {
		f.rt.live.applySourceEvent(glSource{run: f.rt.live.runEpoch, phase: f.rt.live.phaseEpoch}, event.Type, event.Data)
	}
	if mode == "resume_cancel" {
		time.Sleep(450 * time.Millisecond)
		f.rt.live.applyEvent("playing", nil)
		time.Sleep(250 * time.Millisecond)
		if primes.Load() != 0 || selections.Load() != 0 || !f.rt.live.holdsConnectRecoveryFence() {
			t.Fatal("genuine Pause revived Resume credit")
		}
		os.Exit(0)
	}
	deadline := time.Now().Add(time.Second)
	for primes.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if primes.Load() == 0 {
		t.Fatal("new explicit Resume never attempted")
	}
	if mode == "resume_busy" {
		f.rt.live.mu.Lock()
		sameDeadline := f.rt.live.connectRecovery != nil && f.rt.live.connectRecovery.until.Equal(until)
		f.rt.live.mu.Unlock()
		if !sameDeadline || selections.Load() != 0 {
			t.Fatal("pre-effect busy spent/extended credit or selected")
		}
		permit.Store(true)
	}
	if mode != "resume_unknown" {
		deadline = time.Now().Add(time.Second)
		for selections.Load() == 0 && time.Now().Before(deadline) {
			time.Sleep(5 * time.Millisecond)
		}
		if selections.Load() != 1 {
			t.Fatal("fresh explicit Resume did not select once", selections.Load())
		}
	}
	f.rt.live.applySourceEvent(glSource{run: f.rt.live.runEpoch, phase: f.rt.live.phaseEpoch}, grant.Type, grant.Data) // duplicate cannot renew
	f.rt.live.applyEvent("paused", nil)
	f.rt.live.applyEvent("playing", nil)
	time.Sleep(450 * time.Millisecond)
	if mode == "resume_unknown" {
		if primes.Load() != 1 || selections.Load() != 0 {
			t.Fatal("UNKNOWN Resume replayed", primes.Load(), selections.Load())
		}
	} else if selections.Load() != 1 {
		t.Fatal("Resume selected twice")
	}
	os.Exit(0)
}

func TestConnectRecoveryResumeReceiptBounds(t *testing.T) {
	for _, mode := range []string{"match", "missing", "older", "newer", "malformed", "wrong_state", "genuine_pause", "expired", "changed_alias", "old_source"} {
		t.Run(mode, func(t *testing.T) {
			f, _, owner, route := releasedRoomRouteFixture(t)
			installConnectRouteRead(t, f, owner, route)
			l := &f.rt.live
			raw, _ := json.Marshal(l.Get().AliasBinding)
			var binding map[string]any
			_ = json.Unmarshal(raw, &binding)
			l.applySourceEvent(glSource{run: l.runEpoch, phase: l.phaseEpoch}, "connect_transfer", map[string]any{"kind": "resume", "id": float64(1), "ordinal": float64(2), "alias_binding": binding, "resume_ordinal": float64(2), "primary_load_ordinal": float64(3)})
			credit := l.connectRecovery
			if credit == nil || !credit.awaitResume {
				t.Fatal("missing Resume receipt fence")
			}
			until := credit.until
			l.applyEvent("paused", map[string]any{"initial_load_ordinal": float64(3)})
			l.applyEvent("playing", nil)
			if _, ok := l.consumeConnectRecovery(); ok {
				t.Fatal("old initial/generic playing consumed new Resume")
			}
			if mode == "missing" {
				return
			}
			data := map[string]any{"resume": true, "resume_ordinal": float64(2)}
			switch mode {
			case "older":
				data["resume_ordinal"] = float64(1)
			case "newer":
				data["resume_ordinal"] = float64(3)
			case "malformed":
				data["resume_ordinal"] = "2"
			case "wrong_state":
				data["resume"] = false
			case "genuine_pause":
				l.applyEvent("paused", nil)
			case "expired":
				credit.until = time.Now().Add(-time.Second)
			case "changed_alias":
				l.applyEvent("selected_alias", nil)
			case "old_source":
				l.retirePhase(glSource{run: l.runEpoch, phase: l.phaseEpoch})
			}
			l.applyEvent("playing", data)
			_, ok := l.consumeConnectRecovery()
			if ok != (mode == "match") {
				t.Fatal("wrong operation admission", mode, ok)
			}
			if mode == "match" && !credit.until.Equal(until) {
				t.Fatal("receipt extended deadline")
			}
			if _, again := l.consumeConnectRecovery(); again {
				t.Fatal("operation receipt replayed")
			}
		})
	}
}

// Counterfactual: a fresh authenticated transfer must reach the existing protected
// route path even when the legacy release marker is absent. This does not model
// C journal eligibility or claim that C must accept the request.
func TestExplicitConnectWithoutLegacyMarkerReachesProtectedPath(t *testing.T) {
	f, _, owner, route := releasedRoomRouteFixture(t)
	installConnectRouteRead(t, f, owner, route)
	f.selected = false
	if err := os.Remove(releasedPath(f.room)); err != nil {
		t.Fatal(err)
	}
	old := http.DefaultTransport
	protected := 0
	http.DefaultTransport = startTransport(func(r *http.Request) (*http.Response, error) {
		if r.Method == "PUT" {
			if r.URL.Query().Get("recovery") != "connect" {
				t.Fatal("unguarded transfer mutation")
			}
			protected++
		}
		return old.RoundTrip(r)
	})
	announceConnect(t, f, 1)
	expected := f.rt.live.Get().AliasBinding
	handled, done := tryExplicitConnectRecoveryOutcome(f.room, &f.rt.live, nil)
	if !handled || done == nil || done.alias != 1 || done.ordinal != 1 || !sameAliasBinding(done.binding, expected) || !f.selected || f.volume != 35 || f.rt.live.connectRecoveryFence != nil || f.rt.live.connectRecovery != nil || fileExists(releasedPath(f.room)) || protected != 3 || strings.Join(f.effects, ",") != "prime,select,prime" {
		t.Fatalf("missing exact completed route: handled=%t receipt=%+v protected_calls=%d effects=%v", handled, done, protected, f.effects)
	}
	before := len(f.effects)
	if handled, receipt := tryExplicitConnectRecoveryOutcome(f.room, &f.rt.live, nil); handled || receipt != nil || len(f.effects) != before || protected != 3 {
		t.Fatal("subsequent tick replayed completed transfer")
	}
}

func TestConnectFenceWithoutLegacyMarkerBlocksFallback(t *testing.T) {
	for _, mode := range []string{"unknown", "expired", "spent", "foreign_run", "foreign_phase", "foreign_binding"} {
		t.Run(mode, func(t *testing.T) {
			f, _, owner, route := releasedRoomRouteFixture(t)
			installConnectRouteRead(t, f, owner, route)
			f.selected = false
			if err := os.Remove(releasedPath(f.room)); err != nil {
				t.Fatal(err)
			}
			announceConnect(t, f, 1)
			switch mode {
			case "unknown":
				f.failSelect = true
				if handled, done := tryExplicitConnectRecoveryOutcome(f.room, &f.rt.live, nil); !handled || done != nil {
					t.Fatal("unknown route accepted")
				}
			case "expired":
				f.rt.live.connectRecovery.until = time.Now().Add(-time.Second)
				tryExplicitConnectRecoveryOutcome(f.room, &f.rt.live, nil)
			case "spent":
				f.rt.live.consumeConnectRecovery()
			case "foreign_run":
				f.rt.live.connectRecoveryFence.run++
			case "foreign_phase":
				f.rt.live.connectRecoveryFence.phase++
			case "foreign_binding":
				f.rt.live.connectRecoveryFence.binding = copyAliasBinding(f.rt.live.connectRecoveryFence.binding)
				f.rt.live.connectRecoveryFence.binding.Incarnation = "foreign"
			}
			status, intent := f.rt.live.transportSnapshot()
			before := len(f.effects)
			got := holdReleasedBridgeRoute(f.room, status, intent)
			want := mode == "unknown" || mode == "expired" || mode == "spent"
			if got != want || len(f.effects) != before {
				t.Fatalf("fence hold=%t want=%t effects=%v", got, want, f.effects)
			}
		})
	}
}

func TestExplicitConnectBusyCreditSurvivesLegacyMarkerRemoval(t *testing.T) {
	f, _, owner, route := releasedRoomRouteFixture(t)
	installConnectRouteRead(t, f, owner, route)
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
	if !tryExplicitConnectRecovery(f.room, &f.rt.live, nil) || f.rt.live.connectRecovery != original || len(f.effects) != 0 {
		t.Fatal("busy credit lost")
	}
	deadline, intent := original.until, original.transport
	if err := os.Remove(releasedPath(f.room)); err != nil {
		t.Fatal(err)
	}
	if !tryExplicitConnectRecovery(f.room, &f.rt.live, nil) || f.rt.live.connectRecovery != original || original.until != deadline || original.transport != intent || *calls != 2 || len(f.effects) != 0 {
		t.Fatal("marker removal changed pending credit")
	}
	busy = false
	handled, done := tryExplicitConnectRecoveryOutcome(f.room, &f.rt.live, nil)
	if !handled || done != original || !f.selected || f.rt.live.connectRecoveryFence != nil || strings.Join(f.effects, ",") != "prime,select,prime" || *calls != 4 {
		t.Fatal("same credit did not complete exactly once", f.effects)
	}
	tryExplicitConnectRecovery(f.room, &f.rt.live, nil)
	if *calls != 4 || len(f.effects) != 3 {
		t.Fatal("completed credit replayed")
	}
}
