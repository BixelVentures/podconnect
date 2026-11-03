package main

// Explicit inert export of actual coordinator and HTTP JSON for bridge tests.
import (
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRadioExportBridgeContract(t *testing.T) {
	output := os.Getenv("PODVIEW_RADIO_CONTRACT_OUTPUT")
	if output == "" {
		t.Skip("explicit cross-language export only")
	}
	cases := map[string]radioState{"disabled": newRadioCoordinator(nil).snapshot()}
	fresh := func(c *radioCoordinator, state, source string) {
		applyRadioObservation(&c.state, radioNativeObservation{State: state, SourceKind: source, ObservedAt: time.Now().Unix(), ValidUntil: time.Now().Unix() + 30, OwnerGeneration: 1})
	}
	c, _ := radioTestOwner(t)
	cases["unobserved_idle"] = c.snapshot()
	fresh(c, "idle", "none")
	cases["idle"] = c.snapshot()
	c, _ = radioTestOwner(t)
	c.pending = true
	c.state.State = "preparing"
	cases["unobserved_preparing"] = c.snapshot()
	c, f := radioTestOwner(t)
	radioDo(t, c, "start-one", "start", "p3", "r1")
	cases["playing_no_volume"] = c.snapshot()
	v := 30
	c.state.VolumePercent = &v
	cases["playing_volume"] = c.snapshot()
	c.pending = true
	cases["busy"] = c.snapshot()
	c.pending = false
	radioDo(t, c, "stop-one", "stop", "", "")
	cases["stopped"] = c.snapshot()
	fresh(c, "playing", "spotify")
	c.state.TargetID = "r1"
	cases["spotify"] = c.snapshot()
	c, f = radioTestOwner(t)
	f.fail = "reserve::r1"
	_, err := c.execute(context.Background(), radioQ(c, "unknown-one", "start", "p3", "r1"))
	if err == nil {
		t.Fatal("unknown fixture never crossed effect boundary")
	}
	cases["unknown"] = c.snapshot()
	oldOwner, oldDir := radioOwner, dataDir
	defer func() { radioOwner = oldOwner; dataDir = oldDir }()
	dataDir = t.TempDir()
	if err := os.WriteFile(filepath.Join(dataDir, "options.json"), []byte(`{"attention_token":"contract-secret"}`), 0600); err != nil {
		t.Fatal(err)
	}
	receipts := map[string]any{}
	for _, name := range []string{"accepted", "unknown", "rejected"} {
		c, f = radioTestOwner(t)
		fresh(c, "idle", "none")
		q := radioQ(c, "contract-"+name, "start", "p3", "r1")
		if name == "unknown" {
			f.fail = "reserve::r1"
		}
		if name == "rejected" {
			q.ExpectedRevision = 0
		}
		radioOwner = c
		body, err := json.Marshal(q)
		if err != nil {
			t.Fatal(err)
		}
		request := httptest.NewRequest("POST", "/api/radio/action", strings.NewReader(string(body)))
		request.Header.Set("X-PodConnect-Token", "contract-secret")
		response := httptest.NewRecorder()
		radioActionHandler(response, request)
		receipts[name] = map[string]any{"status_code": response.Code, "body": json.RawMessage(response.Body.Bytes())}
	}
	// Relay one real bridge command through actual Go HTTP admission and result.
	c, base := radioTestOwner(t)
	fresh(c, "idle", "none")
	c.state.ProcessID = "contract-manager"
	recon := &radioReconFake{radioFake: base}
	c.backend = recon
	base.fail = "start:p3:r1"
	radioOwner = c
	initial := c.snapshot()
	command := os.Getenv("PODVIEW_RADIO_CONTRACT_COMMAND")
	if command == "" {
		encoded, e := json.Marshal(radioQ(c, "contract-scenario", "start", "p3", "r1"))
		if e != nil {
			t.Fatal(e)
		}
		command = string(encoded)
	}
	request := httptest.NewRequest("POST", "/api/radio/action", strings.NewReader(command))
	request.Header.Set("X-PodConnect-Token", "contract-secret")
	response := httptest.NewRecorder()
	radioActionHandler(response, request)
	if response.Code != 409 || c.snapshot().State != "unknown" {
		t.Fatal("scenario command not admitted to unknown", response.Code, response.Body.String())
	}
	unknownState := c.snapshot()
	recon.result = radioReconciliation{ActionID: c.operation.ActionID, Generation: c.fence.Generation, Token: c.fence.Token, Terminal: true, Outcome: "committed", Observation: radioNativeObservation{State: "playing", SourceKind: "radio", StationID: "p3", TargetID: "r1", OwnerGeneration: c.fence.OwnerGeneration, ObservedAt: time.Now().Unix(), ValidUntil: time.Now().Unix() + 25}}
	query := fmt.Sprintf("/api/radio/result?process_id=contract-manager&action_id=%s&generation=%d", c.operation.ActionID, c.fence.Generation)
	get := httptest.NewRequest("GET", query, nil)
	get.Header.Set("X-PodConnect-Token", "contract-secret")
	terminal := httptest.NewRecorder()
	radioResultHandler(terminal, get)
	if terminal.Code != 200 {
		t.Fatal(terminal.Code, terminal.Body.String())
	}
	c.refresh(context.Background())
	scenario := map[string]any{"initial": initial, "unknown": unknownState, "post_status": response.Code, "post_body": json.RawMessage(response.Body.Bytes()), "result_status": terminal.Code, "result_body": json.RawMessage(terminal.Body.Bytes()), "final": c.snapshot()}
	bytes, err := json.Marshal(map[string]any{"cases": cases, "receipts": receipts, "scenario": scenario})
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(output, bytes, 0600); err != nil {
		t.Fatal(err)
	}
}
