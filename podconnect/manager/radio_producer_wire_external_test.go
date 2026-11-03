package main

// Narrow opt-in Native+C HTTP composition. This deliberately DOES NOT construct
// a radioProducer, radioCoordinator, sender adapter, supervisor, or READY proof.
// It uses the real production strict HTTP transport/DTO and Native client with
// actual C HTTP handlers. Full manager/factory/cgroup acceptance stays NOT RUN.
//
// Optional MODE=idle-no-login requires the real no-login Native fixture and
// pins observed target availability using IDLE_AVAILABILITY=available|unknown
// (default unknown retains the earlier unsupported-metadata regression).
// Required PC_RADIO_WIRE_*: RUN=1, FIXTURE_ROOT, NATIVE_READY_FILE (actual Native
// diagnostic JSON), NATIVE_TOKEN, SENDER_URL, ADMIN_PASSWORD, CALLBACK_PORT.
// External fixtures own both C process lifecycles and Native finite provider;
// only a loopback receiver/provider edge may be synthetic. No hold or receipt
// can be supplied through an environment variable or file. On success the
// harness may consume native-c-wire-result.json and THEN release Native.
import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"sync/atomic"
	"testing"
	"time"
)

// Diagnostics preserve the exact response and expose keys/status only, never
// tokens, credentials, opaque source proofs, or request bodies.
type radioWireDiagnosticTransport struct{ t *testing.T }

func (d radioWireDiagnosticTransport) RoundTrip(q *http.Request) (*http.Response, error) {
	r, err := http.DefaultTransport.RoundTrip(q)
	if err != nil {
		d.t.Logf("actual wire %s transport failed", q.URL.Path)
		return r, err
	}
	raw, err := io.ReadAll(io.LimitReader(r.Body, 65537))
	r.Body.Close()
	r.Body = io.NopCloser(bytes.NewReader(raw))
	var body map[string]json.RawMessage
	keys := []string{}
	if json.Unmarshal(raw, &body) == nil {
		for k := range body {
			keys = append(keys, k)
		}
		sort.Strings(keys)
	}
	phase := ""
	if value := body["phase"]; value != nil {
		var candidate string
		_ = json.Unmarshal(value, &candidate)
		switch candidate {
		case "preparing", "prepared", "reserved", "bound", "starting", "playing", "stopping", "suspended", "drained", "cancelled", "unknown":
			phase = candidate
		}
	}
	d.t.Logf("actual wire %s status=%d phase=%s bytes=%d keys=%v read_error=%v", q.URL.Path, r.StatusCode, phase, len(raw), keys, err)
	return r, nil
}

func TestRadioNativeCProductionHTTPWireExternal(t *testing.T) {
	if os.Getenv("PC_RADIO_WIRE_RUN") != "1" {
		t.Skip("NOT RUN: explicit inert Native+C wire environment not supplied")
	}
	if runtime.GOOS != "linux" {
		t.Skip("NOT RUN: dedicated inert loopback Linux namespace required")
	}
	radioChainInertNamespace(t)
	mode := os.Getenv("PC_RADIO_WIRE_MODE")
	if mode != "" && mode != "idle-no-login" {
		t.Fatal("unsupported explicit wire fixture mode")
	}
	env := func(key string) string {
		t.Helper()
		value := os.Getenv("PC_RADIO_WIRE_" + key)
		if value == "" {
			t.Skip("NOT RUN: missing explicit wire prerequisite " + key)
		}
		return value
	}
	root := radioChainPath(t, env("FIXTURE_ROOT"))
	info, e := os.Stat(root)
	if e != nil || !info.IsDir() || info.Mode().Perm()&0066 != 0 {
		t.Fatal("wire fixture root must deny group/other listing and writing")
	}
	var ready struct {
		URL     string `json:"native_url"`
		Process string `json:"process_id"`
		Target  string `json:"target_id"`
		Device  string `json:"homepod_id"`
		Mode    string `json:"mode"`
	}
	if json.Unmarshal(radioChainPrivateFile(t, root, env("NATIVE_READY_FILE")), &ready) != nil || ready.Target == "" || !producerCounter(ready.Device, false) {
		t.Fatal("invalid actual Native diagnostic; it is not an owner grant")
	}
	if ready.Mode != mode {
		t.Fatal("actual Native fixture mode differs from explicit wire scope")
	}
	token, admin := env("NATIVE_TOKEN"), env("ADMIN_PASSWORD")
	native, e := newRadioProducerNativeClient(ready.URL, token)
	if e != nil {
		t.Fatal(e)
	}
	senderHTTP, e := newRadioProducerHTTP(env("SENDER_URL"), admin)
	if e != nil {
		t.Fatal(e)
	}
	senderHTTP.basicPassword = admin
	native.http.client.Transport = radioWireDiagnosticTransport{t}
	senderHTTP.client.Transport = radioWireDiagnosticTransport{t}
	callbackPort, e := strconv.Atoi(env("CALLBACK_PORT"))
	if e != nil || callbackPort < 1024 || callbackPort > 65535 {
		t.Fatal("invalid private callback port")
	}
	// The real unavailable handler is exposed, never a mock permit endpoint.
	// No callback belongs to this initial start/stop-only scope.
	radioOwner.mu.Lock()
	noOwner := radioOwner.backend == nil && !radioOwner.state.Enabled
	radioOwner.mu.Unlock()
	if !noOwner {
		t.Fatal("wire-only test must not run with an installed manager owner")
	}
	optionsRoot, e := os.MkdirTemp(root, "wire-client-")
	if e != nil {
		t.Fatal(e)
	}
	defer os.RemoveAll(optionsRoot)
	options, _ := json.Marshal(map[string]any{"radio_owner_mode": "builtin", "radio_native_token": token})
	if e = os.WriteFile(filepath.Join(optionsRoot, "options.json"), options, 0600); e != nil {
		t.Fatal(e)
	}
	previousDir := dataDir
	dataDir = optionsRoot
	defer func() { dataDir = previousDir }()
	var callbacks atomic.Int32
	mux := http.NewServeMux()
	mux.HandleFunc("/api/radio/native-preempt", func(w http.ResponseWriter, r *http.Request) { callbacks.Add(1); radioNativePreemptHandler(w, r) })
	listener, e := net.Listen("tcp", "127.0.0.1:"+strconv.Itoa(callbackPort))
	if e != nil {
		t.Fatal(e)
	}
	callbackServer := &http.Server{Handler: mux, ReadHeaderTimeout: 2 * time.Second}
	defer callbackServer.Close()
	go func() { _ = callbackServer.Serve(listener) }()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	evidence, e := native.Evidence(ctx)
	if e != nil || evidence.NativeProcess != ready.Process {
		t.Fatalf("actual Native evidence unavailable: %v", e)
	}
	idleAvailability := os.Getenv("PC_RADIO_WIRE_IDLE_AVAILABILITY")
	if idleAvailability == "" {
		idleAvailability = "unknown"
	}
	if idleAvailability != "unknown" && idleAvailability != "available" {
		t.Fatal("invalid idle availability expectation")
	}
	if mode == "idle-no-login" && idleAvailability == "unknown" {
		if len(evidence.Missing) != 1 || evidence.Missing[0] != "fresh_receiver_source_proof" {
			t.Fatalf("idle support must remain honestly unadvertised before acceptance: %v", evidence.Missing)
		}
	} else if len(evidence.Missing) != 0 {
		t.Fatalf("actual configured target evidence unavailable: %v", evidence.Missing)
	}
	native.mu.Lock()
	controller := native.state.ControllerID
	actualDevice := ""
	idleTargetHonest := mode != "idle-no-login"
	for _, target := range native.state.Targets {
		if target.ID == ready.Target {
			actualDevice = target.HomePodID
			if mode == "idle-no-login" {
				if idleAvailability == "available" {
					idleTargetHonest = target.Selectable && target.Availability == "available"
				} else {
					idleTargetHonest = !target.Selectable && target.Availability == "unknown"
				}
			}
		}
	}
	native.mu.Unlock()
	if actualDevice != ready.Device || !producerControllerID(controller) || !idleTargetHonest {
		t.Fatal("Native diagnostic does not match fresh actual configured receiver/controller")
	}
	var process, pid string
	call := func(action string, body map[string]any) radioProducerSenderReply {
		t.Helper()
		var r radioProducerSenderReply
		if e := senderHTTP.call(ctx, "/api/radio-sender/"+action, body, &r); e != nil {
			t.Fatalf("actual C %s unresolved: %v; effect is never retried", action, e)
		}
		at, atErr := time.Parse(time.RFC3339Nano, r.ObservedAt)
		until, untilErr := time.Parse(time.RFC3339Nano, r.ValidUntil)
		now := time.Now()
		if r.Schema != "podconnect.radio-sender.v1" || !r.OK || r.Physical || !producerUUID(r.Process) || !producerCounter(r.PID, false) || atErr != nil || untilErr != nil || at.After(now) || at.Before(now.Add(-30*time.Second)) || !until.After(now) || until.After(at.Add(30*time.Second)) {
			t.Fatal("actual C response identity/time contract invalid")
		}
		if process != "" && (process != r.Process || pid != r.PID) {
			t.Fatal("actual C process changed during operation")
		}
		process, pid = r.Process, r.PID
		if action != "capabilities" {
			if r.Device != body["device_id"] || r.Operation != body["operation_id"] || r.Generation != body["operation_generation"] || !producerHex(r.Token, 32) || !producerHex(r.Digest, 64) || !producerCounter(r.Epoch, false) || !producerCounter(r.SourceGeneration, false) {
				t.Fatal("C operation tuple/counters changed")
			}
			for _, binding := range []struct{ key, value string }{{"radio_process", r.Process}, {"radio_owner_epoch", r.Epoch}, {"sender_token", r.Token}} {
				if expected, exists := body[binding.key]; exists && expected != binding.value {
					t.Fatal("C source authority changed")
				}
			}
		}
		return r
	}
	caps := call("capabilities", map[string]any{"schema": "podconnect.radio-sender.v1"})
	if caps.ControllerID != controller || !caps.IdleVerified || !caps.PairProbeSupported {
		t.Fatal("actual independent C has not proven initial idle/probe support")
	}
	paired := false
	pairedDigest := ""
	for _, pair := range caps.Targets {
		if pair.Device == actualDevice && producerHex(pair.Digest, 64) {
			paired = true
			pairedDigest = pair.Digest
		}
	}
	if !paired {
		t.Fatal("actual C pairing does not include exact Native receiver")
	}
	// Capture explicit intent BEFORE actual decoder+PairVerify preflight.
	intent, e := native.CaptureIntent(ctx, ready.Target)
	if e != nil {
		t.Fatal(e)
	}
	op := radioProducerIdentity{Process: evidence.NativeProcess, Handoff: attentionUUID(), Generation: 1, ActionID: attentionUUID(), Target: ready.Target}
	prepare := producerSenderBase(actualDevice, op)
	prepare["station_id"] = "p3"
	prepared := call("preflight", prepare)
	state := producerSenderBase(actualDevice, op)
	state["radio_process"] = prepared.Process
	state["radio_owner_epoch"] = prepared.Epoch
	state["sender_token"] = prepared.Token
	wait := func(first radioProducerSenderReply, phase string) radioProducerSenderReply {
		t.Helper()
		deadline := time.Now().Add(5 * time.Second)
		for first.Phase != phase {
			if first.Phase == "unknown" || first.Phase == "cancelled" || time.Now().After(deadline) {
				t.Fatalf("actual C did not settle %s (phase %s); no effect retry", phase, first.Phase)
			}
			select {
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			case <-time.After(100 * time.Millisecond):
			}
			first = call("state", state)
		}
		return first
	}
	prepared = wait(prepared, "prepared")
	if !prepared.Prepared || prepared.Digest != pairedDigest || !producerTargetAuthenticated(prepared, controller, time.Now()) {
		t.Fatal("actual prepared decoder+PairVerify witness unavailable")
	}
	epoch, _ := strconv.ParseUint(prepared.Epoch, 10, 64)
	source, _ := strconv.ParseUint(prepared.SourceGeneration, 10, 64)
	stateProof, _ := json.Marshal(state)
	request := radioProducerReserve{radioProducerIdentity: op, ExpectedIntent: intent, Peer: radioProducerPeer{Operation: op, Process: prepared.Process, OwnerGeneration: epoch, SourceGeneration: source, Target: ready.Target, ReceiverDigest: prepared.Digest, CandidateID: op.Handoff, TargetAuth: prepared.TargetAuth, Proof: stateProof}}
	hold, e := native.Reserve(ctx, request)
	if e != nil {
		t.Fatal("actual Native reserve failed; no effect retry:", e)
	}
	if mode == "idle-no-login" {
		envelope, err := nativeProducerEnvelope(hold)
		if err != nil {
			t.Fatal(err)
		}
		var proof map[string]json.RawMessage
		var schema string
		var source struct {
			Kind string `json:"kind"`
		}
		if json.Unmarshal(envelope.Binding, &proof) != nil || json.Unmarshal(proof["schema"], &schema) != nil || schema != "podconnect.output_radio_absence.v1" || json.Unmarshal(proof["source_absence"], &source) != nil || source.Kind != "global" || proof["native_owner"] != nil || proof["writer_proof"] != nil {
			t.Fatal("idle native hold must be actual distinct global absence, without fabricated source/writer")
		}
	}
	body, e := senderProducerReserveBody(request, hold)
	if e != nil {
		t.Fatal(e)
	}
	bound := call("reserve", body)
	if bound.Phase != "bound" || !bound.Prepared || !producerSameTargetAuth(prepared.TargetAuth, bound.TargetAuth) || !producerTargetAuthenticated(bound, controller, time.Now()) || bound.SourceGeneration != prepared.SourceGeneration || bound.Digest != prepared.Digest {
		t.Fatal("actual C reserve replaced original prepared source/receiver")
	}
	beforePause := call("state", state)
	if beforePause.Phase != "bound" || !producerSameTargetAuth(prepared.TargetAuth, beforePause.TargetAuth) || !producerTargetAuthenticated(beforePause, controller, time.Now()) {
		t.Fatal("original exact authentication witness expired/replaced before Native pause")
	}
	if e = native.PauseSuspend(ctx, hold, op); e != nil {
		t.Fatal("actual pause/suspend unresolved; no replay:", e)
	}
	suspended, e := native.Suspended(ctx, hold)
	if e != nil {
		t.Fatal(e)
	}
	start := map[string]any{}
	for key, value := range body {
		start[key] = value
	}
	start["suspended_signature"] = suspended.SuspendedSignature
	playing := wait(call("start", start), "playing")
	if !playing.Observed || playing.Station != "p3" || playing.SourceGeneration != prepared.SourceGeneration || playing.Epoch != prepared.Epoch {
		t.Fatal("actual signed start/decoder/RTP source was not observed")
	}
	stopped := wait(call("stop", body), "drained")
	if stopped.Prepared || stopped.SourceGeneration != playing.SourceGeneration || stopped.Epoch != playing.Epoch {
		t.Fatal("actual retirement/source generation contract invalid")
	}
	retained, e := native.Result(ctx, request, hold)
	if e != nil || !retained.Terminal || retained.Outcome != "committed" || retained.Hold == nil || retained.Hold.Token != hold.Token {
		t.Fatal("actual Native hold failed to remain suspended after radio stop")
	}
	if callbacks.Load() != 0 {
		t.Fatal("initial-only scope attempted a Spotify return callback")
	}
	result, _ := json.Marshal(struct{ NativeProcess, Target, HomePod, RadioProcess, SourceGeneration, TargetEpoch, Phase string }{ready.Process, ready.Target, actualDevice, process, playing.SourceGeneration, playing.Epoch, stopped.Phase})
	output, e := os.OpenFile(filepath.Join(root, "native-c-wire-result.json"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = output.Write(result); e != nil {
		_ = output.Close()
		t.Fatal(e)
	}
	if e = output.Close(); e != nil {
		t.Fatal(e)
	}
	t.Logf("VERIFIED Native+C real HTTP mode=%s reserve/suspend/start/RTP/stop with actual receipts and preserved Native hold; NOT manager/sender-adapter/READY/cgroup/factory/preempt/physical acceptance", mode)
}
