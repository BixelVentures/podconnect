package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

type primaryRadioFixture struct {
	t       *testing.T
	p       *radioProducer
	state   radioPrimaryState
	session radioPrimarySession
	calls   []string
	lose    string
	badStop bool
}

func (f *primaryRadioFixture) PrimaryState(context.Context) (radioPrimaryState, error) {
	return f.state, nil
}

func (f *primaryRadioFixture) PrimaryCommand(_ context.Context, q radioPrimaryCommand) (radioPrimaryReceipt, error) {
	f.calls = append(f.calls, q.Phase)
	var r radioProducerRecord
	raw, err := os.ReadFile(filepath.Join(f.p.journal.directory, producerRecordName(q.Handoff)))
	if err != nil || json.Unmarshal(raw, &r) != nil {
		f.t.Fatalf("effect lacks a durable journal entry: %v", err)
	}
	if r.Primary == nil || r.Fence.Token != q.Handoff || r.Fence.ActionID != q.Action ||
		r.Request.Target != q.Target || r.Request.Process != q.Process || q.Schema != primaryRadioSchema {
		f.t.Fatalf("effect was not journaled under the exact action: %+v", q)
	}
	if q.Phase != "reserve" && r.Primary.LastCommand == nil {
		f.t.Fatal("effect command missing from the journal")
	}
	result := radioPrimaryReceipt{radioPrimaryCommand: q}
	phase := q.Phase
	if phase == "result" {
		phase = q.ForPhase
	} else if f.lose == phase {
		return result, errRadioUnknown
	}
	generation, _ := strconv.ParseUint(q.Generation, 10, 64)
	switch phase {
	case "reserve":
		result.Phase = "reserved"
		result.OwnerGeneration = strconv.FormatUint(generation+10, 10)
	case "prepare_start":
		result.Phase = "prepared"
	case "start":
		result.Phase = "playing"
		result.Session = &f.session
		result.Observation = &radioNativeObservation{
			State: "playing", SourceKind: "radio", TargetID: q.Target, StationID: q.Station,
			OwnerGeneration: generation + 10, ObservedAt: radioTestNow() - 1, ValidUntil: radioTestNow() + 20,
		}
		f.state.Session, f.state.Observation = result.Session, result.Observation
	case "stop":
		if q.Session == nil || *q.Session != f.session {
			f.t.Fatalf("stop did not retain the exact prior C route: %+v", q)
		}
		if q.Phase != "result" {
			result.Phase = "pending"
			return result, nil
		}
		result.Phase, result.TeardownCSeq = "released", 7
		result.Observation = &radioNativeObservation{
			State: "stopped", SourceKind: "none", OwnerGeneration: generation + 10,
			ObservedAt: radioTestNow() - 1, ValidUntil: radioTestNow() + 20,
		}
		if f.badStop {
			wrong := f.session
			wrong.Epoch = "9"
			result.Session = &wrong
		} else {
			f.state.Session, f.state.Observation = nil, result.Observation
		}
	default:
		f.t.Fatalf("unexpected primary phase: %s", phase)
	}
	return result, nil
}

func TestPrimaryRadioProducerExactStartStopAndUnknown(t *testing.T) {
	for _, variant := range []string{"disjoint", "same-target", "lost-start", "wrong-stop"} {
		t.Run(variant, func(t *testing.T) {
			ctx := context.Background()
			f := &primaryRadioFixture{t: t,
				state: radioPrimaryState{
					Schema: primaryRadioSchema, Process: strings.Repeat("a", 64), Revision: "7",
					Targets: []radioProducerTarget{
						{radioTarget: radioTarget{ID: "r1", Selectable: true, Availability: "available"}, HomePodID: "1"},
						{radioTarget: radioTarget{ID: "r2", Selectable: true, Availability: "available"}, HomePodID: "2"},
					},
					Observation: &radioNativeObservation{State: "playing", SourceKind: "spotify", TargetID: "r1",
						OwnerGeneration: 1, ObservedAt: radioTestNow() - 1, ValidUntil: radioTestNow() + 20},
				},
				session: radioPrimarySession{Process: attentionUUID(), Operation: attentionUUID(), DeviceID: "2", Incarnation: "3", Epoch: "4", Route: "0"},
			}
			target := "r2"
			if variant == "same-target" {
				target = "r1"
				f.session.DeviceID = "1"
			}
			if variant == "lost-start" {
				f.lose = "start"
			}
			f.badStop = variant == "wrong-stop"
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
			if err = c.enable(ctx); err != nil {
				t.Fatal(err)
			}
			c.mu.Lock()
			applyRadioObservation(&c.state, *f.state.Observation)
			c.mu.Unlock()
			current := c.snapshot()
			q := radioCommand{ActionID: attentionUUID(), Action: "start", StationID: "p3", TargetID: target,
				ExpectedRevision: current.Revision, ExpectedProcessID: current.ProcessID,
				ExpectedGeneration: current.Generation, ExpectedOwnerGeneration: current.OwnerGeneration}
			started, err := c.execute(ctx, q)
			if !reflect.DeepEqual(f.calls, []string{"reserve", "prepare_start", "start"}) {
				t.Fatalf("exact start path %v", f.calls)
			}
			if variant == "lost-start" {
				if err == nil || started.State != "unknown" {
					t.Fatalf("lost start receipt accepted: %+v %v", started, err)
				}
				if _, err := c.execute(ctx, q); err == nil || len(f.calls) != 3 {
					t.Fatal("uncertain start was replayed")
				}
				restarted, err := newPrimaryRadioProducer(f, p.journal.directory)
				if err != nil {
					t.Fatal(err)
				}
				if _, _, err = restarted.Ready(ctx); err == nil || len(f.calls) != 3 {
					t.Fatal("restart adopted or replayed unknown custody")
				}
				old := &producerFixture{}
				if _, err := newRadioProducer(producerNativeFixture{old}, producerSenderFixture{old}, p.journal.directory, true); err == nil {
					t.Fatal("v1 producer adopted a primary-session journal")
				}
				return
			}
			if err != nil || started.State != "playing" || started.TargetID != target || started.StationID != "p3" ||
				!reflect.DeepEqual(started.Actions, []string{"stop"}) {
				t.Fatalf("exact start failed: %+v %v", started, err)
			}
			q = radioCommand{ActionID: attentionUUID(), Action: "stop", ExpectedRevision: started.Revision,
				ExpectedProcessID: started.ProcessID, ExpectedGeneration: started.Generation, ExpectedOwnerGeneration: started.OwnerGeneration}
			stopped, err := c.execute(ctx, q)
			if !reflect.DeepEqual(f.calls, []string{"reserve", "prepare_start", "start", "reserve", "stop", "result"}) {
				t.Fatalf("stop must issue one effect and one passive read: %v", f.calls)
			}
			if variant == "wrong-stop" {
				if err == nil || stopped.State != "unknown" || p.records[p.active].SourceDrained {
					t.Fatalf("mismatched C epoch settled stop: %+v %v", stopped, err)
				}
				return
			}
			if err != nil || stopped.State != "stopped" || p.active != "" {
				t.Fatalf("exact stop failed: %+v %v", stopped, err)
			}
		})
	}
}
