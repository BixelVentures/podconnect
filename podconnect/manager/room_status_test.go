package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// After move_existing_session routes the r0 Spotify session to r7's HomePod,
// /api/rooms must name r7 as playing (with r7's own output volume), not r0.
// roomsRead serves /api/rooms against a fake engine status, OwnTone outputs and OwnTone
// player state, and returns the rows by room id.
func roomsRead(t *testing.T, glStatusBody, outputs, playerState string) map[string]roomInfo {
	t.Helper()
	oldDir, oldStore, oldTransport := dataDir, store, http.DefaultTransport
	dataDir = t.TempDir()
	store = newRoomStore()
	t.Cleanup(func() { dataDir, store, http.DefaultTransport = oldDir, oldStore, oldTransport })
	rooms := `{"next_idx":8,"rooms":[` +
		`{"id":"r0","idx":0,"name":"Køkkenalrum","homepod_name":"Køkken HomePod","homepod_id":"101","released":false},` +
		`{"id":"r6","idx":6,"name":"Svends","homepod_name":"Svends HomePod","homepod_id":"47226606268305","released":false},` +
		`{"id":"r7","idx":7,"name":"Fridas","homepod_name":"Fridas HomePod","homepod_id":"226915153478190","released":false}]}`
	if err := os.WriteFile(filepath.Join(dataDir, "rooms.json"), []byte(rooms), 0o644); err != nil {
		t.Fatal(err)
	}
	http.DefaultTransport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		body := `{}`
		switch {
		case r.URL.Host == "localhost:3678" && r.URL.Path == "/status":
			body = glStatusBody
		case r.URL.Host == "localhost:3689" && r.URL.Path == "/api/outputs":
			body = outputs
		case r.URL.Host == "localhost:3689" && r.URL.Path == "/api/player":
			body = `{"state":"` + playerState + `"}`
		case r.Method != http.MethodGet:
			t.Errorf("rooms read sent %s %s", r.Method, r.URL)
		default:
			return &http.Response{StatusCode: 404, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(""))}, nil
		}
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(body))}, nil
	})
	rec := httptest.NewRecorder()
	roomsStateHandler(rec, httptest.NewRequest(http.MethodGet, "/api/rooms", nil))
	var got struct {
		Rooms []roomInfo `json:"rooms"`
	}
	if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &got) != nil || len(got.Rooms) != 3 {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
	byID := map[string]roomInfo{}
	for _, r := range got.Rooms {
		byID[r.ID] = r
	}
	return byID
}

const (
	playingEngine = `{"username":"u","volume":40,"volume_steps":100,"paused":false,"stopped":false,` +
		`"selected_alias_id":1,"track":{"name":"Song","artist_names":["Artist"]}}`
	fridaSelected = `{"outputs":[` +
		`{"id":"101","name":"Køkken HomePod","type":"AirPlay 2","selected":false,"volume":30},` +
		`{"id":"47226606268305","name":"Svends HomePod","type":"AirPlay 2","selected":false,"volume":12},` +
		`{"id":"226915153478190","name":"Fridas HomePod","type":"AirPlay 2","selected":true,"volume":8}]}`
	noneSelected = `{"outputs":[` +
		`{"id":"101","name":"Køkken HomePod","type":"AirPlay 2","selected":false,"volume":30},` +
		`{"id":"47226606268305","name":"Svends HomePod","type":"AirPlay 2","selected":false,"volume":12},` +
		`{"id":"226915153478190","name":"Fridas HomePod","type":"AirPlay 2","selected":false,"volume":8}]}`
)

// After move_existing_session routes the r0 Spotify session to r7's HomePod,
// /api/rooms must name r7 as playing (with r7's own output volume), not r0.
func TestRoomsReportTheRoomThatActuallyOutputsAfterMove(t *testing.T) {
	byID := roomsRead(t, playingEngine, fridaSelected, "play")
	r7 := byID["r7"]
	if !r7.Playing || r7.Volume != 8 || r7.NowPlaying != "Artist — Song" || r7.Source != "spotify" || r7.State != "playing" {
		t.Fatalf("r7 should be the playing room: %+v", r7)
	}
	if byID["r0"].Playing || byID["r0"].NowPlaying != "" || byID["r0"].Volume != 30 || byID["r0"].State != "idle" {
		t.Fatalf("r0 must not report the moved session: %+v", byID["r0"])
	}
	if byID["r6"].Playing || byID["r6"].Volume != 12 {
		t.Fatalf("r6: %+v", byID["r6"])
	}
}

// Green 12 Nov: /api/rooms said r6 played while Svend's HomePod was silent. The
// engine playing is not enough: OwnTone must play to the room's selected HomePod.
func TestRoomIsNotPlayingWhileItsHomePodIsSilent(t *testing.T) {
	for name, tc := range map[string]struct{ outputs, player string }{
		"owntone stopped":    {fridaSelected, "stop"},
		"owntone paused":     {fridaSelected, "pause"},
		"no output selected": {noneSelected, "play"},
	} {
		t.Run(name, func(t *testing.T) {
			for id, row := range roomsRead(t, playingEngine, tc.outputs, tc.player) {
				if row.Playing {
					t.Fatalf("%s reports playing with a silent HomePod: %+v", id, row)
				}
			}
		})
	}
	byID := roomsRead(t, playingEngine, fridaSelected, "stop")
	if byID["r7"].Source != "spotify" || byID["r7"].State != "connecting" {
		t.Fatalf("Spotify plays but the HomePod is silent: %+v", byID["r7"])
	}
}

func TestPausedSpotifyRoomIsPausedNotPlaying(t *testing.T) {
	paused := strings.Replace(playingEngine, `"paused":false`, `"paused":true`, 1)
	r7 := roomsRead(t, paused, fridaSelected, "pause")["r7"]
	if r7.Playing || r7.Source != "spotify" || r7.State != "paused" || r7.NowPlaying != "Artist — Song" {
		t.Fatalf("paused room: %+v", r7)
	}
}

func TestRadioRoomReportsRadioAsItsSource(t *testing.T) {
	radioOwner.mu.Lock()
	old := radioOwner.state
	radioOwner.state.SourceKind, radioOwner.state.State, radioOwner.state.TargetID = "radio", "playing", "r6"
	radioOwner.mu.Unlock()
	t.Cleanup(func() {
		radioOwner.mu.Lock()
		radioOwner.state = old
		radioOwner.mu.Unlock()
	})
	stopped := `{"username":"u","stopped":true}`
	byID := roomsRead(t, stopped, noneSelected, "stop")
	if r6 := byID["r6"]; !r6.Playing || r6.Source != "radio" || r6.State != "playing" {
		t.Fatalf("r6 plays radio: %+v", r6)
	}
	if r0 := byID["r0"]; r0.Playing || r0.State != "idle" {
		t.Fatalf("r0: %+v", r0)
	}
}

func TestActiveRouteFallsBackToSelectedAlias(t *testing.T) {
	rooms := radioRooms()
	gl := glStatus{Active: true, SelAlias: 3}
	if got := activeRouteRoomID(rooms, nil, gl); got != "r7" {
		t.Fatalf("alias fallback %q", got)
	}
	devs := []device{{ID: "101", Name: "Køkken HomePod", Selected: true}, {ID: "107", Name: "Fridas HomePod", Selected: true}}
	if got := activeRouteRoomID(rooms, devs, gl); got != "r7" {
		t.Fatalf("ambiguous selection must prefer the engine's alias: %q", got)
	}
	devs[1].Selected = false
	if got := activeRouteRoomID(rooms, devs, gl); got != "r0" {
		t.Fatalf("selected output wins over a stale alias: %q", got)
	}
}
