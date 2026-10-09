package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

const testConversationA = "00000000-0000-4000-8000-000000000001"
const testConversationB = "00000000-0000-4000-8000-000000000002"
const testNativeProcess = "00000000-0000-4000-8000-000000000003"

func TestAttentionChallengeDoesNotClaimAndOldBeginCannotReplace(t *testing.T) {
	a := &attention{}
	observed := a.challenge()
	if a.challenge() != observed || a.active || a.nativePending {
		t.Fatal("GET changed admission")
	}
	if !a.engageOwned(testConversationA, observed, true, 5, "voice", defaultAttentionTTL, time.Now()) {
		t.Fatal("first BEGIN refused")
	}
	if !a.engageOwned(testConversationA, observed, true, 35, "voice", defaultAttentionTTL, time.Now()) || a.level != 5 || a.admissionRevision != 1 {
		t.Fatal("same nonce minted/changed another admission")
	}
	admitted := a.challenge()
	if a.engageOwned(testConversationB, observed, true, 5, "voice", defaultAttentionTTL, time.Now()) {
		t.Fatal("old challenge replaced admitted conversation")
	}
	if a.releaseOwned(testConversationA, observed) {
		t.Fatal("release accepted preclaim currency")
	}
	if !a.releaseOwned(testConversationA, admitted) {
		t.Fatal("exact release refused")
	}
	if a.engageOwned(testConversationB, admitted, true, 5, "voice", defaultAttentionTTL, time.Now()) {
		t.Fatal("new BEGIN replaced pending restoration")
	}
	if !a.nativeReconcile("", "", false) {
		t.Fatal("no native grants should settle without I/O")
	}
	if !a.engageOwned(testConversationB, admitted, true, 5, "voice", defaultAttentionTTL, time.Now()) {
		t.Fatal("new conversation rejected after exact restoration")
	}
	if a.engageOwned(testConversationA, observed, true, 5, "voice", defaultAttentionTTL, time.Now()) {
		t.Fatal("late A crossed B")
	}
}

// Fixed wire receipts are inert manager contract fixtures, not native AP2 ACKs.
func TestAttentionHandoffRestoresOriginalOutputBasesAndTruthfulDesiredOnly(t *testing.T) {
	var mu sync.Mutex
	commands := []nativeAttentionCommand{}
	states := map[string]nativeAttentionIdentity{}
	for id, base := range map[string]int{"1": 30, "2": 65} {
		states[id] = nativeAttentionIdentity{testNativeProcess, id, "1", "0", "1", "0", "1", "0", base, base, false}
	}
	receipts := map[string]nativeAttentionResult{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
		if len(parts) < 4 {
			t.Errorf("unexpected path %s", r.URL.Path)
			http.Error(w, "path", 400)
			return
		}
		id := parts[2]
		if r.Method == http.MethodGet {
			if len(parts) == 4 {
				json.NewEncoder(w).Encode(nativeAttentionObservation{states[id], "0"})
				return
			}
			json.NewEncoder(w).Encode(receipts[parts[4]])
			return
		}
		var command nativeAttentionCommand
		if json.NewDecoder(r.Body).Decode(&command) != nil {
			http.Error(w, "body", 400)
			return
		}
		if command.Expected != states[id] {
			http.Error(w, "stale", 409)
			return
		}
		commands = append(commands, command)
		identity := command.Expected
		identity.AttentionRevision = "2"
		identity.Lease = id
		identity.Effective = command.Cap
		if command.Action == "release" {
			identity.AttentionRevision = "3"
			identity.Effective = identity.Base
			identity.Restoring = true
		}
		result := nativeAttentionResult{Identity: identity, RequestID: command.RequestID, Operation: command.RequestID[len(command.RequestID)-1:], Action: command.Action, Outcome: "desired_only", Current: true}
		// Operation identity is an explicit ordinal, unrelated to UUID text.
		if id == "1" && command.Action == "begin" {
			result.Operation = "1"
		} else if id == "2" && command.Action == "begin" {
			result.Operation = "2"
		} else if id == "1" {
			result.Operation = "3"
		} else {
			result.Operation = "4"
		}
		receipts[result.Operation] = result
		states[id] = identity
		if command.Action == "release" {
			current := identity
			current.Lease = "0"
			current.Restoring = false
			states[id] = current
		}
		json.NewEncoder(w).Encode(result)
	}))
	defer srv.Close()
	a := &attention{}
	challenge := a.challenge()
	if !a.engageOwned(testConversationA, challenge, true, 5, "voice", defaultAttentionTTL, time.Now()) {
		t.Fatal("admission")
	}
	if !a.nativeTarget(srv.URL, "1") || !a.nativeTarget(srv.URL, "2") || !a.nativeReconcile(srv.URL, "2", false) {
		t.Fatal("handoff unresolved")
	}
	if !a.releaseOwned(testConversationA, a.challenge()) || !a.nativeReconcile(srv.URL, "2", false) {
		t.Fatal("release unresolved")
	}
	mu.Lock()
	defer mu.Unlock()
	if len(commands) != 4 || commands[2].Action != "release" || commands[2].Expected.DeviceID != "1" || commands[2].Expected.Base != 30 || commands[3].Expected.DeviceID != "2" || commands[3].Expected.Base != 65 {
		t.Fatalf("cross-output restore: %+v", commands)
	}
	snapshot := a.snapshot(time.Now())
	if snapshot.Outcome != "released" || snapshot.NativeResults["1"].Outcome != "desired_only" || snapshot.NativeResults["2"].Outcome != "desired_only" {
		t.Fatalf("desired-only mislabeled: %+v", snapshot)
	}
}

func TestAttentionLostNativeResponseObservesOriginalUUIDWithoutSecondPUT(t *testing.T) {
	initial := nativeAttentionIdentity{testNativeProcess, "1", "1", "0", "1", "0", "1", "0", 30, 30, false}
	var mu sync.Mutex
	var sent []nativeAttentionCommand
	stateGets, receiptGets := 0, 0
	var receipt nativeAttentionResult
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		if r.Method == http.MethodGet {
			if r.URL.Query().Get("request_id") == "" {
				stateGets++
				json.NewEncoder(w).Encode(nativeAttentionObservation{initial, "0"})
				return
			}
			receiptGets++
			if r.URL.Query().Get("process") != initial.Process || r.URL.Query().Get("request_id") != receipt.RequestID {
				http.Error(w, "original receipt missing", 404)
				return
			}
			json.NewEncoder(w).Encode(receipt)
			return
		}
		var command nativeAttentionCommand
		if json.NewDecoder(r.Body).Decode(&command) != nil {
			t.Error("body")
			return
		}
		sent = append(sent, command)
		identity := initial
		identity.Lease = "7"
		identity.AttentionRevision = "1"
		identity.Effective = 5
		receipt = nativeAttentionResult{Identity: identity, RequestID: command.RequestID, Operation: "7", Action: "begin", Outcome: "desired_only", Current: true}
		conn, _, err := w.(http.Hijacker).Hijack()
		if err != nil {
			t.Error(err)
			return
		}
		conn.Close()
	}))
	defer srv.Close()
	a := &attention{}
	challenge := a.challenge()
	if !a.engageOwned(testConversationA, challenge, true, 5, "voice", defaultAttentionTTL, time.Now()) {
		t.Fatal("admission")
	}
	if a.nativeTarget(srv.URL, "1") {
		t.Fatal("lost response became success")
	}
	if a.engageOwned(testConversationB, a.challenge(), true, 5, "voice", defaultAttentionTTL, time.Now()) {
		t.Fatal("lost native grant replaced")
	}
	if !a.nativeTarget(srv.URL, "1") {
		t.Fatal("original receipt did not settle")
	}
	mu.Lock()
	defer mu.Unlock()
	if stateGets != 1 || receiptGets != 1 || len(sent) != 1 || sent[0].RequestID == "" || receipt.RequestID != sent[0].RequestID {
		t.Fatalf("fresh GET/new command adoption: state%d receipt%d commands%+v", stateGets, receiptGets, sent)
	}
}

// No synthetic receipt is supplied for a never-admitted command. Retiring the
// manager source grants observation/cleanup custody, never a first stale PUT.
func TestUncertainBeginAndUpdateAfterReleaseCannotReplayFirstCommand(t *testing.T) {
	for _, action := range []string{"begin", "update"} {
		t.Run(action, func(t *testing.T) {
			var mu sync.Mutex
			puts, gets := 0, 0
			state := nativeAttentionIdentity{testNativeProcess, "1", "1", "0", "1", "0", "1", "0", 30, 30, false}
			if action == "update" {
				state.Lease = "7"
				state.AttentionRevision = "1"
				state.Effective = 5
			}
			command := nativeAttentionCommand{state, testConversationB, action, 5, 2000}
			grant := &nativeAttentionGrant{Command: command, Uncertain: true}
			if action == "update" {
				grant.Identity = state
			}
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				defer mu.Unlock()
				if r.Method == http.MethodPut {
					puts++
					conn, _, err := w.(http.Hijacker).Hijack()
					if err != nil {
						t.Error(err)
						return
					}
					conn.Close()
					return
				}
				gets++
				if r.URL.Query().Get("process") != state.Process || r.URL.Query().Get("request_id") != command.RequestID {
					t.Error("observation borrowed another command")
				}
				http.Error(w, "receipt absent", 404)
			}))
			defer srv.Close()
			if grant.submit(srv.URL, "1") == nil {
				t.Fatal("missing reply accepted")
			}
			a := &attention{}
			if !a.engageOwned(testConversationA, a.challenge(), true, 5, "voice", defaultAttentionTTL, time.Now()) {
				t.Fatal("begin")
			}
			a.mu.Lock()
			a.nativeGrants = map[string]*nativeAttentionGrant{"1": grant}
			a.nativePending = true
			a.mu.Unlock()
			if !a.releaseOwned(testConversationA, a.challenge()) {
				t.Fatal("release")
			}
			retained := grant.Command
			for i := 0; i < 2; i++ {
				if a.nativeReconcile(srv.URL, "1", false) {
					t.Fatal("404 became restoration success")
				}
			}
			mu.Lock()
			defer mu.Unlock()
			if puts != 1 || gets != 2 || grant.Command != retained || !grant.Uncertain {
				t.Fatalf("retired uncertain command reapplied or replaced: PUT%d GET%d %+v", puts, gets, grant)
			}
			a.mu.Lock()
			defer a.mu.Unlock()
			if !a.pendingRelease || a.nativeGrants["1"] != grant {
				t.Fatal("unknown receipt discarded original cleanup custody")
			}
		})
	}
}

func TestHeldUpdateStateGETCannotReplaceGrantAfterReleaseOrNewRevision(t *testing.T) {
	for _, retire := range []string{"release", "new_update"} {
		t.Run(retire, func(t *testing.T) {
			state := nativeAttentionIdentity{testNativeProcess, "1", "1", "0", "1", "1", "1", "7", 30, 5, false}
			original := nativeAttentionCommand{state, testConversationB, "begin", 5, 2000}
			originalResult := nativeAttentionResult{Identity: state, RequestID: original.RequestID, Operation: "7", Action: "begin", Outcome: "desired_only", Current: true}
			a := &attention{}
			if !a.engageOwned(testConversationA, a.challenge(), true, 5, "voice", defaultAttentionTTL, time.Now()) {
				t.Fatal("begin")
			}
			grant := &nativeAttentionGrant{Identity: state, Command: original, Result: originalResult, Submitted: true, Revision: 1}
			a.mu.Lock()
			a.nativeGrants = map[string]*nativeAttentionGrant{"1": grant}
			a.nativePending = true
			a.mu.Unlock()
			if !a.engageOwned(testConversationA, a.challenge(), false, 6, "voice", defaultAttentionTTL, time.Now()) {
				t.Fatal("update")
			}
			entered, release, joined := make(chan struct{}), make(chan struct{}), make(chan struct{})
			result := false
			puts := 0
			var mu sync.Mutex
			var once sync.Once
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodPut {
					mu.Lock()
					puts++
					mu.Unlock()
					http.Error(w, "stale UPDATE", 409)
					return
				}
				close(entered)
				select {
				case <-release:
				case <-r.Context().Done():
					return
				}
				json.NewEncoder(w).Encode(nativeAttentionObservation{state, "0"})
			}))
			// Rescue precedes server Close even if an original assertion fails.
			defer func() {
				once.Do(func() { close(release) })
				select {
				case <-joined:
				case <-time.After(5 * time.Second):
					t.Error("retained update owner did not join")
				}
				srv.Close()
			}()
			go func() { defer close(joined); result = a.nativeTarget(srv.URL, "1") }()
			select {
			case <-entered:
			case <-time.After(time.Second):
				t.Fatal("state GET not entered")
			}
			if retire == "release" {
				if !a.releaseOwned(testConversationA, a.challenge()) {
					t.Fatal("release")
				}
			} else if !a.engageOwned(testConversationA, a.challenge(), false, 7, "voice", defaultAttentionTTL, time.Now()) {
				t.Fatal("new revision")
			}
			once.Do(func() { close(release) })
			select {
			case <-joined:
			case <-time.After(5 * time.Second):
				t.Fatal("update owner did not return")
			}
			if result || grant.Command != original || grant.Result != originalResult || grant.Revision != 1 || !grant.Submitted {
				t.Fatal("retired GET replaced original grant or admitted UPDATE")
			}
			mu.Lock()
			defer mu.Unlock()
			if puts != 0 {
				t.Fatal("stale UPDATE emitted")
			}
		})
	}
}

// Actual elapsed manager deadline, no snapshot/tick/expire call while GET is
// held. The only expiry owner after the response is the production admission.
func TestHeldNativeStateGETCannotRenewExpiredBeginOrUpdate(t *testing.T) {
	for _, action := range []string{"begin", "update"} {
		t.Run(action, func(t *testing.T) {
			state := nativeAttentionIdentity{testNativeProcess, "1", "1", "0", "1", "0", "1", "0", 30, 30, false}
			a := &attention{}
			if !a.engageOwned(testConversationA, a.challenge(), true, 5, "voice", defaultAttentionTTL, time.Now()) {
				t.Fatal("begin")
			}
			var original *nativeAttentionGrant
			var originalCommand nativeAttentionCommand
			var originalResult nativeAttentionResult
			if action == "update" {
				state.Lease = "7"
				state.AttentionRevision = "1"
				state.Effective = 5
				originalCommand = nativeAttentionCommand{state, testConversationB, "begin", 5, 2000}
				originalResult = nativeAttentionResult{Identity: state, RequestID: originalCommand.RequestID, Operation: "7", Action: "begin", Outcome: "desired_only", Current: true}
				original = &nativeAttentionGrant{Identity: state, Command: originalCommand, Result: originalResult, Submitted: true, Revision: 1}
				a.mu.Lock()
				a.nativeGrants = map[string]*nativeAttentionGrant{"1": original}
				a.nativePending = true
				a.mu.Unlock()
				if !a.engageOwned(testConversationA, a.challenge(), false, 6, "voice", defaultAttentionTTL, time.Now()) {
					t.Fatal("update")
				}
			}
			a.mu.Lock()
			deadline, revision, admission := a.deadline, a.nativeRevision, a.admissionRevision
			a.mu.Unlock()
			entered, release, joined := make(chan struct{}), make(chan struct{}), make(chan struct{})
			var once sync.Once
			var mu sync.Mutex
			puts := 0
			result := false
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet {
					mu.Lock()
					puts++
					mu.Unlock()
					http.Error(w, "expired command", 409)
					return
				}
				close(entered)
				select {
				case <-release:
				case <-r.Context().Done():
					return
				}
				json.NewEncoder(w).Encode(nativeAttentionObservation{state, "0"})
			}))
			defer func() {
				once.Do(func() { close(release) })
				select {
				case <-joined:
				case <-time.After(5 * time.Second):
					t.Error("retained deadline GET owner did not join")
				}
				srv.Close()
			}()
			go func() { defer close(joined); result = a.nativeTarget(srv.URL, "1") }()
			select {
			case <-entered:
			case <-time.After(time.Second):
				t.Fatal("state GET not entered")
			}
			timer := time.NewTimer(time.Until(deadline))
			defer timer.Stop()
			select {
			case <-timer.C:
			case <-time.After(3 * time.Second):
				t.Fatal("existing manager deadline did not elapse")
			}
			if !time.Now().After(deadline) {
				t.Fatal("deadline witness premature")
			}
			// Read only: prove no other expiry owner cleared active while the GET waited.
			a.mu.Lock()
			stillActive := a.active
			a.mu.Unlock()
			if !stillActive {
				t.Fatal("another expiry owner masked the tested admission")
			}
			once.Do(func() { close(release) })
			select {
			case <-joined:
			case <-time.After(5 * time.Second):
				t.Fatal("held state GET owner did not return")
			}
			if result {
				t.Fatal("expired native admission accepted")
			}
			mu.Lock()
			if puts != 0 {
				t.Errorf("expired %s emitted PUT", action)
			}
			mu.Unlock()
			a.mu.Lock()
			defer a.mu.Unlock()
			if a.active || a.sessionNonce != testConversationA || a.nativeRevision != revision || a.admissionRevision != admission || !a.deadline.Equal(deadline) {
				t.Fatal("admission changed original currency/deadline")
			}
			if action == "update" {
				if a.nativeGrants["1"] != original || original.Command != originalCommand || original.Result != originalResult || original.Revision != 1 || !a.pendingRelease {
					t.Fatal("expired UPDATE discarded original lease cleanup/receipt")
				}
			} else if len(a.nativeGrants) != 0 {
				t.Fatal("expired BEGIN minted a native grant")
			}
		})
	}
}
