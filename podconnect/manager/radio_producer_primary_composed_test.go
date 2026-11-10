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
	"reflect"
	"slices"
	"strings"
	"sync/atomic"
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

func configurePrimaryComposedManager(t *testing.T, directory, nativeURL, token string) {
	t.Helper()
	var err error
	dataDir, err = filepath.EvalSymlinks(directory)
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
}

type primaryComposedReceiverWindows struct {
	Spotify int    `json:"primary_before"`
	Radio   [3]int `json:"primary_radio_windows"`
	Return  int    `json:"primary_after"`
}

func readPrimaryComposedRTP(t *testing.T, ctx context.Context, client *http.Client, receiverURL string) primaryComposedReceiverWindows {
	t.Helper()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, receiverURL+"/fixture/rtp", nil)
	if err != nil {
		t.Fatal(err)
	}
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var result primaryComposedReceiverWindows
	if err := json.NewDecoder(response.Body).Decode(&result); err != nil || response.StatusCode != http.StatusOK {
		t.Fatalf("C receiver witness failed: HTTP %d, %v", response.StatusCode, err)
	}
	return result
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
	coldPrior := os.Getenv("PC_PRIMARY_MANAGER_COLD_PRIOR") == "1"
	directory := t.TempDir()
	if coldPrior {
		directory = os.Getenv("PC_PRIMARY_MANAGER_COLD_DATA")
		if !filepath.IsAbs(directory) {
			t.Fatal("missing absolute historical Manager data directory")
		}
	}
	configurePrimaryComposedManager(t, directory, nativeURL, token)
	lossMarker := os.Getenv("PC_NATIVE_PRIMARY_CONTROL_LOSS")
	if lossMarker != "" && !filepath.IsAbs(lossMarker) || coldPrior && lossMarker == "" {
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
	windows := func() primaryComposedReceiverWindows {
		return readPrimaryComposedRTP(t, ctx, http.DefaultClient, receiverURL)
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
	stations := []string{"p3", "p1", "p6-beat"}
	if coldPrior {
		stations = []string{"p1"}
	}
	for i, station := range stations {
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
		if (!coldPrior && (len(current.Targets) != 2 || !otherUnavailable)) || !currentSelectable || !slices.Contains(current.Actions, "start") {
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
		if coldPrior {
			_, records, err := openRadioProducerJournal(filepath.Join(dataDir, "radio-owner-journal"))
			if err != nil {
				t.Fatal(err)
			}
			producer.mu.Lock()
			incumbent := producer.active
			producer.mu.Unlock()
			old := records[incumbent]
			if !old.Terminal || old.Outcome != "committed" || old.SourceDrained || old.Fence.ActionID != q.ActionID ||
				old.Primary == nil || old.Primary.Session == nil || old.Primary.Station != "p1" {
				t.Fatal("old Manager did not persist committed P1")
			}
			if err := os.WriteFile(lossMarker, nil, 0600); err != nil {
				t.Fatal(err)
			}
			return
		}
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

// This is the second Manager process in the existing cold-detachment runner.
// The prior process must have created the journal through its real action path.
func TestConfiguredPrimaryRadioColdDetachmentComposed(t *testing.T) {
	directory := os.Getenv("PC_PRIMARY_MANAGER_COLD_DATA")
	if directory == "" {
		t.Skip("requires the composed cold Native and C fixture")
	}
	nativeURL, token := os.Getenv("PC_PRIMARY_MANAGER_NATIVE_URL"), os.Getenv("PC_PRIMARY_HTTP_TOKEN")
	receiverURL := os.Getenv("PC_NATIVE_START_URL")
	for _, address := range []string{nativeURL, receiverURL} {
		endpoint, err := url.Parse(address)
		if err != nil || endpoint.Scheme != "http" || endpoint.Hostname() != "127.0.0.1" || endpoint.Port() == "" || endpoint.User != nil {
			t.Fatal("expected private loopback Native and C endpoints")
		}
	}
	if !filepath.IsAbs(directory) || !producerHex(token, 64) {
		t.Fatal("expected private historical Manager data and authenticated loopback Native")
	}
	configurePrimaryComposedManager(t, directory, nativeURL, token)
	journalDir := filepath.Join(dataDir, "radio-owner-journal")
	_, history, err := openRadioProducerJournal(journalDir)
	if err != nil || len(history) != 1 {
		t.Fatalf("old Manager must have persisted exactly one action: %v", err)
	}
	var old radioProducerRecord
	for _, old = range history {
	}
	if !old.Terminal || old.Outcome != "committed" || old.SourceDrained ||
		old.Primary == nil || old.Primary.Station != "p1" || old.Primary.Session == nil || old.Primary.ColdDetachment != nil {
		t.Fatal("missing exact old committed P1 with open historical custody")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	originalTransport := http.DefaultTransport
	defer func() { http.DefaultTransport = originalTransport }()
	var reads atomic.Int32
	var freshAction atomic.Bool
	trace := &primaryComposedTransport{next: originalTransport}
	http.DefaultTransport = startTransport(func(request *http.Request) (*http.Response, error) {
		if request.URL.Path != "/radio/primary" || request.Header.Get("X-PodConnect-Token") != token ||
			request.Method != http.MethodGet && !(freshAction.Load() && request.Method == http.MethodPost) {
			t.Error("cold Manager boot attempted an effect, legacy fallback or unauthenticated read")
			return nil, errRadioUnknown
		}
		if request.Method == http.MethodGet {
			reads.Add(1)
		}
		return trace.RoundTrip(request)
	})
	radioOwner = configuredRadioOwner()
	c := radioOwner
	startConfiguredRadioBuiltin(ctx, c)
	for !c.snapshot().Enabled {
		if err := radioReadinessSleep(ctx, 20*time.Millisecond); err != nil {
			t.Fatalf("restarted Manager never accepted authenticated cold proof: %s", c.snapshot().Error)
		}
	}
	current := c.snapshot()
	c.mu.Lock()
	producer, ok := c.backend.(*radioProducer)
	c.mu.Unlock()
	if !ok || reads.Load() < 2 || current.Generation != old.Fence.Generation || current.State == "playing" || current.PhysicalVerified {
		t.Fatal("restarted Manager adopted old authority or lost the generation floor")
	}
	producer.mu.Lock()
	stored := producer.records[old.Fence.Token]
	opened := producer.active != "" || len(producer.live) != 0 || producer.evidence.NativeProcess == old.Request.Process
	producer.mu.Unlock()
	if opened || stored.Primary == nil || stored.Primary.ColdDetachment == nil ||
		!stored.Primary.ColdDetachment.matchesRecord(old) {
		t.Fatal("restarted Manager did not archive the exact old session without adopting it")
	}
	expected := *old.Primary
	expected.ColdDetachment = stored.Primary.ColdDetachment
	old.Primary = &expected
	_, durable, err := openRadioProducerJournal(journalDir)
	if err != nil || len(durable) != 1 || !reflect.DeepEqual(durable[old.Fence.Token], old) {
		t.Fatal("cold detachment changed the original committed result or recorded another action")
	}
	if _, err := c.result(ctx, current.ProcessID, old.Fence.ActionID, old.Fence.Generation); err != errRadioUnknown {
		t.Fatal("the historical action became a live result in the new Manager")
	}
	t.Log("PASS restarted Manager archived exact committed P1 through authenticated Native readiness without STOP, replay or live adoption")
	if err := os.WriteFile(filepath.Join(directory, "reconciled"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	readState := func() radioState {
		req := httptest.NewRequest(http.MethodGet, "/api/radio/state", nil).WithContext(ctx)
		req.Header.Set("X-PodConnect-Token", token)
		resp := httptest.NewRecorder()
		radioStateHandler(resp, req)
		var state radioState
		if err := json.NewDecoder(resp.Body).Decode(&state); err != nil || resp.Code != http.StatusOK {
			t.Fatalf("restarted Manager state read failed: %v", err)
		}
		return state
	}
	wait := func(done func() bool) {
		t.Helper()
		for !done() {
			if err := radioReadinessSleep(ctx, 75*time.Millisecond); err != nil {
				t.Fatal("fresh Spotify observation or P6 RTP did not arrive", err)
			}
		}
	}
	wait(func() bool {
		_, err := os.Stat(filepath.Join(directory, "spotify-ready"))
		if err != nil && !os.IsNotExist(err) {
			t.Fatal(err)
		}
		return err == nil
	})
	wait(func() bool {
		current = readState()
		return current.Enabled && current.SourceKind == "spotify" && current.State == "playing" && current.TargetID == old.Request.Target &&
			slices.Contains(current.Actions, "start")
	})
	receiverClient := &http.Client{Transport: originalTransport}
	windows := func() primaryComposedReceiverWindows {
		return readPrimaryComposedRTP(t, ctx, receiverClient, receiverURL)
	}
	baseline := windows()
	if baseline.Spotify < 1 || baseline.Radio != [3]int{} {
		t.Fatalf("expected fresh Spotify RTP and no radio before explicit P6: %+v", baseline)
	}
	q := radioCommand{ActionID: attentionUUID(), Action: "start", StationID: "p6-beat", TargetID: old.Request.Target,
		ExpectedRevision: current.Revision, ExpectedProcessID: current.ProcessID, ExpectedGeneration: current.Generation,
		ExpectedOwnerGeneration: current.OwnerGeneration}
	body, err := json.Marshal(q)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/radio/action", bytes.NewReader(body)).WithContext(ctx)
	req.Header.Set("X-PodConnect-Token", token)
	resp := httptest.NewRecorder()
	freshAction.Store(true)
	radioActionHandler(resp, req)
	freshAction.Store(false)
	var receipt struct {
		Action string `json:"action_id"`
		Status string `json:"status"`
		Retry  bool   `json:"retry_allowed"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&receipt); err != nil || resp.Code != http.StatusOK ||
		receipt.Action != q.ActionID || receipt.Status != "accepted" || receipt.Retry {
		t.Fatalf("fresh P6 action through restarted Manager failed: HTTP %d, %v, trace=%v", resp.Code, err, trace.trace)
	}
	for _, step := range trace.trace {
		if strings.HasPrefix(step, "stop ") || strings.HasPrefix(step, "result/stop ") {
			t.Fatal("fresh P6 replayed an old STOP")
		}
	}
	current = readState()
	if current.StationID != "p6-beat" || current.State != "playing" || current.SourceKind != "radio" ||
		current.TargetID != old.Request.Target || current.PhysicalVerified {
		t.Fatal("restarted Manager did not observe exact fresh P6 on the expected target")
	}
	wait(func() bool { return windows().Radio[0] > 0 })
	t.Log("PASS restarted Manager observed fresh Spotify and then selected P6 with authenticated receiver RTP")
}
