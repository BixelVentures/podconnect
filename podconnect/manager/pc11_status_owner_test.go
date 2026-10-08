package main

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

func pc11Source(t *testing.T, live *glLive) (glSource, chan struct{}) {
	t.Helper()
	stop := make(chan struct{})
	run := live.beginRun(stop)
	source, ok := live.beginPhase(run)
	if !ok {
		t.Fatal("fresh source refused")
	}
	return source, stop
}

func pc11Request(t *testing.T, live *glLive, source glSource) glStatusRequest {
	t.Helper()
	request, ok := live.beginStatus(source)
	if !ok {
		t.Fatal("current request refused")
	}
	return request
}

func TestPC11StatusPreservesInterveningWireGroups(t *testing.T) {
	cases := []struct {
		name, event string
		data        map[string]any
		want        glStatus
	}{
		{"alias", "selected_alias", map[string]any{"id": 2}, glStatus{Active: true, Paused: true, Stopped: true, HasVol: true, VolPct: 65, SelAlias: 2}},
		{"transport", "playing", nil, glStatus{Active: true, HasVol: true, VolPct: 65, SelAlias: 3}},
		{"volume", "volume", map[string]any{"value": 42}, glStatus{Active: true, Paused: true, Stopped: true, HasVol: true, VolPct: 42, SelAlias: 3}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			live := &glLive{}
			source, _ := pc11Source(t, live)
			request := pc11Request(t, live, source)
			live.applySourceEvent(source, c.event, c.data)
			if !live.acceptStatus(request, glStatus{Active: true, Paused: true, Stopped: true, HasVol: true, VolPct: 65, SelAlias: 3}) {
				t.Fatal("unaffected groups were starved")
			}
			if got := live.Get(); got != c.want {
				t.Fatalf("wire group overwritten or unrelated refresh lost: got %+v want %+v", got, c.want)
			}
		})
	}
}

func TestPC11StatusIrrelevantAndMalformedEventsDoNotStarveRefresh(t *testing.T) {
	live := &glLive{}
	source, _ := pc11Source(t, live)
	request := pc11Request(t, live, source)
	for _, event := range []struct {
		name string
		data map[string]any
	}{
		{"seek", map[string]any{"value": 3}},
		{"metadata", map[string]any{"uri": "spotify:track:inert"}},
		{"selected_alias", map[string]any{"id": "invalid"}},
		{"volume", map[string]any{"value": "invalid"}},
	} {
		live.applySourceEvent(source, event.name, event.data)
	}
	want := glStatus{Active: true, HasVol: true, VolPct: 65, SelAlias: 2}
	if !live.acceptStatus(request, want) || live.Get() != want || live.trackSeq() != 1 {
		t.Fatal("irrelevant frames blocked status-only refresh or changed metadata semantics")
	}
}

func TestPC11StatusOrdersLatestAcceptedNotLatestIssued(t *testing.T) {
	live := &glLive{}
	source, _ := pc11Source(t, live)
	old := pc11Request(t, live, source)
	newer := pc11Request(t, live, source)
	if !live.acceptStatus(old, glStatus{SelAlias: 1}) {
		t.Fatal("merely starting a newer request incorrectly retired the old response")
	}
	if !live.acceptStatus(newer, glStatus{SelAlias: 2}) {
		t.Fatal("newer completed response refused")
	}
	if live.acceptStatus(old, glStatus{SelAlias: 1}) || live.Get().SelAlias != 2 {
		t.Fatal("older completion overwrote accepted newer response")
	}
}

func TestPC11StatusEntirelyVetoedResponseDoesNotAdvanceAcceptance(t *testing.T) {
	live := &glLive{}
	source, _ := pc11Source(t, live)
	request := pc11Request(t, live, source)
	live.applySourceEvent(source, "playing", nil)
	live.applySourceEvent(source, "volume", map[string]any{"value": 42})
	live.applySourceEvent(source, "selected_alias", map[string]any{"id": 2})
	before := live.Get()
	if live.acceptStatus(request, glStatus{}) || live.acceptedRequest != 0 || live.Get() != before {
		t.Fatal("entirely vetoed response mutated state or accepted order")
	}
}

func TestPC11StatusValidEqualAliasFencesABA(t *testing.T) {
	live := &glLive{}
	live.set(glStatus{SelAlias: 1})
	source, _ := pc11Source(t, live)
	request := pc11Request(t, live, source)
	_, revision := live.routeSnapshot()
	live.applySourceEvent(source, "selected_alias", map[string]any{"id": 2})
	live.applySourceEvent(source, "selected_alias", map[string]any{"id": 1})
	live.applySourceEvent(source, "selected_alias", map[string]any{"id": 1})
	if !live.acceptStatus(request, glStatus{HasVol: true, VolPct: 65, SelAlias: 2}) {
		t.Fatal("unaffected groups refused")
	}
	got, current := live.routeSnapshot()
	if got.SelAlias != 1 || got.VolPct != 65 || current != revision+3 {
		t.Fatal("same alias value concealed newer wire intent or lost unrelated status refresh")
	}
}

func TestPC11StatusRetiredPhaseRunAndStopCannotPublish(t *testing.T) {
	live := &glLive{}
	old, stop := pc11Source(t, live)
	oldRequest := pc11Request(t, live, old)
	live.retirePhase(old)
	phase, ok := live.beginPhase(glSource{run: old.run, stop: stop})
	if !ok {
		t.Fatal("fresh fallback phase refused")
	}
	if live.acceptStatus(oldRequest, glStatus{SelAlias: 1}) {
		t.Fatal("old phase response admitted before any new request was accepted")
	}
	if _, ok := live.beginStatus(old); ok {
		t.Fatal("retired reseed goroutine minted a current request")
	}
	if !live.acceptStatus(pc11Request(t, live, phase), glStatus{SelAlias: 2, HasVol: true, VolPct: 65}) {
		t.Fatal("fresh fallback response refused")
	}
	before := live.Get()
	if live.acceptStatus(oldRequest, glStatus{SelAlias: 1}) {
		t.Fatal("old connection response admitted")
	}
	live.applySourceEvent(old, "selected_alias", map[string]any{"id": 1})
	if live.Get() != before {
		t.Fatal("retired wire producer mutated new phase")
	}
	newSource, newStop := pc11Source(t, live)
	live.retireRun(glSource{run: old.run, stop: stop})
	if live.acceptStatus(oldRequest, glStatus{SelAlias: 1}) {
		t.Fatal("old run response admitted before any new run response")
	}
	newRequest := pc11Request(t, live, newSource)
	if !live.acceptStatus(newRequest, glStatus{SelAlias: 3}) {
		t.Fatal("old run retirement killed new run")
	}
	if live.acceptStatus(oldRequest, glStatus{SelAlias: 1}) {
		t.Fatal("old run response admitted")
	}
	pending := pc11Request(t, live, newSource)
	close(newStop)
	before = live.Get()
	if live.acceptStatus(pending, glStatus{SelAlias: 1}) {
		t.Fatal("response published after Stop")
	}
	live.applySourceEvent(newSource, "selected_alias", map[string]any{"id": 1})
	if live.Get() != before {
		t.Fatal("wire producer published after Stop")
	}
}

func TestPC11RouteAdmissionFencesAliasSourceAndStop(t *testing.T) {
	live := &glLive{}
	source, stop := pc11Source(t, live)
	live.applySourceEvent(source, "selected_alias", map[string]any{"id": 1})
	_, old := live.routeSnapshot()
	live.applySourceEvent(source, "selected_alias", map[string]any{"id": 2})
	if live.admitRoute(1, old) {
		t.Fatal("stale prepared A command admitted")
	}
	state, current := live.routeSnapshot()
	if !live.admitRoute(state.SelAlias, current) || live.routeDispatchSeq != 1 {
		t.Fatal("current command did not create exactly one admission claim")
	}
	live.applySourceEvent(source, "paused", nil)
	_, afterTransport := live.routeSnapshot()
	if afterTransport != current {
		t.Fatal("transport event changed alias intent")
	}
	live.retirePhase(source)
	if live.admitRoute(2, current) {
		t.Fatal("retired source command admitted")
	}
	_, retiredRevision := live.routeSnapshot()
	if live.admitRoute(2, retiredRevision) {
		t.Fatal("fresh command admitted while its source phase is retired")
	}
	_, beforeStop := live.routeSnapshot()
	phase, ok := live.beginPhase(glSource{run: source.run, stop: stop})
	if !ok {
		t.Fatal("replacement phase refused")
	}
	_ = phase
	if live.admitRoute(2, retiredRevision) {
		t.Fatal("claim captured after retirement crossed into a new source phase")
	}
	_, freshRevision := live.routeSnapshot()
	if !live.admitRoute(2, freshRevision) || live.routeDispatchSeq != 2 {
		t.Fatal("fresh replacement source claim was not admitted")
	}
	_, beforeStop = live.routeSnapshot()
	close(stop)
	if live.admitRoute(2, beforeStop) {
		t.Fatal("Stop did not veto new command admission")
	}
	_, afterStop := live.routeSnapshot()
	if afterStop == beforeStop || live.routeDispatchSeq != 2 {
		t.Fatal("Stop failed to invalidate revision or claimed command")
	}
}

func TestPC11PartialNewerCompletionRetiresUnseenOldResponse(t *testing.T) {
	live := &glLive{}
	source, _ := pc11Source(t, live)
	old := pc11Request(t, live, source)
	newer := pc11Request(t, live, source)
	live.applySourceEvent(source, "selected_alias", map[string]any{"id": 2})
	want := glStatus{Active: true, HasVol: true, VolPct: 65, SelAlias: 2}
	if !live.acceptStatus(newer, glStatus{Active: true, HasVol: true, VolPct: 65, SelAlias: 1}) || live.Get() != want {
		t.Fatal("newer partial response failed to refresh unaffected groups")
	}
	if live.acceptStatus(old, glStatus{HasVol: true, VolPct: 10, SelAlias: 1}) || live.Get() != want {
		t.Fatal("previously unseen older response overwrote newer accepted groups")
	}
}

// This invokes the shipped refreshStatus HTTP/parse/publish path, not a copied
// owner algorithm. Its return channel is the completed old publication attempt.
func TestPC11ActualHTTPRefreshCannotCrossRetirement(t *testing.T) {
	live := &glLive{}
	initial := glStatus{SelAlias: 2, HasVol: true, VolPct: 65}
	live.set(initial)
	source, stop := pc11Source(t, live)
	held, release := make(chan struct{}), make(chan struct{})
	var releaseOnce sync.Once
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/status" {
			http.Error(w, "unexpected owned endpoint", http.StatusBadRequest)
			return
		}
		close(held)
		select {
		case <-release:
			_, _ = w.Write([]byte(`{"selected_alias_id":1,"username":"inert","volume":10,"volume_steps":100}`))
		case <-r.Context().Done():
		}
	}))
	oldTransport := http.DefaultTransport
	dialer := &net.Dialer{}
	transport := &http.Transport{Proxy: nil, DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
		if address != server.Listener.Addr().String() {
			return nil, fmt.Errorf("refuse nonowned HTTP destination %q", address)
		}
		return dialer.DialContext(ctx, network, address)
	}}
	http.DefaultTransport = transport
	done := make(chan bool, 1)
	joined := false
	t.Cleanup(func() {
		releaseOnce.Do(func() { close(release) })
		server.CloseClientConnections()
		if !joined {
			select {
			case <-done:
				joined = true
			case <-time.After(2 * time.Second):
				t.Error("old actual HTTP refresh did not join")
			}
		}
		transport.CloseIdleConnections()
		server.Close()
		if joined {
			http.DefaultTransport = oldTransport
		}
	})
	go func() { done <- live.refreshStatus(source, server.URL) }()
	select {
	case <-held:
	case <-time.After(2 * time.Second):
		t.Fatal("old actual GET was not held")
	}
	live.retirePhase(source)
	if _, ok := live.beginPhase(glSource{run: source.run, stop: stop}); !ok {
		t.Fatal("new phase refused")
	}
	if live.acceptedRequest != 0 || live.wireRevision != [3]uint64{} {
		t.Fatal("new status or wire input would conceal the source-only boundary")
	}
	releaseOnce.Do(func() { close(release) })
	select {
	case current := <-done:
		joined = true
		if current {
			t.Fatal("old HTTP refresh reported a current source")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("released old actual HTTP refresh did not return")
	}
	if live.Get() != initial || live.acceptedRequest != 0 {
		t.Fatal("completed old publication mutated current B without any newer accepted status")
	}
}
