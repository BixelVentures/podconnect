package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type revisionHTTPFixture struct {
	server  *httptest.Server
	scope   roomTransportScope
	live    *glLive
	route   outputTransportIdentity
	reads   atomic.Int32
	writes  atomic.Int32
	waiters atomic.Int32
	mu      sync.Mutex
	next    uint64
	close   chan struct{}
}

func newRevisionHTTPFixture(t *testing.T, wait time.Duration) *revisionHTTPFixture {
	t.Helper()
	released, scope, native, route := releasedRoomRouteFixture(t)
	f := &revisionHTTPFixture{scope: scope, live: &released.rt.live, route: *route, close: make(chan struct{})}
	oldRPC := roomTransportRPC
	t.Cleanup(func() { roomTransportRPC = oldRPC })
	roomTransportRPC = func(ctx context.Context, _ string, method, path string, body []byte) ([]byte, error) {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		switch {
		case method == http.MethodGet && strings.HasPrefix(path, "/player/room_transport/bound-state?"):
			query, _ := url.ParseQuery(strings.SplitN(path, "?", 2)[1])
			if query.Get("account_user_id") != scope.Account || query.Get("target_id") != scope.Target ||
				query.Get("room_id") != scope.Room || query.Get("homepod_id") != scope.Homepod {
				return nil, fmt.Errorf("foreign scope")
			}
			return json.Marshal(receiverTransportState{
				Schema: "podconnect.native_room_transport_state.v2", Ready: true,
				Identity: *native, Actions: []string{"pause"},
			})
		case method == http.MethodPost && path == "/api/outputs/42/transport/bound-state":
			var request struct {
				Owner roomTransportIdentity `json:"native_owner"`
			}
			if json.Unmarshal(body, &request) != nil || request.Owner != *native {
				return nil, fmt.Errorf("foreign native proof")
			}
			f.reads.Add(1)
			f.mu.Lock()
			current := f.route
			f.mu.Unlock()
			return json.Marshal(outputTransportState{
				Schema: "podconnect.output_transport_state.v2", OK: true,
				Identity: current, Actions: []string{"pause"}, StartOwned: true,
			})
		default:
			f.writes.Add(1)
			return nil, fmt.Errorf("unexpected effect RPC")
		}
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/api/state", func(w http.ResponseWriter, r *http.Request) {
		headers := r.Header.Values(playbackRevisionHeader)
		if len(headers) == 1 && headers[0] != "seed" {
			f.waiters.Add(1)
			defer f.waiters.Add(-1)
		}
		if len(headers) != 0 && wait != playbackRevisionWait {
			servePlaybackRevision(w, r, headers, wait)
		} else if !playbackRevisionRequest(w, r) {
			writeJSON(w, map[string]bool{"legacy": true})
		}
	})
	mux.HandleFunc("/api/start-volume/identity", startIdentityRequest)
	mux.HandleFunc("/fixture/waiters", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		writeJSON(w, map[string]int32{"waiters": f.waiters.Load()})
	})
	mux.HandleFunc("/fixture/track", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		f.track(false)
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("/fixture/retire", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		f.live.retirePhase(f.source())
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("/fixture/close", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		select {
		case <-f.close:
		default:
			close(f.close)
		}
		w.WriteHeader(http.StatusNoContent)
	})
	f.server = httptest.NewServer(mux)
	t.Cleanup(f.server.Close)
	return f
}

func (f *revisionHTTPFixture) source() glSource {
	f.live.mu.Lock()
	defer f.live.mu.Unlock()
	return glSource{f.live.runEpoch, f.live.phaseEpoch, f.live.sourceStop}
}

func (f *revisionHTTPFixture) track(repeat bool) {
	f.mu.Lock()
	if !repeat {
		f.next++
		f.route.Route = fmt.Sprint(f.next + 3)
		f.route.Input = fmt.Sprint(f.next + 5)
		f.route.Item = fmt.Sprint(f.next + 6)
	}
	uri := fmt.Sprintf("spotify:track:%022d", f.next)
	f.mu.Unlock()
	f.live.applySourceEvent(f.source(), "metadata", map[string]any{"uri": uri})
}

func (f *revisionHTTPFixture) request(ctx context.Context, method, cursor string, query url.Values) (int, playbackRevisionReply, error) {
	if query == nil {
		query = url.Values{"target_id": {f.scope.Target}, "account_user_id": {f.scope.Account}}
	}
	r, err := http.NewRequestWithContext(ctx, method, f.server.URL+"/api/state?"+query.Encode(), nil)
	if err != nil {
		return 0, playbackRevisionReply{}, err
	}
	if cursor != "" {
		r.Header.Set(playbackRevisionHeader, cursor)
	}
	response, err := f.server.Client().Do(r)
	if err != nil {
		return 0, playbackRevisionReply{}, err
	}
	defer response.Body.Close()
	var reply playbackRevisionReply
	if response.StatusCode == http.StatusOK && cursor != "" {
		err = json.NewDecoder(response.Body).Decode(&reply)
	} else {
		_, err = io.Copy(io.Discard, response.Body)
	}
	return response.StatusCode, reply, err
}

func revisionEventually(t *testing.T, ready func() bool) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for !ready() && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if !ready() {
		t.Fatal("revision fixture did not reach expected state")
	}
}

func TestPlaybackRevisionHTTPNativeMetadataAndQuietDuplicate(t *testing.T) {
	f := newRevisionHTTPFixture(t, 150*time.Millisecond)
	ctx := context.Background()
	code, initial, err := f.request(ctx, http.MethodGet, "seed", nil)
	if err != nil || code != 200 || initial.Schema != "podconnect.playback_revision.v1" || initial.Changed || initial.Cursor == "" {
		t.Fatalf("seed: %d %+v %v", code, initial, err)
	}
	before := f.reads.Load()
	done := make(chan playbackRevisionReply, 1)
	go func() {
		code, result, err := f.request(ctx, http.MethodGet, initial.Cursor, nil)
		if err != nil || code != 200 {
			t.Errorf("track wait: %d %v", code, err)
		}
		done <- result
	}()
	revisionEventually(t, func() bool { return f.reads.Load() > before })
	f.track(false)
	updated := <-done
	if !updated.Changed || updated.Cursor == initial.Cursor {
		t.Fatalf("native track did not advance cursor: %+v", updated)
	}
	f.track(true)
	code, _, err = f.request(ctx, http.MethodGet, updated.Cursor, nil)
	if err != nil || code != 204 || f.writes.Load() != 0 {
		t.Fatalf("duplicate woke or wrote: %d %v", code, err)
	}
}

func TestPlaybackRevisionHTTPOldPhaseAndRouteBaseline(t *testing.T) {
	f := newRevisionHTTPFixture(t, 45*time.Millisecond)
	ctx := context.Background()
	_, initial, _ := f.request(ctx, http.MethodGet, "seed", nil)
	old := f.source()
	f.live.retirePhase(old)
	code, _, err := f.request(ctx, http.MethodGet, initial.Cursor, nil)
	if err != nil || code != 409 {
		t.Fatalf("retired phase remained available: %d %v", code, err)
	}
	if _, ok := f.live.beginPhase(glSource{run: old.run, stop: old.stop}); !ok {
		t.Fatal("new phase unavailable")
	}
	code, fresh, err := f.request(ctx, http.MethodGet, initial.Cursor, nil)
	if err != nil || code != 200 || fresh.Changed || fresh.Cursor == initial.Cursor {
		t.Fatalf("new phase did not baseline: %d %+v %v", code, fresh, err)
	}
	f.live.applySourceEvent(old, "metadata", map[string]any{"uri": "spotify:track:retired"})
	code, _, err = f.request(ctx, http.MethodGet, fresh.Cursor, nil)
	if err != nil || code != 204 {
		t.Fatalf("old phase event woke current route: %d %v", code, err)
	}
	f.mu.Lock()
	f.route.Session = "3"
	f.mu.Unlock()
	f.track(false)
	code, moved, err := f.request(ctx, http.MethodGet, fresh.Cursor, nil)
	if err != nil || code != 200 || moved.Changed || moved.Cursor == fresh.Cursor || f.writes.Load() != 0 {
		t.Fatalf("new output session crossed the old cursor: %d %+v %v", code, moved, err)
	}
}

func TestPlaybackRevisionHTTPInputCapacityAndCancellation(t *testing.T) {
	f := newRevisionHTTPFixture(t, playbackRevisionWait)
	ctx := context.Background()
	code, initial, err := f.request(ctx, http.MethodGet, "seed", nil)
	if err != nil || code != 200 {
		t.Fatalf("seed: %d %v", code, err)
	}
	for _, tc := range []struct {
		method, cursor string
		query          url.Values
		want           int
	}{
		{http.MethodGet, "", nil, 200},
		{http.MethodPost, "seed", nil, 400},
		{http.MethodGet, "bad", nil, 400},
		{http.MethodGet, strings.Repeat("b", 32) + ":18446744073709551616", nil, 400},
		{http.MethodGet, "seed", url.Values{"target_id": {f.scope.Target}, "account_user_id": {f.scope.Account, "extra"}}, 400},
		{http.MethodGet, "seed", url.Values{"target_id": {f.scope.Target}, "account_user_id": {"foreign"}}, 409},
	} {
		if code, _, err := f.request(ctx, tc.method, tc.cursor, tc.query); err != nil || code != tc.want {
			t.Fatalf("invalid/read request: got %d want %d %v", code, tc.want, err)
		}
	}
	cancelledCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	var requests sync.WaitGroup
	for range playbackRevisionCap {
		requests.Add(1)
		go func() {
			defer requests.Done()
			if _, _, err := f.request(cancelledCtx, http.MethodGet, initial.Cursor, nil); err == nil {
				t.Error("cancelled wait returned a result")
			}
		}()
	}
	revisionEventually(t, func() bool { return len(playbackRevisionSlots) == playbackRevisionCap })
	if code, _, err := f.request(ctx, http.MethodGet, initial.Cursor, nil); err != nil || code != 429 {
		t.Fatalf("capacity not bounded: %d %v", code, err)
	}
	cancel()
	requests.Wait()
	revisionEventually(t, func() bool { return len(playbackRevisionSlots) == 0 })
	if f.writes.Load() != 0 {
		t.Fatal("read-only requests sent native effects")
	}
}

// Optional HA framework bridge. The test binary runs the real manager handler
// over localhost, with the same inert native/OwnTone owner joins as above.
func TestPlaybackRevisionHTTPBridge(t *testing.T) {
	if os.Getenv("PODCONNECT_PLAYBACK_REVISION_FIXTURE") != "1" {
		t.Skip("used by the composed HA framework test")
	}
	f := newRevisionHTTPFixture(t, playbackRevisionWait)
	fmt.Println(f.server.URL)
	select {
	case <-f.close:
	case <-time.After(time.Minute):
		t.Fatal("composed HA fixture did not close")
	}
	if f.writes.Load() != 0 {
		t.Fatal("read-only bridge sent native effects")
	}
}
