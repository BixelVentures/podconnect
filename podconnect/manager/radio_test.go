package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeRadioPlayer is an OwnTone JSON API stand-in for the radio player.
type fakeRadioPlayer struct {
	mu        sync.Mutex
	state     string // "stop" | "play"
	selected  []string
	volumes   map[string]int
	requests  []string
	neverPlay bool
	queue     []string
}

func (f *fakeRadioPlayer) handler(t *testing.T) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		f.requests = append(f.requests, r.Method+" "+r.URL.Path)
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/player":
			_ = json.NewEncoder(w).Encode(map[string]any{"state": f.state, "volume": 50})
		case r.Method == http.MethodGet && r.URL.Path == "/api/outputs":
			outputs := []map[string]any{}
			for _, row := range []struct{ id, name string }{{"101", "Køkken HomePod"}, {"107", "Fridas HomePod"}} {
				selected := false
				for _, id := range f.selected {
					selected = selected || id == row.id
				}
				outputs = append(outputs, map[string]any{"id": row.id, "name": row.name, "type": "AirPlay 2", "selected": selected, "volume": f.volumes[row.id]})
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"outputs": outputs})
		case r.Method == http.MethodPut && r.URL.Path == "/api/outputs/set":
			var body struct {
				Outputs []string `json:"outputs"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			f.selected = body.Outputs
			w.WriteHeader(http.StatusNoContent)
		case r.Method == http.MethodPut && strings.HasPrefix(r.URL.Path, "/api/outputs/"):
			var body struct {
				Volume int `json:"volume"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			f.volumes[strings.TrimPrefix(r.URL.Path, "/api/outputs/")] = body.Volume
			w.WriteHeader(http.StatusNoContent)
		case r.Method == http.MethodPost && r.URL.Path == "/api/queue/items/add":
			f.queue = []string{r.URL.Query().Get("uris")}
			if r.URL.Query().Get("playback") == "start" && !f.neverPlay && len(f.selected) > 0 {
				f.state = "play"
			}
			_, _ = io.WriteString(w, `{"count":1}`)
		case r.Method == http.MethodPut && r.URL.Path == "/api/player/stop":
			f.state = "stop"
			w.WriteHeader(http.StatusNoContent)
		case r.Method == http.MethodPut && r.URL.Path == "/api/queue/clear":
			f.queue = nil
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Errorf("unexpected radio player request %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	})
}

func (f *fakeRadioPlayer) effects() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := []string{}
	for _, r := range f.requests {
		if !strings.HasPrefix(r, "GET ") {
			out = append(out, r)
		}
	}
	return out
}

type fakeSpotify struct {
	mu      sync.Mutex
	route   string
	playing bool
	pauses  int
	session bool
	ended   int
	delay   time.Duration // simulated Spotify pause round trip
}

func (s *fakeSpotify) routeState() (string, bool, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.route, s.playing, s.session || s.playing
}
func (s *fakeSpotify) endSession() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ended++
}

func (s *fakeSpotify) routeRoom() (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.route, s.playing
}
func (s *fakeSpotify) pause() error {
	time.Sleep(s.delay)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pauses++
	s.playing = false
	return nil
}

func radioRooms() []*Room {
	r0 := &Room{ID: "r0", Idx: 0, Name: "Køkkenalrum", HomepodName: "Køkken HomePod", HomepodID: "101"}
	r6 := &Room{ID: "r6", Idx: 6, Name: "Svends", HomepodName: "Svends HomePod", HomepodID: "106"}
	r7 := &Room{ID: "r7", Idx: 7, Name: "Fridas", HomepodName: "Fridas HomePod", HomepodID: "107"}
	return []*Room{r0, r6, r7}
}

func newTestRadio(t *testing.T) (*radioPlayer, *fakeRadioPlayer, *fakeSpotify) {
	t.Helper()
	fake := &fakeRadioPlayer{state: "stop", volumes: map[string]int{"101": 20, "107": 15}}
	server := httptest.NewServer(fake.handler(t))
	t.Cleanup(server.Close)
	p := newRadioPlayer(radioPlayerConfig{Enabled: true})
	p.base = server.URL
	p.rooms = radioRooms
	spotify := &fakeSpotify{route: "r0"}
	p.spotify = spotify
	previous := radioOwner
	radioOwner = p
	t.Cleanup(func() { radioOwner = previous })
	return p, fake, spotify
}

func radioStart(t *testing.T, p *radioPlayer, id, station, target string, volume *int) error {
	t.Helper()
	_, err := p.execute(context.Background(), radioCommand{ActionID: id, Action: "start", StationID: station, TargetID: target, VolumePercent: volume})
	return err
}

func TestRadioStartPlaysPlainStreamOnChosenSpeaker(t *testing.T) {
	p, fake, spotify := newTestRadio(t)
	if err := radioStart(t, p, "a1", "p3", "r7", nil); err != nil {
		t.Fatal(err)
	}
	s := p.snapshot()
	if s.State != "playing" || s.SourceKind != "radio" || s.TargetID != "r7" || s.StationID != "p3" || s.VolumePercent == nil || *s.VolumePercent != startVolumePercent {
		t.Fatalf("state %+v", s)
	}
	if len(fake.selected) != 1 || fake.selected[0] != "107" || fake.volumes["107"] != startVolumePercent || len(fake.queue) != 1 || fake.queue[0] != radioStreamURLs["p3"] {
		t.Fatalf("player %+v %+v %+v", fake.selected, fake.volumes, fake.queue)
	}
	if spotify.pauses != 0 {
		t.Fatal("Spotify in another room was paused")
	}
	if got := strings.Join(s.Actions, ","); got != "start,stop,move,volume" {
		t.Fatalf("actions %s", got)
	}
}

func TestRadioStartPausesSpotifyOnTheSameSpeaker(t *testing.T) {
	p, _, spotify := newTestRadio(t)
	spotify.route, spotify.playing = "r0", true
	if err := radioStart(t, p, "a1", "p1", "r0", nil); err != nil {
		t.Fatal(err)
	}
	if spotify.pauses != 1 {
		t.Fatal("radio did not take the speaker from Spotify")
	}
}

func TestRadioStartFailureLeavesNoHold(t *testing.T) {
	p, fake, _ := newTestRadio(t)
	fake.neverPlay = true
	old := radioStartWait
	t.Cleanup(func() { radioStartWait = old })
	radioStartWait = 300 * time.Millisecond
	if err := radioStart(t, p, "a1", "p3", "r7", nil); err == nil {
		t.Fatal("start without audio was accepted")
	}
	s := p.snapshot()
	if s.State != "stopped" || s.SourceKind != "none" || s.TargetID != "" {
		t.Fatalf("failed start left radio state %+v", s)
	}
	if len(fake.selected) != 0 || len(fake.queue) != 0 || fake.state != "stop" {
		t.Fatalf("failed start left a speaker held %+v %+v", fake.selected, fake.queue)
	}
	if p.playingOn("") || len(p.outputs) != 0 {
		t.Fatal("failed start left a hold")
	}
	// Spotify is never blocked afterwards.
	ran := false
	spotifyTakesOver(func(http.ResponseWriter, *http.Request) { ran = true })(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/api/stop?room=r7", nil))
	if !ran {
		t.Fatal("Spotify command blocked")
	}
}

func TestSpotifyCommandDuringRadioStopsRadioAndSucceeds(t *testing.T) {
	for _, tc := range []struct {
		name, path, body string
	}{
		{"pause by room query", "/api/stop?room=r7", ""},
		{"volume by body room", "/api/volume", `{"volume":8,"room":"r7"}`},
		{"room transport prepare scope", "/api/room-transport/prepare", `{"scope":{"room_id":"r7","target_id":"t","homepod_id":"107","account_user_id":"a"},"action":"pause"}`},
		{"move destination", "/api/room-move/prepare", `{"destination":{"room_id":"r7"}}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p, fake, _ := newTestRadio(t)
			if err := radioStart(t, p, "a1", "p3", "r7", nil); err != nil {
				t.Fatal(err)
			}
			var gotBody string
			handler := spotifyTakesOver(func(w http.ResponseWriter, r *http.Request) {
				raw, _ := io.ReadAll(r.Body)
				gotBody = string(raw)
				if p.playingOn("r7") {
					t.Error("Spotify command ran while radio still held the speaker")
				}
				w.WriteHeader(http.StatusOK)
			})
			rec := httptest.NewRecorder()
			handler(rec, httptest.NewRequest(http.MethodPost, tc.path, bytes.NewBufferString(tc.body)))
			if rec.Code != 200 || gotBody != tc.body {
				t.Fatalf("Spotify command did not run unchanged: %d %q", rec.Code, gotBody)
			}
			if s := p.snapshot(); s.State != "stopped" || s.SourceKind != "none" {
				t.Fatalf("radio still playing: %+v", s)
			}
			if len(fake.selected) != 0 || fake.state != "stop" {
				t.Fatal("radio player still holds the HomePod")
			}
		})
	}
}

func TestSpotifyCommandForAnotherSpeakerLeavesRadio(t *testing.T) {
	p, _, spotify := newTestRadio(t)
	spotify.route = "r0"
	if err := radioStart(t, p, "a1", "p3", "r7", nil); err != nil {
		t.Fatal(err)
	}
	spotifyTakesOver(func(http.ResponseWriter, *http.Request) {})(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/api/stop?room=r0", nil))
	if !p.playingOn("r7") {
		t.Fatal("pausing Spotify in another room stopped the radio")
	}
}

func TestSpotifyPlaybackOnRadioSpeakerStopsRadio(t *testing.T) {
	p, fake, _ := newTestRadio(t)
	if err := radioStart(t, p, "a1", "p3", "r7", nil); err != nil {
		t.Fatal(err)
	}
	p.spotifyPlaying("r6")
	if !p.playingOn("r7") {
		t.Fatal("Spotify elsewhere stopped radio")
	}
	p.spotifyPlaying("r7")
	deadline := time.Now().Add(2 * time.Second)
	for p.playingOn("r7") && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if p.playingOn("r7") || len(fake.effects()) == 0 || fake.state != "stop" {
		t.Fatal("Spotify playback did not take the speaker")
	}
}

func TestRadioStateReadIsSideEffectFree(t *testing.T) {
	p, fake, _ := newTestRadio(t)
	dir := t.TempDir()
	oldDir := dataDir
	dataDir = dir
	t.Cleanup(func() { dataDir = oldDir })
	if err := os.WriteFile(filepath.Join(dir, "options.json"), []byte(`{"radio_transport_token":"tok"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	var logs bytes.Buffer
	oldOut := log.Writer()
	log.SetOutput(&logs)
	t.Cleanup(func() { log.SetOutput(oldOut) })
	for i := 0; i < 5; i++ {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/api/radio/state", nil)
		req.Header.Set("X-PodConnect-Token", "tok")
		radioStateHandler(rec, req)
		if rec.Code != 200 {
			t.Fatal(rec.Code)
		}
		var s radioState
		if json.Unmarshal(rec.Body.Bytes(), &s) != nil || !s.Enabled || s.Schema != "podconnect.radio.v1" || len(s.BlockedReasons) != 0 {
			t.Fatalf("radio not enabled: %s", rec.Body.String())
		}
		p.watch(context.Background())
	}
	if effects := fake.effects(); len(effects) != 0 {
		t.Fatalf("state reads caused effects: %v", effects)
	}
	if logs.Len() != 0 {
		t.Fatalf("idle reads logged: %q", logs.String())
	}
}

func TestRadioActionHTTPContract(t *testing.T) {
	p, _, _ := newTestRadio(t)
	dir := t.TempDir()
	oldDir := dataDir
	dataDir = dir
	t.Cleanup(func() { dataDir = oldDir })
	if err := os.WriteFile(filepath.Join(dir, "options.json"), []byte(`{"attention_token":"legacy"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	post := func(body string) (int, map[string]any) {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/api/radio/action", strings.NewReader(body))
		req.Header.Set("X-PodConnect-Token", "legacy")
		radioActionHandler(rec, req)
		var out map[string]any
		_ = json.Unmarshal(rec.Body.Bytes(), &out)
		return rec.Code, out
	}
	// Stale expected_* values from an older read no longer reject: last command wins.
	code, out := post(`{"action_id":"x1","action":"start","station_id":"p3","target_id":"r7","expected_revision":1,"expected_process_id":"old","expected_owner_generation":0,"expected_generation":0}`)
	if code != 200 || out["status"] != "accepted" || out["schema"] != "podconnect.radio-action.v1" {
		t.Fatalf("start %d %v", code, out)
	}
	code, out = post(`{"action_id":"x1","action":"start","station_id":"p3","target_id":"r7","expected_revision":1,"expected_process_id":"old","expected_owner_generation":0,"expected_generation":0}`)
	if code != 200 || out["status"] != "accepted" {
		t.Fatalf("duplicate %d %v", code, out)
	}
	code, out = post(`{"action_id":"x2","action":"start","station_id":"nope","target_id":"r7","expected_revision":1,"expected_process_id":"","expected_owner_generation":0,"expected_generation":0}`)
	if code != 409 || out["status"] != "rejected" {
		t.Fatalf("invalid %d %v", code, out)
	}
	s := p.snapshot()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/radio/result?process_id="+s.ProcessID+"&action_id=x1&generation=1", nil)
	req.Header.Set("X-PodConnect-Token", "legacy")
	radioResultHandler(rec, req)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"outcome":"committed"`) || !strings.Contains(rec.Body.String(), `"terminal":true`) {
		t.Fatalf("result %d %s", rec.Code, rec.Body.String())
	}
	code, out = post(`{"action_id":"x3","action":"stop","expected_revision":0,"expected_process_id":"","expected_owner_generation":0,"expected_generation":0}`)
	if code != 200 || out["status"] != "accepted" || p.playingOn("") {
		t.Fatalf("stop %d %v", code, out)
	}
}

func TestRadioDisabledByOptionReportsUnavailable(t *testing.T) {
	p, fake, _ := newTestRadio(t)
	p.cfg.Enabled = false
	p.refresh(context.Background())
	s := p.snapshot()
	if s.Enabled || len(s.Actions) != 0 || len(s.BlockedReasons) != 1 || s.BlockedReasons[0] != "radio_disabled" {
		t.Fatalf("%+v", s)
	}
	if _, err := p.execute(context.Background(), radioCommand{ActionID: "a", Action: "start", StationID: "p3", TargetID: "r0"}); err == nil {
		t.Fatal("disabled radio started")
	}
	if len(fake.effects()) != 0 {
		t.Fatal("disabled radio touched the player")
	}
}

func TestRadioMoveAndVolumeStayOnOwnPlayer(t *testing.T) {
	p, fake, spotify := newTestRadio(t)
	if err := radioStart(t, p, "a1", "p8-jazz", "r0", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := p.execute(context.Background(), radioCommand{ActionID: "a2", Action: "move", TargetID: "r7"}); err != nil {
		t.Fatal(err)
	}
	if s := p.snapshot(); s.TargetID != "r7" || s.StationID != "p8-jazz" || len(fake.selected) != 1 || fake.selected[0] != "107" {
		t.Fatalf("move %+v %v", s, fake.selected)
	}
	five := 5
	if _, err := p.execute(context.Background(), radioCommand{ActionID: "a3", Action: "volume", VolumePercent: &five}); err != nil {
		t.Fatal(err)
	}
	if fake.volumes["107"] != 5 || *p.snapshot().VolumePercent != 5 {
		t.Fatal("volume not on radio output")
	}
	if spotify.pauses != 0 {
		t.Fatal("radio paused Spotify that was not playing on its speaker")
	}
}

func TestRadioStartsAtTheSharedStartVolume(t *testing.T) {
	p, fake, _ := newTestRadio(t)
	fake.volumes["107"] = 90 // the radio player's own remembered level
	if err := radioStart(t, p, "a1", "p3", "r7", nil); err != nil {
		t.Fatal(err)
	}
	if fake.volumes["107"] != startVolumePercent || startVolumePercent != 35 {
		t.Fatalf("radio started at %d", fake.volumes["107"])
	}
	// A start cannot carry its own level: the 35 % rule is the only start level.
	ten := 10
	if err := radioStart(t, p, "a2", "p1", "r0", &ten); err == nil {
		t.Fatal("start with its own volume was accepted")
	}
	// After an explicit volume change, a move is a new start and uses 35 % again.
	five := 5
	if _, err := p.execute(context.Background(), radioCommand{ActionID: "a3", Action: "volume", VolumePercent: &five}); err != nil {
		t.Fatal(err)
	}
	if _, err := p.execute(context.Background(), radioCommand{ActionID: "a4", Action: "move", TargetID: "r0"}); err != nil {
		t.Fatal(err)
	}
	if fake.volumes["101"] != startVolumePercent || *p.snapshot().VolumePercent != startVolumePercent {
		t.Fatalf("move started at %d", fake.volumes["101"])
	}
}
