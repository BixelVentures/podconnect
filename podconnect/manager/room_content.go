package main

// Native owns all selected-content staging and the complete C-gate transaction.
// Manager exposes only bound descriptors/results, never C tokens or a WebAPI
// fallback. Effect handlers are registered only with the complete owner slice.
import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"time"
)

const roomContentPolicy = "7fc3239b1c5e65cdb49cca308ad9368c50ef0a90d4cd5fd537a9f97de488625d"

type roomContentOffer struct {
	Schema      string `json:"schema"`
	ID          string `json:"offer_id"`
	Binding     string `json:"binding_id"`
	Incarnation string `json:"native_incarnation"`
	Policy      string `json:"policy_revision"`
	Percent     int    `json:"start_volume_percent"`
	Expires     int    `json:"expires_after_ms"`
}

type nativeRoomContentControls struct {
	Schema      string             `json:"schema"`
	Ready       bool               `json:"ready"`
	Scope       roomTransportScope `json:"scope"`
	Actions     []string           `json:"actions"`
	Offer       roomContentOffer   `json:"start_offer"`
	Physical    bool               `json:"physical_result_verified"`
	Registry    string             `json:"alias_registry"`
	InputDigest string             `json:"input_path_digest"`
	OutputURL   string             `json:"own_tone_url"`
}

func validRoomContentOffer(offer roomContentOffer) bool {
	return offer.Schema == "podconnect.room_content_offer.v1" && queueObservationPattern.MatchString(offer.ID) && transportDigest.MatchString(offer.Binding) && queueObservationPattern.MatchString(offer.Incarnation) && offer.Policy == roomContentPolicy && offer.Percent == 35 && offer.Expires >= 1 && offer.Expires <= 5000
}

func validRoomContentActions(actions []string) bool {
	if actions == nil || len(actions) > 2 {
		return false
	}
	seen := map[string]bool{}
	for _, action := range actions {
		if !slices.Contains([]string{"play_track", "play_context"}, action) || seen[action] {
			return false
		}
		seen[action] = true
	}
	return true
}

func readRoomContent(ctx context.Context, scope roomTransportScope) (roomContentBinding, nativeRoomContentControls, error) {
	var state nativeRoomContentControls
	binding, ok := roomContentBindingNow(scope)
	if !ok {
		return binding, state, fmt.Errorf("configured content destination unavailable")
	}
	q := url.Values{"account_user_id": {scope.Account}, "target_id": {scope.Target}, "room_id": {scope.Room}, "homepod_id": {scope.Homepod}}
	started := time.Now()
	raw, err := roomContentRPC(ctx, binding.NativeURL, http.MethodGet, "/player/room_content/controls?"+q.Encode(), nil)
	fields, shapeErr := contentFields(raw, "schema", "ready", "scope", "actions", "start_offer", "physical_result_verified", "alias_registry", "input_path_digest", "own_tone_url")
	if err != nil || shapeErr != nil || decodeQueueManagerJSON(raw, &state) != nil || decodeQueueManagerJSON(fields["scope"], &state.Scope) != nil || decodeQueueManagerJSON(fields["start_offer"], &state.Offer) != nil || state.Schema != "podconnect.native_room_content_controls.v1" || !state.Ready || state.Physical || state.Scope != scope || !validRoomContentOffer(state.Offer) || !validRoomContentActions(state.Actions) || state.Registry != binding.Registry || state.InputDigest != binding.PipeDigest || state.OutputURL != binding.OutputURL || !roomContentBindingCurrent(binding) {
		return binding, state, fmt.Errorf("actual native content destination unavailable")
	}
	// Relative expiry can only shrink while traversing the manager. Native keeps
	// the original absolute one-use offer deadline regardless of this descriptor.
	remaining := state.Offer.Expires - int(time.Since(started).Milliseconds())
	if remaining <= 0 {
		return binding, state, fmt.Errorf("content offer expired")
	}
	state.Offer.Expires = remaining
	return binding, state, nil
}

func roomContentStateHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method", 405)
		return
	}
	q := r.URL.Query()
	scope := roomTransportScope{q.Get("target_id"), q.Get("room_id"), q.Get("homepod_id"), q.Get("account_user_id")}
	if len(q) != 4 || !validTransportScope(scope) {
		http.Error(w, "arguments", 400)
		return
	}
	for _, values := range q {
		if len(values) != 1 {
			http.Error(w, "arguments", 400)
			return
		}
	}
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	_, state, err := readRoomContent(ctx, scope)
	if err != nil {
		http.Error(w, "content unavailable", 409)
		return
	}
	writeJSON(w, map[string]any{"schema": "podconnect.room_content_state.v1", "ok": true, "start_offer": state.Offer, "actions": state.Actions, "physical_result_verified": false})
}
