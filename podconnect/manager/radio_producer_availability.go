package main

// Target availability is a passive projection, not a reservation or permission.
// In particular, Native deliberately closes *new* admission for a held receiver;
// only exact live retained custody and sender ownership can preserve that route.
import (
	"context"
	"errors"
	"strconv"
	"time"
)

var errRadioTargetRefreshUnsupported = errors.New("radio target refresh unsupported")

type radioTargetInventory struct {
	Process, Controller string
	Targets             []radioProducerTarget
}
type radioTargetPairings struct {
	Process, Controller string
	PairProbe           bool
	Bindings            map[string]string // physical receiver -> stored pairing digest
}

func (n *radioProducerNativeClient) radioTargetInventory(ctx context.Context) (radioTargetInventory, error) {
	if _, e := n.Evidence(ctx); e != nil {
		return radioTargetInventory{}, e
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	return radioTargetInventory{n.state.ProcessID, n.state.ControllerID, append([]radioProducerTarget{}, n.state.Targets...)}, nil
}
func (s *radioProducerSenderClient) radioTargetPairings(ctx context.Context) (radioTargetPairings, error) {
	r, e := s.call(ctx, "capabilities", map[string]any{"schema": "podconnect.radio-sender.v1"})
	if e != nil {
		return radioTargetPairings{}, e
	}
	pairs := make(map[string]string, len(r.Targets))
	for _, row := range r.Targets {
		pairs[row.Device] = row.Digest // call already rejects duplicate devices/keys.
	}
	return radioTargetPairings{r.Process, r.ControllerID, r.PairProbeSupported, pairs}, nil
}

// CurrentTargets never calls Ready, launches a process, imports credentials or
// acquires a receiver. Every command still performs its normal fresh preflight.
func (p *radioProducer) CurrentTargets(ctx context.Context) ([]radioTarget, error) {
	n, nativeOK := p.native.(interface {
		radioTargetInventory(context.Context) (radioTargetInventory, error)
	})
	s, senderOK := p.sender.(interface {
		radioTargetPairings(context.Context) (radioTargetPairings, error)
	})
	if !nativeOK || !senderOK {
		return nil, errRadioTargetRefreshUnsupported
	}
	p.mu.Lock()
	expected := p.evidence
	expected.Targets = append([]radioTarget{}, expected.Targets...)
	expected.Bindings = make(map[string]radioProducerTargetBinding, len(p.evidence.Bindings))
	for id, binding := range p.evidence.Bindings {
		expected.Bindings[id] = binding
	}
	retained := make(map[string]radioProducerPrior, len(p.retained))
	for id, prior := range p.retained {
		retained[id] = prior
	}
	p.mu.Unlock()
	closed := closedRadioTargets(expected.Targets)
	// Slow/offline discovery must not consume the source-observation read budget
	// and thereby prevent Stop on an otherwise fresh, owned radio sender.
	read, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	inventory, e := n.radioTargetInventory(read)
	if e != nil || read.Err() != nil {
		if e == nil {
			e = read.Err()
		}
		return closed, e
	}
	pairings, e := s.radioTargetPairings(read)
	if e != nil || read.Err() != nil {
		if e == nil {
			e = read.Err()
		}
		return closed, e
	}
	targets, bindings, e := projectRadioTargets(expected, inventory, pairings, func(target radioProducerTarget) bool {
		prior, ok := retained[target.ID]
		return ok && p.retainedTargetAvailable(read, prior, expected.Bindings[target.ID])
	})
	if e != nil || read.Err() != nil {
		if e == nil {
			e = read.Err()
		}
		return closed, e
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	// No slow reply can cross a replacement producer identity/configuration.
	if p.evidence.NativeProcess != expected.NativeProcess || p.evidence.SenderProcess != expected.SenderProcess || p.evidence.Controller != expected.Controller {
		return closed, errRadioUnknown
	}
	for id, before := range expected.Bindings {
		current := p.evidence.Bindings[id]
		observed, present := bindings[id]
		if current.Device != before.Device || before.Digest != "" && current.Digest != before.Digest || current.Digest != "" && present && observed.Digest != "" && observed.Digest != current.Digest {
			return closed, errRadioUnknown
		}
		// Concurrent passive reads may discover the same previously offline key.
		// Never erase that pin or treat its identical promotion as replacement.
		if current.Digest != "" {
			if !present {
				observed = current
			}
			observed.Digest = current.Digest
			bindings[id] = observed
		}
	}
	p.evidence.Targets = append([]radioTarget{}, targets...)
	for id, binding := range bindings {
		p.evidence.Bindings[id] = binding
	}
	return targets, nil
}

func closedRadioTargets(targets []radioTarget) []radioTarget {
	out := append([]radioTarget{}, targets...)
	for i := range out {
		out[i].Selectable = false
		out[i].Availability = "unavailable"
	}
	return out
}

func projectRadioTargets(expected radioProducerEvidence, native radioTargetInventory, sender radioTargetPairings, retained func(radioProducerTarget) bool) ([]radioTarget, map[string]radioProducerTargetBinding, error) {
	if native.Process != expected.NativeProcess || sender.Process != expected.SenderProcess || !producerControllerID(expected.Controller) || sender.Controller != expected.Controller || !sender.PairProbe || native.Controller != "" && native.Controller != expected.Controller || len(native.Targets) != len(expected.Targets) {
		return nil, nil, errRadioUnknown
	}
	known := map[string]bool{}
	for _, target := range expected.Targets {
		known[target.ID] = true
	}
	seen := map[string]bool{}
	for _, current := range native.Targets {
		binding, ok := expected.Bindings[current.ID]
		if !known[current.ID] || seen[current.ID] || !ok || !producerCounter(binding.Device, false) || binding.Device != current.HomePodID {
			return nil, nil, errRadioUnknown
		}
		seen[current.ID] = true
	}
	keys := map[string]bool{}
	for device, digest := range sender.Bindings {
		if !producerCounter(device, false) || !producerHex(digest, 64) || keys[digest] {
			return nil, nil, errRadioUnknown
		}
		keys[digest] = true
	}
	bindings := make(map[string]radioProducerTargetBinding, len(expected.Bindings))
	out := make([]radioTarget, 0, len(native.Targets))
	for _, current := range native.Targets {
		binding := expected.Bindings[current.ID]
		view := current.radioTarget
		view.Selectable = false
		view.Availability = "unavailable"
		paired := sender.Bindings[binding.Device]
		if !producerHex(paired, 64) || binding.Digest != "" && binding.Digest != paired {
			view.Availability = "unavailable"
			out = append(out, view)
			continue
		}
		freshAdmission := native.Controller == expected.Controller && current.Selectable && current.Availability == "available" && current.ReceiverDigest == paired
		// The retained branch never promotes a newly paired/changed fingerprint.
		owned := !freshAdmission && binding.Digest == paired && retained != nil && retained(current)
		if freshAdmission || owned {
			view.Availability = "available"
			view.Selectable = true
			binding.Digest = paired
		} else if current.Availability == "unavailable" {
			view.Availability = "unavailable"
		}
		bindings[current.ID] = binding
		out = append(out, view)
	}
	return out, bindings, nil
}

func (p *radioProducer) retainedTargetAvailable(ctx context.Context, prior radioProducerPrior, binding radioProducerTargetBinding) bool {
	s, ok := p.sender.(*radioProducerSenderClient)
	if !ok || prior.Target != prior.Native.Target || prior.Target != prior.Sender.Target || !producerHex(binding.Digest, 64) {
		return false
	}
	p.mu.Lock()
	var record radioProducerRecord
	found := false
	for key, candidate := range p.records {
		if p.live[key] && candidate.Terminal && !candidate.Preempted && candidate.Outcome == "committed" && sameRadioAvailabilityHold(candidate.Native, prior.Native) && sameRadioAvailabilityHold(candidate.Sender, prior.Sender) && (!found || candidate.Fence.Generation > record.Fence.Generation) {
			record, found = candidate, true
		}
	}
	p.mu.Unlock()
	if !found {
		return false
	}
	result, e := p.native.Result(ctx, record.Request, prior.Native)
	if e != nil || result.Identity != prior.Native.radioProducerIdentity {
		return false
	}
	if result.Custody != nil {
		_, e = s.ValidateRetainedTarget(ctx, prior.Native, result.Custody)
		return e == nil
	}
	if !result.Terminal || result.Outcome != "committed" {
		return false
	}
	s.mu.Lock()
	owned, known := s.owned[prior.Native.Handoff]
	process := s.process
	s.mu.Unlock()
	if !known || !retainedOwnershipMatches(prior.Native, owned, process) || !sameRadioAvailabilityHold(owned.Sender, prior.Sender) {
		return false
	}
	body, e := senderOperationBody(owned.Sender, owned.Operation)
	if e != nil || body["device_id"] != binding.Device {
		return false
	}
	query := map[string]any{}
	for _, key := range []string{"schema", "device_id", "operation_id", "operation_generation", "radio_process", "radio_owner_epoch", "sender_token"} {
		query[key] = body[key]
	}
	r, e := s.call(ctx, "state", query)
	if e == nil && radioAvailabilitySenderTuple(owned, binding, r) && (r.Phase == "playing" && r.Observed || r.Phase == "drained") {
		return true
	}
	// C state is deliberately current-source-specific. After a real Move A→B,
	// A's exact drain is historical, while B alone is current. The archive cannot
	// establish availability by itself: combine it with fresh Native A custody
	// above and a separately fresh, same-process owned B source below.
	return p.retiredTargetAvailable(ctx, s, record.Request, prior, owned, binding)
}

func radioAvailabilitySenderTuple(owned radioSenderOwnership, binding radioProducerTargetBinding, r radioProducerSenderReply) bool {
	return r.Process == owned.Sender.Process && r.Token == owned.Sender.Token && r.Epoch == strconv.FormatUint(owned.Sender.OwnerGeneration, 10) && r.SourceGeneration == strconv.FormatUint(owned.Sender.SourceGeneration, 10) && r.Digest == binding.Digest && r.Device == binding.Device && r.Operation == owned.Operation.Handoff && r.Generation == strconv.FormatUint(owned.Operation.Generation, 10) && r.Signature == "" && r.Challenge == ""
}

func radioAvailabilityStateQuery(owned radioSenderOwnership) (map[string]any, error) {
	body, e := senderOperationBody(owned.Sender, owned.Operation)
	if e != nil {
		return nil, e
	}
	query := map[string]any{}
	for _, key := range []string{"schema", "device_id", "operation_id", "operation_generation", "radio_process", "radio_owner_epoch", "sender_token"} {
		query[key] = body[key]
	}
	return query, nil
}

// Both reads use s.call in production, retaining its actual supervised-PID,
// process, timestamp and exact operation checks. This function never interprets
// an archived drain as a current source, signature, or new receiver grant.
func radioAvailabilityRetiredPair(ctx context.Context, old, current radioSenderOwnership, oldBinding, currentBinding radioProducerTargetBinding, station string, read func(context.Context, string, map[string]any) (radioProducerSenderReply, error)) bool {
	if old.Phase != "drained" || current.Phase != "playing" || old.Native.Target == current.Native.Target || old.Sender.Process != current.Sender.Process || oldBinding.Device == currentBinding.Device || !producerHex(oldBinding.Digest, 64) || !producerHex(currentBinding.Digest, 64) || oldBinding.Digest == currentBinding.Digest || station == "" {
		return false
	}
	query, e := radioAvailabilityStateQuery(current)
	if e != nil || query["device_id"] != currentBinding.Device {
		return false
	}
	now, e := read(ctx, "state", query)
	if e != nil || !radioAvailabilitySenderTuple(current, currentBinding, now) || now.Phase != "playing" || !now.Observed || !now.Prepared || now.Station != station {
		return false
	}
	// The result endpoint accepts its own five-field read-only schema, rather
	// than the seven-field current state schema. Verify every returned old tuple.
	query = producerSenderBase(oldBinding.Device, old.Operation)
	query["radio_process"] = old.Sender.Process
	drain, e := read(ctx, "result", query)
	return e == nil && radioAvailabilitySenderTuple(old, oldBinding, drain) && drain.Phase == "drained" && !drain.Prepared && !drain.Observed && drain.SourceGeneration == drain.DrainGeneration
}

func (p *radioProducer) retiredTargetAvailable(ctx context.Context, s *radioProducerSenderClient, request radioProducerReserve, prior radioProducerPrior, old radioSenderOwnership, binding radioProducerTargetBinding) bool {
	if old.Phase != "drained" {
		return false
	}
	p.mu.Lock()
	active := p.active
	current, live := p.records[active]
	live = live && p.live[active] && current.Terminal && !current.Preempted && current.Outcome == "committed" && current.Observation.SourceKind == "radio" && current.Observation.State == "playing" && current.Observation.TargetID != prior.Target
	currentBinding := p.evidence.Bindings[current.Observation.TargetID]
	p.mu.Unlock()
	if !live {
		return false
	}
	s.mu.Lock()
	owned, known := s.owned[current.Native.Handoff]
	latest, process := s.latest, s.process
	s.mu.Unlock()
	if !known || latest != owned.Operation || owned.Operation != producerSenderOperation(current) || owned.Native.Target != current.Observation.TargetID || !retainedOwnershipMatches(current.Native, owned, process) || !sameRadioAvailabilityHold(current.Sender, owned.Sender) || !radioAvailabilityRetiredPair(ctx, old, owned, binding, currentBinding, current.Observation.StationID, s.call) {
		return false
	}
	// Re-observe A after the independent C reads. Native validates the original
	// immutable proof on this result path; an unavailable/changed hold closes it.
	fresh, e := p.native.Result(ctx, request, prior.Native)
	if e != nil || fresh.Identity != prior.Native.radioProducerIdentity || fresh.Custody != nil || !fresh.Terminal || fresh.Outcome != "committed" {
		return false
	}
	// A concurrent source transition cannot make this independent projection
	// adopt the old source or erase its uncertainty. Commands still preflight.
	p.mu.Lock()
	after, stillLive := p.records[active]
	retained, stillRetained := p.retained[prior.Target]
	unchanged := p.active == active && stillLive && p.live[active] && after.Terminal && !after.Preempted && after.Outcome == "committed" && after.Observation.SourceKind == "radio" && after.Observation.State == "playing" && after.Observation.TargetID == current.Observation.TargetID && after.Observation.StationID == current.Observation.StationID && after.Observation.OwnerGeneration == current.Observation.OwnerGeneration && sameRadioAvailabilityHold(after.Native, current.Native) && sameRadioAvailabilityHold(after.Sender, current.Sender) && stillRetained && sameRadioAvailabilityHold(retained.Native, prior.Native) && sameRadioAvailabilityHold(retained.Sender, prior.Sender) && p.evidence.Bindings[current.Observation.TargetID] == currentBinding && p.evidence.Bindings[prior.Target] == binding
	p.mu.Unlock()
	s.mu.Lock()
	oldAfter, oldKnown := s.owned[prior.Native.Handoff]
	newAfter, newKnown := s.owned[current.Native.Handoff]
	unchanged = unchanged && oldKnown && newKnown && s.process == process && s.latest == latest && oldAfter.Operation == old.Operation && oldAfter.Phase == "drained" && sameRadioAvailabilityHold(oldAfter.Native, old.Native) && sameRadioAvailabilityHold(oldAfter.Sender, old.Sender) && newAfter.Operation == owned.Operation && newAfter.Phase == "playing" && sameRadioAvailabilityHold(newAfter.Native, owned.Native) && sameRadioAvailabilityHold(newAfter.Sender, owned.Sender)
	s.mu.Unlock()
	return unchanged
}
func sameRadioAvailabilityHold(a, b radioProducerHold) bool {
	return a.radioProducerIdentity == b.radioProducerIdentity && a.SourceGeneration == b.SourceGeneration && a.Token == b.Token && producerJSONEqual(a.Proof, b.Proof)
}

func sameRadioTargets(a, b []radioTarget) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func sameRadioTargetIDs(before, after []radioTarget) bool {
	if len(before) != len(after) {
		return false
	}
	known := map[string]bool{}
	for _, target := range before {
		known[target.ID] = true
	}
	seen := map[string]bool{}
	for _, target := range after {
		if !known[target.ID] || seen[target.ID] || target.Availability != "available" && target.Availability != "unavailable" || target.Selectable && target.Availability != "available" {
			return false
		}
		seen[target.ID] = true
	}
	return true
}
