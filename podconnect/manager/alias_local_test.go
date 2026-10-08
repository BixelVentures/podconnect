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
