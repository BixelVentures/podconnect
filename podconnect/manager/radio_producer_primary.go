package main

import (
	"context"
	"strconv"
	"time"
)

const primaryRadioSchema = "podconnect.native_radio_primary.v1"

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
	Schema      string                  `json:"schema"`
	Process     string                  `json:"process_id"`
	Revision    string                  `json:"native_revision"`
	Targets     []radioProducerTarget   `json:"configured_targets"`
	Missing     []string                `json:"missing_proofs"`
	Observation *radioNativeObservation `json:"observation,omitempty"`
	Session     *radioPrimarySession    `json:"target_session,omitempty"`
}

type radioPrimaryPort interface {
	PrimaryState(context.Context) (radioPrimaryState, error)
	PrimaryCommand(context.Context, radioPrimaryCommand) (radioPrimaryReceipt, error)
}

func (n *radioProducerNativeClient) PrimaryState(ctx context.Context) (radioPrimaryState, error) {
	var state radioPrimaryState
	err := n.http.call(ctx, "/radio/primary", nil, &state)
	return state, err
}

func (n *radioProducerNativeClient) PrimaryCommand(ctx context.Context, q radioPrimaryCommand) (radioPrimaryReceipt, error) {
	var result radioPrimaryReceipt
	err := n.http.call(ctx, "/radio/primary", q, &result)
	return result, err
}

type radioPrimaryRecord struct {
	Device      string               `json:"device_id"`
	Station     string               `json:"station_id,omitempty"`
	Incumbent   string               `json:"incumbent,omitempty"`
	Session     *radioPrimarySession `json:"target_session,omitempty"`
	LastCommand *radioPrimaryCommand `json:"last_command,omitempty"`
	LastReceipt *radioPrimaryReceipt `json:"last_receipt,omitempty"`
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
	return &radioProducer{primary: port, journal: journal, records: records, live: make(map[string]bool)}, nil
}

func (p *radioProducer) primaryState(ctx context.Context) (radioPrimaryState, error) {
	state, err := p.primary.PrimaryState(ctx)
	if err != nil || ctx.Err() != nil || state.Schema != primaryRadioSchema || !producerHex(state.Process, 64) || !producerCounter(state.Revision, true) {
		return radioPrimaryState{}, errRadioUnavailable
	}
	return state, nil
}

func (p *radioProducer) primaryReady(ctx context.Context) ([]radioTarget, []string, error) {
	state, err := p.primaryState(ctx)
	if err != nil {
		return nil, nil, err
	}
	if len(state.Missing) != 0 {
		return nil, state.Missing, errRadioUnavailable
	}
	if state.Observation == nil || !validRadioObservation(*state.Observation) {
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
	for token, record := range p.records {
		if record.Primary == nil || !record.Terminal || record.Observation.SourceKind == "radio" &&
			record.Observation.State == "playing" && !record.SourceDrained && !p.live[token] {
			return nil, []string{"durable_unknown_effect_custody"}, errRadioUnavailable
		}
	}
	if p.evidence.NativeProcess != "" && p.evidence.NativeProcess != state.Process {
		return nil, nil, errRadioUnknown
	}
	if state.Observation.SourceKind == "radio" {
		active := p.records[p.active]
		if active.Primary == nil || active.Primary.Session == nil || state.Session == nil ||
			*active.Primary.Session != *state.Session || state.Observation.OwnerGeneration != active.Fence.OwnerGeneration {
			return nil, []string{"durable_unknown_effect_custody"}, errRadioUnavailable
		}
	} else if state.Session != nil || p.active != "" {
		return nil, nil, errRadioUnknown
	}
	p.evidence.NativeProcess = state.Process
	return targets, nil, nil
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
	if device == "" || state.Process != p.evidence.NativeProcess || len(state.Missing) != 0 || p.active != "" {
		return errRadioUnavailable
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
		Stage: "preflight-prepared", Primary: &radioPrimaryRecord{Device: device, Station: station},
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
	if action == "" || generation == 0 || p.evidence.NativeProcess == "" || to != "" && p.active != "" {
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
	if p.active != "" {
		state.Incumbent = incumbent.Fence.Token
	}
	r.Primary = &state
	r.Request.From = from
	r.Stage = "native-reserve-attempted"
	if err := p.storeLocked(r); err != nil {
		p.mu.Unlock()
		return r.Fence, err
	}
	p.live[r.Fence.Token] = true
	q := r.primaryCommand("reserve")
	p.mu.Unlock()

	if ctx.Err() != nil {
		return r.Fence, errRadioUnknown
	}
	receipt, err := p.primary.PrimaryCommand(ctx, q)
	if err != nil || ctx.Err() != nil || !q.matches(receipt) || receipt.Phase != "reserved" ||
		!producerCounter(receipt.OwnerGeneration, false) || receipt.Session != nil || receipt.Observation != nil || receipt.TeardownCSeq != 0 {
		return r.Fence, errRadioUnknown
	}
	owner, _ := strconv.ParseUint(receipt.OwnerGeneration, 10, 64)
	p.mu.Lock()
	defer p.mu.Unlock()
	current := p.records[r.Fence.Token]
	if current.Fence != r.Fence || current.Stage != r.Stage || current.Preempted || p.evidence.NativeProcess != q.Process {
		return r.Fence, errRadioUnknown
	}
	current.Fence.OwnerGeneration = owner
	current.Stage = "reserved"
	return current.Fence, p.storeLocked(current)
}

func (r radioProducerRecord) primaryCommand(phase string) radioPrimaryCommand {
	q := radioPrimaryCommand{
		Schema: primaryRadioSchema, Phase: phase, Process: r.Request.Process, Handoff: r.Fence.Token,
		Action: r.Fence.ActionID, Generation: strconv.FormatUint(r.Fence.Generation, 10), Target: r.Request.Target,
		ExpectedRevision: r.Request.ExpectedIntent.Revision, From: r.Request.From,
	}
	if r.Fence.OwnerGeneration != 0 {
		q.OwnerGeneration = strconv.FormatUint(r.Fence.OwnerGeneration, 10)
	}
	if phase == "start" || phase == "prepare_start" {
		q.Station = r.Primary.Station
	}
	return q
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
		state.Session = receipt.Session
	}
	if stage == "stopped" {
		old := p.records[state.Incumbent]
		if old.Primary == nil || old.Primary.Session == nil || receipt.Session == nil || *old.Primary.Session != *receipt.Session {
			return errRadioUnknown
		}
		old.SourceDrained = true
		if err := p.storeLocked(old); err != nil {
			return err
		}
		if p.active == state.Incumbent {
			p.active = ""
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
	if state.Process != p.evidence.NativeProcess {
		return radioNativeObservation{}, errRadioUnknown
	}
	if state.Observation.SourceKind == "radio" {
		incumbent := p.records[p.active]
		if incumbent.Primary == nil || incumbent.Primary.Session == nil || state.Session == nil ||
			*state.Session != *incumbent.Primary.Session || state.Observation.OwnerGeneration != incumbent.Fence.OwnerGeneration ||
			state.Observation.TargetID != incumbent.Request.Target || state.Observation.StationID != incumbent.Primary.Station {
			return radioNativeObservation{}, errRadioUnknown
		}
	} else if p.active != "" || state.Session != nil {
		return radioNativeObservation{}, errRadioUnknown
	}
	return *state.Observation, nil
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
		return radioReconciliation{}, errRadioUnknown
	}
	if !r.Terminal {
		if r.Stage != stage {
			query := command
			query.Phase, query.ForPhase = "result", command.Phase
			receipt, err := p.primary.PrimaryCommand(ctx, query)
			if err != nil || ctx.Err() != nil || !query.matches(receipt) || !primaryTerminalReceipt(command, receipt, r.Primary.Device) {
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

// Primary volume remains unavailable until the same C owner can return an
// exact receiver acknowledgement for it.
func (p *radioProducer) RadioCapabilities() []string {
	if p.primary != nil {
		return []string{"start", "stop"}
	}
	return []string{"start", "stop", "move", "volume"}
}
