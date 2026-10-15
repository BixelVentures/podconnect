package main

// Exact native queue occurrences within an already selected active room. This
// owner never primes volume, selects outputs, starts a playlist or retries.
import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"reflect"
	"regexp"
	"strings"
	"sync"
	"time"
)

var queueObservationPattern = regexp.MustCompile(`^[a-f0-9]{32}$`)
var queueTrackURI = regexp.MustCompile(`^spotify:track:[A-Za-z0-9]{22}$`)
var queueOptionPattern = regexp.MustCompile(`^[a-f0-9]{24}$`)

type queueNativeItem struct {
	ID       string `json:"id"`
	Position int    `json:"position"`
	URI      string `json:"uri"`
	Queued   bool   `json:"queued"`
}
type queueNativeSnapshot struct {
	Schema           string            `json:"schema"`
	TargetID         string            `json:"target_id"`
	RoomID           string            `json:"room_id"`
	AccountUserID    string            `json:"account_user_id"`
	ConnectionID     string            `json:"connection_id"`
	SessionID        string            `json:"session_id"`
	AliasIncarnation string            `json:"alias_incarnation"`
	ObservationID    string            `json:"observation_id"`
	QueueRevision    uint64            `json:"queue_revision"`
	ExpiresAfterMS   int               `json:"expires_after_ms"`
	Items            []queueNativeItem `json:"items"`
	Truncated        bool              `json:"truncated"`
	HomepodID        string            `json:"homepod_id,omitempty"`
}
type queueManagerOffer struct {
	Snapshot queueNativeSnapshot
	Claim    startClaim
	Expires  time.Time
}

var queueManagerState struct {
	sync.Mutex
	Busy  bool
	Offer *queueManagerOffer
}
var queueCurrentClaim = currentStartClaim
var queueNativeRPC = doQueueNativeRPC

func doQueueNativeRPC(ctx context.Context, base, method, path string, body []byte) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, method, base+path, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	client := &http.Client{Timeout: 4 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return fmt.Errorf("native redirect rejected") }}
	response, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(response.Body, 65537))
	if err != nil || len(raw) > 65536 || response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("native queue outcome unavailable")
	}
	return raw, nil
}
func decodeQueueManagerJSON(raw []byte, out any) error {
	if !queueRequiredJSON(raw, reflect.TypeOf(out).Elem()) {
		return fmt.Errorf("missing or null field")
	}

	scan := json.NewDecoder(bytes.NewReader(raw))
	token, err := scan.Token()
	if err != nil || token != json.Delim('{') {
		return fmt.Errorf("object required")
	}
	seen := map[string]bool{}
	for scan.More() {
		key, err := scan.Token()
		name, ok := key.(string)
		if err != nil || !ok || seen[name] {
			return fmt.Errorf("duplicate field")
		}
		seen[name] = true
		var value json.RawMessage
		if scan.Decode(&value) != nil {
			return fmt.Errorf("invalid field")
		}
	}
	if token, err = scan.Token(); err != nil || token != json.Delim('}') || scan.Decode(new(any)) != io.EOF {
		return fmt.Errorf("trailing data")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	return decoder.Decode(out)
}
func queueClaimCurrent(claim startClaim) bool {
	latest, ok := queueCurrentClaim(claim.Proof.RoomID, claim.Proof.EngineDeviceID)
	return ok && latest.RT == claim.RT && claim.RT.live.startIntentCurrent(claim.Intent) && latest.Intent == claim.Intent && latest.Proof.Mode == "same_session" && latest.Proof.OutputSelected && latest.Proof.HomepodID == claim.Proof.HomepodID && sameAliasBinding(latest.Proof.AliasBinding, claim.Proof.AliasBinding)
}
func queueSnapshotValid(reply queueNativeSnapshot, claim startClaim, user string) bool {
	if reply.Schema != "podconnect.native_queue.v1" || reply.TargetID != claim.Proof.EngineDeviceID || reply.RoomID != claim.Proof.RoomID || reply.AccountUserID != user || reply.ConnectionID == "" || len(reply.ConnectionID) > 160 || reply.SessionID == "" || len(reply.SessionID) > 160 || claim.Proof.AliasBinding == nil || reply.AliasIncarnation != claim.Proof.AliasBinding.Incarnation || !queueObservationPattern.MatchString(reply.ObservationID) || reply.QueueRevision >= 1<<53 || reply.ExpiresAfterMS < 1 || reply.ExpiresAfterMS > 10000 || len(reply.Items) > 20 {
		return false
	}
	ids := map[string]bool{}
	for i, item := range reply.Items {
		if !queueOptionPattern.MatchString(item.ID) || ids[item.ID] || item.Position != i || !queueTrackURI.MatchString(item.URI) {
			return false
		}
		ids[item.ID] = true
	}
	return true
}
func queueReadHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method", 405)
		return
	}
	q := r.URL.Query()
	if len(q) != 3 || len(q["room_id"]) != 1 || len(q["target_id"]) != 1 || len(q["account_user_id"]) != 1 || q.Get("room_id") == "" || len(q.Get("room_id")) > 128 || !engineIDPattern.MatchString(q.Get("target_id")) || q.Get("account_user_id") == "" || len(q.Get("account_user_id")) > 160 {
		http.Error(w, "arguments", 400)
		return
	}
	queueManagerState.Lock()
	if queueManagerState.Busy {
		queueManagerState.Unlock()
		http.Error(w, "busy", 409)
		return
	}
	queueManagerState.Busy = true
	queueManagerState.Unlock()
	defer func() { queueManagerState.Lock(); queueManagerState.Busy = false; queueManagerState.Unlock() }()
	claim, ok := queueCurrentClaim(q.Get("room_id"), q.Get("target_id"))
	if !ok || claim.Proof.Mode != "same_session" || !claim.Proof.OutputSelected {
		http.Error(w, "route", 409)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	started := time.Now()
	raw, err := queueNativeRPC(ctx, claim.RT.room.Librespot, http.MethodGet, "/player/queue_occurrences?"+url.Values{"room_id": {q.Get("room_id")}, "account_user_id": {q.Get("account_user_id")}}.Encode(), nil)
	var reply queueNativeSnapshot
	if err != nil || decodeQueueManagerJSON(raw, &reply) != nil || !queueSnapshotValid(reply, claim, q.Get("account_user_id")) || !queueClaimCurrent(claim) {
		http.Error(w, "native queue unavailable", 409)
		return
	}
	remaining := reply.ExpiresAfterMS - int(time.Since(started).Milliseconds())
	if remaining <= 0 {
		http.Error(w, "expired", 409)
		return
	}
	reply.ExpiresAfterMS = remaining
	reply.HomepodID = claim.Proof.HomepodID
	queueManagerState.Lock()
	queueManagerState.Offer = &queueManagerOffer{reply, claim, time.Now().Add(time.Duration(remaining) * time.Millisecond)}
	queueManagerState.Unlock()
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(reply)
}

type queueManagerJump struct {
	TargetID      string `json:"target_id"`
	RoomID        string `json:"room_id"`
	AccountUserID string `json:"account_user_id"`
	ObservationID string `json:"observation_id"`
	SessionID     string `json:"session_id"`
	QueueRevision uint64 `json:"queue_revision"`
	OccurrenceID  string `json:"occurrence_id"`
}

func queueJumpHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method", 405)
		return
	}
	if r.URL.RawQuery != "" {
		http.Error(w, "arguments", 400)
		return
	}
	raw, err := io.ReadAll(io.LimitReader(r.Body, 4097))
	defer r.Body.Close()
	var body queueManagerJump
	if err != nil || len(raw) > 4096 || decodeQueueManagerJSON(raw, &body) != nil || !queueObservationPattern.MatchString(body.ObservationID) || !queueOptionPattern.MatchString(body.OccurrenceID) {
		http.Error(w, "arguments", 400)
		return
	}
	queueManagerState.Lock()
	offer := queueManagerState.Offer
	if queueManagerState.Busy || offer == nil || offer.Snapshot.ObservationID != body.ObservationID {
		queueManagerState.Unlock()
		http.Error(w, "stale", 409)
		return
	}
	queueManagerState.Offer = nil
	queueManagerState.Busy = true
	queueManagerState.Unlock()
	defer func() { queueManagerState.Lock(); queueManagerState.Busy = false; queueManagerState.Unlock() }()
	s := offer.Snapshot
	if !time.Now().Before(offer.Expires) || body.TargetID != s.TargetID || body.RoomID != s.RoomID || body.AccountUserID != s.AccountUserID || body.SessionID != s.SessionID || body.QueueRevision != s.QueueRevision || !queueClaimCurrent(offer.Claim) {
		http.Error(w, "stale", 409)
		return
	}
	found := false
	for _, item := range s.Items {
		if item.ID == body.OccurrenceID {
			found = true
			break
		}
	}
	if !found {
		http.Error(w, "occurrence", 409)
		return
	}
	nativeBody, _ := json.Marshal(map[string]any{"room_id": body.RoomID, "account_user_id": body.AccountUserID, "observation_id": body.ObservationID, "session_id": body.SessionID, "queue_revision": body.QueueRevision, "occurrence_id": body.OccurrenceID})
	ctx, cancel := context.WithTimeout(r.Context(), 4*time.Second)
	defer cancel()
	reply, err := queueNativeRPC(ctx, offer.Claim.RT.room.Librespot, http.MethodPost, "/player/queue_occurrence", nativeBody)
	var ack struct {
		OK            bool   `json:"ok"`
		Schema        string `json:"schema"`
		ObservationID string `json:"observation_id"`
		OccurrenceID  string `json:"occurrence_id"`
		Physical      bool   `json:"physical_result_verified"`
	}
	if err != nil || decodeQueueManagerJSON(reply, &ack) != nil || !ack.OK || ack.Schema != "podconnect.native_queue_commit.v1" || ack.ObservationID != body.ObservationID || ack.OccurrenceID != body.OccurrenceID || ack.Physical {
		http.Error(w, "outcome unknown", 502)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(reply)
}

// Presence is part of the protocol: absent/null false, zero or empty collections
// must not become apparently verified defaults during typed JSON decoding.
func queueRequiredJSON(raw []byte, typ reflect.Type) bool {
	if string(raw) == "null" {
		return false
	}
	switch typ.Kind() {
	case reflect.Struct:
		var fields map[string]json.RawMessage
		if json.Unmarshal(raw, &fields) != nil || fields == nil {
			return false
		}
		for i := 0; i < typ.NumField(); i++ {
			f := typ.Field(i)
			tag := strings.Split(f.Tag.Get("json"), ",")
			if len(tag) == 0 || tag[0] == "-" || tag[0] == "" {
				continue
			}
			value, exists := fields[tag[0]]
			optional := len(tag) > 1 && tag[1] == "omitempty"
			if !exists {
				if optional {
					continue
				}
				return false
			}
			if !queueRequiredJSON(value, f.Type) {
				return false
			}
		}
	case reflect.Slice:
		var items []json.RawMessage
		if json.Unmarshal(raw, &items) != nil || items == nil {
			return false
		}
		for _, item := range items {
			if !queueRequiredJSON(item, typ.Elem()) {
				return false
			}
		}
	}
	return true
}
