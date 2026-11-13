package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
)

type controlFixture struct {
	mu     sync.Mutex
	writes []string // "METHOD host/path body"
	// The engine's and OwnTone's player state, as their real APIs report it.
	enginePaused bool
	engineRefuse map[string]bool // engine actions answered with HTTP 500, state unchanged
	ownTone      string          // OwnTone /api/player state: play, pause or stop
	// Green 13 Nov 11:41: OwnTone refuses the pause with 500 and keeps reporting
	// "play", and the engine reports its pause only after a few status reads.
	ownTonePauseRefused bool
	enginePauseLag      int // /status reads that still report playing after a pause
	engineStopped       bool
	// Each HomePod output's own volume, as OwnTone reports it: the room's start level source.
	volumes       map[string]int
	volumeRefused bool
}

// newControlFixture: Spotify plays on Frida's HomePod (r7) through the one engine.
func newControlFixture(t *testing.T) *controlFixture {
	t.Helper()
	oldDir, oldStore, oldTransport := dataDir, store, http.DefaultTransport
	dataDir = t.TempDir()
	store = newRoomStore()
	t.Cleanup(func() { dataDir, store, http.DefaultTransport = oldDir, oldStore, oldTransport })
	rooms := `{"next_idx":8,"rooms":[` +
		`{"id":"r0","idx":0,"name":"Køkkenalrum","homepod_name":"Køkken HomePod","homepod_id":"101"},` +
		`{"id":"r6","idx":6,"name":"Svends","homepod_name":"Svends HomePod","homepod_id":"106"},` +
		`{"id":"r7","idx":7,"name":"Fridas","homepod_name":"Fridas HomePod","homepod_id":"107"}]}`
	if err := os.WriteFile(filepath.Join(dataDir, "rooms.json"), []byte(rooms), 0o644); err != nil {
		t.Fatal(err)
	}
	f := &controlFixture{ownTone: "play", engineRefuse: map[string]bool{}, volumes: map[string]int{"101": 50, "106": 50, "107": 40}}
	http.DefaultTransport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		body, status := `{}`, 200
		f.mu.Lock()
		defer f.mu.Unlock()
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/status":
			reported := f.enginePaused
			if reported && f.enginePauseLag > 0 {
				f.enginePauseLag--
				reported = false
			}
			body = fmt.Sprintf(`{"username":"u","volume":40,"volume_steps":100,"paused":%t,"stopped":%t,`+
				`"selected_alias_id":3}`, reported, f.engineStopped)
		case r.Method == http.MethodGet && r.URL.Path == "/api/player":
			body = `{"state":"` + f.ownTone + `"}`
		case r.Method == http.MethodGet && r.URL.Path == "/api/outputs":
			body = fmt.Sprintf(`{"outputs":[{"id":"101","name":"Køkken HomePod","type":"AirPlay 2","selected":false,"volume":%d},`+
				`{"id":"106","name":"Svends HomePod","type":"AirPlay 2","selected":false,"volume":%d},`+
				`{"id":"107","name":"Fridas HomePod","type":"AirPlay 2","selected":true,"volume":%d}]}`,
				f.volumes["101"], f.volumes["106"], f.volumes["107"])
		case r.Method != http.MethodGet:
			sent := ""
			if r.Body != nil {
				b, _ := io.ReadAll(r.Body)
				sent = string(b)
			}
			target := r.URL.Host + r.URL.Path
			if r.URL.RawQuery != "" {
				target += "?" + r.URL.RawQuery
			}
			f.writes = append(f.writes, strings.TrimSpace(r.Method+" "+target+" "+sent))
			status = f.apply(r.Method, r.URL.Path)
			if r.Method == http.MethodPut && r.URL.Path == "/api/player/volume" {
				status = 204
				if f.volumeRefused {
					status = 500
				} else if v, err := strconv.Atoi(r.URL.Query().Get("volume")); err == nil {
					f.volumes[r.URL.Query().Get("output_id")] = v
				}
			}
		}
		return &http.Response{StatusCode: status, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(body))}, nil
	})
	return f
}

// apply mirrors the real players: the engine pauses/resumes unless told to refuse,
// and OwnTone answers 500 to a pause while stopped (player.c playback_pause).
func (f *controlFixture) apply(method, path string) int {
	switch {
	case method == http.MethodPost && (path == "/player/pause" || path == "/player/resume"):
		action := strings.TrimPrefix(path, "/player/")
		if f.engineRefuse[action] {
			return 500
		}
		f.enginePaused = action == "pause"
	case method == http.MethodPut && path == "/api/player/pause":
		if f.ownTone == "stop" || f.ownTonePauseRefused {
			return 500
		}
		f.ownTone = "pause"
	case method == http.MethodPut && path == "/api/player/play":
		f.ownTone = "play"
	}
	return 200
}

func (f *controlFixture) sent() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string{}, f.writes...)
}

func roomControl(t *testing.T, body string) (int, roomControlReply) {
	t.Helper()
	rec := httptest.NewRecorder()
	roomControlHandler(rec, httptest.NewRequest(http.MethodPost, "/api/room-control", strings.NewReader(body)))
	var reply roomControlReply
	if err := json.Unmarshal(rec.Body.Bytes(), &reply); err != nil {
		t.Fatalf("reply %d %q", rec.Code, rec.Body.String())
	}
	return rec.Code, reply
}

// Every Spotify control is one direct engine call: no prepare, lease or token.
func TestRoomControlSendsOneDirectCommandToTheEngine(t *testing.T) {
	for _, tc := range []struct{ body, want string }{
		{`{"room_id":"r7","action":"resume"}`, "POST localhost:3678/player/resume"},
		{`{"room_id":"r7","action":"next"}`, "POST localhost:3678/player/next"},
		{`{"room_id":"r7","action":"previous"}`, "POST localhost:3678/player/prev"},
		{`{"room_id":"r7","action":"seek","position_ms":2500}`, `POST localhost:3678/player/seek {"position":2500}`},
		{`{"room_id":"r7","action":"volume","volume":20}`, `POST localhost:3678/player/volume {"volume":20}`},
	} {
		f := newControlFixture(t)
		code, reply := roomControl(t, tc.body)
		if code != 200 || !reply.OK || reply.Source != "spotify" {
			t.Fatalf("%s: %d %+v", tc.body, code, reply)
		}
		if got := f.sent(); len(got) != 1 || got[0] != tc.want {
			t.Fatalf("%s sent %q, want %q", tc.body, got, tc.want)
		}
	}
}

// A pause also silences the HomePod, even if the engine is slow to follow.
func TestRoomControlPauseAlsoPausesTheHomePod(t *testing.T) {
	f := newControlFixture(t)
	if code, reply := roomControl(t, `{"room_id":"r7","action":"pause"}`); code != 200 || !reply.OK {
		t.Fatalf("%d %+v", code, reply)
	}
	want := []string{"POST localhost:3678/player/pause", "PUT localhost:3689/api/player/pause"}
	if got := f.sent(); strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("sent %q", got)
	}
}

// Testnoter 13. nov., fejl 2: the engine paused, but OwnTone had already stopped
// and answered 500, so the whole pause was reported as failed. The room is quiet:
// that is the confirmed outcome.
func TestRoomControlPauseIsConfirmedWhenTheHomePodAlreadyStopped(t *testing.T) {
	f := newControlFixture(t)
	f.ownTone = "stop"
	if code, reply := roomControl(t, `{"room_id":"r7","action":"pause"}`); code != 200 || !reply.OK {
		t.Fatalf("quiet room reported as failed pause: %d %+v", code, reply)
	}
	if !f.enginePaused {
		t.Fatal("engine not paused")
	}
}

// Green 13 Nov 11:41 (Speakers 0.26.99): the music stopped, but PodView showed a
// failure toast because OwnTone answered 500, still reported "play", and the engine
// confirmed its pause a moment later. The engine stops the sound: its pause decides.
func TestRoomControlPauseIsConfirmedWhenTheEnginePausesAfterOwnToneRefused(t *testing.T) {
	f := newControlFixture(t)
	f.ownTonePauseRefused, f.enginePauseLag = true, 2
	if code, reply := roomControl(t, `{"room_id":"r7","action":"pause"}`); code != 200 || !reply.OK {
		t.Fatalf("stopped music reported as failed pause: %d %+v", code, reply)
	}
}

// A pause is only confirmed when the engine really paused; otherwise the honest answer is failure.
func TestRoomControlPauseFailsWhenTheEngineKeepsPlaying(t *testing.T) {
	f := newControlFixture(t)
	f.engineRefuse["pause"] = true
	code, reply := roomControl(t, `{"room_id":"r7","action":"pause"}`)
	if code != http.StatusBadGateway || reply.OK || reply.Error != "speaker_unavailable" {
		t.Fatalf("unpaused engine reported as paused: %d %+v", code, reply)
	}
}

// Afspil after pause: the engine resumes and the paused HomePod plays again
// (OwnTone does not restart a paused pipe on its own).
func TestRoomControlResumeAlsoResumesAPausedHomePod(t *testing.T) {
	f := newControlFixture(t)
	f.enginePaused, f.ownTone = true, "pause"
	if code, reply := roomControl(t, `{"room_id":"r7","action":"resume"}`); code != 200 || !reply.OK {
		t.Fatalf("%d %+v", code, reply)
	}
	want := []string{"POST localhost:3678/player/resume", "PUT localhost:3689/api/player/play"}
	if got := f.sent(); strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("sent %q, want %q", got, want)
	}
	if f.enginePaused || f.ownTone != "play" {
		t.Fatalf("room not playing after resume: engine paused=%t owntone=%s", f.enginePaused, f.ownTone)
	}
}

// A resume the engine did not carry out is reported, not claimed.
func TestRoomControlResumeFailsWhenTheEngineStaysPaused(t *testing.T) {
	f := newControlFixture(t)
	f.enginePaused, f.engineRefuse["resume"] = true, true
	code, reply := roomControl(t, `{"room_id":"r7","action":"resume"}`)
	if code != http.StatusBadGateway || reply.OK {
		t.Fatalf("paused engine reported as resumed: %d %+v", code, reply)
	}
}

func TestRoomControlRejectsARoomThatIsNotPlaying(t *testing.T) {
	f := newControlFixture(t)
	code, reply := roomControl(t, `{"room_id":"r6","action":"pause"}`)
	if code != http.StatusConflict || reply.Error != "room_not_playing" || len(f.sent()) != 0 {
		t.Fatalf("%d %+v %q", code, reply, f.sent())
	}
	code, reply = roomControl(t, `{"room_id":"nope","action":"pause"}`)
	if code != http.StatusNotFound || reply.Error != "unknown_room" {
		t.Fatalf("%d %+v", code, reply)
	}
}

func TestRoomControlRejectsInvalidArguments(t *testing.T) {
	f := newControlFixture(t)
	for body, reason := range map[string]string{
		`{"room_id":"r7","action":"volume","volume":101}`: "invalid_arguments",
		`{"room_id":"r7","action":"seek"}`:                "invalid_arguments",
		`{"room_id":"r7","action":"pause","volume":3}`:    "invalid_arguments",
		`{"room_id":"r7","action":"shuffle"}`:             "unsupported_action",
		`not json`:                                        "invalid_request",
	} {
		if code, reply := roomControl(t, body); code != http.StatusBadRequest || reply.Error != reason {
			t.Fatalf("%s: %d %+v", body, code, reply)
		}
	}
	if len(f.sent()) != 0 {
		t.Fatalf("invalid requests sent %q", f.sent())
	}
}

// On a radio room the same call controls the radio; tracks do not exist there.
func TestRoomControlOnARadioRoomControlsTheRadio(t *testing.T) {
	f := newControlFixture(t)
	radioOwner.mu.Lock()
	old := radioOwner.state
	radioOwner.state.SourceKind, radioOwner.state.State, radioOwner.state.TargetID = "radio", "playing", "r6"
	radioOwner.mu.Unlock()
	t.Cleanup(func() {
		radioOwner.mu.Lock()
		radioOwner.state = old
		radioOwner.mu.Unlock()
	})
	if code, reply := roomControl(t, `{"room_id":"r6","action":"next"}`); code != http.StatusConflict ||
		reply.Error != "unsupported_for_radio" || reply.Source != "radio" {
		t.Fatalf("%d %+v", code, reply)
	}
	if code, reply := roomControl(t, `{"room_id":"r6","action":"resume"}`); code != 200 || reply.Source != "radio" {
		t.Fatalf("%d %+v", code, reply)
	}
	if len(f.sent()) != 0 {
		t.Fatalf("radio room sent Spotify commands %q", f.sent())
	}
}

// Green check 13 Nov 12:38: volume_set on a quiet Køkkenalrum failed with
// "r0: volume failed" (room_not_playing), so a sound test could not set 10 %
// before play. A quiet room always accepts its volume: it goes on its HomePod
// output, and that output volume is the room's start level.
func TestRoomControlVolumeOnAQuietRoomIsAcceptedAndSetOnItsHomePod(t *testing.T) {
	f := newControlFixture(t) // Spotify plays in Fridas (r7); Køkkenalrum (r0) is quiet
	code, reply := roomControl(t, `{"room_id":"r0","action":"volume","volume":8}`)
	if code != 200 || !reply.OK || reply.Source != "idle" {
		t.Fatalf("quiet room refused its volume: %d %+v", code, reply)
	}
	want := []string{"PUT localhost:3689/api/player/volume?output_id=101&volume=8"}
	if got := f.sent(); strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("sent %q, want %q (the playing engine must not be touched)", got, want)
	}
	if got := startLevelForRoom("r0"); got != 8 {
		t.Fatalf("Køkkenalrum start level %d, want 8", got)
	}
	if got := startLevelForRoom("r7"); got != 35 {
		t.Fatalf("Fridas (output 40 %%) start level %d, want the 35 %% ceiling", got)
	}
}

// Nothing plays anywhere: the volume is still accepted and seated.
func TestRoomControlVolumeIsAcceptedWhenNothingPlays(t *testing.T) {
	f := newControlFixture(t)
	f.engineStopped = true
	if code, reply := roomControl(t, `{"room_id":"r7","action":"volume","volume":5}`); code != 200 || !reply.OK {
		t.Fatalf("%d %+v", code, reply)
	}
	if got := f.sent(); len(got) != 1 || got[0] != "PUT localhost:3689/api/player/volume?output_id=107&volume=5" {
		t.Fatalf("sent %q", got)
	}
}

// A quiet volume OwnTone did not take is reported, not claimed.
func TestRoomControlQuietVolumeOwnToneRefusedIsAFailure(t *testing.T) {
	f := newControlFixture(t)
	f.volumeRefused = true
	code, reply := roomControl(t, `{"room_id":"r0","action":"volume","volume":8}`)
	if code != http.StatusBadGateway || reply.OK || reply.Error != "speaker_unavailable" {
		t.Fatalf("refused volume claimed: %d %+v", code, reply)
	}
}

// The start prime (bridge start, release reclaim, connect-recovery) seats the
// room's own level, never above 35 %: one source, the HomePod output volume.
func TestRoomStartLevelIsTheOutputVolumeButNeverAbove35(t *testing.T) {
	f := newControlFixture(t)
	roomControl(t, `{"room_id":"r0","action":"volume","volume":10}`)
	primeConfiguredStartVolume("http://localhost:3689", "101", false)
	if got := f.sent(); len(got) != 2 || got[1] != "PUT localhost:3689/api/player/volume?output_id=101&volume=10" {
		t.Fatalf("start prime sent %q, want the room's 10 %%", got)
	}
	primeConfiguredStartVolume("http://localhost:3689", "101", false, true)
	if got := f.sent(); len(got) != 3 || got[2] != "PUT localhost:3689/api/player/volume?output_id=101&recovery=connect&volume=10" {
		t.Fatalf("connect-recovery prime sent %q, want the room's 10 %%", got)
	}
	roomControl(t, `{"room_id":"r0","action":"volume","volume":80}`)
	if got := startLevelForRoom("r0"); got != 35 {
		t.Fatalf("start level %d after 80 %%, want the 35 %% ceiling", got)
	}
	if got := startLevelForOutput("unknown"); got != 35 {
		t.Fatalf("unreadable output start level %d, want the ceiling", got)
	}
}

// The other transport actions on a quiet room are still refused honestly.
func TestRoomControlQuietRoomStillRefusesTransport(t *testing.T) {
	f := newControlFixture(t)
	code, reply := roomControl(t, `{"room_id":"r0","action":"resume"}`)
	if code != http.StatusConflict || reply.Error != "room_not_playing" || len(f.sent()) != 0 {
		t.Fatalf("%d %+v %q", code, reply, f.sent())
	}
}
