package main

// Concrete independent-C sender protocol. Every endpoint is source-specific;
// stock OwnTone player/output controls are deliberately absent.
import (
	"context"
	"encoding/json"
	"strconv"
	"sync"
	"time"
)

type radioProducerTargetAuth struct {
	Schema      string `json:"schema"`
	Device      string `json:"device_id"`
	Incarnation string `json:"device_incarnation"`
	Route       string `json:"route_revision"`
	Source      string `json:"source_generation"`
	Controller  string `json:"controller_id"`
	Digest      string `json:"receiver_key_digest"`
	ObservedAt  string `json:"observed_at"`
	ValidUntil  string `json:"valid_until"`
}
type radioProducerSenderReply struct {
	TargetAuthenticated bool                     `json:"target_authenticated"`
	TargetAuth          *radioProducerTargetAuth `json:"target_auth,omitempty"`
	SourceGeneration    string                   `json:"source_generation"`
	PairProbeSupported  bool                     `json:"preflight_pair_verify_supported,omitempty"`
	ControllerID        string                   `json:"controller_id"`
	Schema              string                   `json:"schema"`
	OK                  bool                     `json:"ok"`
	Process             string                   `json:"process"`
	PID                 string                   `json:"sender_pid"`
	ObservedAt          string                   `json:"observed_at"`
	ValidUntil          string                   `json:"valid_until"`
	Device              string                   `json:"device_id"`
	Epoch               string                   `json:"radio_owner_epoch"`
	Generation          string                   `json:"operation_generation"`
	DrainGeneration     string                   `json:"drain_generation"`
	Operation           string                   `json:"operation_id"`
	Token               string                   `json:"sender_token"`
	Digest              string                   `json:"receiver_key_digest"`
	Station             string                   `json:"station_id"`
	Volume              int                      `json:"volume_percent"`
	VolumePending       bool                     `json:"volume_pending"`
	VolumeAccepted      bool                     `json:"volume_protocol_accepted"`
	Phase               string                   `json:"phase"`
	Prepared            bool                     `json:"source_prepared"`
	Observed            bool                     `json:"observed"`
	FinalVolumeKind     string                   `json:"final_volume_kind,omitempty"`
	FinalVolumePercent  int                      `json:"final_volume_percent,omitempty"`
	FinalVolumeRevision string                   `json:"final_volume_revision,omitempty"`
	Signature           string                   `json:"drain_signature,omitempty"`
	Challenge           string                   `json:"reacquire_challenge,omitempty"`
	PrepareFailed       bool                     `json:"prepare_failed"`
	Expired             bool                     `json:"candidate_expired"`
	IdleVerified        bool                     `json:"source_idle_verified,omitempty"`
	Cancelled           bool                     `json:"cancelled,omitempty"`
	Physical            bool                     `json:"physical_result_verified"`
	Targets             []struct {
		Device string `json:"device_id"`
		Digest string `json:"receiver_key_digest"`
	} `json:"paired_targets,omitempty"`
}
type radioProducerSenderEnvelope struct {
	Request    map[string]any           `json:"request"`
	Native     radioProducerHold        `json:"native"`
	TargetAuth *radioProducerTargetAuth `json:"target_auth,omitempty"`
}
type radioProducerSenderClient struct {
	http       *radioProducerHTTP
	native     *radioProducerNativeClient
	supervisor *radioSender
	mu         sync.Mutex
	controller string
	process    string
	targets    map[string]string
	peers      map[string]radioProducerPeer
	holds      map[string]radioProducerHold
	latest     radioProducerIdentity
	admitted   map[string]radioProducerIdentity
	owned      map[string]radioSenderOwnership
}

func newRadioProducerSenderClient(base, token string, native *radioProducerNativeClient, supervisor *radioSender) (*radioProducerSenderClient, error) {
	h, e := newRadioProducerHTTP(base, token)
	if e != nil || native == nil || supervisor == nil {
		return nil, errRadioUnavailable
	}
	h.basicPassword = token
	return &radioProducerSenderClient{http: h, native: native, supervisor: supervisor, targets: map[string]string{}, peers: map[string]radioProducerPeer{}, holds: map[string]radioProducerHold{}, admitted: map[string]radioProducerIdentity{}, owned: map[string]radioSenderOwnership{}}, nil
}
func (s *radioProducerSenderClient) call(ctx context.Context, action string, q map[string]any) (radioProducerSenderReply, error) {
	var r radioProducerSenderReply
	if e := s.http.call(ctx, "/api/radio-sender/"+action, q, &r); e != nil {
		return r, e
	}
	pid, e := strconv.Atoi(r.PID)
	if e != nil || s.supervisor.productionEvidence(pid) != nil {
		return r, errRadioUnavailable
	}
	at, e := time.Parse(time.RFC3339Nano, r.ObservedAt)
	if e != nil {
		return r, errRadioUnknown
	}
	until, e := time.Parse(time.RFC3339Nano, r.ValidUntil)
	now := time.Now()
	if e != nil || at.After(now) || at.Before(now.Add(-30*time.Second)) || !until.After(now) || until.Before(at) || until.After(at.Add(30*time.Second)) || r.Schema != "podconnect.radio-sender.v1" || !r.OK || !producerUUID(r.Process) || r.Physical || !producerCounter(r.PID, false) || !producerCounter(r.Device, true) || !producerCounter(r.Epoch, true) || !producerCounter(r.Generation, true) || !producerCounter(r.DrainGeneration, true) || !producerCounter(r.SourceGeneration, true) {
		return r, errRadioUnknown
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.process != "" && s.process != r.Process {
		return r, errRadioUnknown
	}
	seenDevices, seenKeys := map[string]bool{}, map[string]bool{}
	for _, target := range r.Targets {
		if !producerCounter(target.Device, false) || !producerHex(target.Digest, 64) || seenDevices[target.Device] || seenKeys[target.Digest] {
			return r, errRadioUnknown
		}
		seenDevices[target.Device] = true
		seenKeys[target.Digest] = true
	}
	if action != "capabilities" {
		if !producerSenderOperationMatches(r, q) {
			return r, errRadioUnknown
		}
	}
	return r, nil
}
func producerSenderOperationMatches(r radioProducerSenderReply, q map[string]any) bool {
	if !producerUUID(r.Operation) || !producerHex(r.Token, 32) || !producerHex(r.Digest, 64) || !producerCounter(r.Device, false) || !producerCounter(r.Epoch, false) || !producerCounter(r.Generation, false) {
		return false
	}
	if r.Device != q["device_id"] || r.Operation != q["operation_id"] || r.Generation != q["operation_generation"] {
		return false
	}
	if expected, ok := q["radio_process"]; ok && r.Process != expected {
		return false
	}
	if expected, ok := q["radio_owner_epoch"]; ok && r.Epoch != expected {
		return false
	}
	if expected, ok := q["sender_token"]; ok && r.Token != expected {
		return false
	}
	return true
}

func (s *radioProducerSenderClient) Evidence(ctx context.Context, n radioProducerEvidence) (radioProducerEvidence, error) {
	r, e := s.call(ctx, "capabilities", map[string]any{"schema": "podconnect.radio-sender.v1"})
	if e != nil {
		return radioProducerEvidence{}, e
	}
	s.native.mu.Lock()
	state := s.native.state
	s.native.mu.Unlock()
	if state.ProcessID != n.NativeProcess || r.Process == n.NativeProcess || !producerControllerID(state.ControllerID) || r.ControllerID != state.ControllerID {
		return radioProducerEvidence{}, errRadioUnknown
	}
	mapping := map[string]string{}
	bindings := map[string]radioProducerTargetBinding{}
	targets := []radioTarget{}
	logicalByPhysical := map[string]string{}
	joined := 0
	for _, t := range state.Targets {
		mapping[t.ID] = t.HomePodID
		bindings[t.ID] = radioProducerTargetBinding{Device: t.HomePodID}
		projected := t.radioTarget
		matched := false
		for _, p := range r.Targets {
			if p.Device == t.HomePodID && len(p.Digest) == 64 {
				if previous, ok := logicalByPhysical[p.Digest]; ok && previous != t.ID {
					return radioProducerEvidence{}, errRadioUnknown
				}
				logicalByPhysical[p.Digest] = t.ID
				mapping[t.ID] = p.Device
				bindings[t.ID] = radioProducerTargetBinding{Device: p.Device, Digest: p.Digest}
				matched = true
				joined++
				break
			}
		}
		if !matched || !projected.Selectable || projected.Availability != "available" {
			projected.Selectable = false
			projected.Availability = "unavailable"
		}
		targets = append(targets, projected)
	}
	s.mu.Lock()
	s.process = r.Process
	s.controller = r.ControllerID
	s.targets = mapping
	s.mu.Unlock()
	if joined == 0 {
		if r.IdleVerified && r.PairProbeSupported {
			return radioProducerEvidence{}, errRadioSenderDiscoveryPending
		}
		return radioProducerEvidence{}, errRadioUnavailable
	}
	missing := []string{}
	if !r.PairProbeSupported {
		missing = append(missing, "pre_pause_receiver_pair_authentication")
	}
	return radioProducerEvidence{NativeProcess: n.NativeProcess, SenderProcess: r.Process, Targets: targets, Bindings: bindings, Controller: r.ControllerID, Missing: missing}, nil
}
func (s *radioProducerSenderClient) device(target string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	d := s.targets[target]
	if d == "" {
		return "", errRadioUnavailable
	}
	return d, nil
}
func producerSenderBase(device string, op radioProducerIdentity) map[string]any {
	return map[string]any{"schema": "podconnect.radio-sender.v1", "device_id": device, "operation_id": op.Handoff, "operation_generation": strconv.FormatUint(op.Generation, 10)}
}
func (s *radioProducerSenderClient) Preflight(ctx context.Context, station, target string, op radioProducerIdentity) (radioProducerPeer, error) {
	if _, ok := radioStreamURLs[station]; !ok {
		return radioProducerPeer{}, errRadioUnavailable
	}
	d, e := s.device(target)
	if e != nil {
		return radioProducerPeer{}, e
	}
	q := producerSenderBase(d, op)
	q["station_id"] = station
	s.mu.Lock()
	old, has := s.holds[target]
	s.mu.Unlock()
	if has {
		env, e := senderProducerEnvelope(old)
		if e != nil {
			return radioProducerPeer{}, e
		}
		native, e := nativeProducerEnvelope(env.Native)
		if e != nil {
			return radioProducerPeer{}, e
		}
		q["hold"] = native.Binding
	}
	r, e := s.call(ctx, "preflight", q)
	if e != nil {
		return radioProducerPeer{}, e
	}
	epoch, e := strconv.ParseUint(r.Epoch, 10, 64)
	if e != nil || epoch == 0 || len(r.Token) != 32 || len(r.Digest) != 64 {
		return radioProducerPeer{}, errRadioUnknown
	}
	state := producerSenderBase(d, op)
	state["radio_process"] = r.Process
	state["radio_owner_epoch"] = r.Epoch
	state["sender_token"] = r.Token
	deadline, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	for r.Phase != "prepared" {
		if r.Phase == "cancelled" && r.Cancelled {
			return radioProducerPeer{}, errRadioUnavailable
		}
		if r.Phase != "preparing" {
			return radioProducerPeer{}, errRadioUnknown
		}
		timer := time.NewTimer(time.Second)
		select {
		case <-deadline.Done():
			timer.Stop()
			return radioProducerPeer{}, errRadioUnknown
		case <-timer.C:
		}
		r, e = s.call(deadline, "state", state)
		if e != nil {
			return radioProducerPeer{}, e
		}
	}
	if !r.Prepared || !s.targetAuthenticated(r, time.Now()) {
		return radioProducerPeer{}, errRadioUnknown
	}
	proof, _ := json.Marshal(state)
	sourceGeneration, _ := strconv.ParseUint(r.SourceGeneration, 10, 64)
	peer := radioProducerPeer{SourceGeneration: sourceGeneration, Operation: op, Process: r.Process, OwnerGeneration: epoch, Target: target, ReceiverDigest: r.Digest, CandidateID: op.Handoff, Proof: proof, TargetAuth: r.TargetAuth}
	s.mu.Lock()
	s.peers[op.Handoff] = peer
	s.mu.Unlock()
	return peer, nil
}
func senderProducerEnvelope(h radioProducerHold) (radioProducerSenderEnvelope, error) {
	var e radioProducerSenderEnvelope
	if json.Unmarshal(h.Proof, &e) != nil || e.Request == nil || e.Native.Handoff == "" {
		return e, errRadioUnknown
	}
	return e, nil
}
func senderProducerReserveBody(q radioProducerReserve, n radioProducerHold) (map[string]any, error) {
	env, e := nativeProducerEnvelope(n)
	if e != nil {
		return nil, e
	}
	var body map[string]any
	if json.Unmarshal(q.Peer.Proof, &body) != nil || body == nil {
		return nil, errRadioUnknown
	}
	// Every typed C proof variant remains opaque here; only its actual owner
	// may validate or reduce it. In particular parked child custody does not
	// depend on the manager re-querying its historical parent.
	body["hold"] = env.Binding
	return body, nil
}
func (s *radioProducerSenderClient) Reserve(ctx context.Context, q radioProducerReserve, n radioProducerHold) (radioProducerHold, error) {
	body, e := senderProducerReserveBody(q, n)
	if e != nil {
		return radioProducerHold{}, e
	}
	r, e := s.call(ctx, "reserve", body)
	if e != nil || r.Phase != "bound" || !s.targetAuthenticated(r, time.Now()) || !producerSameTargetAuth(q.Peer.TargetAuth, r.TargetAuth) || r.Digest != q.Peer.ReceiverDigest || r.SourceGeneration != strconv.FormatUint(q.Peer.SourceGeneration, 10) {
		return radioProducerHold{}, errRadioUnknown
	}
	proof, _ := json.Marshal(radioProducerSenderEnvelope{Request: body, Native: n, TargetAuth: q.Peer.TargetAuth})
	id := q.Peer.Operation
	id.Process = r.Process
	id.OwnerGeneration = q.Peer.OwnerGeneration
	h := radioProducerHold{radioProducerIdentity: id, SourceGeneration: q.Peer.SourceGeneration, Token: r.Token, Proof: proof}
	s.mu.Lock()
	s.holds[q.Target] = h
	s.mu.Unlock()
	return h, nil
}
func senderOperationBody(h radioProducerHold, op radioProducerIdentity) (map[string]any, error) {
	env, e := senderProducerEnvelope(h)
	if e != nil {
		return nil, e
	}
	body := env.Request
	body["operation_id"] = op.Handoff
	body["operation_generation"] = strconv.FormatUint(op.Generation, 10)
	return body, nil
}
func (s *radioProducerSenderClient) Start(ctx context.Context, h, n radioProducerHold, op radioProducerIdentity, station, target string) error {
	suspended, e := s.native.Suspended(ctx, n)
	signature, e := producerSenderStartSignature(suspended, e, func(c *radioProducerNativeCustody) (string, error) { return s.ValidateRetainedTarget(ctx, n, c) })
	if e != nil {
		return e
	}
	q, e := senderOperationBody(h, op)
	if e != nil {
		return e
	}
	q["suspended_signature"] = signature
	s.mu.Lock()
	s.admitted[op.Handoff] = op
	s.mu.Unlock()
	r, e := s.call(ctx, "start", q)
	if e != nil {
		return e
	}
	if r.Station != station || r.SourceGeneration != strconv.FormatUint(h.SourceGeneration, 10) || (r.Phase != "starting" && r.Phase != "playing") {
		return errRadioUnknown
	}
	s.mu.Lock()
	s.latest = op
	s.mu.Unlock()
	return nil
}
func (s *radioProducerSenderClient) StopDrain(ctx context.Context, h radioProducerHold, op radioProducerIdentity, target string) (json.RawMessage, error) {
	if h.Target != target {
		return nil, errRadioUnknown
	}
	q, e := senderOperationBody(h, op)
	if e != nil {
		return nil, e
	}
	s.mu.Lock()
	s.admitted[op.Handoff] = op
	s.mu.Unlock()
	r, e := s.call(ctx, "stop", q)
	if e != nil {
		return nil, e
	}
	r, e = s.wait(ctx, q, r, "drained")
	if e != nil {
		return nil, e
	}
	s.rememberOwned(h, op, r)
	s.mu.Lock()
	s.latest = op
	s.mu.Unlock()
	return json.Marshal(r)
}
func (s *radioProducerSenderClient) PreemptDrain(ctx context.Context, h, n radioProducerHold, request radioNativePreemptRequest) (json.RawMessage, error) {
	var challenge struct {
		Nonce     string `json:"challenge"`
		Signature string `json:"challenge_signature"`
	}
	if json.Unmarshal(request.Challenge, &challenge) != nil || !producerHex(challenge.Nonce, 32) || !producerHex(challenge.Signature, 64) {
		return nil, errRadioUnknown
	}
	q, e := senderOperationBody(h, h.radioProducerIdentity)
	if e != nil {
		// The candidate is not bound yet; its exact preflight receipt and the
		// passively recovered Native hold are sufficient only for challenged cancel.
		if json.Unmarshal(h.Proof, &q) != nil || q == nil {
			return nil, errRadioUnknown
		}
		env, e := nativeProducerEnvelope(n)
		if e != nil {
			return nil, e
		}
		q["hold"] = env.Binding
	}
	q["operation_id"] = request.ActionID
	q["operation_generation"] = request.Generation
	q["reacquire_challenge"] = challenge.Nonce
	q["challenge_signature"] = challenge.Signature
	r, e := s.call(ctx, "stop", q)
	if e != nil {
		return nil, e
	}
	r, e = s.wait(ctx, q, r, "drained")
	if e != nil {
		return nil, e
	}
	if r.Challenge != challenge.Nonce || !producerHex(r.Signature, 64) || !producerCounter(r.DrainGeneration, false) || r.FinalVolumePercent < 0 || r.FinalVolumePercent > 100 || !producerCounter(r.FinalVolumeRevision, true) {
		return nil, errRadioUnknown
	}
	if r.FinalVolumeKind != "receiver_acknowledged" && r.FinalVolumeKind != "unchanged_source_base" || r.FinalVolumeKind == "receiver_acknowledged" && r.FinalVolumeRevision == "0" || r.FinalVolumeKind == "unchanged_source_base" && r.FinalVolumeRevision != "0" {
		return nil, errRadioUnknown
	}
	// The exact native/source lease has now been relinquished. A future radio
	// intent on this target must preflight without the retired Native hold.
	s.mu.Lock()
	delete(s.holds, request.Target)
	delete(s.owned, n.Handoff)
	s.mu.Unlock()
	// Native consumes only the actual source signature and fresh nonce. The
	// manager never signs or synthesizes a Spotify reacquire authorization.
	return json.Marshal(map[string]any{"drain_generation": r.DrainGeneration, "drain_signature": r.Signature, "reacquire_challenge": r.Challenge, "final_volume_kind": r.FinalVolumeKind, "final_volume_percent": r.FinalVolumePercent, "final_volume_revision": r.FinalVolumeRevision})
}
func producerHex(s string, n int) bool {
	if len(s) != n {
		return false
	}
	for _, c := range s {
		if c < '0' || c > '9' && c < 'a' || c > 'f' {
			return false
		}
	}
	return true
}
func (s *radioProducerSenderClient) Volume(ctx context.Context, h radioProducerHold, op radioProducerIdentity, target string, v int) error {
	if h.Target != target || v < 0 || v > 100 {
		return errRadioUnknown
	}
	q, e := senderOperationBody(h, op)
	if e != nil {
		return e
	}
	q["volume_percent"] = v
	s.mu.Lock()
	s.admitted[op.Handoff] = op
	s.mu.Unlock()
	r, e := s.call(ctx, "volume", q)
	if e != nil {
		return e
	}
	s.mu.Lock()
	s.latest = op
	s.mu.Unlock()
	state := map[string]any{}
	for _, key := range []string{"schema", "device_id", "operation_id", "operation_generation", "radio_process", "radio_owner_epoch", "sender_token"} {
		state[key] = q[key]
	}
	for r.VolumePending {
		timer := time.NewTimer(time.Second)
		select {
		case <-ctx.Done():
			timer.Stop()
			return errRadioUnknown
		case <-timer.C:
		}
		r, e = s.call(ctx, "state", state)
		if e != nil {
			return e
		}
	}
	if !r.VolumeAccepted || r.Volume != v {
		return errRadioUnknown
	}
	s.mu.Lock()
	s.latest = op
	s.mu.Unlock()
	return nil
}
func (s *radioProducerSenderClient) wait(ctx context.Context, q map[string]any, r radioProducerSenderReply, phase string) (radioProducerSenderReply, error) {
	state := map[string]any{}
	for _, k := range []string{"schema", "device_id", "operation_id", "operation_generation", "radio_process", "radio_owner_epoch", "sender_token"} {
		state[k] = q[k]
	}
	for r.Phase != phase {
		if r.Phase == "unknown" || r.Phase == "drained" && phase != "drained" {
			return r, errRadioUnknown
		}
		timer := time.NewTimer(time.Second)
		select {
		case <-ctx.Done():
			timer.Stop()
			return r, errRadioUnknown
		case <-timer.C:
		}
		var e error
		r, e = s.call(ctx, "state", state)
		if e != nil {
			return r, e
		}
	}
	return r, nil
}
func (s *radioProducerSenderClient) result(ctx context.Context, op radioProducerIdentity) (radioProducerResult, error) {
	d, e := s.device(op.Target)
	if e != nil {
		return radioProducerResult{}, e
	}
	q := producerSenderBase(d, op)
	q["radio_process"] = op.Process
	r, e := s.call(ctx, "result", q)
	if e != nil {
		return radioProducerResult{}, e
	}
	epoch, e := strconv.ParseUint(r.Epoch, 10, 64)
	if e != nil || epoch == 0 || op.OwnerGeneration != 0 && epoch != op.OwnerGeneration {
		return radioProducerResult{}, errRadioUnknown
	}
	sourceGeneration, e := strconv.ParseUint(r.SourceGeneration, 10, 64)
	if e != nil || sourceGeneration == 0 {
		return radioProducerResult{}, errRadioUnknown
	}
	at, _ := time.Parse(time.RFC3339Nano, r.ObservedAt)
	until, _ := time.Parse(time.RFC3339Nano, r.ValidUntil)
	o := radioNativeObservation{State: "unknown", SourceKind: "unknown", OwnerGeneration: sourceGeneration, ObservedAt: at.Unix(), ValidUntil: until.Unix()}
	terminal := false
	if r.Phase == "playing" && r.Observed {
		o.State = "playing"
		o.SourceKind = "radio"
		o.StationID = r.Station
		o.TargetID = op.Target
		terminal = true
	}
	if r.Phase == "cancelled" && r.Cancelled {
		return radioProducerResult{Identity: op, Terminal: true, Outcome: "rejected"}, nil
	}
	if r.Phase == "drained" {
		o.State = "stopped"
		o.SourceKind = "none"
		terminal = true
	}
	if r.VolumeAccepted && !r.VolumePending && r.Volume >= 0 && r.Volume <= 100 {
		v := r.Volume
		o.VolumePercent = &v
	}
	if !validRadioObservation(o) {
		return radioProducerResult{}, errRadioUnknown
	}
	if terminal {
		s.mu.Lock()
		admitted, ok := s.admitted[op.Handoff]
		if ok && admitted == op && op.Generation >= s.latest.Generation {
			s.latest = op
		}
		hold, hasHold := s.holds[op.Target]
		s.mu.Unlock()
		if hasHold {
			s.rememberOwned(hold, op, r)
		}
	}
	return radioProducerResult{Identity: op, Terminal: terminal, Outcome: "committed", Observation: o}, nil
}
func (s *radioProducerSenderClient) Finish(ctx context.Context, h radioProducerHold, op radioProducerIdentity) (radioProducerResult, error) {
	q, e := senderOperationBody(h, op)
	if e != nil {
		return radioProducerResult{}, e
	}
	r, e := s.call(ctx, "state", func() map[string]any {
		m := map[string]any{}
		for _, k := range []string{"schema", "device_id", "operation_id", "operation_generation", "radio_process", "radio_owner_epoch", "sender_token"} {
			m[k] = q[k]
		}
		return m
	}())
	if e != nil {
		return radioProducerResult{}, e
	}
	if r.Phase == "starting" {
		if _, e = s.wait(ctx, q, r, "playing"); e != nil {
			return radioProducerResult{}, e
		}
	}
	return s.result(ctx, op)
}
func (s *radioProducerSenderClient) Result(ctx context.Context, op radioProducerIdentity) (radioProducerResult, error) {
	return s.result(ctx, op)
}
func (s *radioProducerSenderClient) Observe(ctx context.Context) (radioNativeObservation, error) {
	s.mu.Lock()
	op := s.latest
	s.mu.Unlock()
	if op.Handoff == "" {
		r, e := s.call(ctx, "capabilities", map[string]any{"schema": "podconnect.radio-sender.v1"})
		if e != nil || !r.IdleVerified {
			return radioNativeObservation{}, errRadioUnknown
		}
		at, _ := time.Parse(time.RFC3339Nano, r.ObservedAt)
		until, _ := time.Parse(time.RFC3339Nano, r.ValidUntil)
		sourceGeneration, e := strconv.ParseUint(r.SourceGeneration, 10, 64)
		if e != nil {
			return radioNativeObservation{}, errRadioUnknown
		}
		o := radioNativeObservation{State: "idle", SourceKind: "none", OwnerGeneration: sourceGeneration, ObservedAt: at.Unix(), ValidUntil: until.Unix()}
		if !validRadioObservation(o) {
			return radioNativeObservation{}, errRadioUnknown
		}
		return o, nil
	}
	r, e := s.result(ctx, op)
	return r.Observation, e
}

func producerCounter(s string, zero bool) bool {
	n, e := strconv.ParseUint(s, 10, 64)
	return e == nil && (zero || n > 0) && strconv.FormatUint(n, 10) == s
}
func producerUUID(s string) bool {
	if len(s) != 36 {
		return false
	}
	for i, c := range s {
		if i == 8 || i == 13 || i == 18 || i == 23 {
			if c != '-' {
				return false
			}
		} else if c < '0' || c > '9' && c < 'a' || c > 'f' {
			return false
		}
	}
	return true
}

func (s *radioProducerSenderClient) CancelCandidate(ctx context.Context, peer radioProducerPeer) (json.RawMessage, error) {
	var q map[string]any
	if json.Unmarshal(peer.Proof, &q) != nil || q == nil {
		return nil, errRadioUnknown
	}
	r, e := s.call(ctx, "cancel-preflight", q)
	if e != nil {
		return nil, e
	}
	r, e = s.wait(ctx, q, r, "cancelled")
	if e != nil || !r.Cancelled {
		return nil, errRadioUnknown
	}
	return json.Marshal(r)
}

func producerControllerID(s string) bool {
	if len(s) != 16 || s == "0000000000000000" {
		return false
	}
	for _, c := range s {
		if c < '0' || c > '9' && c < 'A' || c > 'F' {
			return false
		}
	}
	return true
}

// PreemptObservation only reads the exact challenged command. Native's durable
// commit and a historical signed ticket do not by themselves prove today's
// sender source or grant authority to reconstruct a receiver session.
func (s *radioProducerSenderClient) PreemptObservation(ctx context.Context, record radioProducerRecord) (radioNativeObservation, error) {
	var q map[string]any
	h := record.Sender
	if h.Handoff != "" {
		q, _ = senderOperationBody(h, h.radioProducerIdentity)
	}
	if q == nil {
		if json.Unmarshal(record.Request.Peer.Proof, &q) != nil || q == nil {
			return radioNativeObservation{}, errRadioUnknown
		}
	}
	state := map[string]any{}
	for _, key := range []string{"schema", "device_id", "radio_process", "radio_owner_epoch", "sender_token"} {
		if q[key] == nil {
			return radioNativeObservation{}, errRadioUnknown
		}
		state[key] = q[key]
	}
	state["operation_id"] = record.PreemptAction
	state["operation_generation"] = strconv.FormatUint(record.Native.Generation, 10)
	reply, err := s.call(ctx, "state", state)
	if err != nil || reply.Phase != "drained" || !producerHex(reply.Signature, 64) {
		return radioNativeObservation{}, errRadioUnknown
	}
	ticket, err := json.Marshal(map[string]any{"drain_generation": reply.DrainGeneration, "drain_signature": reply.Signature, "reacquire_challenge": reply.Challenge, "final_volume_kind": reply.FinalVolumeKind, "final_volume_percent": reply.FinalVolumePercent, "final_volume_revision": reply.FinalVolumeRevision})
	if err != nil || !producerJSONEqual(ticket, record.DrainTicket) {
		return radioNativeObservation{}, errRadioUnknown
	}
	// A different target may now be playing radio; retain that actual winner.
	return s.Observe(ctx)
}

func producerTargetAuthenticated(r radioProducerSenderReply, controller string, now time.Time) bool {
	a := r.TargetAuth
	if !r.TargetAuthenticated || a == nil || a.Schema != "podconnect.radio-target-auth.v1" || a.Device != r.Device || a.Controller != controller || !producerControllerID(a.Controller) || r.ControllerID != "" && r.ControllerID != controller || a.Digest != r.Digest || !producerHex(a.Digest, 64) || a.Source != r.SourceGeneration || !producerCounter(a.Source, false) || !producerCounter(a.Incarnation, false) || !producerCounter(a.Route, true) {
		return false
	}
	at, e := time.Parse(time.RFC3339Nano, a.ObservedAt)
	if e != nil {
		return false
	}
	until, e := time.Parse(time.RFC3339Nano, a.ValidUntil)
	return e == nil && !at.After(now) && at.After(now.Add(-5*time.Second)) && until.After(now) && until.After(at) && !until.After(at.Add(5*time.Second))
}
func (s *radioProducerSenderClient) ValidatePrepared(ctx context.Context, h radioProducerHold) error {
	env, e := senderProducerEnvelope(h)
	if e != nil {
		return e
	}
	q := map[string]any{}
	for _, key := range []string{"schema", "device_id", "operation_id", "operation_generation", "radio_process", "radio_owner_epoch", "sender_token"} {
		q[key] = env.Request[key]
	}
	r, e := s.call(ctx, "state", q)
	if e != nil || r.Phase != "bound" || !r.Prepared || !s.targetAuthenticated(r, time.Now()) || !producerSameTargetAuth(env.TargetAuth, r.TargetAuth) || r.SourceGeneration != strconv.FormatUint(h.SourceGeneration, 10) {
		return errRadioUnknown
	}
	return nil
}

func (s *radioProducerSenderClient) targetAuthenticated(r radioProducerSenderReply, now time.Time) bool {
	s.mu.Lock()
	controller := s.controller
	process := s.process
	s.mu.Unlock()
	return r.Process == process && producerTargetAuthenticated(r, controller, now)
}

func producerSameTargetAuth(a, b *radioProducerTargetAuth) bool {
	return a != nil && b != nil && *a == *b
}
