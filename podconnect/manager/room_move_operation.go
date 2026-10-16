package main

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
	"slices"
	"sync"
	"time"
)

type roomMoveRequest struct {
	Scope  roomTransportScope `json:"scope"`
	Offer  roomMoveOffer      `json:"move_offer"`
	Action string             `json:"action"`
	Args   map[string]any     `json:"args"`
}

type roomMoveResult struct {
	Schema    string  `json:"schema"`
	OK        bool    `json:"ok"`
	Offer     string  `json:"offer_id"`
	Binding   string  `json:"binding_id"`
	State     string  `json:"state"`
	Accepted  bool    `json:"accepted_room_move"`
	Operation *string `json:"move_operation_id"`
	Percent   *int    `json:"session_volume_percent"`
	Room      string  `json:"room_id"`
	Homepod   string  `json:"homepod_id"`
	Physical  bool    `json:"physical_result_verified"`
}

type roomMoveRecord struct {
	Binding          roomContentBinding
	Request          roomMoveRequest
	Digest           string
	Created          time.Time
	Preparing        bool
	Dispatched       bool
	Cancelled        bool
	Result           roomMoveResult
	CancelDispatched bool
	InFlight         int
}

var roomMoveStore = struct {
	sync.Mutex
	Records map[string]*roomMoveRecord
}{Records: make(map[string]*roomMoveRecord)}

var roomMoveRPC = doRoomContentRPC

func decodeRoomMoveRequest(raw []byte) (roomMoveRequest, error) {
	var request roomMoveRequest
	fields, err := contentFields(raw, "scope", "move_offer", "action", "args")
	if err != nil || decodeQueueManagerJSON(fields["scope"], &request.Scope) != nil || decodeQueueManagerJSON(fields["move_offer"], &request.Offer) != nil || json.Unmarshal(fields["action"], &request.Action) != nil || !validTransportScope(request.Scope) || !validRoomMoveOffer(request.Offer) || request.Action != "move_existing_session" {
		return request, fmt.Errorf("invalid retained-session move request")
	}
	if _, err := contentFields(fields["args"]); err != nil {
		return request, fmt.Errorf("empty move args required")
	}
	request.Args = map[string]any{}
	return request, nil
}

func decodeRoomMoveResult(raw []byte, endpoint string, record *roomMoveRecord) (roomMoveResult, error) {
	var result roomMoveResult
	fields, err := contentFields(raw, "schema", "ok", "offer_id", "binding_id", "state", "accepted_room_move", "move_operation_id", "session_volume_percent", "room_id", "homepod_id", "physical_result_verified")
	for key, value := range fields {
		if key != "move_operation_id" && key != "session_volume_percent" && bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return result, fmt.Errorf("null required result field")
		}
	}
	if err != nil || json.Unmarshal(raw, &result) != nil || result.Schema != "podconnect.native_room_move_"+endpoint+".v1" || result.Offer != record.Request.Offer.ID || result.Binding != record.Request.Offer.Binding || result.Room != record.Request.Scope.Room || result.Homepod != record.Request.Scope.Homepod || result.Physical || !slices.Contains([]string{"staging", "staged", "committing", "complete", "rejected", "cancelled", "unknown"}, result.State) || result.OK != (result.State == "complete") || result.Accepted != result.OK || result.Operation != nil && *result.Operation != result.Offer || result.Percent != nil && *result.Percent != record.Request.Offer.Percent || result.OK && (result.Operation == nil || result.Percent == nil) {
		return result, fmt.Errorf("invalid actual move result")
	}
	if endpoint == "prepare" && (result.State == "committing" || result.State == "complete") {
		return result, fmt.Errorf("move phase without actual commit dispatch")
	}
	return result, nil
}

func terminalRoomMove(state string) bool {
	return slices.Contains([]string{"complete", "rejected", "cancelled", "unknown"}, state)
}
func moveUnknown(record *roomMoveRecord) roomMoveResult {
	return roomMoveResult{Offer: record.Request.Offer.ID, Binding: record.Request.Offer.Binding, State: "unknown", Room: record.Request.Scope.Room, Homepod: record.Request.Scope.Homepod}
}
func writeRoomMoveResult(w http.ResponseWriter, endpoint string, result roomMoveResult) {
	result.Schema = "podconnect.room_move_" + endpoint + ".v1"
	writeJSON(w, result)
}
func moveIDBody(r *http.Request) (string, string, error) {
	if r.Method != http.MethodPost || r.URL.RawQuery != "" {
		return "", "", fmt.Errorf("method/arguments")
	}
	raw, err := io.ReadAll(io.LimitReader(r.Body, 1025))
	defer r.Body.Close()
	fields, parseErr := contentFields(raw, "offer_id", "binding_id")
	var offer, binding string
	if err != nil || len(raw) > 1024 || parseErr != nil || json.Unmarshal(fields["offer_id"], &offer) != nil || json.Unmarshal(fields["binding_id"], &binding) != nil || !queueObservationPattern.MatchString(offer) || !transportDigest.MatchString(binding) {
		return "", "", fmt.Errorf("invalid ids")
	}
	return offer, binding, nil
}

func roomMovePrepareHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost || r.URL.RawQuery != "" {
		http.Error(w, "method/arguments", 400)
		return
	}
	raw, err := io.ReadAll(io.LimitReader(r.Body, 8193))
	r.Body.Close()
	request, parseErr := decodeRoomMoveRequest(raw)
	if err != nil || len(raw) > 8192 || parseErr != nil {
		http.Error(w, "arguments", 400)
		return
	}
	binding, ok := roomContentBindingNow(request.Scope)
	if !ok {
		http.Error(w, "destination unavailable", 409)
		return
	}
	canonical, _ := json.Marshal(request)
	digest := sha256.Sum256(canonical)
	record := &roomMoveRecord{Binding: binding, Request: request, Digest: hex.EncodeToString(digest[:]), Created: time.Now(), Preparing: true, InFlight: 1}
	record.Result = moveUnknown(record)
	record.Result.State = "staging"
	roomMoveStore.Lock()
	if old := roomMoveStore.Records[request.Offer.ID]; old != nil {
		matching := old.Digest == record.Digest && old.Binding == binding
		result := old.Result
		roomMoveStore.Unlock()
		if !matching {
			http.Error(w, "operation conflict", 409)
			return
		}
		writeRoomMoveResult(w, "prepare", result)
		return
	}
	// Expired undispatched offers have no future manager commit authority.
	// Dispatched unknowns remain fenced; never evict them to make capacity.
	if len(roomMoveStore.Records) >= 128 {
		for id, old := range roomMoveStore.Records {
			if old.InFlight == 0 && ((!old.Dispatched && time.Since(old.Created) > 35*time.Second) || slices.Contains([]string{"complete", "rejected", "cancelled"}, old.Result.State)) {
				delete(roomMoveStore.Records, id)
				break
			}
		}
	}
	if len(roomMoveStore.Records) >= 128 {
		roomMoveStore.Unlock()
		http.Error(w, "busy", 409)
		return
	}
	roomMoveStore.Records[request.Offer.ID] = record
	roomMoveStore.Unlock()
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	nativeRaw, rpcErr := roomMoveRPC(ctx, binding.NativeURL, http.MethodPost, "/player/room_move/prepare", canonical)
	result, resultErr := decodeRoomMoveResult(nativeRaw, "prepare", record)
	roomMoveStore.Lock()
	record.Preparing = false
	record.InFlight--
	if terminalRoomMove(record.Result.State) {
		result = record.Result
	} else if rpcErr != nil || resultErr != nil || record.Cancelled || !roomContentBindingCurrent(binding) {
		record.Result = moveUnknown(record)
	} else {
		record.Result = result
	}
	result = record.Result
	roomMoveStore.Unlock()
	writeRoomMoveResult(w, "prepare", result)
}

func roomMoveResultHandler(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	offer, binding := q.Get("offer_id"), q.Get("binding_id")
	if r.Method != http.MethodGet || len(q) != 2 || len(q["offer_id"]) != 1 || len(q["binding_id"]) != 1 || !queueObservationPattern.MatchString(offer) || !transportDigest.MatchString(binding) {
		http.Error(w, "arguments", 400)
		return
	}
	roomMoveOperation(w, r, "result", offer, binding)
}
func roomMoveCommitHandler(w http.ResponseWriter, r *http.Request) {
	offer, binding, err := moveIDBody(r)
	if err != nil {
		http.Error(w, "arguments", 400)
		return
	}
	roomMoveOperation(w, r, "commit", offer, binding)
}
func roomMoveCancelHandler(w http.ResponseWriter, r *http.Request) {
	offer, binding, err := moveIDBody(r)
	if err != nil {
		http.Error(w, "arguments", 400)
		return
	}
	roomMoveOperation(w, r, "cancel", offer, binding)
}

func roomMoveOperation(w http.ResponseWriter, r *http.Request, endpoint, offer, binding string) {
	roomMoveStore.Lock()
	record := roomMoveStore.Records[offer]
	if record == nil || record.Request.Offer.Binding != binding {
		roomMoveStore.Unlock()
		http.Error(w, "unknown operation", 409)
		return
	}
	if terminalRoomMove(record.Result.State) && !record.Preparing && (endpoint != "cancel" || record.Result.State != "unknown") {
		result := record.Result
		roomMoveStore.Unlock()
		writeRoomMoveResult(w, endpoint, result)
		return
	}
	if endpoint == "commit" {
		if record.Preparing || record.Dispatched || record.Cancelled || record.Result.State != "staged" || !roomContentBindingCurrent(record.Binding) {
			roomMoveStore.Unlock()
			http.Error(w, "not staged/current", 409)
			return
		}
		record.Dispatched = true
		record.Result.State = "committing"
	} else if endpoint == "cancel" {
		record.Cancelled = true
		if record.CancelDispatched {
			result := record.Result
			roomMoveStore.Unlock()
			writeRoomMoveResult(w, endpoint, result)
			return
		}
		record.CancelDispatched = true
	}
	if record.Preparing && endpoint == "result" {
		result := record.Result
		roomMoveStore.Unlock()
		writeRoomMoveResult(w, endpoint, result)
		return
	}
	nativeURL := record.Binding.NativeURL
	record.InFlight++
	roomMoveStore.Unlock()
	limit := 16 * time.Second
	if endpoint == "cancel" {
		limit = time.Second
	}
	ctx, cancel := context.WithTimeout(r.Context(), limit)
	defer cancel()
	method, path, body := http.MethodGet, "/player/room_move/result?"+url.Values{"offer_id": {offer}, "binding_id": {binding}}.Encode(), []byte(nil)
	if endpoint != "result" {
		method, path = http.MethodPost, "/player/room_move/"+endpoint
		body, _ = json.Marshal(map[string]string{"offer_id": offer, "binding_id": binding})
	}
	raw, rpcErr := roomMoveRPC(ctx, nativeURL, method, path, body)
	result, resultErr := decodeRoomMoveResult(raw, endpoint, record)
	roomMoveStore.Lock()
	record.InFlight--
	if (result.State == "committing" || result.State == "complete") && !record.Dispatched {
		resultErr = fmt.Errorf("move phase without actual commit dispatch")
	}
	// A terminal unknown never becomes a late success, even if another pending
	// observation/cancel completes after it. Native independently retires effects.
	if !terminalRoomMove(record.Result.State) {
		if rpcErr != nil || resultErr != nil || !roomContentBindingCurrent(record.Binding) || record.Cancelled && result.State == "complete" {
			record.Result = moveUnknown(record)
		} else {
			record.Result = result
		}
	}
	result = record.Result
	roomMoveStore.Unlock()
	writeRoomMoveResult(w, endpoint, result)
}
