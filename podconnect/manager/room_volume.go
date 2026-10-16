package main

// Exact-room volume is a distinct additive capability. The receiver owns C
// preparation, reservation and physical admission; manager never holds a C
// token or falls back to WebAPI/attention/start-volume authority.
import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"sync"
	"time"
)

type roomVolumeState struct {
	BaseRevision      string `json:"base_revision"`
	AttentionRevision string `json:"attention_revision"`
	BasePercent       int    `json:"base_percent"`
	EffectivePercent  int    `json:"effective_percent"`
}
type outputRoomVolumeState struct {
	Schema   string                  `json:"schema"`
	OK       bool                    `json:"ok"`
	Identity outputTransportIdentity `json:"identity"`
	Volume   roomVolumeState         `json:"volume_state"`
	Busy     bool                    `json:"busy"`
	Actions  []string                `json:"actions"`
	Physical bool                    `json:"physical_result_verified"`
}
type roomVolumeRequest struct {
	Scope   roomTransportScope      `json:"scope"`
	Native  roomTransportIdentity   `json:"native"`
	Route   outputTransportIdentity `json:"route"`
	Volume  roomVolumeState         `json:"volume_state"`
	Action  string                  `json:"action"`
	Percent int                     `json:"volume_percent"`
}
type nativeRoomVolumePrepare struct {
	Schema   string                  `json:"schema"`
	ID       string                  `json:"native_prepare_id"`
	Expires  int                     `json:"expires_after_ms"`
	Scope    roomTransportScope      `json:"scope"`
	Native   roomTransportIdentity   `json:"native"`
	Route    outputTransportIdentity `json:"route"`
	Volume   roomVolumeState         `json:"volume_state"`
	Action   string                  `json:"action"`
	Percent  int                     `json:"volume_percent"`
	Physical bool                    `json:"physical_result_verified"`
}
type nativeRoomVolumeResult struct {
	Schema          string `json:"schema"`
	ID              string `json:"native_prepare_id"`
	OK              bool   `json:"ok"`
	State           string `json:"state"`
	Reserved        bool   `json:"reservation_accepted"`
	Protocol        bool   `json:"protocol_accepted"`
	Possible        bool   `json:"possible_effect"`
	Terminal        bool   `json:"terminal"`
	DeadlineRetired bool   `json:"deadline_retired"`
	Drained         bool   `json:"contexts_drained"`
	Retry           bool   `json:"retry_allowed"`
	Physical        bool   `json:"physical_result_verified"`
}
type roomVolumeRecord struct {
	Request         roomVolumeRequest
	Claim           startClaim
	Expires         time.Time
	Dispatched      bool
	Cancelled       bool
	CancelSent      bool
	CleanupTerminal bool
	Result          *nativeRoomVolumeResult
}

var roomVolumeStore = struct {
	sync.Mutex
	Preparing bool
	Records   map[string]*roomVolumeRecord
}{Records: make(map[string]*roomVolumeRecord)}

func validRoomVolumeState(v roomVolumeState) bool {
	_, baseErr := strconv.ParseUint(v.BaseRevision, 10, 64)
	_, attentionErr := strconv.ParseUint(v.AttentionRevision, 10, 64)
	return baseErr == nil && attentionErr == nil && transportDecimal.MatchString(v.BaseRevision) && transportDecimal.MatchString(v.AttentionRevision) && v.BasePercent >= 0 && v.BasePercent <= 100 && v.EffectivePercent >= 0 && v.EffectivePercent <= 100
}
func validRoomVolumeActions(a []string) bool {
	return a != nil && (len(a) == 0 || len(a) == 1 && a[0] == "volume")
}
func validRoomVolumeRequest(b roomVolumeRequest) bool {
	return validTransportScope(b.Scope) && validTransportIdentity(b.Native, b.Scope) && validOutputTransportIdentity(b.Route, b.Scope.Homepod) && validRoomVolumeState(b.Volume) && b.Action == "volume" && b.Percent >= 0 && b.Percent <= 100
}
func readRoomVolume(ctx context.Context, s roomTransportScope) (startClaim, receiverTransportState, outputRoomVolumeState, error) {
	var n receiverTransportState
	var o outputRoomVolumeState
	claim, ok := roomTransportClaim(s.Room, s.Target)
	if !validTransportScope(s) || !ok || !transportClaimMatches(claim, s) {
		return claim, n, o, fmt.Errorf("volume unavailable")
	}
	q := url.Values{"account_user_id": {s.Account}, "target_id": {s.Target}, "room_id": {s.Room}, "homepod_id": {s.Homepod}}
	raw, err := roomTransportRPC(ctx, claim.RT.room.Librespot, http.MethodGet, "/player/room_volume/bound-state?"+q.Encode(), nil)
	if err != nil || decodeQueueManagerJSON(raw, &n) != nil || n.Schema != "podconnect.native_room_volume_state.v2" || !n.Ready || n.Physical || !validTransportIdentity(n.Identity, s) || !validRoomVolumeActions(n.Actions) || n.Identity.Incarnation != claim.Proof.AliasBinding.Incarnation || n.Identity.Registry != claim.Proof.AliasBinding.Registry {
		return claim, n, o, fmt.Errorf("native volume unavailable")
	}
	boundState, _ := json.Marshal(map[string]any{"native_owner": n.Identity})
	raw, err = roomTransportRPC(ctx, claim.RT.room.OwnTone, http.MethodPost, "/api/outputs/"+url.PathEscape(s.Homepod)+"/volume/bound-state", boundState)
	if err != nil || decodeQueueManagerJSON(raw, &o) != nil || o.Schema != "podconnect.output_room_volume_state.v2" || !o.OK || o.Physical || !validOutputTransportIdentity(o.Identity, s.Homepod) || o.Identity.InputDigest != configuredTransportInput(claim) || !validRoomVolumeState(o.Volume) || !validRoomVolumeActions(o.Actions) || !transportClaimCurrent(claim, s) {
		return claim, n, o, fmt.Errorf("output volume unavailable")
	}
	return claim, n, o, nil
}
func roomVolumeStateHandler(w http.ResponseWriter, r *http.Request) {
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
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	_, n, o, err := readRoomVolume(ctx, s)
	if err != nil {
		http.Error(w, "volume unavailable", 409)
		return
	}
	actions := []string{}
	if !o.Busy && len(n.Actions) == 1 && len(o.Actions) == 1 {
		actions = []string{"volume"}
	}
	writeJSON(w, map[string]any{"schema": "podconnect.room_volume_state.v1", "ok": true, "native": n.Identity, "route": o.Identity, "volume_state": o.Volume, "actions": actions, "physical_result_verified": false})
}
func roomVolumePrepareHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method", 405)
		return
	}
	var b roomVolumeRequest
	if decodeTransportBody(r, &b) != nil || !validRoomVolumeRequest(b) {
		http.Error(w, "arguments", 400)
		return
	}
	roomVolumeStore.Lock()
	// Never evict active cleanup/fence records to make capacity. Process restart
	// drops manager references, not the receiver/C independent safety fences.
	if !roomVolumeStore.Preparing && len(roomVolumeStore.Records) >= 128 {
		for id, record := range roomVolumeStore.Records {
			if !record.Dispatched && !time.Now().Before(record.Expires) || record.CleanupTerminal || record.Result != nil && record.Result.Terminal && record.Result.Drained {
				delete(roomVolumeStore.Records, id)
				break
			}
		}
	}
	if roomVolumeStore.Preparing || len(roomVolumeStore.Records) >= 128 {
		roomVolumeStore.Unlock()
		http.Error(w, "busy", 409)
		return
	}
	roomVolumeStore.Preparing = true
	roomVolumeStore.Unlock()
	defer func() { roomVolumeStore.Lock(); roomVolumeStore.Preparing = false; roomVolumeStore.Unlock() }()
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	claim, n, o, err := readRoomVolume(ctx, b.Scope)
	if err != nil || n.Identity != b.Native || o.Identity != b.Route || o.Volume != b.Volume || o.Busy || len(n.Actions) != 1 || len(o.Actions) != 1 {
		http.Error(w, "stale volume", 409)
		return
	}
	encoded, _ := json.Marshal(b)
	started := time.Now()
	raw, err := roomTransportRPC(ctx, claim.RT.room.Librespot, http.MethodPost, "/player/room_volume/bound-prepare", encoded)
	var p nativeRoomVolumePrepare
	if err != nil || decodeQueueManagerJSON(raw, &p) != nil || p.Schema != "podconnect.native_room_volume_prepare.v2" || !queueObservationPattern.MatchString(p.ID) || p.Expires < 1 || p.Expires > 5000 || p.Physical || p.Scope != b.Scope || p.Native != b.Native || p.Route != b.Route || p.Volume != b.Volume || p.Action != b.Action || p.Percent != b.Percent || !transportClaimCurrent(claim, b.Scope) {
		http.Error(w, "native volume preparation unavailable", 409)
		return
	}
	expires := started.Add(time.Duration(p.Expires) * time.Millisecond)
	remaining := int(time.Until(expires).Milliseconds())
	if remaining <= 0 {
		http.Error(w, "expired volume", 409)
		return
	}
	roomVolumeStore.Lock()
	if _, exists := roomVolumeStore.Records[p.ID]; exists {
		roomVolumeStore.Unlock()
		http.Error(w, "duplicate preparation", 409)
		return
	}
	roomVolumeStore.Records[p.ID] = &roomVolumeRecord{Request: b, Claim: claim, Expires: expires}
	roomVolumeStore.Unlock()
	writeJSON(w, map[string]any{"schema": "podconnect.room_volume_prepare.v1", "ok": true, "prepare_id": p.ID, "expires_after_ms": remaining, "native": b.Native, "route": b.Route, "volume_state": b.Volume, "action": "volume", "volume_percent": b.Percent, "physical_result_verified": false})
}
func decodeRoomVolumeID(w http.ResponseWriter, r *http.Request) (string, bool) {
	if r.Method != http.MethodPost {
		http.Error(w, "method", 405)
		return "", false
	}
	var b struct {
		ID string `json:"prepare_id"`
	}
	if decodeTransportBody(r, &b) != nil || !queueObservationPattern.MatchString(b.ID) {
		http.Error(w, "arguments", 400)
		return "", false
	}
	return b.ID, true
}
func unknownRoomVolume(id string) nativeRoomVolumeResult {
	// False acceptance flags mean unverified, never a proof of no admission.
	return nativeRoomVolumeResult{ID: id, State: "unknown", Possible: true}
}
func validNativeRoomVolumeResult(v nativeRoomVolumeResult, id, kind string) bool {
	states := []string{"prepared", "pending_claim", "reserved", "pending", "admitted_pending", "protocol_accepted", "rejected_before_admission", "expired_before_admission", "cancelled_before_admission", "unknown"}
	if v.Schema != "podconnect.native_room_volume_"+kind+".v2" || v.ID != id || v.Retry || v.Physical || !slices.Contains(states, v.State) || v.Terminal && !v.Drained {
		return false
	}
	if v.Protocol {
		return v.State == "protocol_accepted" && v.OK && v.Reserved && v.Possible && v.Terminal && v.Drained
	}
	if v.OK || v.State == "protocol_accepted" {
		return false
	}
	if slices.Contains([]string{"prepared", "pending_claim", "reserved", "pending", "admitted_pending"}, v.State) && v.Terminal {
		return false
	}
	if slices.Contains([]string{"rejected_before_admission", "expired_before_admission", "cancelled_before_admission"}, v.State) && v.Possible {
		return false
	}
	if v.State == "admitted_pending" && (!v.Reserved || !v.Possible) {
		return false
	}
	return true
}
func writeRoomVolumeResult(w http.ResponseWriter, kind string, v nativeRoomVolumeResult) {
	writeJSON(w, map[string]any{"schema": "podconnect.room_volume_" + kind + ".v1", "ok": v.Protocol, "prepare_id": v.ID, "state": v.State, "reservation_accepted": v.Reserved, "protocol_accepted": v.Protocol, "possible_effect": v.Possible, "terminal": v.Terminal, "deadline_retired": v.DeadlineRetired, "contexts_drained": v.Drained, "retry_allowed": false, "physical_result_verified": false})
}
func roomVolumeCommitHandler(w http.ResponseWriter, r *http.Request) {
	id, ok := decodeRoomVolumeID(w, r)
	if !ok {
		return
	}
	roomVolumeStore.Lock()
	record := roomVolumeStore.Records[id]
	if record == nil || record.Dispatched || record.Cancelled {
		roomVolumeStore.Unlock()
		http.Error(w, "consumed or missing volume", 409)
		return
	}
	// Consume before every await. Cancellation is allowed to race this request,
	// but can only veto; it never releases either downstream owner fence.
	record.Dispatched = true
	roomVolumeStore.Unlock()
	if !time.Now().Before(record.Expires) || !transportClaimCurrent(record.Claim, record.Request.Scope) {
		v := nativeRoomVolumeResult{ID: id, State: "rejected_before_admission", Terminal: true, Drained: true}
		roomVolumeStore.Lock()
		record.Result = &v
		roomVolumeStore.Unlock()
		writeRoomVolumeResult(w, "commit", v)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 4*time.Second)
	defer cancel()
	encoded, _ := json.Marshal(map[string]any{"native_prepare_id": id})
	roomVolumeStore.Lock()
	cancelled := record.Cancelled
	roomVolumeStore.Unlock()
	if cancelled {
		v := nativeRoomVolumeResult{ID: id, State: "cancelled_before_admission", Terminal: true, Drained: true}
		roomVolumeStore.Lock()
		record.Result = &v
		roomVolumeStore.Unlock()
		writeRoomVolumeResult(w, "commit", v)
		return
	}
	raw, err := roomTransportRPC(ctx, record.Claim.RT.room.Librespot, http.MethodPost, "/player/room_volume/bound-commit", encoded)
	v := unknownRoomVolume(id)
	var received nativeRoomVolumeResult
	if err == nil && decodeQueueManagerJSON(raw, &received) == nil && validNativeRoomVolumeResult(received, id, "commit") {
		v = received
	}
	roomVolumeStore.Lock()
	record.Result = &v
	roomVolumeStore.Unlock()
	writeRoomVolumeResult(w, "commit", v)
}
func roomVolumeCancelHandler(w http.ResponseWriter, r *http.Request) {
	id, ok := decodeRoomVolumeID(w, r)
	if !ok {
		return
	}
	roomVolumeStore.Lock()
	record := roomVolumeStore.Records[id]
	if record == nil {
		roomVolumeStore.Unlock()
		http.Error(w, "missing volume", 404)
		return
	}
	record.Cancelled = true
	if record.CancelSent {
		v := unknownRoomVolume(id)
		roomVolumeStore.Unlock()
		writeRoomVolumeResult(w, "cancel", v)
		return
	}
	record.CancelSent = true
	roomVolumeStore.Unlock()
	ctx, cancel := context.WithTimeout(r.Context(), time.Second)
	defer cancel()
	encoded, _ := json.Marshal(map[string]any{"native_prepare_id": id})
	raw, err := roomTransportRPC(ctx, record.Claim.RT.room.Librespot, http.MethodPost, "/player/room_volume/bound-cancel", encoded)
	v := unknownRoomVolume(id)
	var received nativeRoomVolumeResult
	if err == nil && decodeQueueManagerJSON(raw, &received) == nil && validNativeRoomVolumeResult(received, id, "cancel") {
		v = received
	}
	roomVolumeStore.Lock()
	record.CleanupTerminal = v.Terminal && v.Drained
	roomVolumeStore.Unlock()
	writeRoomVolumeResult(w, "cancel", v)
}
func roomVolumeResultHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method", 405)
		return
	}
	q := r.URL.Query()
	id := q.Get("prepare_id")
	if len(q) != 1 || len(q["prepare_id"]) != 1 || !queueObservationPattern.MatchString(id) {
		http.Error(w, "arguments", 400)
		return
	}
	roomVolumeStore.Lock()
	record := roomVolumeStore.Records[id]
	if record == nil {
		roomVolumeStore.Unlock()
		http.Error(w, "missing volume", 404)
		return
	}
	if record.Result != nil {
		v := *record.Result
		roomVolumeStore.Unlock()
		writeRoomVolumeResult(w, "result", v)
		return
	}
	roomVolumeStore.Unlock()
	ctx, cancel := context.WithTimeout(r.Context(), time.Second)
	defer cancel()
	raw, err := roomTransportRPC(ctx, record.Claim.RT.room.Librespot, http.MethodGet, "/player/room_volume/bound-result?native_prepare_id="+url.QueryEscape(id), nil)
	v := unknownRoomVolume(id)
	var received nativeRoomVolumeResult
	if err == nil && decodeQueueManagerJSON(raw, &received) == nil && validNativeRoomVolumeResult(received, id, "result") {
		v = received
	}
	writeRoomVolumeResult(w, "result", v)
}
