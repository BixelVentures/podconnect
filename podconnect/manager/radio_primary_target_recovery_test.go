package main

import (
	"context"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// Exercises the current primary producer and coordinator, with an inert Native
// port. It proves projection/recovery without setup or effects, not mDNS or audio.
func TestPrimaryConfiguredTargetReturnsThroughPassiveRefreshWithoutEffects(t *testing.T) {
	ctx := context.Background()
	f := &primaryRadioFixture{t: t, state: radioPrimaryState{
		Schema: primaryRadioSchema, Protocol: primaryRadioProtocol,
		Process: strings.Repeat("a", 64), Revision: "7", Missing: []string{},
		Targets: []radioProducerTarget{
			{radioTarget: radioTarget{ID: "r1", Name: "Kitchen", Selectable: true, Availability: "available"}, HomePodID: "1"},
			{radioTarget: radioTarget{ID: "r2", Name: "Configured bedroom", Availability: "unavailable"}, HomePodID: "2"},
		},
		Observation: &radioNativeObservation{State: "playing", SourceKind: "spotify", TargetID: "r1", OwnerGeneration: 1, ObservedAt: radioTestNow() - 1, ValidUntil: radioTestNow() + 20},
	}}
	directory, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	p, err := newPrimaryRadioProducer(f, filepath.Join(directory, "private-journal"))
	if err != nil {
		t.Fatal(err)
	}
	f.p = p
	c := newRadioCoordinator(p)
	if err := c.enable(ctx); err != nil {
		t.Fatal(err)
	}
	c.refresh(ctx)
	initial := c.snapshot()
	if len(initial.Targets) != 2 || initial.Targets[1].Selectable || initial.Targets[1].Availability != "unavailable" {
		t.Fatalf("configured offline target missing or selectable: %#v", initial.Targets)
	}
	configured := append([]radioProducerTarget{}, f.state.Targets...)
	previous := initial
	for i, available := range []bool{true, false, true} {
		f.state.Revision = strconv.Itoa(8 + i)
		f.state.Targets[1].Selectable = available
		f.state.Targets[1].Availability = "unavailable"
		if available {
			f.state.Targets[1].Availability = "available"
		}
		c.refresh(ctx)
		current := c.snapshot()
		if len(current.Targets) != 2 || current.Targets[1].ID != configured[1].ID || current.Targets[1].Name != configured[1].Name || current.Targets[1].Selectable != available || current.Targets[1].Availability != f.state.Targets[1].Availability {
			t.Fatalf("transition %d did not recover same configured target: %#v", i, current.Targets)
		}
		if current.Revision <= previous.Revision || current.Generation != initial.Generation || current.OwnerGeneration != initial.OwnerGeneration || current.ProcessID != initial.ProcessID || current.TargetID != initial.TargetID || current.StationID != initial.StationID || current.SourceKind != initial.SourceKind || current.State != initial.State {
			t.Fatalf("transition %d changed source identity or failed to publish target revision: before=%#v after=%#v", i, initial, current)
		}
		if !reflect.DeepEqual(current.Targets[0], initial.Targets[0]) || !slices.Contains(current.Actions, "start") || len(f.calls) != 0 {
			t.Fatalf("transition %d changed incumbent, lost normal start, or sent effects: targets=%#v actions=%v effects=%v", i, current.Targets, current.Actions, f.calls)
		}
		if f.state.Targets[1].HomePodID != configured[1].HomePodID {
			t.Fatal("configured physical binding changed")
		}
		// Re-reading identical fresh state must not create another target revision.
		c.refresh(ctx)
		if again := c.snapshot(); again.Revision != current.Revision || len(f.calls) != 0 {
			t.Fatalf("unchanged passive refresh caused revision/effect: %#v %v", again, f.calls)
		}
		previous = current
	}
	t.Log("VERIFIED primary configured target unavailable -> available -> unavailable -> available through passive coordinator refresh; same ID/name/physical binding and incumbent source; zero Native commands; no setup")
}
