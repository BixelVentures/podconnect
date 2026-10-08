package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

func TestAliasRoutingKeepsRecoveryPendingOnSelectionFailure(t *testing.T) {
	oldDir, oldStore, oldMgr := dataDir, store, mgr
	t.Cleanup(func() { dataDir, store, mgr = oldDir, oldStore, oldMgr })
	setupAttentionRoom(t)
	calls := 0
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/outputs":
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"outputs":[{"id":"42","name":"Kitchen HomePod","type":"AirPlay"}]}`))
		case "/api/outputs/set":
			calls++
			if calls == 1 {
				w.WriteHeader(http.StatusServiceUnavailable)
			} else {
				w.WriteHeader(http.StatusNoContent)
			}
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
		}
	}))
	defer backend.Close()
	// false is what keeps the existing throttled alias recovery path armed.
	if routeAliasOutput(backend.URL, 1) {
		t.Fatal("failed selection incorrectly acknowledged")
	}
	if calls != 1 {
		t.Fatalf("selection retried inside one attempt: %d", calls)
	}
	if !routeAliasOutput(backend.URL, 1) {
		t.Fatal("later accepted selection did not recover")
	}
	if calls != 2 {
		t.Fatalf("selection calls=%d", calls)
	}
}

func TestSelectOutputDisconnected(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	backend.Close()
	if selectOnOwntoneAt(backend.URL, "42") {
		t.Fatal("disconnected backend acknowledged")
	}
}

// Exercise the actual selector with bounded owned HTTP/goroutine fixtures. No
// roomBridge goroutine, process launcher, global transport or native ACK claim.
func testAliasIntentCurrency(t *testing.T, holdPut, aba bool) {
	t.Helper()
	oldDir, oldStore, oldMgr := dataDir, store, mgr
	t.Cleanup(func() { dataDir, store, mgr = oldDir, oldStore, oldMgr })
	setupAttentionRoom(t)
	store.mu.Lock()
	err := store.saveLocked(&roomsFile{NextIdx: 2, Rooms: []*Room{
		{ID: "r0", Idx: 0, Name: "A", HomepodID: "42", HomepodName: "A"},
		{ID: "r1", Idx: 1, Name: "B", HomepodID: "43", HomepodName: "B"},
	}})
	store.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	held, release := make(chan struct{}), make(chan struct{})
	var holdOnce, releaseOnce sync.Once
	var mu sync.Mutex
	var puts []string
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		wait := func() {
			holdOnce.Do(func() {
				close(held)
				select {
				case <-release:
				case <-r.Context().Done():
				}
			})
		}
		switch r.URL.Path {
		case "/api/outputs":
			if !holdPut {
				wait()
			}
			w.Write([]byte(`{"outputs":[{"id":"42","name":"A","type":"AirPlay"},{"id":"43","name":"B","type":"AirPlay"}]}`))
		case "/api/outputs/set":
			var payload struct {
				Outputs []string `json:"outputs"`
			}
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil || len(payload.Outputs) != 1 {
				http.Error(w, "invalid owned PUT", 400)
				return
			}
			mu.Lock()
			puts = append(puts, payload.Outputs[0])
			mu.Unlock()
			if holdPut {
				wait()
			}
			w.WriteHeader(http.StatusNoContent)
		default:
			http.Error(w, "unexpected owned endpoint", 500)
		}
	}))
	var live glLive
	live.applyEvent("selected_alias", map[string]any{"id": 1})
	status, revision := live.routeSnapshot()
	result, done := make(chan [2]bool, 1), make(chan struct{})
	t.Cleanup(func() {
		releaseOnce.Do(func() { close(release) })
		backend.CloseClientConnections()
		backend.Close()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("retained route owner did not join")
		}
	})
	go func() {
		defer close(done)
		accepted, current := routeAliasOutputForIntent(backend.URL, status.SelAlias, &live, revision)
		result <- [2]bool{accepted, current}
	}()
	select {
	case <-held:
	case <-time.After(5 * time.Second):
		t.Fatal("actual held HTTP boundary not reached")
	}
	live.applyEvent("selected_alias", map[string]any{"id": 2})
	if aba {
		live.applyEvent("selected_alias", map[string]any{"id": 1})
	}
	releaseOnce.Do(func() { close(release) })
	select {
	case got := <-result:
		if got != [2]bool{false, false} {
			t.Fatalf("obsolete result accepted/published: %v", got)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("held route completion not observed")
	}
	mu.Lock()
	before := append([]string(nil), puts...)
	mu.Unlock()
	if (!holdPut && len(before) != 0) || (holdPut && (len(before) != 1 || before[0] != "42")) {
		t.Fatalf("incorrect pre-admission/already-issued boundary: %v", before)
	}
	fresh, nextRevision := live.routeSnapshot()
	accepted, current := routeAliasOutputForIntent(backend.URL, fresh.SelAlias, &live, nextRevision)
	if !accepted || !current {
		t.Fatalf("fresh current route not accepted: %v/%v", accepted, current)
	}
	want := "43"
	if aba {
		want = "42"
	}
	mu.Lock()
	after := append([]string(nil), puts...)
	mu.Unlock()
	if len(after) != len(before)+1 || after[len(after)-1] != want {
		t.Fatalf("fresh current desired PUT missing/duplicated: %v", after)
	}
}

func TestAliasHeldCatalogRejectsNewIntentAndRoutesCurrent(t *testing.T) {
	testAliasIntentCurrency(t, false, false)
}
func TestAliasHeldCatalogABARequiresFreshIntent(t *testing.T) {
	testAliasIntentCurrency(t, false, true)
}
func TestAliasAlreadyIssuedPutCompletesObsoleteThenCurrent(t *testing.T) {
	testAliasIntentCurrency(t, true, false)
}
