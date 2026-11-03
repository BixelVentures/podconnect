package main

import (
	"context"
	"encoding/json"
	"net/url"
	"os"
	"testing"
	"time"
)

// Explicit read-only diagnostic. No coordinator, sender, source action or
// factory is constructed. The independent Native fixture proves no effects.
func TestRadioNativeUnknownCustodyAvailabilityExternal(t *testing.T) {
	if os.Getenv("PC_RADIO_C44_STATE_RUN") != "1" {
		t.Skip("explicit isolated C44 GET-state diagnostic only")
	}
	raw, e := os.ReadFile(os.Getenv("PC_RADIO_C44_STATE_READY"))
	if e != nil {
		t.Fatal(e)
	}
	var ready struct {
		Schema  string `json:"schema"`
		URL     string `json:"native_url"`
		Process string `json:"process_id"`
		Target  string `json:"target_id"`
		Device  string `json:"homepod_id"`
		Mode    string `json:"mode"`
	}
	if _, e = custodyObject(raw, &ready, []string{"schema", "native_url", "process_id", "target_id", "homepod_id", "mode"}); e != nil || ready.Schema != "podconnect.native_radio_wire_fixture.v1" || ready.Mode != "idle-no-login" || ready.Device != "1" {
		t.Fatal("actual Native fixture identity", e)
	}
	u, e := url.Parse(ready.URL)
	if e != nil || u.Scheme != "http" || u.Hostname() != "127.0.0.1" {
		t.Fatal("inert loopback only")
	}
	native, e := newRadioProducerNativeClient(ready.URL, os.Getenv("PC_RADIO_C44_STATE_TOKEN"))
	if e != nil {
		t.Fatal(e)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	evidence, e := native.Evidence(ctx)
	if e != nil || evidence.NativeProcess != ready.Process {
		t.Fatal("actual strict Native state", e)
	}
	native.mu.Lock()
	state := native.state
	native.mu.Unlock()
	found := false
	for _, target := range state.Targets {
		if target.ID == ready.Target {
			found = true
			if target.HomePodID != ready.Device {
				t.Fatal("physical identity changed")
			}
			diagnostic, _ := json.Marshal(map[string]any{"scope": "actual GET NativeState only", "target_available": target.Availability, "selectable": target.Selectable, "missing": evidence.Missing, "reserve_invoked": false, "sender_started": false})
			if out := os.Getenv("PC_RADIO_C44_STATE_RESULT"); out != "" {
				if e = os.WriteFile(out, diagnostic, 0600); e != nil {
					t.Fatal(e)
				}
			}
			t.Log(string(diagnostic))
			if target.Availability != "unknown" || target.Selectable {
				t.Fatal("UNKNOWN historical C custody is incorrectly advertised as an available target")
			}
		}
	}
	if !found {
		t.Fatal("configured actual target missing")
	}
}
