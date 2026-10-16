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

func contentStateFixture(t *testing.T) (*nativeRoomContentControls, *roomContentBinding) {
	t.Helper()
	primary, runtime, rooms, scope := contentBindingFixture(t)
	binding, ok := roomContentBindingFor(primary, runtime, rooms, scope)
	if !ok {
		t.Fatal("binding unavailable")
	}
	state := &nativeRoomContentControls{
		Schema: "podconnect.native_room_content_controls.v1", Ready: true, Scope: scope,
		Actions:  []string{"play_track", "play_context"},
		Offer:    roomContentOffer{"podconnect.room_content_offer.v1", strings.Repeat("b", 32), strings.Repeat("c", 64), strings.Repeat("d", 32), roomContentPolicy, 35, 5000},
		Registry: binding.Registry, InputDigest: binding.PipeDigest, OutputURL: binding.OutputURL,
	}
	oldBinding, oldRPC := roomContentBindingNow, roomContentRPC
	t.Cleanup(func() { roomContentBindingNow, roomContentRPC = oldBinding, oldRPC })
	roomContentBindingNow = func(s roomTransportScope) (roomContentBinding, bool) { return binding, s == scope }
	roomContentRPC = func(ctx context.Context, base, method, path string, body []byte) ([]byte, error) {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if base != binding.NativeURL || method != http.MethodGet || !strings.HasPrefix(path, "/player/room_content/controls?") || body != nil {
			t.Fatalf("unexpected effect/client path %s %s %s", base, method, path)
		}
		return json.Marshal(state)
	}
	return state, &binding
}

func TestContentStateUsesActualIdleEngineDestinationWithoutProviderDeviceInference(t *testing.T) {
	state, binding := contentStateFixture(t)
	q := url.Values{"account_user_id": {binding.Scope.Account}, "target_id": {binding.Scope.Target}, "room_id": {binding.Scope.Room}, "homepod_id": {binding.Scope.Homepod}}
	w := httptest.NewRecorder()
	roomContentStateHandler(w, httptest.NewRequest("GET", "/api/room-content/state?"+q.Encode(), nil))
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

func TestContentStateRejectsMixedOwnerConfigurationAndUnavailableSchemas(t *testing.T) {
	for name, mutate := range map[string]func(*nativeRoomContentControls){
		"foreign account":   func(s *nativeRoomContentControls) { s.Scope.Account = "other" },
		"other room":        func(s *nativeRoomContentControls) { s.Scope.Room = "r0" },
		"stale registry":    func(s *nativeRoomContentControls) { s.Registry = strings.Repeat("e", 64) },
		"foreign pipe":      func(s *nativeRoomContentControls) { s.InputDigest = strings.Repeat("e", 64) },
		"different C owner": func(s *nativeRoomContentControls) { s.OutputURL = "http://127.0.0.1:3690" },
		"old schema":        func(s *nativeRoomContentControls) { s.Schema = "podconnect.native_room_content_controls.v0" },
		"wrong policy":      func(s *nativeRoomContentControls) { s.Offer.Policy = strings.Repeat("e", 64) },
		"not ready":         func(s *nativeRoomContentControls) { s.Ready = false },
		"duplicate actions": func(s *nativeRoomContentControls) { s.Actions = []string{"play_track", "play_track"} },
		"other action":      func(s *nativeRoomContentControls) { s.Actions = []string{"resume"} },
	} {
		t.Run(name, func(t *testing.T) {
			state, binding := contentStateFixture(t)
			mutate(state)
			if _, _, err := readRoomContent(context.Background(), binding.Scope); err == nil {
				t.Fatal("mixed/unavailable native owner admitted")
			}
		})
	}
	t.Run("configuration changes while read awaits", func(t *testing.T) {
		state, binding := contentStateFixture(t)
		roomContentRPC = func(context.Context, string, string, string, []byte) ([]byte, error) {
			roomContentBindingNow = func(roomTransportScope) (roomContentBinding, bool) { return roomContentBinding{}, false }
			return json.Marshal(state)
		}
		if _, _, err := readRoomContent(context.Background(), binding.Scope); err == nil {
			t.Fatal("replaced runtime/configuration admitted")
		}
	})
	t.Run("closed native shape", func(t *testing.T) {
		state, binding := contentStateFixture(t)
		roomContentRPC = func(context.Context, string, string, string, []byte) ([]byte, error) {
			raw, _ := json.Marshal(state)
			return []byte(fmt.Sprintf("%s,\"authorized\":true}", raw[:len(raw)-1])), nil
		}
		if _, _, err := readRoomContent(context.Background(), binding.Scope); err == nil {
			t.Fatal("extra asserted authority accepted")
		}
	})
}
