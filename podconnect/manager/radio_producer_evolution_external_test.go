package main

// Opt-in extension of the actual configured-factory composition. The only
// substitutes are the finite provider and registered inert receiver edges.
// No Native/C source, capability, custody, completion or grant is manufactured.
import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func radioEvolutionMetrics(t *testing.T) radioMoveMetrics {
	t.Helper()
	var value radioMoveMetrics
	root, path := os.Getenv("PC_RADIO_CHAIN_FIXTURE_ROOT"), os.Getenv("PC_RADIO_CHAIN_PEER_METRICS")
	if root == "" || path == "" {
		t.Fatal("evolution requires actual private receiver diagnostics")
	}
	if err := json.Unmarshal(radioChainPrivateFile(t, root, path), &value); err != nil || value.Schema != "podconnect.inert_receiver_metrics.v1" || len(value.Targets) != 2 || value.Targets[0].Device == value.Targets[1].Device {
		t.Fatal("actual evolution receiver diagnostics invalid", err)
	}
	return value
}

// Invoked before the first Radio command: an actually empty Native worker must
// not be represented by a seeded, paused source in this scenario.
func radioAssertActualEvolutionBoot(t *testing.T, initial radioState) {
	t.Helper()
	if os.Getenv("PC_RADIO_CHAIN_EVOLUTION") != "1" {
		return
	}
	if !initial.Enabled || initial.SourceKind != "none" || initial.State != "idle" || len(initial.Targets) != 2 {
		t.Fatal("evolution factory did not boot honestly idle", initial)
	}
	observed := radioEvolutionMetrics(t)
	if observed.Item != 0 {
		t.Fatal("boot-empty fixture has a real seeded source", observed)
	}
	for _, row := range observed.Targets {
		if row.Audio != 0 || row.Selected || row.Session {
			t.Fatal("boot-empty fixture has selected/session/RTP effects", observed)
		}
	}
	t.Log("VERIFIED actual configured factory starts with empty Native source, no selected receiver/session and zero RTP; no seeded pause")
}

type radioEvolutionReceipt struct {
	Schema    string `json:"schema"`
	OK        bool   `json:"ok"`
	Operation string `json:"start_operation_id"`
	Offer     string `json:"offer_id"`
	Binding   string `json:"binding_id"`
	State     string `json:"state"`
	Accepted  bool   `json:"accepted_selected_content"`
	Volume    int    `json:"start_volume_percent"`
	Room      string `json:"room_id"`
	Homepod   string `json:"homepod_id"`
	Physical  bool   `json:"physical_result_verified"`
}

func radioExerciseActualEvolution(t *testing.T, ctx context.Context, producer *radioProducer, read func() radioState, target, nativeURL, peerMetricPath string) radioState {
	t.Helper()
	if os.Getenv("PC_RADIO_CHAIN_EVOLUTION") != "1" {
		return read()
	}
	if peerMetricPath != os.Getenv("PC_RADIO_CHAIN_PEER_METRICS") {
		t.Fatal("evolution metrics must use the same actual fixture")
	}
	playing := read()
	if playing.SourceKind != "radio" || playing.State != "playing" || playing.TargetID != target {
		t.Fatal("initial actual Radio A missing", playing)
	}
	var ready struct {
		Scope  map[string]string   `json:"scope"`
		Scopes []map[string]string `json:"scopes"`
	}
	root := os.Getenv("PC_RADIO_CHAIN_FIXTURE_ROOT")
	if err := json.Unmarshal(radioChainPrivateFile(t, root, filepath.Join(root, "native-ready.json")), &ready); err != nil || len(ready.Scopes) != 2 || ready.Scope["room_id"] != target {
		t.Fatal("actual evolution scope inventory invalid", err)
	}
	scopes := map[string]map[string]string{}
	for _, scope := range ready.Scopes {
		if len(scope) != 4 || scopes[scope["room_id"]] != nil {
			t.Fatal("duplicate or incomplete evolution scope")
		}
		for _, key := range []string{"account_user_id", "target_id", "room_id", "homepod_id"} {
			if scope[key] == "" {
				t.Fatal("missing actual scope field", key)
			}
		}
		scopes[scope["room_id"]] = scope
	}
	destination := ""
	for room, scope := range scopes {
		if room != target {
			destination = room
			if scope["homepod_id"] == ready.Scope["homepod_id"] {
				t.Fatal("evolution receiver identities collapsed")
			}
		}
	}
	if destination == "" {
		t.Fatal("second configured evolution receiver missing")
	}
	producer.mu.Lock()
	prior, held := producer.retained[target]
	original, recorded := producer.records[producer.active]
	producer.mu.Unlock()
	if !held || !recorded || original.Native.Handoff == "" || original.Native.Token != prior.Native.Token || original.Native.Handoff != prior.Native.Handoff {
		t.Fatal("actual global absence hold missing")
	}
	envelope, err := nativeProducerEnvelope(original.Native)
	if err != nil {
		t.Fatal(err)
	}
	var proof map[string]json.RawMessage
	var schema string
	var absence struct {
		Kind string `json:"kind"`
	}
	if json.Unmarshal(envelope.Binding, &proof) != nil || json.Unmarshal(proof["schema"], &schema) != nil || schema != "podconnect.output_radio_absence.v1" || json.Unmarshal(proof["source_absence"], &absence) != nil || absence.Kind != "global" || proof["native_owner"] != nil || proof["writer_proof"] != nil {
		t.Fatal("initial A was not admitted by actual global absence")
	}
	sender := producer.sender.(*radioProducerSenderClient).supervisor
	sender.mu.Lock()
	pid := sender.cmd.Process.Pid
	sender.mu.Unlock()
	call := func(method, path string, body any, out any) {
		t.Helper()
		var input io.Reader
		if body != nil {
			raw, e := json.Marshal(body)
			if e != nil {
				t.Fatal(e)
			}
			input = bytes.NewReader(raw)
		}
		request, e := http.NewRequestWithContext(ctx, method, nativeURL+path, input)
		if e != nil {
			t.Fatal(e)
		}
		if body != nil {
			request.Header.Set("Content-Type", "application/json")
		}
		response, e := (&http.Client{Timeout: 12 * time.Second}).Do(request)
		if e != nil {
			t.Fatal("actual evolution Native request unresolved; no retry", path, e)
		}
		defer response.Body.Close()
		raw, e := io.ReadAll(io.LimitReader(response.Body, 65537))
		if e != nil || len(raw) > 65536 {
			t.Fatal("actual evolution Native response invalid", e)
		}
		if response.StatusCode != http.StatusOK {
			t.Fatalf("actual evolution Native %s rejected (%d): %.4096s", path, response.StatusCode, raw)
		}
		if out != nil {
			if e = json.Unmarshal(raw, out); e != nil {
				t.Fatal("actual evolution Native response decode", path, e)
			}
		}
	}
	selectContent := func(room string) (radioEvolutionReceipt, string) {
		t.Helper()
		scope := scopes[room]
		query := url.Values{}
		for key, value := range scope {
			query.Set(key, value)
		}
		var controls struct {
			Schema   string          `json:"schema"`
			Ready    bool            `json:"ready"`
			Offer    json.RawMessage `json:"start_offer"`
			Physical bool            `json:"physical_result_verified"`
		}
		call(http.MethodGet, "/player/room_content/controls?"+query.Encode(), nil, &controls)
		if controls.Schema != "podconnect.native_room_content_controls.v1" || !controls.Ready || controls.Physical {
			t.Fatal("actual evolution controls unavailable", room)
		}
		var offer struct {
			ID      string `json:"offer_id"`
			Binding string `json:"binding_id"`
			Volume  int    `json:"start_volume_percent"`
			Expiry  int    `json:"expires_after_ms"`
		}
		if e := json.Unmarshal(controls.Offer, &offer); e != nil || len(offer.ID) != 32 || len(offer.Binding) != 64 || offer.Volume != 35 || offer.Expiry <= 0 {
			t.Fatal("actual evolution opaque offer invalid", e)
		}
		var staged radioEvolutionReceipt
		call(http.MethodPost, "/player/room_content/prepare", map[string]any{"scope": scope, "start_offer": controls.Offer, "action": "play_track", "args": map[string]any{"track_uri": "spotify:track:0000000000000000000004", "max_volume": 35}}, &staged)
		resultPath := "/player/room_content/result?" + url.Values{"offer_id": {offer.ID}, "binding_id": {offer.Binding}}.Encode()
		deadline := time.Now().Add(3 * time.Second)
		for {
			if staged.Offer != offer.ID || staged.Binding != offer.Binding {
				t.Fatal("evolution staging replaced opaque binding")
			}
			if staged.State == "staged" {
				break
			}
			if staged.State != "staging" || time.Now().After(deadline) {
				t.Fatal("actual evolution selected staging failed", room, staged.State)
			}
			select {
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			case <-time.After(25 * time.Millisecond):
			}
			call(http.MethodGet, resultPath, nil, &staged)
		}
		var committed radioEvolutionReceipt
		call(http.MethodPost, "/player/room_content/commit", map[string]string{"offer_id": offer.ID, "binding_id": offer.Binding}, &committed)
		if committed.Schema != "podconnect.native_room_content_commit.v1" || !committed.OK || committed.Operation == "" || committed.State != "complete" || !committed.Accepted || committed.Offer != offer.ID || committed.Binding != offer.Binding || committed.Volume != 35 || committed.Room != room || committed.Homepod != scope["homepod_id"] || committed.Physical {
			t.Fatal("actual evolution selected commit failed", room, committed)
		}
		return committed, resultPath
	}
	peer := func(value radioMoveMetrics, device string) radioMovePeerMetric {
		t.Helper()
		for _, row := range value.Targets {
			if row.Device == device {
				return row
			}
		}
		t.Fatal("evolution metric receiver missing", device)
		return radioMovePeerMetric{}
	}
	wait := func(label string, check func(radioMoveMetrics) bool) radioMoveMetrics {
		t.Helper()
		deadline := time.Now().Add(5 * time.Second)
		for {
			observed := radioEvolutionMetrics(t)
			if check(observed) {
				return observed
			}
			if time.Now().After(deadline) {
				t.Fatal(label, observed)
			}
			select {
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			case <-time.After(50 * time.Millisecond):
			}
		}
	}
	a, b := scopes[target]["homepod_id"], scopes[destination]["homepod_id"]
	before := radioEvolutionMetrics(t)
	if before.Item != 0 || peer(before, b).Audio != 0 || peer(before, b).Selected || peer(before, b).Session {
		t.Fatal("Native B already had source effects before explicit selection", before)
	}
	bReceipt, bResultPath := selectContent(destination)
	wait("actual Native B and Radio A did not produce independent fresh RTP", func(v radioMoveMetrics) bool {
		pa, pb := peer(v, a), peer(v, b)
		return v.Item != 0 && pa.Audio > peer(before, a).Audio+2 && pb.Audio > peer(before, b).Audio+2 && pb.Selected && pb.Session
	})
	current := read()
	sender.mu.Lock()
	samePID := sender.cmd != nil && sender.cmd.Process.Pid == pid
	sender.mu.Unlock()
	if !samePID || current.SourceKind != "radio" || current.State != "playing" || current.TargetID != target || current.StationID != playing.StationID || current.OwnerGeneration != playing.OwnerGeneration || current.Generation != playing.Generation {
		t.Fatal("disjoint Native B selection disturbed actual Radio A", current)
	}
	if playing.VolumePercent != nil && (current.VolumePercent == nil || *current.VolumePercent != *playing.VolumePercent) {
		t.Fatal("disjoint Native selection changed radio source volume")
	}
	evolved, e := producer.native.Result(ctx, original.Request, original.Native)
	if e != nil || evolved.Custody == nil || evolved.Custody.Phase != "source_evolved" || evolved.Custody.Proof.Continuity == nil || !producerJSONEqual(evolved.Custody.Source, envelope.Binding) {
		t.Fatal("actual Native/C global absence did not evolve into exact historical custody with fresh target continuity", e)
	}
	immutableEvolution, _ := json.Marshal(evolved.Custody.Proof.Evolution)
	again, e := producer.native.Result(ctx, original.Request, original.Native)
	if e != nil || again.Custody == nil {
		t.Fatal("actual evolved custody disappeared", e)
	}
	againEvolution, _ := json.Marshal(again.Custody.Proof.Evolution)
	if !producerJSONEqual(immutableEvolution, againEvolution) || !producerJSONEqual(again.Custody.Source, envelope.Binding) {
		t.Fatal("read-only evolved custody restamped immutable history")
	}
	t.Log("VERIFIED actual boot-empty global absence → explicit opaque selected Native B source while Radio A continues fresh RTP; immutable original hold/evolution and separately fresh target continuity")
	aReceipt, _ := selectContent(target)
	if aReceipt.Offer == bReceipt.Offer || aReceipt.Binding == bReceipt.Binding || aReceipt.Operation == bReceipt.Operation {
		t.Fatal("explicit selected A reused B selected-content authority")
	}
	observed := read()
	if observed.SourceKind != "none" || observed.State == "playing" || observed.PhysicalVerified {
		t.Fatal("selected A did not retire the exact Radio A source", observed)
	}
	producer.mu.Lock()
	_, stillHeld := producer.retained[target]
	producer.mu.Unlock()
	if stillHeld {
		t.Fatal("completed selected A retained stale evolved custody")
	}
	// Baseline only after actual radio retirement and settled loopback queues.
	// Packets delivered during the selected transaction cannot prove the new
	// Native A source continues to emit after the old radio source has drained.
	select {
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	case <-time.After(250 * time.Millisecond):
	}
	selectedBaseline := radioEvolutionMetrics(t)
	selectedA := peer(selectedBaseline, a)
	if selectedBaseline.Item == 0 || !selectedA.Selected || !selectedA.Session {
		t.Fatal("actual selected A session missing after radio retirement", selectedBaseline)
	}
	wait("actual selected A did not produce fresh post-retirement RTP", func(v radioMoveMetrics) bool {
		pa := peer(v, a)
		return v.Item == selectedBaseline.Item && pa.Selected && pa.Session && pa.Audio > selectedA.Audio+2
	})
	var historicalB radioEvolutionReceipt
	call(http.MethodGet, bResultPath, nil, &historicalB)
	if !historicalB.OK || historicalB.Operation != bReceipt.Operation || historicalB.Offer != bReceipt.Offer || historicalB.Binding != bReceipt.Binding || historicalB.State != "complete" || !historicalB.Accepted || historicalB.Volume != 35 || historicalB.Room != destination || historicalB.Homepod != b || historicalB.Physical {
		t.Fatal("new explicit A selection altered completed B selected-content audit", historicalB)
	}
	t.Log("VERIFIED actual evolved-absence A selected35 return consumes exact custody, drains Radio A, publishes fresh selected A RTP and preserves completed B offer audit; Native fixture separately verifies new canonical source/queue/writer")
	return observed
}
