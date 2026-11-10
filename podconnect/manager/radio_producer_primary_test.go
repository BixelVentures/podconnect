package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"slices"
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
	expiredStop  bool
	rejectFirst  string
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
	wantAction := r.Fence.ActionID
	if !producerUUID(wantAction) {
		wantAction = r.Fence.Token
	}
	if r.Primary == nil || r.Fence.Token != q.Handoff || wantAction != q.Action || !producerUUID(q.Action) ||
		r.Request.Target != q.Target || r.Request.Process != q.Process || q.Schema != primaryRadioSchema {
		f.t.Fatalf("effect was not journaled under the exact action: %+v", q)
	}
	if (q.Phase != "reserve" || wantAction != r.Fence.ActionID) && r.Primary.LastCommand == nil {
		f.t.Fatal("effect command missing from the journal")
	}
	if q.Phase == "reserve" && wantAction != r.Fence.ActionID && !q.matches(radioPrimaryReceipt{radioPrimaryCommand: *r.Primary.LastCommand}) {
		f.t.Fatal("journal did not retain the exact mapped reserve command before dispatch")
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
		if f.rejectFirst == phase {
			result.Phase = "rejected"
		}
	case "start":
		if f.rejectFirst == phase {
			result.Phase = "rejected"
			return result, nil
		}
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
		f.state.IsolatedSession = nil
	case "stop":
		if f.expiredStop {
			result.Phase = "replacement_rejected"
			return result, nil
		}
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
	for _, variant := range []string{"station", "move", "no-capability", "stale-incumbent", "rejected-preflight", "lost-preflight", "lost-stop", "expired-stop", "lost-expired-stop"} {
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
			case "expired-stop", "lost-expired-stop":
				f.expiredStop = true
				if variant == "lost-expired-stop" {
					f.lose = "stop"
				}
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
			case "lost-expired-stop":
				if err == nil || next.State != "unknown" || p.active != incumbent {
					t.Fatalf("lost terminal refusal claimed an effect: %+v %v", next, err)
				}
				c.refresh(ctx)
				fresh := c.snapshot()
				if fresh.State != "playing" || fresh.StationID != "p3" || p.active != incumbent ||
					!reflect.DeepEqual(f.calls, append(wantCalls, "result")) || !p.records[c.fence.Token].Terminal {
					t.Fatalf("passive exact refusal did not preserve incumbent: %+v %v", fresh, f.calls)
				}
			case "lost-preflight", "lost-stop":
				if err == nil || next.State != "unknown" || p.active != incumbent || p.records[incumbent].SourceDrained {
					t.Fatalf("lost reply did not retain unknown custody: %+v %v", next, err)
				}
				c.refresh(ctx)
				wantCalls = append(wantCalls, "result")
				if c.snapshot().State != "unknown" {
					t.Fatal("ambiguous replacement adopted a successor without a user action")
				}
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
				if (variant == "rejected-preflight" || variant == "expired-stop") && (!p.records[c.fence.Token].Terminal || p.records[c.fence.Token].Outcome != "rejected") {
					t.Fatal("exact no-effects rejection did not retire candidate")
				}
			}
		})
	}
}

func TestPrimaryRadioProducerExactStartStopAndUnknown(t *testing.T) {
	for _, variant := range []string{"disjoint", "same-target", "podview-task", "lost-start", "wrong-stop", "passive-exact", "passive-wrong-session", "passive-incomplete", "passive-old", "passive-unknown", "rejected-preflight-first", "rejected-decode-first", "lost-rejected-preflight-first", "lost-rejected-decode-first"} {
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
			if variant == "podview-task" {
				q.ActionID = strings.Repeat("a", 32)
			}
			if strings.Contains(variant, "rejected-") {
				lost := strings.HasPrefix(variant, "lost-")
				f.rejectFirst = "start"
				want := []string{"reserve", "prepare_start", "start"}
				if strings.Contains(variant, "preflight") {
					f.rejectFirst, want = "prepare_start", want[:2]
				}
				wantSource := "spotify"
				if lost {
					f.lose, wantSource = f.rejectFirst, "unknown"
				}
				state, err := c.execute(ctx, q)
				if err == nil || state.SourceKind != wantSource || !reflect.DeepEqual(f.calls, want) || p.active != "" {
					t.Fatalf("receiver-free rejection changed Spotify or emitted another operation: %v %v", state, err)
				}
				c.refresh(ctx)
				if lost {
					want = append(want, "result")
				}
				if got := c.snapshot(); got.SourceKind != "spotify" || !reflect.DeepEqual(f.calls, want) {
					t.Fatalf("passive observation lost Spotify or replayed the rejected action: %v %v", got, f.calls)
				}
				current := c.snapshot()
				q.ActionID, q.StationID = attentionUUID(), "p1"
				q.ExpectedRevision, q.ExpectedGeneration = current.Revision, current.Generation
				q.ExpectedOwnerGeneration = current.OwnerGeneration
				f.rejectFirst, f.lose, f.calls = "", "", nil
				state, err = c.execute(ctx, q)
				if err != nil || state.StationID != "p1" || state.SourceKind != "radio" ||
					!reflect.DeepEqual(f.calls, []string{"reserve", "prepare_start", "start"}) {
					t.Fatalf("new explicit selection did not recover: %v %v", state, err)
				}
				return
			}
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
			if strings.HasPrefix(variant, "passive-") {
				testPassivePrimaryRelease(t, ctx, p, c, f, variant)
				return
			}
			q = radioCommand{ActionID: attentionUUID(), Action: "stop", ExpectedRevision: started.Revision,
				ExpectedProcessID: started.ProcessID, ExpectedGeneration: started.Generation, ExpectedOwnerGeneration: started.OwnerGeneration}
			if variant == "podview-task" {
				q.ActionID = strings.Repeat("b", 32)
			}
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

func TestPrimaryRadioRestartsOnlyAfterImpossibleLegacyReserve(t *testing.T) {
	for _, variant := range []string{"legacy-compact", "mapped-wire", "canonical", "mismatched-fence",
		"legacy-hold", "legacy-peer", "retained-custody", "drain-ticket", "rejected-with-custody"} {
		t.Run(variant, func(t *testing.T) {
			ctx := context.Background()
			process := strings.Repeat("a", 64)
			f := &primaryRadioFixture{t: t, state: radioPrimaryState{
				Schema: primaryRadioSchema, Process: process, Revision: "7",
				Targets: []radioProducerTarget{
					{radioTarget: radioTarget{ID: "r0", Selectable: true, Availability: "available"}, HomePodID: "1"},
				},
				Observation: &radioNativeObservation{State: "stopped", SourceKind: "spotify", TargetID: "r0",
					ObservedAt: radioTestNow() - 1, ValidUntil: radioTestNow() + 20},
			}}
			directory, err := filepath.EvalSymlinks(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			journalDir := filepath.Join(directory, "private-journal")
			p, err := newPrimaryRadioProducer(f, journalDir)
			if err != nil {
				t.Fatal(err)
			}
			action, handoff := strings.Repeat("b", 32), attentionUUID()
			if variant == "canonical" {
				action = attentionUUID()
			}
			r := radioProducerRecord{
				Fence: radioFence{Token: handoff, ActionID: action, Generation: 3},
				Request: radioProducerReserve{
					radioProducerIdentity: radioProducerIdentity{
						Process: process, Handoff: handoff, ActionID: action, Generation: 3, Target: "r0",
					},
					ExpectedIntent: radioProducerIntent{Process: process, Revision: "7"}, From: "r0",
				},
				Stage: "native-reserve-attempted", Primary: &radioPrimaryRecord{Device: "1", Station: "p3"},
			}
			if variant == "mapped-wire" {
				command := r.primaryCommand("reserve")
				command.Action = handoff
				r.Primary.LastCommand = &command
			}
			if variant == "mismatched-fence" {
				r.Request.ActionID = strings.Repeat("c", 32)
			}
			switch variant {
			case "legacy-hold":
				r.Native.Token = "retained"
			case "legacy-peer":
				r.Request.Peer.Proof = json.RawMessage(`{"fixture":"retained"}`)
			case "retained-custody":
				r.Request.Retained = &radioProducerPrior{}
			case "drain-ticket":
				r.DrainTicket = json.RawMessage(`{"fixture":"retained"}`)
			case "rejected-with-custody":
				r.Terminal, r.Outcome, r.Stage = true, "rejected", primaryLegacyRejectedStage
				r.Sender.Proof = json.RawMessage(`{"fixture":"retained"}`)
			}
			if err = p.storeLocked(r); err != nil {
				t.Fatal(err)
			}
			p, err = newPrimaryRadioProducer(f, journalDir)
			if err != nil {
				t.Fatal(err)
			}
			_, _, err = p.Ready(ctx)
			wantReady := variant == "legacy-compact"
			if (err == nil) != wantReady || len(f.calls) != 0 {
				t.Fatalf("startup readiness=%t Native command count=%d, want readiness=%t", err == nil, len(f.calls), wantReady)
			}
			_, persisted, err := openRadioProducerJournal(journalDir)
			if err != nil {
				t.Fatal(err)
			}
			row := persisted[handoff]
			if row.Fence != r.Fence || row.Terminal != (wantReady || r.Terminal) || wantReady && row.Outcome != "rejected" {
				t.Fatal("startup did not preserve the exact legacy caller fence and outcome")
			}
			if producerPrimaryHistoryTerminal(persisted, row) != wantReady {
				t.Fatal("only the exact rejected historical record may allow legacy startup")
			}
			if wantReady && (p.primaryPrepare(ctx, "p3", "r0", action, 4) == nil || len(f.calls) != 0) {
				t.Fatal("a preserved rejected action must not gain admission or replay")
			}
		})
	}
}

func testPassivePrimaryRelease(t *testing.T, ctx context.Context, p *radioProducer, c *radioCoordinator, f *primaryRadioFixture, variant string) {
	t.Helper()
	token := p.active
	record := p.records[token]
	session := f.session
	if variant == "passive-old" {
		newer := *f.state.Observation
		newer.ObservedAt = radioTestNow()
		f.state.Observation = &newer
		c.refresh(ctx)
	}
	observed := &radioNativeObservation{State: "playing", SourceKind: "spotify", TargetID: record.Request.Target,
		OwnerGeneration: record.Fence.OwnerGeneration, ObservedAt: radioTestNow(), ValidUntil: radioTestNow() + 20}
	f.state.Session, f.state.Observation, f.state.ReleasedSession = nil, observed, &session
	releaseAction, teardown := attentionUUID(), 42
	f.state.ReleaseActionID, f.state.TeardownCSeq = &releaseAction, &teardown
	restarted, err := newPrimaryRadioProducer(f, p.journal.directory)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := restarted.Ready(ctx); err == nil {
		t.Fatal("restart adopted an unresolved active primary session")
	}
	switch variant {
	case "passive-wrong-session":
		session.Epoch = "99"
	case "passive-incomplete":
		f.state.ReleaseActionID = nil
	case "passive-old":
		observed.ObservedAt = p.primaryObservedAt
	case "passive-unknown":
		observed.State, observed.SourceKind = "unknown", "unknown"
	}
	c.refresh(ctx)
	if variant != "passive-exact" {
		if c.snapshot().State != "unknown" || p.active != token || p.records[token].SourceDrained {
			t.Fatal("invalid passive observation retired the active radio")
		}
		// An unknown presentation must not resurrect the historical START.
		c.refresh(ctx)
		if c.snapshot().State != "unknown" {
			t.Fatal("historical radio START became current again")
		}
		session = f.session
		f.state.ReleaseActionID = &releaseAction
		observed.State, observed.SourceKind = "playing", "spotify"
		observed.ObservedAt = radioTestNow()
		observed.OwnerGeneration++
		c.refresh(ctx)
	}
	if state := c.snapshot(); state.SourceKind != "spotify" || state.State != "playing" || p.active != "" {
		t.Fatalf("exact passive release did not update the coordinator: %+v", state)
	}
	var stored radioProducerRecord
	raw, err := os.ReadFile(filepath.Join(p.journal.directory, producerRecordName(token)))
	if err != nil || json.Unmarshal(raw, &stored) != nil || !stored.SourceDrained ||
		stored.Primary.ReleaseActionID != releaseAction || stored.Primary.TeardownCSeq != 42 {
		t.Fatalf("exact passive release was not retained: %+v %v", stored, err)
	}
	if !reflect.DeepEqual(f.calls, []string{"reserve", "prepare_start", "start"}) {
		t.Fatalf("passive release replayed a command: %v", f.calls)
	}
}

func TestPrimaryRadioIsolationRecoversOnlyExactLiveCustody(t *testing.T) {
	for _, variant := range []string{
		"exact", "no-proof", "wrong-process", "wrong-session", "wrong-owner", "older-revision",
		"older-observation", "missing-capability", "closed-target", "wrong-device", "stop-receipt",
		"restart", "unresolved-stop", "restart-after-adoption", "different-target",
	} {
		t.Run(variant, func(t *testing.T) {
			ctx := context.Background()
			process := strings.Repeat("a", 64)
			target := radioProducerTarget{
				radioTarget: radioTarget{ID: "r1", Selectable: true, Availability: "available"}, HomePodID: "1",
			}
			f := &primaryRadioFixture{t: t,
				state: radioPrimaryState{
					Schema: primaryRadioSchema, Protocol: primaryRadioProtocol, Process: process, Revision: "7",
					Targets: []radioProducerTarget{target}, Missing: []string{},
					Capabilities: []string{primaryReplacementCapability},
					Observation: &radioNativeObservation{
						State: "idle", SourceKind: "none", ObservedAt: radioTestNow() - 1, ValidUntil: radioTestNow() + 20,
					},
				},
				session: radioPrimarySession{
					Process: attentionUUID(), Operation: attentionUUID(), DeviceID: "1",
					Incarnation: "3", Epoch: "4", Route: "0",
				},
			}
			if variant == "different-target" {
				f.state.Targets = append(f.state.Targets, radioProducerTarget{
					radioTarget: radioTarget{ID: "r2", Selectable: true, Availability: "available"}, HomePodID: "2",
				})
			}
			dir, err := filepath.EvalSymlinks(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			p, err := newPrimaryRadioProducer(f, filepath.Join(dir, "private-journal"))
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
			start := command("start", "p3", "r1")
			started, err := c.execute(ctx, start)
			if err != nil || started.State != "playing" {
				t.Fatalf("incumbent did not start: %+v %v", started, err)
			}
			c.refresh(ctx) // Record the latest playing Native revision.
			incumbent, startFence := p.active, c.fence
			if variant == "unresolved-stop" {
				f.lose = "stop"
				if _, err = c.execute(ctx, command("stop", "", "")); err == nil {
					t.Fatal("unresolved STOP unexpectedly succeeded")
				}
				f.expiredStop = true // The result read cannot produce a positive STOP.
			} else {
				f.state.Observation = nil
				c.refresh(ctx)
			}
			if s := c.snapshot(); s.State != "unknown" || len(s.Actions) != 0 {
				t.Fatalf("loss of current source must withdraw all controls: %+v", s)
			}

			isolated := radioPrimaryIsolation{radioPrimarySession: f.session, Reason: primaryIsolationReason}
			f.state.IsolatedSession = &isolated
			f.state.Revision = "9"
			f.state.Session = nil
			f.state.Observation = &radioNativeObservation{
				State: "stopped", SourceKind: "none", TargetID: "r1",
				OwnerGeneration: started.OwnerGeneration, ObservedAt: radioTestNow() - 1, ValidUntil: radioTestNow() + 20,
			}
			switch variant {
			case "no-proof":
				f.state.IsolatedSession = nil
			case "wrong-process":
				f.state.Process = strings.Repeat("b", 64)
			case "wrong-session":
				isolated.Epoch = "5"
			case "wrong-owner":
				f.state.Observation.OwnerGeneration++
			case "older-revision":
				f.state.Revision = "8"
			case "older-observation":
				f.state.Observation.ObservedAt = p.primaryObservedAt - 1
			case "missing-capability":
				f.state.Missing = nil
			case "closed-target":
				f.state.Targets[0].Selectable = false
			case "wrong-device":
				f.state.Targets[0].HomePodID = "2"
			case "stop-receipt":
				cseq, action := 42, attentionUUID()
				f.state.TeardownCSeq, f.state.ReleaseActionID, f.state.ReleasedSession = &cseq, &action, &f.session
			case "restart":
				restarted, err := newPrimaryRadioProducer(f, p.journal.directory)
				if err != nil {
					t.Fatal(err)
				}
				if err = newRadioCoordinator(restarted).enable(ctx); err == nil {
					t.Fatal("fresh Manager implicitly adopted old local isolation")
				}
				return
			}
			f.calls = nil
			c.refresh(ctx)
			for _, call := range f.calls {
				if call != "result" {
					t.Fatalf("passive isolation dispatched an effect: %v", f.calls)
				}
			}
			if variant != "exact" && variant != "restart-after-adoption" && variant != "different-target" {
				if c.snapshot().State != "unknown" || p.active != incumbent || p.records[incumbent].SourceDrained {
					t.Fatalf("invalid proof retired live custody: %+v", c.snapshot())
				}
				return
			}
			retired := p.records[incumbent]
			if s := c.snapshot(); s.State != "stopped" || s.SourceKind != "none" || !slices.Contains(s.Actions, "start") ||
				p.active != "" || !retired.SourceDrained || retired.Primary.Isolation == nil {
				t.Fatalf("exact proof did not restore fresh start: %+v", s)
			}
			if retired.Primary.StopReceipt != nil || retired.Primary.ReleaseActionID != "" || retired.Primary.TeardownCSeq != 0 {
				t.Fatal("local isolation created a positive STOP receipt")
			}
			if _, err = c.result(ctx, started.ProcessID, start.ActionID, startFence.Generation); err == nil {
				t.Fatal("isolation changed the historical action result")
			}
			if _, err = c.execute(ctx, start); err == nil || len(f.calls) != 0 {
				t.Fatal("an old action could replay")
			}
			switch variant {
			case "restart-after-adoption":
				restarted, err := newPrimaryRadioProducer(f, p.journal.directory)
				if err != nil {
					t.Fatal(err)
				}
				f.state.IsolatedSession = nil
				f.state.Revision = "10"
				f.state.Observation = &radioNativeObservation{
					State: "idle", SourceKind: "none", ObservedAt: radioTestNow() - 1, ValidUntil: radioTestNow() + 20,
				}
				if err = newRadioCoordinator(restarted).enable(ctx); err == nil {
					t.Fatal("fresh Manager treated durable local isolation as restart authority")
				}
				return
			case "different-target":
				if _, err := c.execute(ctx, command("start", "p1", "r2")); err == nil || len(f.calls) != 0 {
					t.Fatalf("local isolation authorized a different advertised target: err=%v calls=%v", err, f.calls)
				}
				return
			}
			ready := f.state
			f.state.IsolatedSession = nil
			f.state.Revision = "8"
			if _, err = c.execute(ctx, command("start", "p1", "r1")); err == nil || len(f.calls) != 0 {
				t.Fatal("older Native read authorized a fresh user choice")
			}
			f.state = ready
			// The receiver returns a fresh session for the new, explicit start.
			f.session.Operation = attentionUUID()
			f.session.Epoch = "5"
			next, err := c.execute(ctx, command("start", "p1", "r1"))
			if err != nil || next.State != "playing" || next.StationID != "p1" ||
				!reflect.DeepEqual(f.calls, []string{"reserve", "prepare_start", "start"}) {
				t.Fatalf("new explicit choice did not take only the fresh start path: %+v %v calls=%v", next, err, f.calls)
			}
		})
	}
}
