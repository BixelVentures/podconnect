package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
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
