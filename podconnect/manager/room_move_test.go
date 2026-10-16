package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func moveStateFixture(t *testing.T) (*nativeRoomMoveControls, *roomContentBinding) {
	t.Helper()
	primary, runtime, rooms, scope := contentBindingFixture(t)
	binding, ok := roomContentBindingFor(primary, runtime, rooms, scope)
	if !ok {
		t.Fatal("binding unavailable")
	}
	state := &nativeRoomMoveControls{
		Schema: "podconnect.native_room_move_controls.v1", Ready: true, Scope: scope,
		Actions:  []string{"move_existing_session"},
		Offer:    roomMoveOffer{"podconnect.room_output_move_offer.v1", strings.Repeat("b", 32), strings.Repeat("c", 64), strings.Repeat("d", 32), roomMovePolicy, 27, 5000},
		Registry: binding.Registry, InputDigest: binding.PipeDigest, OutputURL: binding.OutputURL,
		OldOwner: roomTransportIdentity{Account: scope.Account, Target: scope.Target, Room: "r0", Homepod: "42", Connection: "actual-connection", Session: "same-session", Incarnation: "actual-alias", Registry: binding.Registry, Revision: 2},
	}
	oldBinding, oldRPC := roomContentBindingNow, roomMoveRPC
	t.Cleanup(func() { roomContentBindingNow, roomMoveRPC = oldBinding, oldRPC })
	roomContentBindingNow = func(s roomTransportScope) (roomContentBinding, bool) {
		b := binding
		b.Scope = s
		return b, s == scope || s == (roomTransportScope{scope.Target, "r0", "42", scope.Account})
	}
	roomMoveRPC = func(ctx context.Context, base, method, path string, body []byte) ([]byte, error) {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if base != binding.NativeURL || method != http.MethodGet || !strings.HasPrefix(path, "/player/room_move/controls?") || body != nil {
			t.Fatalf("unexpected effect/client path %s %s %s", base, method, path)
		}
		return json.Marshal(state)
	}
	return state, &binding
}

func TestMoveStateUsesActualOldSessionSeparateFromDestination(t *testing.T) {
	state, binding := moveStateFixture(t)
	q := url.Values{"account_user_id": {binding.Scope.Account}, "target_id": {binding.Scope.Target}, "room_id": {binding.Scope.Room}, "homepod_id": {binding.Scope.Homepod}}
	w := httptest.NewRecorder()
	roomMoveStateHandler(w, httptest.NewRequest("GET", "/api/room-content/state?"+q.Encode(), nil))
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var got map[string]json.RawMessage
	if json.Unmarshal(w.Body.Bytes(), &got) != nil || len(got) != 5 {
		t.Fatal("public state shape", w.Body.String())
	}
	for _, private := range []string{"alias_registry", "input_path_digest", "own_tone_url", "session_id", "name", "volume_percent"} {
		if _, ok := got[private]; ok {
			t.Fatal("invented/private field exposed", private)
		}
	}
	if !strings.Contains(w.Body.String(), state.Offer.ID) {
		t.Fatal("actual native offer lost")
	}
}

func TestMoveStateRejectsMixedOwnerConfigurationAndUnavailableSchemas(t *testing.T) {
	for name, mutate := range map[string]func(*nativeRoomMoveControls){
		"foreign account":   func(s *nativeRoomMoveControls) { s.Scope.Account = "other" },
		"other room":        func(s *nativeRoomMoveControls) { s.Scope.Room = "r0" },
		"stale registry":    func(s *nativeRoomMoveControls) { s.Registry = strings.Repeat("e", 64) },
		"foreign pipe":      func(s *nativeRoomMoveControls) { s.InputDigest = strings.Repeat("e", 64) },
		"different C owner": func(s *nativeRoomMoveControls) { s.OutputURL = "http://127.0.0.1:3690" },
		"old schema":        func(s *nativeRoomMoveControls) { s.Schema = "podconnect.native_room_move_controls.v0" },
		"wrong policy":      func(s *nativeRoomMoveControls) { s.Offer.Policy = strings.Repeat("e", 64) },
		"not ready":         func(s *nativeRoomMoveControls) { s.Ready = false },
		"duplicate actions": func(s *nativeRoomMoveControls) {
			s.Actions = []string{"move_existing_session", "move_existing_session"}
		},
		"other action": func(s *nativeRoomMoveControls) { s.Actions = []string{"resume"} },
	} {
		t.Run(name, func(t *testing.T) {
			state, binding := moveStateFixture(t)
			mutate(state)
			if _, _, err := readRoomMove(context.Background(), binding.Scope); err == nil {
				t.Fatal("mixed/unavailable native owner admitted")
			}
		})
	}
	t.Run("configuration changes while read awaits", func(t *testing.T) {
		state, binding := moveStateFixture(t)
		roomMoveRPC = func(context.Context, string, string, string, []byte) ([]byte, error) {
			roomContentBindingNow = func(roomTransportScope) (roomContentBinding, bool) { return roomContentBinding{}, false }
			return json.Marshal(state)
		}
		if _, _, err := readRoomMove(context.Background(), binding.Scope); err == nil {
			t.Fatal("replaced runtime/configuration admitted")
		}
	})
	t.Run("closed native shape", func(t *testing.T) {
		state, binding := moveStateFixture(t)
		roomMoveRPC = func(context.Context, string, string, string, []byte) ([]byte, error) {
			raw, _ := json.Marshal(state)
			return []byte(fmt.Sprintf("%s,\"authorized\":true}", raw[:len(raw)-1])), nil
		}
		if _, _, err := readRoomMove(context.Background(), binding.Scope); err == nil {
			t.Fatal("extra asserted authority accepted")
		}
	})
}
