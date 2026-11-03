package main

// Actual public Native selected-content flow against its finite offline provider.
// The original opaque offer is forwarded unchanged; no source or receipt is built here.
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

func radioExerciseActualSelected(t *testing.T, ctx context.Context, producer *radioProducer, read func() radioState, target, nativeURL, peerMetricPath string) radioState {
	t.Helper()
	root := os.Getenv("PC_RADIO_CHAIN_FIXTURE_ROOT")
	if root == "" || peerMetricPath == "" {
		t.Fatal("selected flow needs private actual fixture diagnostics")
	}
	var ready struct {
		Scope map[string]string `json:"scope"`
	}
	if err := json.Unmarshal(radioChainPrivateFile(t, root, filepath.Join(root, "native-ready.json")), &ready); err != nil {
		t.Fatal(err)
	}
	if len(ready.Scope) != 4 || ready.Scope["room_id"] != target {
		t.Fatal("selected scope is not actual original Native scope")
	}
	query := url.Values{}
	for _, key := range []string{"account_user_id", "target_id", "room_id", "homepod_id"} {
		if ready.Scope[key] == "" {
			t.Fatal("missing actual selected scope")
		}
		query.Set(key, ready.Scope[key])
	}
	call := func(method, path string, body any, out any) {
		var input io.Reader
		if body != nil {
			raw, err := json.Marshal(body)
			if err != nil {
				t.Fatal(err)
			}
			input = bytes.NewReader(raw)
		}
		request, err := http.NewRequestWithContext(ctx, method, nativeURL+path, input)
		if err != nil {
			t.Fatal(err)
		}
		if body != nil {
			request.Header.Set("Content-Type", "application/json")
		}
		response, err := (&http.Client{Timeout: 12 * time.Second}).Do(request)
		if err != nil {
			t.Fatal("actual selected Native request failed", path, err)
		}
		defer response.Body.Close()
		if response.StatusCode != http.StatusOK {
			t.Fatal("actual selected Native request rejected", path, response.StatusCode)
		}
		if out != nil {
			if err := json.NewDecoder(io.LimitReader(response.Body, 65536)).Decode(out); err != nil {
				t.Fatal(err)
			}
		}
	}
	type receipt struct {
		Schema   string `json:"schema"`
		Offer    string `json:"offer_id"`
		Binding  string `json:"binding_id"`
		State    string `json:"state"`
		Accepted bool   `json:"accepted_selected_content"`
		Volume   int    `json:"start_volume_percent"`
		Room     string `json:"room_id"`
		Homepod  string `json:"homepod_id"`
		Physical bool   `json:"physical_result_verified"`
	}
	var controls struct {
		Schema   string          `json:"schema"`
		Ready    bool            `json:"ready"`
		Offer    json.RawMessage `json:"start_offer"`
		Physical bool            `json:"physical_result_verified"`
	}
	call(http.MethodGet, "/player/room_content/controls?"+query.Encode(), nil, &controls)
	if controls.Schema != "podconnect.native_room_content_controls.v1" || !controls.Ready || controls.Physical {
		t.Fatal("actual selected controls unavailable")
	}
	var offer struct {
		ID      string `json:"offer_id"`
		Binding string `json:"binding_id"`
		Volume  int    `json:"start_volume_percent"`
		Expiry  int    `json:"expires_after_ms"`
	}
	if err := json.Unmarshal(controls.Offer, &offer); err != nil || len(offer.ID) != 32 || len(offer.Binding) != 64 || offer.Volume != 35 || offer.Expiry <= 0 {
		t.Fatal("actual opaque selected offer malformed", err)
	}
	var prepared receipt
	call(http.MethodPost, "/player/room_content/prepare", map[string]any{"scope": ready.Scope, "start_offer": controls.Offer, "action": "play_track", "args": map[string]any{"track_uri": "spotify:track:0000000000000000000004", "max_volume": 35}}, &prepared)
	if prepared.Offer != offer.ID || prepared.Binding != offer.Binding {
		t.Fatal("prepare replaced original opaque selected offer")
	}
	ids := map[string]string{"offer_id": offer.ID, "binding_id": offer.Binding}
	resultPath := "/player/room_content/result?" + url.Values{"offer_id": {offer.ID}, "binding_id": {offer.Binding}}.Encode()
	until := time.Now().Add(3 * time.Second)
	staged := prepared
	for staged.State != "staged" {
		if staged.State != "staging" || time.Now().After(until) {
			t.Fatal("actual offline provider failed to stage selected content", staged.State)
		}
		select {
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-time.After(25 * time.Millisecond):
		}
		call(http.MethodGet, resultPath, nil, &staged)
		if staged.Offer != offer.ID || staged.Binding != offer.Binding {
			t.Fatal("staging changed opaque selected binding")
		}
	}
	var committed receipt
	call(http.MethodPost, "/player/room_content/commit", ids, &committed)
	if committed.Schema != "podconnect.native_room_content_commit.v1" || committed.State != "complete" || !committed.Accepted || committed.Offer != offer.ID || committed.Binding != offer.Binding || committed.Volume != 35 || committed.Room != target || committed.Homepod != ready.Scope["homepod_id"] || committed.Physical {
		t.Fatal("actual selected content not accepted exactly once", committed)
	}
	type peer struct {
		Schema   string `json:"schema"`
		Audio    uint64 `json:"audio_packets"`
		Item     uint64 `json:"source_item"`
		Selected bool   `json:"selected"`
		Session  bool   `json:"session_present"`
	}
	metric := func() peer {
		var value peer
		if err := json.Unmarshal(radioChainPrivateFile(t, root, peerMetricPath), &value); err != nil || value.Schema != "podconnect.inert_receiver_metrics.v1" {
			t.Fatal("actual selected receiver diagnostics invalid", err)
		}
		return value
	}
	// Establish a baseline only after the old C source is actually selected and
	// has a live session. A second observation then proves fresh selected RTP.
	until = time.Now().Add(5 * time.Second)
	var baseline peer
	for {
		baseline = metric()
		if baseline.Selected && baseline.Session && baseline.Item != 0 {
			break
		}
		if time.Now().After(until) {
			t.Fatal("actual selected receiver session missing", baseline)
		}
		select {
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-time.After(50 * time.Millisecond):
		}
	}
	until = time.Now().Add(5 * time.Second)
	for {
		after := metric()
		if after.Selected && after.Session && after.Item == baseline.Item && after.Audio > baseline.Audio+2 {
			break
		}
		if time.Now().After(until) {
			t.Fatal("actual selected source produced no fresh RTP", baseline, after)
		}
		select {
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-time.After(50 * time.Millisecond):
		}
	}
	observed := read()
	if observed.SourceKind != "none" || observed.State == "playing" {
		t.Fatal("selected return did not drain radio", observed)
	}
	producer.mu.Lock()
	_, held := producer.retained[target]
	producer.mu.Unlock()
	if held {
		t.Fatal("completed selected return retained stale radio custody")
	}
	t.Log("VERIFIED actual opaque controls/prepare/offline encrypted-FLAC stage/commit, authenticated radio preempt/drain, selected35 complete receipt and fresh selected receiver RTP; Native final assertion owns new URI/source/queue/writer proof")
	return observed
}
