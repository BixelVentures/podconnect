package main

// A passive terminal read proves retirement of one exact operation. It never
// issues an effect, renews an offer or makes an unknown action retryable.
import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

func validRadioReconciliation(r radioReconciliation, f radioFence, operation radioCommand) bool {
	if !r.Terminal || r.ActionID != f.ActionID || r.Generation != f.Generation || (f.Token != "" && r.Token != f.Token) || !validRadioObservation(r.Observation) || (r.Outcome != "committed" && r.Outcome != "rejected" && r.Outcome != "preempted") {
		return false
	}
	if r.Observation.State == "unknown" || r.Observation.SourceKind == "unknown" {
		return false
	}
	if r.Outcome != "committed" {
		return true
	}
	if r.Observation.OwnerGeneration == 0 || (f.OwnerGeneration != 0 && r.Observation.OwnerGeneration != f.OwnerGeneration) {
		return false
	}
	switch operation.Action {
	case "start", "move":
		return r.Observation.State == "playing" && r.Observation.SourceKind == "radio" && r.Observation.StationID == operation.StationID && r.Observation.TargetID == operation.TargetID
	case "volume":
		return r.Observation.SourceKind == "radio" && r.Observation.StationID == operation.StationID && r.Observation.TargetID == operation.TargetID && r.Observation.VolumePercent != nil && operation.VolumePercent != nil && *r.Observation.VolumePercent == *operation.VolumePercent
	case "stop":
		return r.Observation.State == "stopped" && r.Observation.SourceKind == "none"
	default:
		return false
	}
}

type radioActionResult struct {
	Schema      string                 `json:"schema"`
	ProcessID   string                 `json:"process_id"`
	ActionID    string                 `json:"action_id"`
	Generation  uint64                 `json:"generation"`
	Terminal    bool                   `json:"terminal"`
	Outcome     string                 `json:"outcome"`
	Observation radioNativeObservation `json:"observation"`
}

func (c *radioCoordinator) result(ctx context.Context, process, action string, generation uint64) (radioActionResult, error) {
	c.mu.Lock()
	f, operation, backend := c.fence, c.operation, c.backend
	allowed := c.state.Enabled && !c.pending && process == c.state.ProcessID && action != "" && len(action) <= 128 && action == operation.ActionID && action == f.ActionID && generation == f.Generation
	c.mu.Unlock()
	if !allowed {
		return radioActionResult{}, errRadioUnknown
	}
	reconciler, ok := backend.(interface {
		Reconcile(context.Context, radioFence) (radioReconciliation, error)
	})
	if !ok {
		return radioActionResult{}, errRadioUnknown
	}
	r, err := reconciler.Reconcile(ctx, f)
	now := time.Now().Unix()
	if err != nil || !validRadioReconciliation(r, f, operation) || r.Observation.ObservedAt < now-30 || r.Observation.ValidUntil > now+30 {
		return radioActionResult{}, errRadioUnknown
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	// A delayed result must not cross replacement custody or a newer operation.
	if c.pending || !c.state.Enabled || c.state.ProcessID != process || c.operation != operation || c.fence != f {
		return radioActionResult{}, errRadioUnknown
	}
	return radioActionResult{"podconnect.radio-result.v1", process, action, generation, true, r.Outcome, r.Observation}, nil
}

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
	result, err := radioOwner.result(r.Context(), q.Get("process_id"), q.Get("action_id"), generation)
	if err != nil {
		http.Error(w, "radio result unresolved", http.StatusConflict)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(result)
}
