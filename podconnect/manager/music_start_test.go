package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

type startTransport func(*http.Request) (*http.Response, error)

func (f startTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func startFixture(t *testing.T, output http.Handler) (*Room, *roomRuntime, []*Room, string, *httptest.Server) {
	t.Helper()
	oldDir, oldStore := dataDir, store
	dataDir = t.TempDir()
	store = newRoomStore()
	t.Cleanup(func() { dataDir, store = oldDir, oldStore })
	room := &Room{ID: "r0", Idx: 0, Name: "Kitchen", HomepodID: "native-output-42", HomepodName: "Kitchen"}
	room.fill()
	store.mu.Lock()
	err := store.saveLocked(&roomsFile{NextIdx: 1, Rooms: []*Room{room}})
	store.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	id := "a123456789012345678901234567890123456789"
	if err := os.MkdirAll(room.ConfigDir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(room.ConfigDir, "device_id"), []byte(id), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(room.ConfigDir, "config.yml"), []byte(fmt.Sprintf("device_id: %q\ninitial_volume: %d\n", id, startVolumePercent)), 0600); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(output)
	t.Cleanup(server.Close)
	room.OwnTone = server.URL
	rt := &roomRuntime{room: room}
	run := rt.live.beginRun(nil)
	phase, ok := rt.live.beginPhase(run)
	if !ok {
		t.Fatal("source unavailable")
	}
	request, ok := rt.live.beginStatus(phase)
	if !ok {
		t.Fatal("status unavailable")
	}
	entries, ok := aliasEntriesForRooms([]*Room{room}, []string{room.Name})
	if !ok {
		t.Fatal("registry unavailable")
	}
	binding := &aliasBinding{Incarnation: "player-a", Registry: aliasRegistryHash(entries), RoomID: room.ID, Ready: true}
	if !rt.live.acceptStatus(request, glStatus{SelAlias: 1, AliasBinding: binding}) {
		t.Fatal("status rejected")
	}
	return room, rt, []*Room{room}, id, server
}

func TestStartProofRequiresExactEngineRoomOutputAndCurrentSource(t *testing.T) {
	var mu sync.Mutex
	held := make(chan struct{})
	release := make(chan struct{})
	block := false
	output := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		shouldBlock := block
		mu.Unlock()
		if shouldBlock {
			close(held)
			<-release
		}
		fmt.Fprint(w, `{"outputs":[{"id":"native-output-42","type":"AirPlay 2","selected":true,"volume":35,"needs_auth_key":false}]}`)
	})
	room, rt, rooms, id, _ := startFixture(t, output)
	claim, ok := startClaimFor(room, rt, rooms, "r0", id)
	if !ok || claim.Proof.EngineDeviceID != id || claim.Proof.HomepodID != room.HomepodID || claim.Proof.StartVolumePercent != startVolumePercent {
		t.Fatalf("exact native proof missing: %+v %v", claim.Proof, ok)
	}
	if _, ok := startClaimFor(room, rt, rooms, "r0", "b"+id[1:]); ok {
		t.Fatal("wrong Spotify device joined")
	}
	if _, ok := startClaimFor(room, rt, rooms, "r1", id); ok {
		t.Fatal("wrong room joined")
	}
	mu.Lock()
	block = true
	mu.Unlock()
	result := make(chan bool, 1)
	go func() { _, valid := startClaimFor(room, rt, rooms, "r0", id); result <- valid }()
	<-held
	rt.live.set(glStatus{SelAlias: 2, AliasBinding: &aliasBinding{Incarnation: "player-b", Registry: "different", RoomID: "r1", Ready: true}})
	close(release)
	if <-result {
		t.Fatal("held output read minted proof after source replacement")
	}
}

func TestStartVolumeNativeReadbackBeforeSendAndUnknownFailClosed(t *testing.T) {
	volume := 90
	writes := 0
	output := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/player/volume":
			if r.URL.Query().Get("output_id") == "unknown-output" {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			if r.Method != http.MethodPut || r.URL.Query().Get("output_id") != "native-output-42" || r.URL.Query().Get("volume") != "35" {
				t.Error("non-exact native volume write")
			}
			writes++
			volume = 35
			w.WriteHeader(http.StatusNoContent)
		case "/api/outputs":
			fmt.Fprintf(w, `{"outputs":[{"id":"native-output-42","type":"AirPlay 2","selected":true,"volume":%d,"needs_auth_key":false}]}`, volume)
		default:
			t.Error("unexpected native path")
		}
	})
	room, _, _, _, _ := startFixture(t, output)
	if !checkedStartVolume(room.OwnTone, room.HomepodID) || writes != 1 || volume != startVolumePercent {
		t.Fatal("native start level was not written and read back before caller could send")
	}
	if checkedStartVolume(room.OwnTone, "unknown-output") {
		t.Fatal("unknown output was treated as ready")
	}
}

func TestNativeReleasedOutputPrepareAndOneUseCommit(t *testing.T) {
	selected, volume := false, 90
	var effects []string
	output := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/outputs":
			fmt.Fprintf(w, `{"outputs":[{"id":"native-output-42","type":"AirPlay 2","selected":%t,"volume":%d,"needs_auth_key":false}]}`, selected, volume)
		case "/api/outputs/set":
			var request struct {
				Outputs []string `json:"outputs"`
			}
			if json.NewDecoder(r.Body).Decode(&request) != nil || len(request.Outputs) != 1 || request.Outputs[0] != "native-output-42" {
				t.Error("prepare selected an unbound output")
			}
			selected = true
			effects = append(effects, "select")
			w.WriteHeader(http.StatusNoContent)
		case "/api/player/volume":
			if r.URL.Query().Get("output_id") != "native-output-42" || r.URL.Query().Get("volume") != "35" {
				t.Error("prepare did not prime the exact shared start")
			}
			volume = 35
			effects = append(effects, "volume")
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Error("unexpected native endpoint")
		}
	})
	room, rt, _, id, server := startFixture(t, output)
	oldMgr, oldTransport := mgr, http.DefaultTransport
	mgr = newRoomManager(store)
	mgr.runtimes[room.ID] = rt
	destination, _ := url.Parse(server.URL)
	http.DefaultTransport = startTransport(func(r *http.Request) (*http.Response, error) {
		if r.URL.Host == "localhost:3689" {
			copy := r.Clone(r.Context())
			urlCopy := *r.URL
			urlCopy.Scheme, urlCopy.Host = destination.Scheme, destination.Host
			copy.URL = &urlCopy
			return oldTransport.RoundTrip(copy)
		}
		return oldTransport.RoundTrip(r)
	})
	startLeases.Lock()
	oldLease := startLeases.Current
	startLeases.Current = nil
	startLeases.Unlock()
	t.Cleanup(func() {
		mgr, http.DefaultTransport = oldMgr, oldTransport
		startLeases.Lock()
		startLeases.Current = oldLease
		startLeases.Unlock()
	})
	if err := os.MkdirAll(filepath.Dir(releasedPath(room)), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(releasedPath(room), []byte("1"), 0600); err != nil {
		t.Fatal(err)
	}
	identity := httptest.NewRecorder()
	startIdentityRequest(identity, httptest.NewRequest(http.MethodGet, "/api/start-volume/identity", nil))
	if identity.Code != http.StatusOK || !bytes.Contains(identity.Body.Bytes(), []byte(id)) {
		t.Fatalf("supervised engine identity unavailable: %d %s", identity.Code, identity.Body.String())
	}
	query := url.Values{"room_id": {room.ID}, "target_id": {id}}
	read := httptest.NewRecorder()
	startRequest(read, httptest.NewRequest(http.MethodGet, "/api/start-volume?"+query.Encode(), nil))
	var proof startProof
	if read.Code != http.StatusOK || json.Unmarshal(read.Body.Bytes(), &proof) != nil || proof.OutputSelected || proof.Mode != "fresh_start" {
		t.Fatalf("released configured output was not offered for safe preparation: %d %s", read.Code, read.Body.String())
	}
	body, _ := json.Marshal(map[string]any{"room_id": room.ID, "target_id": id, "policy_revision": proof.PolicyRevision, "alias_incarnation": proof.AliasBinding.Incarnation})
	prepare := httptest.NewRecorder()
	startRequest(prepare, httptest.NewRequest(http.MethodPost, "/api/start-volume", bytes.NewReader(body)))
	var lease struct {
		LeaseID string     `json:"lease_id"`
		Proof   startProof `json:"proof"`
	}
	if prepare.Code != http.StatusOK || json.Unmarshal(prepare.Body.Bytes(), &lease) != nil || len(lease.LeaseID) != 32 || !lease.Proof.OutputSelected || volume != startVolumePercent || len(effects) != 3 || effects[0] != "volume" || effects[1] != "select" || effects[2] != "volume" {
		t.Fatalf("released output was not selected and primed before lease: %d %s %v", prepare.Code, prepare.Body.String(), effects)
	}
	commitBody, _ := json.Marshal(map[string]any{"lease_id": lease.LeaseID})
	first := httptest.NewRecorder()
	commitStartRequest(first, httptest.NewRequest(http.MethodPost, "/api/start-volume/commit", bytes.NewReader(commitBody)))
	second := httptest.NewRecorder()
	commitStartRequest(second, httptest.NewRequest(http.MethodPost, "/api/start-volume/commit", bytes.NewReader(commitBody)))
	if first.Code != http.StatusOK || second.Code != http.StatusConflict {
		t.Fatalf("native lease was not one-use: %d then %d", first.Code, second.Code)
	}
	newLeaseResponse := httptest.NewRecorder()
	startRequest(newLeaseResponse, httptest.NewRequest(http.MethodPost, "/api/start-volume", bytes.NewReader(body)))
	var newer struct {
		LeaseID string `json:"lease_id"`
	}
	if newLeaseResponse.Code != http.StatusOK || json.Unmarshal(newLeaseResponse.Body.Bytes(), &newer) != nil {
		t.Fatal("fresh lease fixture unavailable")
	}
	tooLow, _ := json.Marshal(map[string]any{"lease_id": newer.LeaseID, "max_volume": 30})
	tooLoud := httptest.NewRecorder()
	commitStartRequest(tooLoud, httptest.NewRequest(http.MethodPost, "/api/start-volume/commit", bytes.NewReader(tooLow)))
	if tooLoud.Code != http.StatusConflict {
		t.Fatal("native output above approved maximum retained provider permission")
	}
	newLeaseResponse = httptest.NewRecorder()
	startRequest(newLeaseResponse, httptest.NewRequest(http.MethodPost, "/api/start-volume", bytes.NewReader(body)))
	if newLeaseResponse.Code != http.StatusOK || json.Unmarshal(newLeaseResponse.Body.Bytes(), &newer) != nil {
		t.Fatal("low-drift lease fixture unavailable")
	}
	volume = 0 // Change the native output after exact 35 preparation, before commit.
	lowDrift, _ := json.Marshal(map[string]any{"lease_id": newer.LeaseID})
	tooQuiet := httptest.NewRecorder()
	commitStartRequest(tooQuiet, httptest.NewRequest(http.MethodPost, "/api/start-volume/commit", bytes.NewReader(lowDrift)))
	if tooQuiet.Code != http.StatusConflict {
		t.Fatal("lowered native output retained fixed-start provider permission")
	}
	newLeaseResponse = httptest.NewRecorder()
	startRequest(newLeaseResponse, httptest.NewRequest(http.MethodPost, "/api/start-volume", bytes.NewReader(body)))
	if newLeaseResponse.Code != http.StatusOK || json.Unmarshal(newLeaseResponse.Body.Bytes(), &newer) != nil {
		t.Fatal("replacement lease fixture unavailable")
	}
	rt.live.set(glStatus{SelAlias: 2, AliasBinding: &aliasBinding{Incarnation: "replacement", Registry: "different", RoomID: "r1", Ready: true}})
	staleBody, _ := json.Marshal(map[string]any{"lease_id": newer.LeaseID})
	stale := httptest.NewRecorder()
	commitStartRequest(stale, httptest.NewRequest(http.MethodPost, "/api/start-volume/commit", bytes.NewReader(staleBody)))
	if stale.Code != http.StatusConflict {
		t.Fatal("source replacement retained a pre-send lease")
	}
}

func TestIdleDifferentRoomPrimesBeforeAliasSwitchAndProviderLease(t *testing.T) {
	selectedID := "native-output-42"
	secondVolume := 90
	var effects []string
	output := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/outputs":
			fmt.Fprintf(w, `{"outputs":[{"id":"native-output-42","type":"AirPlay 2","selected":%t,"volume":35},{"id":"native-output-43","type":"AirPlay 2","selected":%t,"volume":%d}]}`,
				selectedID == "native-output-42", selectedID == "native-output-43", secondVolume)
		case "/api/player/volume":
			if r.URL.Query().Get("output_id") != "native-output-43" || r.URL.Query().Get("volume") != "35" {
				t.Error("future output was not primed exactly")
			}
			secondVolume = 35
			effects = append(effects, "prime")
			w.WriteHeader(http.StatusNoContent)
		case "/api/outputs/set":
			selectedID = "native-output-43"
			effects = append(effects, "select")
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Error("unexpected output path")
		}
	})
	primary, rt, _, id, outputServer := startFixture(t, output)
	second := &Room{ID: "r1", Idx: 1, Name: "Office", HomepodID: "native-output-43", HomepodName: "Office"}
	second.fill()
	store.mu.Lock()
	err := store.saveLocked(&roomsFile{NextIdx: 2, Rooms: []*Room{primary, second}})
	store.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	entries, _ := aliasEntriesForRooms([]*Room{primary, second}, []string{"Kitchen", "Office"})
	binding := &aliasBinding{Incarnation: "player-a", Registry: aliasRegistryHash(entries), RoomID: "r0", Ready: true}
	rt.live.set(glStatus{SelAlias: 1, AliasBinding: binding})
	engine := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/player/aliases" || r.Method != http.MethodPost || secondVolume != startVolumePercent {
			t.Error("alias switch preceded exact future-output prime")
		}
		var request aliasSelectionRequest
		if json.NewDecoder(r.Body).Decode(&request) != nil || request.RoomID != "r1" || request.ExpectedIncarnation != binding.Incarnation || request.ExpectedRegistry != binding.Registry {
			t.Error("idle switch lost source-bound request")
		}
		effects = append(effects, "alias")
		newBinding := *binding
		newBinding.RoomID = "r1"
		rt.live.set(glStatus{SelAlias: 2, AliasBinding: &newBinding})
		json.NewEncoder(w).Encode(map[string]any{"accepted_local": true, "binding": newBinding})
	}))
	defer engine.Close()
	primary.Librespot = engine.URL
	oldMgr, oldTransport := mgr, http.DefaultTransport
	mgr = newRoomManager(store)
	mgr.runtimes[primary.ID] = rt
	destination, _ := url.Parse(outputServer.URL)
	http.DefaultTransport = startTransport(func(r *http.Request) (*http.Response, error) {
		if r.URL.Host == "localhost:3689" {
			copy := r.Clone(r.Context())
			urlCopy := *r.URL
			urlCopy.Scheme, urlCopy.Host = destination.Scheme, destination.Host
			copy.URL = &urlCopy
			return oldTransport.RoundTrip(copy)
		}
		return oldTransport.RoundTrip(r)
	})
	t.Cleanup(func() { mgr, http.DefaultTransport = oldMgr, oldTransport })
	query := url.Values{"room_id": {"r1"}, "target_id": {id}}
	rt.live.set(glStatus{Active: true, SelAlias: 1, AliasBinding: binding})
	active := httptest.NewRecorder()
	startRequest(active, httptest.NewRequest(http.MethodGet, "/api/start-volume?"+query.Encode(), nil))
	if active.Code != http.StatusConflict {
		t.Fatal("active different-room move was offered without a safe barrier")
	}
	rt.live.set(glStatus{SelAlias: 1, AliasBinding: binding})
	read := httptest.NewRecorder()
	startRequest(read, httptest.NewRequest(http.MethodGet, "/api/start-volume?"+query.Encode(), nil))
	var proof startProof
	if read.Code != http.StatusOK || json.Unmarshal(read.Body.Bytes(), &proof) != nil || proof.AliasBinding.RoomID != "r0" || proof.OutputSelected {
		t.Fatalf("idle different room was not source-offered: %d %s", read.Code, read.Body.String())
	}
	body, _ := json.Marshal(map[string]any{"room_id": "r1", "target_id": id, "policy_revision": proof.PolicyRevision, "alias_incarnation": proof.AliasBinding.Incarnation})
	prepare := httptest.NewRecorder()
	startRequest(prepare, httptest.NewRequest(http.MethodPost, "/api/start-volume", bytes.NewReader(body)))
	if prepare.Code != http.StatusOK || len(effects) != 5 || effects[0] != "prime" || effects[1] != "alias" || effects[2] != "prime" || effects[3] != "select" || effects[4] != "prime" {
		t.Fatalf("idle transfer did not prime before route and again before lease: %d %s %v", prepare.Code, prepare.Body.String(), effects)
	}
}

func TestReleasedLocalResumePrimesExactOutputBeforeEngineCommand(t *testing.T) {
	selected, volume := false, 90
	var room *Room
	outputReads, driftAtFinal := 0, false
	engineStatus := http.StatusOK
	var effects []string
	output := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/outputs":
			outputReads++
			reported := volume
			if driftAtFinal && outputReads >= 5 {
				reported = 0
			}
			fmt.Fprintf(w, `{"outputs":[{"id":"native-output-42","type":"AirPlay 2","selected":%t,"volume":%d}]}`, selected, reported)
		case "/api/player/volume":
			if r.URL.Query().Get("output_id") != "native-output-42" || r.URL.Query().Get("volume") != "35" {
				t.Error("resume used an unbound output or level")
			}
			volume = startVolumePercent
			effects = append(effects, "prime")
			w.WriteHeader(http.StatusNoContent)
		case "/api/outputs/set":
			selected = true
			effects = append(effects, "select")
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Errorf("unexpected output request: %s", r.URL.Path)
		}
	})
	fixtureRoom, rt, _, _, outputServer := startFixture(t, output)
	room = fixtureRoom
	status, _ := rt.live.transportSnapshot()
	status.Active, status.Paused = true, true
	rt.live.set(status)
	engine := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/player/resume" || !selected || volume != startVolumePercent {
			t.Error("engine resumed before exact output was safe")
		}
		effects = append(effects, "resume")
		w.WriteHeader(engineStatus)
	}))
	defer engine.Close()
	room.Librespot = engine.URL
	oldMgr, oldTransport := mgr, http.DefaultTransport
	mgr = newRoomManager(store)
	mgr.runtimes[room.ID] = rt
	destination, _ := url.Parse(outputServer.URL)
	http.DefaultTransport = startTransport(func(r *http.Request) (*http.Response, error) {
		if r.URL.Host == "localhost:3689" {
			copy := r.Clone(r.Context())
			urlCopy := *r.URL
			urlCopy.Scheme, urlCopy.Host = destination.Scheme, destination.Host
			copy.URL = &urlCopy
			return oldTransport.RoundTrip(copy)
		}
		return oldTransport.RoundTrip(r)
	})
	t.Cleanup(func() { mgr, http.DefaultTransport = oldMgr, oldTransport })
	if err := os.MkdirAll(filepath.Dir(releasedPath(room)), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(releasedPath(room), []byte("1"), 0600); err != nil {
		t.Fatal(err)
	}
	reply := httptest.NewRecorder()
	transportRooms(reply, []*Room{room}, "resume")
	if reply.Code != http.StatusOK || !fileExists(releasedPath(room)) ||
		len(effects) != 4 || effects[0] != "prime" || effects[1] != "select" || effects[2] != "prime" || effects[3] != "resume" {
		t.Fatalf("released resume missed native barrier: HTTP %d effects=%v", reply.Code, effects)
	}
	for attempt := 0; attempt < 2; attempt++ {
		selected, volume, effects = false, 90, nil
		outputReads, driftAtFinal = 0, true
		reply = httptest.NewRecorder()
		transportRooms(reply, []*Room{room}, "resume")
		if reply.Code != http.StatusBadGateway || !fileExists(releasedPath(room)) {
			t.Fatalf("low drift/retry lost release barrier: attempt=%d HTTP %d reads=%d effects=%v", attempt, reply.Code, outputReads, effects)
		}
		for _, effect := range effects {
			if effect == "resume" {
				t.Fatal("engine resumed after native output drifted to zero")
			}
		}
	}
	selected, volume, effects = false, 90, nil
	outputReads, driftAtFinal = 0, false
	engineStatus = http.StatusServiceUnavailable
	reply = httptest.NewRecorder()
	transportRooms(reply, []*Room{room}, "resume")
	if reply.Code != http.StatusBadGateway || !fileExists(releasedPath(room)) {
		t.Fatal("failed engine resume lost release barrier")
	}
	selected, volume, effects = false, 90, nil
	outputReads, driftAtFinal = 0, true
	engineStatus = http.StatusOK
	reply = httptest.NewRecorder()
	transportRooms(reply, []*Room{room}, "resume")
	if reply.Code != http.StatusBadGateway || !fileExists(releasedPath(room)) {
		t.Fatal("retry after unknown engine outcome bypassed release barrier")
	}
	for _, effect := range effects {
		if effect == "resume" {
			t.Fatal("retry after engine failure sent through native low-drift guard")
		}
	}
}

func TestBoundAliasRoutePrimesOutputBeforeSelection(t *testing.T) {
	_, binding := setupLocalAliasRooms(t)
	var live glLive
	run := live.beginRun(nil)
	phase, ok := live.beginPhase(run)
	if !ok {
		t.Fatal("source phase unavailable")
	}
	request, ok := live.beginStatus(phase)
	if !ok || !live.acceptStatus(request, glStatus{Active: true, SelAlias: 2, AliasBinding: binding}) {
		t.Fatal("source status unavailable")
	}
	volume := 90
	selected := false
	denyPrime := false
	var effects []string
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/outputs":
			fmt.Fprintf(w, `{"outputs":[{"id":"42","name":"A","type":"AirPlay","selected":%t,"volume":35},{"id":"43","name":"B","type":"AirPlay","selected":%t,"volume":%d}]}`, !selected, selected, volume)
		case "/api/player/volume":
			if r.Method != http.MethodPut || r.URL.Query().Get("output_id") != "43" || r.URL.Query().Get("volume") != "35" {
				t.Error("route primed wrong output")
			}
			if denyPrime {
				w.WriteHeader(http.StatusConflict)
				return
			}
			volume = startVolumePercent
			effects = append(effects, "prime")
			w.WriteHeader(http.StatusNoContent)
		case "/api/outputs/set":
			if volume != startVolumePercent {
				t.Error("route selected before target volume")
			}
			selected = true
			effects = append(effects, "select")
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Errorf("unexpected native route endpoint %s", r.URL.Path)
		}
	}))
	defer backend.Close()
	status, revision := live.routeSnapshot()
	accepted, current := routeAliasOutputForAttentionIntent(backend.URL, status.SelAlias, &live, revision, nil, true)
	if !accepted || !current || len(effects) != 3 || effects[0] != "prime" || effects[1] != "select" || effects[2] != "prime" {
		t.Fatalf("alias route bypassed start barrier: %v current=%v effects=%v", accepted, current, effects)
	}
	volume = 90
	denyPrime = true
	effects = nil
	accepted, current = routeAliasOutputForAttentionIntent(backend.URL, status.SelAlias, &live, revision, nil, true)
	if accepted || !current || len(effects) != 0 {
		t.Fatalf("failed prime selected a loud output: %v current=%v effects=%v", accepted, current, effects)
	}
}
