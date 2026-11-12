package main

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
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

// Latency: Green measured radio start at 4.3 s (requirement < 3 s). The radio
// player had no start_buffer_ms, so OwnTone used its 2250 ms default before
// audio started; Spotify's OwnTone already uses the buffer_ms option (500 ms).
func TestRadioPlayerUsesTheSameShortStartBufferAsSpotify(t *testing.T) {
	p := newRadioPlayer(radioPlayerConfig{Enabled: true, Root: t.TempDir()})
	conf := p.config()
	want := fmt.Sprintf("start_buffer_ms = %d", readBufferMs())
	if !strings.Contains(conf, want) {
		t.Fatalf("radio player config lacks %q (OwnTone falls back to 2250 ms):\n%s", want, conf)
	}
	if readBufferMs() >= 2250 {
		t.Fatalf("start buffer %d ms is not shorter than OwnTone's default", readBufferMs())
	}
}
