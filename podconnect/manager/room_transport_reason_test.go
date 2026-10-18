package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func assertReadReason(t *testing.T, scope roomTransportScope, reason string) {
	t.Helper()
	q := url.Values{"target_id": {scope.Target}, "room_id": {scope.Room}, "homepod_id": {scope.Homepod}, "account_user_id": {scope.Account}}
	w := httptest.NewRecorder()
	transportStateHandler(w, httptest.NewRequest("GET", "/api/room-transport/state?"+q.Encode(), nil))
	var got map[string]any
	if json.Unmarshal(w.Body.Bytes(), &got) != nil || w.Code != 409 || len(got) != 4 || got["ok"] != false || got["error_kind"] != "room_transport_unavailable" || got["retry_allowed"] != false || got["reason"] != reason {
		t.Fatal(w.Code, w.Body.String())
	}
}
func TestRoomReadClosedReasons(t *testing.T) {
	for _, reason := range []string{"claim_unavailable", "receiver_proof_unavailable", "output_proof_unavailable"} {
		t.Run(reason, func(t *testing.T) {
			f := roomTransportFixture(t)
			switch reason {
			case "claim_unavailable":
				f.Claim.Proof.OutputSelected = false
			case "receiver_proof_unavailable":
				f.Native.Homepod = "999"
			case "output_proof_unavailable":
				f.Route.InputDigest = strings.Repeat("0", 64)
			}
			assertReadReason(t, f.Scope, reason)
			if len(f.Writes) != 0 {
				t.Fatal("read wrote", f.Writes)
			}
		})
	}
	if roomTransportReadReason(errors.New("private endpoint token")) != "" {
		t.Fatal("arbitrary error exposed")
	}
}
func TestRoomReadReleasedOwnerReason(t *testing.T) {
	_, scope, owner, route := releasedRoomRouteFixture(t)
	old := roomTransportRPC
	t.Cleanup(func() { roomTransportRPC = old })
	roomTransportRPC = func(_ context.Context, _, method, path string, _ []byte) ([]byte, error) {
		switch {
		case strings.HasPrefix(path, "/player/room_transport/bound-state?"):
			return json.Marshal(receiverTransportState{"podconnect.native_room_transport_state.v2", true, *owner, transportActions, false})
		case path == "/api/outputs/42/transport/bound-state":
			return json.Marshal(outputTransportState{"podconnect.output_transport_state.v2", true, *route, false, transportActions, false, false, true})
		default:
			t.Fatalf("unexpected request %s %s", method, path)
			return nil, errors.New("unexpected")
		}
	}
	assertReadReason(t, scope, "route_owner_unavailable")
}
