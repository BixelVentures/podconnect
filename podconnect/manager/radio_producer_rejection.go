package main

import (
	"context"
	"encoding/json"
	"strings"
)

type radioProducerOperationRejected struct{ Receipt json.RawMessage }

func (*radioProducerOperationRejected) Error() string {
	return "native radio operation rejected before receiver effects"
}

type radioProducerNoReceiverEffects struct{}

func (*radioProducerNoReceiverEffects) Error() string {
	return "radio request rejected; receiver unchanged"
}
func (p *radioProducer) rejectCandidate(ctx context.Context, r radioProducerRecord, rejection *radioProducerOperationRejected) error {
	p.mu.Lock()
	current := p.records[r.Fence.Token]
	if current.Preempted || current.Stage != "native-reserve-attempted" {
		p.mu.Unlock()
		return errRadioUnknown
	}
	current.NativeRejection = append(json.RawMessage{}, rejection.Receipt...)
	current.Stage = "candidate-cancel-attempted"
	if e := p.storeLocked(current); e != nil {
		p.mu.Unlock()
		return e
	}
	p.mu.Unlock()
	receipt, e := p.sender.CancelCandidate(ctx, current.Request.Peer)
	if e != nil || !json.Valid(receipt) {
		return errRadioUnknown
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	current = p.records[r.Fence.Token]
	if current.Preempted || current.Stage != "candidate-cancel-attempted" {
		return errRadioUnknown
	}
	current.CancellationReceipt = append(json.RawMessage{}, receipt...)
	current.Terminal = true
	current.Outcome = "rejected"
	current.Stage = "no-effects-rejected"
	if e = p.storeLocked(current); e != nil {
		return e
	}
	return &radioProducerNoReceiverEffects{}
}

// Recovery observes the owner-expired/cancelled decoder candidate. It cannot
// send cancellation, release custody, or synthesize a source from the rejection.
func (p *radioProducer) reconcileRejected(ctx context.Context, r radioProducerRecord, f radioFence) (radioReconciliation, error) {
	cancelled, e := p.sender.Result(ctx, r.Request.Peer.Operation)
	if e != nil || cancelled.Identity != r.Request.Peer.Operation || !cancelled.Terminal || cancelled.Outcome != "rejected" {
		return radioReconciliation{}, errRadioUnknown
	}
	observation, e := p.sender.Observe(ctx)
	if e != nil || !validRadioObservation(observation) {
		return radioReconciliation{}, errRadioUnknown
	}
	p.mu.Lock()
	current := p.records[r.Fence.Token]
	if current.Fence != r.Fence || current.Preempted {
		p.mu.Unlock()
		return radioReconciliation{}, errRadioUnknown
	}
	current.Terminal = true
	current.Outcome = "rejected"
	current.Observation = observation
	current.Stage = "rejected-reconciled"
	if r.Native.Handoff == "" && strings.HasPrefix(r.Stage, "preflight-") {
		current.Stage = "preflight-reconciled"
	}
	if e = p.storeLocked(current); e != nil {
		p.mu.Unlock()
		return radioReconciliation{}, e
	}
	p.mu.Unlock()
	return radioReconciliation{ActionID: f.ActionID, Generation: f.Generation, Token: r.Fence.Token, Terminal: true, Outcome: "rejected", Observation: observation}, nil
}
