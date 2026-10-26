package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"
)

type roomContentRequest struct {
	Scope  roomTransportScope `json:"scope"`
	Offer  roomContentOffer   `json:"start_offer"`
	Action string             `json:"action"`
	Args   map[string]any     `json:"args"`
}

type roomContentResult struct {
	Schema    string  `json:"schema"`
	OK        bool    `json:"ok"`
	Offer     string  `json:"offer_id"`
	Binding   string  `json:"binding_id"`
	State     string  `json:"state"`
	Accepted  bool    `json:"accepted_selected_content"`
	Operation *string `json:"start_operation_id"`
	Percent   *int    `json:"start_volume_percent"`
	Room      string  `json:"room_id"`
	Homepod   string  `json:"homepod_id"`
	Physical  bool    `json:"physical_result_verified"`
}

type roomCurrentTarget struct {
	Process     string `json:"process"`
	Device      string `json:"device_id"`
	Incarnation string `json:"device_incarnation"`
}
type roomCurrentWitness struct {
	Schema    string                  `json:"schema"`
	Offer     string                  `json:"offer_id"`
	Binding   string                  `json:"binding_id"`
	Operation string                  `json:"operation_id"`
	Native    roomTransportIdentity   `json:"native"`
	Route     outputTransportIdentity `json:"route"`
	Target    roomCurrentTarget       `json:"target"`
	Physical  bool                    `json:"physical_result_verified"`
}

func decodeRoomCurrent(raw []byte, offer, binding string, scope roomTransportScope) (roomCurrentWitness, error) {
	var witness roomCurrentWitness
	_, err := contentFields(raw, "schema", "offer_id", "binding_id", "operation_id", "native", "route", "target", "physical_result_verified")
	if err != nil || decodeQueueManagerJSON(raw, &witness) != nil || witness.Schema != "podconnect.native_completed_current.v1" || witness.Offer != offer || witness.Binding != binding || witness.Operation != offer || witness.Physical || !validTransportIdentity(witness.Native, scope) || !validOutputTransportIdentity(witness.Route, scope.Homepod) || witness.Target.Process != witness.Route.Process || witness.Target.Device != scope.Homepod || witness.Target.Incarnation != witness.Route.Incarnation {
		return witness, fmt.Errorf("invalid current owner witness")
	}
	return witness, nil
}

func roomCurrentResult(w http.ResponseWriter, r *http.Request, kind, offer, binding string) {
	var nativeURL string
	var scope roomTransportScope
	var originalContent *roomContentRecord
	var originalMove *roomMoveRecord
	if kind == "content" {
		roomContentStore.Lock()
		record := roomContentStore.Records[offer]
		valid := record != nil && record.Request.Offer.Binding == binding && record.Dispatched && !record.Cancelled && record.Result.State == "complete" && record.Result.Operation != nil && *record.Result.Operation == offer && roomContentBindingCurrent(record.Binding)
		if valid {
			nativeURL, scope = record.Binding.NativeURL, record.Request.Scope
			originalContent = record
		}
		roomContentStore.Unlock()
		if !valid {
			http.Error(w, "not current", http.StatusConflict)
			return
		}
	} else {
		roomMoveStore.Lock()
		record := roomMoveStore.Records[offer]
		valid := record != nil && record.Request.Offer.Binding == binding && record.Dispatched && !record.Cancelled && record.Result.State == "complete" && record.Result.Operation != nil && *record.Result.Operation == offer && roomContentBindingCurrent(record.Binding)
		if valid {
			nativeURL, scope = record.Binding.NativeURL, record.Request.Scope
			originalMove = record
		}
		roomMoveStore.Unlock()
		if !valid {
			http.Error(w, "not current", http.StatusConflict)
			return
		}
	}
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	path := "/player/room_" + kind + "/result?" + url.Values{"offer_id": {offer}, "binding_id": {binding}, "include_current": {"1"}}.Encode()
	rpc := roomContentRPC
	if kind == "move" {
		rpc = roomMoveRPC
	}
	raw, err := rpc(ctx, nativeURL, http.MethodGet, path, nil)
	witness, parseErr := decodeRoomCurrent(raw, offer, binding, scope)
	if err != nil || parseErr != nil || ctx.Err() != nil {
		http.Error(w, "current owner unavailable", http.StatusConflict)
		return
	}
	if kind == "content" {
		roomContentStore.Lock()
		record := roomContentStore.Records[offer]
		valid := record != nil && record == originalContent && record.Request.Offer.Binding == binding && record.Dispatched && !record.Cancelled && record.Result.State == "complete" && record.Result.Operation != nil && *record.Result.Operation == offer && record.Binding.NativeURL == nativeURL && roomContentBindingCurrent(record.Binding)
		roomContentStore.Unlock()
		if !valid {
			http.Error(w, "owner replaced", http.StatusConflict)
			return
		}
	} else {
		roomMoveStore.Lock()
		record := roomMoveStore.Records[offer]
		valid := record != nil && record == originalMove && record.Request.Offer.Binding == binding && record.Dispatched && !record.Cancelled && record.Result.State == "complete" && record.Result.Operation != nil && *record.Result.Operation == offer && record.Binding.NativeURL == nativeURL && roomContentBindingCurrent(record.Binding)
		roomMoveStore.Unlock()
		if !valid {
			http.Error(w, "owner replaced", http.StatusConflict)
			return
		}
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(witness)
}

type roomContentRecord struct {
	Binding    roomContentBinding
	Request    roomContentRequest
	Digest     string
	Created    time.Time
	Preparing  bool
	Dispatched bool
	Cancelled  bool
	Result     roomContentResult
}

var roomContentStore = struct {
	sync.Mutex
	Records map[string]*roomContentRecord
}{Records: make(map[string]*roomContentRecord)}

var contentTrackURI = regexp.MustCompile(`^spotify:track:[A-Za-z0-9]{22}$`)
var contentEpisodeURI = regexp.MustCompile(`^spotify:episode:[A-Za-z0-9]{22}$`)
var contentContextURI = regexp.MustCompile(`^spotify:(playlist|album|artist|show):[A-Za-z0-9]{22}$`)
var roomContentRPC = doRoomContentRPC

// The native critical section has its own immutable twelve-second cutoff.
// This HTTP limit never replaces that actual-owner fence or retries a send.
func doRoomContentRPC(ctx context.Context, base, method, path string, body []byte) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, method, base+path, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	client := &http.Client{Timeout: 18 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return fmt.Errorf("content redirect rejected") }}
	response, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(response.Body, 65537))
	if err != nil || len(raw) > 65536 || response.StatusCode != 200 {
		return nil, fmt.Errorf("content owner unavailable")
	}
	return raw, nil
}

func contentFields(raw []byte, keys ...string) (map[string]json.RawMessage, error) {
	var fields map[string]json.RawMessage
	if decodeQueueManagerJSON(raw, &fields) != nil || len(fields) != len(keys) {
		return nil, fmt.Errorf("closed object required")
	}
	for _, key := range keys {
		if _, ok := fields[key]; !ok {
			return nil, fmt.Errorf("missing field")
		}
	}
	return fields, nil
}

func decodeRoomContentRequest(raw []byte) (roomContentRequest, error) {
	var request roomContentRequest
	fields, err := contentFields(raw, "scope", "start_offer", "action", "args")
	if err != nil || decodeQueueManagerJSON(fields["scope"], &request.Scope) != nil || decodeQueueManagerJSON(fields["start_offer"], &request.Offer) != nil || json.Unmarshal(fields["action"], &request.Action) != nil || !validTransportScope(request.Scope) || !validRoomContentOffer(request.Offer) {
		return request, fmt.Errorf("invalid content request")
	}
	if request.Action == "play_tracks" {
		args, err := contentFields(fields["args"], "track_uris", "shuffle", "library_source", "max_volume")
		var uris []string
		var shuffle *bool
		var source string
		var ceiling int
		if err != nil || json.Unmarshal(args["track_uris"], &uris) != nil || len(uris) < 1 || len(uris) > 50 || json.Unmarshal(args["shuffle"], &shuffle) != nil || shuffle == nil || json.Unmarshal(args["library_source"], &source) != nil || !slices.Contains([]string{"liked", "top_tracks", "recent"}, source) || json.Unmarshal(args["max_volume"], &ceiling) != nil || ceiling < 35 || ceiling > 100 {
			return request, fmt.Errorf("invalid content arguments")
		}
		for _, uri := range uris {
			id, valid := new(big.Int).SetString(strings.TrimPrefix(uri, "spotify:track:"), 62)
			if !contentTrackURI.MatchString(uri) || !valid || id.BitLen() > 128 {
				return request, fmt.Errorf("invalid content track")
			}
		}
		request.Args = map[string]any{"track_uris": uris, "shuffle": *shuffle, "library_source": source, "max_volume": ceiling}
		return request, nil
	}
	key, uriPattern := "track_uri", contentTrackURI
	if request.Action == "play_context" {
		key, uriPattern = "context_uri", contentContextURI
	} else if request.Action == "play_episode" {
		key, uriPattern = "episode_uri", contentEpisodeURI
	} else if request.Action != "play_track" {
		return request, fmt.Errorf("invalid action")
	}
	args, err := contentFields(fields["args"], key, "max_volume")
	var uri string
	var ceiling int
	if err != nil || json.Unmarshal(args[key], &uri) != nil || json.Unmarshal(args["max_volume"], &ceiling) != nil || !uriPattern.MatchString(uri) || ceiling < 35 || ceiling > 100 {
		return request, fmt.Errorf("invalid content arguments")
	}
	request.Args = map[string]any{key: uri, "max_volume": ceiling}
	return request, nil
}

func decodeRoomContentResult(raw []byte, endpoint string, record *roomContentRecord) (roomContentResult, error) {
	var result roomContentResult
	fields, err := contentFields(raw, "schema", "ok", "offer_id", "binding_id", "state", "accepted_selected_content", "start_operation_id", "start_volume_percent", "room_id", "homepod_id", "physical_result_verified")
	for key, value := range fields {
		if key != "start_operation_id" && key != "start_volume_percent" && bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return result, fmt.Errorf("null required result field")
		}
	}
	if err != nil || json.Unmarshal(raw, &result) != nil || result.Schema != "podconnect.native_room_content_"+endpoint+".v1" || result.Offer != record.Request.Offer.ID || result.Binding != record.Request.Offer.Binding || result.Room != record.Request.Scope.Room || result.Homepod != record.Request.Scope.Homepod || result.Physical || !slices.Contains([]string{"staging", "staged", "committing", "complete", "rejected", "cancelled", "unknown"}, result.State) || result.OK != (result.State == "complete") || result.Accepted != result.OK || result.Operation != nil && *result.Operation != result.Offer || result.Percent != nil && *result.Percent != 35 || result.OK && (result.Operation == nil || result.Percent == nil) {
		return result, fmt.Errorf("invalid actual content result")
	}
	if endpoint == "prepare" && (result.State == "committing" || result.State == "complete") {
		return result, fmt.Errorf("start phase without actual commit dispatch")
	}
	return result, nil
}

func terminalRoomContent(state string) bool {
	return slices.Contains([]string{"complete", "rejected", "cancelled", "unknown"}, state)
}
func contentUnknown(record *roomContentRecord) roomContentResult {
	return roomContentResult{Offer: record.Request.Offer.ID, Binding: record.Request.Offer.Binding, State: "unknown", Room: record.Request.Scope.Room, Homepod: record.Request.Scope.Homepod}
}
func writeRoomContentResult(w http.ResponseWriter, endpoint string, result roomContentResult) {
	result.Schema = "podconnect.room_content_" + endpoint + ".v1"
	writeJSON(w, result)
}
func contentIDBody(r *http.Request) (string, string, error) {
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

func roomContentPrepareHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost || r.URL.RawQuery != "" {
		http.Error(w, "method/arguments", 400)
		return
	}
	raw, err := io.ReadAll(io.LimitReader(r.Body, 8193))
	r.Body.Close()
	request, parseErr := decodeRoomContentRequest(raw)
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
	record := &roomContentRecord{Binding: binding, Request: request, Digest: hex.EncodeToString(digest[:]), Created: time.Now(), Preparing: true}
	record.Result = contentUnknown(record)
	roomContentStore.Lock()
	if old := roomContentStore.Records[request.Offer.ID]; old != nil {
		matching := old.Digest == record.Digest && old.Binding == binding
		result := old.Result
		roomContentStore.Unlock()
		if !matching {
			http.Error(w, "operation conflict", 409)
			return
		}
		writeRoomContentResult(w, "prepare", result)
		return
	}
	// Expired undispatched offers have no future manager commit authority.
	// Dispatched unknowns remain fenced; never evict them to make capacity.
	if len(roomContentStore.Records) >= 128 {
		for id, old := range roomContentStore.Records {
			if !old.Preparing && !old.Dispatched && time.Since(old.Created) > 35*time.Second || slices.Contains([]string{"complete", "rejected", "cancelled"}, old.Result.State) {
				delete(roomContentStore.Records, id)
				break
			}
		}
	}
	if len(roomContentStore.Records) >= 128 {
		roomContentStore.Unlock()
		http.Error(w, "busy", 409)
		return
	}
	roomContentStore.Records[request.Offer.ID] = record
	roomContentStore.Unlock()
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	nativeRaw, rpcErr := roomContentRPC(ctx, binding.NativeURL, http.MethodPost, "/player/room_content/prepare", canonical)
	result, resultErr := decodeRoomContentResult(nativeRaw, "prepare", record)
	roomContentStore.Lock()
	record.Preparing = false
	if rpcErr != nil || resultErr != nil || record.Cancelled || !roomContentBindingCurrent(binding) {
		record.Result = contentUnknown(record)
	} else {
		record.Result = result
	}
	result = record.Result
	roomContentStore.Unlock()
	writeRoomContentResult(w, "prepare", result)
}

func roomContentResultHandler(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	offer, binding := q.Get("offer_id"), q.Get("binding_id")
	current := len(q) == 3 && len(q["include_current"]) == 1 && q.Get("include_current") == "1"
	if r.Method != http.MethodGet || (!current && len(q) != 2) || len(q["offer_id"]) != 1 || len(q["binding_id"]) != 1 || !queueObservationPattern.MatchString(offer) || !transportDigest.MatchString(binding) {
		http.Error(w, "arguments", 400)
		return
	}
	if current {
		roomCurrentResult(w, r, "content", offer, binding)
		return
	}
	roomContentOperation(w, r, "result", offer, binding)
}
func roomContentCommitHandler(w http.ResponseWriter, r *http.Request) {
	offer, binding, err := contentIDBody(r)
	if err != nil {
		http.Error(w, "arguments", 400)
		return
	}
	roomContentOperation(w, r, "commit", offer, binding)
}
func roomContentCancelHandler(w http.ResponseWriter, r *http.Request) {
	offer, binding, err := contentIDBody(r)
	if err != nil {
		http.Error(w, "arguments", 400)
		return
	}
	roomContentOperation(w, r, "cancel", offer, binding)
}

func roomContentOperation(w http.ResponseWriter, r *http.Request, endpoint, offer, binding string) {
	roomContentStore.Lock()
	record := roomContentStore.Records[offer]
	if record == nil || record.Request.Offer.Binding != binding {
		roomContentStore.Unlock()
		http.Error(w, "unknown operation", 409)
		return
	}
	if terminalRoomContent(record.Result.State) && !record.Preparing && (endpoint != "cancel" || record.Result.State != "unknown") {
		result := record.Result
		roomContentStore.Unlock()
		writeRoomContentResult(w, endpoint, result)
		return
	}
	if endpoint == "commit" {
		if record.Preparing || record.Dispatched || record.Cancelled || record.Result.State != "staged" || !roomContentBindingCurrent(record.Binding) {
			roomContentStore.Unlock()
			http.Error(w, "not staged/current", 409)
			return
		}
		record.Dispatched = true
		record.Result.State = "committing"
	} else if endpoint == "cancel" {
		record.Cancelled = true
	}
	if record.Preparing && endpoint == "result" {
		result := record.Result
		roomContentStore.Unlock()
		writeRoomContentResult(w, endpoint, result)
		return
	}
	nativeURL := record.Binding.NativeURL
	roomContentStore.Unlock()
	ctx, cancel := context.WithTimeout(r.Context(), 18*time.Second)
	defer cancel()
	method, path, body := http.MethodGet, "/player/room_content/result?"+url.Values{"offer_id": {offer}, "binding_id": {binding}}.Encode(), []byte(nil)
	if endpoint != "result" {
		method, path = http.MethodPost, "/player/room_content/"+endpoint
		body, _ = json.Marshal(map[string]string{"offer_id": offer, "binding_id": binding})
	}
	raw, rpcErr := roomContentRPC(ctx, nativeURL, method, path, body)
	result, resultErr := decodeRoomContentResult(raw, endpoint, record)
	roomContentStore.Lock()
	if (result.State == "committing" || result.State == "complete") && !record.Dispatched {
		resultErr = fmt.Errorf("start phase without actual commit dispatch")
	}
	// A terminal unknown never becomes a late success, even if another pending
	// observation/cancel completes after it. Native independently retires effects.
	if !terminalRoomContent(record.Result.State) {
		if rpcErr != nil || resultErr != nil || !roomContentBindingCurrent(record.Binding) || record.Cancelled && result.State == "complete" {
			record.Result = contentUnknown(record)
		} else {
			record.Result = result
		}
	}
	result = record.Result
	roomContentStore.Unlock()
	writeRoomContentResult(w, endpoint, result)
}
