package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Intercepts native HTTP only; no listener, Spotify or physical output is used.
type releasedStartFixture struct {
	room       *Room
	rt         *roomRuntime
	id         string
	selected   bool
	volume     int
	effects    []string
	onRead     func()
	onPrime    func()
	failPrime  bool
	failSelect bool
}

func releasedURIFixture(t *testing.T) *releasedStartFixture {
	t.Helper()
	oldDir, oldStore, oldMgr, oldTransport := dataDir, store, mgr, http.DefaultTransport
	dataDir = t.TempDir()
	store = newRoomStore()
	startLeases.Lock()
	oldLease := startLeases.Current
	startLeases.Current = nil
	startLeases.Unlock()
	t.Cleanup(func() {
		dataDir, store, mgr, http.DefaultTransport = oldDir, oldStore, oldMgr, oldTransport
		startLeases.Lock()
		startLeases.Current = oldLease
		startLeases.Unlock()
	})
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
	for name, content := range map[string]string{"device_id": id, "config.yml": fmt.Sprintf("device_id: %q\ninitial_volume: %d\n", id, startVolumePercent)} {
		if err := os.WriteFile(filepath.Join(room.ConfigDir, name), []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	rt := &roomRuntime{room: room}
	run := rt.live.beginRun(nil)
	phase, ok := rt.live.beginPhase(run)
	if !ok {
		t.Fatal("source unavailable")
	}
	entries, ok := aliasEntriesForRooms([]*Room{room}, []string{room.Name})
	if !ok {
		t.Fatal("registry unavailable")
	}
	binding := &aliasBinding{Incarnation: "player-a", Registry: aliasRegistryHash(entries), RoomID: room.ID, Ready: true}
	request, ok := rt.live.beginStatus(phase)
	if !ok || !rt.live.acceptStatus(request, glStatus{Active: true, Paused: true, HasVol: true, VolPct: 64, SelAlias: 1, AliasBinding: binding}) {
		t.Fatal("status unavailable")
	}
	mgr = newRoomManager(store)
	mgr.runtimes[room.ID] = rt
	f := &releasedStartFixture{room: room, rt: rt, id: id, volume: 90}
	if err := os.MkdirAll(filepath.Dir(releasedPath(room)), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(releasedPath(room), []byte("1"), 0600); err != nil {
		t.Fatal(err)
	}
	http.DefaultTransport = startTransport(func(req *http.Request) (*http.Response, error) {
		code := http.StatusOK
		body := ""
		switch {
		case req.Method == "GET" && req.URL.Path == "/api/outputs":
			if f.onRead != nil {
				callback := f.onRead
				f.onRead = nil
				callback()
			}
			body = fmt.Sprintf(`{"outputs":[{"id":"native-output-42","type":"AirPlay 2","selected":%t,"volume":%d,"needs_auth_key":false}]}`, f.selected, f.volume)
		case req.Method == "PUT" && req.URL.Path == "/api/player/volume":
			if req.URL.Query().Get("output_id") != room.HomepodID || req.URL.Query().Get("volume") != fmt.Sprint(startVolumePercent) {
				t.Fatal("unbound output/volume")
			}
			f.effects = append(f.effects, "prime")
			if f.failPrime {
				code = 503
			} else {
				f.volume = startVolumePercent
				if f.onPrime != nil {
					callback := f.onPrime
					f.onPrime = nil
					callback()
				}
			}
		case req.Method == "PUT" && req.URL.Path == "/api/outputs/set":
			var b struct {
				Outputs []string `json:"outputs"`
			}
			if json.NewDecoder(req.Body).Decode(&b) != nil || len(b.Outputs) != 1 || b.Outputs[0] != room.HomepodID {
				t.Fatal("unbound select")
			}
			if f.volume != startVolumePercent {
				t.Fatal("selected before safe pre-prime")
			}
			f.effects = append(f.effects, "select")
			if f.failSelect {
				code = 503
			} else {
				f.selected = true
			}
		default:
			t.Fatalf("unexpected native/provider request %s %s", req.Method, req.URL.Path)
		}
		return &http.Response{StatusCode: code, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)), Request: req}, nil
	})
	return f
}
func (f *releasedStartFixture) get() *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	startRequest(w, httptest.NewRequest("GET", "/api/start-volume?room_id=r0&target_id="+f.id, nil))
	return w
}
func (f *releasedStartFixture) prepare(t *testing.T) *httptest.ResponseRecorder {
	t.Helper()
	w := f.get()
	var proof startProof
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &proof) != nil {
		t.Fatalf("read %d %s", w.Code, w.Body.String())
	}
	raw, _ := json.Marshal(map[string]any{"room_id": "r0", "target_id": f.id, "policy_revision": proof.PolicyRevision, "alias_incarnation": proof.AliasBinding.Incarnation})
	w = httptest.NewRecorder()
	startRequest(w, httptest.NewRequest("POST", "/api/start-volume", bytes.NewReader(raw)))
	return w
}
func commitReleasedLease(w *httptest.ResponseRecorder) *httptest.ResponseRecorder {
	var lease struct {
		ID string `json:"lease_id"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &lease)
	raw, _ := json.Marshal(map[string]any{"lease_id": lease.ID})
	out := httptest.NewRecorder()
	commitStartRequest(out, httptest.NewRequest("POST", "/api/start-volume/commit", bytes.NewReader(raw)))
	return out
}

func TestReleasedPausedURIStartPrimesBeforeSelectAndCommitsOnce(t *testing.T) {
	for _, selected := range []bool{false, true} {
		t.Run(fmt.Sprint(selected), func(t *testing.T) {
			f := releasedURIFixture(t)
			f.selected = selected
			read := f.get()
			if read.Code != 200 || !bytes.Contains(read.Body.Bytes(), []byte(`"mode":"fresh_start"`)) {
				t.Fatalf("logged-in released route refused: %d %s", read.Code, read.Body.String())
			}
			if len(f.effects) != 0 {
				t.Fatal("GET wrote output")
			}
			lease := f.prepare(t)
			if lease.Code != 200 {
				t.Fatalf("prepare %d %s", lease.Code, lease.Body.String())
			}
			expected := "prime,select,prime"
			if selected {
				expected = "prime"
			}
			if strings.Join(f.effects, ",") != expected || !fileExists(releasedPath(f.room)) {
				t.Fatalf("unsafe order or marker lost: %v", f.effects)
			}
			if got := commitReleasedLease(lease); got.Code != 200 {
				t.Fatalf("commit %d", got.Code)
			}
			if got := commitReleasedLease(lease); got.Code != 409 {
				t.Fatalf("replayed commit %d", got.Code)
			}
		})
	}
}
func TestOrdinaryPausedURIStartRetainsManualVolume(t *testing.T) {
	f := releasedURIFixture(t)
	f.selected = true
	f.volume = 64
	if err := os.Remove(releasedPath(f.room)); err != nil {
		t.Fatal(err)
	}
	read := f.get()
	if read.Code != 200 || !bytes.Contains(read.Body.Bytes(), []byte(`"mode":"same_session"`)) {
		t.Fatalf("ordinary pause lost: %d", read.Code)
	}
	lease := f.prepare(t)
	if lease.Code != 200 || commitReleasedLease(lease).Code != 200 || len(f.effects) != 0 || f.volume != 64 {
		t.Fatalf("ordinary session reset %d %v", lease.Code, f.effects)
	}
}
func TestReleasedURIStartRejectsUnselectedWithoutMarkerAndPlayingWithMarker(t *testing.T) {
	for _, kind := range []string{"unselected-unreleased", "playing-released"} {
		t.Run(kind, func(t *testing.T) {
			f := releasedURIFixture(t)
			if kind == "unselected-unreleased" {
				_ = os.Remove(releasedPath(f.room))
			} else {
				st, _ := f.rt.live.transportSnapshot()
				st.Paused = false
				f.rt.live.set(st)
			}
			if got := f.get(); got.Code != 409 {
				t.Fatalf("unexpected permission %d", got.Code)
			}
			if len(f.effects) != 0 {
				t.Fatal("rejected read wrote")
			}
		})
	}
}
func TestReleasedURIStartReadRejectsMarkerOrOwnerReplacement(t *testing.T) {
	for _, kind := range []string{"marker", "owner"} {
		t.Run(kind, func(t *testing.T) {
			f := releasedURIFixture(t)
			f.onRead = func() {
				if kind == "marker" {
					_ = os.Remove(releasedPath(f.room))
				} else {
					st, _ := f.rt.live.transportSnapshot()
					st.AliasBinding = copyAliasBinding(st.AliasBinding)
					st.AliasBinding.Incarnation = "player-b"
					f.rt.live.set(st)
				}
			}
			if got := f.get(); got.Code != 409 {
				t.Fatalf("changed source minted proof %d", got.Code)
			}
		})
	}
}
func TestReleasedURIStartFailuresKeepMarkerAndMintNoLease(t *testing.T) {
	for _, kind := range []string{"prime", "select"} {
		t.Run(kind, func(t *testing.T) {
			f := releasedURIFixture(t)
			f.failPrime = kind == "prime"
			f.failSelect = kind == "select"
			if got := f.prepare(t); got.Code != 409 {
				t.Fatalf("failure minted lease %d", got.Code)
			}
			if !fileExists(releasedPath(f.room)) {
				t.Fatal("failure removed reclaim marker")
			}
			startLeases.Lock()
			lease := startLeases.Current
			startLeases.Unlock()
			if lease != nil {
				t.Fatal("failure left lease")
			}
			if kind == "prime" && strings.Contains(strings.Join(f.effects, ","), "select") {
				t.Fatal("selected after failed prime")
			}
		})
	}
}
func TestReleasedURIStartCommitRejectsMarkerReplacementAndConsumesLease(t *testing.T) {
	f := releasedURIFixture(t)
	lease := f.prepare(t)
	if lease.Code != 200 {
		t.Fatalf("prepare %d", lease.Code)
	}
	if err := os.Remove(releasedPath(f.room)); err != nil {
		t.Fatal(err)
	}
	if got := commitReleasedLease(lease); got.Code != 409 {
		t.Fatalf("marker replacement admitted %d", got.Code)
	}
	if err := os.WriteFile(releasedPath(f.room), []byte("1"), 0600); err != nil {
		t.Fatal(err)
	}
	if got := commitReleasedLease(lease); got.Code != 409 {
		t.Fatalf("consumed lease replayed %d", got.Code)
	}
}

func TestReleasedURIStartOwnerReplacementAfterPrimeNeverSelects(t *testing.T) {
	f := releasedURIFixture(t)
	f.onPrime = func() {
		st, _ := f.rt.live.transportSnapshot()
		st.AliasBinding = copyAliasBinding(st.AliasBinding)
		st.AliasBinding.Incarnation = "player-b"
		f.rt.live.set(st)
	}
	if got := f.prepare(t); got.Code != 409 {
		t.Fatalf("owner changed after prime admitted %d", got.Code)
	}
	if strings.Join(f.effects, ",") != "prime" || f.selected || !fileExists(releasedPath(f.room)) {
		t.Fatalf("stale work selected output: %v", f.effects)
	}
}

func TestReleasedURIStartCommitRejectsDeselectedOutputAndConsumesLease(t *testing.T) {
	f := releasedURIFixture(t)
	lease := f.prepare(t)
	if lease.Code != 200 {
		t.Fatalf("prepare %d", lease.Code)
	}
	f.selected = false
	if got := commitReleasedLease(lease); got.Code != 409 {
		t.Fatalf("deselected output admitted %d", got.Code)
	}
	f.selected = true
	if got := commitReleasedLease(lease); got.Code != 409 {
		t.Fatalf("consumed lease replayed %d", got.Code)
	}
}
