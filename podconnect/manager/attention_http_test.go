package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// setupAttentionRoom wires a single supervised room r0 into the package globals (store + mgr) so the
// attention handlers, which resolve through roomByID + attentionFor, can find it. dataDir is a fresh
// temp dir per test so options.json (auth) starts empty.
func setupAttentionRoom(t *testing.T) {
	t.Helper()
	dataDir = t.TempDir()
	store = newRoomStore()
	rf := &roomsFile{NextIdx: 1, Rooms: []*Room{{ID: "r0", Idx: 0, Name: "Kitchen", HomepodName: "Kitchen HomePod"}}}
	store.mu.Lock()
	err := store.saveLocked(rf)
	store.mu.Unlock()
	if err != nil {
		t.Fatalf("save rooms: %v", err)
	}
	mgr = newRoomManager(store)
	r0 := roomByID("r0")
	if r0 == nil {
		t.Fatal("r0 not loadable after save")
	}
	mgr.runtimes["r0"] = &roomRuntime{room: r0, stop: make(chan struct{}), done: make(chan struct{})}
}

func callAttention(h http.HandlerFunc, method, path, body string, hdr map[string]string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	h(w, req)
	return w
}

func TestAttentionHandlerEngage(t *testing.T) {
	setupAttentionRoom(t)
	w := callAttention(attentionHandler, http.MethodPost, "/api/attention", `{"room":"r0","level":5,"ttl_ms":5000}`, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("engage: status %d, body %q", w.Code, w.Body.String())
	}
	var snap attSnapshot
	if err := json.Unmarshal(w.Body.Bytes(), &snap); err != nil {
		t.Fatalf("decode snapshot: %v", err)
	}
	if !snap.Active || snap.Level != 5 {
		t.Fatalf("want active level 5, got %+v", snap)
	}
	if snap.RemainingMS <= 0 || snap.RemainingMS > 5000 {
		t.Fatalf("remaining_ms out of range: %d", snap.RemainingMS)
	}
	// And the live state actually reflects the duck.
	if a := attentionFor("r0"); a == nil || !a.snapshot(time.Now()).Active {
		t.Fatalf("room r0 attention not active after engage")
	}
}

func TestAttentionHandlerClampsTTL(t *testing.T) {
	setupAttentionRoom(t)
	w := callAttention(attentionHandler, http.MethodPost, "/api/attention", `{"room":"r0","level":5,"ttl_ms":3600000}`, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d", w.Code)
	}
	var snap attSnapshot
	_ = json.Unmarshal(w.Body.Bytes(), &snap)
	if snap.RemainingMS > int(maxAttentionTTL.Milliseconds()) {
		t.Fatalf("ttl not clamped: remaining_ms=%d > max=%d", snap.RemainingMS, maxAttentionTTL.Milliseconds())
	}
}

func TestAttentionHandlerUnknownRoom(t *testing.T) {
	setupAttentionRoom(t)
	w := callAttention(attentionHandler, http.MethodPost, "/api/attention", `{"room":"nope","level":5}`, nil)
	if w.Code != http.StatusNotFound {
		t.Fatalf("unknown room: want 404, got %d", w.Code)
	}
}

func TestAttentionHandlerUnsupervised(t *testing.T) {
	setupAttentionRoom(t)
	delete(mgr.runtimes, "r0") // room exists in store but isn't supervised
	w := callAttention(attentionHandler, http.MethodPost, "/api/attention", `{"room":"r0","level":5}`, nil)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("unsupervised: want 503, got %d", w.Code)
	}
}

func TestAttentionHandlerMethodNotAllowed(t *testing.T) {
	setupAttentionRoom(t)
	w := callAttention(attentionHandler, http.MethodPut, "/api/attention", `{}`, nil)
	if w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("PUT: want 405, got %d", w.Code)
	}
}

func TestAttentionHandlerGET(t *testing.T) {
	setupAttentionRoom(t)
	callAttention(attentionHandler, http.MethodPost, "/api/attention", `{"room":"r0","level":5,"ttl_ms":3000}`, nil)

	w := callAttention(attentionHandler, http.MethodGet, "/api/attention", "", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("GET status %d", w.Code)
	}
	var got struct {
		Rooms map[string]attSnapshot `json:"rooms"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode GET: %v", err)
	}
	if r0, ok := got.Rooms["r0"]; !ok || !r0.Active || r0.Level != 5 {
		t.Fatalf("GET should report r0 ducked, got %+v", got.Rooms)
	}
}

func TestAttentionReleaseHandler(t *testing.T) {
	setupAttentionRoom(t)
	callAttention(attentionHandler, http.MethodPost, "/api/attention", `{"room":"r0","level":5,"ttl_ms":60000}`, nil)

	w := callAttention(attentionReleaseHandler, http.MethodPost, "/api/attention/release", `{"room":"r0"}`, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("release status %d", w.Code)
	}
	// After release the next tick should report released (and no longer hold).
	a := attentionFor("r0")
	if a == nil {
		t.Fatal("missing runtime")
	}
	if hold, _, released, _ := a.tick(time.Now()); hold || !released {
		t.Fatalf("after release: want hold=false released=true, got hold=%v released=%v", hold, released)
	}
}

func TestAttentionReleaseHandlerMethodNotAllowed(t *testing.T) {
	setupAttentionRoom(t)
	w := callAttention(attentionReleaseHandler, http.MethodGet, "/api/attention/release", "", nil)
	if w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("GET release: want 405, got %d", w.Code)
	}
}

func TestAttentionAuth(t *testing.T) {
	setupAttentionRoom(t)
	writeOptions(t, `{"attention_token":"s3cret"}`)

	// No header → unauthorized.
	if w := callAttention(attentionHandler, http.MethodGet, "/api/attention", "", nil); w.Code != http.StatusUnauthorized {
		t.Fatalf("missing token: want 401, got %d", w.Code)
	}
	// Wrong header → unauthorized.
	if w := callAttention(attentionHandler, http.MethodGet, "/api/attention", "", map[string]string{"X-PodConnect-Token": "nope"}); w.Code != http.StatusUnauthorized {
		t.Fatalf("wrong token: want 401, got %d", w.Code)
	}
	// Correct header → OK.
	if w := callAttention(attentionHandler, http.MethodGet, "/api/attention", "", map[string]string{"X-PodConnect-Token": "s3cret"}); w.Code != http.StatusOK {
		t.Fatalf("correct token: want 200, got %d", w.Code)
	}
}

func TestReadAttentionToken(t *testing.T) {
	dataDir = t.TempDir()
	if got := readAttentionToken(); got != "" {
		t.Fatalf("no options.json should give empty token, got %q", got)
	}
	writeOptions(t, `{"attention_token":"  abc  "}`)
	if got := readAttentionToken(); got != "abc" {
		t.Fatalf("token should be trimmed to %q, got %q", "abc", got)
	}
}

func writeOptions(t *testing.T, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dataDir, "options.json"), []byte(body), 0o644); err != nil {
		t.Fatalf("write options.json: %v", err)
	}
}
