package main

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strconv"
	"time"
)

const primaryRadioSchema = "podconnect.native_radio_primary.v1"
const primaryReplacementCapability = "replacement_decoder_preflight"
const primaryIsolationReason = "rtsp_control_lost"

// This is a primary C session identity. It cannot stand in for a v1 sender
// pairing digest or be reconstructed from the configured receiver ID.
type radioPrimarySession struct {
	Process     string `json:"process"`
	Operation   string `json:"operation"`
	DeviceID    string `json:"device_id"`
	Incarnation string `json:"incarnation"`
	Epoch       string `json:"session_epoch"`
	Route       string `json:"route_revision"`
}

func (s radioPrimarySession) valid() bool {
	return producerUUID(s.Process) && producerUUID(s.Operation) && producerCounter(s.DeviceID, false) &&
		producerCounter(s.Incarnation, false) && producerCounter(s.Epoch, false) && producerCounter(s.Route, true)
}

type radioPrimaryColdTarget struct {
	Process     string `json:"process"`
	DeviceID    string `json:"device_id"`
	Incarnation string `json:"incarnation"`
	Epoch       string `json:"session_epoch"`
	Route       string `json:"route_revision"`
}

type radioPrimaryColdDetachment struct {
	PriorNativeProcess string                 `json:"prior_native_process_id"`
	PriorHandoff       string                 `json:"prior_handoff_id"`
	PriorOwner         string                 `json:"prior_owner_generation"`
	Target             string                 `json:"target_id"`
	PriorSession       radioPrimarySession    `json:"prior_session"`
	CurrentTarget      radioPrimaryColdTarget `json:"current_target"`
}

func (d radioPrimaryColdDetachment) valid() bool {
	t := d.CurrentTarget
	return producerHex(d.PriorNativeProcess, 64) && producerUUID(d.PriorHandoff) &&
		producerCounter(d.PriorOwner, false) && d.Target != "" && d.PriorSession.valid() &&
		producerUUID(t.Process) && t.Process != d.PriorSession.Process && t.DeviceID == d.PriorSession.DeviceID &&
		producerCounter(t.Incarnation, false) && producerCounter(t.Epoch, true) && producerCounter(t.Route, true)
}

type radioPrimaryIsolation struct {
	radioPrimarySession
	Reason string `json:"local_isolation"`
}

type radioPrimaryIsolationBinding struct {
	session         radioPrimaryIsolation
	target          string
	ownerGeneration uint64
	revision        uint64
}

type radioPrimaryCommand struct {
	Schema           string               `json:"schema"`
	Phase            string               `json:"phase"`
	ForPhase         string               `json:"for_phase,omitempty"`
	Process          string               `json:"process_id"`
	Handoff          string               `json:"handoff_id"`
	Generation       string               `json:"generation"`
	Action           string               `json:"action_id"`
	Target           string               `json:"target_id"`
	ExpectedRevision string               `json:"expected_native_revision"`
	OwnerGeneration  string               `json:"owner_generation,omitempty"`
	From             string               `json:"from_target_id,omitempty"`
	Station          string               `json:"station_id,omitempty"`
	Session          *radioPrimarySession `json:"target_session,omitempty"`
}

type radioPrimaryReceipt struct {
	radioPrimaryCommand
	TeardownCSeq int                     `json:"teardown_cseq,omitempty"`
	Observation  *radioNativeObservation `json:"observation,omitempty"`
}

type radioPrimaryState struct {
	ColdDetachment  *radioPrimaryColdDetachment `json:"cold_detachment,omitempty"`
	Protocol        string                      `json:"protocol,omitempty"`
	Schema          string                      `json:"schema"`
	Process         string                      `json:"process_id"`
	Revision        string                      `json:"native_revision"`
	Targets         []radioProducerTarget       `json:"configured_targets"`
	Missing         []string                    `json:"missing_proofs"`
	Capabilities    []string                    `json:"capabilities,omitempty"`
	Observation     *radioNativeObservation     `json:"observation,omitempty"`
	Session         *radioPrimarySession        `json:"target_session,omitempty"`
	IsolatedSession *radioPrimaryIsolation      `json:"isolated_session,omitempty"`
	ReleasedSession *radioPrimarySession        `json:"released_session,omitempty"`
	ReleaseActionID *string                     `json:"release_action_id,omitempty"`
	TeardownCSeq    *int                        `json:"teardown_cseq,omitempty"`
}

type radioPrimaryPort interface {
	PrimaryState(context.Context) (radioPrimaryState, error)
	PrimaryCommand(context.Context, radioPrimaryCommand) (radioPrimaryReceipt, error)
}

type radioPrimaryBackend struct {
	radioPrimaryPort
	replacement bool // Negotiated by Ready and protected by radioProducer.mu.
}

func (n *radioProducerNativeClient) primaryProtocolState(ctx context.Context) (radioPrimaryState, error) {
	var state radioPrimaryState
	err := n.http.call(ctx, "/radio/primary", nil, &state)
	return state, err
}

func (n *radioProducerNativeClient) PrimaryState(ctx context.Context) (radioPrimaryState, error) {
	state, err := n.primaryProtocolState(ctx)
	if err != nil {
		return state, err
	}
	if state.Protocol != primaryRadioProtocol {
		return radioPrimaryState{}, errRadioUnavailable
	}
	return state, nil
}

func (n *radioProducerNativeClient) PrimaryCommand(ctx context.Context, q radioPrimaryCommand) (radioPrimaryReceipt, error) {
	var result radioPrimaryReceipt
	err := n.http.call(ctx, "/radio/primary", q, &result)
	return result, err
}

type radioPrimaryRecord struct {
	ColdDetachment   *radioPrimaryColdDetachment `json:"cold_detachment,omitempty"`
	Device           string                      `json:"device_id"`
	Station          string                      `json:"station_id,omitempty"`
	Incumbent        string                      `json:"incumbent,omitempty"`
	IncumbentSession *radioPrimarySession        `json:"incumbent_session,omitempty"`
	Session          *radioPrimarySession        `json:"target_session,omitempty"`
	LastCommand      *radioPrimaryCommand        `json:"last_command,omitempty"`
	LastReceipt      *radioPrimaryReceipt        `json:"last_receipt,omitempty"`
	Isolation        *radioPrimaryIsolation      `json:"local_isolation,omitempty"`
	StopReceipt      *radioPrimaryReceipt        `json:"positive_stop_receipt,omitempty"`
	ReleaseActionID  string                      `json:"release_action_id,omitempty"`
	TeardownCSeq     int                         `json:"teardown_cseq,omitempty"`
}

// Bootstrap continues to construct the v1 producer. The primary port is only
// selected when explicitly provided by the owner integration.
func newPrimaryRadioProducer(port radioPrimaryPort, directory string) (*radioProducer, error) {
	if port == nil {
		return nil, errRadioUnavailable
	}
	journal, records, err := openRadioProducerJournal(directory)
	if err != nil {
		return nil, err
	}
	return &radioProducer{primary: &radioPrimaryBackend{radioPrimaryPort: port}, journal: journal, records: records, live: make(map[string]bool)}, nil
}

func (p *radioProducer) primaryState(ctx context.Context) (radioPrimaryState, error) {
	state, err := p.primary.PrimaryState(ctx)
	if err != nil || ctx.Err() != nil || state.Schema != primaryRadioSchema || !producerHex(state.Process, 64) || !producerCounter(state.Revision, true) {
		return radioPrimaryState{}, errRadioUnavailable
	}
	if state.ColdDetachment != nil && (state.Protocol != primaryRadioProtocol || !state.ColdDetachment.valid()) {
		return radioPrimaryState{}, errRadioUnknown
	}
	if state.IsolatedSession != nil && (state.Protocol != primaryRadioProtocol || !state.IsolatedSession.valid() ||
		state.IsolatedSession.Reason != primaryIsolationReason || state.Session != nil ||
		state.ReleasedSession != nil || state.ReleaseActionID != nil || state.TeardownCSeq != nil ||
		state.Observation == nil || !validRadioObservation(*state.Observation) ||
		state.Observation.SourceKind != "none" || state.Observation.State != "stopped" ||
		state.Observation.TargetID == "" || state.Observation.StationID != "" ||
		state.Observation.VolumePercent != nil) {
		return radioPrimaryState{}, errRadioUnknown
	}
	if state.ReleasedSession != nil || state.ReleaseActionID != nil || state.TeardownCSeq != nil {
		if state.ReleasedSession == nil || !state.ReleasedSession.valid() ||
			state.ReleaseActionID == nil || !producerUUID(*state.ReleaseActionID) || state.TeardownCSeq == nil ||
			*state.TeardownCSeq <= 0 || state.Session != nil || state.Observation == nil ||
			!validRadioObservation(*state.Observation) || !primaryKnownNonradio(*state.Observation) {
			return radioPrimaryState{}, errRadioUnknown
		}
	}
	return state, nil
}

func primaryKnownNonradio(o radioNativeObservation) bool {
	return o.SourceKind == "spotify" && (o.State == "playing" || o.State == "stopped") ||
		o.SourceKind == "none" && (o.State == "stopped" || o.State == "idle")
}

func (p *radioProducer) primaryReady(ctx context.Context) ([]radioTarget, []string, error) {
	state, err := p.primaryState(ctx)
	if err != nil {
		return nil, nil, err
	}
	if len(state.Missing) != 0 {
		return nil, state.Missing, errRadioUnavailable
	}
	backend := p.primary.(*radioPrimaryBackend)
	native, configured := backend.radioPrimaryPort.(*radioProducerNativeClient)
	if configured && native.primaryBootstrapProcess != "" &&
		(state.Process != native.primaryBootstrapProcess || !primaryBootstrapStateReady(state)) {
		return nil, nil, errRadioUnavailable
	}
	if state.IsolatedSession != nil || state.Observation == nil || !validRadioObservation(*state.Observation) {
		return nil, nil, errRadioUnavailable
	}
	targets := make([]radioTarget, 0, len(state.Targets))
	seen := make(map[string]bool)
	for _, target := range state.Targets {
		if target.ID == "" || seen[target.ID] || !producerCounter(target.HomePodID, false) || target.ReceiverDigest != "" {
			return nil, nil, errRadioUnavailable
		}
		seen[target.ID] = true
		targets = append(targets, target.radioTarget)
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.evidence.NativeProcess != "" && p.evidence.NativeProcess != state.Process {
		return nil, nil, errRadioUnknown
	}
	var coldRecord *radioProducerRecord
	for token, record := range p.records {
		if record.Primary == nil && producerLegacyHistoryTerminal(p.records, record) {
			continue
		}
		if record.Stage == primaryLegacyRejectedStage {
			if !primaryLegacyInvalidReserve(record) {
				return nil, []string{"durable_unknown_effect_custody"}, errRadioUnavailable
			}
			continue
		}
		if primaryLegacyInvalidReserve(record) {
			record.Terminal, record.Outcome, record.Stage = true, "rejected", primaryLegacyRejectedStage
			if err := p.storeLocked(record); err != nil {
				return nil, nil, err
			}
			continue
		}
		// A live local isolation observation is not a durable release receipt.
		if record.Primary != nil && record.Primary.Isolation != nil && !p.live[token] {
			return nil, []string{"durable_unknown_effect_custody"}, errRadioUnavailable
		}
		if record.Primary == nil || !record.Terminal {
			return nil, []string{"durable_unknown_effect_custody"}, errRadioUnavailable
		}
		if record.Primary.ColdDetachment != nil {
			if p.live[token] || !record.Primary.ColdDetachment.matchesRecord(record) {
				return nil, []string{"durable_unknown_effect_custody"}, errRadioUnavailable
			}
			continue
		}
		if record.Observation.SourceKind == "radio" && record.Observation.State == "playing" && !record.SourceDrained && !p.live[token] {
			if coldRecord != nil || p.active != "" || !primaryColdDetachmentMatches(state, record) {
				return nil, []string{"durable_unknown_effect_custody"}, errRadioUnavailable
			}
			candidate := record
			coldRecord = &candidate
		}
	}
	if state.Observation.SourceKind == "radio" {
		active := p.records[p.active]
		if !primaryOwnsPlaying(state, active) {
			return nil, []string{"durable_unknown_effect_custody"}, errRadioUnavailable
		}
	} else if state.Session != nil {
		return nil, nil, errRadioUnknown
	} else if p.active != "" {
		if err := p.retireObservedPrimaryLocked(state); err != nil {
			return nil, nil, err
		}
	}
	// Close only the prospective fence, after checking the entire journal.
	// The original result is historical and never becomes live in this process.
	if coldRecord != nil {
		primary := *coldRecord.Primary
		primary.ColdDetachment = state.ColdDetachment
		coldRecord.Primary = &primary
		if err := p.storeLocked(*coldRecord); err != nil {
			return nil, nil, err
		}
	}
	p.evidence.NativeProcess = state.Process
	p.evidence.Targets = append([]radioTarget(nil), targets...)
	backend.replacement = slices.Contains(state.Capabilities, primaryReplacementCapability)
	return targets, nil, nil
}

func (d radioPrimaryColdDetachment) matchesRecord(record radioProducerRecord) bool {
	return d.valid() && record.Primary != nil && record.Terminal && !record.Preempted && !record.SourceDrained &&
		record.Outcome == "committed" && record.Stage == "terminal" &&
		record.Request.Process == d.PriorNativeProcess && record.Request.Target == d.Target &&
		record.Fence.Token == d.PriorHandoff && strconv.FormatUint(record.Fence.OwnerGeneration, 10) == d.PriorOwner &&
		record.Primary.Session != nil && *record.Primary.Session == d.PriorSession && record.Primary.Device == d.PriorSession.DeviceID &&
		record.Primary.StopReceipt == nil && record.Primary.ReleaseActionID == "" && record.Primary.TeardownCSeq == 0 &&
		record.Observation.SourceKind == "radio" && record.Observation.State == "playing" &&
		record.Observation.TargetID == d.Target && record.Observation.OwnerGeneration == record.Fence.OwnerGeneration
}

func primaryColdDetachmentMatches(state radioPrimaryState, record radioProducerRecord) bool {
	d, o := state.ColdDetachment, state.Observation
	if d == nil || o == nil || o.SourceKind != "none" || o.State != "idle" || o.StationID != "" ||
		(o.TargetID != "" && o.TargetID != d.Target) || o.VolumePercent != nil ||
		state.Session != nil || state.IsolatedSession != nil || state.ReleasedSession != nil ||
		state.ReleaseActionID != nil || state.TeardownCSeq != nil || state.Process == d.PriorNativeProcess ||
		!d.matchesRecord(record) {
		return false
	}
	matched := false
	for _, target := range state.Targets {
		if target.HomePodID == d.CurrentTarget.DeviceID {
			if matched || target.ID != d.Target || !target.Selectable || target.Availability != "available" {
				return false
			}
			matched = true
		}
	}
	return matched
}

const primaryLegacyRejectedStage = "legacy-reserve-invalid-action"

// The old adapter sent the compact caller ID verbatim. The primary Native
// HTTP owner has always rejected this shape before reaching its actor. The
// corrected adapter saves the actual reserve command before sending it, so a
// missing reply to a valid command can never match this historical case.
func primaryLegacyInvalidReserve(r radioProducerRecord) bool {
	primary, fence, request := r.Primary, r.Fence, r.Request
	attempt := r.Stage == "native-reserve-attempted" && !r.Terminal && r.Outcome == ""
	rejected := r.Stage == primaryLegacyRejectedStage && r.Terminal && r.Outcome == "rejected"
	if (!attempt && !rejected) || r.Preempted || r.SourceDrained || r.Observation != (radioNativeObservation{}) ||
		primary == nil || *primary != (radioPrimaryRecord{Device: primary.Device, Station: primary.Station}) ||
		!producerCounter(primary.Device, false) || radioStreamURLs[primary.Station] == "" ||
		!producerUUID(fence.Token) || !producerHex(fence.ActionID, 32) || fence.Generation == 0 ||
		fence.OwnerGeneration != 0 || request.OwnerGeneration != 0 || request.Handoff != fence.Token ||
		request.ActionID != fence.ActionID || request.Generation != fence.Generation || request.Target == "" ||
		!producerHex(request.Process, 64) || request.ExpectedIntent.Process != request.Process ||
		!producerCounter(request.ExpectedIntent.Revision, true) {
		return false
	}
	if r.Controller != "" || r.PreemptAction != "" || request.Prior != nil || request.Retained != nil {
		return false
	}
	for _, hold := range [...]radioProducerHold{r.Native, r.Sender} {
		if hold.radioProducerIdentity != (radioProducerIdentity{}) || hold.SourceGeneration != 0 || hold.Token != "" {
			return false
		}
	}
	peer := request.Peer
	if peer.Operation != (radioProducerIdentity{}) || peer.Process != "" || peer.OwnerGeneration != 0 ||
		peer.Target != "" || peer.ReceiverDigest != "" || peer.SourceGeneration != 0 ||
		peer.CandidateID != "" || peer.TargetAuth != nil {
		return false
	}
	for _, proof := range [...]json.RawMessage{r.Native.Proof, r.Sender.Proof, peer.Proof, r.NativeRelease,
		r.NativeRejection, r.CancellationReceipt, r.PreemptChallenge, r.DrainTicket} {
		if len(proof) != 0 && string(proof) != "null" {
			return false
		}
	}
	return true
}

func primaryOwnsPlaying(state radioPrimaryState, incumbent radioProducerRecord) bool {
	return incumbent.Primary != nil && incumbent.Primary.Session != nil && incumbent.Primary.Session.valid() &&
		state.Session != nil && *state.Session == *incumbent.Primary.Session &&
		state.Observation != nil && validRadioObservation(*state.Observation) &&
		state.Observation.State == "playing" && state.Observation.SourceKind == "radio" &&
		state.Observation.OwnerGeneration == incumbent.Fence.OwnerGeneration &&
		state.Observation.TargetID == incumbent.Request.Target && state.Observation.StationID == incumbent.Primary.Station
}

func (p *radioProducer) primaryPrepare(ctx context.Context, station, target, action string, generation uint64) error {
	if _, ok := radioStreamURLs[station]; !ok || action == "" || generation == 0 {
		return errRadioUnavailable
	}
	state, err := p.primaryState(ctx)
	if err != nil {
		return err
	}
	var device string
	for _, entry := range state.Targets {
		if entry.ID == target && entry.Selectable && entry.Availability == "available" && entry.ReceiverDigest == "" && producerCounter(entry.HomePodID, false) {
			if device != "" {
				return errRadioUnavailable
			}
			device = entry.HomePodID
		}
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if device == "" || state.Process != p.evidence.NativeProcess || len(state.Missing) != 0 {
		return errRadioUnavailable
	}
	if !p.primaryIsolationReadCurrentLocked(state) {
		return errRadioUnknown
	}
	if state.IsolatedSession != nil && state.Observation.TargetID != target {
		return errRadioUnavailable
	}
	candidate := &radioPrimaryRecord{Device: device, Station: station}
	if p.active != "" {
		incumbent := p.records[p.active]
		if !p.primary.(*radioPrimaryBackend).replacement || !slices.Contains(state.Capabilities, primaryReplacementCapability) {
			return errRadioUnavailable
		}
		if !p.live[p.active] || !incumbent.Terminal || incumbent.SourceDrained ||
			!primaryOwnsPlaying(state, incumbent) {
			return errRadioUnknown
		}
		candidate.Incumbent = p.active
		session := *state.Session
		candidate.IncumbentSession = &session
	} else if state.Session != nil || state.Observation != nil && state.Observation.SourceKind == "radio" {
		return errRadioUnknown
	}
	for _, record := range p.records {
		if !record.Terminal || record.Fence.ActionID == action {
			return errRadioUnknown
		}
	}
	id := attentionUUID()
	r := radioProducerRecord{
		Fence: radioFence{Token: id, ActionID: action, Generation: generation},
		Request: radioProducerReserve{
			radioProducerIdentity: radioProducerIdentity{Process: state.Process, Handoff: id, ActionID: action, Generation: generation, Target: target},
			ExpectedIntent:        radioProducerIntent{Process: state.Process, Revision: state.Revision},
		},
		Stage: "preflight-prepared", Primary: candidate,
	}
	if err := p.storeLocked(r); err != nil {
		return err
	}
	p.live[id] = true
	return nil
}

func (p *radioProducer) primaryReserve(ctx context.Context, from, to, action string, generation uint64) (radioFence, error) {
	var intent radioPrimaryState
	if to == "" {
		var err error
		intent, err = p.primaryState(ctx)
		if err != nil {
			return radioFence{}, err
		}
	}
	p.mu.Lock()
	if action == "" || generation == 0 || p.evidence.NativeProcess == "" {
		p.mu.Unlock()
		return radioFence{}, errRadioUnavailable
	}
	var r radioProducerRecord
	for _, old := range p.records {
		if old.Primary != nil && old.Stage == "preflight-prepared" && old.Fence.ActionID == action &&
			old.Fence.Generation == generation && old.Request.Target == to && p.live[old.Fence.Token] {
			r = old
			continue
		}
		if !old.Terminal || old.Fence.ActionID == action {
			p.mu.Unlock()
			return radioFence{}, errRadioUnknown
		}
	}
	incumbent := p.records[p.active]
	if (p.active != "" || to == "") && (from == "" || incumbent.Primary == nil ||
		incumbent.Primary.Session == nil || incumbent.Request.Target != from) {
		p.mu.Unlock()
		return radioFence{}, errRadioUnknown
	}
	if r.Primary == nil {
		if to != "" || from == "" || intent.Process != p.evidence.NativeProcess {
			p.mu.Unlock()
			return radioFence{}, errRadioUnavailable
		}
		id := attentionUUID()
		r = radioProducerRecord{
			Fence: radioFence{Token: id, ActionID: action, Generation: generation},
			Request: radioProducerReserve{
				radioProducerIdentity: radioProducerIdentity{Process: intent.Process, Handoff: id, ActionID: action, Generation: generation, Target: from},
				ExpectedIntent:        radioProducerIntent{Process: intent.Process, Revision: intent.Revision},
			},
			Primary: &radioPrimaryRecord{Device: incumbent.Primary.Device},
		}
	}
	state := *r.Primary
	if to != "" && (state.Incumbent != p.active || p.active != "" &&
		(state.IncumbentSession == nil || *state.IncumbentSession != *incumbent.Primary.Session ||
			incumbent.SourceDrained || !p.primary.(*radioPrimaryBackend).replacement)) {
		p.mu.Unlock()
		return r.Fence, errRadioUnknown
	}
	if p.active != "" {
		state.Incumbent = incumbent.Fence.Token
	}
	r.Primary = &state
	r.Request.From = from
	r.Stage = "native-reserve-attempted"
	q := r.primaryCommand("reserve")
	q.Session = state.IncumbentSession
	state.LastCommand = &q
	if err := p.storeLocked(r); err != nil {
		p.mu.Unlock()
		return r.Fence, err
	}
	p.live[r.Fence.Token] = true
	p.mu.Unlock()

	if ctx.Err() != nil {
		return r.Fence, errRadioUnknown
	}
	receipt, err := p.primary.PrimaryCommand(ctx, q)
	if err != nil || ctx.Err() != nil || !q.matches(receipt) || receipt.Phase != "reserved" ||
		!producerCounter(receipt.OwnerGeneration, false) || (receipt.Session == nil) != (q.Session == nil) ||
		receipt.Observation != nil || receipt.TeardownCSeq != 0 {
		return r.Fence, errRadioUnknown
	}
	owner, _ := strconv.ParseUint(receipt.OwnerGeneration, 10, 64)
	if state.IncumbentSession != nil && owner <= incumbent.Fence.OwnerGeneration {
		return r.Fence, errRadioUnknown
	}
	p.mu.Lock()
	current := p.records[r.Fence.Token]
	if current.Fence != r.Fence || current.Stage != r.Stage || current.Preempted || p.evidence.NativeProcess != q.Process {
		p.mu.Unlock()
		return r.Fence, errRadioUnknown
	}
	current.Fence.OwnerGeneration = owner
	current.Stage = "reserved"
	err = p.storeLocked(current)
	p.mu.Unlock()
	if err == nil && state.IncumbentSession != nil {
		err = p.primaryPrepareReplacement(ctx, current.Fence)
	}
	return current.Fence, err
}

func (r radioProducerRecord) primaryWireAction() string {
	if producerUUID(r.Fence.ActionID) {
		return r.Fence.ActionID
	}
	// As in v1, the per-operation Handoff UUID is the Native wire ID for an
	// opaque caller ID. The public action and its journal fence stay unchanged.
	return r.Fence.Token
}

func (r radioProducerRecord) primaryCommand(phase string) radioPrimaryCommand {
	q := radioPrimaryCommand{
		Schema: primaryRadioSchema, Phase: phase, Process: r.Request.Process, Handoff: r.Fence.Token,
		Action: r.primaryWireAction(), Generation: strconv.FormatUint(r.Fence.Generation, 10), Target: r.Request.Target,
		ExpectedRevision: r.Request.ExpectedIntent.Revision, From: r.Request.From,
	}
	if r.Fence.OwnerGeneration != 0 {
		q.OwnerGeneration = strconv.FormatUint(r.Fence.OwnerGeneration, 10)
	}
	if phase == "start" || phase == "prepare_start" || phase == "prepare_replace" {
		q.Station = r.Primary.Station
	}
	return q
}

// Reserve returns to the coordinator only after the destination decoder has
// been proved under the same fence, before the first possible receiver effect.
func (p *radioProducer) primaryPrepareReplacement(ctx context.Context, f radioFence) error {
	p.mu.Lock()
	r := p.records[f.Token]
	if r.Fence != f || r.Primary == nil || r.Primary.IncumbentSession == nil ||
		r.Stage != "reserved" || r.Preempted || p.active != r.Primary.Incumbent ||
		p.evidence.NativeProcess != r.Request.Process {
		p.mu.Unlock()
		return errRadioUnknown
	}
	q := r.primaryCommand("prepare_replace")
	q.Session = r.Primary.IncumbentSession
	state := *r.Primary
	state.LastCommand = &q
	r.Primary = &state
	r.Stage = "replacement-preflight-attempted"
	err := p.storeLocked(r)
	p.mu.Unlock()
	if err != nil {
		return err
	}
	if ctx.Err() != nil {
		return errRadioUnknown
	}
	receipt, err := p.primary.PrimaryCommand(ctx, q)
	if err != nil {
		return errRadioUnknown
	}
	receipt, err = p.primaryWait(ctx, q, receipt)
	if err != nil || receipt.Observation != nil || receipt.TeardownCSeq != 0 {
		return errRadioUnknown
	}
	if receipt.Phase == "replacement_prepared" {
		return p.primaryRecordEffect(r, "reserved", receipt)
	}
	if receipt.Phase != "replacement_rejected" {
		return errRadioUnknown
	}
	return p.primaryRejectNoEffects(r, receipt)
}

// Only an exact terminal owner receipt can retire the candidate before any
// receiver STOP. A timeout or malformed reply still holds the operation.
func (p *radioProducer) primaryRejectNoEffects(r radioProducerRecord, receipt radioPrimaryReceipt) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	current := p.records[r.Fence.Token]
	if current.Fence != r.Fence || current.Stage != r.Stage || current.Preempted || current.Terminal || current.Primary == nil ||
		(current.Primary.Incumbent != "" && current.Primary.IncumbentSession == nil) || p.active != current.Primary.Incumbent || p.evidence.NativeProcess != receipt.Process {
		return errRadioUnknown
	}
	state := *current.Primary
	state.LastReceipt = &receipt
	current.Primary = &state
	current.Terminal, current.Outcome, current.Stage = true, "rejected", "no-effects-rejected"
	if err := p.storeLocked(current); err != nil {
		return err
	}
	return &radioProducerNoReceiverEffects{}
}

func (q radioPrimaryCommand) matches(r radioPrimaryReceipt) bool {
	return r.Schema == q.Schema && r.Process == q.Process && r.Handoff == q.Handoff && r.Generation == q.Generation &&
		r.Action == q.Action && r.Target == q.Target && r.ExpectedRevision == q.ExpectedRevision &&
		(q.OwnerGeneration == "" || q.OwnerGeneration == r.OwnerGeneration) &&
		r.ForPhase == q.ForPhase && r.From == q.From && r.Station == q.Station &&
		(q.Session == nil || r.Session != nil && *r.Session == *q.Session)
}

func (p *radioProducer) primaryEffect(ctx context.Context, f radioFence, phase, target, station string) error {
	attempt, terminal := "", ""
	switch phase {
	case "prepare_start":
		attempt, terminal = "pause-suspend-attempted", "suspended"
	case "start":
		attempt, terminal = "start-attempted", "started"
	case "stop":
		attempt, terminal = "stop-attempted", "stopped"
	default:
		return errRadioUnavailable
	}
	r, err := p.admit(f, attempt)
	if err != nil || r.Primary == nil {
		return errRadioUnknown
	}
	q := r.primaryCommand(phase)
	p.mu.Lock()
	current := p.records[f.Token]
	if current.Fence != f || current.Stage != attempt || current.Preempted ||
		current.Terminal || p.evidence.NativeProcess != q.Process {
		p.mu.Unlock()
		return errRadioUnknown
	}
	if phase == "stop" {
		old := p.records[r.Primary.Incumbent]
		if old.Primary == nil || old.Primary.Session == nil || old.Request.Target != target || old.SourceDrained || p.active != r.Primary.Incumbent {
			p.mu.Unlock()
			return errRadioUnknown
		}
		if pinned := r.Primary.IncumbentSession; pinned != nil &&
			(*pinned != *old.Primary.Session || r.Primary.LastReceipt == nil ||
				r.Primary.LastReceipt.Phase != "replacement_prepared") {
			p.mu.Unlock()
			return errRadioUnknown
		}
		q.Session = old.Primary.Session
	} else if target != q.Target || phase == "start" && station != q.Station {
		p.mu.Unlock()
		return errRadioUnknown
	}
	state := *r.Primary
	state.LastCommand = &q
	state.LastReceipt = nil
	r.Primary = &state
	if err = p.storeLocked(r); err != nil {
		p.mu.Unlock()
		return err
	}
	p.mu.Unlock()
	if ctx.Err() != nil {
		return errRadioUnknown
	}
	receipt, err := p.primary.PrimaryCommand(ctx, q)
	if err != nil {
		return errRadioUnknown
	}
	receipt, err = p.primaryWait(ctx, q, receipt)
	if err == nil && receipt.Observation == nil && receipt.TeardownCSeq == 0 {
		if phase == "stop" && r.Primary.IncumbentSession != nil && receipt.Phase == "replacement_rejected" {
			return p.primaryRejectNoEffects(r, receipt)
		}
		if (phase == "prepare_start" || phase == "start") && r.Primary.Incumbent == "" &&
			receipt.Phase == "rejected" && receipt.Session == nil {
			return p.primaryRejectNoEffects(r, receipt)
		}
	}
	if err != nil || !primaryTerminalReceipt(q, receipt, r.Primary.Device) {
		return errRadioUnknown
	}
	return p.primaryRecordEffect(r, terminal, receipt)
}

func (p *radioProducer) primaryWait(ctx context.Context, command radioPrimaryCommand, receipt radioPrimaryReceipt) (radioPrimaryReceipt, error) {
	query := command
	ticker := time.NewTicker(35 * time.Millisecond)
	defer ticker.Stop()
	for ctx.Err() == nil && query.matches(receipt) {
		if receipt.Phase != "pending" {
			return receipt, nil
		}
		if receipt.TeardownCSeq != 0 || receipt.Observation != nil {
			break
		}
		select {
		case <-ctx.Done():
			return radioPrimaryReceipt{}, errRadioUnknown
		case <-ticker.C:
		}
		query.Phase, query.ForPhase = "result", command.Phase
		var err error
		receipt, err = p.primary.PrimaryCommand(ctx, query)
		if err != nil {
			break
		}
	}
	return radioPrimaryReceipt{}, errRadioUnknown
}

func primaryTerminalReceipt(q radioPrimaryCommand, r radioPrimaryReceipt, device string) bool {
	if r.Observation != nil {
		generation, err := strconv.ParseUint(q.OwnerGeneration, 10, 64)
		if err != nil || !validRadioObservation(*r.Observation) || r.Observation.OwnerGeneration != generation {
			return false
		}
	}
	switch q.Phase {
	case "prepare_start":
		return r.Phase == "prepared" && r.Observation == nil && r.TeardownCSeq == 0 && r.Session == nil
	case "start":
		return r.Phase == "playing" && r.TeardownCSeq == 0 && r.Session != nil && r.Session.valid() && r.Session.DeviceID == device &&
			r.Observation != nil && r.Observation.State == "playing" && r.Observation.SourceKind == "radio" &&
			r.Observation.StationID == q.Station && r.Observation.TargetID == q.Target
	case "stop":
		return r.Phase == "released" && r.TeardownCSeq > 0 && r.Session != nil && q.Session != nil && *r.Session == *q.Session &&
			r.Observation != nil && r.Observation.State == "stopped" && r.Observation.SourceKind == "none"
	}
	return false
}

func (p *radioProducer) primaryRecordEffect(r radioProducerRecord, stage string, receipt radioPrimaryReceipt) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	current := p.records[r.Fence.Token]
	if current.Fence != r.Fence || current.Stage != r.Stage || current.Preempted || current.Terminal || current.Primary == nil ||
		p.evidence.NativeProcess != r.Request.Process {
		return errRadioUnknown
	}
	state := *current.Primary
	state.LastReceipt = &receipt
	if stage == "started" {
		if state.IncumbentSession != nil && receipt.Session != nil && *receipt.Session == *state.IncumbentSession {
			return errRadioUnknown
		}
		state.Session = receipt.Session
	}
	if stage == "stopped" {
		old := p.records[state.Incumbent]
		if old.Primary == nil || old.Primary.Session == nil || receipt.Session == nil || *old.Primary.Session != *receipt.Session {
			return errRadioUnknown
		}
		retired := *old.Primary
		retired.StopReceipt = &receipt
		old.Primary, old.SourceDrained = &retired, true
		if err := p.storeLocked(old); err != nil {
			return err
		}
		if p.active == state.Incumbent {
			p.active = ""
			p.primaryObservedAt = 0
		}
	}
	current.Primary = &state
	current.Stage = stage
	return p.storeLocked(current)
}

func (p *radioProducer) primaryFinish(f radioFence) (radioNativeObservation, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	r := p.records[f.Token]
	if r.Fence != f || r.Primary == nil || r.Primary.LastReceipt == nil || r.Primary.LastReceipt.Observation == nil ||
		r.Preempted || r.Terminal || !p.live[f.Token] || r.Request.Process != p.evidence.NativeProcess ||
		(r.Stage != "started" && r.Stage != "stopped") ||
		(r.Stage == "stopped" && r.Primary.Station != "") ||
		!validRadioObservation(*r.Primary.LastReceipt.Observation) {
		return radioNativeObservation{}, errRadioUnknown
	}
	r.Observation = *r.Primary.LastReceipt.Observation
	r.Terminal, r.Outcome, r.Stage = true, "committed", "terminal"
	if err := p.storeLocked(r); err != nil {
		return radioNativeObservation{}, err
	}
	if r.Observation.SourceKind == "radio" {
		p.active = f.Token
		p.primaryObservedAt = r.Observation.ObservedAt
		revision, _ := strconv.ParseUint(r.Request.ExpectedIntent.Revision, 10, 64)
		p.primaryObservedRevision = max(p.primaryObservedRevision, revision)
		p.primaryIsolation = nil
	}
	return r.Observation, nil
}

func (p *radioProducer) primaryObserve(ctx context.Context) (radioNativeObservation, error) {
	state, err := p.primaryState(ctx)
	if err != nil || state.Observation == nil || !validRadioObservation(*state.Observation) {
		return radioNativeObservation{}, errRadioUnknown
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if state.Process != p.evidence.NativeProcess || !p.primaryIsolationReadCurrentLocked(state) {
		return radioNativeObservation{}, errRadioUnknown
	}
	if state.Observation.SourceKind == "radio" {
		incumbent := p.records[p.active]
		if !primaryOwnsPlaying(state, incumbent) || state.Observation.ObservedAt < p.primaryObservedAt {
			return radioNativeObservation{}, errRadioUnknown
		}
		p.primaryObservedAt = max(p.primaryObservedAt, state.Observation.ObservedAt)
		revision, _ := strconv.ParseUint(state.Revision, 10, 64)
		p.primaryObservedRevision = max(p.primaryObservedRevision, revision)
	} else if state.Session != nil {
		return radioNativeObservation{}, errRadioUnknown
	} else if p.active != "" {
		if err := p.retireObservedPrimaryLocked(state); err != nil {
			return radioNativeObservation{}, err
		}
	}
	if p.primaryIsolation != nil {
		revision, _ := strconv.ParseUint(state.Revision, 10, 64)
		p.primaryObservedRevision = max(p.primaryObservedRevision, revision)
		p.primaryObservedAt = max(p.primaryObservedAt, state.Observation.ObservedAt)
	}
	return *state.Observation, nil
}

// Local isolation is a passive custody observation, never a STOP receipt.
func (p *radioProducer) observePrimaryIsolation(ctx context.Context, fence radioFence) (radioNativeObservation, error) {
	if p.primary == nil {
		return radioNativeObservation{}, errRadioUnavailable
	}
	state, err := p.primaryState(ctx)
	if err != nil || state.IsolatedSession == nil || state.Missing == nil || len(state.Missing) != 0 {
		return radioNativeObservation{}, errRadioUnknown
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	record := p.records[p.active]
	isolated := state.IsolatedSession
	observation := *state.Observation
	revision, _ := strconv.ParseUint(state.Revision, 10, 64)
	if ctx.Err() != nil || state.Process != p.evidence.NativeProcess || record.Request.Process != state.Process ||
		!p.live[p.active] || record.Fence != fence || !record.Terminal || record.Outcome != "committed" ||
		record.Preempted || record.SourceDrained || record.Primary == nil || record.Primary.Session == nil ||
		record.Primary.Device != isolated.DeviceID || *record.Primary.Session != isolated.radioPrimarySession || observation.TargetID != record.Request.Target ||
		observation.OwnerGeneration != record.Fence.OwnerGeneration ||
		observation.ObservedAt < p.primaryObservedAt || revision <= p.primaryObservedRevision {
		return radioNativeObservation{}, errRadioUnknown
	}
	if !primaryIsolatedTargetAvailable(state, record.Request.Target, isolated.DeviceID) {
		return radioNativeObservation{}, errRadioUnknown
	}
	for _, pending := range p.records {
		if !pending.Terminal {
			return radioNativeObservation{}, errRadioUnknown
		}
	}
	custody := *record.Primary
	custody.Isolation = isolated
	record.Primary, record.SourceDrained = &custody, true
	if err := p.storeLocked(record); err != nil {
		return radioNativeObservation{}, err
	}
	p.active = ""
	p.primaryIsolation = &radioPrimaryIsolationBinding{
		session: *isolated, target: observation.TargetID, ownerGeneration: observation.OwnerGeneration, revision: revision,
	}
	p.primaryObservedAt = observation.ObservedAt
	p.primaryObservedRevision = revision
	return observation, nil
}

func primaryIsolatedTargetAvailable(state radioPrimaryState, target, device string) bool {
	found := false
	for _, entry := range state.Targets {
		if entry.ID != target && entry.HomePodID != device {
			continue
		}
		if found || entry.ID != target || entry.HomePodID != device || entry.ReceiverDigest != "" ||
			!entry.Selectable || entry.Availability != "available" {
			return false
		}
		found = true
	}
	return found
}

func (p *radioProducer) primaryIsolationReadCurrentLocked(state radioPrimaryState) bool {
	if p.primaryIsolation == nil {
		return state.IsolatedSession == nil
	}
	revision, _ := strconv.ParseUint(state.Revision, 10, 64)
	observation := state.Observation
	if state.Missing == nil || len(state.Missing) != 0 || revision < p.primaryObservedRevision ||
		observation == nil || !validRadioObservation(*observation) || !primaryKnownNonradio(*observation) || observation.OwnerGeneration < p.primaryIsolation.ownerGeneration || observation.ObservedAt < p.primaryObservedAt {
		return false
	}
	if state.IsolatedSession == nil {
		return revision > p.primaryIsolation.revision
	}
	return *state.IsolatedSession == p.primaryIsolation.session && revision == p.primaryIsolation.revision &&
		observation.OwnerGeneration == p.primaryIsolation.ownerGeneration && observation.TargetID == p.primaryIsolation.target &&
		primaryIsolatedTargetAvailable(state, observation.TargetID, state.IsolatedSession.DeviceID)
}

func (p *radioProducer) retireObservedPrimaryLocked(state radioPrimaryState) error {
	record := p.records[p.active]
	observation := *state.Observation
	if !p.live[p.active] || !record.Terminal || record.Preempted || record.SourceDrained ||
		record.Request.Process != state.Process || record.Primary == nil || record.Primary.Session == nil ||
		state.ReleasedSession == nil || *record.Primary.Session != *state.ReleasedSession ||
		observation.OwnerGeneration < record.Fence.OwnerGeneration ||
		observation.OwnerGeneration == record.Fence.OwnerGeneration && observation.ObservedAt <= p.primaryObservedAt {
		return errRadioUnknown
	}
	for _, pending := range p.records {
		if !pending.Terminal {
			return errRadioUnknown
		}
	}
	custody := *record.Primary
	custody.ReleaseActionID, custody.TeardownCSeq = *state.ReleaseActionID, *state.TeardownCSeq
	record.Primary = &custody
	record.SourceDrained = true
	if err := p.storeLocked(record); err != nil {
		return err
	}
	p.active = ""
	p.primaryObservedAt = 0
	return nil
}

// Reconciliation can observe the exact pending effect. It never sends its
// command again and never resumes a multi-effect action after an uncertain STOP.
func (p *radioProducer) primaryReconcile(ctx context.Context, f radioFence) (radioReconciliation, error) {
	p.mu.Lock()
	r := p.records[f.Token]
	if f.Token == "" {
		for _, candidate := range p.records {
			if candidate.Fence.ActionID == f.ActionID && candidate.Fence.Generation == f.Generation {
				r = candidate
				break
			}
		}
	}
	current := r.Fence.ActionID == f.ActionID && r.Fence.Generation == f.Generation &&
		(f.Token == "" || r.Fence == f) && p.live[r.Fence.Token] && r.Request.Process == p.evidence.NativeProcess
	p.mu.Unlock()
	if !current || r.Primary == nil || r.Primary.LastCommand == nil || r.Preempted {
		return radioReconciliation{}, errRadioUnknown
	}
	command := *r.Primary.LastCommand
	stage := ""
	switch command.Phase {
	case "start":
		stage = "started"
	case "stop":
		if r.Primary.Station == "" {
			stage = "stopped"
		}
	}
	if stage == "" {
		// A replacement may have refused STOP before any receiver effect.
		// Recover only that exact terminal refusal, never a positive STOP
		// followed by a speculative successor.
		replacement := r.Primary.IncumbentSession != nil && (command.Phase == "prepare_replace" || command.Phase == "stop")
		first := r.Primary.Incumbent == "" && command.Phase == "prepare_start"
		if replacement || first {
			query := command
			query.Phase, query.ForPhase = "result", command.Phase
			receipt, err := p.primary.PrimaryCommand(ctx, query)
			if err == nil && ctx.Err() == nil && query.matches(receipt) && receipt.Observation == nil && receipt.TeardownCSeq == 0 &&
				(replacement && receipt.Phase == "replacement_rejected" || first && receipt.Phase == "rejected" && receipt.Session == nil) {
				return p.primaryReconcileNoEffects(ctx, r, receipt)
			}
		}
		return radioReconciliation{}, errRadioUnknown
	}
	if r.Terminal && r.Primary.Session != nil {
		// A prior successful START is historical. After an observation failure,
		// read the current owner again instead of reviving its old playing receipt.
		return p.primaryReconcileStarted(ctx, r)
	}
	if !r.Terminal {
		if r.Stage != stage {
			query := command
			query.Phase, query.ForPhase = "result", command.Phase
			receipt, err := p.primary.PrimaryCommand(ctx, query)
			if err != nil || ctx.Err() != nil || !query.matches(receipt) {
				return radioReconciliation{}, errRadioUnknown
			}
			if command.Phase == "start" && r.Primary.Incumbent == "" && receipt.Phase == "rejected" &&
				receipt.Session == nil && receipt.Observation == nil && receipt.TeardownCSeq == 0 {
				return p.primaryReconcileNoEffects(ctx, r, receipt)
			}
			if !primaryTerminalReceipt(command, receipt, r.Primary.Device) {
				return radioReconciliation{}, errRadioUnknown
			}
			if err = p.primaryRecordEffect(r, stage, receipt); err != nil {
				return radioReconciliation{}, err
			}
		}
		observation, err := p.primaryFinish(r.Fence)
		if err != nil {
			return radioReconciliation{}, err
		}
		r.Observation = observation
	}
	if !validRadioObservation(r.Observation) {
		return radioReconciliation{}, errRadioUnknown
	}
	return radioReconciliation{ActionID: f.ActionID, Generation: f.Generation, Token: r.Fence.Token,
		Terminal: true, Outcome: "committed", Observation: r.Observation}, nil
}

func (p *radioProducer) primaryReconcileNoEffects(ctx context.Context, r radioProducerRecord, receipt radioPrimaryReceipt) (radioReconciliation, error) {
	observation, err := p.primaryObserve(ctx)
	wantRadio := r.Primary.Incumbent != ""
	if err != nil || wantRadio && (observation.SourceKind != "radio" || observation.State != "playing") ||
		!wantRadio && !primaryKnownNonradio(observation) {
		return radioReconciliation{}, errRadioUnknown
	}
	if !r.Terminal {
		var rejected *radioProducerNoReceiverEffects
		if err = p.primaryRejectNoEffects(r, receipt); !errors.As(err, &rejected) {
			return radioReconciliation{}, errRadioUnknown
		}
	} else if r.Outcome != "rejected" {
		return radioReconciliation{}, errRadioUnknown
	}
	return radioReconciliation{ActionID: r.Fence.ActionID, Generation: r.Fence.Generation, Token: r.Fence.Token,
		Terminal: true, Outcome: "rejected", Observation: observation}, nil
}

func (p *radioProducer) primaryReconcileStarted(ctx context.Context, r radioProducerRecord) (radioReconciliation, error) {
	observation, err := p.primaryObserve(ctx)
	if err != nil {
		return radioReconciliation{}, err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	current := p.records[r.Fence.Token]
	if current.Fence != r.Fence || !p.live[r.Fence.Token] || current.Primary == nil {
		return radioReconciliation{}, errRadioUnknown
	}
	outcome := "committed"
	if observation.SourceKind == "radio" {
		if p.active != r.Fence.Token || current.SourceDrained {
			return radioReconciliation{}, errRadioUnknown
		}
	} else {
		if !primaryKnownNonradio(observation) || !current.SourceDrained || current.Primary.ReleaseActionID == "" || p.active != "" {
			return radioReconciliation{}, errRadioUnknown
		}
		outcome = "preempted"
	}
	return radioReconciliation{ActionID: r.Fence.ActionID, Generation: r.Fence.Generation, Token: r.Fence.Token,
		Terminal: true, Outcome: outcome, Observation: observation}, nil
}

// Primary volume remains unavailable until the same C owner can return an
// exact receiver acknowledgement for it.
func (p *radioProducer) RadioCapabilities() []string {
	if p.primary != nil {
		p.mu.Lock()
		defer p.mu.Unlock()
		if p.primary.(*radioPrimaryBackend).replacement {
			return []string{"start", "stop", "move"}
		}
		return []string{"start", "stop"}
	}
	return []string{"start", "stop", "move", "volume"}
}
