package main

// Radio is deliberately additive. No existing Spotify owner is altered by
// constructing this coordinator. Only a native owner that fences every receiver
// effect can implement radioBackend; HTTP/queue acceptance is not that proof.
import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"time"
)

type radioStation struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

var radioStations = []radioStation{{"p3", "P3"}, {"p1", "P1"}, {"p4-ostjylland", "P4 Østjylland"}, {"p6-beat", "P6 Beat"}, {"p8-jazz", "P8 Jazz"}}
var radioEnableGates = []string{"independent_sender_identity_and_pairing", "native_all_effect_path_fences", "exact_spotify_pause_observation_and_receiver_release", "external_spotify_start_event_including_same_track", "durable_unknown_effect_custody", "physical_homepod_acceptance"}

type radioTarget struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	Availability string `json:"availability"`
	Selectable   bool   `json:"selectable"`
	Default      bool   `json:"default"`
}
type radioState struct {
	Schema           string         `json:"schema"`
	ProcessID        string         `json:"process_id"`
	Actions          []string       `json:"actions"`
	SourceKind       string         `json:"source_kind"`
	OwnerGeneration  uint64         `json:"owner_generation"`
	RetrievedAt      *int64         `json:"retrieved_at"`
	ValidUntil       *int64         `json:"valid_until"`
	ObservedAt       *int64         `json:"observed_at"`
	VolumePercent    *int           `json:"volume_percent"`
	Enabled          bool           `json:"enabled"`
	BlockedReasons   []string       `json:"blocked_reasons"`
	Stations         []radioStation `json:"stations"`
	Targets          []radioTarget  `json:"targets"`
	Capabilities     []string       `json:"capabilities"`
	State            string         `json:"state"`
	StationID        string         `json:"station_id"`
	TargetID         string         `json:"target_id"`
	Generation       uint64         `json:"generation"`
	Revision         uint64         `json:"revision"`
	Error            string         `json:"error,omitempty"`
	PhysicalVerified bool           `json:"physical_result_verified"`
}
type radioCommand struct {
	ActionID                string `json:"action_id"`
	Action                  string `json:"action"`
	StationID               string `json:"station_id,omitempty"`
	TargetID                string `json:"target_id,omitempty"`
	ExpectedRevision        uint64 `json:"expected_revision"`
	ExpectedProcessID       string `json:"expected_process_id"`
	ExpectedOwnerGeneration uint64 `json:"expected_owner_generation"`
	ExpectedGeneration      uint64 `json:"expected_generation"`
	VolumePercent           *int   `json:"volume_percent,omitempty"`
}

// A fence represents durable native custody, not a manager-side mutex. Reserve
// atomically checks account + both endpoints and drains old receiver effects.
// PauseAndRelease rechecks exact account/device, pauses once, observes pause,
// releases only that receiver and proves completion. Every mutator must reject
// a stale generation, including delayed duck/volume restoration callbacks.
type radioFence struct {
	OwnerGeneration uint64
	Token           string
	ActionID        string
	Generation      uint64
}
type radioBackend interface {
	Ready(context.Context) ([]radioTarget, []string, error)
	Preflight(context.Context, string, string) error
	Reserve(context.Context, string, string, string, uint64) (radioFence, error)
	PauseAndRelease(context.Context, radioFence, string) error
	Start(context.Context, radioFence, string, string) error
	Stop(context.Context, radioFence, string) error
	Volume(context.Context, radioFence, string, int) error
	// Finish retires proven terminal custody, never an unresolved effect.
	Finish(context.Context, radioFence) (radioNativeObservation, error)
}

var errRadioUnknown = errors.New("radio effect outcome unknown")
var errRadioUnavailable = errors.New("radio safety prerequisites unavailable")

type radioCoordinator struct {
	mu        sync.Mutex
	backend   radioBackend
	state     radioState
	pending   bool
	fence     radioFence
	operation radioCommand
	seen      map[string]bool
}

func newRadioCoordinator(b radioBackend) *radioCoordinator {
	return &radioCoordinator{backend: b, state: radioState{Schema: "podconnect.radio.v1", ProcessID: attentionUUID(), SourceKind: "unknown", Actions: []string{}, State: "idle", Revision: 1, Stations: append([]radioStation(nil), radioStations...), Targets: []radioTarget{}, Capabilities: []string{}, BlockedReasons: append([]string(nil), radioEnableGates...)}, seen: make(map[string]bool)}
}
func (c *radioCoordinator) snapshot() radioState {
	c.mu.Lock()
	defer c.mu.Unlock()
	s := c.state
	s.Actions = c.actionsLocked()
	s.Stations = append([]radioStation{}, s.Stations...)
	s.Targets = append([]radioTarget{}, s.Targets...)
	s.Capabilities = append([]string{}, s.Capabilities...)
	s.BlockedReasons = append([]string{}, s.BlockedReasons...)
	return s
}

// Capabilities describe protocol support; actions are the executable offers for
// this exact observed state. Snapshot and final admission share this owner rule.
func (c *radioCoordinator) actionsLocked() []string {
	actions := []string{}
	if !c.state.Enabled || c.pending || c.state.State == "preparing" || c.state.State == "unknown" {
		return actions
	}
	selectable, movable, currentRoute := false, false, false
	for _, target := range c.state.Targets {
		// Owned Stop/Volume depend on the fresh radio observation below, not
		// on Native's ability to admit another receiver/source right now.
		if target.ID == c.state.TargetID {
			currentRoute = true
		}
		if target.Selectable && target.Availability == "available" {
			selectable = true
			if target.ID != c.state.TargetID {
				movable = true
			}
		}
	}
	playingRadio := c.state.State == "playing" && c.state.SourceKind == "radio"
	if selectable && slices.Contains(c.state.Capabilities, "start") && (!playingRadio || slices.Contains(c.state.Capabilities, "move")) {
		actions = append(actions, "start")
	}
	if !playingRadio || !currentRoute {
		return actions
	}
	now := time.Now().Unix()
	if c.state.ObservedAt == nil || c.state.ValidUntil == nil || *c.state.ObservedAt > now || *c.state.ValidUntil <= now {
		return actions
	}
	if slices.Contains(c.state.Capabilities, "stop") {
		actions = append(actions, "stop")
	}
	if movable && slices.Contains(c.state.Capabilities, "move") {
		actions = append(actions, "move")
	}
	if c.state.VolumePercent != nil && *c.state.VolumePercent >= 0 && *c.state.VolumePercent <= 100 &&
		slices.Contains(c.state.Capabilities, "volume") {
		actions = append(actions, "volume")
	}
	return actions
}

// Enable is intentionally not exposed through HTTP/config. Native capability
// verification and physical acceptance must precede wiring a backend in main.
func (c *radioCoordinator) enable(ctx context.Context) error {
	if c.backend == nil {
		return errRadioUnavailable
	}
	targets, gates, err := c.backend.Ready(ctx)
	if err != nil {
		return err
	}
	if len(gates) != 0 || len(targets) == 0 {
		return errRadioUnavailable
	}
	ids := map[string]bool{}
	for _, t := range targets {
		if t.ID == "" || ids[t.ID] {
			return errRadioUnavailable
		}
		ids[t.ID] = true
	}
	floor := uint64(0)
	if history, ok := c.backend.(interface{ RadioGenerationFloor() uint64 }); ok {
		floor = history.RadioGenerationFloor()
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if floor > c.state.Generation {
		c.state.Generation = floor
	}
	c.state.Enabled = true
	c.state.Targets = targets
	c.state.BlockedReasons = []string{}
	c.state.Capabilities = []string{"start", "stop", "move", "volume"}
	if backend, ok := c.backend.(interface{ RadioCapabilities() []string }); ok {
		c.state.Capabilities = backend.RadioCapabilities()
	}
	return nil
}
func (c *radioCoordinator) execute(ctx context.Context, q radioCommand) (radioState, error) {
	c.mu.Lock()
	if !c.state.Enabled {
		c.mu.Unlock()
		return c.snapshot(), errRadioUnavailable
	}
	if q.ActionID == "" || len(q.ActionID) > 128 || c.seen[q.ActionID] || q.ExpectedRevision != c.state.Revision || q.ExpectedProcessID != c.state.ProcessID || q.ExpectedGeneration != c.state.Generation || q.ExpectedOwnerGeneration != c.state.OwnerGeneration || c.pending || c.state.State == "unknown" {
		c.mu.Unlock()
		return c.snapshot(), errors.New("radio stale, duplicate or unresolved action")
	}
	offered := false
	for _, action := range c.actionsLocked() {
		offered = offered || action == q.Action
	}
	if !offered {
		c.mu.Unlock()
		return c.snapshot(), errors.New("radio action not currently offered")
	}
	validStation := false
	for _, s := range radioStations {
		validStation = validStation || s.ID == q.StationID
	}
	validTarget := false
	for _, t := range c.state.Targets {
		validTarget = validTarget || (t.ID == q.TargetID && t.Selectable && t.Availability == "available")
	}
	switch q.Action {
	case "start":
		if !validStation || !validTarget || q.VolumePercent != nil {
			c.mu.Unlock()
			return c.snapshot(), errors.New("invalid station or target")
		}
	case "move":
		if !validTarget || (c.state.State != "playing" || c.state.SourceKind != "radio") || q.StationID != "" || q.VolumePercent != nil {
			c.mu.Unlock()
			return c.snapshot(), errors.New("invalid move")
		}
		q.StationID = c.state.StationID
	case "stop":
		if q.StationID != "" || q.TargetID != "" || q.VolumePercent != nil {
			c.mu.Unlock()
			return c.snapshot(), errors.New("invalid stop")
		}
	case "volume":
		if (c.state.State != "playing" || c.state.SourceKind != "radio") || q.VolumePercent == nil || *q.VolumePercent < 0 || *q.VolumePercent > 100 || q.StationID != "" || q.TargetID != "" {
			c.mu.Unlock()
			return c.snapshot(), errors.New("invalid volume")
		}
	default:
		c.mu.Unlock()
		return c.snapshot(), errors.New("unsupported radio action")
	}
	if len(c.seen) >= 4096 {
		c.mu.Unlock()
		return c.snapshot(), errors.New("radio action history full; reconcile required")
	}
	c.seen[q.ActionID] = true
	old := c.state
	if (q.Action == "start" || q.Action == "move") && old.State == "playing" && old.SourceKind == "radio" && old.StationID == q.StationID && old.TargetID == q.TargetID {
		c.mu.Unlock()
		return c.snapshot(), nil
	}
	if q.Action == "stop" && (old.State != "playing" || old.SourceKind != "radio") {
		c.mu.Unlock()
		return c.snapshot(), nil
	}
	c.operation = q
	if q.Action == "volume" {
		c.operation.StationID = old.StationID
		c.operation.TargetID = old.TargetID
	}
	c.pending = true
	c.state.Generation++
	generation := c.state.Generation
	c.state.Revision++
	c.state.State = "preparing"
	c.state.Error = ""
	c.mu.Unlock()
	// Preflight has no receiver effects. Failure preserves previously observed audio.
	if q.Action == "start" || q.Action == "move" {
		prep, cancel := context.WithTimeout(ctx, 5*time.Second)
		var err error
		if preparer, ok := c.backend.(interface {
			PreflightOperation(context.Context, string, string, string, uint64) error
		}); ok {
			err = preparer.PreflightOperation(prep, q.StationID, q.TargetID, q.ActionID, generation)
		} else {
			err = c.backend.Preflight(prep, q.StationID, q.TargetID)
		}
		cancel()
		if err != nil {
			return c.complete(generation, old, radioFence{ActionID: q.ActionID, Generation: generation}, fmt.Errorf("preflight: %w", err), false)
		}
	}
	c.mu.Lock()
	stillCurrent := c.state.Generation == generation && c.pending
	c.mu.Unlock()
	if !stillCurrent {
		return c.snapshot(), errRadioUnknown
	}
	effect, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	f, err := c.backend.Reserve(effect, old.TargetID, q.TargetID, q.ActionID, generation)
	if err != nil {
		var noEffects *radioProducerNoReceiverEffects
		if errors.As(err, &noEffects) {
			return c.complete(generation, old, f, err, false)
		}
		return c.complete(generation, old, radioFence{ActionID: q.ActionID, Generation: generation}, err, true)
	}
	c.mu.Lock()
	c.fence = f
	c.mu.Unlock()
	if f.Token == "" || f.Generation != generation || f.ActionID != q.ActionID {
		return c.complete(generation, old, f, errRadioUnknown, true)
	}
	// Stop the original source before acquiring a new destination. No rollback.
	if old.State == "playing" && old.SourceKind == "radio" && (q.Action == "stop" || q.Action == "move" || q.Action == "start") {
		err = c.backend.Stop(effect, f, old.TargetID)
	}
	if err == nil && (q.Action == "start" || q.Action == "move") {
		err = c.backend.PauseAndRelease(effect, f, q.TargetID)
		if err == nil {
			err = c.backend.Start(effect, f, q.StationID, q.TargetID)
		}
	}
	if err == nil && q.Action == "volume" {
		err = c.backend.Volume(effect, f, old.TargetID, *q.VolumePercent)
	}
	if err != nil {
		return c.complete(generation, old, f, err, true)
	}
	observation, err := c.backend.Finish(effect, f)
	if err != nil {
		return c.complete(generation, old, f, err, true)
	}
	if !validRadioObservation(observation) || observation.OwnerGeneration != f.OwnerGeneration {
		return c.complete(generation, old, f, errRadioUnknown, true)
	}
	if (q.Action == "start" || q.Action == "move") && (observation.State != "playing" || observation.SourceKind != "radio" || observation.StationID != q.StationID || observation.TargetID != q.TargetID) {
		return c.complete(generation, old, f, errRadioUnknown, true)
	}
	if q.Action == "stop" && (observation.State != "stopped" || observation.SourceKind != "none") {
		return c.complete(generation, old, f, errRadioUnknown, true)
	}
	if q.Action == "volume" && (observation.SourceKind != "radio" || observation.TargetID != old.TargetID || observation.StationID != old.StationID || observation.VolumePercent == nil || *observation.VolumePercent != *q.VolumePercent) {
		return c.complete(generation, old, f, errRadioUnknown, true)
	}
	next := old
	applyRadioObservation(&next, observation)

	return c.complete(generation, next, f, nil, false)
}
func (c *radioCoordinator) complete(g uint64, s radioState, f radioFence, err error, effected bool) (radioState, error) {
	c.mu.Lock()
	if c.state.Generation != g {
		c.mu.Unlock()
		return c.snapshot(), errRadioUnknown
	}
	c.pending = false
	c.state.Revision++
	s.Revision = c.state.Revision
	s.Generation = g
	if err != nil {
		s.Error = err.Error()
		if effected {
			s.State = "unknown"
			s.SourceKind = "unknown"
			s.ObservedAt = nil
			s.RetrievedAt = nil
			s.ValidUntil = nil
		}
	}
	c.state = s
	c.fence = f
	c.mu.Unlock()
	return c.snapshot(), err
}

// Called only by a source-bound native Spotify START event. Same-track starts
// count; polls and old playing snapshots must never manufacture this event.
// Backend.Preempt must stop/retire radio atomically before publishing the event.
func (c *radioCoordinator) observeNativePreempt(target string, g uint64) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.state.TargetID != target || c.state.Generation != g {
		return false
	}
	c.state.Generation++
	c.state.Revision++
	c.state.State = "unknown"
	c.state.SourceKind = "unknown"
	c.state.StationID = ""
	c.state.ObservedAt = nil
	c.state.ValidUntil = nil
	c.state.RetrievedAt = nil
	c.state.VolumePercent = nil
	c.state.Error = ""
	c.pending = false
	return true
}
