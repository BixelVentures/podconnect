package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

func queueFixture(t *testing.T) (startClaim, string, *int) {
	t.Helper()
	room, rt, rooms, id, _ := startFixture(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"outputs":[{"id":"native-output-42","type":"AirPlay 2","selected":true,"volume":35,"needs_auth_key":false}]}`)
	}))
	status := rt.live.Get()
	status.Active = true
	rt.live.set(status)
	claim, ok := startClaimFor(room, rt, rooms, "r0", id)
	if !ok || claim.Proof.Mode != "same_session" {
		t.Fatalf("missing same-session fixture %+v", claim)
	}
	oldClaim, oldRPC := queueCurrentClaim, queueNativeRPC
	queueManagerState.Lock()
	queueManagerState.Busy = false
	queueManagerState.Offer = nil
	queueManagerState.Unlock()
	t.Cleanup(func() {
		queueCurrentClaim = oldClaim
		queueNativeRPC = oldRPC
		queueManagerState.Lock()
		queueManagerState.Busy = false
		queueManagerState.Offer = nil
		queueManagerState.Unlock()
	})
	queueCurrentClaim = func(room, target string) (startClaim, bool) { return claim, room == "r0" && target == id }
	sends := new(int)
	queueNativeRPC = func(ctx context.Context, base, method, path string, body []byte) ([]byte, error) {
		if method == http.MethodPost {
			*sends++
			var args map[string]any
			_ = json.Unmarshal(body, &args)
			if _, ok := args["uri"]; ok {
				t.Fatal("URI selector")
			}
			return json.Marshal(map[string]any{"ok": true, "schema": "podconnect.native_queue_commit.v1", "observation_id": args["observation_id"], "occurrence_id": args["occurrence_id"], "physical_result_verified": false})
		}
		return json.Marshal(queueNativeSnapshot{Schema: "podconnect.native_queue.v1", TargetID: id, RoomID: "r0", AccountUserID: "native-user", ConnectionID: "connection", SessionID: "session", AliasIncarnation: claim.Proof.AliasBinding.Incarnation, ObservationID: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", QueueRevision: 7, ExpiresAfterMS: 10000, Items: []queueNativeItem{{"111111111111111111111111", 0, "spotify:track:AAAAAAAAAAAAAAAAAAAAAA", true}, {"222222222222222222222222", 1, "spotify:track:AAAAAAAAAAAAAAAAAAAAAA", true}}, Truncated: false})
	}
	return claim, id, sends
}
func queueRead(t *testing.T, id string) queueNativeSnapshot {
	t.Helper()
	out := httptest.NewRecorder()
	queueReadHandler(out, httptest.NewRequest("GET", "/api/queue-occurrences?"+url.Values{"room_id": {"r0"}, "target_id": {id}, "account_user_id": {"native-user"}}.Encode(), nil))
	if out.Code != 200 {
		t.Fatalf("read %d %s", out.Code, out.Body.String())
	}
	var reply queueNativeSnapshot
	if json.Unmarshal(out.Body.Bytes(), &reply) != nil {
		t.Fatal("invalid reply")
	}
	return reply
}
func queueJump(t *testing.T, id, option string) *httptest.ResponseRecorder {
	t.Helper()
	raw, _ := json.Marshal(queueManagerJump{id, "r0", "native-user", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "session", 7, option})
	out := httptest.NewRecorder()
	queueJumpHandler(out, httptest.NewRequest("POST", "/api/queue-occurrence", bytes.NewReader(raw)))
	return out
}
func TestQueueManagerExactOccurrenceOneUseAndRouteScope(t *testing.T) {
	_, id, sends := queueFixture(t)
	reply := queueRead(t, id)
	if reply.HomepodID != "native-output-42" || reply.Items[1].ID == reply.Items[0].ID {
		t.Fatal("scope or duplicate identity lost")
	}
	if got := queueJump(t, id, "222222222222222222222222"); got.Code != 200 {
		t.Fatal(got.Code, got.Body.String())
	}
	if queueJump(t, id, "222222222222222222222222").Code != 409 || *sends != 1 {
		t.Fatal("replay sent")
	}
}
func TestQueueManagerRetiredOwnerAndMissingOccurrenceNeverSend(t *testing.T) {
	claim, id, sends := queueFixture(t)
	queueRead(t, id)
	if queueJump(t, id, "333333333333333333333333").Code != 409 || *sends != 0 {
		t.Fatal("missing occurrence sent")
	}
	queueRead(t, id)
	claim.RT.live.beginRun(nil)
	if queueJump(t, id, "222222222222222222222222").Code != 409 || *sends != 0 {
		t.Fatal("retired route sent")
	}
}
func TestQueueManagerClosedRequiredSchema(t *testing.T) {
	for _, raw := range []string{`{"ok":true,"schema":"podconnect.native_queue_commit.v1","observation_id":"a","occurrence_id":"b"}`, `{"ok":true,"ok":true}`, `null`, `{} {}`} {
		var ack struct {
			OK       bool `json:"ok"`
			Physical bool `json:"physical_result_verified"`
		}
		if decodeQueueManagerJSON([]byte(raw), &ack) == nil {
			t.Fatalf("accepted %s", raw)
		}
	}
}
