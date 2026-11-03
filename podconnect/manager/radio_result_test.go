package main

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"testing"
	"time"
)

func resultTestOwner(t *testing.T) (*radioCoordinator, *radioReconFake) {
	t.Helper()
	c, base := radioTestOwner(t)
	f := &radioReconFake{radioFake: base}
	c.backend = f
	base.fail = "start:p3:r1"
	state, err := c.execute(context.Background(), radioQ(c, "original", "start", "p3", "r1"))
	if err == nil || state.State != "unknown" {
		t.Fatal(state, err)
	}
	f.result = radioReconciliation{ActionID: "original", Generation: c.fence.Generation, Token: c.fence.Token, Terminal: true, Outcome: "committed", Observation: radioNativeObservation{State: "playing", SourceKind: "radio", StationID: "p3", TargetID: "r1", OwnerGeneration: c.fence.OwnerGeneration, ObservedAt: time.Now().Unix() - 1, ValidUntil: time.Now().Unix() + 25}}
	return c, f
}

func TestRadioResultReadIsCorrelatedAndNeverRepeatsEffects(t *testing.T) {
	c, f := resultTestOwner(t)
	before := c.snapshot()
	calls := append([]string{}, f.calls...)
	result, err := c.result(context.Background(), before.ProcessID, "original", c.fence.Generation)
	if err != nil || !result.Terminal || result.ActionID != "original" || result.Schema != "podconnect.radio-result.v1" {
		t.Fatal(result, err)
	}
	if !reflect.DeepEqual(c.snapshot(), before) || !reflect.DeepEqual(f.calls, calls) {
		t.Fatal("passive result changed state or repeated effects")
	}
	c.refresh(context.Background())
	if c.snapshot().State != "playing" || !reflect.DeepEqual(f.calls, calls) {
		t.Fatal("terminal reconciliation did not remain read only")
	}
	// Result remains readable after the ordinary state read retired the unknown.
	if _, err = c.result(context.Background(), before.ProcessID, "original", result.Generation); err != nil {
		t.Fatal(err)
	}
}

func TestRadioResultRejectsForeignStaleOrUnprovenCustody(t *testing.T) {
	for _, name := range []string{"manager", "action", "generation", "token", "nonterminal", "outcome", "owner", "target", "expired", "long-lease", "old-observation", "pending", "disabled"} {
		t.Run(name, func(t *testing.T) {
			c, f := resultTestOwner(t)
			process, action, generation := c.state.ProcessID, "original", c.fence.Generation
			switch name {
			case "manager":
				process = "replacement"
			case "action":
				action = "foreign"
			case "generation":
				generation++
			case "token":
				f.result.Token = "foreign"
			case "nonterminal":
				f.result.Terminal = false
			case "outcome":
				f.result.Outcome = "unknown"
			case "owner":
				f.result.Observation.OwnerGeneration++
			case "target":
				f.result.Observation.TargetID = "r2"
			case "expired":
				f.result.Observation.ValidUntil = time.Now().Unix()
			case "long-lease":
				f.result.Observation.ValidUntil = time.Now().Unix() + 90
			case "old-observation":
				f.result.Observation.ObservedAt = time.Now().Unix() - 90
			case "pending":
				c.pending = true
			case "disabled":
				c.state.Enabled = false
			}
			before := c.snapshot()
			calls := append([]string{}, f.calls...)
			if _, err := c.result(context.Background(), process, action, generation); err == nil {
				t.Fatal("unproven custody unlocked")
			}
			if !reflect.DeepEqual(c.snapshot(), before) || !reflect.DeepEqual(f.calls, calls) {
				t.Fatal("rejected read mutated owner")
			}
		})
	}
}

func TestRadioResultHTTPStrictReadOnlyAdmission(t *testing.T) {
	oldDir, oldOwner := dataDir, radioOwner
	defer func() { dataDir = oldDir; radioOwner = oldOwner }()
	dataDir = t.TempDir()
	if err := os.WriteFile(filepath.Join(dataDir, "options.json"), []byte(`{"attention_token":"secret"}`), 0600); err != nil {
		t.Fatal(err)
	}
	c, f := resultTestOwner(t)
	radioOwner = c
	query := url.Values{"process_id": {c.state.ProcessID}, "action_id": {"original"}, "generation": {strconv.FormatUint(c.fence.Generation, 10)}}.Encode()
	for _, tc := range []struct {
		method, query, token string
		code                 int
	}{{"GET", query, "secret", 200}, {"POST", query, "secret", 405}, {"GET", query, "", 401}, {"GET", query + "&extra=1", "secret", 400}, {"GET", query + "&generation=1", "secret", 400}, {"GET", "process_id=p&action_id=a&generation=01", "secret", 400}, {"GET", "process_id=p&action_id=a&generation=18446744073709551616", "secret", 400}, {"GET", "process_id=p&action_id=foreign&generation=1", "secret", 409}} {
		req := httptest.NewRequest(tc.method, "/api/radio/result?"+tc.query, nil)
		req.Header.Set("X-PodConnect-Token", tc.token)
		response := httptest.NewRecorder()
		radioResultHandler(response, req)
		if response.Code != tc.code || response.Header().Get("Cache-Control") != "no-store" {
			t.Fatal(tc, response.Code, response.Body.String())
		}
		if tc.code == 200 {
			var result radioActionResult
			if json.Unmarshal(response.Body.Bytes(), &result) != nil || !result.Terminal || result.Observation.TargetID != "r1" {
				t.Fatal(response.Body.String())
			}
		}
	}
	if c.snapshot().State != "unknown" || len(f.calls) != 4 {
		t.Fatal("HTTP result repeated effects", f.calls)
	}
}
