package main

// Room queue selection is a separate, one-use bound transport purpose. Read
// observes native and C ownership; prepare stages one exact occurrence without
// an effect; commit is the only possible-effect dispatch.
import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

const roomQueuePurpose = "queue_occurrence_v1"
const roomQueueAutoplayHeader = "X-Podconnect-Queue-Autoplay"

type queueSelection struct {
	Observation string `json:"observation_id"`
	Session     string `json:"session_id"`
	Revision    uint64 `json:"queue_revision"`
	Occurrence  string `json:"occurrence_id"`
}
type queueProcessProof struct {
	BootID string `json:"boot_id"`
	PID    string `json:"pid"`
	Start  string `json:"proc_start_ticks"`
}
type queuePipeProof struct {
	Device string `json:"device"`
	Inode  string `json:"inode"`
	Type   string `json:"type"`
}
type queueWriterProof struct {
	Process        queueProcessProof `json:"native_process"`
	FD             string            `json:"writer_fd"`
	Pipe           queuePipeProof    `json:"pipe_identity"`
	Epoch          string            `json:"source_epoch"`
	OpenGeneration string            `json:"open_generation"`
	InputDigest    string            `json:"input_path_digest"`
}
type queueDisplayMetadata struct {
	Name    string `json:"name"`
	Artist  string `json:"artist"`
	Artwork string `json:"artwork,omitempty"`
}
type nativeRoomQueueState struct {
	Schema      string                          `json:"schema"`
	Ready       bool                            `json:"ready"`
	Purpose     string                          `json:"purpose"`
	Owner       roomTransportIdentity           `json:"native_owner"`
	Observation string                          `json:"observation_id"`
	Revision    uint64                          `json:"queue_revision"`
	Expires     int                             `json:"expires_after_ms"`
	Items       []queueNativeItem               `json:"items"`
	Metadata    map[string]queueDisplayMetadata `json:"metadata,omitempty"`
	Truncated   bool                            `json:"truncated"`
	Writer      queueWriterProof                `json:"writer_proof"`
	Physical    bool                            `json:"physical_result_verified"`
	Autoplay    *json.RawMessage                `json:"autoplay,omitempty"`
}

type queueAutoplayDisplay struct {
	Kind       string            `json:"kind"`
	DisplayIDs map[string]string `json:"display_ids,omitempty"`
}

type nativeQueueAutoplayPreview struct {
	Kind        string                          `json:"kind"`
	DisplayIDs  map[string]string               `json:"display_ids"`
	Observation string                          `json:"observation_id"`
	Revision    uint64                          `json:"queue_revision"`
	Expires     int                             `json:"expires_after_ms"`
	Items       []queueNativeItem               `json:"items"`
	Truncated   bool                            `json:"truncated"`
	Metadata    map[string]queueDisplayMetadata `json:"metadata,omitempty"`
}

type nativeQueueAutoplayPromotion struct {
	Kind       string                          `json:"kind"`
	DisplayIDs map[string]string               `json:"display_ids"`
	Metadata   map[string]queueDisplayMetadata `json:"metadata,omitempty"`
}
type outputRoomQueueState struct {
	Schema   string                  `json:"schema"`
	OK       bool                    `json:"ok"`
	Purpose  string                  `json:"purpose"`
	Identity outputTransportIdentity `json:"identity"`
	Busy     bool                    `json:"busy"`
	Actions  []string                `json:"actions"`
	Owner    roomTransportIdentity   `json:"native_owner"`
	Writer   queueWriterProof        `json:"writer_proof"`
	Physical bool                    `json:"physical_result_verified"`
}
type nativeRoomQueuePrepare struct {
	Schema    string                `json:"schema"`
	Purpose   string                `json:"purpose"`
	PrepareID string                `json:"prepare_id"`
	Owner     roomTransportIdentity `json:"native_owner"`
	Revision  string                `json:"native_revision"`
	Action    string                `json:"action"`
	Percent   int                   `json:"volume_percent"`
	Digest    string                `json:"args_digest"`
	Expires   int                   `json:"expires_after_ms"`
	Queue     queueSelection        `json:"queue"`
	Writer    queueWriterProof      `json:"writer_proof"`
}
type outputRoomQueuePrepare struct {
	Schema    string                  `json:"schema"`
	OK        bool                    `json:"ok"`
	Purpose   string                  `json:"purpose"`
	Token     string                  `json:"token"`
	Identity  outputTransportIdentity `json:"identity"`
	Owner     roomTransportIdentity   `json:"native_owner"`
	Revision  string                  `json:"native_revision"`
	PrepareID string                  `json:"native_prepare_id"`
	Digest    string                  `json:"args_digest"`
	Queue     queueSelection          `json:"queue"`
	Writer    queueWriterProof        `json:"writer_proof"`
	Expires   int                     `json:"expires_after_ms"`
}
type roomQueueRead struct {
	Native   nativeRoomQueueState
	Route    outputTransportIdentity
	Claim    startClaim
	Expires  time.Time
	Autoplay bool
}
type roomQueuePrepareRequest struct {
	Scope  roomTransportScope      `json:"scope"`
	Native roomTransportIdentity   `json:"native"`
	Route  outputTransportIdentity `json:"route"`
	Queue  queueSelection          `json:"queue"`
}
type roomQueuePrepared struct {
	Read    roomQueueRead
	Native  nativeRoomQueuePrepare
	Output  outputRoomQueuePrepare
	Expires time.Time
}

var roomQueueStore struct {
	sync.Mutex
	Read     *roomQueueRead
	Prepared *roomQueuePrepared
}
var roomQueueClaim = currentStartClaim
var roomQueueRPC = doRoomQueueRPC

func queueClosedJSON(raw []byte, out any) error {
	if len(raw) == 0 || len(raw) > 16384 || queueDuplicateJSON(raw) != nil {
		return fmt.Errorf("queue JSON invalid")
	}
	return decodeQueueManagerJSON(raw, out)
}

// encoding/json accepts duplicate nested members; this walk closes that gap
// before the existing required-field and unknown-field decoder runs.
func queueDuplicateJSON(raw []byte) error {
	d := json.NewDecoder(bytes.NewReader(raw))
	var walk func() error
	walk = func() error {
		tok, err := d.Token()
		if err != nil {
			return err
		}
		switch tok {
		case json.Delim('{'):
			seen := map[string]bool{}
			for d.More() {
				key, err := d.Token()
				name, ok := key.(string)
				if err != nil || !ok || seen[name] {
					return fmt.Errorf("duplicate member")
				}
				seen[name] = true
				if err := walk(); err != nil {
					return err
				}
			}
			_, err := d.Token()
			return err
		case json.Delim('['):
			for d.More() {
				if err := walk(); err != nil {
					return err
				}
			}
			_, err := d.Token()
			return err
		default:
			return nil
		}
	}
	if err := walk(); err != nil {
		return err
	}
	if _, err := d.Token(); err != io.EOF {
		return fmt.Errorf("trailing queue JSON")
	}
	return nil
}

func queuePositiveDecimal(value string) bool {
	if !transportDecimal.MatchString(value) {
		return false
	}
	n, err := strconv.ParseUint(value, 10, 64)
	return err == nil && n > 0
}
func queueUnsignedDecimal(value string) bool {
	if !transportDecimal.MatchString(value) {
		return false
	}
	_, err := strconv.ParseUint(value, 10, 64)
	return err == nil
}
func queueRouteValid(route outputTransportIdentity, homepod string) bool {
	return validOutputTransportIdentity(route, homepod) && queuePositiveDecimal(route.Device) && queuePositiveDecimal(route.Incarnation) && queuePositiveDecimal(route.Session) && queueUnsignedDecimal(route.Route) && queueUnsignedDecimal(route.Selection) && queuePositiveDecimal(route.Input) && queuePositiveDecimal(route.Item)
}
func queueOwnerText(value string, limit int) bool {
	if value == "" || len(value) > limit {
		return false
	}
	for i := 0; i < len(value); i++ {
		if value[i] < 0x20 || value[i] > 0x7e || value[i] == '"' || value[i] == '\\' {
			return false
		}
	}
	return true
}
func queueOwnerSafe(owner roomTransportIdentity) bool {
	if !queueOwnerText(owner.Account, 160) {
		return false
	}
	for _, value := range []string{owner.Target, owner.Room, owner.Connection, owner.Session} {
		if !queueOwnerText(value, 255) {
			return false
		}
	}
	return queuePositiveDecimal(owner.Homepod) && queueObservationPattern.MatchString(owner.Incarnation)
}
func validQueueWriter(w queueWriterProof, claim startClaim) bool {
	if !transportUUID.MatchString(w.Process.BootID) || !queuePositiveDecimal(w.Process.PID) || !queuePositiveDecimal(w.Process.Start) || !queueUnsignedDecimal(w.FD) || !queuePositiveDecimal(w.Pipe.Device) || !queuePositiveDecimal(w.Pipe.Inode) || w.Pipe.Type != "fifo" || !queueUnsignedDecimal(w.Epoch) || !queuePositiveDecimal(w.OpenGeneration) || !transportDigest.MatchString(w.InputDigest) || w.InputDigest != configuredTransportInput(claim) {
		return false
	}
	pid, _ := strconv.ParseUint(w.Process.PID, 10, 32)
	fd, _ := strconv.ParseUint(w.FD, 10, 32)
	return pid > 0 && pid <= 1<<31-1 && fd <= 1<<31-1
}
func validQueueItems(items []queueNativeItem) bool {
	if items == nil || len(items) > 20 {
		return false
	}
	seen := map[string]bool{}
	for i, item := range items {
		if !queueOptionPattern.MatchString(item.ID) || seen[item.ID] || item.Position != i || !queueTrackURI.MatchString(item.URI) {
			return false
		}
		seen[item.ID] = true
	}
	return true
}
func validQueueSelection(s queueSelection, state nativeRoomQueueState) bool {
	if !queueObservationPattern.MatchString(s.Observation) || !queueOptionPattern.MatchString(s.Occurrence) || s.Observation != state.Observation || s.Session != state.Owner.Session || s.Revision != state.Revision {
		return false
	}
	for _, item := range state.Items {
		if item.ID == s.Occurrence {
			return true
		}
	}
	return false
}
func validQueueState(n nativeRoomQueueState, claim startClaim, scope roomTransportScope) bool {
	return n.Schema == "podconnect.native_room_queue_state.v1" && n.Ready && n.Purpose == roomQueuePurpose && !n.Physical && validTransportIdentity(n.Owner, scope) && queueOwnerSafe(n.Owner) && n.Owner.Incarnation == claim.Proof.AliasBinding.Incarnation && n.Owner.Registry == claim.Proof.AliasBinding.Registry && queueObservationPattern.MatchString(n.Observation) && n.Revision < 1<<53 && n.Expires > 0 && n.Expires <= 10000 && validQueueItems(n.Items) && validQueueWriter(n.Writer, claim)
}

// projectQueueAutoplay changes queue authority only for a fully bound preview.
func projectQueueAutoplay(n *nativeRoomQueueState, opted bool) (queueAutoplayDisplay, bool) {
	if n.Autoplay == nil {
		return queueAutoplayDisplay{}, true
	}
	var discriminator struct {
		Kind string `json:"kind"`
	}
	if !opted || json.Unmarshal(*n.Autoplay, &discriminator) != nil {
		return queueAutoplayDisplay{}, false
	}
	var ids map[string]string
	var metadata map[string]queueDisplayMetadata
	switch discriminator.Kind {
	case "pending":
		if queueClosedJSON(*n.Autoplay, &discriminator) != nil || len(n.Items) != 0 || n.Truncated {
			return queueAutoplayDisplay{}, false
		}
		n.Autoplay = nil
		return queueAutoplayDisplay{Kind: "pending"}, true
	case "preview":
		var preview nativeQueueAutoplayPreview
		if queueClosedJSON(*n.Autoplay, &preview) != nil || len(n.Items) != 0 || n.Truncated ||
			!queueObservationPattern.MatchString(preview.Observation) || preview.Observation == n.Observation ||
			preview.Revision != n.Revision || preview.Expires < 1 || preview.Expires > 10000 ||
			len(preview.Items) == 0 || !validQueueItems(preview.Items) || len(preview.DisplayIDs) != len(preview.Items) {
			return queueAutoplayDisplay{}, false
		}
		for _, item := range preview.Items {
			if item.Queued {
				return queueAutoplayDisplay{}, false
			}
		}
		n.Observation, n.Expires, n.Items, n.Truncated = preview.Observation, min(n.Expires, preview.Expires), preview.Items, preview.Truncated
		n.Metadata = nil
		ids, metadata = preview.DisplayIDs, preview.Metadata
	case "promotion":
		var promotion nativeQueueAutoplayPromotion
		if queueClosedJSON(*n.Autoplay, &promotion) != nil {
			return queueAutoplayDisplay{}, false
		}
		ids, metadata = promotion.DisplayIDs, promotion.Metadata
	default:
		return queueAutoplayDisplay{}, false
	}
	if len(ids) == 0 || len(ids) > len(n.Items) {
		return queueAutoplayDisplay{}, false
	}
	seen := make(map[string]bool, len(ids))
	uris := make(map[string]bool, len(ids))
	for _, item := range n.Items {
		if display, ok := ids[item.ID]; ok {
			suffix, ok := strings.CutPrefix(display, "display-")
			if !ok || !queueOptionPattern.MatchString(suffix) || seen[display] || item.Queued {
				return queueAutoplayDisplay{}, false
			}
			seen[display], uris[item.URI] = true, true
		}
	}
	if len(seen) != len(ids) {
		return queueAutoplayDisplay{}, false
	}
	for uri, label := range metadata {
		if !uris[uri] {
			return queueAutoplayDisplay{}, false
		}
		if n.Metadata == nil {
			n.Metadata = make(map[string]queueDisplayMetadata)
		}
		n.Metadata[uri] = label
	}
	n.Autoplay = nil
	return queueAutoplayDisplay{discriminator.Kind, ids}, true
}

func validQueueOutputState(o outputRoomQueueState, n nativeRoomQueueState, claim startClaim, scope roomTransportScope) bool {
	return o.Schema == "podconnect.output_queue_state.v1" && o.OK && o.Purpose == roomQueuePurpose && !o.Physical && !o.Busy && len(o.Actions) == 1 && o.Actions[0] == "queue" && o.Owner == n.Owner && o.Writer == n.Writer && queueRouteValid(o.Identity, scope.Homepod) && o.Identity.InputDigest == configuredTransportInput(claim)
}
func roomQueueCurrent(read roomQueueRead, scope roomTransportScope) bool {
	return roomQueueClaimCurrent(read.Claim, scope) && time.Now().Before(read.Expires)
}
func roomQueueClaimCurrent(claim startClaim, scope roomTransportScope) bool {
	next, ok := roomQueueClaim(scope.Room, scope.Target)
	return ok && transportClaimMatches(next, scope) && next.RT == claim.RT && next.Intent == claim.Intent && claim.RT.live.startIntentCurrent(claim.Intent) && sameAliasBinding(next.Proof.AliasBinding, claim.Proof.AliasBinding)
}
func roomQueueScopeQuery(q url.Values) (roomTransportScope, bool) {
	s := roomTransportScope{q.Get("target_id"), q.Get("room_id"), q.Get("homepod_id"), q.Get("account_user_id")}
	if len(q) != 4 || !validTransportScope(s) || !queuePositiveDecimal(s.Homepod) {
		return s, false
	}
	for _, values := range q {
		if len(values) != 1 {
			return s, false
		}
	}
	return s, true
}
func roomQueueStateHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		roomReject(w, "room_queue_state", "method", "method", 405)
		return
	}
	scope, ok := roomQueueScopeQuery(r.URL.Query())
	if !ok {
		roomReject(w, "room_queue_state", "arguments", "arguments", 400)
		return
	}
	if !acquireRoomQueue() {
		roomReject(w, "room_queue_state", "busy", "busy", 409)
		return
	}
	defer releaseRoomQueue()
	roomQueueStore.Lock()
	roomQueueStore.Read = nil
	roomQueueStore.Unlock()
	claim, ok := roomQueueClaim(scope.Room, scope.Target)
	if !ok || !transportClaimMatches(claim, scope) {
		roomReject(w, "room_queue_state", "route", "route", 409)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	started := time.Now()
	q := url.Values{"account_user_id": {scope.Account}, "target_id": {scope.Target}, "room_id": {scope.Room}, "homepod_id": {scope.Homepod}}
	values := r.Header.Values(roomQueueAutoplayHeader)
	opted := len(values) == 1 && values[0] == "1"
	raw, err := roomQueueRPC(ctx, claim.RT.room.Librespot, http.MethodGet, "/player/room_transport/bound-queue?"+q.Encode(), nil, opted)
	var n nativeRoomQueueState
	if err != nil || queueClosedJSON(raw, &n) != nil || !validQueueState(n, claim, scope) || !roomQueueClaimCurrent(claim, scope) {
		roomReject(w, "room_queue_state", "native_room_queue_unavailable", "native room queue unavailable", 409)
		return
	}
	display, valid := projectQueueAutoplay(&n, opted)
	if !valid {
		roomReject(w, "room_queue_state", "native_room_queue_unavailable", "native room queue unavailable", 409)
		return
	}
	remaining := n.Expires - int(time.Since(started).Milliseconds())
	if remaining <= 0 {
		roomReject(w, "room_queue_state", "expired", "expired", 409)
		return
	}
	stateBody, _ := json.Marshal(map[string]any{"native_owner": n.Owner, "writer_proof": n.Writer})
	raw, err = roomQueueRPC(ctx, claim.RT.room.OwnTone, http.MethodPost, "/api/outputs/"+url.PathEscape(scope.Homepod)+"/transport/bound-state", stateBody, false)
	var o outputRoomQueueState
	if err != nil || queueClosedJSON(raw, &o) != nil || !validQueueOutputState(o, n, claim, scope) || !roomQueueClaimCurrent(claim, scope) {
		roomReject(w, "room_queue_state", "output_room_queue_unavailable", "output room queue unavailable", 409)
		return
	}
	remaining = n.Expires - int(time.Since(started).Milliseconds())
	if remaining <= 0 {
		roomReject(w, "room_queue_state", "expired", "expired", 409)
		return
	}
	actions := []string{}
	if display.Kind != "pending" {
		read := roomQueueRead{n, o.Identity, claim, started.Add(time.Duration(n.Expires) * time.Millisecond), display.Kind == "preview"}
		roomQueueStore.Lock()
		roomQueueStore.Read = &read
		roomQueueStore.Unlock()
		actions = append(actions, "play_queue_occurrence")
	}
	w.Header().Set("Cache-Control", "no-store")
	queue := map[string]any{"observation_id": n.Observation, "queue_revision": n.Revision, "expires_after_ms": remaining, "items": n.Items, "truncated": n.Truncated}
	if values := r.Header.Values("X-Podconnect-Queue-Metadata"); len(values) == 1 && values[0] == "1" && len(n.Metadata) > 0 {
		queue["metadata"] = n.Metadata
	}
	response := map[string]any{"schema": "podconnect.room_queue_state.v1", "ok": true, "purpose": roomQueuePurpose, "native": n.Owner, "route": o.Identity, "queue": queue, "actions": actions, "physical_result_verified": false}
	if display.Kind != "" {
		response["autoplay"] = display
	}
	writeJSON(w, response)
}

func decodeRoomQueueBody(r *http.Request, out any) error {
	if r.URL.RawQuery != "" {
		return fmt.Errorf("query rejected")
	}
	raw, err := io.ReadAll(io.LimitReader(r.Body, 8193))
	defer r.Body.Close()
	if err != nil || len(raw) > 8192 {
		return fmt.Errorf("body rejected")
	}
	return queueClosedJSON(raw, out)
}
func acquireRoomQueue() bool {
	// The existing bound transport admission serializes queue preparation and
	// commit with ordinary transport commands on the same manager process.
	return acquireRoomTransport()
}
func releaseRoomQueue() {
	releaseRoomTransport()
}
func roomQueuePrepareHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		roomReject(w, "room_queue_prepare", "method", "method", 405)
		return
	}
	var request roomQueuePrepareRequest
	if decodeRoomQueueBody(r, &request) != nil || !validTransportScope(request.Scope) || !queuePositiveDecimal(request.Scope.Homepod) || !validTransportIdentity(request.Native, request.Scope) || !queueOwnerSafe(request.Native) || !queueRouteValid(request.Route, request.Scope.Homepod) {
		roomReject(w, "room_queue_prepare", "arguments", "arguments", 400)
		return
	}
	if !acquireRoomQueue() {
		roomReject(w, "room_queue_prepare", "busy", "busy", 409)
		return
	}
	defer releaseRoomQueue()
	roomQueueStore.Lock()
	read := roomQueueStore.Read
	stale := ""
	switch {
	case roomQueueStore.Prepared != nil && time.Now().Before(roomQueueStore.Prepared.Expires):
		stale = "unconsumed_preparation" // A new selection cannot replace an unconsumed prepared token.
	case read == nil:
		stale = "no_current_read"
	case read.Native.Owner != request.Native:
		stale = "native_owner_changed"
	case read.Route != request.Route:
		stale = "route_changed"
	case !validQueueSelection(request.Queue, read.Native):
		stale = "selection_not_in_read"
	default:
		roomQueueStore.Read = nil // Selected observation is one-use before staging.
	}
	if stale != "" {
		read = nil
	}
	roomQueueStore.Unlock()
	if stale == "" && !roomQueueCurrent(*read, request.Scope) {
		stale = "claim_not_current"
	}
	if stale != "" {
		roomReject(w, "room_queue_prepare", stale, "stale room queue", 409)
		return
	}
	ctx, cancel := context.WithDeadline(r.Context(), read.Expires)
	defer cancel()
	requestBody, _ := json.Marshal(map[string]any{"account_user_id": request.Scope.Account, "target_id": request.Scope.Target, "room_id": request.Scope.Room, "homepod_id": request.Scope.Homepod, "action": "queue", "volume_percent": 0, "queue": request.Queue})
	started := time.Now()
	raw, err := roomQueueRPC(ctx, read.Claim.RT.room.Librespot, http.MethodPost, "/player/room_transport/bound-prepare", requestBody, read.Autoplay)
	var n nativeRoomQueuePrepare
	if err != nil {
		roomReject(w, "room_queue_prepare", "native_rpc_failed", "native queue preparation unavailable", 409)
		return
	}
	if queueClosedJSON(raw, &n) != nil || n.Schema != "podconnect.native_room_queue_prepare.v1" || n.Purpose != roomQueuePurpose || !queueObservationPattern.MatchString(n.PrepareID) || n.Owner != read.Native.Owner || !validBoundNativeRevision(n.Revision) || n.Action != "queue" || n.Percent != 0 || !transportDigest.MatchString(n.Digest) || n.Expires < 1 || n.Expires > 5000 || n.Queue != request.Queue || n.Writer != read.Native.Writer || !roomQueueCurrent(*read, request.Scope) {
		roomReject(w, "room_queue_prepare", "native_queue_preparation_unavailable", "native queue preparation unavailable", 409)
		return
	}
	// The native deadline is fixed before the C calls and never refreshed.
	nativeDeadline := started.Add(time.Duration(n.Expires) * time.Millisecond)
	if nativeDeadline.After(read.Expires) {
		nativeDeadline = read.Expires
	}
	if !time.Now().Before(nativeDeadline) {
		roomReject(w, "room_queue_prepare", "expired", "expired", 409)
		return
	}
	stateBody, _ := json.Marshal(map[string]any{"native_owner": n.Owner, "writer_proof": n.Writer})
	raw, err = roomQueueRPC(ctx, read.Claim.RT.room.OwnTone, http.MethodPost, "/api/outputs/"+url.PathEscape(request.Scope.Homepod)+"/transport/bound-state", stateBody, false)
	var state outputRoomQueueState
	if err != nil || queueClosedJSON(raw, &state) != nil || !validQueueOutputState(state, read.Native, read.Claim, request.Scope) || state.Identity != request.Route || !roomQueueCurrent(*read, request.Scope) || !time.Now().Before(nativeDeadline) {
		roomReject(w, "room_queue_prepare", "output_queue_state_unavailable", "output queue state unavailable", 409)
		return
	}
	outputBody, _ := json.Marshal(map[string]any{"native_prepare_id": n.PrepareID, "args_digest": n.Digest, "action": "queue", "native_owner": n.Owner, "native_revision": n.Revision, "queue": n.Queue, "writer_proof": n.Writer})
	cStarted := time.Now()
	raw, err = roomQueueRPC(ctx, read.Claim.RT.room.OwnTone, http.MethodPost, "/api/outputs/"+url.PathEscape(request.Scope.Homepod)+"/transport/bound-prepare", outputBody, false)
	var o outputRoomQueuePrepare
	if err != nil || queueClosedJSON(raw, &o) != nil || o.Schema != "podconnect.output_queue_prepare.v1" || !o.OK || o.Purpose != roomQueuePurpose || !queueObservationPattern.MatchString(o.Token) || o.Identity != request.Route || o.Owner != n.Owner || o.Revision != n.Revision || o.PrepareID != n.PrepareID || o.Digest != n.Digest || o.Queue != n.Queue || o.Writer != n.Writer || o.Expires < 1 || o.Expires > 10000 || !roomQueueCurrent(*read, request.Scope) || !time.Now().Before(nativeDeadline) {
		roomReject(w, "room_queue_prepare", "output_queue_preparation_unavailable", "output queue preparation unavailable", 409)
		return
	}
	cDeadline := cStarted.Add(time.Duration(o.Expires) * time.Millisecond)
	if cDeadline.Before(nativeDeadline) {
		nativeDeadline = cDeadline
	}
	remaining := int(time.Until(nativeDeadline).Milliseconds())
	if remaining <= 0 {
		roomReject(w, "room_queue_prepare", "expired", "expired", 409)
		return
	}
	prepared := roomQueuePrepared{*read, n, o, nativeDeadline}
	roomQueueStore.Lock()
	roomQueueStore.Prepared = &prepared
	roomQueueStore.Unlock()
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, map[string]any{"schema": "podconnect.room_queue_prepare.v1", "ok": true, "purpose": roomQueuePurpose, "prepare_id": n.PrepareID, "native": n.Owner, "route": o.Identity, "queue": n.Queue, "writer_proof": n.Writer, "expires_after_ms": remaining, "physical_result_verified": false})
}

func roomQueueCommitHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		roomReject(w, "room_queue_commit", "method", "method", 405)
		return
	}
	var body struct {
		PrepareID string `json:"prepare_id"`
	}
	if decodeRoomQueueBody(r, &body) != nil || !queueObservationPattern.MatchString(body.PrepareID) {
		roomReject(w, "room_queue_commit", "arguments", "arguments", 400)
		return
	}
	if !acquireRoomQueue() {
		roomReject(w, "room_queue_commit", "busy", "busy", 409)
		return
	}
	defer releaseRoomQueue()
	roomQueueStore.Lock()
	prepared := roomQueueStore.Prepared
	if prepared != nil && prepared.Native.PrepareID == body.PrepareID {
		roomQueueStore.Prepared = nil // Consume before any awaited possible effect.
	} else {
		prepared = nil
	}
	roomQueueStore.Unlock()
	if prepared == nil {
		roomReject(w, "room_queue_commit", "stale_room_queue", "stale room queue", 409)
		return
	}
	scope := roomTransportScope{prepared.Read.Native.Owner.Target, prepared.Read.Native.Owner.Room, prepared.Read.Native.Owner.Homepod, prepared.Read.Native.Owner.Account}
	if !roomQueueCurrent(prepared.Read, scope) || !time.Now().Before(prepared.Expires) {
		roomReject(w, "room_queue_commit", "stale_room_queue", "stale room queue", 409)
		return
	}
	ctx, cancel := context.WithDeadline(r.Context(), prepared.Expires)
	defer cancel()
	encoded, _ := json.Marshal(map[string]any{"prepare_id": body.PrepareID, "own_tone_token": prepared.Output.Token})
	raw, err := roomQueueRPC(ctx, prepared.Read.Claim.RT.room.Librespot, http.MethodPost, "/player/room_transport/bound-commit", encoded, false)
	var ack struct {
		Schema      string                `json:"schema"`
		Accepted    bool                  `json:"accepted_native"`
		Purpose     string                `json:"purpose"`
		PrepareID   string                `json:"prepare_id"`
		Observation string                `json:"observation_id"`
		Occurrence  string                `json:"occurrence_id"`
		Revision    uint64                `json:"queue_revision"`
		Owner       roomTransportIdentity `json:"native_owner"`
		NativeRev   string                `json:"native_revision"`
		Digest      string                `json:"args_digest"`
		Physical    bool                  `json:"physical_result_verified"`
	}
	accepted := err == nil && queueClosedJSON(raw, &ack) == nil && ack.Schema == "podconnect.native_room_queue_commit.v1" && ack.Accepted && ack.Purpose == roomQueuePurpose && ack.PrepareID == body.PrepareID && ack.Observation == prepared.Native.Queue.Observation && ack.Occurrence == prepared.Native.Queue.Occurrence && ack.Revision == prepared.Native.Queue.Revision && ack.Owner == prepared.Native.Owner && ack.NativeRev == prepared.Native.Revision && ack.Digest == prepared.Native.Digest && !ack.Physical
	outcome := "unknown"
	if accepted {
		outcome = "accepted"
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, map[string]any{"schema": "podconnect.room_queue_commit.v1", "ok": accepted, "prepare_id": body.PrepareID, "outcome": outcome, "accepted_native": accepted, "retry_allowed": false, "physical_result_verified": false})
}

// roomReject names the first failed check in one bounded line (#140): finite
// stage names only, never identifiers, URIs or payloads.
func roomReject(w http.ResponseWriter, event, stage, message string, code int) {
	log.Printf("%s event=rejected stage=%s status=%d", event, stage, code)
	http.Error(w, message, code)
}
