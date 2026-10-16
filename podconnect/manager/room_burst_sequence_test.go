package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// Actual manager handlers compose the unchanged public contracts against inert
// private owner endpoints. C socket admission and native actor release have
// separate real-owner tests; this is not a physical/backend latency claim.
func TestRoomBoundSequencePauseResumeVolumeUsesFreshPrivateAuthority(t *testing.T) {
	f := roomTransportFixture(t)
	roomVolumeStore.Lock()
	roomVolumeStore.Preparing = false
	roomVolumeStore.Records = map[string]*roomVolumeRecord{}
	roomVolumeStore.Unlock()
	t.Cleanup(func() {
		roomVolumeStore.Lock()
		roomVolumeStore.Preparing = false
		roomVolumeStore.Records = map[string]*roomVolumeRecord{}
		roomVolumeStore.Unlock()
	})
	original := roomTransportRPC
	revision := 0
	ids := map[string]bool{}
	volume := roomVolumeState{"7", "8", 27, 27}
	roomTransportRPC = func(ctx context.Context, base, method, path string, body []byte) ([]byte, error) {
		switch path {
		case "/player/room_transport/bound-prepare":
			var request struct {
				Account string `json:"account_user_id"`
				Target  string `json:"target_id"`
				Room    string `json:"room_id"`
				Homepod string `json:"homepod_id"`
				Action  string `json:"action"`
				Percent int    `json:"volume_percent"`
			}
			if decodeQueueManagerJSON(body, &request) != nil || request.Room != f.Scope.Room || request.Homepod != f.Scope.Homepod || request.Percent != 0 {
				t.Fatalf("wrong audio authority %s", body)
			}
			revision++
			id := fmt.Sprintf("%032x", revision)
			if ids[id] {
				t.Fatal("preparation reused")
			}
			ids[id] = true
			return json.Marshal(receiverTransportPrepare{"podconnect.native_room_transport.v2", id, f.Native, request.Action, 0, strings.Repeat("b", 64), 5000, fmt.Sprint(revision)})
		case "/api/outputs/42/transport/bound-prepare":
			var request struct {
				ID       string                `json:"native_prepare_id"`
				Action   string                `json:"action"`
				Digest   string                `json:"args_digest"`
				Owner    roomTransportIdentity `json:"native_owner"`
				Revision string                `json:"native_revision"`
			}
			if decodeQueueManagerJSON(body, &request) != nil || request.ID != fmt.Sprintf("%032x", revision) || request.Owner != f.Native || request.Revision != fmt.Sprint(revision) {
				t.Fatalf("counter/owner was not actual prepared authority %s", body)
			}
			return json.Marshal(outputTransportPrepare{"podconnect.output_transport_prepare.v2", true, fmt.Sprintf("%032x", 100+revision), f.Route, f.Native, fmt.Sprint(revision), 10000})
		case "/player/room_transport/bound-commit":
			var request struct {
				ID    string `json:"prepare_id"`
				Token string `json:"own_tone_token"`
			}
			if decodeQueueManagerJSON(body, &request) != nil || request.ID != fmt.Sprintf("%032x", revision) || request.Token != fmt.Sprintf("%032x", 100+revision) {
				t.Fatalf("old authority reused %s", body)
			}
			return json.Marshal(map[string]any{"schema": "podconnect.native_room_transport_commit.v2", "accepted_native": true, "prepare_id": request.ID, "physical_result_verified": false})
		case "/api/outputs/42/volume/bound-state":
			var request struct {
				Owner roomTransportIdentity `json:"native_owner"`
			}
			if decodeQueueManagerJSON(body, &request) != nil || request.Owner != f.Native {
				t.Fatalf("volume owner not source-derived %s", body)
			}
			return json.Marshal(outputRoomVolumeState{"podconnect.output_room_volume_state.v2", true, f.Route, volume, false, []string{"volume"}, false})
		case "/player/room_volume/bound-prepare":
			var request roomVolumeRequest
			if decodeQueueManagerJSON(body, &request) != nil || request.Native != f.Native || request.Route != f.Route || request.Volume != volume || request.Percent != 44 {
				t.Fatalf("wrong volume authority %s", body)
			}
			revision++
			id := fmt.Sprintf("%032x", revision)
			if ids[id] {
				t.Fatal("audio/volume shared authority")
			}
			ids[id] = true
			return json.Marshal(nativeRoomVolumePrepare{"podconnect.native_room_volume_prepare.v2", id, 5000, request.Scope, request.Native, request.Route, request.Volume, "volume", 44, false})
		case "/player/room_volume/bound-commit":
			var request struct {
				ID string `json:"native_prepare_id"`
			}
			if decodeQueueManagerJSON(body, &request) != nil || request.ID != fmt.Sprintf("%032x", revision) {
				t.Fatalf("wrong volume token %s", body)
			}
			volume = roomVolumeState{"8", "8", 44, 44}
			return json.Marshal(nativeRoomVolumeResult{Schema: "podconnect.native_room_volume_commit.v2", ID: request.ID, OK: true, State: "protocol_accepted", Reserved: true, Protocol: true, Possible: true, Terminal: true, Drained: true})
		}
		if strings.HasPrefix(path, "/player/room_volume/bound-state?") {
			return json.Marshal(receiverTransportState{"podconnect.native_room_volume_state.v2", true, f.Native, []string{"volume"}, false})
		}
		return original(ctx, base, method, path, body)
	}
	started := time.Now()
	for i, action := range []string{"pause", "resume"} {
		body, _ := json.Marshal(roomTransportRequest{f.Scope, f.Native, f.Route, action, 0})
		w := httptest.NewRecorder()
		transportPrepareHandler(w, httptest.NewRequest("POST", "/api/room-transport/prepare", bytes.NewReader(body)))
		if w.Code != 200 {
			t.Fatal(w.Code, w.Body.String())
		}
		w = httptest.NewRecorder()
		transportCommitHandler(w, httptest.NewRequest("POST", "/api/room-transport/commit", strings.NewReader(fmt.Sprintf(`{"prepare_id":"%032x"}`, i+1))))
		if w.Code != 200 || !strings.Contains(w.Body.String(), `"accepted_native":true`) {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	if volume.BasePercent != 27 {
		t.Fatal("audio changed manual27")
	}
	body, _ := json.Marshal(roomVolumeRequest{f.Scope, f.Native, f.Route, volume, "volume", 44})
	w := httptest.NewRecorder()
	roomVolumePrepareHandler(w, httptest.NewRequest("POST", "/api/room-volume/prepare", bytes.NewReader(body)))
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	w = httptest.NewRecorder()
	roomVolumeCommitHandler(w, httptest.NewRequest("POST", "/api/room-volume/commit", strings.NewReader(fmt.Sprintf(`{"prepare_id":"%032x"}`, 3))))
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"protocol_accepted":true`) {
		t.Fatal(w.Code, w.Body.String())
	}
	if len(ids) != 3 || volume.BasePercent != 44 || time.Since(started) >= time.Second {
		t.Fatal("fresh owner path unexpectedly serialized/rate-limited", len(ids), volume, time.Since(started))
	}
}
