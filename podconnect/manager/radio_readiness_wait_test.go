package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestRadioPassiveReadinessOutlivesBootDeadlineWithBoundedReads(t *testing.T) {
	calls := 0
	var elapsed time.Duration
	var pauses []time.Duration
	err := waitRadioPassiveReadiness(context.Background(), func(ctx context.Context) (bool, bool, error) {
		deadline, ok := ctx.Deadline()
		if !ok || time.Until(deadline) > 8*time.Second || time.Until(deadline) <= 0 {
			t.Fatal("unbounded passive probe")
		}
		calls++
		if calls == 12 {
			return true, false, nil
		}
		return false, true, errRadioSenderDiscoveryPending
	}, func(_ context.Context, delay time.Duration) error {
		if delay < time.Second || delay > 8*time.Second {
			t.Fatal("unbounded readiness backoff", delay)
		}
		pauses = append(pauses, delay)
		elapsed += delay
		return nil
	})
	if err != nil || calls != 12 || elapsed <= 20*time.Second || len(pauses) != 11 {
		t.Fatal("late readiness lost or repeated", err, calls, elapsed, pauses)
	}
	if pauses[0] != time.Second || pauses[1] != 2*time.Second || pauses[2] != 4*time.Second || pauses[3] != 8*time.Second {
		t.Fatal("unexpected passive backoff", pauses)
	}
}

func TestRadioPassiveReadinessUnknownAndCancellationNeverRetryEffect(t *testing.T) {
	for _, failure := range []error{errRadioUnknown, errRadioUnavailable} {
		probes, pauses := 0, 0
		err := waitRadioPassiveReadiness(context.Background(), func(context.Context) (bool, bool, error) { probes++; return false, false, failure }, func(context.Context, time.Duration) error { pauses++; return nil })
		if !errors.Is(err, failure) || probes != 1 || pauses != 0 {
			t.Fatal("fatal custody/read error retried", err, probes, pauses)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	probes, effects := 0, 0
	err := waitRadioPassiveReadiness(ctx, func(context.Context) (bool, bool, error) {
		probes++
		return false, true, errRadioSenderDiscoveryPending
	}, func(ctx context.Context, _ time.Duration) error { cancel(); return ctx.Err() })
	if err == nil {
		effects++
	}
	if !errors.Is(err, context.Canceled) || probes != 1 || effects != 0 {
		t.Fatal("cancelled wait reached preparation", err, probes, effects)
	}
}

func TestRadioNativeBootstrapColdReplacementUsesOnlyReadsAndReturnsOnce(t *testing.T) {
	var calls atomic.Int32
	var posts atomic.Int32
	makeState := func(boot string, ready bool) radioProducerNativeState {
		process, _ := json.Marshal(map[string]any{"boot_id": boot, "pid": "42", "proc_start_ticks": "8"})
		sum := sha256.Sum256(process)
		missing := []string{"radio_no_ready_targets"}
		if ready {
			missing = []string{}
		}
		return radioProducerNativeState{Schema: "podconnect.native_radio_handoff_state.v1", ControllerID: "AABBCCDDEEFF0011", ProcessID: hex.EncodeToString(sum[:]), NativeProcess: process, Revision: "7", Targets: []radioProducerTarget{{radioTarget: radioTarget{ID: "room-A", Availability: "available", Selectable: true}, HomePodID: "123"}}, Missing: missing}
	}
	first, final := makeState("cold-process", false), makeState("ready-replacement", true)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			posts.Add(1)
			http.Error(w, "no effect allowed", http.StatusMethodNotAllowed)
			return
		}
		if r.URL.Path != "/radio/handoff/state" || r.Header.Get("X-PodConnect-Token") != "test-control-token" {
			t.Error("unexpected passive request", r.URL.Path)
		}
		if calls.Add(1) < 7 {
			json.NewEncoder(w).Encode(first)
		} else {
			json.NewEncoder(w).Encode(final)
		}
	}))
	defer server.Close()
	var elapsed time.Duration
	client, err := awaitRadioNativeBootstrap(context.Background(), server.URL, "test-control-token", func(_ context.Context, delay time.Duration) error { elapsed += delay; return nil })
	if err != nil || client == nil || calls.Load() != 7 || posts.Load() != 0 || elapsed <= 20*time.Second {
		t.Fatal("cold/read-only boot did not settle", err, calls.Load(), posts.Load(), elapsed)
	}
	client.mu.Lock()
	process := client.state.ProcessID
	client.mu.Unlock()
	if process != final.ProcessID {
		t.Fatal("preparation inherited old Native process", process)
	}
}

func TestRadioReadinessSleepStopsOnCancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if !errors.Is(radioReadinessSleep(ctx, time.Hour), context.Canceled) {
		t.Fatal("cancelled readiness sleep remained live")
	}
}

func TestRadioNativeBootstrapWaitsForActualTargetBeyondHeldControllerMetadata(t *testing.T) {
	state := producerNativeStateFixture()
	state.ControllerID = "AABBCCDDEEFF0011"
	state.Missing = []string{}
	state.Targets[0].Selectable = false
	state.Targets[0].Availability = "unknown"
	var reads atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Error("startup sent an effect", r.Method)
		}
		current := state
		current.Targets = append([]radioProducerTarget{}, state.Targets...)
		if reads.Add(1) >= 5 {
			current.Targets[0].Selectable = true
			current.Targets[0].Availability = "available"
		}
		json.NewEncoder(w).Encode(current)
	}))
	defer server.Close()
	pauses := 0
	client, err := awaitRadioNativeBootstrap(context.Background(), server.URL, "control-token", func(context.Context, time.Duration) error { pauses++; return nil })
	if err != nil || client == nil || reads.Load() != 5 || pauses != 4 {
		t.Fatal("held-only metadata consumed initial preparation", err, reads.Load(), pauses)
	}
}

func TestRadioNativeBootstrapLogsOnlyChangedClosedReadiness(t *testing.T) {
	var logs bytes.Buffer
	previous := log.Writer()
	log.SetOutput(&logs)
	t.Cleanup(func() { log.SetOutput(previous) })

	state := producerNativeStateFixture()
	state.ControllerID = ""
	state.Targets[0].ID = "fixture-private-target-id"
	state.Targets[0].Selectable = false
	state.Targets[0].Availability = "unknown"
	var reads atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/radio/handoff/state" {
			t.Error("bootstrap made a request other than the passive Native read")
			http.Error(w, "unexpected request", http.StatusBadRequest)
			return
		}
		reply := state
		switch reads.Add(1) {
		case 1, 2:
			http.Error(w, "fixture-private-upstream-body", http.StatusServiceUnavailable)
			return
		case 3:
			reply.Missing = []string{"fresh_receiver_source_proof", "fixture-private-proof"}
		case 4:
			reply.Missing = []string{"different-private-proof", "fresh_receiver_source_proof", "fresh_receiver_source_proof"}
		default:
			reply.ControllerID = "AABBCCDDEEFF0011"
			reply.Targets[0].Selectable = true
			reply.Targets[0].Availability = "available"
			reply.Missing = nil
		}
		json.NewEncoder(w).Encode(reply)
	}))
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	client, err := awaitRadioNativeBootstrap(ctx, server.URL, "fixture-private-token", func(context.Context, time.Duration) error { return nil })
	if err != nil || client == nil || reads.Load() != 5 {
		t.Fatal("diagnostics changed passive bootstrap", err, reads.Load())
	}
	lines := strings.Split(strings.TrimSpace(logs.String()), "\n")
	expected := []string{
		"result=read_failed",
		"result=waiting controller_registered=false configured_targets=1 selectable_available=0 missing_proofs=[actual_registered_radio_controller_binding,fresh_receiver_source_proof,other]",
		"result=ready controller_registered=true configured_targets=1 selectable_available=1 missing_proofs=[]",
	}
	if len(lines) != len(expected) {
		t.Fatal("unchanged readiness logged again")
	}
	for i, want := range expected {
		if !strings.HasSuffix(lines[i], "radio: bootstrap phase=native_wait "+want) {
			t.Fatal("unexpected or unbounded readiness fields at entry", i)
		}
	}
	for _, private := range []string{"fixture-private", "different-private", "AABBCCDDEEFF0011", server.URL, state.ProcessID} {
		if strings.Contains(logs.String(), private) {
			t.Fatal("private Native data escaped to readiness logs")
		}
	}
}
