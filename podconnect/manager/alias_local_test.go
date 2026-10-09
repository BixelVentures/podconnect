package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestBoundAliasMissingMetadataCannotDowngradeToLegacy(t *testing.T) {
	rooms, binding := setupLocalAliasRooms(t)
	var live glLive
	run := live.beginRun(nil)
	phase, ok := live.beginPhase(run)
	if !ok {
		t.Fatal("no phase")
	}
	first, _ := live.beginStatus(phase)
	live.acceptStatus(first, glStatus{SelAlias: 2, AliasBinding: binding})
	missing, _ := live.beginStatus(phase)
	live.acceptStatus(missing, glStatus{SelAlias: 1})
	status, revision := live.routeSnapshot()
	if status.AliasBinding == nil || status.AliasBinding.Ready || status.SelAlias != 2 || admitRoomAliasRoute(rooms[1], status.AliasBinding, &live, status.SelAlias, revision) {
		t.Fatal("missing current metadata downgraded to positional fallback")
	}
	fresh, _ := live.beginStatus(phase)
	live.acceptStatus(fresh, glStatus{SelAlias: 2, AliasBinding: binding})
	status, revision = live.routeSnapshot()
	if !admitRoomAliasRoute(rooms[1], status.AliasBinding, &live, 2, revision) {
		t.Fatal("fresh actual binding did not recover")
	}
	live.applySourceEvent(phase, "selected_alias", map[string]any{"id": 1})
	status, revision = live.routeSnapshot()
	if status.SelAlias != 2 || status.AliasBinding.Ready || admitRoomAliasRoute(rooms[1], status.AliasBinding, &live, 2, revision) {
		t.Fatal("unbound event downgraded current identity")
	}
}

func TestActualRoomSwitchRelayOnlyAcceptsExactLocalEngineContract(t *testing.T) {
	_, binding := setupLocalAliasRooms(t)
	var requestMu sync.Mutex
	requests := 0
	requestCount := func() int { requestMu.Lock(); defer requestMu.Unlock(); return requests }
	engine := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestMu.Lock()
		requests++
		requestMu.Unlock()
		if r.URL.Path != "/player/aliases" || r.Method != http.MethodPost {
			t.Error("relay escaped local alias API")
			w.WriteHeader(500)
			return
		}
		var data aliasSelectionRequest
		if json.NewDecoder(r.Body).Decode(&data) != nil || data.RoomID != binding.RoomID || data.ExpectedRegistry != binding.Registry || data.ExpectedIncarnation != binding.Incarnation {
			t.Error("relay lost identity")
		}
		json.NewEncoder(w).Encode(map[string]any{"accepted_local": true, "id": 2, "binding": binding})
	}))
	defer engine.Close()
	raw, _ := json.Marshal(aliasSelectionRequest{RoomID: binding.RoomID, ExpectedRegistry: binding.Registry, ExpectedIncarnation: binding.Incarnation})
	response := httptest.NewRecorder()
	roomSwitchAt(response, httptest.NewRequest(http.MethodPost, "/api/room-switch", bytes.NewReader(raw)), engine.URL)
	if response.Code != 200 || requestCount() != 1 {
		t.Fatalf("actual local relay failed: %d %d", response.Code, requestCount())
	}
	var reply struct {
		AcceptedLocal bool          `json:"accepted_local"`
		Binding       *aliasBinding `json:"binding"`
	}
	if json.Unmarshal(response.Body.Bytes(), &reply) != nil || !reply.AcceptedLocal || !sameAliasBinding(reply.Binding, binding) {
		t.Fatal("relay invented another acknowledgement")
	}
	obsolete := aliasSelectionRequest{RoomID: "r1", ExpectedRegistry: "old", ExpectedIncarnation: binding.Incarnation}
	raw, _ = json.Marshal(obsolete)
	response = httptest.NewRecorder()
	roomSwitchAt(response, httptest.NewRequest(http.MethodPost, "/api/room-switch", bytes.NewReader(raw)), engine.URL)
	if response.Code != 409 || requestCount() != 1 {
		t.Fatal("stale manager map reached engine")
	}
	oldEngine := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(`{"playback_ready":true}`)) }))
	defer oldEngine.Close()
	response = httptest.NewRecorder()
	roomSwitchAt(response, httptest.NewRequest(http.MethodGet, "/api/room-switch", nil), oldEngine.URL)
	if response.Code != 502 {
		t.Fatal("old engine root masqueraded as alias contract")
	}
}

func setupLocalAliasRooms(t *testing.T) ([]*Room, *aliasBinding) {
	t.Helper()
	oldDir, oldStore, oldMgr := dataDir, store, mgr
	t.Cleanup(func() { dataDir, store, mgr = oldDir, oldStore, oldMgr })
	setupAttentionRoom(t)
	rooms := []*Room{{ID: "r0", Idx: 0, Name: "A", HomepodID: "42", HomepodName: "A"}, {ID: "r1", Idx: 1, Name: "B", HomepodID: "43", HomepodName: "B"}}
	store.mu.Lock()
	err := store.saveLocked(&roomsFile{NextIdx: 2, Rooms: rooms})
	store.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	entries, ok := aliasEntriesForRooms(rooms, []string{"A", "B"})
	if !ok {
		t.Fatal("invalid test registry")
	}
	return rooms, &aliasBinding{Incarnation: "actual-player-A", Registry: aliasRegistryHash(entries), RoomID: "r1", Ready: true}
}

func TestLocalAliasBindingCannotBeMintedByWireEvent(t *testing.T) {
	_, binding := setupLocalAliasRooms(t)
	var live glLive
	run := live.beginRun(nil)
	phase, ok := live.beginPhase(run)
	if !ok {
		t.Fatal("no phase")
	}
	request, ok := live.beginStatus(phase)
	if !ok {
		t.Fatal("no current owner")
	}
	if !live.acceptStatus(request, glStatus{SelAlias: 2, AliasBinding: binding}) {
		t.Fatal("actual status refused")
	}
	_, revision := live.routeSnapshot()
	oldRequest, _ := live.beginStatus(phase)
	other := *binding
	other.Incarnation = "new-player-B"
	other.RoomID = "r0"
	live.applySourceEvent(phase, "selected_alias", map[string]any{"id": 1, "alias_binding": other})
	after, newRevision := live.routeSnapshot()
	if after.SelAlias != 2 || after.AliasBinding.Ready || newRevision == revision || live.admitRoute(2, revision) {
		t.Fatal("unproved event installed or admitted another player")
	}
	live.acceptStatus(oldRequest, glStatus{SelAlias: 2, AliasBinding: binding})
	after, _ = live.routeSnapshot()
	if after.AliasBinding.Ready {
		t.Fatal("held old status crossed newer wire veto")
	}
	fresh, _ := live.beginStatus(phase)
	live.acceptStatus(fresh, glStatus{SelAlias: 1, AliasBinding: &other})
	after, _ = live.routeSnapshot()
	if after.SelAlias != 1 || !sameAliasBinding(after.AliasBinding, &other) {
		t.Fatal("fresh actual status did not recover current identity")
	}
	live.applySourceEvent(phase, "selected_alias", map[string]any{"id": 2, "alias_binding": binding})
	after, _ = live.routeSnapshot()
	if after.SelAlias != 1 || after.AliasBinding.Ready || after.AliasBinding.Incarnation != other.Incarnation {
		t.Fatal("late old callback rolled incarnation back")
	}
	retired := other
	retired.Ready = false
	retired.RoomID = ""
	retiredRequest, _ := live.beginStatus(phase)
	live.acceptStatus(retiredRequest, glStatus{SelAlias: 1, AliasBinding: &retired})
	live.applySourceEvent(phase, "selected_alias", map[string]any{"id": 1, "alias_binding": &other})
	after, _ = live.routeSnapshot()
	if after.AliasBinding.Ready || after.AliasBinding.RoomID != "" {
		t.Fatal("late committed event reactivated retired player status")
	}
	live.retirePhase(phase)
	live.applySourceEvent(phase, "selected_alias", map[string]any{"id": 2, "alias_binding": binding})
	after, _ = live.routeSnapshot()
	if after.SelAlias != 1 {
		t.Fatal("retired source crossed phase")
	}
}

func TestLocalAliasRoomMutationBeforeHeldCatalogAdmission(t *testing.T) {
	rooms, binding := setupLocalAliasRooms(t)
	var live glLive
	live.set(glStatus{SelAlias: 2, AliasBinding: binding})
	held, release, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var once sync.Once
	var mu sync.Mutex
	puts := 0
	var lastOutputs []string
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/outputs" {
			once.Do(func() {
				close(held)
				select {
				case <-release:
				case <-r.Context().Done():
				}
			})
			w.Write([]byte(`{"outputs":[{"id":"42","name":"A","type":"AirPlay"},{"id":"43","name":"B","type":"AirPlay"}]}`))
			return
		}
		if r.URL.Path == "/api/outputs/set" {
			var command struct {
				Outputs []string `json:"outputs"`
			}
			if json.NewDecoder(r.Body).Decode(&command) != nil {
				t.Error("invalid actual output command")
			}
			mu.Lock()
			lastOutputs = command.Outputs
			puts++
			mu.Unlock()
			w.WriteHeader(204)
			return
		}
		http.NotFound(w, r)
	}))
	var releaseOnce sync.Once
	t.Cleanup(func() {
		releaseOnce.Do(func() { close(release) })
		backend.CloseClientConnections()
		backend.Close()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("actual retained route owner not joined")
		}
	})
	result := make(chan [2]bool, 1)
	status, revision := live.routeSnapshot()
	go func() {
		defer close(done)
		accepted, current := routeAliasOutputForIntent(backend.URL, status.SelAlias, &live, revision)
		result <- [2]bool{accepted, current}
	}()
	select {
	case <-held:
	case <-time.After(5 * time.Second):
		t.Fatal("catalog barrier not reached")
	}
	// Alias index 2 now means A; stable room B remains identifiable, but the
	// old registry must be refused before an actual output command is admitted.
	store.mu.Lock()
	err := store.saveLocked(&roomsFile{NextIdx: 2, Rooms: []*Room{rooms[1], rooms[0]}})
	store.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	releaseOnce.Do(func() { close(release) })
	select {
	case got := <-result:
		if got[0] {
			t.Fatal("obsolete room map dispatched")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("retained route did not return")
	}
	mu.Lock()
	count := puts
	mu.Unlock()
	if count != 0 {
		t.Fatal("old room mapping reached output PUT")
	}
	entries, _ := aliasEntriesForRooms([]*Room{rooms[1], rooms[0]}, []string{"B", "A"})
	next := *binding
	next.Registry = aliasRegistryHash(entries)
	live.set(glStatus{SelAlias: 1, AliasBinding: &next})
	status, revision = live.routeSnapshot()
	accepted, current := routeAliasOutputForIntent(backend.URL, status.SelAlias, &live, revision)
	if !accepted || !current {
		t.Fatal("fresh stable B room failed after reorder")
	}
	mu.Lock()
	count = puts
	outputB := len(lastOutputs) == 1 && lastOutputs[0] == "43"
	mu.Unlock()
	if count != 1 || !outputB {
		t.Fatal("fresh route did not own one PUT")
	}
}

func TestLocalAliasRegistryRejectsDuplicateOrMismatchedStableIDs(t *testing.T) {
	for _, rooms := range [][]*Room{{{ID: "same"}, {ID: "same"}}, {{ID: "r0"}}} {
		if _, ok := aliasEntriesForRooms(rooms, []string{"A", "B"}); ok {
			t.Fatal("ambiguous stable mapping accepted")
		}
	}
}

func TestBoundAliasMalformedEventCannotInstallUnfencedRoom(t *testing.T) {
	_, binding := setupLocalAliasRooms(t)
	var live glLive
	run := live.beginRun(nil)
	phase, _ := live.beginPhase(run)
	first, _ := live.beginStatus(phase)
	live.acceptStatus(first, glStatus{SelAlias: 2, AliasBinding: binding})
	before, revision := live.routeSnapshot()
	changed := *binding
	changed.RoomID = "r0"
	for _, data := range []map[string]any{{"alias_binding": changed}, {"id": "2", "alias_binding": changed}, {"id": 1.5, "alias_binding": changed}, {"id": 0, "alias_binding": changed}} {
		live.applySourceEvent(phase, "selected_alias", data)
		after, current := live.routeSnapshot()
		if after.SelAlias != before.SelAlias || !sameAliasBinding(after.AliasBinding, before.AliasBinding) || current != revision || !live.admitRoute(2, revision) {
			t.Fatal("malformed event installed an unfenced room")
		}
	}
}

func TestSameNumericAliasBindingChangeUsesActualRouteTrigger(t *testing.T) {
	rooms, binding := setupLocalAliasRooms(t)
	var live glLive
	run := live.beginRun(nil)
	phase, valid := live.beginPhase(run)
	if !valid {
		t.Fatal("actual phase refused")
	}
	initial, valid := live.beginStatus(phase)
	if !valid || !live.acceptStatus(initial, glStatus{SelAlias: 2, AliasBinding: binding}) {
		t.Fatal("actual initial status refused")
	}
	var mu sync.Mutex
	var outputs []string
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/outputs" {
			w.Write([]byte(`{"outputs":[{"id":"42","name":"A","type":"AirPlay"},{"id":"43","name":"B","type":"AirPlay"}]}`))
			return
		}
		if r.URL.Path == "/api/outputs/set" {
			var command struct {
				Outputs []string `json:"outputs"`
			}
			if json.NewDecoder(r.Body).Decode(&command) != nil || len(command.Outputs) != 1 {
				t.Error("bad actual command")
			}
			mu.Lock()
			outputs = append(outputs, command.Outputs...)
			mu.Unlock()
			w.WriteHeader(204)
			return
		}
		http.NotFound(w, r)
	}))
	defer backend.Close()
	status, revision := live.routeSnapshot()
	if !aliasRouteChanged(2, 0, status.AliasBinding, nil) {
		t.Fatal("initial bridge route not scheduled")
	}
	accepted, current := routeAliasOutputForIntent(backend.URL, 2, &live, revision)
	if !accepted || !current {
		t.Fatal("initial route failed")
	}
	oldRevision := revision
	previous := copyAliasBinding(status.AliasBinding)
	if aliasRouteChanged(2, 2, status.AliasBinding, previous) {
		t.Fatal("unchanged status floods output command")
	}
	store.mu.Lock()
	err := store.saveLocked(&roomsFile{NextIdx: 2, Rooms: []*Room{rooms[1], rooms[0]}})
	store.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	entries, _ := aliasEntriesForRooms([]*Room{rooms[1], rooms[0]}, []string{"B", "A"})
	replacement := *binding
	replacement.Registry = aliasRegistryHash(entries)
	replacement.RoomID = "r0"
	replacement.Incarnation = "next-player"
	request, valid := live.beginStatus(phase)
	if !valid || !live.acceptStatus(request, glStatus{SelAlias: 2, AliasBinding: &replacement}) {
		t.Fatal("actual replacement status refused")
	}
	status, revision = live.routeSnapshot()
	if revision == oldRevision || live.admitRoute(2, oldRevision) || !live.admitRoute(2, revision) {
		t.Fatal("binding-only status failed to fence/admit actual revisions")
	}
	if !aliasRouteChanged(2, 2, status.AliasBinding, previous) {
		t.Fatal("fresh equal-id binding does not schedule bridge route")
	}
	accepted, current = routeAliasOutputForIntent(backend.URL, 2, &live, revision)
	if !accepted || !current {
		t.Fatal("new stable A route failed")
	}
	mu.Lock()
	defer mu.Unlock()
	if len(outputs) != 2 || outputs[0] != "43" || outputs[1] != "42" {
		t.Fatalf("wrong actual output order: %v", outputs)
	}
}

func TestBoundUnknownAttentionExpiryRetainsPendingRestore(t *testing.T) {
	for _, explicit := range []bool{false, true} {
		var att attention
		now := time.Unix(1, 0)
		att.engage(5, 30, "fixture", time.Second, now)
		if explicit {
			att.release()
		}
		att.expire(now.Add(2 * time.Second))
		if att.snapshot(now.Add(2 * time.Second)).Active {
			t.Fatal("unknown binding kept expired lease active")
		}
		att.mu.Lock()
		pending, previous := att.pendingRelease, att.prevLevel
		att.mu.Unlock()
		if !pending || previous != 30 {
			t.Fatal("unknown identity consumed or changed pending restore")
		}
		att.expire(now.Add(3 * time.Second))
		hold, _, released, target := att.tick(now.Add(4 * time.Second))
		if hold || !released || target != 30 {
			t.Fatal("known binding did not process original pending restore")
		}
		if _, _, again, _ := att.tick(now.Add(5 * time.Second)); again {
			t.Fatal("restore processed more than once")
		}
	}
}

func TestGeneratedInvalidAliasMappingIsExplicitlyUnavailable(t *testing.T) {
	rooms, _ := setupLocalAliasRooms(t)
	if err := os.WriteFile(filepath.Join(dataDir, "options.json"), []byte(`{"connect_aliases":["override-only-one"]}`), 0600); err != nil {
		t.Fatal(err)
	}
	primary := *rooms[0]
	primary.ConfigDir = t.TempDir()
	if err := renderGLConfig(&primary); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(primary.ConfigDir, "config.yml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "device_alias_room_ids: []\n") || !strings.Contains(string(raw), "override-only-one") {
		t.Fatal("generated invalid mapping silently fell back to legacy")
	}
}

// Native command admission fences held observations; an already admitted duck
// retains its original lease for compensating restore after route retirement.
// Wire receipts are inert manager fixtures, not AirPlay apply/restore proof.
func TestLocalAliasRetiredDuringNativeObservationCannotDuck(t *testing.T) {
	for _, boundary := range []string{"begin_observation", "update_observation", "begin_submitted"} {
		for _, mutation := range []string{"room_registry", "alias_intent"} {
			t.Run(boundary+"/"+mutation, func(t *testing.T) {
				rooms, binding := setupLocalAliasRooms(t)
				var live glLive
				live.set(glStatus{SelAlias: 2, AliasBinding: binding})
				att := &attention{}
				if !att.engageOwned(testConversationA, att.challenge(), true, 5, "voice", maxAttentionTTL, time.Now()) {
					t.Fatal("attention admission")
				}
				held, release, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
				var once, releaseOnce sync.Once
				var mu sync.Mutex
				nativePuts, routePuts, releases := 0, 0, 0
				armed := false
				identity := nativeAttentionIdentity{testNativeProcess, "43", "1", "0", "1", "0", "1", "0", 65, 65, false}
				backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					mu.Lock()
					hold := armed && r.URL.Path == "/api/outputs/43/attention" && ((boundary == "begin_submitted" && r.Method == http.MethodPut) || (boundary != "begin_submitted" && r.Method == http.MethodGet))
					mu.Unlock()
					if hold {
						once.Do(func() {
							close(held)
							select {
							case <-release:
							case <-r.Context().Done():
							}
						})
					}
					switch r.URL.Path {
					case "/api/outputs":
						w.Write([]byte(`{"outputs":[{"id":"42","name":"A","type":"AirPlay"},{"id":"43","name":"B","type":"AirPlay"}]}`))
					case "/api/outputs/43/attention":
						mu.Lock()
						defer mu.Unlock()
						if r.Method == http.MethodGet {
							json.NewEncoder(w).Encode(nativeAttentionObservation{identity, "0"})
							return
						}
						var command nativeAttentionCommand
						if json.NewDecoder(r.Body).Decode(&command) != nil || command.Expected != identity {
							t.Error("invalid/stale native command")
							http.Error(w, "contract", 409)
							return
						}
						nativePuts++
						identity.Lease, identity.AttentionRevision, identity.Effective = "1", "1", command.Cap
						if command.Action == "update" {
							identity.AttentionRevision = "2"
						}
						if command.Action == "release" {
							releases++
							if identity.Base != 65 {
								t.Error("restore lost original B base")
							}
							identity.AttentionRevision, identity.Effective, identity.Restoring = "3", identity.Base, true
						}
						result := nativeAttentionResult{Identity: identity, RequestID: command.RequestID, Operation: "1", Action: command.Action, Outcome: "desired_only", Current: true}
						if command.Action == "release" {
							result.Operation = "2"
							identity.Lease = "0"
							identity.Restoring = false
						}
						json.NewEncoder(w).Encode(result)
					case "/api/outputs/set":
						mu.Lock()
						routePuts++
						mu.Unlock()
						w.WriteHeader(http.StatusNoContent)
					default:
						http.NotFound(w, r)
					}
				}))
				t.Cleanup(func() {
					releaseOnce.Do(func() { close(release) })
					backend.CloseClientConnections()
					backend.Close()
					select {
					case <-done:
					case <-time.After(5 * time.Second):
						t.Error("route owner not joined")
					}
				})
				if boundary == "update_observation" {
					if !att.nativeTarget(backend.URL, "43") {
						t.Fatal("original native lease not admitted")
					}
					if !att.engageOwned(testConversationA, att.challenge(), false, 5, "voice", maxAttentionTTL, time.Now()) {
						t.Fatal("fresh heartbeat refused")
					}
				}
				mu.Lock()
				before := nativePuts
				armed = true
				mu.Unlock()
				status, revision := live.routeSnapshot()
				result := make(chan [2]bool, 1)
				go func() {
					defer close(done)
					accepted, current := routeAliasOutputForAttentionIntent(backend.URL, status.SelAlias, &live, revision, att)
					result <- [2]bool{accepted, current}
				}()
				select {
				case <-held:
				case <-time.After(5 * time.Second):
					t.Fatal("native barrier not reached")
				}
				if mutation == "room_registry" {
					store.mu.Lock()
					err := store.saveLocked(&roomsFile{NextIdx: 2, Rooms: []*Room{rooms[1], rooms[0]}})
					store.mu.Unlock()
					if err != nil {
						t.Fatal(err)
					}
				} else {
					fresh := *binding
					fresh.RoomID = "r0"
					live.set(glStatus{SelAlias: 1, AliasBinding: &fresh})
				}
				releaseOnce.Do(func() { close(release) })
				select {
				case got := <-result:
					if got[0] {
						t.Fatal("retired route accepted")
					}
				case <-time.After(5 * time.Second):
					t.Fatal("route did not return")
				}
				mu.Lock()
				nativeCount, routeCount := nativePuts-before, routePuts
				mu.Unlock()
				wantNative := 0
				if boundary == "begin_submitted" {
					wantNative = 1
				}
				if nativeCount != wantNative || routeCount != 0 {
					t.Fatalf("retired route issued native duck=%d output selection=%d", nativeCount, routeCount)
				}
				att.mu.Lock()
				grants := len(att.nativeGrants)
				att.mu.Unlock()
				if boundary == "begin_observation" {
					if grants != 0 {
						t.Fatal("retired route acquired native lease custody")
					}
					return
				}
				if grants != 1 {
					t.Fatal("retirement lost original admitted lease custody")
				}
				if !att.nativeReconcile(backend.URL, "", false) {
					t.Fatal("compensating original lease restore did not settle")
				}
				mu.Lock()
				releaseCount, effective := releases, identity.Effective
				mu.Unlock()
				att.mu.Lock()
				remaining := len(att.nativeGrants)
				att.mu.Unlock()
				if releaseCount != 1 || effective != 65 || remaining != 0 {
					t.Fatalf("restore custody: releases=%d effective=%d grants=%d", releaseCount, effective, remaining)
				}
				if !att.nativeReconcile(backend.URL, "", false) {
					t.Fatal("settled cleanup refused")
				}
				mu.Lock()
				finalPuts := nativePuts
				mu.Unlock()
				if finalPuts != before+wantNative+1 {
					t.Fatal("settled cleanup repeated a native write")
				}
			})
		}
	}
}
