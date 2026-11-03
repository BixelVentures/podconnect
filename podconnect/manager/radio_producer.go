package main

// The built-in producer is the existing coordinator's backend. Native/C ports
// own receiver authority; this journal only records which exact once-only call
// was admitted. It is never a substitute for an all-effect native fence.
import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
)

type radioProducerIdentity struct {
	Process         string `json:"process_id"`
	Handoff         string `json:"handoff_id"`
	Generation      uint64 `json:"generation"`
	ActionID        string `json:"action_id"`
	Target          string `json:"target_id"`
	OwnerGeneration uint64 `json:"owner_generation"`
}
type radioProducerHold struct {
	radioProducerIdentity
	// Full owner/writer/route proof is opaque to this layer, retained byte-for-byte.
	// Its native recipient validates it; no manager-created bool authorizes effects.
	SourceGeneration uint64          `json:"source_generation,omitempty"`
	Token            string          `json:"token"`
	Proof            json.RawMessage `json:"proof"`
}
type radioProducerPeer struct {
	Operation        radioProducerIdentity    `json:"operation"`
	Process          string                   `json:"radio_process"`
	OwnerGeneration  uint64                   `json:"radio_owner_epoch"`
	Target           string                   `json:"target_id"`
	ReceiverDigest   string                   `json:"receiver_key_digest"`
	SourceGeneration uint64                   `json:"source_generation,omitempty"`
	CandidateID      string                   `json:"candidate_id"`
	TargetAuth       *radioProducerTargetAuth `json:"target_auth,omitempty"`
	Proof            json.RawMessage          `json:"proof"`
}
type radioProducerPrior struct {
	Native radioProducerHold `json:"native"`
	Sender radioProducerHold `json:"sender"`
	Target string            `json:"target_id"`
}
type radioProducerIntent struct {
	Process  string `json:"process_id"`
	Revision string `json:"native_revision"`
}
type radioProducerReserve struct {
	ExpectedIntent radioProducerIntent `json:"expected_intent"`
	radioProducerIdentity
	Peer     radioProducerPeer   `json:"sender_peer"`
	From     string              `json:"source_target_id"`
	Prior    *radioProducerPrior `json:"prior,omitempty"`
	Retained *radioProducerPrior `json:"retained_target,omitempty"`
}
type radioProducerTargetBinding struct {
	Device string
	Digest string
}
type radioProducerEvidence struct {
	Bindings      map[string]radioProducerTargetBinding
	Controller    string
	NativeProcess string
	SenderProcess string
	Targets       []radioTarget
	// Only concrete production ports may validate the full actual capabilities,
	// separate sender identity/pairing and resource-custody attestation.
	Missing []string
}
type radioProducerResult struct {
	// Disjoint historical custody. It is never a grant or terminal settlement.
	Custody     *radioProducerNativeCustody
	Hold        *radioProducerHold
	Identity    radioProducerIdentity
	Terminal    bool
	Outcome     string
	Observation radioNativeObservation
	// Signed exact-peer drain ticket remains opaque; Native/C verifies/consumes it.
	DrainTicket    json.RawMessage
	ReleaseReceipt json.RawMessage
}
type radioProducerNativePort interface {
	Evidence(context.Context) (radioProducerEvidence, error)
	CaptureIntent(context.Context, string) (radioProducerIntent, error)
	Reserve(context.Context, radioProducerReserve) (radioProducerHold, error)
	PauseSuspend(context.Context, radioProducerHold, radioProducerIdentity) error
	Result(context.Context, radioProducerReserve, radioProducerHold) (radioProducerResult, error)
}
type radioProducerSenderPort interface {
	Evidence(context.Context, radioProducerEvidence) (radioProducerEvidence, error)
	Preflight(context.Context, string, string, radioProducerIdentity) (radioProducerPeer, error)
	CancelCandidate(context.Context, radioProducerPeer) (json.RawMessage, error)
	Reserve(context.Context, radioProducerReserve, radioProducerHold) (radioProducerHold, error)
	Start(context.Context, radioProducerHold, radioProducerHold, radioProducerIdentity, string, string) error
	StopDrain(context.Context, radioProducerHold, radioProducerIdentity, string) (json.RawMessage, error)
	PreemptDrain(context.Context, radioProducerHold, radioProducerHold, radioNativePreemptRequest) (json.RawMessage, error)
	Volume(context.Context, radioProducerHold, radioProducerIdentity, string, int) error
	Finish(context.Context, radioProducerHold, radioProducerIdentity) (radioProducerResult, error)
	Observe(context.Context) (radioNativeObservation, error)
	Result(context.Context, radioProducerIdentity) (radioProducerResult, error)
}
type radioProducerRecord struct {
	Controller          string                 `json:"controller_id,omitempty"`
	NativeRelease       json.RawMessage        `json:"native_release_receipt,omitempty"`
	NativeRejection     json.RawMessage        `json:"native_rejection,omitempty"`
	CancellationReceipt json.RawMessage        `json:"cancellation_receipt,omitempty"`
	SourceDrained       bool                   `json:"source_drained"`
	PreemptChallenge    json.RawMessage        `json:"preempt_challenge,omitempty"`
	PreemptAction       string                 `json:"preempt_action_id,omitempty"`
	Fence               radioFence             `json:"fence"`
	Request             radioProducerReserve   `json:"request"`
	Native              radioProducerHold      `json:"native"`
	Sender              radioProducerHold      `json:"sender"`
	Stage               string                 `json:"stage"`
	Preempted           bool                   `json:"preempted"`
	Terminal            bool                   `json:"terminal"`
	Outcome             string                 `json:"outcome"`
	Observation         radioNativeObservation `json:"observation"`
	DrainTicket         json.RawMessage        `json:"drain_ticket,omitempty"`
}
type radioProducer struct {
	physicalAccepted bool
	// A deployed worker proves runtime custody from the actual process. Physical
	// acceptance remains separately reported; it is not fabricated at activation.
	runtimeReadinessOnly bool
	mu                   sync.Mutex
	native               radioProducerNativePort
	sender               radioProducerSenderPort
	journal              *radioProducerJournal
	evidence             radioProducerEvidence
	active               string
	records              map[string]radioProducerRecord
	prepared             map[string]radioProducerPeer
	live                 map[string]bool
	retained             map[string]radioProducerPrior
}

func newRadioProducer(native radioProducerNativePort, sender radioProducerSenderPort, directory string, physicalAccepted bool) (*radioProducer, error) {
	if native == nil || sender == nil {
		return nil, errRadioUnavailable
	}
	journal, records, e := openRadioProducerJournal(directory)
	if e != nil {
		return nil, e
	}
	return &radioProducer{native: native, sender: sender, journal: journal, records: records, prepared: make(map[string]radioProducerPeer), physicalAccepted: physicalAccepted, live: make(map[string]bool), retained: make(map[string]radioProducerPrior)}, nil
}
func (p *radioProducer) Ready(ctx context.Context) ([]radioTarget, []string, error) {
	p.recoverPassive(ctx)
	native, e := p.native.Evidence(ctx)
	if e != nil {
		return nil, nil, e
	}
	sender, e := p.sender.Evidence(ctx, native)
	if e != nil {
		return nil, nil, e
	}
	sender.NativeProcess = native.NativeProcess
	missing := append(append([]string{}, native.Missing...), sender.Missing...)
	if !p.physicalAccepted && !p.runtimeReadinessOnly {
		missing = append(missing, "physical_homepod_acceptance")
	}
	if native.NativeProcess == "" || sender.SenderProcess == "" || native.NativeProcess == sender.SenderProcess {
		missing = append(missing, "independent_sender_identity_and_pairing")
	}
	retired := p.completedNativeCustody(ctx, sender)
	p.mu.Lock()
	defer p.mu.Unlock()
	// Startup never adopts an old hold or starts/replays a radio effect. Unsettled
	// durable records are an explicit enable blocker until passive recovery.
	for key, r := range p.records {
		if !r.Terminal || r.Observation.SourceKind == "radio" && r.Observation.State == "playing" && !p.live[key] && !retired[key] {
			missing = append(missing, "durable_unknown_effect_custody")
			break
		}
	}
	if len(missing) > 0 {
		return nil, missing, errRadioUnavailable
	}
	p.evidence = sender
	p.evidence.NativeProcess = native.NativeProcess
	return append([]radioTarget{}, sender.Targets...), nil, nil
}

// completedNativeCustody is a passive current-authority read, never a journal
// unlock or a custody grant. A STOP receipt alone leaves Native suspended and
// cannot retire historical playing records. The exact completed return must
// either be read from its original Native process or retained byte-for-byte
// after that authenticated read, and match the current physical pairing.
func (p *radioProducer) completedNativeCustody(ctx context.Context, evidence radioProducerEvidence) map[string]bool {
	p.mu.Lock()
	records := make(map[string]radioProducerRecord, len(p.records))
	for key, record := range p.records {
		records[key] = record
	}
	p.mu.Unlock()
	retired := map[string]bool{}
	latest := map[string]radioProducerRecord{}
	for _, record := range records {
		key := record.Native.Process + "/" + record.Native.Handoff + "/" + record.Native.Token
		if previous, ok := latest[key]; !ok || record.Fence.Generation > previous.Fence.Generation {
			latest[key] = record
		}
	}
	for _, completed := range latest {
		if !completed.Preempted || !completed.Terminal || completed.Outcome != "preempted" || len(completed.DrainTicket) == 0 {
			continue
		}
		binding, ok := evidence.Bindings[completed.Native.Target]
		envelope, err := senderProducerEnvelope(completed.Sender)
		if !ok || err != nil || completed.Controller != evidence.Controller || envelope.Request["device_id"] != binding.Device || completed.Request.Peer.ReceiverDigest != binding.Digest {
			continue
		}
		result := radioProducerResult{}
		if validProducerNativeRelease(completed) {
			result = radioProducerResult{Identity: producerNativeResultIdentity(completed), Terminal: true, Outcome: "preempted", DrainTicket: completed.DrainTicket}
		} else if completed.Native.Process == evidence.NativeProcess {
			result, err = p.native.Result(ctx, completed.Request, completed.Native)
			if err == nil && len(result.ReleaseReceipt) == 0 {
				err = errRadioUnknown
			}
			if err == nil && len(result.ReleaseReceipt) > 0 {
				updated := completed
				updated.NativeRelease = append(json.RawMessage{}, result.ReleaseReceipt...)
				if validProducerNativeRelease(updated) {
					p.mu.Lock()
					current := p.records[completed.Fence.Token]
					if current.Fence == completed.Fence && current.Preempted && producerJSONEqual(current.DrainTicket, completed.DrainTicket) {
						current.NativeRelease = updated.NativeRelease
						err = p.storeLocked(current)
					}
					p.mu.Unlock()
				} else {
					err = errRadioUnknown
				}
			}
		} else {
			err = errRadioUnknown
		}
		if err != nil || !result.Terminal || result.Outcome != "preempted" || result.Identity != producerNativeResultIdentity(completed) || !producerJSONEqual(result.DrainTicket, completed.DrainTicket) {
			continue
		}
		for key, old := range records {
			if old.Native.radioProducerIdentity == completed.Native.radioProducerIdentity && old.Native.Token == completed.Native.Token && producerJSONEqual(old.Native.Proof, completed.Native.Proof) {
				retired[key] = true
			}
		}
	}
	return retired
}
func validProducerNativeRelease(record radioProducerRecord) bool {
	envelope, err := nativeProducerEnvelope(record.Native)
	if err != nil || len(record.NativeRelease) == 0 {
		return false
	}
	reply, err := decodeProducerNativeCompletion(record.NativeRelease, envelope.Request, "result")
	return err == nil && producerJSONEqual(reply.Proof, envelope.Binding) && producerJSONEqual(reply.TerminalDrain, record.DrainTicket)
}
func producerHistoryReleased(records map[string]radioProducerRecord, old radioProducerRecord) bool {
	for _, completed := range records {
		if completed.Preempted && completed.Terminal && completed.Native.radioProducerIdentity == old.Native.radioProducerIdentity && completed.Native.Token == old.Native.Token && producerJSONEqual(completed.Native.Proof, old.Native.Proof) && validProducerNativeRelease(completed) {
			return true
		}
	}
	return false
}
func (p *radioProducer) Preflight(context.Context, string, string) error { return errRadioUnavailable }
func (p *radioProducer) PreflightOperation(ctx context.Context, station, target, action string, generation uint64) error {
	if action == "" || generation == 0 {
		return errRadioUnavailable
	}
	if _, ok := radioStreamURLs[station]; !ok || target == "" {
		return errRadioUnavailable
	}
	p.mu.Lock()
	senderProcess, nativeProcess := p.evidence.SenderProcess, p.evidence.NativeProcess
	if senderProcess == "" || nativeProcess == "" {
		p.mu.Unlock()
		return errRadioUnavailable
	}
	for _, old := range p.records {
		if !old.Terminal || old.Fence.ActionID == action {
			p.mu.Unlock()
			return errRadioUnknown
		}
	}
	p.mu.Unlock()
	expectedIntent, e := p.native.CaptureIntent(ctx, target)
	if e != nil || expectedIntent.Process != nativeProcess || expectedIntent.Revision == "" {
		return errRadioUnavailable
	}
	p.mu.Lock()
	for _, old := range p.records {
		if !old.Terminal || old.Fence.ActionID == action {
			p.mu.Unlock()
			return errRadioUnknown
		}
	}
	operation := radioProducerIdentity{Process: senderProcess, Handoff: attentionUUID(), Generation: generation, ActionID: action, Target: target}
	r := radioProducerRecord{Fence: radioFence{Token: operation.Handoff, ActionID: action, Generation: generation}, Stage: "preflight-attempted"}
	nativeOperation := operation
	nativeOperation.Process = nativeProcess
	r.Request = radioProducerReserve{ExpectedIntent: expectedIntent, radioProducerIdentity: nativeOperation, Peer: radioProducerPeer{Process: senderProcess, Operation: operation, Target: target}}
	if e := p.storeLocked(r); e != nil {
		p.mu.Unlock()
		return e
	}
	p.live[operation.Handoff] = true
	p.mu.Unlock()
	peer, e := p.sender.Preflight(ctx, station, target, operation)
	p.mu.Lock()
	defer p.mu.Unlock()
	r = p.records[operation.Handoff]
	if r.Preempted || r.Stage != "preflight-attempted" {
		return errRadioUnknown
	}
	if e != nil {
		if errors.Is(e, errRadioUnavailable) {
			r.Terminal = true
			r.Outcome = "rejected"
			r.Stage = "preflight-rejected"
			if saveErr := p.storeLocked(r); saveErr != nil {
				return saveErr
			}
		}
		return e
	}
	if peer.Process != senderProcess || peer.OwnerGeneration == 0 || peer.SourceGeneration == 0 || peer.Target != target || len(peer.ReceiverDigest) != 64 || peer.CandidateID != operation.Handoff || !json.Valid(peer.Proof) {
		return errRadioUnknown
	}
	peer.Operation = operation
	r.Request.Peer = peer
	r.Stage = "preflight-prepared"
	if e = p.storeLocked(r); e != nil {
		return e
	}
	p.prepared[target] = peer
	return nil
}
func (p *radioProducer) Reserve(ctx context.Context, from, to, action string, generation uint64) (radioFence, error) {
	p.mu.Lock()
	if action == "" || generation == 0 || p.evidence.NativeProcess == "" || p.evidence.SenderProcess == "" {
		p.mu.Unlock()
		return radioFence{}, errRadioUnavailable
	}
	sourceOperation := to == ""
	if sourceOperation {
		to = from
	}
	if to == "" {
		p.mu.Unlock()
		return radioFence{}, errRadioUnavailable
	}
	var r radioProducerRecord
	found := false
	for _, old := range p.records {
		same := old.Fence.ActionID == action && old.Fence.Generation == generation
		if same && old.Stage == "preflight-prepared" && old.Request.Target == to {
			r = old
			found = true
			continue
		}
		if old.Fence.ActionID == action || !old.Terminal {
			p.mu.Unlock()
			return radioFence{}, errRadioUnknown
		}
	}
	if !found {
		if !sourceOperation || p.active == "" {
			p.mu.Unlock()
			return radioFence{}, errRadioUnavailable
		}
		prior := p.records[p.active]
		if prior.Request.Target != to {
			p.mu.Unlock()
			return radioFence{}, errRadioUnavailable
		}
		id := attentionUUID()
		r = radioProducerRecord{Fence: radioFence{Token: id, ActionID: action, Generation: generation}, Request: radioProducerReserve{radioProducerIdentity: radioProducerIdentity{Process: p.evidence.NativeProcess, Handoff: id, Generation: generation, ActionID: action, Target: to}, Peer: prior.Request.Peer, ExpectedIntent: prior.Request.ExpectedIntent}}
	}
	r.Request.From = from
	if retained, ok := p.retained[to]; ok && retained.Sender.OwnerGeneration == r.Request.Peer.OwnerGeneration {
		copy := retained
		r.Request.Retained = &copy
	}
	if p.active != "" {
		prior := p.records[p.active]
		r.Request.Prior = &radioProducerPrior{Native: prior.Native, Sender: prior.Sender, Target: prior.Request.Target}
	}
	r.Stage = "native-reserve-attempted"
	expectedNative, expectedSender := p.evidence.NativeProcess, p.evidence.SenderProcess
	if e := p.storeLocked(r); e != nil {
		p.mu.Unlock()
		return r.Fence, e
	}
	p.live[r.Fence.Token] = true
	p.mu.Unlock()
	var native radioProducerHold
	var e error
	if sourceOperation && r.Request.Prior != nil {
		// Stopping or changing volume on our own sender never depends on the
		// continued availability of the paused Spotify/Native process.
		native = r.Request.Prior.Native
	} else if r.Request.Retained != nil {
		native = r.Request.Retained.Native
		if p.retainedResult(ctx, r, native) != nil {
			return r.Fence, errRadioUnknown
		}
	} else if r.Request.Prior != nil && r.Request.Prior.Target == to {
		native = r.Request.Prior.Native
		if p.retainedResult(ctx, r, native) != nil {
			return r.Fence, errRadioUnknown
		}
	} else {
		native, e = p.native.Reserve(ctx, r.Request)
	}
	if e != nil {
		var rejected *radioProducerOperationRejected
		if errors.As(e, &rejected) {
			return r.Fence, p.rejectCandidate(ctx, r, rejected)
		}
		return r.Fence, errRadioUnknown
	}
	if !producerSourceHoldMatches(native, to, expectedNative) {
		return r.Fence, errRadioUnknown
	}
	p.mu.Lock()
	r = p.records[r.Fence.Token]
	if r.Preempted {
		p.mu.Unlock()
		return r.Fence, errRadioUnknown
	}
	r.Native = native
	r.Stage = "sender-reserve-attempted"
	e = p.storeLocked(r)
	p.mu.Unlock()
	if e != nil {
		return r.Fence, e
	}
	var sender radioProducerHold
	if sourceOperation && r.Request.Prior != nil {
		sender = r.Request.Prior.Sender
	} else {
		sender, e = p.sender.Reserve(ctx, r.Request, native)
	}
	if e != nil {
		return r.Fence, errRadioUnknown
	}
	if !producerSourceHoldMatches(sender, to, expectedSender) || sender.OwnerGeneration == 0 || sender.SourceGeneration == 0 {
		return r.Fence, errRadioUnknown
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	r = p.records[r.Fence.Token]
	if r.Preempted {
		return r.Fence, errRadioUnknown
	}
	r.Sender = sender
	r.Fence.OwnerGeneration = sender.SourceGeneration
	r.Stage = "reserved"
	if e = p.storeLocked(r); e != nil {
		return r.Fence, e
	}
	p.retained[to] = radioProducerPrior{Native: native, Sender: sender, Target: to}
	return r.Fence, nil
}

func producerHoldMatches(h radioProducerHold, want radioProducerIdentity, process string) bool {
	return h.Process == process && h.Handoff == want.Handoff && h.Generation == want.Generation && h.ActionID == want.ActionID && h.Target == want.Target && h.Token != "" && len(h.Proof) > 0 && json.Valid(h.Proof)
}
func (p *radioProducer) admit(f radioFence, stage string) (radioProducerRecord, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	r, ok := p.records[f.Token]
	allowed := map[string][]string{"pause-suspend-attempted": {"reserved", "stopped"}, "start-attempted": {"suspended"}, "stop-attempted": {"reserved"}, "volume-attempted": {"reserved"}, "finish-attempted": {"started", "stopped", "volume-observed"}}
	permitted := false
	for _, previous := range allowed[stage] {
		permitted = permitted || r.Stage == previous
	}
	if !ok || r.Fence != f || r.Preempted || r.Terminal || !permitted {
		return r, errRadioUnknown
	}
	r.Stage = stage
	return r, p.storeLocked(r)
}
func (p *radioProducer) PauseAndRelease(ctx context.Context, f radioFence, target string) error {
	r, e := p.admit(f, "pause-suspend-attempted")
	if e != nil || r.Request.Target != target {
		return errRadioUnknown
	}
	if r.Request.Retained != nil && r.Request.Retained.Native.Handoff == r.Native.Handoff || r.Request.Prior != nil && r.Request.Prior.Target == target && r.Request.Prior.Native.Handoff == r.Native.Handoff {
		// Station changes retain the already suspended source. They never
		// pause Spotify again or create a replacement Native hold.
		if p.retainedResult(ctx, r, r.Native) != nil {
			return errRadioUnknown
		}
	} else {
		if verifier, ok := p.sender.(interface {
			ValidatePrepared(context.Context, radioProducerHold) error
		}); ok {
			if e = verifier.ValidatePrepared(ctx, r.Sender); e != nil {
				return e
			}
		}
		if e = p.native.PauseSuspend(ctx, r.Native, r.Request.radioProducerIdentity); e != nil {
			return e
		}
	}
	return p.advance(f, "pause-suspend-attempted", "suspended")
}
func (p *radioProducer) Start(ctx context.Context, f radioFence, station, target string) error {
	r, e := p.admit(f, "start-attempted")
	if e != nil || r.Request.Target != target {
		return errRadioUnknown
	}
	if _, ok := radioStreamURLs[station]; !ok {
		return errRadioUnavailable
	}
	if e = p.sender.Start(ctx, r.Sender, r.Native, producerSenderOperation(r), station, target); e != nil {
		return e
	}
	return p.advance(f, "start-attempted", "started")
}
func (p *radioProducer) Stop(ctx context.Context, f radioFence, target string) error {
	r, e := p.admit(f, "stop-attempted")
	if e != nil {
		return e
	}
	// A station change/move stops only the captured incumbent, never a guessed
	// current sender. On initial STOP the reserve carries that incumbent as Prior.
	hold := r.Sender
	if r.Request.Prior != nil {
		hold = r.Request.Prior.Sender
	}
	if hold.Handoff == "" || target == "" {
		return errRadioUnknown
	}
	ticket, e := p.sender.StopDrain(ctx, hold, producerSenderOperation(r), target)
	if e != nil || len(ticket) == 0 || !json.Valid(ticket) {
		return errRadioUnknown
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	current := p.records[f.Token]
	if current.Preempted {
		return errRadioUnknown
	}
	if current.Fence != f || current.Stage != "stop-attempted" {
		return errRadioUnknown
	}
	current.DrainTicket = append(json.RawMessage{}, ticket...)
	current.Stage = "stopped"
	if current.Request.Prior != nil {
		priorID := current.Request.Prior.Sender.Handoff
		prior, ok := p.records[priorID]
		if ok {
			prior.SourceDrained = true
			prior.DrainTicket = append(json.RawMessage{}, ticket...)
			if e := p.storeLocked(prior); e != nil {
				return e
			}
		}
	}
	return p.storeLocked(current)
}
func (p *radioProducer) Volume(ctx context.Context, f radioFence, target string, percent int) error {
	r, e := p.admit(f, "volume-attempted")
	if e != nil || percent < 0 || percent > 100 {
		return errRadioUnknown
	}
	if e = p.sender.Volume(ctx, r.Sender, producerSenderOperation(r), target, percent); e != nil {
		return e
	}
	return p.advance(f, "volume-attempted", "volume-observed")
}
func (p *radioProducer) Finish(ctx context.Context, f radioFence) (radioNativeObservation, error) {
	r, e := p.admit(f, "finish-attempted")
	if e != nil {
		return radioNativeObservation{}, e
	}
	result, e := p.sender.Finish(ctx, r.Sender, producerSenderOperation(r))
	if e != nil || result.Outcome != "committed" || !producerResultMatches(result, r.Sender, producerSenderOperation(r)) {
		return radioNativeObservation{}, errRadioUnknown
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	r = p.records[f.Token]
	if r.Preempted {
		return radioNativeObservation{}, errRadioUnknown
	}
	r.Terminal = true
	r.Outcome = result.Outcome
	r.Observation = result.Observation
	r.Stage = "terminal"
	if e = p.storeLocked(r); e != nil {
		return radioNativeObservation{}, e
	}
	if result.Observation.SourceKind == "radio" && result.Observation.State == "playing" {
		p.active = f.Token
	} else {
		p.active = ""
	}
	return result.Observation, nil
}
func producerResultMatches(result radioProducerResult, hold radioProducerHold, operation radioProducerIdentity) bool {
	return result.Identity == operation && result.Observation.OwnerGeneration == hold.SourceGeneration && result.Terminal && (result.Outcome == "committed" || result.Outcome == "rejected" || result.Outcome == "preempted") && validRadioObservation(result.Observation)
}
func (p *radioProducer) Observe(ctx context.Context) (radioNativeObservation, error) {
	p.mu.Lock()
	evidence := p.evidence
	p.mu.Unlock()
	_ = p.completedNativeCustody(ctx, evidence)
	return p.sender.Observe(ctx)
}
func (p *radioProducer) Reconcile(ctx context.Context, f radioFence) (radioReconciliation, error) {
	p.mu.Lock()
	r, ok := p.records[f.Token]
	if !ok && f.Token == "" {
		for _, candidate := range p.records {
			if candidate.Fence.ActionID == f.ActionID && candidate.Fence.Generation == f.Generation {
				r = candidate
				ok = true
				break
			}
		}
	}
	p.mu.Unlock()
	if !ok || r.Fence.ActionID != f.ActionID || r.Fence.Generation != f.Generation {
		return radioReconciliation{}, errRadioUnknown
	}
	// A no-effect operation receipt is not a source observation. Only an actual
	// fresh unaffected sender source can settle a late unknown rejection.
	if len(r.NativeRejection) > 0 || r.Native.Handoff == "" && strings.HasPrefix(r.Stage, "preflight-") {
		return p.reconcileRejected(ctx, r, f)
	}
	// Native and sender reads cannot issue, repeat, repair or reacquire an effect.
	native, e := p.native.Result(ctx, r.Request, r.Native)
	var rejected *radioProducerOperationRejected
	if errors.As(e, &rejected) {
		p.mu.Lock()
		current := p.records[r.Fence.Token]
		if current.Fence != r.Fence || current.Preempted {
			p.mu.Unlock()
			return radioReconciliation{}, errRadioUnknown
		}
		current.NativeRejection = append(json.RawMessage{}, rejected.Receipt...)
		current.Stage = "operation-rejection-observed"
		if err := p.storeLocked(current); err != nil {
			p.mu.Unlock()
			return radioReconciliation{}, err
		}
		r = current
		p.mu.Unlock()
		return p.reconcileRejected(ctx, r, f)
	}
	if e != nil || !native.Terminal || native.Identity != producerNativeResultIdentity(r) {
		return radioReconciliation{}, errRadioUnknown
	}
	if r.Preempted {
		if native.Outcome != "preempted" || len(r.DrainTicket) == 0 || !producerJSONEqual(native.DrainTicket, r.DrainTicket) {
			return radioReconciliation{}, errRadioUnknown
		}
		if len(native.ReleaseReceipt) > 0 {
			updated := r
			updated.NativeRelease = append(json.RawMessage{}, native.ReleaseReceipt...)
			if !validProducerNativeRelease(updated) {
				return radioReconciliation{}, errRadioUnknown
			}
			p.mu.Lock()
			current := p.records[r.Fence.Token]
			if current.Fence != r.Fence || !current.Preempted || !producerJSONEqual(current.DrainTicket, r.DrainTicket) {
				p.mu.Unlock()
				return radioReconciliation{}, errRadioUnknown
			}
			current.NativeRelease = updated.NativeRelease
			err := p.storeLocked(current)
			p.mu.Unlock()
			if err != nil {
				return radioReconciliation{}, err
			}
		}
		observer, ok := p.sender.(interface {
			PreemptObservation(context.Context, radioProducerRecord) (radioNativeObservation, error)
		})
		if !ok {
			return radioReconciliation{}, errRadioUnknown
		}
		observation, err := observer.PreemptObservation(ctx, r)
		if err != nil || !validRadioObservation(observation) {
			return radioReconciliation{}, errRadioUnknown
		}
		return radioReconciliation{ActionID: f.ActionID, Generation: f.Generation, Token: r.Fence.Token, Terminal: true, Outcome: "preempted", Observation: observation}, nil
	}
	sender, e := p.sender.Result(ctx, producerSenderOperation(r))
	if e != nil || !producerResultMatches(sender, r.Sender, producerSenderOperation(r)) {
		return radioReconciliation{}, errRadioUnknown
	}
	p.mu.Lock()
	current := p.records[r.Fence.Token]
	if p.live[r.Fence.Token] && !current.Preempted && current.Fence == r.Fence && p.evidence.NativeProcess == r.Native.Process && p.evidence.SenderProcess == r.Sender.Process {
		current.Terminal = true
		current.Outcome = sender.Outcome
		current.Observation = sender.Observation
		current.Stage = "reconciled"
		if e := p.storeLocked(current); e != nil {
			p.mu.Unlock()
			return radioReconciliation{}, e
		}
		if sender.Outcome == "committed" && sender.Observation.SourceKind == "radio" && sender.Observation.State == "playing" {
			p.active = r.Fence.Token
		} else if p.active == r.Fence.Token || r.Request.Prior != nil && p.active == r.Request.Prior.Sender.Handoff {
			p.active = ""
		}
	}
	p.mu.Unlock()
	return radioReconciliation{ActionID: f.ActionID, Generation: f.Generation, Token: r.Fence.Token, Terminal: true, Outcome: sender.Outcome, Observation: sender.Observation}, nil
}
func (p *radioProducer) storeLocked(r radioProducerRecord) error {
	if r.Controller == "" {
		r.Controller = p.evidence.Controller
	}
	if _, exists := p.records[r.Fence.Token]; !exists && len(p.records) >= 4096 {
		return errRadioUnknown
	}
	if e := p.journal.write(r); e != nil {
		return errors.Join(errRadioUnknown, e)
	}
	p.records[r.Fence.Token] = r
	return nil
}

func (p *radioProducer) advance(f radioFence, from, to string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	r, ok := p.records[f.Token]
	if !ok || r.Fence != f || r.Preempted || r.Stage != from {
		return errRadioUnknown
	}
	r.Stage = to
	return p.storeLocked(r)
}

// Source identity is immutable across operations. Operation identity is fresh
// for every user command and must never be used as a replacement source lease.
func producerSenderOperation(r radioProducerRecord) radioProducerIdentity {
	o := r.Request.radioProducerIdentity
	o.Process = r.Sender.Process
	o.OwnerGeneration = r.Sender.OwnerGeneration
	return o
}
func producerSourceHoldMatches(h radioProducerHold, target, process string) bool {
	return h.Process == process && h.Target == target && h.Handoff != "" && h.Generation > 0 && h.ActionID != "" && h.Token != "" && len(h.Proof) > 0 && json.Valid(h.Proof)
}

func (p *radioProducer) recoverPassive(ctx context.Context) {
	p.mu.Lock()
	pending := []radioProducerRecord{}
	for _, r := range p.records {
		if !r.Terminal {
			pending = append(pending, r)
		}
	}
	p.mu.Unlock()
	for _, r := range pending {
		// PREPARE has no receiver effect; only its exact C operation result can
		// establish termination. It can never become an implicit Play.
		if r.Stage == "preflight-attempted" || r.Stage == "preflight-prepared" {
			result, e := p.sender.Result(ctx, r.Request.Peer.Operation)
			if e != nil || result.Identity != r.Request.Peer.Operation || !result.Terminal || result.Outcome != "rejected" {
				continue
			}
			p.mu.Lock()
			current := p.records[r.Fence.Token]
			if current.Stage == r.Stage && !current.Terminal {
				current.Terminal = true
				current.Outcome = "rejected"
				current.Stage = "preflight-reconciled"
				_ = p.storeLocked(current)
			}
			p.mu.Unlock()
			continue
		}
		result, e := p.Reconcile(ctx, r.Fence)
		if e != nil {
			continue
		}
		p.mu.Lock()
		current := p.records[r.Fence.Token]
		if !current.Terminal && current.Fence == r.Fence && current.Stage == r.Stage {
			current.Terminal = true
			current.Outcome = result.Outcome
			current.Observation = result.Observation
			current.Stage = "reconciled"
			_ = p.storeLocked(current)
		}
		p.mu.Unlock()
	}
}

func producerNativeResultIdentity(r radioProducerRecord) radioProducerIdentity {
	if r.Native.Handoff != "" {
		return r.Native.radioProducerIdentity
	}
	return r.Request.radioProducerIdentity
}

// Preserve only the monotone operation counter across clean manager restarts.
// No source, hold, selection or old action is restored by this floor.
func (p *radioProducer) RadioGenerationFloor() uint64 {
	p.mu.Lock()
	defer p.mu.Unlock()
	var floor uint64
	for _, record := range p.records {
		if record.Fence.Generation > floor {
			floor = record.Fence.Generation
		}
	}
	return floor
}
