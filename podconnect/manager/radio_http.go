package main

import (
	"crypto/subtle"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// readRadioTransportToken guards the radio routes PodVoice calls. Older
// configurations without the dedicated option keep using the attention token.
func readRadioTransportToken() string {
	raw, err := os.ReadFile(filepath.Join(dataDir, "options.json"))
	if err != nil {
		return ""
	}
	var options struct {
		RadioTransportToken string          `json:"radio_transport_token"`
		AttentionToken      json.RawMessage `json:"attention_token"`
	}
	if json.Unmarshal(raw, &options) != nil {
		return ""
	}
	if token := strings.TrimSpace(options.RadioTransportToken); token != "" {
		return token
	}
	var legacy string
	if json.Unmarshal(options.AttentionToken, &legacy) != nil {
		return ""
	}
	return strings.TrimSpace(legacy)
}

func radioAuthed(r *http.Request) bool {
	token := readRadioTransportToken()
	given := r.Header.Get("X-PodConnect-Token")
	return token != "" && subtle.ConstantTimeCompare([]byte(token), []byte(given)) == 1
}

func radioStateHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if !radioAuthed(r) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", "GET")
		http.Error(w, "method not allowed", 405)
		return
	}
	radioOwner.refresh(r.Context())
	writeJSON(w, radioOwner.snapshot())
}

func radioActionHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if !radioAuthed(r) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		http.Error(w, "method not allowed", 405)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	var q radioCommand
	if dec.Decode(&q) != nil || dec.Decode(new(any)) != io.EOF {
		http.Error(w, "invalid radio command", 400)
		return
	}
	state, err := radioOwner.execute(r.Context(), q)
	w.Header().Set("Content-Type", "application/json")
	status := "accepted"
	if err != nil {
		status = "rejected"
		if !state.Enabled {
			w.WriteHeader(http.StatusServiceUnavailable)
		} else {
			w.WriteHeader(http.StatusConflict)
		}
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"schema": "podconnect.radio-action.v1", "action_id": q.ActionID, "status": status, "retry_allowed": false})
}

// radioResultHandler answers for an action this process executed. Every
// action is terminal when its reply is sent, so this is a plain lookup.
func radioResultHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if !radioAuthed(r) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", "GET")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	q, queryErr := url.ParseQuery(r.URL.RawQuery)
	if queryErr != nil || len(q) != 3 || len(q["process_id"]) != 1 || len(q["action_id"]) != 1 || len(q["generation"]) != 1 {
		http.Error(w, "invalid radio result query", 400)
		return
	}
	generation, err := strconv.ParseUint(q.Get("generation"), 10, 64)
	if err != nil || strconv.FormatUint(generation, 10) != q.Get("generation") {
		http.Error(w, "invalid radio generation", 400)
		return
	}
	radioOwner.mu.Lock()
	result, ok := radioOwner.results[q.Get("action_id")]
	process := radioOwner.state.ProcessID
	radioOwner.mu.Unlock()
	if !ok || process != q.Get("process_id") || result.Generation != generation {
		http.Error(w, "radio result unresolved", http.StatusConflict)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(result)
}
