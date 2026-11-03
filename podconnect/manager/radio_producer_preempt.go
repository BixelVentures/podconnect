package main

import (
	"bytes"
	"context"
	"crypto/subtle"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
)

type radioNativePreemptRequest struct {
	Challenge     json.RawMessage `json:"reacquire_challenge"`
	Schema        string          `json:"schema"`
	NativeProcess string          `json:"native_process_id"`
	Handoff       string          `json:"handoff_id"`
	Generation    string          `json:"generation"`
	Token         string          `json:"token"`
	Target        string          `json:"target_id"`
	ActionID      string          `json:"action_id"`
	Cause         string          `json:"cause"`
}
type radioNativePreemptReply struct {
	Token         string          `json:"token"`
	Target        string          `json:"target_id"`
	Cause         string          `json:"cause"`
	Challenge     string          `json:"reacquire_challenge"`
	Schema        string          `json:"schema"`
	NativeProcess string          `json:"native_process_id"`
	Handoff       string          `json:"handoff_id"`
	Generation    string          `json:"generation"`
	ActionID      string          `json:"action_id"`
	Terminal      bool            `json:"terminal"`
	DrainTicket   json.RawMessage `json:"drain_ticket"`
}

// Sender authenticates the C ticket; the manager projects its actual nonce.
func (r *radioNativePreemptReply) setDrainTicket(raw json.RawMessage) error {
	var ticket struct {
		Challenge string `json:"reacquire_challenge"`
	}
	if json.Unmarshal(raw, &ticket) != nil || !producerHex(ticket.Challenge, 32) {
		return errRadioUnknown
	}
	r.Challenge = ticket.Challenge
	r.DrainTicket = append(json.RawMessage{}, raw...)
	r.Terminal = true
	return nil
}

func (p *radioProducer) preempt(ctx context.Context, q radioNativePreemptRequest) (radioNativePreemptReply, error) {
	reply := radioNativePreemptReply{Schema: "podconnect.radio-preempt.v1", NativeProcess: q.NativeProcess, Handoff: q.Handoff, Generation: q.Generation, ActionID: q.ActionID, Token: q.Token, Target: q.Target, Cause: q.Cause}
	if q.Schema != "podconnect.radio-preempt.v1" || !json.Valid(q.Challenge) || len(q.Challenge) == 0 || q.ActionID == "" || len(q.ActionID) > 128 || (q.Cause != "play" && q.Cause != "resume" && q.Cause != "transfer") {
		return reply, errRadioUnknown
	}
	p.mu.Lock()
	var r radioProducerRecord
	key := ""
	found := false
	for candidateKey, candidate := range p.records {
		h := candidate.Native
		// The pre-call journal exists before Native Reserve. A callback may
		// arrive before its response; source authority is recovered passively
		// below, after fencing every late response from reviving this operation.
		identity := h.radioProducerIdentity
		if h.Handoff == "" {
			identity = candidate.Request.radioProducerIdentity
		}
		if identity.Handoff == q.Handoff && identity.Process == q.NativeProcess && identity.Target == q.Target && strconv.FormatUint(identity.Generation, 10) == q.Generation && q.Token != "" && (h.Token == "" || subtle.ConstantTimeCompare([]byte(q.Token), []byte(h.Token)) == 1) && (!found || candidate.Fence.Generation > r.Fence.Generation) {
			r = candidate
			key = candidateKey
			found = true
		}
	}
	if !found {
		p.mu.Unlock()
		return reply, errRadioUnknown
	}

	if r.Preempted {
		p.mu.Unlock()
		if r.PreemptAction == q.ActionID && bytes.Equal(r.PreemptChallenge, q.Challenge) && r.Terminal && r.Outcome == "preempted" && len(r.DrainTicket) > 0 {
			if e := reply.setDrainTicket(r.DrainTicket); e != nil {
				return reply, e
			}
			return reply, nil
		}
		return reply, errRadioUnknown
	}
	r.Preempted = true
	r.PreemptAction = q.ActionID
	r.PreemptChallenge = append(json.RawMessage{}, q.Challenge...)
	r.Terminal = false
	r.Stage = "preempt-drain-attempted"
	if e := p.storeLocked(r); e != nil {
		p.mu.Unlock()
		return reply, e
	}
	p.mu.Unlock()
	if r.Native.Handoff == "" {
		result, e := p.native.Result(ctx, r.Request, radioProducerHold{})
		if e != nil || result.Hold == nil || result.Hold.Handoff != q.Handoff || result.Hold.Process != q.NativeProcess || result.Hold.Target != q.Target || strconv.FormatUint(result.Hold.Generation, 10) != q.Generation || subtle.ConstantTimeCompare([]byte(q.Token), []byte(result.Hold.Token)) != 1 {
			return reply, errRadioUnknown
		}
		p.mu.Lock()
		current := p.records[key]
		if !current.Preempted || current.PreemptAction != q.ActionID || current.Stage != "preempt-drain-attempted" {
			p.mu.Unlock()
			return reply, errRadioUnknown
		}
		current.Native = *result.Hold
		if e := p.storeLocked(current); e != nil {
			p.mu.Unlock()
			return reply, e
		}
		r = current
		p.mu.Unlock()
	}
	if r.Sender.Handoff == "" {
		// This is a prepared candidate, not a claimed receiver. The concrete
		// sender validates its signed old-C cancellation challenge before drain.
		r.Sender = radioProducerHold{radioProducerIdentity: r.Request.Peer.Operation, SourceGeneration: r.Request.Peer.SourceGeneration, Token: "", Proof: append(json.RawMessage{}, r.Request.Peer.Proof...)}
		r.Sender.Process = r.Request.Peer.Process
		r.Sender.OwnerGeneration = r.Request.Peer.OwnerGeneration
	}
	ticket, e := p.sender.PreemptDrain(ctx, r.Sender, r.Native, q)
	if e != nil || len(ticket) == 0 || !json.Valid(ticket) {
		return reply, errRadioUnknown
	}
	if e = reply.setDrainTicket(ticket); e != nil {
		return reply, e
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	current := p.records[key]
	if !current.Preempted || current.PreemptAction != q.ActionID || current.Stage != "preempt-drain-attempted" {
		return reply, errRadioUnknown
	}
	current.DrainTicket = append(json.RawMessage{}, ticket...)
	current.Stage = "preempted"
	current.Terminal = true
	current.Outcome = "preempted"
	if e = p.storeLocked(current); e != nil {
		return reply, e
	}
	if p.active == key {
		p.active = ""
	}
	if retained, ok := p.retained[q.Target]; ok && retained.Native.Handoff == q.Handoff {
		delete(p.retained, q.Target)
	}
	return reply, nil
}
func radioNativePreemptHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if !radioNativePreemptAuthed(r) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		http.Error(w, "method not allowed", 405)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 8192)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	var q radioNativePreemptRequest
	if decoder.Decode(&q) != nil || decoder.Decode(new(any)) != io.EOF {
		http.Error(w, "invalid preemption", 400)
		return
	}
	radioOwner.mu.Lock()
	producer, ok := radioOwner.backend.(*radioProducer)
	generation := radioOwner.state.Generation
	radioOwner.mu.Unlock()
	if !ok {
		http.Error(w, "native producer unavailable", 503)
		return
	}
	reply, e := producer.preempt(r.Context(), q)
	if e != nil {
		http.Error(w, "preemption unresolved", 409)
		return
	}
	// Revoke presentation only after signed native sender drain. This never
	// resumes Spotify: its original explicit action verifies/consumes the ticket.
	radioOwner.observeNativePreempt(q.Target, generation)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(reply)
}

func radioNativePreemptAuthed(r *http.Request) bool {
	// A running Native process and producer keep the same control credential
	// until an explicit boot configuration change. A file edit cannot strand
	// its existing held receiver by silently rotating callback authentication.
	radioOwner.mu.Lock()
	producer, ok := radioOwner.backend.(*radioProducer)
	radioOwner.mu.Unlock()
	if ok {
		if native, matched := producer.native.(*radioProducerNativeClient); matched && native.http != nil {
			token := native.http.token
			provided := r.Header.Get("X-PodConnect-Token")
			return len(token) >= 32 && len(provided) == len(token) && subtle.ConstantTimeCompare([]byte(provided), []byte(token)) == 1
		}
	}
	options, e := readRadioOptions()
	if e != nil {
		return false
	}
	if options.Mode != "builtin" || len(options.NativeToken) < 32 || !producerHex(options.NativeToken, len(options.NativeToken)) {
		return false
	}
	provided := r.Header.Get("X-PodConnect-Token")
	return len(provided) == len(options.NativeToken) && subtle.ConstantTimeCompare([]byte(provided), []byte(options.NativeToken)) == 1
}
