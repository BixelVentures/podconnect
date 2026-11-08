package main

// Concrete Native transport. It speaks only the new authenticated handoff
// protocol; ordinary Spotify transport/release APIs are never a fallback.
import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"sync"
	"time"
)

type radioProducerNativePeer struct {
	Process string `json:"radio_process"`
	Epoch   string `json:"radio_owner_epoch"`
	Digest  string `json:"receiver_key_digest"`
}
type radioProducerNativeRequest struct {
	Schema           string                  `json:"schema"`
	Process          string                  `json:"process_id"`
	Target           string                  `json:"target_id"`
	Handoff          string                  `json:"handoff_id"`
	Generation       string                  `json:"generation"`
	Action           string                  `json:"action_id"`
	Peer             radioProducerNativePeer `json:"sender_peer"`
	Token            string                  `json:"token,omitempty"`
	ExpectedRevision string                  `json:"expected_native_revision"`
}
type radioProducerNativeReply struct {
	Custody *radioProducerNativeCustody `json:"-"`
	radioProducerNativeRequest
	Phase              string          `json:"phase"`
	Proof              json.RawMessage `json:"proof"`
	RequiresPause      bool            `json:"requires_native_pause"`
	ObservedAt         string          `json:"observed_at"`
	SuspendedSignature string          `json:"suspended_signature,omitempty"`
	TerminalDrain      json.RawMessage `json:"-"`
	TerminalRelease    json.RawMessage `json:"-"`
}
type radioProducerTarget struct {
	radioTarget
	HomePodID      string `json:"homepod_id"`
	ReceiverDigest string `json:"receiver_key_digest,omitempty"`
}
type radioProducerNativeState struct {
	Schema        string                `json:"schema"`
	ControllerID  string                `json:"controller_id"`
	ProcessID     string                `json:"process_id"`
	NativeProcess json.RawMessage       `json:"native_process"`
	Revision      string                `json:"native_revision"`
	Targets       []radioProducerTarget `json:"configured_targets"`
	Missing       []string              `json:"missing_proofs"`
}
type radioProducerHTTP struct {
	base, token   string
	basicPassword string
	client        *http.Client
}

func newRadioProducerHTTP(base, token string) (*radioProducerHTTP, error) {
	u, e := url.Parse(base)
	if e != nil || u.Scheme != "http" || (u.Hostname() != "127.0.0.1" && u.Hostname() != "localhost" && u.Hostname() != "::1") || u.Path != "" || u.RawQuery != "" || u.Fragment != "" || u.User != nil || token == "" {
		return nil, errRadioUnavailable
	}
	return &radioProducerHTTP{base: base, token: token, client: &http.Client{Timeout: 8 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return errRadioUnknown }}}, nil
}
func (p *radioProducerHTTP) call(ctx context.Context, path string, args any, out any) error {
	method := http.MethodPost
	var body []byte
	var e error
	if args == nil {
		method = http.MethodGet
	} else {
		body, e = json.Marshal(args)
		if e != nil {
			return errRadioUnavailable
		}
	}
	req, e := http.NewRequestWithContext(ctx, method, p.base+path, bytes.NewReader(body))
	if e != nil {
		return errRadioUnavailable
	}
	if p.basicPassword != "" {
		req.SetBasicAuth("admin", p.basicPassword)
	} else {
		req.Header.Set("X-PodConnect-Token", p.token)
	}
	req.Header.Set("Content-Type", "application/json")
	response, e := p.client.Do(req)
	if e != nil {
		return errRadioUnknown
	}
	defer response.Body.Close()
	// A peer's explicit conflict is not inferred from network/timeout errors.
	primaryRead := method == http.MethodGet && path == "/radio/primary"
	if primaryRead && response.StatusCode == http.StatusNotFound {
		return errRadioPrimaryUnsupported
	}
	if response.StatusCode != 200 {
		return errRadioUnknown
	}
	invalid := errRadioUnknown
	if primaryRead {
		invalid = errors.Join(invalid, errRadioPrimaryAdvertised)
	}
	raw, e := io.ReadAll(io.LimitReader(response.Body, 65537))
	if e != nil || len(raw) > 65536 {
		return invalid
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(out) != nil || decoder.Decode(new(any)) != io.EOF {
		return invalid
	}
	return nil
}

type radioProducerNativeClient struct {
	http                    *radioProducerHTTP
	mu                      sync.Mutex
	state                   radioProducerNativeState
	holds                   map[string]radioProducerNativeReply
	established             map[string]radioProducerNativeReply
	primaryBootstrapProcess string
}

func newRadioProducerNativeClient(base, token string) (*radioProducerNativeClient, error) {
	http, e := newRadioProducerHTTP(base, token)
	if e != nil {
		return nil, e
	}
	return &radioProducerNativeClient{http: http, holds: map[string]radioProducerNativeReply{}, established: map[string]radioProducerNativeReply{}}, nil
}
func (n *radioProducerNativeClient) readState(ctx context.Context) (radioProducerNativeState, error) {
	var s radioProducerNativeState
	if e := n.http.call(ctx, "/radio/handoff/state", nil, &s); e != nil {
		return s, e
	}
	if s.Schema != "podconnect.native_radio_handoff_state.v1" || len(s.ProcessID) != 64 || !json.Valid(s.NativeProcess) {
		return s, errRadioUnknown
	}
	compact := new(bytes.Buffer)
	if json.Compact(compact, s.NativeProcess) != nil {
		return s, errRadioUnknown
	}
	digest := sha256.Sum256(compact.Bytes())
	if hex.EncodeToString(digest[:]) != s.ProcessID {
		return s, errRadioUnknown
	}
	if _, e := strconv.ParseUint(s.Revision, 10, 64); e != nil {
		return s, errRadioUnknown
	}
	n.mu.Lock()
	if n.state.ProcessID != "" && n.state.ProcessID != s.ProcessID {
		n.mu.Unlock()
		return s, errRadioUnknown
	}
	n.state = s
	n.mu.Unlock()
	return s, nil
}
func (n *radioProducerNativeClient) Evidence(ctx context.Context) (radioProducerEvidence, error) {
	s, e := n.readState(ctx)
	if e != nil {
		return radioProducerEvidence{}, e
	}
	targets := make([]radioTarget, 0, len(s.Targets))
	seen := map[string]bool{}
	for _, target := range s.Targets {
		if target.ID == "" || seen[target.ID] {
			return radioProducerEvidence{}, errRadioUnknown
		}
		seen[target.ID] = true
		if !producerCounter(target.HomePodID, false) {
			target.Selectable = false
			target.Availability = "unknown"
		}
		targets = append(targets, target.radioTarget)
	}
	missing := append([]string{}, s.Missing...)
	if !producerControllerID(s.ControllerID) {
		missing = append(missing, "actual_registered_radio_controller_binding")
	}
	return radioProducerEvidence{NativeProcess: s.ProcessID, Targets: targets, Missing: missing}, nil
}
func (n *radioProducerNativeClient) CaptureIntent(ctx context.Context, target string) (radioProducerIntent, error) {
	s, e := n.readState(ctx)
	if e != nil {
		return radioProducerIntent{}, e
	}
	for _, known := range s.Targets {
		if known.ID == target {
			return radioProducerIntent{Process: s.ProcessID, Revision: s.Revision}, nil
		}
	}
	return radioProducerIntent{}, errRadioUnavailable
}
func nativeProducerWire(q radioProducerReserve) radioProducerNativeRequest {
	return radioProducerNativeRequest{Schema: "podconnect.native_radio_handoff.v1", Process: q.Process, Target: q.Target, Handoff: q.Handoff, Generation: strconv.FormatUint(q.Generation, 10), Action: q.Handoff, ExpectedRevision: q.ExpectedIntent.Revision, Peer: radioProducerNativePeer{Process: q.Peer.Process, Epoch: strconv.FormatUint(q.Peer.OwnerGeneration, 10), Digest: q.Peer.ReceiverDigest}}
}
func (n *radioProducerNativeClient) invoke(ctx context.Context, action string, q radioProducerNativeRequest) (radioProducerNativeReply, error) {
	var r radioProducerNativeReply
	var raw json.RawMessage
	if e := n.http.call(ctx, "/radio/handoff/"+action, q, &raw); e != nil {
		return r, e
	}
	var discriminator struct {
		Schema string `json:"schema"`
	}
	if json.Unmarshal(raw, &discriminator) != nil {
		return r, errRadioUnknown
	}
	if discriminator.Schema == "podconnect.native_radio_handoff_custody.v1" {
		if action != "result" {
			return r, errRadioUnknown
		}
		custody, e := decodeProducerNativeCustody(raw, q)
		if e != nil {
			return r, e
		}
		return radioProducerNativeReply{Custody: custody}, nil
	}
	if discriminator.Schema == "podconnect.native_radio_handoff_rejected.v1" {
		if !producerNativeRejectionMatches(raw, q) {
			return r, errRadioUnknown
		}
		return r, &radioProducerOperationRejected{Receipt: append(json.RawMessage{}, raw...)}
	}
	if discriminator.Schema == "podconnect.native_radio_handoff_preempted.v1" {
		return decodeProducerNativeCompletion(raw, q, action)
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&r) != nil || decoder.Decode(new(any)) != io.EOF {
		return r, errRadioUnknown
	}
	expected := q
	if q.Token == "" {
		expected.Token = r.Token
	}
	if r.radioProducerNativeRequest != expected || r.Schema != "podconnect.native_radio_handoff.v1" || !producerHex(r.Token, 32) || len(r.Proof) == 0 || !json.Valid(r.Proof) {
		return r, errRadioUnknown
	}
	if _, e := time.Parse(time.RFC3339Nano, r.ObservedAt); e != nil {
		return r, errRadioUnknown
	}
	n.mu.Lock()
	if r.Phase == "suspended" && producerHex(r.SuspendedSignature, 64) {
		if old, ok := n.established[r.Handoff]; ok && (old.radioProducerNativeRequest != r.radioProducerNativeRequest || !producerJSONEqual(old.Proof, r.Proof) || old.SuspendedSignature != r.SuspendedSignature) {
			n.mu.Unlock()
			return radioProducerNativeReply{}, errRadioUnknown
		}
	}
	old, known := n.holds[r.Handoff]
	if known && (!producerJSONEqual(old.Proof, r.Proof) || old.Token != r.Token) {
		n.mu.Unlock()
		return radioProducerNativeReply{}, errRadioUnknown
	}
	if action == "reserve" || known {
		n.holds[r.Handoff] = r
	}
	n.mu.Unlock()
	return r, nil
}

func producerNativeRejectionMatches(raw json.RawMessage, q radioProducerNativeRequest) bool {
	var rejected struct {
		radioProducerNativeRequest
		Rejected bool   `json:"operation_rejected"`
		Effects  *bool  `json:"effects_started"`
		Terminal bool   `json:"terminal"`
		Reason   string `json:"reason"`
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	expected := q
	expected.Schema = "podconnect.native_radio_handoff_rejected.v1"
	return !(decoder.Decode(&rejected) != nil || decoder.Decode(new(any)) != io.EOF || rejected.radioProducerNativeRequest != expected || q.Token != "" || !rejected.Rejected || rejected.Effects == nil || *rejected.Effects || !rejected.Terminal || rejected.Reason != "stale_intent")
}

func decodeProducerNativeCompletion(raw json.RawMessage, q radioProducerNativeRequest, action string) (radioProducerNativeReply, error) {
	var r radioProducerNativeReply
	if len(raw) > 65536 {
		return r, errRadioUnknown
	}
	var terminal struct {
		radioProducerNativeRequest
		Phase       string          `json:"phase"`
		Terminal    bool            `json:"terminal"`
		Effects     bool            `json:"effects_started"`
		Proof       json.RawMessage `json:"source_proof"`
		Drain       json.RawMessage `json:"sender_drain"`
		Commit      json.RawMessage `json:"native_commit"`
		CommittedAt string          `json:"committed_at"`
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if d.Decode(&terminal) != nil || d.Decode(new(any)) != io.EOF {
		return r, errRadioUnknown
	}
	expected := q
	expected.Schema = "podconnect.native_radio_handoff_preempted.v1"
	if q.Token == "" {
		expected.Token = terminal.Token
	}
	at, err := time.Parse(time.RFC3339Nano, terminal.CommittedAt)
	if action != "result" || terminal.radioProducerNativeRequest != expected || !producerHex(terminal.Token, 32) || !terminal.Terminal || !terminal.Effects || terminal.Phase != "preempted" || err != nil || at.After(time.Now()) || !producerJSONObject(terminal.Proof) || !producerJSONObject(terminal.Drain) || !producerJSONObject(terminal.Commit) {
		return r, errRadioUnknown
	}
	// Historical completion is never cached as a new custody grant.
	request := terminal.radioProducerNativeRequest
	request.Schema = q.Schema
	return radioProducerNativeReply{radioProducerNativeRequest: request, Phase: "preempted", Proof: terminal.Proof, TerminalDrain: terminal.Drain, TerminalRelease: append(json.RawMessage{}, raw...)}, nil
}

type radioProducerNativeEnvelope struct {
	Request            radioProducerNativeRequest `json:"request"`
	Binding            json.RawMessage            `json:"binding"`
	SuspendedSignature string                     `json:"suspended_signature,omitempty"`
}

func nativeProducerHold(r radioProducerNativeReply) radioProducerHold {
	g, _ := strconv.ParseUint(r.Generation, 10, 64)
	envelope, _ := json.Marshal(radioProducerNativeEnvelope{Request: r.radioProducerNativeRequest, Binding: r.Proof, SuspendedSignature: r.SuspendedSignature})
	return radioProducerHold{radioProducerIdentity: radioProducerIdentity{Process: r.Process, Handoff: r.Handoff, Generation: g, ActionID: r.Action, Target: r.Target}, Token: r.Token, Proof: envelope}
}
func nativeProducerEnvelope(h radioProducerHold) (radioProducerNativeEnvelope, error) {
	var envelope radioProducerNativeEnvelope
	d := json.NewDecoder(bytes.NewReader(h.Proof))
	d.DisallowUnknownFields()
	if d.Decode(&envelope) != nil || d.Decode(new(any)) != io.EOF || envelope.Request.Process != h.Process || envelope.Request.Handoff != h.Handoff || envelope.Request.Target != h.Target || envelope.Request.Action != h.ActionID || envelope.Request.Generation != strconv.FormatUint(h.Generation, 10) || envelope.Request.Token != h.Token || !json.Valid(envelope.Binding) {
		return envelope, errRadioUnknown
	}
	return envelope, nil
}

func (n *radioProducerNativeClient) Reserve(ctx context.Context, q radioProducerReserve) (radioProducerHold, error) {
	request := nativeProducerWire(q)
	r, e := n.invoke(ctx, "reserve", request)
	if e != nil {
		return radioProducerHold{}, e
	}
	if r.Phase != "reserved" && r.Phase != "prepared" {
		return radioProducerHold{}, errRadioUnknown
	}
	return nativeProducerHold(r), nil
}
func (n *radioProducerNativeClient) PauseSuspend(ctx context.Context, h radioProducerHold, op radioProducerIdentity) error {
	envelope, e := nativeProducerEnvelope(h)
	if e != nil {
		return e
	}
	// Suspend is dispatched once. The real C receiver STOP/drain is async;
	// only exact passive receipts may complete this still-pending operation.
	observeCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	reply, e := n.invoke(observeCtx, "suspend", envelope.Request)
	for {
		if e != nil {
			return e
		}
		if !producerJSONEqual(reply.Proof, envelope.Binding) {
			return errRadioUnknown
		}
		if reply.Phase == "suspended" {
			if !producerHex(reply.SuspendedSignature, 64) || observeCtx.Err() != nil {
				return errRadioUnknown
			}
			return n.rememberSuspended(h, reply)
		}
		if reply.Phase != "prepared" && reply.Phase != "stopping" {
			return errRadioUnknown
		}
		timer := time.NewTimer(time.Second)
		select {
		case <-observeCtx.Done():
			timer.Stop()
			return errRadioUnknown
		case <-timer.C:
		}
		reply, e = n.invoke(observeCtx, "result", envelope.Request)
	}
}
func (n *radioProducerNativeClient) Suspended(ctx context.Context, h radioProducerHold) (radioProducerNativeReply, error) {
	envelope, e := nativeProducerEnvelope(h)
	if e != nil {
		return radioProducerNativeReply{}, e
	}
	result, e := n.invoke(ctx, "result", envelope.Request)
	if e != nil || result.Phase != "suspended" || len(result.SuspendedSignature) != 64 || !producerJSONEqual(result.Proof, envelope.Binding) {
		return result, errRadioUnknown
	}
	if e = n.rememberSuspended(h, result); e != nil {
		return radioProducerNativeReply{}, e
	}
	return result, nil
}
func (n *radioProducerNativeClient) Result(ctx context.Context, q radioProducerReserve, h radioProducerHold) (radioProducerResult, error) {
	request := nativeProducerWire(q)
	identity := q.radioProducerIdentity
	var expectedProof json.RawMessage
	if h.Handoff != "" {
		envelope, e := nativeProducerEnvelope(h)
		if e != nil {
			return radioProducerResult{}, e
		}
		request = envelope.Request
		identity = h.radioProducerIdentity
		expectedProof = envelope.Binding
	}
	reply, e := n.invoke(ctx, "result", request)
	if e != nil {
		return radioProducerResult{}, e
	}
	if reply.Custody != nil {
		if h.Handoff == "" || len(expectedProof) == 0 || !producerJSONEqual(expectedProof, reply.Custody.Source) {
			return radioProducerResult{}, errRadioUnknown
		}
		return radioProducerResult{Identity: identity, Custody: reply.Custody}, nil
	}
	if len(expectedProof) > 0 && !producerJSONEqual(expectedProof, reply.Proof) {
		return radioProducerResult{}, errRadioUnknown
	}
	if h.Handoff != "" && reply.Phase == "suspended" {
		if e = n.rememberSuspended(h, reply); e != nil {
			return radioProducerResult{}, e
		}
	}
	terminal := reply.Phase == "suspended" || reply.Phase == "rejected" || reply.Phase == "preempted"
	outcome := "committed"
	if reply.Phase == "rejected" {
		outcome = "rejected"
	}
	if reply.Phase == "preempted" {
		outcome = "preempted"
	}
	hold := nativeProducerHold(reply)
	return radioProducerResult{Identity: identity, Hold: &hold, Terminal: terminal, Outcome: outcome, DrainTicket: reply.TerminalDrain, ReleaseReceipt: reply.TerminalRelease}, nil
}

func producerJSONObject(raw json.RawMessage) bool {
	var object map[string]json.RawMessage
	return len(raw) > 0 && json.Unmarshal(raw, &object) == nil && len(object) > 0
}
func producerJSONEqual(a, b json.RawMessage) bool {
	var x, y any
	decode := func(raw json.RawMessage, value *any) error {
		d := json.NewDecoder(bytes.NewReader(raw))
		d.UseNumber()
		if err := d.Decode(value); err != nil {
			return err
		}
		if d.Decode(new(any)) != io.EOF {
			return errRadioUnknown
		}
		return nil
	}
	if decode(a, &x) != nil || decode(b, &y) != nil {
		return false
	}
	ax, _ := json.Marshal(x)
	by, _ := json.Marshal(y)
	return bytes.Equal(ax, by)
}
