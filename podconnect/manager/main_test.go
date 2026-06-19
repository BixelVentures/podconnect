package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestDecideVolume covers the bidirectional reconcile: initial sync, echo (no ping-pong),
// Spotify-side change, HomePod-side change, simultaneous (Spotify wins), and tolerance.
func TestDecideVolume(t *testing.T) {
	cases := []struct {
		name                       string
		canon, gl                  int
		glOk                       bool
		ot                         int
		otOk                       bool
		wantCanon                  int
		wantSetGl, wantSetOt       bool
	}{
		{"init from spotify pushes homepod", -1, 60, true, 8, true, 60, false, true},
		{"init from homepod when no session", -1, 0, false, 30, true, 30, false, false},
		{"steady echo: both equal canon -> no writes", 60, 60, true, 60, true, 60, false, false},
		{"rounding within tolerance -> no writes", 60, 61, true, 59, true, 60, false, false},
		{"spotify changed -> push homepod", 60, 80, true, 60, true, 80, false, true},
		{"homepod button changed -> push spotify", 60, 60, true, 35, true, 35, true, false},
		{"both changed -> spotify wins, push homepod", 60, 80, true, 20, true, 80, false, true},
		{"no session, no output -> nothing", 60, 0, false, 0, false, 60, false, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			gotCanon, gotGl, gotOt := decideVolume(c.canon, c.gl, c.glOk, c.ot, c.otOk)
			if gotCanon != c.wantCanon || gotGl != c.wantSetGl || gotOt != c.wantSetOt {
				t.Fatalf("decideVolume = (canon %d, setGl %v, setOt %v); want (%d, %v, %v)",
					gotCanon, gotGl, gotOt, c.wantCanon, c.wantSetGl, c.wantSetOt)
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
