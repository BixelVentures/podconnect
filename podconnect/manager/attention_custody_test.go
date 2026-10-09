package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func readCustodyTest(t *testing.T, path string) attentionCustody {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var doc attentionCustody
	if err = json.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	return doc
}

// These inert protocol receipts prove manager correlation, not HomePod RTSP.
func TestCustodyCrashRetiresEveryOriginalBeginWithoutReplay(t *testing.T) {
	setupAttentionRoom(t)
	originalManagerProcess := attentionManagerProcess()
	defer func() { attentionProcess = originalManagerProcess }()
	a := attentionFor("r0")
	path := filepath.Join(dataDir, "rooms", "r0", "attention-custody.json")
	a.loadCustody(path, "r0")
	before := a.challenge()
	preCrashState := callAttention(attentionHandler, http.MethodGet, "/api/attention", "", nil)
	originalBody := `{"room":"r0","session":"` + testConversationA + `","begin":true,"level":5,"ttl_ms":15000,"expected":{"process":"` + before.Process + `","revision":"` + before.Revision + `"}}`
	originalBegin := callAttention(attentionHandler, http.MethodPost, "/api/attention", originalBody, nil)
	if originalBegin.Code != 200 {
		t.Fatal("original actual BEGIN not admitted")
	}
	oldChallenge := a.challenge()
	doc := readCustodyTest(t, path)
	if len(doc.Sessions) != 1 || doc.Sessions[0].Nonce != testConversationA || doc.Sessions[0].Revision != oldChallenge.Revision {
		t.Fatal("ACK lacks durable session")
	}

	var mu sync.Mutex
	nativeProcess := testNativeProcess
	origins := map[string]nativeAttentionOrigin{}
	begins, recovers := 0, 0
	secondLost := false
	unknown := true
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
		id := parts[2]
		state := nativeAttentionIdentity{nativeProcess, id, "1", "0", "1", "0", "1", "0", 65, 65, false}
		if r.Method == http.MethodGet {
			json.NewEncoder(w).Encode(nativeRecoveryObservation{nativeAttentionObservation: nativeAttentionObservation{state, "0"}, Idle: custodyIdle(true)})
			return
		}
		var command nativeRecoveryCommand
		if err := json.NewDecoder(r.Body).Decode(&command); err != nil {
			t.Error(err)
			return
		}
		persisted := readCustodyTest(t, path)
		if command.Action == "begin" {
			begins++
			child := persisted.Sessions[0].Outputs[command.RequestID]
			if child == nil || child.DeviceID != id || child.Origin.Process != command.Expected.Process || child.Base != srvURL(r) {
				t.Error("native BEGIN outran original durable custody")
			}
			origins[id] = nativeAttentionOrigin{command.Expected.Process, command.RequestID}
			state.Lease = id
			state.Effective = command.Cap
			if id == "2" && !secondLost {
				secondLost = true
				conn, _, err := w.(http.Hijacker).Hijack()
				if err != nil {
					t.Error(err)
					return
				}
				conn.Close()
				return
			}
			json.NewEncoder(w).Encode(nativeAttentionResult{Identity: state, RequestID: command.RequestID, Operation: id, Action: "begin", Outcome: "desired_only", Current: true})
			return
		}
		if command.Action != "recover" {
			t.Errorf("replayed command %s", command.Action)
			http.Error(w, "action", 400)
			return
		}
		recovers++
		child := persisted.Sessions[0].Outputs[command.Origin.RequestID]
		if child == nil || child.Recovery == nil || child.Recovery.RequestID != command.RequestID || command.Origin != origins[id] || command.Expected.Process != nativeProcess || command.Cap != 0 || command.TTL != 0 {
			t.Error("fresh recovery lacks durable original/fresh binding")
		}
		outcome := "protocol_accepted"
		code, cseq := 200, 7
		if unknown {
			outcome = "unknown"
			code, cseq = 0, 0
		}
		json.NewEncoder(w).Encode(nativeRecoveryResult{nativeAttentionResult: nativeAttentionResult{Identity: state, RequestID: command.RequestID, Operation: id, Action: "recover", Outcome: outcome, ResponseCode: code, CSeq: cseq, Current: true}, Origin: command.Origin, State: "restored", Proof: &nativeRecoveryProof{Database: testNativeProcess, LedgerComplete: true, Terminal: &nativeRecoveryTerminal{Identity: state, RequestID: command.RequestID, Operation: id, Action: "recover", ResponseCode: code, CSeq: cseq}}})
	}))
	defer srv.Close()
	if !a.nativeReconcile(srv.URL, "1", true) {
		t.Fatal("first target not admitted")
	}
	if a.nativeReconcile(srv.URL, "2", true) {
		t.Fatal("lost response reported success")
	}
	doc = readCustodyTest(t, path)
	if len(doc.Sessions[0].Outputs) != 2 {
		t.Fatal("handoff dropped original output")
	}

	// New room owner imports only cleanup custody. Native replacement loses all
	// volatile leases; the old immutable origin must still authorize fresh cleanup.
	attentionProcess = attentionUUID()
	if attentionProcess == "" || attentionProcess == originalManagerProcess {
		t.Fatal("startup must mint a genuinely new manager process")
	}
	previousRoom := mgr.runtimes["r0"].room
	mgr.runtimes["r0"] = &roomRuntime{room: previousRoom}
	restored := &mgr.runtimes["r0"].att
	restored.loadCustody(path, "r0")
	freshState := callAttention(attentionHandler, http.MethodGet, "/api/attention", "", nil)
	mu.Lock()
	nativeProcess = testConversationB
	mu.Unlock()
	if restored.engageOwned(testConversationB, restored.challenge(), true, 5, "voice", defaultAttentionTTL, time.Now()) {
		t.Fatal("new wake crossed unresolved imported custody")
	}
	if restored.nativeManualReconcile(srv.URL, &glLive{}) {
		t.Fatal("manual owner crossed unresolved custody")
	}
	blocked := httptest.NewRecorder()
	if !custodyMutationBlocked(blocked) || blocked.Code != 409 {
		t.Fatal("direct output/manual mutation not fenced")
	}
	body := `{"room":"r0","session":"` + testConversationA + `","expected":{"process":"` + oldChallenge.Process + `","revision":"` + oldChallenge.Revision + `"}}`
	pending := callAttention(attentionReleaseHandler, http.MethodPost, "/api/attention/release", body, nil)
	if pending.Code != 200 || !strings.Contains(pending.Body.String(), `"outcome":"pending"`) {
		t.Fatalf("old exact release not retained: %d %s", pending.Code, pending.Body.String())
	}
	if restored.reconcileCustody() {
		t.Fatal("unknown fresh receipt retired custody")
	}
	mu.Lock()
	unknown = false
	nativeProcess = testConversationA
	mu.Unlock() // Fresh process is cleanup authority, never old BEGIN replay.
	if !restored.reconcileCustody() {
		t.Fatal("both exact original obligations did not retire")
	}
	released := callAttention(attentionReleaseHandler, http.MethodPost, "/api/attention/release", body, nil)
	var snap attSnapshot
	if released.Code != 200 || json.Unmarshal(released.Body.Bytes(), &snap) != nil || snap.Outcome != "released" || snap.Challenge != restored.challenge() || len(snap.RecoveryResults) != 2 {
		t.Fatalf("release proof/current challenge wrong: %s", released.Body.String())
	}
	stateReply := callAttention(attentionHandler, http.MethodGet, "/api/attention", "", nil)
	challenge := restored.challenge()
	nextBody := `{"room":"r0","session":"` + testConversationB + `","begin":true,"level":5,"expected":{"process":"` + challenge.Process + `","revision":"` + challenge.Revision + `"}}`
	nextReply := callAttention(attentionHandler, http.MethodPost, "/api/attention", nextBody, nil)
	if nextReply.Code != 200 {
		t.Fatal("next wake still blocked after exact cleanup")
	}
	if export := os.Getenv("PC_CUSTODY_ADAPTER_RECEIPT"); export != "" {
		receipt := map[string]any{"original_state": json.RawMessage(preCrashState.Body.Bytes()), "original_begin": json.RawMessage(originalBegin.Body.Bytes()), "fresh_state": json.RawMessage(freshState.Body.Bytes()), "room": "r0", "old_lease": map[string]any{"session": testConversationA, "expected": oldChallenge}, "pending_release": json.RawMessage(pending.Body.Bytes()), "released": json.RawMessage(released.Body.Bytes()), "state": json.RawMessage(stateReply.Body.Bytes()), "new_session": testConversationB, "new_begin": json.RawMessage(nextReply.Body.Bytes())}
		data, err := json.MarshalIndent(receipt, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(export, data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	again := callAttention(attentionReleaseHandler, http.MethodPost, "/api/attention/release", body, nil)
	if again.Code != 200 || !strings.Contains(again.Body.String(), `"outcome":"released"`) || !restored.active {
		t.Fatal("old duplicate release touched new owner")
	}
	bad := strings.Replace(body, `"revision":"1"`, `"revision":"9"`, 1)
	if w := callAttention(attentionReleaseHandler, http.MethodPost, "/api/attention/release", bad, nil); w.Code != 409 {
		t.Fatal("wrong old revision was credited")
	}
	mu.Lock()
	defer mu.Unlock()
	if begins != 2 || recovers != 3 {
		t.Fatalf("effects begin=%d recover=%d; no replay permitted", begins, recovers)
	}
}
func srvURL(r *http.Request) string { return "http://" + r.Host }

func TestCustodyDurabilityFailureAndMalformedImportFenceEffects(t *testing.T) {
	for _, kind := range []string{"unwritable-parent", "malformed"} {
		t.Run(kind, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "custody")
			if kind == "malformed" {
				if err := os.WriteFile(path, []byte(`{"version":1,"room":"other"}`), 0600); err != nil {
					t.Fatal(err)
				}
			} else {
				if err := os.WriteFile(path, []byte("file"), 0600); err != nil {
					t.Fatal(err)
				}
				path = filepath.Join(path, "child")
			}
			a := &attention{}
			a.loadCustody(path, "r0")
			if a.engageOwned(testConversationA, a.challenge(), true, 5, "voice", defaultAttentionTTL, time.Now()) || a.nativeReconcile("invalid", "1", false) || a.nativeManualReconcile("invalid", &glLive{}) || a.reconcileCustody() {
				t.Fatal("failed journal admitted native work")
			}
			if s := a.snapshot(time.Now()); s.Contract != "native_attention_v1" || s.Outcome != "pending" {
				t.Fatal("failed custody claimed clean legacy state")
			}
		})
	}
}

func TestRecoveryReceiptRejectsDesiredOnlyAndWrongOriginalIdentity(t *testing.T) {
	identity := nativeAttentionIdentity{testNativeProcess, "1", "1", "0", "1", "0", "1", "0", 65, 65, false}
	command := nativeRecoveryCommand{nativeAttentionCommand{identity, testConversationA, "recover", 0, 0}, nativeAttentionOrigin{testConversationB, testConversationA}}
	result := nativeRecoveryResult{nativeAttentionResult: nativeAttentionResult{Identity: identity, RequestID: testConversationA, Operation: "1", Action: "recover", Outcome: "protocol_accepted", ResponseCode: 200, CSeq: 7, Current: true}, Origin: command.Origin, State: "restored", Proof: &nativeRecoveryProof{Database: testNativeProcess, LedgerComplete: true, Terminal: &nativeRecoveryTerminal{Identity: identity, RequestID: testConversationA, Operation: "1", Action: "recover", ResponseCode: 200, CSeq: 7}}}
	if !recoveryReceipt(command, identity, "1", result) {
		t.Fatal("exact strict receipt rejected")
	}
	for _, mutate := range []func(*nativeRecoveryResult){
		func(r *nativeRecoveryResult) { r.Outcome = "desired_only" }, func(r *nativeRecoveryResult) { r.ResponseCode = 500 }, func(r *nativeRecoveryResult) { r.CSeq = 0 }, func(r *nativeRecoveryResult) { r.Origin.RequestID = testConversationB }, func(r *nativeRecoveryResult) { r.Identity.DeviceID = "2" }, func(r *nativeRecoveryResult) { r.Identity.Process = testConversationB }, func(r *nativeRecoveryResult) { r.Current = false }, func(r *nativeRecoveryResult) { r.RequestID = testConversationB },
	} {
		bad := result
		mutate(&bad)
		if recoveryReceipt(command, identity, "1", bad) {
			t.Fatalf("invalid receipt credited: %+v", bad)
		}
	}
}

func TestRecoveryNoEffectRequiresExactPersistentProof(t *testing.T) {
	original := nativeAttentionIdentity{testNativeProcess, "1", "1", "0", "1", "0", "1", "0", 65, 65, false}
	fresh := original
	fresh.Process = testConversationB
	command := nativeRecoveryCommand{nativeAttentionCommand{fresh, testConversationA, "recover", 0, 0}, nativeAttentionOrigin{original.Process, testConversationB}}
	result := nativeRecoveryResult{nativeAttentionResult: nativeAttentionResult{Identity: fresh, RequestID: command.RequestID, Operation: "9", Action: "recover", Outcome: "no_effect", Current: true}, Origin: command.Origin, State: "never_admitted", Proof: &nativeRecoveryProof{Database: testNativeProcess, LedgerComplete: true}}
	if !recoveryReceipt(command, original, "1", result) {
		t.Fatal("complete persistent never-admitted proof rejected")
	}
	for _, mutate := range []func(*nativeRecoveryResult){
		func(r *nativeRecoveryResult) { r.State = "unknown" }, func(r *nativeRecoveryResult) { r.Outcome = "desired_only" }, func(r *nativeRecoveryResult) { r.Proof = nil }, func(r *nativeRecoveryResult) { r.ResponseCode = 200 }, func(r *nativeRecoveryResult) { r.Origin.Process = testConversationA },
	} {
		bad := result
		mutate(&bad)
		if recoveryReceipt(command, original, "1", bad) {
			t.Fatalf("unproven absence credited: %+v", bad)
		}
	}
	incomplete := result
	incomplete.Proof = &nativeRecoveryProof{Database: testNativeProcess, LedgerComplete: false}
	if recoveryReceipt(command, original, "1", incomplete) {
		t.Fatal("evicted/incomplete ledger accepted")
	}
	retired := result
	retired.State = "already_retired"
	terminal := nativeRecoveryTerminal{Identity: original, RequestID: testConversationA, Operation: "3", Action: "release", ResponseCode: 200, CSeq: 7}
	terminal.Identity.Lease = "3"
	retired.Proof = &nativeRecoveryProof{Database: testNativeProcess, LedgerComplete: true, Terminal: &terminal}
	if !recoveryReceipt(command, original, "1", retired) {
		t.Fatal("exact durable earlier RTSP terminal rejected")
	}
	terminal.ResponseCode = 500
	if recoveryReceipt(command, original, "1", retired) {
		t.Fatal("failed historical volume proof accepted")
	}
}

func TestCustodySessionWithoutNativeSubmissionAndRetiredImport(t *testing.T) {
	path := filepath.Join(t.TempDir(), "custody.json")
	a := &attention{}
	a.loadCustody(path, "r0")
	old := a.challenge()
	if !a.engageOwned(testConversationA, old, true, 5, "voice", defaultAttentionTTL, time.Now()) {
		t.Fatal("admission failed")
	}
	admitted := a.challenge()
	recovered := &attention{}
	recovered.loadCustody(path, "r0")
	if !recovered.reconcileCustody() {
		t.Fatal("session ACK with no native child did not settle without HTTP")
	}
	if snap, ok := recovered.custodyRelease(testConversationA, admitted); !ok || snap.Outcome != "released" {
		t.Fatal("exact never-submitted session release not returned")
	}
	again := &attention{}
	again.loadCustody(path, "r0")
	if again.custodyBlocked() {
		t.Fatal("durable session tombstone did not survive another restart")
	}
	if _, ok := again.custodyRelease(testConversationA, attentionChallenge{admitted.Process, "9"}); ok {
		t.Fatal("wrong revision adopted retired record")
	}
	// Naked retired child/another-room journal cannot manufacture terminal proof.
	doc := readCustodyTest(t, path)
	origin := nativeAttentionOrigin{testNativeProcess, testConversationB}
	doc.Sessions[0].Outputs[origin.RequestID] = &custodyOutput{Base: "http://unused.invalid", DeviceID: "1", Origin: origin, Original: nativeAttentionIdentity{testNativeProcess, "1", "1", "0", "1", "0", "1", "0", 65, 65, false}, Retired: true}
	data, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	invalid := &attention{}
	invalid.loadCustody(path, "r0")
	if !invalid.custodyBlocked() {
		t.Fatal("naked retirement boolean credited")
	}
	other := &attention{}
	other.loadCustody(path, "r1")
	if !other.custodyBlocked() {
		t.Fatal("another room inherited cleanup authority")
	}
}

func TestCustodyNormalReleaseDurablyRetainsExactOriginalReceipt(t *testing.T) {
	path := filepath.Join(t.TempDir(), "custody.json")
	a := &attention{}
	a.loadCustody(path, "r0")
	if !a.engageOwned(testConversationA, a.challenge(), true, 5, "voice", defaultAttentionTTL, time.Now()) {
		t.Fatal("BEGIN refused")
	}
	old := a.challenge()
	initial := nativeAttentionIdentity{testNativeProcess, "1", "1", "0", "1", "0", "1", "0", 65, 65, false}
	var commands []nativeAttentionCommand
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			json.NewEncoder(w).Encode(nativeRecoveryObservation{nativeAttentionObservation: nativeAttentionObservation{initial, "0"}, Idle: custodyIdle(true)})
			return
		}
		var command nativeAttentionCommand
		if err := json.NewDecoder(r.Body).Decode(&command); err != nil {
			t.Error(err)
			return
		}
		commands = append(commands, command)
		identity := initial
		identity.Lease = "1"
		if command.Action == "begin" {
			identity.Effective = 5
			initial = identity
		} else if command.Action == "release" {
			identity.Effective = 65
			initial.Lease = "0"
		} else {
			t.Error("unexpected action")
		}
		json.NewEncoder(w).Encode(nativeAttentionResult{Identity: identity, RequestID: command.RequestID, Operation: "1", Action: command.Action, Outcome: "protocol_accepted", ResponseCode: 200, CSeq: 7, Current: true})
	}))
	defer srv.Close()
	if !a.nativeReconcile(srv.URL, "1", true) || !a.releaseOwned(testConversationA, old) || !a.nativeReconcile(srv.URL, "1", false) {
		t.Fatal("current exact release failed")
	}
	imported := &attention{}
	imported.loadCustody(path, "r0")
	if imported.custodyBlocked() {
		t.Fatal("normal strict durable release lost its proof at restart")
	}
	if snap, ok := imported.custodyRelease(testConversationA, old); !ok || snap.Outcome != "released" {
		t.Fatal("normal retired session cannot settle old release")
	}
	doc := readCustodyTest(t, path)
	if len(commands) != 2 || len(doc.Sessions[0].Outputs) != 1 {
		t.Fatal("release replayed or original record dropped")
	}
	for _, o := range doc.Sessions[0].Outputs {
		if o.Origin.RequestID != commands[0].RequestID || o.Release == nil || o.Release.RequestID != commands[1].RequestID || o.NoSubmission {
			t.Fatal("UPDATE/RELEASE overwrote original BEGIN custody")
		}
	}
}

func TestCustodyNativeOnlyRestartRetiresCurrentSessionWithoutLeaseAdoption(t *testing.T) {
	a := &attention{}
	a.loadCustody(filepath.Join(t.TempDir(), "custody"), "r0")
	if !a.engageOwned(testConversationA, a.challenge(), true, 5, "voice", defaultAttentionTTL, time.Now()) {
		t.Fatal("begin failed")
	}
	old := a.challenge()
	state := nativeAttentionIdentity{testNativeProcess, "1", "1", "0", "1", "0", "1", "0", 65, 65, false}
	beginCount, recoverCount := 0, 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			if r.URL.Query().Get("process") != "" {
				http.Error(w, "receipt lost with old process", 404)
				return
			}
			json.NewEncoder(w).Encode(nativeRecoveryObservation{nativeAttentionObservation: nativeAttentionObservation{state, "0"}, Idle: custodyIdle(true)})
			return
		}
		var command nativeRecoveryCommand
		if json.NewDecoder(r.Body).Decode(&command) != nil {
			t.Error("invalid command")
			return
		}
		identity := state
		if command.Action == "begin" {
			beginCount++
			identity.Lease = "1"
			state = identity
			json.NewEncoder(w).Encode(nativeAttentionResult{Identity: identity, RequestID: command.RequestID, Operation: "1", Action: "begin", Outcome: "desired_only", Current: true})
			return
		}
		if command.Action != "recover" || command.Origin.Process != testNativeProcess {
			t.Error("old owner replay/adoption")
			http.Error(w, "action", 400)
			return
		}
		recoverCount++
		terminal := nativeRecoveryTerminal{Identity: identity, RequestID: command.RequestID, Operation: "2", Action: "recover", ResponseCode: 200, CSeq: 7}
		json.NewEncoder(w).Encode(nativeRecoveryResult{nativeAttentionResult: nativeAttentionResult{Identity: identity, RequestID: command.RequestID, Operation: "2", Action: "recover", Outcome: "protocol_accepted", ResponseCode: 200, CSeq: 7, Current: true}, Origin: command.Origin, State: "restored", Proof: &nativeRecoveryProof{Database: testNativeProcess, LedgerComplete: true, Terminal: &terminal}})
	}))
	defer srv.Close()
	if !a.nativeReconcile(srv.URL, "1", true) {
		t.Fatal("first native target failed")
	}
	state.Process = testConversationB
	state.Lease = "0" // Supervisor restarted native; manager challenge remains unchanged.
	if !a.releaseOwned(testConversationA, old) {
		t.Fatal("current owner release refused")
	}
	if a.nativeReconcile(srv.URL, "1", false) || !a.custodyBlocked() {
		t.Fatal("native process swap falsely settled old physical obligation")
	}
	if a.engageOwned(testConversationA, old, false, 5, "voice", defaultAttentionTTL, time.Now()) {
		t.Fatal("old heartbeat renewed dead native owner")
	}
	if !a.reconcileCustody() {
		t.Fatal("exact native-only crash recovery failed")
	}
	if snap, ok := a.custodyRelease(testConversationA, old); !ok || snap.Outcome != "released" || snap.Challenge.Process != old.Process {
		t.Fatal("current manager lost old release authority")
	}
	if beginCount != 1 || recoverCount != 1 {
		t.Fatal("old BEGIN replayed after process swap")
	}
	if !a.engageOwned(testConversationB, a.challenge(), true, 5, "voice", defaultAttentionTTL, time.Now()) {
		t.Fatal("fresh wake blocked after native-only recovery")
	}
}

func TestCustodyColdFenceProtectsReleaseRescanAndRoomDeletion(t *testing.T) {
	setupAttentionRoom(t)
	a := attentionFor("r0")
	path := filepath.Join(dataDir, "custody")
	a.loadCustody(path, "r0")
	if !a.engageOwned(testConversationA, a.challenge(), true, 5, "voice", defaultAttentionTTL, time.Now()) {
		t.Fatal("session admission failed")
	}
	// Active original custody also retains its configuration owner until cleanup.
	originalRuntime := mgr.runtime("r0")
	activeDelete := callAttention(roomsItemHandler, http.MethodDelete, "/api/rooms/r0", "", nil)
	if activeDelete.Code != 409 || mgr.runtime("r0") != originalRuntime || roomByID("r0") == nil {
		t.Fatal("active pending custody owner was deleted")
	}
	// ACK-before-first-output crash still owns retirement; no interactive route
	// may deselect native, restart it, or remove the owner while that fence exists.
	replacement := &roomRuntime{room: mgr.runtimes["r0"].room}
	mgr.runtimes["r0"] = replacement
	replacement.att.loadCustody(path, "r0")
	originalRooms := loadRooms()
	for _, tc := range []struct {
		name, method, path string
		handler            http.HandlerFunc
	}{
		{"release", http.MethodPost, "/api/release", speakerReleaseHandler},
		{"rescan", http.MethodPost, "/api/rescan", speakerRescanHandler},
		{"delete", http.MethodDelete, "/api/rooms/r0", roomsItemHandler},
	} {
		w := callAttention(tc.handler, tc.method, tc.path, "", nil)
		if w.Code != http.StatusConflict {
			t.Fatalf("%s bypassed original custody: %d %s", tc.name, w.Code, w.Body.String())
		}
		if mgr.runtime("r0") != replacement || len(loadRooms()) != len(originalRooms) || roomByID("r0") == nil || roomByID("r0").Released {
			t.Fatalf("%s changed cleanup owner/config", tc.name)
		}
	}
	if !replacement.att.reconcileCustody() {
		t.Fatal("no-effect original session did not retire")
	}
	if custodyMutationBlocked(httptest.NewRecorder()) {
		t.Fatal("ordinary interaction remains fenced after exact retirement")
	}
}

func TestCustodyHeldCatalogCannotCommitSelectOrTestAfterNativeReplacement(t *testing.T) {
	for _, tc := range []struct {
		name    string
		handler http.HandlerFunc
		body    string
	}{
		{"select", speakerSelectHandler, `{"room":"r0","name":"Kitchen HomePod"}`},
		{"test", speakerTestHandler, `{"room":"r0"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			setupAttentionRoom(t)
			a := attentionFor("r0")
			a.loadCustody(filepath.Join(dataDir, "custody"), "r0")
			if !a.engageOwned(testConversationA, a.challenge(), true, 5, "voice", defaultAttentionTTL, time.Now()) {
				t.Fatal("begin refused")
			}
			held, release := make(chan struct{}), make(chan struct{})
			var mu sync.Mutex
			state := nativeAttentionIdentity{testNativeProcess, "1", "1", "0", "1", "0", "1", "0", 65, 65, false}
			effects := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodGet && r.URL.Path == "/api/outputs" {
					close(held)
					select {
					case <-release:
					case <-r.Context().Done():
						return
					}
					w.Write([]byte(`{"outputs":[{"id":"1","name":"Kitchen HomePod","type":"AirPlay 2"}]}`))
					return
				}
				mu.Lock()
				defer mu.Unlock()
				if r.Method == http.MethodGet {
					json.NewEncoder(w).Encode(nativeRecoveryObservation{nativeAttentionObservation: nativeAttentionObservation{state, "0"}, Idle: custodyIdle(true)})
					return
				}
				if r.Method == http.MethodPut && strings.HasSuffix(r.URL.Path, "/attention") {
					var command nativeAttentionCommand
					json.NewDecoder(r.Body).Decode(&command)
					state.Lease = "1"
					json.NewEncoder(w).Encode(nativeAttentionResult{Identity: state, RequestID: command.RequestID, Operation: "1", Action: "begin", Outcome: "desired_only", Current: true})
					return
				}
				effects++
				w.Write([]byte(`{}`))
			}))
			oldTransport := http.DefaultTransport
			dialer := &net.Dialer{}
			transport := &http.Transport{Proxy: nil, DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
				if address != server.Listener.Addr().String() && address != "localhost:3689" && address != "localhost:3678" {
					return nil, fmt.Errorf("refuse nonowned target %q", address)
				}
				return dialer.DialContext(ctx, network, server.Listener.Addr().String())
			}}
			http.DefaultTransport = transport
			joined := false
			done := make(chan *httptest.ResponseRecorder, 1)
			t.Cleanup(func() {
				server.CloseClientConnections()
				transport.CloseIdleConnections()
				server.Close()
				if joined {
					http.DefaultTransport = oldTransport
				}
			})
			if !a.nativeReconcile(server.URL, "1", true) {
				t.Fatal("native initial BEGIN failed")
			}
			go func() { done <- callAttention(tc.handler, http.MethodPost, "/api/"+tc.name, tc.body, nil) }()
			select {
			case <-held:
			case <-time.After(2 * time.Second):
				t.Fatal("catalog not held")
			}
			mu.Lock()
			state.Process = testConversationB
			state.Lease = "0"
			mu.Unlock()
			a.detectNativeReplacement(server.URL, "1", a.nativeGrants["1"])
			if !a.custodyBlocked() {
				t.Fatal("actual native replacement did not fence owner")
			}
			close(release)
			var response *httptest.ResponseRecorder
			select {
			case response = <-done:
				joined = true
			case <-time.After(2 * time.Second):
				t.Fatal("handler did not join")
			}
			mu.Lock()
			gotEffects := effects
			mu.Unlock()
			if response.Code != 409 || gotEffects != 0 {
				t.Fatalf("held catalog admitted stale %s: status=%d native/BASE effects=%d", tc.name, response.Code, gotEffects)
			}
		})
	}
}

func custodyIdle(value bool) *bool { return &value }

func TestCustodyLiveUnknownDrainReconnectAndLateCallback(t *testing.T) {
	for _, freshEpoch := range []bool{false, true} {
		t.Run(fmt.Sprint("new-epoch-", freshEpoch), func(t *testing.T) {
			a := &attention{}
			a.loadCustody(filepath.Join(t.TempDir(), "custody"), "r0")
			if !a.engageOwned(testConversationA, a.challenge(), true, 5, "voice", defaultAttentionTTL, time.Now()) {
				t.Fatal("begin refused")
			}
			old := a.challenge()
			state := nativeAttentionIdentity{testNativeProcess, "1", "1", "0", "1", "0", "1", "0", 65, 65, false}
			idle := true
			drainedOperation, drainedRequest := "0", ""
			recoverCount := 0
			var first, second nativeRecoveryCommand
			var staleReads int
			proof := func(command nativeRecoveryCommand, operation, outcome, phase string) nativeRecoveryResult {
				identity := command.Expected
				result := nativeRecoveryResult{nativeAttentionResult: nativeAttentionResult{Identity: identity, RequestID: command.RequestID, Operation: operation, Action: "recover", Outcome: outcome, Current: true}, Origin: command.Origin, State: phase}
				if outcome == "protocol_accepted" {
					result.ResponseCode = 200
					result.CSeq = 17
					result.Proof = &nativeRecoveryProof{Database: testNativeProcess, LedgerComplete: true, Terminal: &nativeRecoveryTerminal{Identity: identity, RequestID: command.RequestID, Operation: operation, Action: "recover", ResponseCode: 200, CSeq: 17}}
				}
				return result
			}
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodGet {
					if requestID := r.URL.Query().Get("request_id"); requestID != "" {
						if requestID != second.RequestID {
							t.Error("queried wrong recover owner")
							http.Error(w, "query", 400)
							return
						}
						staleReads++
						if staleReads == 1 {
							json.NewEncoder(w).Encode(proof(first, "3", "protocol_accepted", "restored"))
							return
						}
						state.Restoring = false
						idle = true
						json.NewEncoder(w).Encode(proof(second, "4", "protocol_accepted", "restored"))
						return
					}
					json.NewEncoder(w).Encode(nativeRecoveryObservation{nativeAttentionObservation: nativeAttentionObservation{state, "2"}, Idle: custodyIdle(idle), DrainedOperation: drainedOperation, DrainedRequestID: drainedRequest})
					return
				}
				var command nativeRecoveryCommand
				if json.NewDecoder(r.Body).Decode(&command) != nil {
					t.Error("invalid command")
					return
				}
				switch command.Action {
				case "begin":
					state.Lease = "1"
					state.Effective = 5
					json.NewEncoder(w).Encode(nativeAttentionResult{Identity: state, RequestID: command.RequestID, Operation: "1", Action: "begin", Outcome: "desired_only", Current: true})
				case "release":
					original := state
					original.Restoring = true
					state.Lease = "0"
					state.Restoring = false
					state.Session = "1"
					drainedOperation = "2"
					drainedRequest = command.RequestID
					json.NewEncoder(w).Encode(nativeAttentionResult{Identity: original, RequestID: command.RequestID, Operation: "2", Action: "release", Outcome: "unknown", Current: false})
				case "recover":
					recoverCount++
					if recoverCount == 1 {
						first = command
						if freshEpoch {
							state.Session = "2"
						}
						drainedOperation = "3"
						drainedRequest = command.RequestID
						state.Base = 78
						state.BaseRevision = "2"
						json.NewEncoder(w).Encode(proof(command, "3", "unknown", "unknown"))
						return
					}
					second = command
					if command.Expected.Base != 78 || command.Cap != 0 || command.TTL != 0 {
						t.Error("fresh recovery borrowed old BASE or caller cap")
					}
					// The exact preceding drained edge must be consumed durably before PUT.
					doc := readCustodyTest(t, a.custody.path)
					for _, o := range doc.Sessions[0].Outputs {
						key := state.Process + ":3:" + first.RequestID
						if !o.ConsumedDrains[key] {
							t.Error("fresh compensation outran durable drain consumption")
						}
					}
					state.Restoring = true
					idle = false
					json.NewEncoder(w).Encode(proof(command, "4", "pending", "pending"))
				default:
					t.Errorf("unexpected replay %s", command.Action)
				}
			}))
			defer srv.Close()
			if !a.nativeReconcile(srv.URL, "1", true) || !a.releaseOwned(testConversationA, old) {
				t.Fatal("initial native custody failed")
			}
			if a.nativeReconcile(srv.URL, "1", false) || !a.custodyBlocked() {
				t.Fatal("normal UNKNOWN drain did not enter same-process original retirement")
			}
			if a.reconcileCustody() || recoverCount != 1 {
				t.Fatal("lost strict ACK falsely retired")
			}
			if a.reconcileCustody() || recoverCount != 2 {
				t.Fatal("fresh epoch/exact drain did not admit one new compensation")
			}
			if a.reconcileCustody() || recoverCount != 2 {
				t.Fatal("old callback credited/replayed fresh operation")
			}
			if !a.reconcileCustody() || recoverCount != 2 {
				t.Fatal("exact fresh strict restore did not retire")
			}
			if snap, ok := a.custodyRelease(testConversationA, old); !ok || snap.Outcome != "released" {
				t.Fatal("old exact release cannot finish")
			}
			if !a.engageOwned(testConversationB, a.challenge(), true, 5, "voice", defaultAttentionTTL, time.Now()) {
				t.Fatal("next wake blocked after strict native drain")
			}
		})
	}
}

func TestCustodyAlreadyAdmittedDirectOwnerDelaysCleanup(t *testing.T) {
	setupAttentionRoom(t)
	a := attentionFor("r0")
	path := filepath.Join(dataDir, "custody")
	a.loadCustody(path, "r0")
	if !a.engageOwned(testConversationA, a.challenge(), true, 5, "voice", defaultAttentionTTL, time.Now()) {
		t.Fatal("admission failed")
	}
	done, admitted := custodyMutationAdmission(httptest.NewRecorder())
	if !admitted {
		t.Fatal("initial direct owner rejected")
	}
	a.mu.Lock()
	a.custody.imported = true
	a.mu.Unlock()
	if a.reconcileCustody() {
		t.Fatal("cleanup overlapped already admitted direct effect")
	}
	if _, ok := custodyMutationAdmission(httptest.NewRecorder()); ok {
		t.Fatal("late direct owner crossed fence")
	}
	done()
	if !a.reconcileCustody() {
		t.Fatal("cleanup did not resume after exact admitted owner joined")
	}
}

func TestCustodyDeleteAdmissionCannotRaceNewBegin(t *testing.T) {
	setupAttentionRoom(t)
	a := attentionFor("r0")
	a.loadCustody(filepath.Join(dataDir, "custody"), "r0")
	done, ok := custodyDeleteAdmission(httptest.NewRecorder(), "r0")
	if !ok {
		t.Fatal("empty retired owner refused delete")
	}
	if a.engageOwned(testConversationA, a.challenge(), true, 5, "voice", defaultAttentionTTL, time.Now()) {
		t.Fatal("new BEGIN crossed owner deletion commit")
	}
	done()
	if !a.engageOwned(testConversationA, a.challenge(), true, 5, "voice", defaultAttentionTTL, time.Now()) {
		t.Fatal("failed delete did not relinquish its owner fence")
	}
}

func TestCustodyLiveFreshIncarnationRetiresOriginalWithoutAdoption(t *testing.T) {
	for _, prior := range []string{"begin", "update", "release"} {
		t.Run(prior, func(t *testing.T) {
			a := &attention{}
			a.loadCustody(filepath.Join(t.TempDir(), "custody"), "r0")
			if !a.engageOwned(testConversationA, a.challenge(), true, 5, "voice", defaultAttentionTTL, time.Now()) {
				t.Fatal("begin refused")
			}
			old := a.challenge()
			state := nativeAttentionIdentity{testNativeProcess, "1", "1", "0", "1", "0", "1", "0", 65, 65, false}
			idle := true
			begins, recovers := 0, 0
			var origin nativeAttentionOrigin
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodGet {
					json.NewEncoder(w).Encode(nativeRecoveryObservation{nativeAttentionObservation: nativeAttentionObservation{state, "0"}, Idle: custodyIdle(idle), DrainedOperation: "0"})
					return
				}
				var command nativeRecoveryCommand
				if err := json.NewDecoder(r.Body).Decode(&command); err != nil {
					t.Error(err)
					return
				}
				switch command.Action {
				case "begin":
					begins++
					origin = nativeAttentionOrigin{command.Expected.Process, command.RequestID}
					state.Lease = "1"
					json.NewEncoder(w).Encode(nativeAttentionResult{Identity: state, RequestID: command.RequestID, Operation: "1", Action: command.Action, Outcome: "protocol_accepted", ResponseCode: 200, CSeq: 7, Current: true})
				case "update":
					json.NewEncoder(w).Encode(nativeAttentionResult{Identity: state, RequestID: command.RequestID, Operation: "2", Action: command.Action, Outcome: "protocol_accepted", ResponseCode: 200, CSeq: 8, Current: true})
				case "release":
					json.NewEncoder(w).Encode(nativeAttentionResult{Identity: state, RequestID: command.RequestID, Operation: "3", Action: command.Action, Outcome: "failed", Current: true})
				case "recover":
					recovers++
					if command.Origin != origin || command.Expected != state || command.Cap != 0 || command.TTL != 0 {
						t.Error("recovery adopted or lost original custody")
					}
					terminal := nativeRecoveryTerminal{Identity: state, RequestID: command.RequestID, Operation: "4", Action: "recover", ResponseCode: 200, CSeq: 9}
					json.NewEncoder(w).Encode(nativeRecoveryResult{nativeAttentionResult: nativeAttentionResult{Identity: state, RequestID: command.RequestID, Operation: "4", Action: "recover", Outcome: "protocol_accepted", ResponseCode: 200, CSeq: 9, Current: true}, Origin: command.Origin, State: "restored", Proof: &nativeRecoveryProof{Database: testNativeProcess, LedgerComplete: true, Terminal: &terminal}})
				default:
					t.Errorf("unexpected action %s", command.Action)
				}
			}))
			defer srv.Close()
			if !a.nativeReconcile(srv.URL, "1", true) {
				t.Fatal("original native begin failed")
			}
			if prior == "update" {
				if !a.engageOwned(testConversationA, old, false, 6, "voice", defaultAttentionTTL, time.Now()) || !a.nativeReconcile(srv.URL, "1", true) {
					t.Fatal("original update failed")
				}
			}
			if !a.releaseOwned(testConversationA, old) {
				t.Fatal("release refused")
			}
			if prior == "release" && a.nativeReconcile(srv.URL, "1", false) {
				t.Fatal("failed release credited")
			}
			g := a.nativeGrants["1"]
			if g.Command.Action != prior {
				t.Fatalf("prior owner = %s", g.Command.Action)
			}
			state.Lease = "0"
			// A stale old-incarnation idle observation alone cannot open recovery.
			a.detectNativeReplacement(srv.URL, "1", g)
			if a.custodyBlocked() {
				t.Fatal("stale same incarnation imported custody")
			}
			state.Incarnation = "2" // Stable device ID, genuinely fresh native incarnation; no old drain edge.
			idle = false
			a.detectNativeReplacement(srv.URL, "1", g)
			if a.custodyBlocked() {
				t.Fatal("non-idle owner imported custody")
			}
			idle = true
			state.Lease = "2"
			a.detectNativeReplacement(srv.URL, "1", g)
			if a.custodyBlocked() {
				t.Fatal("newer lease imported custody")
			}
			state.Lease = "0"
			state.Restoring = true
			a.detectNativeReplacement(srv.URL, "1", g)
			if a.custodyBlocked() {
				t.Fatal("restoring owner imported custody")
			}
			state.Restoring = false
			if a.nativeReconcile(srv.URL, "1", false) || !a.custodyBlocked() || recovers != 0 {
				t.Fatal("fresh incarnation did not import retirement-only original custody")
			}
			if a.engageOwned(testConversationA, old, false, 5, "voice", defaultAttentionTTL, time.Now()) {
				t.Fatal("stale heartbeat adopted fresh incarnation")
			}
			if !a.reconcileCustody() || begins != 1 || recovers != 1 {
				t.Fatal("strict fresh original recovery failed or BEGIN replayed")
			}
			if snap, ok := a.custodyRelease(testConversationA, old); !ok || snap.Outcome != "released" {
				t.Fatal("original obligation not retired")
			}
			if !a.engageOwned(testConversationB, a.challenge(), true, 5, "voice", defaultAttentionTTL, time.Now()) {
				t.Fatal("next wake refused")
			}
			a.detectNativeReplacement(srv.URL, "1", g)
			if a.custodyBlocked() {
				t.Fatal("stale old grant poisoned new wake")
			}
		})
	}
}

// Native replies are inert strict protocol receipts; this proves manager HTTP
// retirement order, not HomePod audio. Both original handoff outputs must retire.
func TestAttentionUpdateRetiredOnlyAfterDurableOriginalOutputs(t *testing.T) {
	setupAttentionRoom(t)
	writeOptions(t, `{"attention_token":"retirement-fixture"}`)
	headers := map[string]string{"X-PodConnect-Token": "retirement-fixture"}
	a := attentionFor("r0")
	path := filepath.Join(dataDir, "custody.json")
	a.loadCustody(path, "r0")
	before := a.challenge()
	body := func(nonce string, expected attentionChallenge, begin bool) string {
		data, err := json.Marshal(map[string]any{"room": "r0", "session": nonce, "expected": expected, "begin": begin, "level": 5, "ttl_ms": 15000})
		if err != nil {
			t.Fatal(err)
		}
		return string(data)
	}
	begin := callAttention(attentionHandler, http.MethodPost, "/api/attention", body(testConversationA, before, true), headers)
	if begin.Code != 200 {
		t.Fatalf("BEGIN: %d %s", begin.Code, begin.Body.String())
	}
	original := a.challenge()
	updateBody := body(testConversationA, original, false)
	var mu sync.Mutex
	states := map[string]nativeAttentionIdentity{}
	receipts := map[string]nativeAttentionResult{}
	completed := map[string]bool{}
	commands := []nativeAttentionCommand{}
	for _, id := range []string{"1", "2"} {
		states[id] = nativeAttentionIdentity{testNativeProcess, id, "1", "0", "1", "0", "1", "0", 65, 65, false}
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
		id := parts[2]
		if r.Method == http.MethodGet {
			if len(parts) == 4 {
				json.NewEncoder(w).Encode(nativeAttentionObservation{states[id], "0"})
			} else {
				receipt := receipts[parts[4]]
				if receipt.Action == "release" && completed[id] {
					receipt.Outcome, receipt.ResponseCode, receipt.CSeq = "protocol_accepted", 200, 7
					state := states[id]
					state.Lease, state.Restoring = "0", false
					states[id] = state
				}
				json.NewEncoder(w).Encode(receipt)
			}
			return
		}
		var command nativeAttentionCommand
		if json.NewDecoder(r.Body).Decode(&command) != nil || command.Expected != states[id] {
			t.Error("unbound native command")
			http.Error(w, "stale", 409)
			return
		}
		commands = append(commands, command)
		identity := command.Expected
		identity.Lease = id
		outcome := "desired_only"
		if command.Action == "release" {
			identity.Restoring = true
			outcome = "pending"
		}
		states[id] = identity
		receipt := nativeAttentionResult{Identity: identity, RequestID: command.RequestID, Operation: fmt.Sprint(len(commands)), Action: command.Action, Outcome: outcome, Current: true}
		receipts[receipt.Operation] = receipt
		json.NewEncoder(w).Encode(receipt)
	}))
	defer srv.Close()
	if !a.nativeTarget(srv.URL, "1") || !a.nativeTarget(srv.URL, "2") {
		t.Fatal("original handoff outputs not admitted")
	}
	a.expire(time.Now().Add(maxAttentionTTL + time.Second))
	pending := callAttention(attentionHandler, http.MethodPost, "/api/attention", updateBody, headers)
	if pending.Code != 409 || pending.Header().Get("Content-Type") == "application/json" {
		t.Fatal("expiry alone claimed durable retirement")
	}
	if a.nativeReconcile(srv.URL, "2", false) {
		t.Fatal("pending restore reported success")
	}
	mu.Lock()
	for id, state := range states {
		if state.Restoring {
			completed[id] = true
		}
	}
	mu.Unlock()
	if a.nativeReconcile(srv.URL, "2", false) {
		t.Fatal("one restored output credited whole session")
	}
	partial := readCustodyTest(t, path)
	retired := 0
	for _, output := range partial.Sessions[0].Outputs {
		if output.Retired {
			retired++
		}
	}
	// Map traversal can start the other pending release before durably retiring
	// the first strict receipt. Exactly one original output is confirmed either
	// in its retained grant or the journal; neither ordering retires the session.
	confirmed := retired
	for _, grant := range a.nativeGrants {
		if grant.Result.Action == "release" && grant.Result.Outcome == "protocol_accepted" {
			confirmed++
		}
	}
	if confirmed != 1 || partial.Sessions[0].Retired {
		t.Fatal("partial restore lost original custody")
	}
	stillPending := callAttention(attentionHandler, http.MethodPost, "/api/attention", updateBody, headers)
	if stillPending.Code != 409 || strings.Contains(stillPending.Body.String(), "lease_retired") {
		t.Fatal("partial restore credited session")
	}
	mu.Lock()
	completed["1"], completed["2"] = true, true
	mu.Unlock()
	if !a.nativeReconcile(srv.URL, "2", false) {
		t.Fatal("all original outputs failed to retire")
	}
	if doc := readCustodyTest(t, path); !doc.Sessions[0].Retired || len(doc.Sessions[0].Outputs) != 2 {
		t.Fatal("missing durable whole-session retirement")
	}
	reply := callAttention(attentionHandler, http.MethodPost, "/api/attention", updateBody, headers)
	var observed map[string]any
	if reply.Code != 409 || reply.Header().Get("Content-Type") != "application/json" || json.Unmarshal(reply.Body.Bytes(), &observed) != nil {
		t.Fatalf("typed refusal: %d %s", reply.Code, reply.Body.String())
	}
	want := map[string]any{"contract": "native_attention_v1", "outcome": "lease_retired", "room": "r0", "session": testConversationA, "expected": map[string]any{"process": original.Process, "revision": original.Revision}}
	wantBytes, _ := json.Marshal(want)
	gotBytes, _ := json.Marshal(observed)
	if string(gotBytes) != string(wantBytes) {
		t.Fatalf("wrong original identity: %s", gotBytes)
	}
	for name, requestBody := range map[string]string{
		"begin":     body(testConversationA, original, true),
		"nonce":     body(testConversationB, original, false),
		"process":   body(testConversationA, attentionChallenge{testConversationB, original.Revision}, false),
		"revision":  body(testConversationA, attentionChallenge{original.Process, "99"}, false),
		"malformed": `{"room":"r0","session":"` + testConversationA + `","expected":{"process":"` + original.Process + `","revision":true}}`,
	} {
		w := callAttention(attentionHandler, http.MethodPost, "/api/attention", requestBody, headers)
		if strings.Contains(w.Body.String(), "lease_retired") || w.Header().Get("Content-Type") == "application/json" {
			t.Fatalf("%s got typed retirement", name)
		}
	}
	if w := callAttention(attentionHandler, http.MethodPost, "/api/attention", updateBody, nil); w.Code != 401 {
		t.Fatal("typed retirement bypassed auth")
	}
	if a.custodyRetired("different-room", testConversationA, original) {
		t.Fatal("different room matched custody")
	}
	fresh := callAttention(attentionHandler, http.MethodPost, "/api/attention", body(testConversationB, a.challenge(), true), headers)
	if fresh.Code != 200 {
		t.Fatal("next admission blocked")
	}
	newOwner := a.snapshot(time.Now())
	duplicate := callAttention(attentionHandler, http.MethodPost, "/api/attention", updateBody, headers)
	if duplicate.Code != 409 || !strings.Contains(duplicate.Body.String(), "lease_retired") || a.challenge() != newOwner.Challenge || !a.active || a.sessionNonce != testConversationB {
		t.Fatal("late old update changed newer owner")
	}
	if export := os.Getenv("PC_RETIRED_UPDATE_RECEIPT"); export != "" {
		receipt := map[string]any{"room": "r0", "session": testConversationA, "expected": original, "begin": json.RawMessage(begin.Body.Bytes()), "pending_status": pending.Code, "pending_body": pending.Body.String(), "partial_status": stillPending.Code, "partial_body": stillPending.Body.String(), "retired_status": reply.Code, "retired_body": json.RawMessage(reply.Body.Bytes()), "next_begin": json.RawMessage(fresh.Body.Bytes())}
		data, err := json.MarshalIndent(receipt, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(export, data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if len(commands) != 4 || commands[0].Action != "begin" || commands[1].Action != "begin" || commands[2].Action != "release" || commands[3].Action != "release" {
		t.Fatal("HTTP refusal replayed/created effects")
	}
}

func TestAttentionUpdateFailedRetirementSaveNeverClaimsRetired(t *testing.T) {
	setupAttentionRoom(t)
	a := attentionFor("r0")
	path := filepath.Join(dataDir, "custody.json")
	a.loadCustody(path, "r0")
	if !a.engageOwned(testConversationA, a.challenge(), true, 5, "voice", maxAttentionTTL, time.Now()) {
		t.Fatal("admission failed")
	}
	original := a.challenge()
	block := filepath.Join(dataDir, "not-directory")
	if err := os.WriteFile(block, []byte("file"), 0600); err != nil {
		t.Fatal(err)
	}
	a.custody.path = filepath.Join(block, "custody.json")
	a.expire(time.Now().Add(maxAttentionTTL + time.Second))
	a.nativeReconcile("", "", false) // The final durable save fails after in-memory Retired.
	if !a.custody.Sessions[0].Retired || a.custody.fault == nil || readCustodyTest(t, path).Sessions[0].Retired {
		t.Fatal("failed persistence boundary not exercised")
	}
	data, _ := json.Marshal(map[string]any{"room": "r0", "session": testConversationA, "expected": original})
	w := callAttention(attentionHandler, http.MethodPost, "/api/attention", string(data), nil)
	if w.Code != 409 || w.Header().Get("Content-Type") == "application/json" || strings.Contains(w.Body.String(), "lease_retired") {
		t.Fatal("failed retirement save was credited")
	}
}
