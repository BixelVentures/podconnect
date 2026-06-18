package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

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
