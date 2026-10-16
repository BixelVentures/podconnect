package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type moveOperationFixture struct {
	Request    roomMoveRequest
	Prepared   int
	Commits    int
	Cancels    int
	Stage      string
	FailCommit bool
}

func moveOperationSetup(t *testing.T) *moveOperationFixture {
	t.Helper()
	state, binding := moveStateFixture(t)
	f := &moveOperationFixture{Request: roomMoveRequest{binding.Scope, state.Offer, "move_existing_session", map[string]any{}}, Stage: "staging"}
	roomMoveStore.Lock()
	roomMoveStore.Records = make(map[string]*roomMoveRecord)
	roomMoveStore.Unlock()
	t.Cleanup(func() {
		roomMoveStore.Lock()
		roomMoveStore.Records = make(map[string]*roomMoveRecord)
		roomMoveStore.Unlock()
	})
	roomMoveRPC = func(ctx context.Context, base, method, path string, body []byte) ([]byte, error) {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if base != binding.NativeURL {
			t.Fatal("foreign native selected", base)
		}
		endpoint, phase := "", f.Stage
		switch {
		case path == "/player/room_move/prepare":
			f.Prepared++
			endpoint = "prepare"
			if method != http.MethodPost {
				t.Fatal("not POST")
			}
			parsed, err := decodeRoomMoveRequest(body)
			if err != nil || parsed.Scope != f.Request.Scope || parsed.Offer != f.Request.Offer {
				t.Fatal("unbound native preparation", string(body), err)
			}
		case strings.HasPrefix(path, "/player/room_move/result?"):
			endpoint = "result"
		case path == "/player/room_move/commit":
			endpoint = "commit"
			f.Commits++
			if f.FailCommit {
				return nil, fmt.Errorf("lost commit reply")
			}
			phase = "complete"
		case path == "/player/room_move/cancel":
			endpoint = "cancel"
			f.Cancels++
			phase = "cancelled"
		default:
			t.Fatal("unexpected/fallback source path", method, path)
		}
		result := roomMoveResult{Schema: "podconnect.native_room_move_" + endpoint + ".v1", Offer: f.Request.Offer.ID, Binding: f.Request.Offer.Binding, State: phase, Room: f.Request.Scope.Room, Homepod: f.Request.Scope.Homepod}
		if phase == "complete" {
			id, percent := result.Offer, f.Request.Offer.Percent
			result.OK = true
			result.Accepted = true
			result.Operation = &id
			result.Percent = &percent
		}
		return json.Marshal(result)
	}
	return f
}

func (f *moveOperationFixture) prepare(t *testing.T) *httptest.ResponseRecorder {
	t.Helper()
	raw, _ := json.Marshal(f.Request)
	w := httptest.NewRecorder()
	roomMovePrepareHandler(w, httptest.NewRequest("POST", "/api/room-move/prepare", bytes.NewReader(raw)))
	return w
}
func (f *moveOperationFixture) operation(t *testing.T, endpoint string) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	if endpoint == "result" {
		roomMoveResultHandler(w, httptest.NewRequest("GET", "/api/room-move/result?offer_id="+f.Request.Offer.ID+"&binding_id="+f.Request.Offer.Binding, nil))
		return w
	}
	raw, _ := json.Marshal(map[string]string{"offer_id": f.Request.Offer.ID, "binding_id": f.Request.Offer.Binding})
	request := httptest.NewRequest("POST", "/api/room-move/"+endpoint, bytes.NewReader(raw))
	if endpoint == "commit" {
		roomMoveCommitHandler(w, request)
	} else {
		roomMoveCancelHandler(w, request)
	}
	return w
}

func TestMoveStagingAndReadsNeverStartAndFinalCommitIsOneUse(t *testing.T) {
	f := moveOperationSetup(t)
	if w := f.prepare(t); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	for i := 0; i < 3; i++ {
		if w := f.operation(t, "result"); w.Code != 200 {
			t.Fatal(w.Code)
		}
	}
	if f.Commits != 0 {
		t.Fatal("stage/read caused physical commit")
	}
	if w := f.operation(t, "commit"); w.Code != 409 {
		t.Fatal("unstaged source admitted")
	}
	f.Stage = "staged"
	f.operation(t, "result")
	w := f.operation(t, "commit")
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"accepted_room_move":true`) || f.Commits != 1 {
		t.Fatal(w.Code, w.Body.String(), f.Commits)
	}
	f.operation(t, "commit")
	f.operation(t, "result")
	f.prepare(t)
	if f.Commits != 1 || f.Prepared != 1 {
		t.Fatal("consumed request replayed", f.Commits, f.Prepared)
	}
}

func TestMoveCancelledOrReplacedDestinationCannotCommit(t *testing.T) {
	t.Run("cancelled staged operation", func(t *testing.T) {
		f := moveOperationSetup(t)
		f.Stage = "staged"
		f.prepare(t)
		f.operation(t, "cancel")
		w := f.operation(t, "commit")
		if w.Code != 200 || f.Commits != 0 || !strings.Contains(w.Body.String(), `"state":"cancelled"`) {
			t.Fatal(w.Code, w.Body.String(), f.Commits)
		}
	})
	t.Run("held mapping replaced", func(t *testing.T) {
		f := moveOperationSetup(t)
		f.Stage = "staged"
		f.prepare(t)
		roomContentBindingNow = func(roomTransportScope) (roomContentBinding, bool) { return roomContentBinding{}, false }
		w := f.operation(t, "commit")
		if w.Code != 409 || f.Commits != 0 {
			t.Fatal("stale destination sent", w.Code, f.Commits)
		}
	})
	t.Run("same offer different content", func(t *testing.T) {
		f := moveOperationSetup(t)
		f.prepare(t)
		f.Request.Offer.Percent = 60
		if w := f.prepare(t); w.Code != 409 || f.Prepared != 1 {
			t.Fatal("offer reassigned to changed content", w.Code, f.Prepared)
		}
	})
}

func TestMoveUnknownNeverUpgradesOrReplaysButCancellationStillReachesNative(t *testing.T) {
	f := moveOperationSetup(t)
	f.Stage = "staged"
	f.prepare(t)
	f.FailCommit = true
	w := f.operation(t, "commit")
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"state":"unknown"`) || f.Commits != 1 {
		t.Fatal(w.Code, w.Body.String(), f.Commits)
	}
	f.FailCommit = false
	f.Stage = "complete"
	for _, endpoint := range []string{"result", "commit", "cancel", "result"} {
		w = f.operation(t, endpoint)
		if w.Code != 200 || !strings.Contains(w.Body.String(), `"state":"unknown"`) || strings.Contains(w.Body.String(), `"accepted_room_move":true`) {
			t.Fatal("immutable unknown upgraded", endpoint, w.Code, w.Body.String())
		}
	}
	if f.Commits != 1 || f.Cancels != 1 {
		t.Fatal("uncertain effect replayed or cancel suppressed", f.Commits, f.Cancels)
	}
}

func TestMoveClosedRequestsRejectCallerSourceVolumeAndAuthority(t *testing.T) {
	f := moveOperationSetup(t)
	raw, _ := json.Marshal(f.Request)
	for name, mutate := range map[string]func(string) string{
		"caller volume": func(s string) string { return strings.Replace(s, `"args":{}`, `"args":{"percent":35}`, 1) },
		"caller content": func(s string) string {
			return strings.Replace(s, `"args":{}`, `"args":{"track_uri":"spotify:track:1234567890123456789012"}`, 1)
		},
		"start action": func(s string) string { return strings.Replace(s, "move_existing_session", "play_track", 1) },
		"null args":    func(s string) string { return strings.Replace(s, `"args":{}`, `"args":null`, 1) },
		"duplicate offer": func(s string) string {
			return strings.Replace(s, `"offer_id":`, `"offer_id":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","offer_id":`, 1)
		},
		"authority flag": func(s string) string { return s[:len(s)-1] + `,"authorized":true}` },
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := decodeRoomMoveRequest([]byte(mutate(string(raw)))); err == nil {
				t.Fatal("caller source/volume/authority admitted")
			}
		})
	}
}

func TestMoveCapacityRetainsDispatchedUnknownButReclaimsExpiredUndispatched(t *testing.T) {
	f := moveOperationSetup(t)
	roomMoveStore.Lock()
	for i := 0; i < 128; i++ {
		id := fmt.Sprintf("%032x", i)
		roomMoveStore.Records[id] = &roomMoveRecord{Dispatched: true, Created: time.Now().Add(-time.Hour), Result: roomMoveResult{State: "unknown"}}
	}
	roomMoveStore.Unlock()
	if w := f.prepare(t); w.Code != 409 || f.Prepared != 0 {
		t.Fatal("dispatched unknown evicted", w.Code, f.Prepared)
	}
	roomMoveStore.Lock()
	roomMoveStore.Records[fmt.Sprintf("%032x", 0)].Dispatched = false
	roomMoveStore.Unlock()
	if w := f.prepare(t); w.Code != 200 || f.Prepared != 1 {
		t.Fatal("expired undispatched offer exhausted capacity", w.Code, w.Body.String(), f.Prepared)
	}
}

func TestMoveCannotCreditPhysicalCompletionFromStageOrUndispatchedStatus(t *testing.T) {
	t.Run("prepare claims complete", func(t *testing.T) {
		f := moveOperationSetup(t)
		f.Stage = "complete"
		w := f.prepare(t)
		if w.Code != 200 || !strings.Contains(w.Body.String(), `"state":"unknown"`) || strings.Contains(w.Body.String(), `"accepted_room_move":true`) || f.Commits != 0 {
			t.Fatal("prepare credited physical effect", w.Code, w.Body.String(), f.Commits)
		}
	})
	t.Run("result claims complete before commit", func(t *testing.T) {
		f := moveOperationSetup(t)
		f.prepare(t)
		f.Stage = "complete"
		w := f.operation(t, "result")
		if w.Code != 200 || !strings.Contains(w.Body.String(), `"state":"unknown"`) || strings.Contains(w.Body.String(), `"accepted_room_move":true`) || f.Commits != 0 {
			t.Fatal("read credited undispatched physical effect", w.Code, w.Body.String(), f.Commits)
		}
	})
}
