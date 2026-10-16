package main

// Native owns all retained-session movement and the complete C-gate transaction.
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

const roomMovePolicy = "a674d94e377e8ba2b51ca4ea5f541370bd9dd7f2ecc6fabb2cf3e2f50cbdb5b4"

type roomMoveOffer struct {
	Schema      string `json:"schema"`
	ID          string `json:"offer_id"`
	Binding     string `json:"binding_id"`
	Incarnation string `json:"native_incarnation"`
	Policy      string `json:"policy_revision"`
	Percent     int    `json:"session_volume_percent"`
	Expires     int    `json:"expires_after_ms"`
}

type nativeRoomMoveControls struct {
	Schema      string                `json:"schema"`
	Ready       bool                  `json:"ready"`
	Scope       roomTransportScope    `json:"scope"`
	Actions     []string              `json:"actions"`
	Offer       roomMoveOffer         `json:"move_offer"`
	Physical    bool                  `json:"physical_result_verified"`
	Registry    string                `json:"alias_registry"`
	InputDigest string                `json:"input_path_digest"`
	OutputURL   string                `json:"own_tone_url"`
	OldOwner    roomTransportIdentity `json:"old_native_owner"`
}

func validRoomMoveOffer(offer roomMoveOffer) bool {
	return offer.Schema == "podconnect.room_output_move_offer.v1" && queueObservationPattern.MatchString(offer.ID) && transportDigest.MatchString(offer.Binding) && queueObservationPattern.MatchString(offer.Incarnation) && offer.Policy == roomMovePolicy && offer.Percent >= 0 && offer.Percent <= 100 && offer.Expires >= 1 && offer.Expires <= 5000
}

func validRoomMoveActions(actions []string) bool {
	return slices.Equal(actions, []string{"move_existing_session"})
}

// Validate the actual OLD session against its own configured registry pair,
// independently from the explicitly chosen destination. Configuration binds
// provenance only; native remains the actual retained-source owner.
func validRoomMoveOldOwner(binding roomContentBinding, owner roomTransportIdentity) bool {
	oldScope := roomTransportScope{binding.Scope.Target, owner.Room, owner.Homepod, binding.Scope.Account}
	if !validTransportIdentity(owner, oldScope) || owner.Registry != binding.Registry {
		return false
	}
	oldBinding, ok := roomContentBindingNow(oldScope)
	oldBinding.Scope = binding.Scope
	return ok && oldBinding == binding
}

func readRoomMove(ctx context.Context, scope roomTransportScope) (roomContentBinding, nativeRoomMoveControls, error) {
	var state nativeRoomMoveControls
	binding, ok := roomContentBindingNow(scope)
	if !ok {
		return binding, state, fmt.Errorf("configured move destination unavailable")
	}
	q := url.Values{"account_user_id": {scope.Account}, "target_id": {scope.Target}, "room_id": {scope.Room}, "homepod_id": {scope.Homepod}}
	started := time.Now()
	raw, err := roomMoveRPC(ctx, binding.NativeURL, http.MethodGet, "/player/room_move/controls?"+q.Encode(), nil)
	fields, shapeErr := contentFields(raw, "schema", "ready", "scope", "actions", "move_offer", "physical_result_verified", "alias_registry", "input_path_digest", "own_tone_url", "old_native_owner")
	if err != nil || shapeErr != nil || decodeQueueManagerJSON(raw, &state) != nil || decodeQueueManagerJSON(fields["scope"], &state.Scope) != nil || decodeQueueManagerJSON(fields["move_offer"], &state.Offer) != nil || decodeQueueManagerJSON(fields["old_native_owner"], &state.OldOwner) != nil || !validRoomMoveOldOwner(binding, state.OldOwner) || state.Schema != "podconnect.native_room_move_controls.v1" || !state.Ready || state.Physical || state.Scope != scope || !validRoomMoveOffer(state.Offer) || !validRoomMoveActions(state.Actions) || state.Registry != binding.Registry || state.InputDigest != binding.PipeDigest || state.OutputURL != binding.OutputURL || !roomContentBindingCurrent(binding) {
		return binding, state, fmt.Errorf("actual native move destination unavailable")
	}
	// Relative expiry can only shrink while traversing the manager. Native keeps
	// the original absolute one-use offer deadline regardless of this descriptor.
	remaining := state.Offer.Expires - int(time.Since(started).Milliseconds())
	if remaining <= 0 {
		return binding, state, fmt.Errorf("move offer expired")
	}
	state.Offer.Expires = remaining
	return binding, state, nil
}

func roomMoveStateHandler(w http.ResponseWriter, r *http.Request) {
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
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	_, state, err := readRoomMove(ctx, scope)
	if err != nil {
		http.Error(w, "move unavailable", 409)
		return
	}
	writeJSON(w, map[string]any{"schema": "podconnect.room_move_state.v1", "ok": true, "move_offer": state.Offer, "actions": state.Actions, "physical_result_verified": false})
}
