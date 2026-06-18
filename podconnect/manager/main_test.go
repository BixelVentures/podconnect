package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestFetchOutputsRealisticOwnTone reproduces the 0.2.0 bug: OwnTone serializes the 64-bit output
// "id" as a JSON number, which the old strict `ID string` decoder rejected — blanking every device.
// This feeds the exact shape OwnTone returns (numeric id, mixed types, auth flags) and asserts the
// AirPlay devices parse. The old parser would fail this; the 0.2.1 parser must pass it.
func TestFetchOutputsRealisticOwnTone(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"outputs":[
			{"id":47226606268305,"name":"Køkkenalrum HomePod","type":"AirPlay","selected":true,"needs_auth_key":false,"volume":35},
			{"id":"123456","name":"Frida's HomePod","type":"AirPlay","selected":false,"requires_auth":true},
			{"id":9,"name":"HTTP Stream","type":"stream","selected":false}
		]}`))
	}))
	defer srv.Close()

	owntone = srv.URL // override the package-level target at runtime
	devs, up := fetchOutputs()

	if !up {
		t.Fatal("expected owntone_up=true when OwnTone answers")
	}
	if len(devs) != 2 {
		t.Fatalf("expected 2 AirPlay devices (stream filtered out), got %d: %+v", len(devs), devs)
	}
	// Numeric id must survive as a string id usable in /api/outputs/set.
	if devs[0].ID != "47226606268305" || devs[0].Name != "Køkkenalrum HomePod" || !devs[0].Selected {
		t.Fatalf("device[0] wrong (numeric id case): %+v", devs[0])
	}
	// String id must also work, and requires_auth must surface as NeedsAuth.
	if devs[1].ID != "123456" || !devs[1].NeedsAuth {
		t.Fatalf("device[1] wrong (string id / auth case): %+v", devs[1])
	}
}
