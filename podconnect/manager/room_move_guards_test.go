package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestMoveActualOldOwnerRequiresItsOwnClosedConfiguredPair(t *testing.T) {
	for name, mutate := range map[string]func(*nativeRoomMoveControls){
		"missing session":    func(s *nativeRoomMoveControls) { s.OldOwner.Session = "" },
		"wrong account":      func(s *nativeRoomMoveControls) { s.OldOwner.Account = "foreign" },
		"wrong engine":       func(s *nativeRoomMoveControls) { s.OldOwner.Target = strings.Repeat("f", 40) },
		"unknown old room":   func(s *nativeRoomMoveControls) { s.OldOwner.Room = "unknown" },
		"wrong old HomePod":  func(s *nativeRoomMoveControls) { s.OldOwner.Homepod = "66" },
		"stale old registry": func(s *nativeRoomMoveControls) { s.OldOwner.Registry = strings.Repeat("e", 64) },
	} {
		t.Run(name, func(t *testing.T) {
			state, binding := moveStateFixture(t)
			mutate(state)
			if _, _, err := readRoomMove(context.Background(), binding.Scope); err == nil {
				t.Fatal("foreign/missing actual old owner admitted")
			}
		})
	}
	for _, field := range []string{"null", "extra", "duplicate"} {
		t.Run(field, func(t *testing.T) {
			state, binding := moveStateFixture(t)
			roomMoveRPC = func(context.Context, string, string, string, []byte) ([]byte, error) {
				raw, _ := json.Marshal(state)
				owner, _ := json.Marshal(state.OldOwner)
				replacement := "null"
				if field == "extra" {
					replacement = string(owner[:len(owner)-1]) + `,"approved":true}`
				} else if field == "duplicate" {
					replacement = strings.Replace(string(owner), `"session_id":`, `"session_id":"invented","session_id":`, 1)
				}
				return []byte(strings.Replace(string(raw), string(owner), replacement, 1)), nil
			}
			if _, _, err := readRoomMove(context.Background(), binding.Scope); err == nil {
				t.Fatal("nonclosed old session proof admitted")
			}
		})
	}
}

func TestMoveOnlyBoundOldManualBaseCanCompleteIncludingZero(t *testing.T) {
	for _, percent := range []int{0, 27, 100} {
		t.Run(fmt.Sprint(percent), func(t *testing.T) {
			f := moveOperationSetup(t)
			f.Request.Offer.Percent = percent
			f.Stage = "staged"
			f.prepare(t)
			w := f.operation(t, "commit")
			var got roomMoveResult
			if json.Unmarshal(w.Body.Bytes(), &got) != nil || !got.Accepted || got.Percent == nil || *got.Percent != percent {
				t.Fatal("bound session base lost", w.Body.String())
			}
		})
	}
	for _, wrong := range []int{35, 60} {
		t.Run(fmt.Sprintf("reject substitute %d", wrong), func(t *testing.T) {
			f := moveOperationSetup(t)
			f.Stage = "staged"
			f.prepare(t)
			original := roomMoveRPC
			roomMoveRPC = func(ctx context.Context, base, method, path string, body []byte) ([]byte, error) {
				raw, err := original(ctx, base, method, path, body)
				var got roomMoveResult
				json.Unmarshal(raw, &got)
				if got.State == "complete" {
					got.Percent = &wrong
					raw, _ = json.Marshal(got)
				}
				return raw, err
			}
			w := f.operation(t, "commit")
			if !strings.Contains(w.Body.String(), `"state":"unknown"`) || strings.Contains(w.Body.String(), `"accepted_room_move":true`) {
				t.Fatal("fresh35/target60 substituted for old27", w.Body.String())
			}
		})
	}
}

func TestMoveOldRegistryRevocationBlocksAlreadyStagedCommit(t *testing.T) {
	f := moveOperationSetup(t)
	primary, runtime, rooms, scope := contentBindingFixture(t)
	roomContentBindingNow = func(s roomTransportScope) (roomContentBinding, bool) {
		return roomContentBindingFor(primary, runtime, rooms, s)
	}
	f.Request.Scope = scope
	f.Stage = "staged"
	f.prepare(t)
	// Revoke the old route while preserving the selected destination. The
	// complete registry digest must invalidate the already staged binding.
	primary.HomepodID = "43"
	w := f.operation(t, "commit")
	if w.Code != 409 || f.Commits != 0 {
		t.Fatal("old owner catalogue revocation dispatched", w.Code, f.Commits)
	}
}

func TestMoveCapturedOwnerCancellationOnceWhilePrepareIsHeld(t *testing.T) {
	f := moveOperationSetup(t)
	entered, release := make(chan struct{}), make(chan struct{})
	var cancels atomic.Int32
	old := roomMoveRPC
	roomMoveRPC = func(ctx context.Context, base, method, path string, body []byte) ([]byte, error) {
		switch path {
		case "/player/room_move/prepare":
			close(entered)
			<-release
			result := roomMoveResult{Schema: "podconnect.native_room_move_prepare.v1", Offer: f.Request.Offer.ID, Binding: f.Request.Offer.Binding, State: "staged", Room: f.Request.Scope.Room, Homepod: f.Request.Scope.Homepod}
			return json.Marshal(result)
		case "/player/room_move/cancel":
			cancels.Add(1)
			deadline, ok := ctx.Deadline()
			if !ok || time.Until(deadline) > time.Second {
				t.Error("cancel exceeded captured-owner one-second wait")
			}
			result := roomMoveResult{Schema: "podconnect.native_room_move_cancel.v1", Offer: f.Request.Offer.ID, Binding: f.Request.Offer.Binding, State: "cancelled", Room: f.Request.Scope.Room, Homepod: f.Request.Scope.Homepod}
			return json.Marshal(result)
		default:
			return old(ctx, base, method, path, body)
		}
	}
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() { done <- f.prepare(t) }()
	<-entered
	// Actual configured owner can disappear after preparation begins. Cleanup
	// must still go to its captured native client, never the replacement.
	roomContentBindingNow = func(roomTransportScope) (roomContentBinding, bool) { return roomContentBinding{}, false }
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); f.operation(t, "cancel") }()
	}
	wg.Wait()
	close(release)
	w := <-done
	if cancels.Load() != 1 || !strings.Contains(w.Body.String(), `"state":"unknown"`) || f.Commits != 0 {
		t.Fatal("cancel replayed or late staged reply revived operation", cancels.Load(), w.Body.String())
	}
	for i := 0; i < 3; i++ {
		f.operation(t, "cancel")
	}
	if cancels.Load() != 1 {
		t.Fatal("cancel replayed after terminal result")
	}
}

func TestMoveReadWaitBudgetDoesNotRenewOffer(t *testing.T) {
	state, binding := moveStateFixture(t)
	original := roomMoveRPC
	roomMoveRPC = func(ctx context.Context, base, method, path string, body []byte) ([]byte, error) {
		deadline, ok := ctx.Deadline()
		if !ok || time.Until(deadline) > 8*time.Second || time.Until(deadline) < 7*time.Second {
			t.Fatal("unexpected finite read ceiling")
		}
		return original(ctx, base, method, path, body)
	}
	q := url.Values{"account_user_id": {binding.Scope.Account}, "target_id": {binding.Scope.Target}, "room_id": {binding.Scope.Room}, "homepod_id": {binding.Scope.Homepod}}
	w := httptest.NewRecorder()
	roomMoveStateHandler(w, httptest.NewRequest(http.MethodGet, "/api/room-move/state?"+q.Encode(), nil))
	if w.Code != 200 || state.Offer.Expires != 5000 {
		t.Fatal("read renewed actual native offer", w.Code, state.Offer.Expires)
	}
}

func TestMovePrepareAndCommitUseSeparateFiniteWaitCeilings(t *testing.T) {
	f := moveOperationSetup(t)
	f.Stage = "staged"
	original := roomMoveRPC
	roomMoveRPC = func(ctx context.Context, base, method, path string, body []byte) ([]byte, error) {
		ceiling := 8 * time.Second
		if strings.HasSuffix(path, "/commit") {
			ceiling = 16 * time.Second
		}
		deadline, ok := ctx.Deadline()
		if !ok || time.Until(deadline) > ceiling || time.Until(deadline) < ceiling-time.Second {
			t.Fatal("wrong communication ceiling", path)
		}
		return original(ctx, base, method, path, body)
	}
	f.prepare(t)
	if w := f.operation(t, "commit"); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
}
