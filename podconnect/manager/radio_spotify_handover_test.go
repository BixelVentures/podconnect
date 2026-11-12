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
	"testing"
	"time"
)

// Green 0.26.95: radio start on Køkkenalrum (Spotify's room) only paused
// Spotify. The radio's AirPlay connection then replaced Spotify's HomePod
// session underneath Spotify's OwnTone, Native's retirement was rejected
// (409 session_changed) and every later phone transfer, seek and pause failed
// with "alias registry unavailable or changed" until the add-on restarted.
// A radio start on Spotify's speaker must end the Spotify session (fresh,
// idle Spotify engine), so radio stop leaves Spotify Connect clean.
func TestRadioStartEndsSpotifySessionOnItsSpeaker(t *testing.T) {
	for _, tc := range []struct {
		name            string
		route           string
		playing, sess   bool
		target          string
		pauses, endings int
	}{
		{"playing on same speaker", "r0", true, true, "r0", 1, 1},
		{"paused on same speaker", "r0", false, true, "r0", 0, 1},
		{"spotify on another speaker", "r6", true, true, "r0", 0, 0},
		{"no spotify session", "r0", false, false, "r0", 0, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p, _, spotify := newTestRadio(t)
			spotify.route, spotify.playing, spotify.session = tc.route, tc.playing, tc.sess
			if err := radioStart(t, p, "a1", "p8-jazz", tc.target, nil); err != nil {
				t.Fatal(err)
			}
			if spotify.pauses != tc.pauses || spotify.ended != tc.endings {
				t.Fatalf("pauses=%d ended=%d, want %d/%d", spotify.pauses, spotify.ended, tc.pauses, tc.endings)
			}
		})
	}
}

// Radio start → stop → every Spotify command reaches Spotify, and a Spotify
// command during radio stops the radio first and still succeeds.
func TestRadioStartStopThenSpotifyCommandsSucceed(t *testing.T) {
	p, fake, spotify := newTestRadio(t)
	spotify.route, spotify.playing, spotify.session = "r0", true, true
	if err := radioStart(t, p, "a1", "p8-jazz", "r0", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := p.execute(context.Background(), radioCommand{ActionID: "a2", Action: "stop"}); err != nil {
		t.Fatal(err)
	}
	if len(fake.selected) != 0 || fake.state != "stop" {
		t.Fatalf("radio stop left the speaker held: %+v %s", fake.selected, fake.state)
	}
	for _, path := range []string{"/api/play?room=r0", "/api/stop?room=r0", "/api/volume?room=r0", "/api/room-transport/commit"} {
		ran := false
		spotifyTakesOver(func(http.ResponseWriter, *http.Request) { ran = true })(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{"room_id":"r0"}`)))
		if !ran {
			t.Fatalf("%s blocked after radio stop", path)
		}
	}
	// Radio playing → Spotify takes over and succeeds.
	if err := radioStart(t, p, "a3", "p3", "r0", nil); err != nil {
		t.Fatal(err)
	}
	ran := false
	spotifyTakesOver(func(http.ResponseWriter, *http.Request) { ran = p.playingOn("") })(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/api/room-transport/commit", strings.NewReader(`{"room_id":"r0"}`)))
	if ran {
		t.Fatal("Spotify command ran while radio still held the speaker")
	}
	if p.playingOn("") || len(fake.selected) != 0 {
		t.Fatal("Spotify takeover did not stop the radio")
	}
}

func TestNativeWedgeLinesAreRecognised(t *testing.T) {
	wedged := []string{
		`connect-recovery native reason=ownership_failure stage=room_handoff retained_start=true retained_move=false custody_unknown=true retirement_pending=true`,
		`retirement_prepare outcome=request_failed reason=unavailable`,
		`retirement_prepare outcome=skipped reason=custody_uncertain`,
		`failed loading: output admission custody_unknown: alias registry unavailable or changed`,
	}
	for _, line := range wedged {
		if !nativeWedgeLine(line) {
			t.Errorf("not recognised: %s", line)
		}
	}
	for _, line := range []string{
		`connect-recovery native reason=ownership_failure stage=room_handoff custody_unknown=false retirement_pending=false`,
		`retirement_prepare outcome=prepared reason=none`,
		`retirement_prepare outcome=skipped reason=no_matching_custody`,
	} {
		if nativeWedgeLine(line) {
			t.Errorf("false wedge: %s", line)
		}
	}
}

// A real child process standing in for Native logs the Green wedge line. The
// supervisor must replace only that process with a fresh one quickly (no 2 s
// backoff), once, without waiting for an add-on restart.
func TestNativeWedgeIsHealedByAFreshSpotifyEngine(t *testing.T) {
	if os.Getenv("PODCONNECT_FAKE_NATIVE") != "" {
		return
	}
	dir := t.TempDir()
	rt := &roomRuntime{room: &Room{ID: "wedge-fixture"}, stop: make(chan struct{})}
	var mu sync.Mutex
	starts := []time.Time{}
	build := func() *exec.Cmd {
		mu.Lock()
		n := len(starts)
		starts = append(starts, time.Now())
		mu.Unlock()
		cmd := exec.Command(os.Args[0], "-test.run=^TestFakeNativeChild$")
		cmd.Env = append(os.Environ(), "PODCONNECT_FAKE_NATIVE="+fmt.Sprint(n), "PODCONNECT_FAKE_NATIVE_DIR="+dir)
		return cmd
	}
	done := make(chan struct{})
	go func() { defer close(done); rt.keepAlive("go-librespot", &rt.glCmd, build) }()
	defer func() {
		rt.requestStop(context.Background())
		rt.mu.Lock()
		cmd := rt.glCmd
		rt.mu.Unlock()
		if cmd != nil && cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		<-done
	}()
	deadline := time.Now().Add(8 * time.Second)
	for {
		mu.Lock()
		n := len(starts)
		mu.Unlock()
		if n >= 2 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("wedged Native was not replaced")
		}
		time.Sleep(20 * time.Millisecond)
	}
	mu.Lock()
	gap := starts[1].Sub(starts[0])
	mu.Unlock()
	if gap > 1500*time.Millisecond {
		t.Fatalf("fresh Spotify engine took %v (2 s respawn backoff still applied)", gap)
	}
	time.Sleep(300 * time.Millisecond)
	mu.Lock()
	n := len(starts)
	mu.Unlock()
	if n != 2 {
		t.Fatalf("Native replaced %d times, want exactly once", n-1)
	}
}

func TestFakeNativeChild(t *testing.T) {
	role := os.Getenv("PODCONNECT_FAKE_NATIVE")
	if role == "" {
		return
	}
	if role == "0" {
		fmt.Println("connect-recovery native reason=ownership_failure stage=room_handoff retained_start=true retained_move=false custody_unknown=true retirement_pending=true")
		fmt.Println("connect-recovery native reason=transfer_failed")
	} else {
		fmt.Println("go-librespot fresh and idle")
	}
	time.Sleep(30 * time.Second)
}

// Latency: Green measured ~4.8 s radio start when Spotify played on the same
// HomePod (requirement < 3 s). The Spotify hand-over used to run before the
// radio's own start; now both run together, so the start costs the slower one.
func TestRadioStartDoesNotWaitForSpotifyPauseOnTopOfItsOwnStart(t *testing.T) {
	p, fake, spotify := newTestRadio(t)
	spotify.route, spotify.playing, spotify.session = "r0", true, true
	spotify.delay = 600 * time.Millisecond
	// The fake radio player needs ~600 ms before it reports "play".
	fake.mu.Lock()
	fake.neverPlay = true
	fake.mu.Unlock()
	go func() {
		time.Sleep(600 * time.Millisecond)
		fake.mu.Lock()
		fake.state = "play"
		fake.mu.Unlock()
	}()
	started := time.Now()
	if err := radioStart(t, p, "a1", "p8-jazz", "r0", nil); err != nil {
		t.Fatal(err)
	}
	took := time.Since(started)
	t.Logf("radio start with Spotify hand-over took %v (sequential would be ≥1.2 s)", took)
	if took >= 1100*time.Millisecond {
		t.Fatalf("radio start waited for Spotify pause and its own start in sequence: %v", took)
	}
	if spotify.pauses != 1 || spotify.ended != 1 {
		t.Fatalf("hand-over incomplete: pauses=%d ended=%d", spotify.pauses, spotify.ended)
	}
	if !p.playingOn("r0") {
		t.Fatal("radio not playing")
	}
}

// While a start takes the speaker, Spotify still reporting "playing" there
// (its pause is in flight) must not make the radio yield to it.
func TestBridgeDoesNotYieldRadioToSpotifyItIsHandingOver(t *testing.T) {
	p, _, _ := newTestRadio(t)
	p.mu.Lock()
	p.pending, p.starting = true, "r0"
	p.state.SourceKind, p.state.State = "radio", "playing"
	p.mu.Unlock()
	p.spotifyPlaying("r0")
	time.Sleep(50 * time.Millisecond)
	p.mu.Lock()
	still := p.state.State == "playing"
	p.mu.Unlock()
	if !still {
		t.Fatal("radio yielded to the Spotify playback it was pausing")
	}
}

// The replaced Native's own shutdown lines (its retirement is refused once the
// radio holds the HomePod) must not trigger a second replacement.
func TestFreshNativeShutdownLinesDoNotTriggerAnotherReplacement(t *testing.T) {
	rt := &roomRuntime{room: &Room{ID: "heal-fixture"}, stop: make(chan struct{})}
	defer rt.requestStop(context.Background())
	var mu sync.Mutex
	replaced := 0
	previous := restartNativeHook
	restartNativeHook = func(*roomRuntime) { mu.Lock(); replaced++; mu.Unlock(); time.Sleep(20 * time.Millisecond) }
	t.Cleanup(func() { restartNativeHook = previous })
	rt.freshNative("radio took the speaker")
	time.Sleep(60 * time.Millisecond)
	rt.healNativeWedge() // late "retirement_prepare outcome=request_failed" from the old process
	time.Sleep(60 * time.Millisecond)
	mu.Lock()
	defer mu.Unlock()
	if replaced != 1 {
		t.Fatalf("Native replaced %d times, want 1", replaced)
	}
}
