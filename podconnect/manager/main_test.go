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

// TestTransState_NoFlickerOnPlay: pressing play must NOT bounce Spotify while OwnTone is still
// starting up (otPlaying=false for a few ticks). It should only command OwnTone to play, never
// command Spotify to pause.
func TestTransState_NoFlickerOnPlay(t *testing.T) {
	ts := transState{canon: -1, otTarget: -1}
	// Tick 1: Spotify playing, OwnTone not yet (startup).
	sg, so := ts.decide(true, false, true)
	if sg != -1 || so != 1 {
		t.Fatalf("tick1 got (sg %d, so %d) want (-1, 1)", sg, so)
	}
	// Ticks 2-5: OwnTone still starting — must keep NOT pausing Spotify (no flicker).
	for i := 0; i < 4; i++ {
		sg, _ = ts.decide(true, false, true)
		if sg != -1 {
			t.Fatalf("tick %d flickered Spotify (sg=%d)", i+2, sg)
		}
	}
	// OwnTone reaches play -> steady, no commands.
	if sg, so = ts.decide(true, true, true); sg != -1 || so != -1 {
		t.Fatalf("steady got (%d,%d) want (-1,-1)", sg, so)
	}
}

// TestTransState_HomePodTap: once OwnTone has confirmed our play, a top-tap pause must pause Spotify;
// a following top-tap play must resume Spotify (the "second tap" fix).
func TestTransState_HomePodTap(t *testing.T) {
	ts := transState{canon: -1, otTarget: -1}
	ts.decide(true, false, true) // play; commands OwnTone
	ts.decide(true, true, true)  // OwnTone confirms play
	// Top-tap pause: Spotify still playing, OwnTone now paused.
	if sg, _ := ts.decide(true, false, true); sg != 0 {
		t.Fatalf("top-tap pause: sg=%d want 0 (pause Spotify)", sg)
	}
	ts.decide(false, false, true) // Spotify now paused, settle
	// Top-tap play: Spotify paused, OwnTone now playing.
	if sg, _ := ts.decide(false, true, true); sg != 1 {
		t.Fatalf("top-tap play: sg=%d want 1 (resume Spotify)", sg)
	}
}

// TestTransState_RapidToggle: the forward path is never delayed, so each Spotify flip immediately
// commands OwnTone to follow (OwnTone is modelled as obeying our last command).
func TestTransState_RapidToggle(t *testing.T) {
	ts := transState{canon: -1, otTarget: -1}
	otPlaying := false
	_, so := ts.decide(true, otPlaying, true) // initial play
	if so >= 0 {
		otPlaying = so == 1 // OwnTone follows our command
	}
	for i, gl := range []bool{false, true, false, true} {
		_, so := ts.decide(gl, otPlaying, true)
		if so != b(gl) {
			t.Fatalf("toggle %d: so=%d want %d (OwnTone must follow Spotify each flip)", i, so, b(gl))
		}
		otPlaying = so == 1
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

	devs, up := fetchOutputsFrom(srv.URL)

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
