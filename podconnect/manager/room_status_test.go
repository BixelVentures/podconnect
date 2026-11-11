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
func TestRoomsReportTheRoomThatActuallyOutputsAfterMove(t *testing.T) {
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
	glStatusBody := `{"username":"u","volume":40,"volume_steps":100,"paused":false,"stopped":false,"selected_alias_id":1,"track":{"name":"Song","artist_names":["Artist"]}}`
	outputs := `{"outputs":[` +
		`{"id":"101","name":"Køkken HomePod","type":"AirPlay 2","selected":false,"volume":30},` +
		`{"id":"47226606268305","name":"Svends HomePod","type":"AirPlay 2","selected":false,"volume":12},` +
		`{"id":"226915153478190","name":"Fridas HomePod","type":"AirPlay 2","selected":true,"volume":8}]}`
	http.DefaultTransport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		body := `{}`
		switch {
		case r.URL.Host == "localhost:3678" && r.URL.Path == "/status":
			body = glStatusBody
		case r.URL.Host == "localhost:3689" && r.URL.Path == "/api/outputs":
			body = outputs
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
	if !byID["r7"].Playing || byID["r7"].Volume != 8 || byID["r7"].NowPlaying != "Artist — Song" {
		t.Fatalf("r7 should be the playing room: %+v", byID["r7"])
	}
	if byID["r0"].Playing || byID["r0"].NowPlaying != "" || byID["r0"].Volume != 30 {
		t.Fatalf("r0 must not report the moved session: %+v", byID["r0"])
	}
	if byID["r6"].Playing || byID["r6"].Volume != 12 {
		t.Fatalf("r6: %+v", byID["r6"])
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
