package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

type controlFixture struct {
	mu     sync.Mutex
	writes []string // "METHOD host/path body"
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
	f := &controlFixture{}
	http.DefaultTransport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		body := `{}`
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/status":
			body = `{"username":"u","volume":40,"volume_steps":100,"paused":false,"stopped":false,"selected_alias_id":3}`
		case r.Method == http.MethodGet && r.URL.Path == "/api/outputs":
			body = `{"outputs":[{"id":"101","name":"Køkken HomePod","selected":false},` +
				`{"id":"106","name":"Svends HomePod","selected":false},` +
				`{"id":"107","name":"Fridas HomePod","selected":true}]}`
		case r.Method != http.MethodGet:
			sent := ""
			if r.Body != nil {
				b, _ := io.ReadAll(r.Body)
				sent = string(b)
			}
			f.mu.Lock()
			f.writes = append(f.writes, strings.TrimSpace(r.Method+" "+r.URL.Host+r.URL.Path+" "+sent))
			f.mu.Unlock()
		}
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(body))}, nil
	})
	return f
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
