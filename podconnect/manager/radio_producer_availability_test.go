package main

// Inert protocol/projection fixtures. Full C/PID/cgroup/offline-discovery
// acceptance remains in the separately gated actual two-receiver runner.
import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"
)

func availabilityFixture() (radioProducerEvidence, radioTargetInventory, radioTargetPairings) {
	a, b := strings.Repeat("a", 64), strings.Repeat("b", 64)
	base := radioProducerEvidence{NativeProcess: "native", SenderProcess: "sender", Controller: "0123456789ABCDEF", Targets: []radioTarget{{ID: "A", Name: "Stuen", Default: true}, {ID: "B", Name: "Køkken"}}, Bindings: map[string]radioProducerTargetBinding{"A": {Device: "101", Digest: a}, "B": {Device: "102"}}}
	native := radioTargetInventory{Process: base.NativeProcess, Controller: base.Controller, Targets: []radioProducerTarget{{radioTarget: base.Targets[0], HomePodID: "101"}, {radioTarget: base.Targets[1], HomePodID: "102"}}}
	sender := radioTargetPairings{Process: base.SenderProcess, Controller: base.Controller, PairProbe: true, Bindings: map[string]string{"101": a, "102": b}}
	return base, native, sender
}
func TestRadioAvailabilityPairedOfflineTargetReturnsWithoutEffectsOrLosingHeldRoute(t *testing.T) {
	base, native, sender := availabilityFixture()
	checks := []string{}
	retained := func(target radioProducerTarget) bool { checks = append(checks, target.ID); return target.ID == "A" }
	initial, bindings, err := projectRadioTargets(base, native, sender, retained)
	if err != nil || !initial[0].Selectable || initial[0].Availability != "available" || initial[1].Selectable || initial[1].Availability != "unavailable" || bindings["B"].Digest != "" || !reflect.DeepEqual(checks, []string{"A"}) {
		t.Fatalf("offline metadata borrowed authority or revoked owned route: %#v %#v %v %v", initial, bindings, checks, err)
	}
	native.Targets[1].Selectable = true
	native.Targets[1].Availability = "available"
	native.Targets[1].ReceiverDigest = sender.Bindings["102"]
	checks = nil
	online, bindings, err := projectRadioTargets(base, native, sender, retained)
	if err != nil || !online[0].Selectable || !online[1].Selectable || online[1].Availability != "available" || bindings["B"].Digest != sender.Bindings["102"] || !reflect.DeepEqual(checks, []string{"A"}) {
		t.Fatalf("fresh paired receiver did not recover: %#v %#v %v %v", online, bindings, checks, err)
	}
	// A missing newly available digest is never replaced by the sender's copy.
	native.Targets[1].ReceiverDigest = ""
	withoutProof, _, err := projectRadioTargets(base, native, sender, retained)
	if err != nil || withoutProof[1].Selectable {
		t.Fatal("Native availability without matching receiver proof became a grant", withoutProof, err)
	}
}
func TestRadioAvailabilityAllHeldUsesFreshCustodyNotControllerFallbackForAcquisition(t *testing.T) {
	base, native, sender := availabilityFixture()
	native.Controller = "" // Actual Native intentionally skips new-admission on held A.
	rows, _, err := projectRadioTargets(base, native, sender, func(target radioProducerTarget) bool { return target.ID == "A" })
	if err != nil || !rows[0].Selectable || rows[1].Selectable {
		t.Fatal(rows, err)
	}
	rows, _, err = projectRadioTargets(base, native, sender, func(radioProducerTarget) bool { return false })
	if err != nil || rows[0].Selectable || rows[1].Selectable {
		t.Fatal("old held metadata became fresh authority without current ownership", rows, err)
	}
}
func TestRadioAvailabilityRejectsForeignProcessControllerPhysicalMapAndKey(t *testing.T) {
	for _, change := range []string{"native-process", "sender-process", "controller", "sender-controller", "device", "alias", "duplicate", "probe"} {
		t.Run(change, func(t *testing.T) {
			base, native, sender := availabilityFixture()
			switch change {
			case "native-process":
				native.Process = "replacement"
			case "sender-process":
				sender.Process = "replacement"
			case "controller":
				native.Controller = "FFFFFFFFFFFFFFFF"
			case "sender-controller":
				sender.Controller = "FFFFFFFFFFFFFFFF"
			case "device":
				native.Targets[1].HomePodID = "103"
			case "alias":
				native.Targets[1].ID = "new-alias"
			case "duplicate":
				native.Targets[1].ID = "A"
			case "probe":
				sender.PairProbe = false
			}
			if _, _, err := projectRadioTargets(base, native, sender, func(radioProducerTarget) bool { t.Fatal("foreign tuple reached retained-source read"); return true }); err == nil {
				t.Fatal("foreign target metadata accepted")
			}
		})
	}
	base, native, sender := availabilityFixture()
	base.Bindings["B"] = radioProducerTargetBinding{Device: "102", Digest: strings.Repeat("c", 64)}
	native.Targets[1].Selectable, native.Targets[1].Availability, native.Targets[1].ReceiverDigest = true, "available", sender.Bindings["102"]
	rows, _, err := projectRadioTargets(base, native, sender, nil)
	if err != nil || rows[1].Selectable || rows[1].Availability != "unavailable" {
		t.Fatal("a changed pairing silently replaced a pinned key", rows, err)
	}
}

type availabilityBackendFixture struct {
	*radioFake
	targets []radioTarget
	err     error
	reads   int
}

func (f *availabilityBackendFixture) CurrentTargets(context.Context) ([]radioTarget, error) {
	f.reads++
	return append([]radioTarget{}, f.targets...), f.err
}
func (f *availabilityBackendFixture) Observe(context.Context) (radioNativeObservation, error) {
	return f.observation, nil
}
func TestRadioAvailabilityNativeOfflineKeepsOwnedStopVolumeButNeverStartMove(t *testing.T) {
	volume := 41
	base := &radioFake{observation: radioNativeObservation{State: "playing", SourceKind: "radio", StationID: "p3", TargetID: "r1", OwnerGeneration: 3, VolumePercent: &volume, ObservedAt: time.Now().Unix() - 1, ValidUntil: time.Now().Unix() + 20}}
	backend := &availabilityBackendFixture{radioFake: base, err: errors.New("Native temporarily offline")}
	c := newRadioCoordinator(backend)
	if err := c.enable(context.Background()); err != nil {
		t.Fatal(err)
	}
	c.refresh(context.Background())
	s := c.snapshot()
	if !reflect.DeepEqual(s.Actions, []string{"stop", "volume"}) || s.Targets[0].Selectable || s.Targets[1].Selectable || len(base.calls) != 0 || backend.reads != 1 {
		t.Fatalf("offline discovery affected owned route or sent an effect: %#v %v", s, base.calls)
	}
	q := radioQ(c, "bad-offline-start", "start", "p1", "r1")
	if _, err := c.execute(context.Background(), q); err == nil || len(base.calls) != 0 {
		t.Fatal("offline acquisition was offered/executed", base.calls)
	}
	// A real fresh B projection restores Start/Move; held A remains Stop-only.
	backend.err = nil
	backend.targets = []radioTarget{{ID: "r1", Name: "Stuen", Availability: "unavailable"}, {ID: "r2", Name: "Køkken", Availability: "available", Selectable: true}}
	before := c.snapshot()
	c.refresh(context.Background())
	s = c.snapshot()
	if !reflect.DeepEqual(s.Actions, []string{"start", "stop", "move", "volume"}) || s.Revision <= before.Revision || s.OwnerGeneration != before.OwnerGeneration || len(base.calls) != 0 {
		t.Fatal("target recovery invented a source epoch/effect", s, base.calls)
	}
	// Actual source lease expiry revokes owned transport as before.
	c.mu.Lock()
	c.state.ValidUntil = new(int64)
	*c.state.ValidUntil = time.Now().Unix() - 1
	c.mu.Unlock()
	for _, action := range c.snapshot().Actions {
		if action == "stop" || action == "volume" {
			t.Fatal("expired ownership retained controls")
		}
	}
}
func TestRadioAvailabilityUnknownPendingAndForeignTargetReadsCannotGrantCommands(t *testing.T) {
	base := &radioFake{}
	backend := &availabilityBackendFixture{radioFake: base, targets: []radioTarget{{ID: "foreign", Selectable: true, Availability: "available"}, {ID: "r2", Selectable: true, Availability: "available"}}}
	c := newRadioCoordinator(backend)
	if err := c.enable(context.Background()); err != nil {
		t.Fatal(err)
	}
	c.refresh(context.Background())
	if c.snapshot().Targets[0].Selectable || c.snapshot().Targets[1].Selectable {
		t.Fatal("foreign inventory changed configured receiver set")
	}
	c.mu.Lock()
	c.pending = true
	c.mu.Unlock()
	before := backend.reads
	c.refresh(context.Background())
	if backend.reads != before {
		t.Fatal("pending effect issued discovery reads")
	}
	c.mu.Lock()
	c.pending = false
	c.state.State = "unknown"
	c.mu.Unlock()
	backend.targets = []radioTarget{{ID: "r1", Selectable: true, Availability: "available"}, {ID: "r2", Selectable: true, Availability: "available"}}
	c.refresh(context.Background())
	if len(c.snapshot().Actions) != 0 {
		t.Fatal("availability cleared unknown effect custody")
	}
	if len(base.calls) != 0 {
		t.Fatal("passive availability issued effects", base.calls)
	}
}
func TestRadioAvailabilityInventoryUsesActualClosedNativeAdapterRead(t *testing.T) {
	state := producerNativeStateFixture()
	state.ControllerID = "0123456789ABCDEF"
	state.Targets[0].ReceiverDigest = strings.Repeat("a", 64)
	reads := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reads++
		if r.Method != "GET" || r.URL.Path != "/radio/handoff/state" || r.Header.Get("X-PodConnect-Token") != "secret" {
			t.Error("non-passive/wrong endpoint", r.Method, r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(state)
	}))
	defer server.Close()
	client, err := newRadioProducerNativeClient(server.URL, "secret")
	if err != nil {
		t.Fatal(err)
	}
	inventory, err := client.radioTargetInventory(context.Background())
	if err != nil || reads != 1 || inventory.Process != state.ProcessID || inventory.Controller != state.ControllerID || inventory.Targets[0].ReceiverDigest != state.Targets[0].ReceiverDigest {
		t.Fatal(inventory, reads, err)
	}
}

type availabilityNativePort struct {
	producerNativeFixture
	inventory radioTargetInventory
	err       error
	reads     int
}

func (n *availabilityNativePort) radioTargetInventory(context.Context) (radioTargetInventory, error) {
	n.reads++
	return n.inventory, n.err
}

type availabilitySenderPort struct {
	producerSenderFixture
	pairings radioTargetPairings
	err      error
	reads    int
}

func (s *availabilitySenderPort) radioTargetPairings(context.Context) (radioTargetPairings, error) {
	s.reads++
	return s.pairings, s.err
}
func TestRadioAvailabilityProducerRefreshIsReadOnlyAndPinsRecoveredPairing(t *testing.T) {
	base, native, sender := availabilityFixture()
	native.Targets[0].Availability = "available"
	native.Targets[0].Selectable = true
	native.Targets[0].ReceiverDigest = sender.Bindings["101"]
	f := &producerFixture{}
	n := &availabilityNativePort{producerNativeFixture: producerNativeFixture{f}, inventory: native}
	s := &availabilitySenderPort{producerSenderFixture: producerSenderFixture{f}, pairings: sender}
	p := &radioProducer{native: n, sender: s, evidence: base, retained: map[string]radioProducerPrior{}}
	initial, err := p.CurrentTargets(context.Background())
	if err != nil || !initial[0].Selectable || initial[1].Selectable || len(f.calls) != 0 {
		t.Fatal(initial, err, f.calls)
	}
	n.inventory.Targets[1].Availability = "available"
	n.inventory.Targets[1].Selectable = true
	n.inventory.Targets[1].ReceiverDigest = sender.Bindings["102"]
	online, err := p.CurrentTargets(context.Background())
	if err != nil || !online[1].Selectable || p.evidence.Bindings["B"].Digest != sender.Bindings["102"] || len(f.calls) != 0 {
		t.Fatal(online, err, f.calls)
	}
	n.err = errors.New("offline")
	offline, err := p.CurrentTargets(context.Background())
	if err == nil || offline[0].Selectable || offline[1].Selectable || len(f.calls) != 0 {
		t.Fatal(offline, err, f.calls)
	}
	n.err = nil
	n.inventory.Targets[1].HomePodID = "103"
	foreign, err := p.CurrentTargets(context.Background())
	if err == nil || foreign[0].Selectable || foreign[1].Selectable || len(f.calls) != 0 {
		t.Fatal(foreign, err, f.calls)
	}
	if n.reads != 4 || s.reads != 3 {
		t.Fatal("read schedule changed or extra calls", n.reads, s.reads)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	closed, err := p.CurrentTargets(ctx)
	if err == nil || closed[0].Selectable || closed[1].Selectable {
		t.Fatal("cancelled read admitted metadata", closed, err)
	}
}

type availabilityBlockedPairings struct {
	*availabilitySenderPort
	entered chan struct{}
	release chan struct{}
}

func (s *availabilityBlockedPairings) radioTargetPairings(ctx context.Context) (radioTargetPairings, error) {
	close(s.entered)
	select {
	case <-s.release:
		return s.pairings, nil
	case <-ctx.Done():
		return radioTargetPairings{}, ctx.Err()
	}
}
func TestRadioAvailabilityConcurrentFirstPairPinIsMonotone(t *testing.T) {
	for _, conflicting := range []bool{false, true} {
		t.Run(map[bool]string{false: "same-key", true: "foreign-key"}[conflicting], func(t *testing.T) {
			base, native, pairs := availabilityFixture()
			for i := range native.Targets {
				native.Targets[i].Selectable = true
				native.Targets[i].Availability = "available"
				native.Targets[i].ReceiverDigest = pairs.Bindings[native.Targets[i].HomePodID]
			}
			f := &producerFixture{}
			n := &availabilityNativePort{producerNativeFixture: producerNativeFixture{f}, inventory: native}
			s := &availabilityBlockedPairings{availabilitySenderPort: &availabilitySenderPort{producerSenderFixture: producerSenderFixture{f}, pairings: pairs}, entered: make(chan struct{}), release: make(chan struct{})}
			p := &radioProducer{native: n, sender: s, evidence: base, retained: map[string]radioProducerPrior{}}
			type result struct {
				targets []radioTarget
				err     error
			}
			done := make(chan result, 1)
			go func() { rows, err := p.CurrentTargets(context.Background()); done <- result{rows, err} }()
			select {
			case <-s.entered:
			case <-time.After(time.Second):
				t.Fatal("read did not reach controlled pairing boundary")
			}
			pinned := pairs.Bindings["102"]
			if conflicting {
				pinned = strings.Repeat("c", 64)
			}
			p.mu.Lock()
			p.evidence.Bindings["B"] = radioProducerTargetBinding{Device: "102", Digest: pinned}
			p.mu.Unlock()
			close(s.release)
			got := <-done
			if conflicting {
				if got.err == nil || got.targets[1].Selectable {
					t.Fatal("concurrent foreign pin replaced known key", got)
				}
			} else if got.err != nil || !got.targets[1].Selectable {
				t.Fatal("identical passive pin caused false disconnect", got)
			}
			if p.evidence.Bindings["B"].Digest != pinned || len(f.calls) != 0 {
				t.Fatal("read erased pin or issued effect")
			}
		})
	}
}

// The operation, C source, and C owner counters intentionally differ. An exact
// old archive is useful only alongside a fresh independently owned current B.
func TestRadioAvailabilityRetiredRouteRequiresCurrentOwnedOtherSource(t *testing.T) {
	ownership := func(target, device, digest, token string, owner, source, operation uint64, phase string) (radioSenderOwnership, radioProducerTargetBinding, radioProducerSenderReply) {
		op := radioProducerIdentity{Process: "11111111-1111-4111-8111-111111111111", Handoff: target + "-operation", Generation: operation, Target: target}
		native := radioProducerHold{radioProducerIdentity: radioProducerIdentity{Handoff: target + "-native", Target: target}}
		request := map[string]any{"schema": "podconnect.radio-sender.v1", "device_id": device, "radio_process": op.Process, "radio_owner_epoch": strconv.FormatUint(owner, 10), "sender_token": token}
		proof, _ := json.Marshal(radioProducerSenderEnvelope{Request: request, Native: native})
		hold := radioProducerHold{radioProducerIdentity: radioProducerIdentity{Process: op.Process, Target: target, OwnerGeneration: owner}, SourceGeneration: source, Token: token, Proof: proof}
		owned := radioSenderOwnership{Native: native, Sender: hold, Operation: op, Phase: phase}
		binding := radioProducerTargetBinding{Device: device, Digest: digest}
		reply := radioProducerSenderReply{Process: op.Process, Device: device, Digest: digest, Token: token, Epoch: strconv.FormatUint(owner, 10), SourceGeneration: strconv.FormatUint(source, 10), DrainGeneration: strconv.FormatUint(source, 10), Operation: op.Handoff, Generation: strconv.FormatUint(operation, 10), Phase: phase, Station: "p3", Prepared: phase == "playing", Observed: phase == "playing"}
		return owned, binding, reply
	}
	for _, mode := range []string{"exact", "archive-alone", "current-unknown", "current-unobserved", "current-token", "current-source", "old-process", "old-not-drained", "same-target", "archive-token", "archive-operation", "archive-source", "archive-drain-counter", "archive-not-drained", "archive-prepared", "archive-observed", "archive-signature", "archive-challenge"} {
		t.Run(mode, func(t *testing.T) {
			old, oldBinding, drain := ownership("A", "101", strings.Repeat("a", 64), strings.Repeat("c", 32), 7, 101, 901, "drained")
			current, currentBinding, live := ownership("B", "102", strings.Repeat("b", 64), strings.Repeat("d", 32), 8, 102, 902, "playing")
			switch mode {
			case "current-unknown":
				live.Phase = "unknown"
			case "current-unobserved":
				live.Observed = false
			case "current-token":
				live.Token = strings.Repeat("e", 32)
			case "current-source":
				live.SourceGeneration = "902" // operation domain is not C source domain.
			case "old-process":
				old.Sender.Process = "22222222-2222-4222-8222-222222222222"
			case "old-not-drained":
				old.Phase = "playing"
			case "same-target":
				current.Native.Target = "A"
			case "archive-token":
				drain.Token = strings.Repeat("e", 32)
			case "archive-operation":
				drain.Generation = "101"
			case "archive-source":
				drain.SourceGeneration = "901"
			case "archive-drain-counter":
				drain.DrainGeneration = "901"
			case "archive-not-drained":
				drain.Phase = "unknown"
			case "archive-prepared":
				drain.Prepared = true
			case "archive-observed":
				drain.Observed = true
			case "archive-signature":
				drain.Signature = strings.Repeat("e", 64)
			case "archive-challenge":
				drain.Challenge = strings.Repeat("e", 32)
			}
			beforeOld, beforeCurrent := old, current
			calls := []string{}
			read := func(_ context.Context, action string, query map[string]any) (radioProducerSenderReply, error) {
				calls = append(calls, action)
				if action == "state" {
					if len(query) != 7 || query["device_id"] != "102" || query["operation_generation"] != "902" {
						t.Fatal("current query lost its own tuple", query)
					}
					if mode == "archive-alone" {
						return radioProducerSenderReply{}, errRadioUnknown
					}
					return live, nil
				}
				if action != "result" || len(query) != 5 || query["device_id"] != "101" || query["operation_generation"] != "901" || query["radio_process"] != old.Sender.Process {
					t.Fatal("archive query is not its exact read-only schema", action, query)
				}
				return drain, nil
			}
			available := radioAvailabilityRetiredPair(context.Background(), old, current, oldBinding, currentBinding, "p3", read)
			if available != (mode == "exact") {
				t.Fatal("historical drain borrowed current authority", mode, available, calls)
			}
			if mode == "exact" && !reflect.DeepEqual(calls, []string{"state", "result"}) {
				t.Fatal("unexpected read composition", calls)
			}
			if !reflect.DeepEqual(old, beforeOld) || !reflect.DeepEqual(current, beforeCurrent) {
				t.Fatal("passive projection changed holds/source epochs")
			}
		})
	}
}
