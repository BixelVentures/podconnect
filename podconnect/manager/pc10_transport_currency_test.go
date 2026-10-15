package main

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// roomBridge has no stop/join seam. Run its actual loop in a bounded owned test
// process instead of leaking a bridge goroutine or copying its iteration.
func TestPC10BridgeRejectsStaleTransport(t *testing.T) {
	mode := os.Getenv("PC10_BRIDGE_CHILD")
	if mode == "" {
		for _, mode := range []string{"playing", "paused", "event_aba", "poll_playing", "poll_aba", "alias_aba", "phase", "new_run", "admitted_completion"} {
			t.Run(mode, func(t *testing.T) {
				ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
				defer cancel()
				command := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestPC10BridgeRejectsStaleTransport$", "-test.timeout=6s")
				command.Env = append(os.Environ(), "PC10_BRIDGE_CHILD="+mode)
				output, err := command.CombinedOutput()
				if err != nil {
					t.Fatalf("actual bridge child joined: %v\n%s", err, output)
				}
				t.Logf("actual bridge child joined: %s", output)
			})
		}
		return
	}
	setupAttentionRoom(t)
	live := &glLive{}
	source, stop := pc11Source(t, live)
	defer func() { close(stop) }()
	initial := glStatus{Active: true, Paused: mode != "paused" && mode != "admitted_completion", HasVol: true, VolPct: 35}
	initial.SelAlias = 1
	if !live.acceptStatus(pc11Request(t, live, source), initial) {
		t.Fatal("initial source refused")
	}
	held, release := make(chan struct{}), make(chan struct{})
	var releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	defer unblock()
	commands := make(chan string, 8)
	var playerReads atomic.Int32
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/outputs":
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"outputs":[{"id":"42","name":"Kitchen HomePod","type":"AirPlay","selected":true,"volume":35}]}`)
		case "/api/player/volume":
			if r.Method != http.MethodPut || r.URL.Query().Get("output_id") != "42" || r.URL.Query().Get("volume") != "35" {
				t.Error("fresh-start primer lost the fixture's exact output and level")
			}
			w.WriteHeader(http.StatusNoContent)
		case "/api/player":
			if playerReads.Add(1) == 1 && mode != "admitted_completion" {
				close(held)
				select {
				case <-release:
				case <-r.Context().Done():
					return
				}
				if initial.Paused {
					fmt.Fprint(w, `{"state":"play"}`)
				} else {
					fmt.Fprint(w, `{"state":"pause"}`)
				}
			} else if mode == "paused" || mode == "event_aba" || mode == "poll_aba" || mode == "alias_aba" || mode == "admitted_completion" && playerReads.Load() > 1 {
				fmt.Fprint(w, `{"state":"play"}`)
			} else {
				fmt.Fprint(w, `{"state":"pause"}`)
			}
		case "/api/player/pause", "/api/player/play":
			commands <- "native:" + strings.TrimPrefix(r.URL.Path, "/api/player/")
			if mode == "admitted_completion" && playerReads.Load() == 1 {
				close(held)
				select {
				case <-release:
				case <-r.Context().Done():
					return
				}
			}
			w.WriteHeader(http.StatusNoContent)
		case "/player/pause", "/player/resume":
			commands <- "spotify:" + strings.TrimPrefix(r.URL.Path, "/player/")
			w.WriteHeader(http.StatusOK)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer func() { unblock(); backend.Close() }()
	room := &Room{ID: "r0", Idx: 1, Name: "fixture", OwnTone: backend.URL, Librespot: backend.URL, HomepodID: "42", HomepodName: "Kitchen HomePod"}
	go roomBridge(room, &boolFlag{}, live, &attention{})
	select {
	case <-held:
	case <-time.After(2 * time.Second):
		t.Fatal("actual bridge did not reach held native read")
	}
	want := "native:play"
	switch mode {
	case "playing":
		live.applySourceEvent(source, "playing", nil)
	case "paused", "admitted_completion":
		live.applySourceEvent(source, "paused", nil)
		want = "native:pause"
	case "event_aba":
		live.applySourceEvent(source, "playing", nil)
		live.applySourceEvent(source, "paused", nil)
		want = "native:pause"
	case "poll_playing", "poll_aba":
		next := initial
		next.Paused = false
		if !live.acceptStatus(pc11Request(t, live, source), next) {
			t.Fatal("intervening actual poll not accepted")
		}
		if mode == "poll_aba" {
			if !live.acceptStatus(pc11Request(t, live, source), initial) {
				t.Fatal("return poll not accepted")
			}
			want = "native:pause"
		}
	case "alias_aba":
		live.applySourceEvent(source, "selected_alias", map[string]any{"id": float64(2)})
		live.applySourceEvent(source, "selected_alias", map[string]any{"id": float64(1)})
		want = "native:pause"
	case "phase":
		live.retirePhase(source)
		var ok bool
		source, ok = live.beginPhase(glSource{run: source.run, stop: stop})
		if !ok {
			t.Fatal("fresh phase refused")
		}
		live.applySourceEvent(source, "playing", nil)
	case "new_run":
		close(stop)
		stop = make(chan struct{})
		run := live.beginRun(stop)
		var ok bool
		source, ok = live.beginPhase(run)
		if !ok {
			t.Fatal("fresh run refused")
		}
		live.applySourceEvent(source, "playing", nil)
	}
	if mode == "admitted_completion" {
		// An already admitted HTTP cannot be revoked. Its old completion must
		// not defeat the subsequent current paused decision.
		if action := <-commands; action != "native:play" {
			t.Fatalf("initial admitted effect: %s", action)
		}
	}
	unblock()
	select {
	case action := <-commands:
		if action != want {
			t.Fatalf("PC10 causal stale dispatch after %s: %s; native reads=%d", mode, action, playerReads.Load())
		}
		if playerReads.Load() < 2 {
			t.Fatal("fresh transport command reused the held old native read")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("current intent did not reach fresh actual bridge dispatch")
	}
}

func TestPC10TransportAdmissionPreservesCanonicalOnStale(t *testing.T) {
	for _, change := range []string{"wire", "poll_aba", "alias_aba", "phase", "stop", "volume", "equal_poll"} {
		t.Run(change, func(t *testing.T) {
			live := &glLive{}
			source, stop := pc11Source(t, live)
			defer func() {
				if !sourceStopped(stop) {
					close(stop)
				}
			}()
			initial := glStatus{Active: true, Paused: true, SelAlias: 1}
			live.acceptStatus(pc11Request(t, live, source), initial)
			_, intent := live.transportSnapshot()
			canonical := transState{canon: -1, otTarget: -1}
			before := canonical
			next := canonical
			next.decide(false, false, true) // A no-command tick still mutates confirmation/state.
			valid := false
			switch change {
			case "wire":
				live.applySourceEvent(source, "paused", nil) // equal event still fences work
			case "poll_aba":
				playing := initial
				playing.Paused = false
				live.acceptStatus(pc11Request(t, live, source), playing)
				live.acceptStatus(pc11Request(t, live, source), initial)
			case "alias_aba":
				live.applySourceEvent(source, "selected_alias", map[string]any{"id": float64(2)})
				live.applySourceEvent(source, "selected_alias", map[string]any{"id": float64(1)})
			case "phase":
				live.retirePhase(source)
			case "stop":
				close(stop)
			case "volume":
				live.applySourceEvent(source, "volume", map[string]any{"value": float64(10)})
				valid = true
			case "equal_poll":
				live.acceptStatus(pc11Request(t, live, source), initial)
				valid = true
			}
			if got := live.admitTransport(intent, &canonical, next); got != valid {
				t.Fatalf("admission=%v want %v", got, valid)
			}
			if !valid && canonical != before {
				t.Fatalf("stale no-command tick committed %+v", canonical)
			}
			if valid && canonical != next {
				t.Fatal("current no-command decision not committed")
			}
		})
	}
}
