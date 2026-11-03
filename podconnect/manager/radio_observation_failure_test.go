package main

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"
)

func TestRadioFailedSourceReadRevokesControlsWithoutReplayingEffects(t *testing.T) {
	c, base := radioTestOwner(t)
	radioDo(t, c, "source-start", "start", "p3", "r1")
	backend := &radioReconFake{radioFake: base, readErr: errors.New("sender exited")}
	c.backend = backend
	fence, operation := c.fence, c.operation
	calls := append([]string{}, base.calls...)
	c.refresh(context.Background())
	state := c.snapshot()
	if state.State != "unknown" || len(state.Actions) != 0 || state.ObservedAt != nil || state.ValidUntil != nil || state.VolumePercent != nil || state.StationID != "p3" || state.TargetID != "r1" || c.fence != fence || c.operation != operation {
		t.Fatal("failed source retained authority or lost reconciliation identity", state)
	}
	c.refresh(context.Background())
	if c.snapshot().State != "unknown" || !reflect.DeepEqual(base.calls, calls) {
		t.Fatal("failed read replayed an effect or silently unlocked UNKNOWN")
	}
	backend.readErr = nil
	backend.result = radioReconciliation{ActionID: fence.ActionID, Generation: fence.Generation, Token: fence.Token, Terminal: true, Outcome: "committed", Observation: radioNativeObservation{State: "playing", SourceKind: "radio", StationID: "p3", TargetID: "r1", OwnerGeneration: fence.OwnerGeneration, ObservedAt: time.Now().Unix(), ValidUntil: time.Now().Unix() + 25}}
	c.refresh(context.Background())
	if c.snapshot().State != "playing" || !reflect.DeepEqual(base.calls, calls) {
		t.Fatal("fresh correlated recovery failed or replayed an effect")
	}
}

func TestRadioFailedReadDoesNotReplaceSpotifyOwner(t *testing.T) {
	c, base := radioTestOwner(t)
	c.state.State, c.state.SourceKind = "playing", "spotify"
	c.backend = &radioReconFake{radioFake: base, readErr: errors.New("radio unavailable")}
	before := c.snapshot()
	c.refresh(context.Background())
	if !reflect.DeepEqual(before, c.snapshot()) || len(base.calls) != 0 {
		t.Fatal("radio read failure changed Spotify presentation or issued an effect")
	}
}
