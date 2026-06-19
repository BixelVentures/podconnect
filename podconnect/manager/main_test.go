package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestDecideVolume covers the bidirectional volume reconcile: init, echo (no ping-pong), rounding
// tolerance, Spotify-side change, HomePod-side change, ties, and the no-signal case.
func TestDecideVolume(t *testing.T) {
	cases := []struct {
		name                 string
		canon, gl            int
		glOk                 bool
		ot                   int
		otOk                 bool
		wantCanon            int
		wantSetGl, wantSetOt bool
	}{
		{"init from spotify pushes homepod", -1, 60, true, 8, true, 60, false, true},
		{"init from homepod when no session", -1, 0, false, 30, true, 30, false, false},
		{"steady echo -> no writes", 60, 60, true, 60, true, 60, false, false},
		{"rounding within tolerance -> no writes", 60, 61, true, 59, true, 60, false, false},
		{"spotify changed -> push homepod", 60, 80, true, 60, true, 80, false, true},
		{"homepod button changed -> push spotify", 60, 60, true, 35, true, 35, true, false},
		{"both changed -> spotify wins", 60, 80, true, 20, true, 80, false, true},
		{"no session, no output -> nothing", 60, 0, false, 0, false, 60, false, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			gc, gg, go_ := decideVolume(c.canon, c.gl, c.glOk, c.ot, c.otOk)
			if gc != c.wantCanon || gg != c.wantSetGl || go_ != c.wantSetOt {
				t.Fatalf("got (%d,%v,%v) want (%d,%v,%v)", gc, gg, go_, c.wantCanon, c.wantSetGl, c.wantSetOt)
			}
		})
	}
}

// TestDecideTransport covers the bidirectional play/pause reconcile — including the "HomePod top-tap
// resumes Spotify" case (the second-tap bug) and echo suppression.
func TestDecideTransport(t *testing.T) {
	cases := []struct {
		name                 string
		canon                int
		glPlaying, glOk      bool
		otPlaying, otOk      bool
		wantCanon            int
		wantSetGl, wantSetOt bool
	}{
		{"init playing -> play homepod", -1, true, true, false, true, 1, false, true},
		{"init from homepod when no session", -1, false, false, true, true, 1, false, false},
		{"steady both playing -> nothing", 1, true, true, true, true, 1, false, false},
		{"spotify paused -> pause homepod", 1, false, true, true, true, 0, false, true},
		{"homepod top-tap pause -> pause spotify", 1, true, true, false, true, 0, true, false},
		{"homepod top-tap play -> resume spotify", 0, false, true, true, true, 1, true, false},
		{"both flip to play -> agree, no writes", 0, true, true, true, true, 1, false, false},
		{"no session, no output -> nothing", 1, false, false, false, false, 1, false, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			gc, gg, go_ := decideTransport(c.canon, c.glPlaying, c.glOk, c.otPlaying, c.otOk)
			if gc != c.wantCanon || gg != c.wantSetGl || go_ != c.wantSetOt {
				t.Fatalf("got (%d,%v,%v) want (%d,%v,%v)", gc, gg, go_, c.wantCanon, c.wantSetGl, c.wantSetOt)
			}
		})
	}
}

// TestFetchOutputsRealisticOwnTone uses the EXACT shape OwnTone 29.2 returns for HomePods —
// captured from a real device. The key trap: type is "AirPlay 2" (not "AirPlay"), so an exact
// "airplay" match drops every device and blanks the picker. Also covers string id, numeric id
// (defensive), auth flags, and filtering out non-AirPlay outputs.
func TestFetchOutputsRealisticOwnTone(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"outputs":[
			{"id":"227114848637555","name":"Frida's HomePod","type":"AirPlay 2","selected":true,"needs_auth_key":false,"volume":2},
			{"id":"72873779386105","name":"MacBook Pro","type":"AirPlay 2","selected":false,"requires_auth":true,"volume":50},
			{"id":47226606268305,"name":"Numeric Id HomePod","type":"AirPlay","selected":false},
			{"id":"9","name":"HTTP Stream","type":"stream","selected":false}
		]}`))
	}))
	defer srv.Close()

	owntone = srv.URL // override the package-level target at runtime
	devs, up := fetchOutputs()

	if !up {
		t.Fatal("expected owntone_up=true when OwnTone answers")
	}
	if len(devs) != 3 {
		t.Fatalf("expected 3 AirPlay devices (stream filtered out), got %d: %+v", len(devs), devs)
	}
	// "AirPlay 2" type must be accepted (the bug that blanked the picker).
	if devs[0].ID != "227114848637555" || devs[0].Name != "Frida's HomePod" || !devs[0].Selected {
		t.Fatalf("device[0] wrong (AirPlay 2 case): %+v", devs[0])
	}
	// requires_auth must surface as NeedsAuth.
	if !devs[1].NeedsAuth {
		t.Fatalf("device[1] should need auth: %+v", devs[1])
	}
	// Numeric id must still survive as a string (defensive parsing).
	if devs[2].ID != "47226606268305" {
		t.Fatalf("device[2] numeric id not stringified: %+v", devs[2])
	}
}
