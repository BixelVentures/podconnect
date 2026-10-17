package main

// Additive exact-room transport. These handlers never invoke Spotify Web API,
// select a route, prime volume, or treat a manager pre-read as the effect fence.
// Actual route admission belongs to OwnTone; final native admission belongs to
// the receiver actor and its guarded pipe/player mutator.
import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"sync"
	"time"
	"unicode/utf8"
)

var transportActions = []string{"pause", "resume", "next", "previous"}
var transportUUID = regexp.MustCompile(`^[a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12}$`)
var transportDecimal = regexp.MustCompile(`^(0|[1-9][0-9]{0,19})$`)
var transportDigest = regexp.MustCompile(`^[a-f0-9]{64}$`)

type roomTransportIdentity struct {
	Account     string `json:"account_user_id"`
	Target      string `json:"target_id"`
	Room        string `json:"room_id"`
	Homepod     string `json:"homepod_id"`
	Connection  string `json:"connection_id"`
	Session     string `json:"session_id"`
	Incarnation string `json:"alias_incarnation"`
	Registry    string `json:"alias_registry"`
	Revision    uint64 `json:"alias_revision"`
}
type outputTransportIdentity struct {
	Process     string `json:"process"`
	Device      string `json:"device_id"`
	Incarnation string `json:"device_incarnation"`
	Session     string `json:"session_epoch"`
	Route       string `json:"route_revision"`
	Selection   string `json:"selection_revision"`
	Input       string `json:"input_generation"`
	Item        string `json:"input_item_id"`
	InputDigest string `json:"input_path_digest"`
}
type receiverTransportState struct {
	Schema   string                `json:"schema"`
	Ready    bool                  `json:"ready"`
	Identity roomTransportIdentity `json:"identity"`
	Actions  []string              `json:"actions"`
	Physical bool                  `json:"physical_result_verified"`
}
type outputTransportState struct {
	Schema      string                  `json:"schema"`
	OK          bool                    `json:"ok"`
	Identity    outputTransportIdentity `json:"identity"`
	Busy        bool                    `json:"busy"`
	Actions     []string                `json:"actions"`
	Physical    bool                    `json:"physical_result_verified"`
	StartOwned  bool                    `json:"start_owned"`
	CleanAbsent bool                    `json:"clean_absent"`
}

type currentStartRoute int

const (
	startRouteUnknown currentStartRoute = iota
	startRouteOwned
	startRouteCleanAbsent
)

type receiverTransportPrepare struct {
	Schema    string `json:"schema"`
	PrepareID string `json:"prepare_id"`
	roomTransportIdentity
	Action         string `json:"action"`
	Percent        int    `json:"volume_percent"`
	Digest         string `json:"args_digest"`
	Expires        int    `json:"expires_after_ms"`
	IntentRevision string `json:"native_revision"`
}
type outputTransportPrepare struct {
	Schema   string                  `json:"schema"`
	OK       bool                    `json:"ok"`
	Token    string                  `json:"token"`
	Identity outputTransportIdentity `json:"identity"`
	Owner    roomTransportIdentity   `json:"native_owner"`
	Revision string                  `json:"native_revision"`
	Expires  int                     `json:"expires_after_ms"`
}
type roomTransportScope struct {
	Target  string `json:"target_id"`
	Room    string `json:"room_id"`
	Homepod string `json:"homepod_id"`
	Account string `json:"account_user_id"`
}
type roomTransportRequest struct {
	Scope   roomTransportScope      `json:"scope"`
	Native  roomTransportIdentity   `json:"native"`
	Route   outputTransportIdentity `json:"route"`
	Action  string                  `json:"action"`
	Percent int                     `json:"volume_percent"`
}
type roomTransportOffer struct {
	Request roomTransportRequest
	Native  receiverTransportPrepare
	Output  outputTransportPrepare
	Claim   startClaim
	Expires time.Time
}

var roomTransportStore struct {
	sync.Mutex
	Busy  bool
	Offer *roomTransportOffer
}
var roomTransportClaim = currentStartClaim
var roomTransportRPC = doQueueNativeRPC

func validTransportActions(actions []string) bool {
	if actions == nil || len(actions) > len(transportActions) {
		return false
	}
	seen := map[string]bool{}
	for _, action := range actions {
		if !slices.Contains(transportActions, action) || seen[action] {
			return false
		}
		seen[action] = true
	}
	return true
}
func validTransportScope(s roomTransportScope) bool {
	return engineIDPattern.MatchString(s.Target) && s.Room != "" && len(s.Room) <= 128 && s.Homepod != "" && len(s.Homepod) <= 128 && s.Account != "" && len(s.Account) <= 160
}
func validTransportIdentity(i roomTransportIdentity, s roomTransportScope) bool {
	return i.Account == s.Account && i.Target == s.Target && i.Room == s.Room && i.Homepod == s.Homepod && i.Connection != "" && len(i.Connection) <= 160 && i.Session != "" && len(i.Session) <= 160 && i.Incarnation != "" && len(i.Incarnation) <= 160 && transportDigest.MatchString(i.Registry) && i.Revision < 1<<53
}
func validOutputTransportIdentity(i outputTransportIdentity, homepod string) bool {
	return transportUUID.MatchString(i.Process) && i.Device == homepod && transportDecimal.MatchString(i.Device) && transportDecimal.MatchString(i.Incarnation) && transportDecimal.MatchString(i.Session) && transportDecimal.MatchString(i.Route) && transportDecimal.MatchString(i.Selection) && transportDecimal.MatchString(i.Input) && transportDecimal.MatchString(i.Item) && transportDigest.MatchString(i.InputDigest)
}
func configuredTransportInput(claim startClaim) string {
	if claim.RT == nil || claim.RT.room == nil || claim.RT.room.Pipe == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(claim.RT.room.Pipe))
	return hex.EncodeToString(sum[:])
}
func transportClaimMatches(claim startClaim, s roomTransportScope) bool {
	return claim.RT != nil && claim.Proof.Mode == "same_session" && claim.Proof.OutputSelected && claim.Proof.EngineDeviceID == s.Target && claim.Proof.RoomID == s.Room && claim.Proof.HomepodID == s.Homepod && claim.Proof.AliasBinding != nil && claim.Proof.AliasBinding.RoomID == s.Room
}
func transportClaimCurrent(claim startClaim, s roomTransportScope) bool {
	next, ok := roomTransportClaim(s.Room, s.Target)
	return ok && transportClaimMatches(next, s) && next.RT == claim.RT && next.Intent == claim.Intent && claim.RT.live.startIntentCurrent(claim.Intent) && sameAliasBinding(next.Proof.AliasBinding, claim.Proof.AliasBinding)
}
func transportRouteClaimSnapshotCurrent(claim startClaim, s roomTransportScope) bool {
	if !claim.Released {
		return transportClaimCurrent(claim, s)
	}
	next, ok := currentRoomTransportClaim(s.Room, s.Target)
	return ok && next.Released && transportClaimMatches(next, s) && next.RT == claim.RT &&
		next.Intent == claim.Intent && next.Paused == claim.Paused && claim.RT.live.startIntentCurrent(claim.Intent) &&
		sameAliasBinding(next.Proof.AliasBinding, claim.Proof.AliasBinding)
}

// A released route is only a preliminary claim. The caller must join a fresh
// receiver session with C's current START route before offering any action.
func readRoomTransportState(ctx context.Context, s roomTransportScope) (startClaim, receiverTransportState, outputTransportState, currentStartRoute, error) {
	var n receiverTransportState
	var o outputTransportState
	claim, ok := roomTransportClaim(s.Room, s.Target)
	if !ok || claim.Released && claim.Proof.Mode != "same_session" {
		claim, ok = currentRoomTransportClaim(s.Room, s.Target)
	}
	if !validTransportScope(s) || !ok || !transportClaimMatches(claim, s) {
		return claim, n, o, startRouteUnknown, fmt.Errorf("room transport unavailable")
	}
	q := url.Values{"account_user_id": {s.Account}, "target_id": {s.Target}, "room_id": {s.Room}, "homepod_id": {s.Homepod}}
	raw, err := roomTransportRPC(ctx, claim.RT.room.Librespot, http.MethodGet, "/player/room_transport/bound-state?"+q.Encode(), nil)
	if err != nil || decodeQueueManagerJSON(raw, &n) != nil || n.Schema != "podconnect.native_room_transport_state.v2" || !n.Ready || n.Physical || !validTransportIdentity(n.Identity, s) || !validTransportActions(n.Actions) || n.Identity.Incarnation != claim.Proof.AliasBinding.Incarnation || n.Identity.Registry != claim.Proof.AliasBinding.Registry {
		return claim, n, o, startRouteUnknown, fmt.Errorf("native room transport unavailable")
	}
	boundState, _ := json.Marshal(map[string]any{"native_owner": n.Identity})
	raw, err = roomTransportRPC(ctx, claim.RT.room.OwnTone, http.MethodPost, "/api/outputs/"+url.PathEscape(s.Homepod)+"/transport/bound-state", boundState)
	if err != nil || decodeQueueManagerJSON(raw, &o) != nil || o.Schema != "podconnect.output_transport_state.v2" || !o.OK || o.Physical || o.Busy || o.StartOwned && o.CleanAbsent || !validOutputTransportIdentity(o.Identity, s.Homepod) || o.Identity.InputDigest != configuredTransportInput(claim) || !validTransportActions(o.Actions) || !transportRouteClaimSnapshotCurrent(claim, s) {
		return claim, n, o, startRouteUnknown, fmt.Errorf("output room transport unavailable")
	}
	if o.StartOwned {
		return claim, n, o, startRouteOwned, nil
	}
	if o.CleanAbsent {
		return claim, n, o, startRouteCleanAbsent, nil
	}
	return claim, n, o, startRouteUnknown, nil
}

func readRoomTransport(ctx context.Context, s roomTransportScope) (startClaim, receiverTransportState, outputTransportState, error) {
	claim, n, o, state, err := readRoomTransportState(ctx, s)
	if err != nil || claim.Released && state != startRouteOwned {
		return claim, n, o, fmt.Errorf("room transport owner unavailable")
	}
	return claim, n, o, nil
}

func transportRouteClaimCurrent(ctx context.Context, claim startClaim, s roomTransportScope, expectedNative roomTransportIdentity, expectedRoute outputTransportIdentity) bool {
	if !transportRouteClaimSnapshotCurrent(claim, s) {
		return false
	}
	if !claim.Released {
		return true
	}
	latest, n, o, state, err := readRoomTransportState(ctx, s)
	return err == nil && state == startRouteOwned && latest.RT == claim.RT && latest.Intent == claim.Intent &&
		latest.Paused == claim.Paused &&
		n.Identity == expectedNative && o.Identity == expectedRoute &&
		sameAliasBinding(latest.Proof.AliasBinding, claim.Proof.AliasBinding)
}

// The bridge does not know the account from a display name or selected output.
// Read the native AP identity, then let the receiver's bound-state prove its
// current session before C joins that exact owner to the current output route.
func currentNativeAccount(ctx context.Context, base, engineID string) string {
	raw, err := roomTransportRPC(ctx, base, http.MethodGet, "/status", nil)
	if err != nil || len(raw) > 65536 || !utf8.Valid(raw) {
		return ""
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	if token, err := decoder.Token(); err != nil || token != json.Delim('{') {
		return ""
	}
	seen := map[string]bool{}
	account, observedEngine := "", ""
	for decoder.More() {
		token, err := decoder.Token()
		key, ok := token.(string)
		if err != nil || !ok || seen[key] {
			return ""
		}
		seen[key] = true
		var value json.RawMessage
		if decoder.Decode(&value) != nil {
			return ""
		}
		if key == "username" && json.Unmarshal(value, &account) != nil {
			return ""
		}
		if key == "device_id" && json.Unmarshal(value, &observedEngine) != nil {
			return ""
		}
	}
	if token, err := decoder.Token(); err != nil || token != json.Delim('}') {
		return ""
	}
	if _, err := decoder.Token(); err != io.EOF || observedEngine != engineID ||
		len(account) > 160 || canonicalUsernameDigest(account) == "" {
		return ""
	}
	return account
}

// This result is intentionally not cached: a positive only suppresses this
// tick's legacy route writes while the persistent released marker remains.
func bridgeCurrentStartRoute(ctx context.Context, primary *Room, status glStatus, intent glTransportIntent) currentStartRoute {
	if primary == nil || primary.Idx != 0 || mgr == nil || status.AliasBinding == nil ||
		!status.AliasBinding.valid() || !status.Active || status.Paused || status.Stopped ||
		!fileExists(releasedPath(primary)) {
		return startRouteUnknown
	}
	rt := mgr.runtime(primary.ID)
	selected := roomByID(status.AliasBinding.RoomID)
	if rt == nil || selected == nil || selected.HomepodID == "" || !rt.live.startIntentCurrent(intent) {
		return startRouteUnknown
	}
	target := configuredEngineID(primary)
	if target == "" {
		return startRouteUnknown
	}
	account := currentNativeAccount(ctx, primary.Librespot, target)
	if account == "" || !rt.live.startIntentCurrent(intent) {
		return startRouteUnknown
	}
	scope := roomTransportScope{target, selected.ID, selected.HomepodID, account}
	claim, native, route, state, err := readRoomTransportState(ctx, scope)
	latest := roomByID(selected.ID)
	if err != nil || !claim.Released || claim.RT != rt || claim.Intent != intent ||
		claim.Proof.AliasBinding == nil || !sameAliasBinding(claim.Proof.AliasBinding, status.AliasBinding) ||
		native.Identity.Account != account || route.Identity.Device != selected.HomepodID ||
		latest == nil || latest.HomepodID != selected.HomepodID ||
		!rt.live.startIntentCurrent(intent) ||
		!fileExists(releasedPath(primary)) {
		return startRouteUnknown
	}
	return state
}

// A released, playing primary may enter ordinary recovery only after explicit
// clean absence. Owned and unknown keep the marker and skip every legacy write.
func holdReleasedBridgeRoute(primary *Room, status glStatus, intent glTransportIntent) bool {
	if primary == nil || primary.Idx != 0 || !fileExists(releasedPath(primary)) {
		return false
	}
	if !status.Active || status.Paused || status.Stopped {
		return true
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	return bridgeCurrentStartRoute(ctx, primary, status, intent) != startRouteCleanAbsent
}
func transportStateHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method", 405)
		return
	}
	q := r.URL.Query()
	s := roomTransportScope{q.Get("target_id"), q.Get("room_id"), q.Get("homepod_id"), q.Get("account_user_id")}
	if len(q) != 4 || !validTransportScope(s) {
		http.Error(w, "arguments", 400)
		return
	}
	for _, v := range q {
		if len(v) != 1 {
			http.Error(w, "arguments", 400)
			return
		}
	}
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	claim, n, o, err := readRoomTransport(ctx, s)
	if err != nil {
		http.Error(w, "room transport unavailable", 409)
		return
	}
	actions := []string{}
	if !o.Busy {
		for _, a := range n.Actions {
			if slices.Contains(o.Actions, a) && !(claim.Released && claim.Paused && a != "resume") {
				actions = append(actions, a)
			}
		}
	}
	writeJSON(w, map[string]any{"schema": "podconnect.room_transport_state.v1", "ok": true, "native": n.Identity, "route": o.Identity, "actions": actions, "physical_result_verified": false})
}
func decodeTransportBody(r *http.Request, out any) error {
	if r.URL.RawQuery != "" {
		return fmt.Errorf("query rejected")
	}
	raw, err := io.ReadAll(io.LimitReader(r.Body, 8193))
	defer r.Body.Close()
	if err != nil || len(raw) > 8192 {
		return fmt.Errorf("body rejected")
	}
	return decodeQueueManagerJSON(raw, out)
}
func acquireRoomTransport() bool {
	roomTransportStore.Lock()
	defer roomTransportStore.Unlock()
	if roomTransportStore.Busy {
		return false
	}
	roomTransportStore.Busy = true
	return true
}
func releaseRoomTransport() {
	roomTransportStore.Lock()
	roomTransportStore.Busy = false
	roomTransportStore.Unlock()
}
func transportPrepareHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method", 405)
		return
	}
	var body roomTransportRequest
	if decodeTransportBody(r, &body) != nil || !validTransportScope(body.Scope) || !validTransportIdentity(body.Native, body.Scope) || !validOutputTransportIdentity(body.Route, body.Scope.Homepod) || !slices.Contains(transportActions, body.Action) || body.Percent < 0 || body.Percent > 100 || body.Action != "volume" && body.Percent != 0 {
		http.Error(w, "arguments", 400)
		return
	}
	if !acquireRoomTransport() {
		http.Error(w, "busy", 409)
		return
	}
	defer releaseRoomTransport()
	roomTransportStore.Lock()
	roomTransportStore.Offer = nil
	roomTransportStore.Unlock()
	ctx, cancel := context.WithTimeout(r.Context(), 12*time.Second)
	defer cancel()
	claim, n, o, err := readRoomTransport(ctx, body.Scope)
	if err != nil || n.Identity != body.Native || o.Identity != body.Route || o.Busy ||
		claim.Released && claim.Paused && body.Action != "resume" ||
		!slices.Contains(n.Actions, body.Action) || !slices.Contains(o.Actions, body.Action) {
		http.Error(w, "stale room transport", 409)
		return
	}
	request := map[string]any{"account_user_id": body.Scope.Account, "target_id": body.Scope.Target, "room_id": body.Scope.Room, "homepod_id": body.Scope.Homepod, "action": body.Action, "volume_percent": body.Percent}
	encoded, _ := json.Marshal(request)
	started := time.Now()
	raw, err := roomTransportRPC(ctx, claim.RT.room.Librespot, http.MethodPost, "/player/room_transport/bound-prepare", encoded)
	var prepared receiverTransportPrepare
	// Embedded structs are decoded by encoding/json, but strict field presence must
	// validate their flattened identity independently of queue's struct walker.
	if err != nil || decodeTransportPrepare(raw, &prepared) != nil || prepared.Schema != "podconnect.native_room_transport.v2" || !queueObservationPattern.MatchString(prepared.PrepareID) || prepared.roomTransportIdentity != body.Native || prepared.Action != body.Action || prepared.Percent != body.Percent || !transportDigest.MatchString(prepared.Digest) || prepared.Expires != 5000 || !validBoundNativeRevision(prepared.IntentRevision) || !transportRouteClaimCurrent(ctx, claim, body.Scope, body.Native, body.Route) {
		http.Error(w, "native preparation unavailable", 409)
		return
	}
	outputArgs := map[string]any{"native_prepare_id": prepared.PrepareID, "action": body.Action, "args_digest": prepared.Digest, "native_owner": prepared.roomTransportIdentity, "native_revision": prepared.IntentRevision}
	if body.Action == "volume" {
		outputArgs["percent"] = body.Percent
	}
	encoded, _ = json.Marshal(outputArgs)
	raw, err = roomTransportRPC(ctx, claim.RT.room.OwnTone, http.MethodPost, "/api/outputs/"+url.PathEscape(body.Scope.Homepod)+"/transport/bound-prepare", encoded)
	var output outputTransportPrepare
	remaining := 5000 - int(time.Since(started).Milliseconds())
	if err != nil || decodeQueueManagerJSON(raw, &output) != nil || output.Schema != "podconnect.output_transport_prepare.v2" || !output.OK || !queueObservationPattern.MatchString(output.Token) || output.Identity != body.Route || output.Expires != 10000 || output.Owner != prepared.roomTransportIdentity || output.Revision != prepared.IntentRevision || remaining <= 0 || !transportRouteClaimCurrent(ctx, claim, body.Scope, body.Native, body.Route) {
		http.Error(w, "output preparation unavailable", 409)
		return
	}
	roomTransportStore.Lock()
	roomTransportStore.Offer = &roomTransportOffer{body, prepared, output, claim, started.Add(5 * time.Second)}
	roomTransportStore.Unlock()
	writeJSON(w, map[string]any{"schema": "podconnect.room_transport_prepare.v1", "ok": true, "prepare_id": prepared.PrepareID, "expires_after_ms": remaining, "native": body.Native, "route": body.Route, "action": body.Action, "volume_percent": body.Percent, "physical_result_verified": false})
}

// queueRequiredJSON intentionally skips anonymous fields; explicitly require
// every flat native field, then use the same duplicate/unknown/trailing guard.
func decodeTransportPrepare(raw []byte, out *receiverTransportPrepare) error {
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil || len(fields) != 16 {
		return fmt.Errorf("native fields rejected")
	}
	for _, key := range []string{"schema", "prepare_id", "account_user_id", "target_id", "room_id", "homepod_id", "connection_id", "session_id", "alias_incarnation", "alias_registry", "alias_revision", "action", "volume_percent", "args_digest", "expires_after_ms", "native_revision"} {
		if v, ok := fields[key]; !ok || string(v) == "null" {
			return fmt.Errorf("native field missing")
		}
	}
	return decodeQueueManagerJSON(raw, out)
}
func transportCommitHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method", 405)
		return
	}
	var body struct {
		PrepareID string `json:"prepare_id"`
	}
	if decodeTransportBody(r, &body) != nil || !queueObservationPattern.MatchString(body.PrepareID) {
		http.Error(w, "arguments", 400)
		return
	}
	if !acquireRoomTransport() {
		http.Error(w, "busy", 409)
		return
	}
	defer releaseRoomTransport()
	roomTransportStore.Lock()
	offer := roomTransportStore.Offer
	if offer != nil && offer.Native.PrepareID == body.PrepareID {
		roomTransportStore.Offer = nil
	} else {
		offer = nil
	}
	roomTransportStore.Unlock()
	if offer == nil || !time.Now().Before(offer.Expires) {
		http.Error(w, "stale room transport", 409)
		return
	}
	ctx, cancel := context.WithDeadline(r.Context(), offer.Expires)
	defer cancel()
	if !transportRouteClaimCurrent(ctx, offer.Claim, offer.Request.Scope, offer.Request.Native, offer.Request.Route) {
		http.Error(w, "stale room transport", 409)
		return
	}
	encoded, _ := json.Marshal(map[string]any{"prepare_id": body.PrepareID, "own_tone_token": offer.Output.Token})
	// This is the only possible-effect dispatch. Every result after it is native
	// acceptance or unknown, never a safe rejection inferred from an HTTP error.
	raw, err := roomTransportRPC(ctx, offer.Claim.RT.room.Librespot, http.MethodPost, "/player/room_transport/bound-commit", encoded)
	var ack struct {
		Schema    string `json:"schema"`
		Accepted  bool   `json:"accepted_native"`
		PrepareID string `json:"prepare_id"`
		Physical  bool   `json:"physical_result_verified"`
	}
	accepted := err == nil && decodeQueueManagerJSON(raw, &ack) == nil && ack.Schema == "podconnect.native_room_transport_commit.v2" && ack.Accepted && ack.PrepareID == body.PrepareID && !ack.Physical
	outcome := "unknown"
	if accepted {
		outcome = "accepted"
	}
	writeJSON(w, map[string]any{"schema": "podconnect.room_transport_commit.v1", "ok": accepted, "prepare_id": body.PrepareID, "outcome": outcome, "accepted_native": accepted, "retry_allowed": false, "physical_result_verified": false})
}

// The revision is minted by the actual native PREPARE, never by this caller.
func validBoundNativeRevision(value string) bool {
	if !transportDecimal.MatchString(value) {
		return false
	}
	n, err := strconv.ParseUint(value, 10, 64)
	return err == nil && n > 0
}
