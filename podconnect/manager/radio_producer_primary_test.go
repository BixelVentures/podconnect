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
	t            *testing.T
	p            *radioProducer
	state        radioPrimaryState
	session      radioPrimarySession
	calls        []string
	lose         string
	badStop      bool
	replaceReply string
	waitReplace  bool
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
		if q.ExpectedRevision != f.state.Revision {
			f.t.Fatalf("reserve did not use the captured Native revision: %+v", q)
		}
		if r.Primary.IncumbentSession != nil && (q.Session == nil || *q.Session != f.session ||
			*r.Primary.IncumbentSession != f.session || q.From != f.state.Observation.TargetID) {
			f.t.Fatalf("replacement reserve lost the exact incumbent: %+v", q)
		}
	case "prepare_replace":
		if q.Session == nil || *q.Session != f.session || q.From != f.state.Observation.TargetID ||
			q.Station != r.Primary.Station || q.OwnerGeneration != strconv.FormatUint(generation+10, 10) ||
			r.Stage != "replacement-preflight-attempted" {
			f.t.Fatalf("replacement preflight lost its pinned reservation: %+v", q)
		}
		if f.waitReplace && q.Phase != "result" {
			result.Phase = "pending"
			return result, nil
		}
		result.Phase = "replacement_prepared"
		if f.replaceReply != "" {
			result.Phase = f.replaceReply
		}
	case "prepare_start":
		result.Phase = "prepared"
	case "start":
		result.Phase = "playing"
		session := f.session
		if r.Primary.IncumbentSession != nil {
			session.Operation = attentionUUID()
			session.DeviceID = r.Primary.Device
			session.Epoch = strconv.FormatUint(generation+10, 10)
		}
		f.session = session
		result.Session = &session
		revision, _ := strconv.ParseUint(f.state.Revision, 10, 64)
		f.state.Revision = strconv.FormatUint(revision+1, 10)
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

func TestPrimaryRadioProducerReplacesOnlyAfterReservedPreflight(t *testing.T) {
	for _, variant := range []string{"station", "move", "no-capability", "stale-incumbent", "rejected-preflight", "lost-preflight", "lost-stop"} {
		t.Run(variant, func(t *testing.T) {
			ctx := context.Background()
			f := &primaryRadioFixture{t: t,
				state: radioPrimaryState{
					Schema: primaryRadioSchema, Process: strings.Repeat("a", 64), Revision: "7",
					Capabilities: []string{primaryReplacementCapability},
					Targets: []radioProducerTarget{
						{radioTarget: radioTarget{ID: "r1", Selectable: true, Availability: "available"}, HomePodID: "1"},
						{radioTarget: radioTarget{ID: "r2", Selectable: true, Availability: "available"}, HomePodID: "2"},
					},
					Observation: &radioNativeObservation{State: "idle", SourceKind: "none",
						ObservedAt: radioTestNow() - 1, ValidUntil: radioTestNow() + 20},
				},
				session: radioPrimarySession{Process: attentionUUID(), Operation: attentionUUID(), DeviceID: "1", Incarnation: "3", Epoch: "4", Route: "0"},
			}
			if variant == "no-capability" {
				f.state.Capabilities = nil
			}
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
			command := func(action, station, target string) radioCommand {
				s := c.snapshot()
				return radioCommand{ActionID: attentionUUID(), Action: action, StationID: station, TargetID: target,
					ExpectedRevision: s.Revision, ExpectedProcessID: s.ProcessID,
					ExpectedGeneration: s.Generation, ExpectedOwnerGeneration: s.OwnerGeneration}
			}
			started, err := c.execute(ctx, command("start", "p3", "r1"))
			if err != nil || started.State != "playing" {
				t.Fatalf("incumbent did not start: %+v %v", started, err)
			}
			incumbent, session := p.active, f.session
			f.calls = nil
			q := command("start", "p1", "r1")
			wantTarget, wantStation := "r1", "p1"
			wantCalls := []string{"reserve", "prepare_replace", "stop", "result", "prepare_start", "start"}
			switch variant {
			case "station":
				f.waitReplace = true
				wantCalls = []string{"reserve", "prepare_replace", "result", "stop", "result", "prepare_start", "start"}
			case "move":
				q = command("move", "", "r2")
				wantTarget, wantStation = "r2", "p3"
			case "no-capability":
				wantCalls = nil
			case "stale-incumbent":
				wrong := f.session
				wrong.Epoch = "9"
				f.state.Session = &wrong
				wantCalls = nil
			case "rejected-preflight":
				f.replaceReply = "replacement_rejected"
				wantCalls = []string{"reserve", "prepare_replace"}
			case "lost-preflight":
				f.lose = "prepare_replace"
				wantCalls = []string{"reserve", "prepare_replace"}
			case "lost-stop":
				f.lose = "stop"
				wantCalls = []string{"reserve", "prepare_replace", "stop"}
			}
			next, err := c.execute(ctx, q)
			if !reflect.DeepEqual(f.calls, wantCalls) {
				t.Fatalf("replacement phases %v, want %v", f.calls, wantCalls)
			}
			switch variant {
			case "station", "move":
				if err != nil || next.State != "playing" || next.StationID != wantStation || next.TargetID != wantTarget ||
					p.active == incumbent || !p.records[incumbent].SourceDrained || f.session == session {
					t.Fatalf("successor did not replace exact incumbent: %+v %v", next, err)
				}
			case "lost-preflight", "lost-stop":
				if err == nil || next.State != "unknown" || p.active != incumbent || p.records[incumbent].SourceDrained {
					t.Fatalf("lost reply did not retain unknown custody: %+v %v", next, err)
				}
				c.refresh(ctx)
				if _, err = c.execute(ctx, q); err == nil || !reflect.DeepEqual(f.calls, wantCalls) {
					t.Fatalf("unknown replacement replayed: %v", f.calls)
				}
				if _, err = c.execute(ctx, command("start", "p1", "r1")); err == nil || !reflect.DeepEqual(f.calls, wantCalls) {
					t.Fatalf("fresh action bypassed unknown replacement: %v", f.calls)
				}
			default:
				if err == nil || next.State != "playing" || next.StationID != "p3" || next.TargetID != "r1" ||
					p.active != incumbent || p.records[incumbent].SourceDrained {
					t.Fatalf("denied replacement changed incumbent: %+v %v", next, err)
				}
				if variant == "rejected-preflight" && (!p.records[c.fence.Token].Terminal || p.records[c.fence.Token].Outcome != "rejected") {
					t.Fatal("exact no-effects rejection did not retire candidate")
				}
			}
		})
	}
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
