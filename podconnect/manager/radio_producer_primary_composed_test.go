package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

type primaryComposedTransport struct {
	next  http.RoundTripper
	trace []string
}

type primaryComposedBody struct {
	io.Reader
	close func() error
}

func (b primaryComposedBody) Close() error { return b.close() }

func primaryComposedPhase(phase string) string {
	switch phase {
	case "reserve", "reserved", "prepare_replacement", "replacement_prepared", "replacement_rejected",
		"prepare_start", "prepared", "start", "playing", "stop", "released", "finish",
		"result", "pending", "rejected", "unknown":
		return phase
	default:
		return "unrecognized"
	}
}

func (tr *primaryComposedTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.Method != http.MethodPost || req.URL.Path != "/radio/primary" {
		return tr.next.RoundTrip(req)
	}
	var command radioPrimaryCommand
	if req.GetBody != nil {
		if body, err := req.GetBody(); err == nil {
			_ = json.NewDecoder(body).Decode(&command)
			body.Close()
		}
	}
	report := func(result string) {
		phase := primaryComposedPhase(command.Phase)
		if command.Phase == "result" {
			phase += "/" + primaryComposedPhase(command.ForPhase)
		}
		line := phase + " " + result
		if len(tr.trace) == 0 || tr.trace[len(tr.trace)-1] != line {
			tr.trace = append(tr.trace, line)
		}
	}
	resp, err := tr.next.RoundTrip(req)
	if err != nil {
		report("transport_error")
		return resp, err
	}
	if resp.StatusCode != http.StatusOK {
		report(fmt.Sprintf("HTTP=%d", resp.StatusCode))
		return resp, nil
	}
	body := resp.Body
	var observed bytes.Buffer
	resp.Body = primaryComposedBody{Reader: io.TeeReader(body, &observed), close: func() error {
		var receipt radioPrimaryReceipt
		if json.Unmarshal(observed.Bytes(), &receipt) != nil {
			report("HTTP=200 unreadable_receipt")
		} else {
			report(fmt.Sprintf("HTTP=200 reply=%s matches=%t observation=%t session=%t teardown=%t",
				primaryComposedPhase(receipt.Phase), command.matches(receipt), receipt.Observation != nil,
				receipt.Session != nil, receipt.TeardownCSeq > 0))
		}
		return body.Close()
	}}
	return resp, nil
}

func TestConfiguredPrimaryRadioComposed(t *testing.T) {
	nativeURL := os.Getenv("PC_PRIMARY_MANAGER_NATIVE_URL")
	if nativeURL == "" {
		t.Skip("requires the composed loopback Native and C fixture")
	}
	receiverURL := os.Getenv("PC_NATIVE_START_URL")
	for _, endpoint := range []string{nativeURL, receiverURL} {
		u, err := url.Parse(endpoint)
		if err != nil || u.Scheme != "http" || u.Hostname() != "127.0.0.1" || u.Port() == "" || u.User != nil {
			t.Fatal("expected a private loopback fixture")
		}
	}
	token := os.Getenv("PC_PRIMARY_HTTP_TOKEN")
	if !producerHex(token, 64) {
		t.Fatal("missing fixture token")
	}
	var err error
	dataDir, err = filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	options, err := json.Marshal(map[string]any{
		"radio_enabled": true, "radio_owner_mode": "builtin", "radio_containment": "worker",
		"radio_physical_accepted": false, "radio_native_url": nativeURL, "radio_transport_token": token,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dataDir, "options.json"), options, 0600); err != nil {
		t.Fatal(err)
	}
	// The fixture Native is already configured with this private worker identity.
	settings, err := json.Marshal(radioWorkerSettings{Schema: radioWorkerSettingsSchema,
		Token: token, Secret: strings.Repeat("a", 64), Admin: strings.Repeat("b", 64)})
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(radioWorkerSettingsPath(), settings, 0600); err != nil {
		t.Fatal(err)
	}
	lossMarker := os.Getenv("PC_NATIVE_PRIMARY_CONTROL_LOSS")
	if lossMarker != "" && !filepath.IsAbs(lossMarker) {
		t.Fatal("expected an absolute local receiver-loss trigger")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	radioOwner = configuredRadioOwner()
	if radioOwner.snapshot().Enabled {
		t.Fatal("builtin radio became ready without Native proof")
	}
	startConfiguredRadioBuiltin(ctx, radioOwner)
	call := func(method, path string, body []byte, handler http.HandlerFunc, dst any) {
		t.Helper()
		request := httptest.NewRequest(method, path, bytes.NewReader(body)).WithContext(ctx)
		request.Header.Set("X-PodConnect-Token", token)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if err := json.NewDecoder(response.Body).Decode(dst); err != nil || response.Code != http.StatusOK {
			t.Fatalf("Manager %s %s failed: HTTP %d, %v", method, path, response.Code, err)
		}
	}
	readState := func() radioState {
		var state radioState
		call(http.MethodGet, "/api/radio/state", nil, radioStateHandler, &state)
		return state
	}
	wait := func(done func() bool) {
		t.Helper()
		for !done() {
			if err := radioReadinessSleep(ctx, 20*time.Millisecond); err != nil {
				t.Fatal("configured Manager or authenticated receiver window did not become ready", err)
			}
		}
	}
	type receiverWindows struct {
		Spotify int    `json:"primary_before"`
		Radio   [3]int `json:"primary_radio_windows"`
		Return  int    `json:"primary_after"`
	}
	windows := func() receiverWindows {
		t.Helper()
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, receiverURL+"/fixture/rtp", nil)
		if err != nil {
			t.Fatal(err)
		}
		response, err := http.DefaultClient.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		var result receiverWindows
		if err := json.NewDecoder(response.Body).Decode(&result); err != nil || response.StatusCode != http.StatusOK {
			t.Fatalf("C receiver witness failed: HTTP %d, %v", response.StatusCode, err)
		}
		return result
	}
	var current radioState
	wait(func() bool {
		current = readState()
		return current.Enabled && current.SourceKind == "spotify" && slices.Contains(current.Actions, "start")
	})
	if current.TargetID == "" || current.PhysicalVerified || len(current.BlockedReasons) != 0 {
		t.Fatal("Manager did not publish an observed, inert Spotify target")
	}
	target := current.TargetID
	if initial := windows(); initial.Spotify < 1 || initial.Radio != [3]int{} || initial.Return != 0 {
		t.Fatalf("passive Manager readiness touched receiver output: %+v", initial)
	}
	// The configured bootstrap has published Enabled and has no further client
	// work. Tap this fixture's existing client only during the synchronous actions.
	radioOwner.mu.Lock()
	producer, ok := radioOwner.backend.(*radioProducer)
	radioOwner.mu.Unlock()
	if !ok {
		t.Fatal("configured Manager did not select the builtin producer")
	}
	backend, ok := producer.primary.(*radioPrimaryBackend)
	if !ok {
		t.Fatal("configured Manager did not negotiate the Native primary protocol")
	}
	client, ok := backend.radioPrimaryPort.(*radioProducerNativeClient)
	if !ok || client.http.client.Transport != nil {
		t.Fatal("configured Manager did not use the default Native HTTP transport")
	}
	trace := &primaryComposedTransport{next: http.DefaultTransport}
	client.http.client.Transport = trace
	for i, station := range []string{"p3", "p1", "p6-beat"} {
		// PodVoice sends its compact offer task ID as the public action identity.
		q := radioCommand{ActionID: strings.ReplaceAll(attentionUUID(), "-", ""), Action: "start", StationID: station, TargetID: target,
			ExpectedRevision: current.Revision, ExpectedProcessID: current.ProcessID,
			ExpectedGeneration: current.Generation, ExpectedOwnerGeneration: current.OwnerGeneration}
		body, err := json.Marshal(q)
		if err != nil {
			t.Fatal(err)
		}
		var receipt struct {
			Action string `json:"action_id"`
			Status string `json:"status"`
			Retry  bool   `json:"retry_allowed"`
		}
		trace.trace = nil
		request := httptest.NewRequest(http.MethodPost, "/api/radio/action", bytes.NewReader(body)).WithContext(ctx)
		request.Header.Set("X-PodConnect-Token", token)
		response := httptest.NewRecorder()
		radioActionHandler(response, request)
		if err := json.NewDecoder(response.Body).Decode(&receipt); err != nil || response.Code != http.StatusOK {
			for _, step := range trace.trace {
				t.Logf("Manager %s Native %s", station, step)
			}
			stage := "no_record"
			producer.mu.Lock()
			for _, record := range producer.records {
				if record.Fence.ActionID == q.ActionID {
					stage = record.Stage
					break
				}
			}
			producer.mu.Unlock()
			t.Fatalf("Manager %s action failed: HTTP=%d unknown=%t rejected=%t journal_stage=%s",
				station, response.Code, receipt.Status == "unknown", receipt.Status == "rejected", stage)
		}
		if receipt.Action != q.ActionID || receipt.Status != "accepted" || receipt.Retry {
			t.Fatalf("Manager did not accept the exact %s action", station)
		}
		if lossMarker != "" && i == 2 {
			for _, step := range trace.trace {
				if strings.HasPrefix(step, "stop ") || strings.HasPrefix(step, "result/stop ") {
					t.Fatalf("fresh P6 tried to stop the already isolated receiver: %s", step)
				}
			}
		}
		current = readState()
		if current.State != "playing" || current.SourceKind != "radio" || current.StationID != station ||
			current.TargetID != target || current.PhysicalVerified {
			t.Fatalf("Manager did not observe %s on the configured target", station)
		}
		currentSelectable, otherUnavailable := false, false
		for _, output := range current.Targets {
			if output.ID == target {
				currentSelectable = output.Selectable && output.Availability == "available"
			} else {
				otherUnavailable = !output.Selectable && output.Availability == "unavailable"
			}
		}
		if len(current.Targets) != 2 || !currentSelectable || !otherUnavailable || !slices.Contains(current.Actions, "start") {
			t.Fatalf("parked %s should retain current station selection when the other receiver is unavailable", station)
		}
		wait(func() bool {
			observed := windows()
			if observed.Return != 0 {
				t.Fatal("Spotify RTP returned without an explicit Native Resume")
			}
			return observed.Radio[i] > 0
		})
		t.Logf("verified configured Manager %s with authenticated receiver RTP", station)
		if lossMarker != "" && i == 1 {
			playing := current
			producer.mu.Lock()
			incumbent := producer.active
			producer.mu.Unlock()
			radioOwner.mu.Lock()
			fence := radioOwner.fence
			radioOwner.mu.Unlock()
			resultPath := fmt.Sprintf("/api/radio/result?process_id=%s&action_id=%s&generation=%d",
				playing.ProcessID, q.ActionID, fence.Generation)
			readResult := func() *httptest.ResponseRecorder {
				request := httptest.NewRequest(http.MethodGet, resultPath, nil).WithContext(ctx)
				request.Header.Set("X-PodConnect-Token", token)
				response := httptest.NewRecorder()
				radioResultHandler(response, request)
				return response
			}
			resultResp := readResult()
			var result radioActionResult
			if err := json.NewDecoder(resultResp.Body).Decode(&result); err != nil ||
				resultResp.Code != http.StatusOK || result.ActionID != q.ActionID || result.Outcome != "committed" {
				t.Fatal("historical result probe did not resolve the live P1 start before receiver loss")
			}
			trace.trace = nil
			if err := os.WriteFile(lossMarker, nil, 0600); err != nil {
				t.Fatal(err)
			}
			wait(func() bool {
				current = readState()
				return current.State == "stopped" && current.SourceKind == "none" &&
					current.TargetID == target && slices.Contains(current.Actions, "start")
			})
			producer.mu.Lock()
			retired := producer.records[incumbent]
			active := producer.active
			producer.mu.Unlock()
			if len(trace.trace) != 0 || active != "" || retired.Primary == nil || !retired.SourceDrained ||
				retired.Primary.Isolation == nil || retired.Primary.StopReceipt != nil ||
				retired.Primary.TeardownCSeq != 0 || retired.Primary.ReleaseActionID != "" ||
				current.OwnerGeneration != playing.OwnerGeneration || current.Generation <= playing.Generation {
				t.Fatal("passive loss recovery did not retain exact custody without a positive STOP")
			}
			if readResult().Code != http.StatusConflict || len(trace.trace) != 0 {
				t.Fatal("passive isolation changed the historical action result")
			}
			replayReq := httptest.NewRequest(http.MethodPost, "/api/radio/action", bytes.NewReader(body)).WithContext(ctx)
			replayReq.Header.Set("X-PodConnect-Token", token)
			replayResp := httptest.NewRecorder()
			radioActionHandler(replayResp, replayReq)
			if replayResp.Code != http.StatusConflict || len(trace.trace) != 0 {
				t.Fatal("old P1 action replayed after passive isolation")
			}
			t.Log("verified configured Manager observed exact receiver isolation without STOP or a new effect")
		}
	}
}
