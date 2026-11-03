package main

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

type radioRetiredSenderFixture struct {
	producerSenderFixture
	binding radioProducerTargetBinding
}

func (s radioRetiredSenderFixture) Evidence(ctx context.Context, n radioProducerEvidence) (radioProducerEvidence, error) {
	e, err := s.producerSenderFixture.Evidence(ctx, n)
	e.Bindings = map[string]radioProducerTargetBinding{"r1": s.binding}
	return e, err
}

// These are causal orchestration negatives, not native/receiver acceptance.
func TestRadioProducerHistoricalPlayingRequiresCurrentCompletedNativeReturn(t *testing.T) {
	for _, scenario := range []string{"terminal_bool_only", "stop_only", "incomplete_return", "wrong_drain", "changed_device", "changed_pairing", "changed_native"} {
		t.Run(scenario, func(t *testing.T) {
			p, f := producerTest(t, true)
			identity := radioProducerIdentity{Process: "native-process", Handoff: "native-hold", Generation: 1, ActionID: "native-action", Target: "r1"}
			hold := radioProducerHold{radioProducerIdentity: identity, Token: "native-token", Proof: json.RawMessage(`{"immutable":"original"}`)}
			binding := radioProducerTargetBinding{Device: "1", Digest: "original-key"}
			senderProof, _ := json.Marshal(radioProducerSenderEnvelope{Request: map[string]any{"device_id": "1"}, Native: hold})
			old := radioProducerRecord{Fence: radioFence{Token: "start", Generation: 1}, Native: hold, Terminal: true, Observation: radioNativeObservation{SourceKind: "radio", State: "playing"}}
			done := radioProducerRecord{Fence: radioFence{Token: "stop", Generation: 2}, Native: hold, Sender: radioProducerHold{Proof: senderProof}, Preempted: true, Terminal: true, Outcome: "preempted", DrainTicket: json.RawMessage(`{"drain":"exact"}`)}
			done.Request.Peer.ReceiverDigest = "original-key"
			f.nativeResults = map[string]radioProducerResult{"native-action": {Identity: identity, Terminal: true, Outcome: "preempted", DrainTicket: done.DrainTicket}}
			switch scenario {
			case "stop_only":
				done.Preempted = false
				old.SourceDrained = true
			case "incomplete_return":
				r := f.nativeResults["native-action"]
				r.Outcome = "committed"
				f.nativeResults["native-action"] = r
			case "wrong_drain":
				r := f.nativeResults["native-action"]
				r.DrainTicket = json.RawMessage(`{"drain":"other"}`)
				f.nativeResults["native-action"] = r
			case "changed_device":
				binding.Device = "2"
			case "changed_pairing":
				binding.Digest = "replacement-key"
			case "changed_native":
				done.Native.Process = "new-native"
			}
			if err := p.storeLocked(old); err != nil {
				t.Fatal(err)
			}
			if err := p.storeLocked(done); err != nil {
				t.Fatal(err)
			}
			p.sender = radioRetiredSenderFixture{producerSenderFixture{f}, binding}
			_, _, err := p.Ready(context.Background())
			if err == nil {
				t.Fatalf("scenario=%s ready error=%v", scenario, err)
			}
			if p.active != "" || len(p.retained) != 0 || len(f.calls) != 0 {
				t.Fatal("passive retirement adopted/replayed custody", f.calls)
			}
		})
	}
}

func TestRadioProducerPersistedExactCompletionNeedsFreshPhysicalAdmission(t *testing.T) {
	for _, scenario := range []string{"new_native", "missing_receipt", "wrong_source", "wrong_drain", "wrong_controller", "changed_pairing"} {
		t.Run(scenario, func(t *testing.T) {
			p, f := producerTest(t, true)
			request := radioProducerNativeRequest{Schema: "podconnect.native_radio_handoff.v1", Process: "old-native", Target: "r1", Handoff: attentionUUID(), Generation: "2", Action: attentionUUID(), ExpectedRevision: "4", Token: strings.Repeat("b", 32), Peer: radioProducerNativePeer{Process: attentionUUID(), Epoch: "1", Digest: strings.Repeat("c", 64)}}
			proof := json.RawMessage(`{"immutable":"old-source"}`)
			hold := nativeProducerHold(radioProducerNativeReply{radioProducerNativeRequest: request, Proof: proof})
			senderProof, _ := json.Marshal(radioProducerSenderEnvelope{Request: map[string]any{"device_id": "1"}, Native: hold})
			drain := json.RawMessage(`{"actual":"drain"}`)
			raw, _ := json.Marshal(request)
			var completion map[string]any
			_ = json.Unmarshal(raw, &completion)
			completion["schema"] = "podconnect.native_radio_handoff_preempted.v1"
			completion["phase"] = "preempted"
			completion["terminal"] = true
			completion["effects_started"] = true
			completion["source_proof"] = proof
			completion["sender_drain"] = drain
			completion["native_commit"] = map[string]any{"actual": "commit"}
			completion["committed_at"] = time.Now().Add(-time.Hour).UTC().Format(time.RFC3339Nano)
			record := radioProducerRecord{Fence: radioFence{Token: "retired", ActionID: "retired", Generation: 9}, Native: hold, Sender: radioProducerHold{Proof: senderProof}, Preempted: true, Terminal: true, Outcome: "preempted", DrainTicket: drain}
			record.Request.Peer.ReceiverDigest = "original-key"
			binding := radioProducerTargetBinding{Device: "1", Digest: "original-key"}
			switch scenario {
			case "wrong_source":
				completion["source_proof"] = map[string]any{"rotated": "source"}
			case "wrong_drain":
				completion["sender_drain"] = map[string]any{"other": "drain"}
			case "wrong_controller":
				record.Controller = "old-controller"
			case "changed_pairing":
				binding.Digest = "new-key"
			}
			record.NativeRelease, _ = json.Marshal(completion)
			if scenario == "missing_receipt" {
				record.NativeRelease = nil
			}
			old := record
			old.Fence = radioFence{Token: "playing", ActionID: "playing", Generation: 2}
			old.Preempted = false
			old.NativeRelease = nil
			old.Observation = radioNativeObservation{State: "playing", SourceKind: "radio"}
			if err := p.storeLocked(old); err != nil {
				t.Fatal(err)
			}
			if err := p.storeLocked(record); err != nil {
				t.Fatal(err)
			}
			restored, err := newRadioProducer(producerNativeFixture{f}, radioRetiredSenderFixture{producerSenderFixture{f}, binding}, p.journal.directory, true)
			if err != nil {
				t.Fatal(err)
			}
			_, _, err = restored.Ready(context.Background())
			if (err == nil) != (scenario == "new_native") {
				t.Fatalf("scenario %s error=%v", scenario, err)
			}
			if restored.active != "" || len(restored.retained) != 0 || len(f.calls) != 0 {
				t.Fatal("cached history replayed/adopted old authority")
			}
			if restored.RadioGenerationFloor() != 9 {
				t.Fatal("durable monotone operation floor lost")
			}
		})
	}
}

func TestRadioProducerHistoricalProofPreservesIntegerIdentity(t *testing.T) {
	if producerJSONEqual(json.RawMessage(`{"alias_revision":9007199254740992}`), json.RawMessage(`{"alias_revision":9007199254740993}`)) {
		t.Fatal("distinct uint64 source epochs collapsed")
	}
	if !producerJSONEqual(json.RawMessage(`{"epoch":18446744073709551614,"binding":"a"}`), json.RawMessage(`{"binding":"a","epoch":18446744073709551614}`)) {
		t.Fatal("key ordering changed exact proof")
	}
}
