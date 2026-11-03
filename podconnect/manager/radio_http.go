package main

import (
	"crypto/subtle"
	"encoding/json"
	"io"
	"net/http"
)

var radioOwner = newRadioCoordinator(nil)

func radioAuthed(r *http.Request) bool {
	token := readAttentionToken()
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
	if err != nil {
		if !state.Enabled {
			w.WriteHeader(http.StatusServiceUnavailable)
		} else {
			w.WriteHeader(http.StatusConflict)
		}
	}
	status := "accepted"
	if err != nil {
		status = "rejected"
		if state.State == "unknown" {
			status = "unknown"
		}
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"schema": "podconnect.radio-action.v1", "action_id": q.ActionID, "status": status, "retry_allowed": false})
}
