package main

// Manual disconnect belongs to the same bound Native/C transport owner. The
// displayed binding is read knowledge; every execution joins that owner again.
import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/url"
	"time"
)

const roomReleasePurpose = "manual_release_v1"

type nativeReleaseOwner struct {
	Schema string                `json:"schema"`
	Owner  roomTransportIdentity `json:"native_owner"`
	Writer queueWriterProof      `json:"writer_proof"`
}
type nativeReleaseState struct {
	Schema   string                `json:"schema"`
	Ready    bool                  `json:"ready"`
	Identity roomTransportIdentity `json:"identity"`
	Actions  []string              `json:"actions"`
	Physical bool                  `json:"physical_result_verified"`
	Release  nativeReleaseOwner    `json:"release"`
}
type roomReleaseRead struct {
	Owner nativeReleaseOwner      `json:"native"`
	Route outputTransportIdentity `json:"route"`
	Claim startClaim              `json:"-"`
}
type roomReleaseProjection struct {
	OwnerBindingID string `json:"owner_binding_id"`
}
type roomReleaseRequest struct {
	RoomID    string `json:"room_id"`
	Binding   string `json:"expected_binding_id"`
	RequestID string `json:"request_id"`
}
type nativeReleasePrepare struct {
	receiverTransportPrepare
	Purpose string           `json:"purpose"`
	Writer  queueWriterProof `json:"writer_proof"`
}
type roomReleaseResult struct {
	Schema          string `json:"schema"`
	OK              bool   `json:"ok"`
	PrepareID       string `json:"prepare_id"`
	Outcome         string `json:"outcome"`
	Terminal        bool   `json:"terminal"`
	ContextsDrained bool   `json:"contexts_drained"`
	RetryAllowed    bool   `json:"retry_allowed"`
	Physical        bool   `json:"physical_result_verified"`
}

func releaseBinding(read roomReleaseRead) string {
	raw, err := json.Marshal(read)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func readRoomRelease(ctx context.Context, scope roomTransportScope) (roomReleaseRead, error) {
	var read roomReleaseRead
	claim, ok := roomTransportClaim(scope.Room, scope.Target)
	if !ok || claim.Released && claim.Proof.Mode != "same_session" {
		claim, ok = currentRoomTransportClaim(scope.Room, scope.Target)
	}
	if !ok || !validTransportScope(scope) || !transportClaimMatches(claim, scope) || !transportRouteClaimSnapshotCurrent(claim, scope) {
		return read, errTransportClaim
	}
	q := url.Values{"account_user_id": {scope.Account}, "target_id": {scope.Target}, "room_id": {scope.Room}, "homepod_id": {scope.Homepod}, "include_release": {"true"}}
	raw, err := roomTransportRPC(ctx, claim.RT.room.Librespot, http.MethodGet, "/player/room_transport/bound-state?"+q.Encode(), nil)
	var native nativeReleaseState
	if err != nil || queueClosedJSON(raw, &native) != nil || native.Schema != "podconnect.native_room_transport_state.v2" || !native.Ready || native.Physical || !validTransportActions(native.Actions) || native.Release.Schema != "podconnect.native_release_owner.v1" || native.Release.Owner != native.Identity || !validTransportIdentity(native.Identity, scope) || !queueOwnerSafe(native.Identity) || !validQueueWriter(native.Release.Writer, claim) || native.Identity.Incarnation != claim.Proof.AliasBinding.Incarnation || native.Identity.Registry != claim.Proof.AliasBinding.Registry || !transportRouteClaimSnapshotCurrent(claim, scope) {
		return read, errTransportReceiver
	}
	body, _ := json.Marshal(map[string]any{"native_owner": native.Identity})
	raw, err = roomTransportRPC(ctx, claim.RT.room.OwnTone, http.MethodPost, "/api/outputs/"+url.PathEscape(scope.Homepod)+"/transport/bound-state", body)
	var output outputTransportState
	if err != nil || queueClosedJSON(raw, &output) != nil || output.Schema != "podconnect.output_transport_state.v2" || !output.OK || output.Physical || output.Busy || output.StartOwned && output.CleanAbsent || !validTransportActions(output.Actions) || !validOutputTransportIdentity(output.Identity, scope.Homepod) || output.Identity.InputDigest != configuredTransportInput(claim) || !transportRouteClaimSnapshotCurrent(claim, scope) {
		return read, errTransportOutput
	}
	return roomReleaseRead{native.Release, output.Identity, claim}, nil
}

func roomReleaseScope(ctx context.Context, room *Room) (roomTransportScope, bool) {
	primary := primaryRoom()
	if room == nil || primary == nil || room.HomepodID == "" {
		return roomTransportScope{}, false
	}
	target := configuredEngineID(primary)
	if !engineIDPattern.MatchString(target) {
		return roomTransportScope{}, false
	}
	account := currentNativeAccount(ctx, primary.Librespot, target)
	scope := roomTransportScope{target, room.ID, room.HomepodID, account}
	return scope, validTransportScope(scope)
}

func roomReleaseForDisplay(ctx context.Context, room *Room) *roomReleaseProjection {
	primary := primaryRoom()
	if room == nil || primary == nil || mgr == nil {
		return nil
	}
	runtime := mgr.runtime(primary.ID)
	if runtime == nil {
		return nil
	}
	status := runtime.live.Get()
	if status.AliasBinding == nil || !status.AliasBinding.valid() || status.AliasBinding.RoomID != room.ID {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, 4*time.Second)
	defer cancel()
	scope, ok := roomReleaseScope(ctx, room)
	if !ok {
		return nil
	}
	read, err := readRoomRelease(ctx, scope)
	if err != nil || ctx.Err() != nil {
		return nil
	}
	return &roomReleaseProjection{releaseBinding(read)}
}

func releaseResultValid(result roomReleaseResult, prepareID string) bool {
	if result.Schema != "podconnect.release_result.v1" || result.PrepareID != prepareID || result.RetryAllowed || result.Physical {
		return false
	}
	switch result.Outcome {
	case "retired":
		return result.OK && result.Terminal && result.ContextsDrained
	case "rejected":
		return !result.OK && result.Terminal && !result.ContextsDrained
	case "unknown":
		return !result.OK && !result.Terminal
	default:
		return false
	}
}

func manualReleaseHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet && r.URL.RawQuery != "" {
		manualReleaseResultHandler(w, r)
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", 405)
		return
	}
	var request roomReleaseRequest
	if decodeTransportBody(r, &request) != nil || request.RoomID == "" || len(request.RoomID) > 128 || !transportDigest.MatchString(request.Binding) || !queueObservationPattern.MatchString(request.RequestID) {
		w.WriteHeader(http.StatusConflict)
		writeJSON(w, map[string]any{"ok": false, "error": "release_unsupported", "outcome": "rejected", "retryable": false})
		return
	}
	if !acquireRoomTransport() {
		http.Error(w, "busy", 409)
		return
	}
	defer releaseRoomTransport()
	roomTransportStore.Lock()
	if roomTransportStore.ReleasePresent && roomTransportStore.ReleaseRequest.RequestID == request.RequestID {
		prior, same := roomTransportStore.ReleaseResult, roomTransportStore.ReleaseRequest == request
		base := roomTransportStore.ReleaseNativeBase
		roomTransportStore.Unlock()
		if !same {
			http.Error(w, "request conflict", 409)
			return
		}
		prior = refreshRoomReleaseResult(r.Context(), request, base, prior)
		writeJSON(w, prior)
		return
	}
	roomTransportStore.Unlock()
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	room := roomByID(request.RoomID)
	scope, ok := roomReleaseScope(ctx, room)
	if !ok {
		http.Error(w, "release owner unavailable", 409)
		return
	}
	read, err := readRoomRelease(ctx, scope)
	if err != nil || releaseBinding(read) != request.Binding || ctx.Err() != nil {
		http.Error(w, "release owner changed", 409)
		return
	}
	started := time.Now()
	body, _ := json.Marshal(map[string]any{"account_user_id": scope.Account, "target_id": scope.Target, "room_id": scope.Room, "homepod_id": scope.Homepod, "action": "release", "volume_percent": 0, "expected_release_owner": read.Owner})
	raw, err := roomTransportRPC(ctx, read.Claim.RT.room.Librespot, http.MethodPost, "/player/room_transport/bound-prepare", body)
	var prepared nativeReleasePrepare
	var fields map[string]json.RawMessage
	flatFields := json.Unmarshal(raw, &fields) == nil && len(fields) == 18
	for _, value := range fields {
		if string(value) == "null" {
			flatFields = false
		}
	}
	if err != nil || !flatFields || queueClosedJSON(raw, &prepared) != nil || prepared.Schema != "podconnect.native_room_transport.v2" || prepared.Purpose != roomReleasePurpose || prepared.Action != "release" || prepared.Percent != 0 || prepared.roomTransportIdentity != read.Owner.Owner || prepared.Writer != read.Owner.Writer || prepared.Expires != 5000 || !queueObservationPattern.MatchString(prepared.PrepareID) || !transportDigest.MatchString(prepared.Digest) || !validBoundNativeRevision(prepared.IntentRevision) || !transportRouteClaimSnapshotCurrent(read.Claim, scope) {
		http.Error(w, "release preparation unavailable", 409)
		return
	}
	// The same current route must still be the binding shown to this caller.
	current, err := readRoomRelease(ctx, scope)
	if err != nil || releaseBinding(current) != request.Binding || time.Since(started) >= 5*time.Second {
		http.Error(w, "release owner changed", 409)
		return
	}
	body, _ = json.Marshal(map[string]any{"native_prepare_id": prepared.PrepareID, "action": "release", "args_digest": prepared.Digest, "native_owner": prepared.roomTransportIdentity, "native_revision": prepared.IntentRevision, "purpose": roomReleasePurpose, "writer_proof": prepared.Writer})
	raw, err = roomTransportRPC(ctx, read.Claim.RT.room.OwnTone, http.MethodPost, "/api/outputs/"+url.PathEscape(scope.Homepod)+"/transport/bound-prepare", body)
	var output outputTransportPrepare
	if err != nil || queueClosedJSON(raw, &output) != nil || output.Schema != "podconnect.output_transport_prepare.v2" || !output.OK || !queueObservationPattern.MatchString(output.Token) || output.Identity != read.Route || output.Owner != read.Owner.Owner || output.Revision != prepared.IntentRevision || output.Expires != 10000 || time.Since(started) >= 5*time.Second || !transportRouteClaimSnapshotCurrent(read.Claim, scope) || ctx.Err() != nil {
		http.Error(w, "release output preparation unavailable", 409)
		return
	}
	result := roomReleaseResult{Schema: "podconnect.release_result.v1", PrepareID: prepared.PrepareID, Outcome: "unknown"}
	// Once dispatched, never replay this request, including an unknown HTTP ACK.
	roomTransportStore.Lock()
	roomTransportStore.ReleaseRequest = request
	roomTransportStore.ReleaseResult = result
	roomTransportStore.ReleasePresent = true
	roomTransportStore.ReleaseNativeBase = read.Claim.RT.room.Librespot
	roomTransportStore.Unlock()
	body, _ = json.Marshal(map[string]string{"prepare_id": prepared.PrepareID, "own_tone_token": output.Token})
	raw, err = roomTransportRPC(ctx, read.Claim.RT.room.Librespot, http.MethodPost, "/player/room_transport/bound-commit", body)
	var received roomReleaseResult
	if err == nil && queueClosedJSON(raw, &received) == nil && releaseResultValid(received, prepared.PrepareID) {
		result = received
	}
	writeJSON(w, storeRoomReleaseResult(request, result))
}

func storeRoomReleaseResult(request roomReleaseRequest, result roomReleaseResult) roomReleaseResult {
	roomTransportStore.Lock()
	defer roomTransportStore.Unlock()
	if roomTransportStore.ReleasePresent && roomTransportStore.ReleaseRequest == request && roomTransportStore.ReleaseResult.PrepareID == result.PrepareID {
		// A late unknown HTTP response cannot erase a terminal same-operation read.
		if roomTransportStore.ReleaseResult.Terminal {
			return roomTransportStore.ReleaseResult
		}
		roomTransportStore.ReleaseResult = result
	}
	return result
}

func refreshRoomReleaseResult(ctx context.Context, request roomReleaseRequest, base string, prior roomReleaseResult) roomReleaseResult {
	if prior.Outcome != "unknown" || base == "" {
		return prior
	}
	ctx, cancel := context.WithTimeout(ctx, 4*time.Second)
	defer cancel()
	query := url.Values{"prepare_id": {prior.PrepareID}}
	raw, err := roomTransportRPC(ctx, base, http.MethodGet, "/player/room_transport/bound-result?"+query.Encode(), nil)
	var observed roomReleaseResult
	if err == nil && queueClosedJSON(raw, &observed) == nil && releaseResultValid(observed, prior.PrepareID) {
		return storeRoomReleaseResult(request, observed)
	}
	return prior
}

func manualReleaseResultHandler(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	if len(query) != 1 || len(query["request_id"]) != 1 || !queueObservationPattern.MatchString(query.Get("request_id")) {
		http.Error(w, "release result query", http.StatusBadRequest)
		return
	}
	roomTransportStore.Lock()
	request, result, base := roomTransportStore.ReleaseRequest, roomTransportStore.ReleaseResult, roomTransportStore.ReleaseNativeBase
	present := roomTransportStore.ReleasePresent && request.RequestID == query.Get("request_id")
	roomTransportStore.Unlock()
	if !present {
		w.WriteHeader(http.StatusNotFound)
		writeJSON(w, roomReleaseResult{Schema: "podconnect.release_result.v1", Outcome: "unknown"})
		return
	}
	writeJSON(w, refreshRoomReleaseResult(r.Context(), request, base, result))
}

// The incumbent rooms read remains independent of release health unless requested.
func roomsStateHandler(w http.ResponseWriter, r *http.Request) {
	includeRelease := false
	if values, present := r.URL.Query()["include_release"]; present {
		if r.Method != http.MethodGet || len(r.URL.Query()) != 1 || len(values) != 1 || values[0] != "1" {
			http.Error(w, "release projection query", http.StatusBadRequest)
			return
		}
		includeRelease = true
	}
	if r.Method == http.MethodPost {
		roomsItemHandler(w, r)
		return
	}
	out := []roomInfo{}
	for _, rm := range loadRooms() {
		isAlias := rm.Idx != 0
		gl := librespotStatus(rm.Librespot)
		// A non-primary room has no engine of its own — don't probe it (it'd always read down). It's an
		// alias advertised on the primary; output is routed there on selection.
		up := isAlias
		if !isAlias {
			_, up = fetchOutputsFrom(rm.OwnTone)
		}
		info := roomInfo{
			ID: rm.ID, Name: rm.Name, HomepodName: rm.HomepodName, HomepodID: rm.HomepodID,
			OwntoneUp:  up,
			Alias:      isAlias,
			Playing:    gl.Active && !gl.Paused && !gl.Stopped,
			Released:   fileExists(releasedPath(rm)),
			NowPlaying: nowPlaying(rm.Librespot),
			Volume:     -1,

			GraceMinutes:      roomGrace(rm),
			GraceOverridden:   rm.GraceMinutes != nil,
			Bitrate:           roomBitrate(rm),
			BitrateOverridden: rm.Bitrate != "",
		}
		if gl.HasVol {
			info.Volume = gl.VolPct
		}
		if includeRelease {
			info.Release = roomReleaseForDisplay(r.Context(), rm)
		}
		out = append(out, info)
	}
	writeJSON(w, map[string]any{"rooms": out})
}
