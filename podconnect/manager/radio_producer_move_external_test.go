package main

// Opt-in extension of the actual inert production-factory composition. Metrics
// are observations from real UDP receivers, never target/source permissions.
import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"
)

type radioMovePeerMetric struct {
	Device   string `json:"device_id"`
	Audio    uint64 `json:"audio_packets"`
	Selected bool   `json:"selected"`
	Session  bool   `json:"session_present"`
}
type radioMoveMetrics struct {
	Schema  string                `json:"schema"`
	Item    uint64                `json:"source_item"`
	Targets []radioMovePeerMetric `json:"targets"`
}

func radioExerciseActualMove(t *testing.T, ctx context.Context, producer *radioProducer, read func() radioState, action func(radioState, string, string, string, ...int) string, target, nativeURL, peerMetricPath string) radioState {
	t.Helper()
	mode := os.Getenv("PC_RADIO_CHAIN_MOVE")
	playing := read()
	if mode == "" {
		return playing
	}
	if mode == "1" {
		mode = "roundtrip"
	}
	if mode != "roundtrip" && mode != "coexist" {
		t.Fatal("unknown actual Move scenario")
	}
	root := os.Getenv("PC_RADIO_CHAIN_FIXTURE_ROOT")
	if root == "" || peerMetricPath == "" {
		t.Fatal("actual Move needs private receiver diagnostics")
	}
	var ready struct {
		Scopes []struct {
			Room     string `json:"room_id"`
			Physical string `json:"homepod_id"`
		} `json:"scopes"`
	}
	if err := json.Unmarshal(radioChainPrivateFile(t, root, filepath.Join(root, "native-ready.json")), &ready); err != nil {
		t.Fatal(err)
	}
	physical := map[string]string{}
	for _, scope := range ready.Scopes {
		if scope.Room == "" || scope.Physical == "" || physical[scope.Room] != "" {
			t.Fatal("invalid actual Native scope map")
		}
		physical[scope.Room] = scope.Physical
	}
	destination := ""
	for _, candidate := range playing.Targets {
		if candidate.ID != target && candidate.Selectable && candidate.Availability == "available" && physical[candidate.ID] != "" {
			if destination != "" {
				t.Fatal("bounded fixture has more than two targets")
			}
			destination = candidate.ID
		}
	}
	if destination == "" || physical[target] == "" || physical[target] == physical[destination] {
		t.Fatal("actual second registered available receiver missing")
	}
	readMetrics := func() radioMoveMetrics {
		var value radioMoveMetrics
		if err := json.Unmarshal(radioChainPrivateFile(t, root, peerMetricPath), &value); err != nil || value.Schema != "podconnect.inert_receiver_metrics.v1" || len(value.Targets) != 2 {
			t.Fatal("actual two-peer diagnostics malformed", err)
		}
		if value.Targets[0].Device == value.Targets[1].Device {
			t.Fatal("receiver identities collapsed")
		}
		return value
	}
	peer := func(value radioMoveMetrics, device string) radioMovePeerMetric {
		for _, p := range value.Targets {
			if p.Device == device {
				return p
			}
		}
		t.Fatal("actual peer identity missing", device)
		return radioMovePeerMetric{}
	}
	wait := func(check func(radioMoveMetrics) bool) radioMoveMetrics {
		until := time.Now().Add(5 * time.Second)
		for {
			value := readMetrics()
			if check(value) {
				return value
			}
			if time.Now().After(until) {
				t.Fatalf("actual receiver observation did not complete: %+v", value)
			}
			select {
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			case <-time.After(50 * time.Millisecond):
			}
		}
	}
	if playing.VolumePercent == nil {
		t.Fatal("initial radio target lacks an actual acknowledged volume")
	}
	// Each room retains its own last ACK. First admission into a different room
	// uses that room's authenticated binding base, never the source-room level.
	lastVolume := map[string]int{target: *playing.VolumePercent}
	move := func(state radioState, to string) radioState {
		before := readMetrics()
		old := peer(before, physical[state.TargetID])
		next := peer(before, physical[to])
		_ = action(state, "move", "", to)
		current := read()
		if current.State != "playing" || current.SourceKind != "radio" || current.TargetID != to || current.StationID != state.StationID || current.OwnerGeneration == 0 || current.PhysicalVerified {
			t.Fatal("actual Move did not preserve radio station/source", current)
		}
		expectedVolume, visited := lastVolume[to]
		if !visited {
			producer.mu.Lock()
			active, exists := producer.records[producer.active]
			producer.mu.Unlock()
			var bound struct {
				Request struct {
					Target string `json:"target_id"`
				} `json:"request"`
				Binding struct {
					Initial *int `json:"initial_volume_percent"`
					Output  struct {
						Device string `json:"device_id"`
					} `json:"output_identity"`
				} `json:"binding"`
			}
			if !exists || active.Native.Target != to || json.Unmarshal(active.Native.Proof, &bound) != nil || bound.Request.Target != to || bound.Binding.Output.Device != physical[to] || bound.Binding.Initial == nil || *bound.Binding.Initial < 0 || *bound.Binding.Initial > 100 {
				t.Fatal("first destination lacks its actual authenticated target-volume binding")
			}
			expectedVolume = *bound.Binding.Initial
		}
		if current.VolumePercent == nil || *current.VolumePercent != expectedVolume {
			t.Fatal("actual Move lost the destination room's own acknowledged volume")
		}
		lastVolume[to] = *current.VolumePercent
		wait(func(v radioMoveMetrics) bool { return peer(v, physical[to]).Audio > next.Audio+2 })
		// Drain has completed before Move returns. Baseline after queued loopback
		// packets settle, then verify the old destination does not keep receiving.
		select {
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-time.After(250 * time.Millisecond):
		}
		settled := peer(readMetrics(), physical[state.TargetID])
		if settled.Audio < old.Audio {
			t.Fatal("receiver counter went backwards")
		}
		select {
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-time.After(250 * time.Millisecond):
		}
		after := readMetrics()
		if peer(after, physical[state.TargetID]).Audio != settled.Audio {
			t.Fatal("old receiver still receives after exact radio Move drain")
		}
		t.Logf("VERIFIED actual manager Move %s→%s, new receiver RTP and old receiver drained", state.TargetID, to)
		// The observation waits can outlive a short offer. Re-read before the
		// caller issues its next command, as the UI must do at click time.
		fresh := read()
		if fresh.State != "playing" || fresh.SourceKind != "radio" || fresh.TargetID != to || fresh.StationID != current.StationID || fresh.OwnerGeneration != current.OwnerGeneration || fresh.VolumePercent == nil || *fresh.VolumePercent != expectedVolume {
			t.Fatal("radio source changed while observing completed Move", fresh)
		}
		return fresh
	}
	playing = move(playing, destination)
	if mode == "roundtrip" {
		return move(playing, target)
	}
	before := readMetrics()
	a := peer(before, physical[target])
	b := peer(before, physical[destination])
	if a.Selected || a.Session || before.Item == 0 {
		t.Fatal("original actual Spotify source is not parked before explicit Resume")
	}
	// Alias selection is navigation using its current registry/incarnation. It
	// neither fabricates availability nor authorizes the subsequent Resume.
	nativeCall := func(method, path string, body any, out any) {
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
			t.Fatal("actual Native request failed", err)
		}
		defer response.Body.Close()
		if response.StatusCode != http.StatusOK {
			t.Fatal("actual Native request rejected", path, response.StatusCode)
		}
		if out != nil {
			if err := json.NewDecoder(io.LimitReader(response.Body, 65536)).Decode(out); err != nil {
				t.Fatal(err)
			}
		}
	}
	var aliases struct {
		Binding struct {
			Incarnation string `json:"incarnation"`
			Registry    string `json:"registry"`
			Room        string `json:"room_id"`
			Ready       bool   `json:"ready"`
		} `json:"binding"`
	}
	nativeCall(http.MethodGet, "/player/aliases", nil, &aliases)
	if !aliases.Binding.Ready || aliases.Binding.Incarnation == "" || aliases.Binding.Registry == "" {
		t.Fatal("actual Native alias binding unavailable")
	}
	if aliases.Binding.Room != target {
		nativeCall(http.MethodPost, "/player/aliases", map[string]string{"room_id": target, "expected_incarnation": aliases.Binding.Incarnation, "expected_registry": aliases.Binding.Registry}, nil)
	}
	producer.mu.Lock()
	other, heldBefore := producer.retained[destination]
	producer.mu.Unlock()
	if !heldBefore || other.Native.Handoff == "" {
		t.Fatal("actual radio B hold missing before explicit Spotify A return")
	}
	nativeCall(http.MethodPost, "/player/resume", nil, nil)
	wait(func(v radioMoveMetrics) bool {
		currentA := peer(v, physical[target])
		return currentA.Audio > a.Audio+2 && currentA.Selected && currentA.Session && v.Item == before.Item && peer(v, physical[destination]).Audio > b.Audio+2
	})
	current := read()
	if current.State != "playing" || current.SourceKind != "radio" || current.TargetID != destination || current.StationID != playing.StationID || current.OwnerGeneration != playing.OwnerGeneration {
		t.Fatal("explicit Spotify A return disturbed radio B", current)
	}
	if playing.VolumePercent != nil && (current.VolumePercent == nil || *current.VolumePercent != *playing.VolumePercent) {
		t.Fatal("explicit Spotify A return changed acknowledged radio B volume")
	}
	producer.mu.Lock()
	_, originalStillHeld := producer.retained[target]
	otherAfter, otherStillHeld := producer.retained[destination]
	producer.mu.Unlock()
	if originalStillHeld || !otherStillHeld || otherAfter.Native.Token != other.Native.Token || otherAfter.Native.Handoff != other.Native.Handoff {
		t.Fatal("per-target return changed unrelated B custody")
	}
	t.Log("VERIFIED actual explicit Native Spotify A Resume and fresh original RTP while radio B continues fresh RTP with exact same B custody/source")
	return current
}
