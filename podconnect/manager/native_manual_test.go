package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

func manualOwnerFixture(t *testing.T) (*glLive, glSource, *aliasBinding, nativeAttentionIdentity) {
	t.Helper()
	_, binding := setupLocalAliasRooms(t)
	live := &glLive{}
	run := live.beginRun(make(chan struct{}))
	source, ok := live.beginPhase(run)
	if !ok {
		t.Fatal("source")
	}
	live.set(glStatus{SelAlias: 2, AliasBinding: binding})
	state := nativeAttentionIdentity{testNativeProcess, "43", "1", "0", "1", "0", "1", "0", 65, 65, false}
	return live, source, binding, state
}
func manualWire(binding *aliasBinding, ordinal string, value int) map[string]any {
	return map[string]any{"origin": "spotify_connect", "ordinal": ordinal, "value": float64(value), "alias_id": float64(2), "alias_binding": map[string]any{"incarnation": binding.Incarnation, "registry": binding.Registry, "room_id": binding.RoomID, "ready": true}}
}
func observedManual(live *glLive, state nativeAttentionIdentity) {
	live.observeNativeDevices([]device{{ID: "43", Name: "B", Selected: true, AttentionIdentity: &state}}, live.nativeCatalogRequest())
}

func TestManualActionBeforeCatalogCannotBorrowLaterSnapshot(t *testing.T) {
	live, source, binding, state := manualOwnerFixture(t)
	live.applySourceEvent(source, "volume", map[string]any{"value": float64(65)})
	if live.manualSnapshot() != nil {
		t.Fatal("generic volume became manual")
	}
	live.applySourceEvent(source, "volume_action", manualWire(binding, "1", 65))
	first := live.manualSnapshot()
	if first == nil || first.Observation != nil {
		t.Fatal("action minted native challenge")
	}
	observedManual(live, state)
	if live.manualSnapshot().Observation != nil {
		t.Fatal("later catalog retroactively rebased action")
	}
	live.applySourceEvent(source, "volume_action", manualWire(binding, "2", 65))
	second := live.manualSnapshot()
	if second == nil || second.ID == first.ID || second.Observation == nil || second.Observation.Identity != state {
		t.Fatal("new equal-value action did not retain previous atomic snapshot")
	}
	observedManual(live, nativeAttentionIdentity{testNativeProcess, "43", "1", "0", "2", "0", "1", "0", 30, 30, false})
	if live.manualSnapshot().Observation.Identity != state {
		t.Fatal("challenge changed after action admission")
	}
}

func TestManagerEchoAndOldSourceHaveNoManualAuthority(t *testing.T) {
	live, source, binding, state := manualOwnerFixture(t)
	observedManual(live, state)
	for _, origin := range []string{attentionManagerProcess(), testConversationA} {
		event := manualWire(binding, "1", 65)
		event["origin"] = "api"
		event["manager_process"] = origin
		event["request_id"] = testConversationB
		live.applySourceEvent(source, "volume_action", event)
		if live.manualSnapshot() != nil {
			t.Fatal("current or previous manager echo acquired authority")
		}
	}
	live.applySourceEvent(source, "volume_action", manualWire(binding, "1", 65))
	first := live.manualSnapshot()
	if first == nil {
		t.Fatal("external action")
	}
	live.applySourceEvent(source, "volume_action", manualWire(binding, "1", 30))
	if live.manualSnapshot().ID != first.ID {
		t.Fatal("same ordinal replay admitted")
	}
	live.retirePhase(source)
	fresh, ok := live.beginPhase(glSource{run: source.run, stop: source.stop})
	if !ok {
		t.Fatal("phase")
	}
	live.applySourceEvent(source, "volume_action", manualWire(binding, "2", 30))
	if live.manualSnapshot().ID != first.ID || live.claimManual(*first) {
		t.Fatal("retired source crossed phase")
	}
	live.applySourceEvent(fresh, "volume_action", manualWire(binding, "2", 30))
	if live.manualSnapshot().Observation != nil {
		t.Fatal("old phase observation crossed fresh event")
	}
}

// This is actual HTTP owner behavior with inert wire receipts, not native ACKs.
func TestManualLostResponseObservesOriginalUUIDWithoutSecondPUT(t *testing.T) {
	live, source, binding, state := manualOwnerFixture(t)
	observedManual(live, state)
	live.applySourceEvent(source, "volume_action", manualWire(binding, "1", 30))
	var mu sync.Mutex
	var commands []nativeAttentionCommand
	gets := 0
	var receipt nativeAttentionResult
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		if r.Method == http.MethodGet {
			gets++
			if r.URL.Query().Get("process") != state.Process || r.URL.Query().Get("request_id") != receipt.RequestID {
				http.Error(w, "missing original receipt", 404)
				return
			}
			json.NewEncoder(w).Encode(receipt)
			return
		}
		var command nativeAttentionCommand
		if json.NewDecoder(r.Body).Decode(&command) != nil {
			http.Error(w, "body", 400)
			return
		}
		commands = append(commands, command)
		identity := state
		identity.BaseRevision = "2"
		identity.Base = 30
		identity.Effective = 30
		receipt = nativeAttentionResult{Identity: identity, RequestID: command.RequestID, Operation: "9", Action: "manual", Outcome: "desired_only", Current: true}
		conn, _, err := w.(http.Hijacker).Hijack()
		if err != nil {
			t.Errorf("hijack: %v", err)
			return
		}
		conn.Close()
	}))
	defer srv.Close()
	attention := &attention{}
	if attention.nativeManualReconcile(srv.URL, live) {
		t.Fatal("lost response became accepted")
	}
	// Retired event ownership still permits observing the already admitted UUID.
	live.retirePhase(source)
	if !attention.nativeManualReconcile(srv.URL, live) {
		t.Fatal("same original receipt did not settle")
	}
	mu.Lock()
	defer mu.Unlock()
	if gets != 1 || len(commands) != 1 || commands[0].Expected != state || receipt.RequestID != commands[0].RequestID {
		t.Fatalf("rebased/reapplied: %+v GET%d", commands, gets)
	}
	report := attention.snapshot(time.Now()).ManualResult
	if report == nil || report.Result.Outcome != "desired_only" || report.Result.Identity.BaseRevision != "2" {
		t.Fatalf("false native acceptance: %+v", report)
	}
}

func TestNeverReceivedManualAfterSourceRetirementIsLookupOnlyUnknown(t *testing.T) {
	live, source, binding, state := manualOwnerFixture(t)
	observedManual(live, state)
	live.applySourceEvent(source, "volume_action", manualWire(binding, "1", 30))
	var mu sync.Mutex
	puts, gets := 0, 0
	var original nativeAttentionCommand
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		if r.Method == http.MethodGet {
			gets++
			if r.URL.Query().Get("process") != state.Process || r.URL.Query().Get("request_id") != original.RequestID {
				t.Error("lookup lost original identity")
			}
			http.Error(w, "not admitted or evicted", 404)
			return
		}
		puts++
		json.NewDecoder(r.Body).Decode(&original)
		// The native operation was never admitted; EOF alone cannot prove otherwise.
		conn, _, err := w.(http.Hijacker).Hijack()
		if err != nil {
			t.Error(err)
			return
		}
		conn.Close()
	}))
	defer srv.Close()
	attention := &attention{}
	if attention.nativeManualReconcile(srv.URL, live) {
		t.Fatal("missing reply became known")
	}
	live.retirePhase(source)
	if !attention.nativeManualReconcile(srv.URL, live) {
		t.Fatal("missing lookup was not reported")
	}
	if !attention.nativeManualReconcile(srv.URL, live) {
		t.Fatal("terminal UNKNOWN unexpectedly reapplied")
	}
	report := attention.snapshot(time.Now()).ManualResult
	if report == nil || !strings.HasPrefix(report.Fault, "unknown:") || report.Result.Operation != "" {
		t.Fatalf("404 claimed rejection/apply: %+v", report)
	}
	mu.Lock()
	defer mu.Unlock()
	if puts != 1 || gets != 1 {
		t.Fatalf("first stale apply: PUT%d GET%d", puts, gets)
	}
}

// The actual catalog function owns both token capture before HTTP and fold after
// the held response. Helper-only snapshots cannot prove this ordering.
func TestHeldCatalogCannotPromoteOldSourceAcrossReconnectOrAliasABA(t *testing.T) {
	for _, transition := range []string{"phase", "alias_ABA"} {
		t.Run(transition, func(t *testing.T) {
			live, source, binding, state := manualOwnerFixture(t)
			entered, release, joined := make(chan struct{}), make(chan struct{}), make(chan struct{})
			var once sync.Once
			var mu sync.Mutex
			gets := 0
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet || r.URL.Path != "/api/outputs" {
					http.Error(w, "unexpected native command", 400)
					return
				}
				mu.Lock()
				gets++
				count := gets
				mu.Unlock()
				if count == 1 {
					close(entered)
					select {
					case <-release:
					case <-r.Context().Done():
						return
					}
				}
				json.NewEncoder(w).Encode(map[string]any{"outputs": []map[string]any{{"id": "43", "name": "B", "type": "AirPlay", "selected": true, "volume": 65, "attention_identity": state}}})
			}))
			defer func() {
				once.Do(func() { close(release) })
				select {
				case <-joined:
				case <-time.After(4 * time.Second):
					t.Error("retained catalog owner did not join")
				}
				srv.Close()
			}()
			go func() { defer close(joined); owntoneOutputVolumeForIntent(srv.URL, live) }()
			select {
			case <-entered:
			case <-time.After(time.Second):
				t.Fatal("catalog GET not entered")
			}
			if transition == "phase" {
				live.retirePhase(source)
				var ok bool
				source, ok = live.beginPhase(glSource{run: source.run, stop: source.stop})
				if !ok {
					t.Fatal("new source")
				}
			} else {
				live.set(glStatus{SelAlias: 1})
				live.set(glStatus{SelAlias: 2, AliasBinding: binding})
			}
			once.Do(func() { close(release) })
			select {
			case <-joined:
			case <-time.After(4 * time.Second):
				t.Fatal("held catalog did not return")
			}
			live.applySourceEvent(source, "volume_action", manualWire(binding, "1", 30))
			if action := live.manualSnapshot(); action == nil || action.Observation != nil {
				t.Fatal("old catalog stamped new source/ABA challenge")
			}
			if _, _, ok := owntoneOutputVolumeForIntent(srv.URL, live); !ok {
				t.Fatal("fresh actual catalog absent")
			}
			live.applySourceEvent(source, "volume_action", manualWire(binding, "2", 30))
			if action := live.manualSnapshot(); action == nil || action.Observation == nil || action.Observation.Identity != state {
				t.Fatal("new action lost its fresh previous observation")
			}
		})
	}
}

func TestManualDelayedCASConflictNeverRefreshesAndReplays(t *testing.T) {
	live, source, binding, state := manualOwnerFixture(t)
	observedManual(live, state)
	live.applySourceEvent(source, "volume_action", manualWire(binding, "1", 30))
	entered, release, done := make(chan struct{}), make(chan struct{}), make(chan bool, 1)
	var mu sync.Mutex
	current := state
	puts, gets := 0, 0
	var once sync.Once
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut {
			mu.Lock()
			gets++
			mu.Unlock()
			http.Error(w, "unexpected GET", 400)
			return
		}
		var command nativeAttentionCommand
		json.NewDecoder(r.Body).Decode(&command)
		mu.Lock()
		puts++
		count := puts
		mu.Unlock()
		if count == 1 {
			close(entered)
			select {
			case <-release:
			case <-r.Context().Done():
				return
			}
		}
		mu.Lock()
		defer mu.Unlock()
		if command.Expected != current {
			http.Error(w, "CAS conflict", 409)
			return
		}
		n, _ := strconv.ParseUint(current.BaseRevision, 10, 64)
		current.BaseRevision = strconv.FormatUint(n+1, 10)
		current.Base = command.Cap
		current.Effective = command.Cap
		json.NewEncoder(w).Encode(nativeAttentionResult{Identity: current, RequestID: command.RequestID, Operation: "11", Action: "manual", Outcome: "desired_only", Current: true})
	}))
	attention := &attention{}
	go func() { done <- attention.nativeManualReconcile(srv.URL, live) }()
	defer func() {
		once.Do(func() { close(release) })
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("retained manual owner did not join")
		}
		srv.Close()
	}()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("HTTP owner not entered")
	}
	mu.Lock()
	current.BaseRevision = "2"
	current.Base = 65
	newer := current
	mu.Unlock()
	once.Do(func() { close(release) })
	select {
	case settled := <-done:
		if !settled {
			t.Fatal("conflict did not become truthful terminal")
		}
		done <- settled
	case <-time.After(5 * time.Second):
		t.Fatal("owner did not return")
	}
	observedManual(live, newer)
	if !attention.nativeManualReconcile(srv.URL, live) {
		t.Fatal("terminal conflict remained pending")
	}
	mu.Lock()
	if puts != 1 || gets != 0 || current.Base != 65 {
		t.Fatalf("old action replayed over HA manual: %d/%d %+v", puts, gets, current)
	}
	mu.Unlock()
	if attention.snapshot(time.Now()).ManualResult.Fault == "" {
		t.Fatal("conflict hidden")
	}
	live.applySourceEvent(source, "volume_action", manualWire(binding, "2", 30))
	if !attention.nativeManualReconcile(srv.URL, live) {
		t.Fatal("subsequently NEW action not admitted")
	}
	mu.Lock()
	defer mu.Unlock()
	if puts != 2 || gets != 0 || current.Base != 30 {
		t.Fatal("new action did not use its independently observed challenge")
	}
}

func TestManualPendingRestoreRetainsLeaseAndOriginalCapState(t *testing.T) {
	live, source, binding, state := manualOwnerFixture(t)
	state.Lease = "8"
	state.AttentionRevision = "3"
	state.Restoring = true
	observedManual(live, state)
	live.applySourceEvent(source, "volume_action", manualWire(binding, "1", 30))
	var command nativeAttentionCommand
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut {
			http.Error(w, "GET forbidden", 400)
			return
		}
		json.NewDecoder(r.Body).Decode(&command)
		after := state
		after.BaseRevision = "2"
		after.Base = 30
		after.Effective = 30
		json.NewEncoder(w).Encode(nativeAttentionResult{Identity: after, RequestID: command.RequestID, Operation: "12", Action: "manual", Outcome: "unknown", Current: true})
	}))
	defer srv.Close()
	attention := &attention{}
	if !attention.nativeManualReconcile(srv.URL, live) {
		t.Fatal("immutable UNKNOWN not terminal")
	}
	if command.Expected != state || command.TTL != 0 || command.Action != "manual" {
		t.Fatal("manual intent renewed/adopted restoration")
	}
	result := attention.snapshot(time.Now()).ManualResult.Result
	if result.Identity.Lease != "8" || !result.Identity.Restoring || result.Outcome != "unknown" {
		t.Fatal("unknown manual reply cleared restoration or became ACK")
	}
}
