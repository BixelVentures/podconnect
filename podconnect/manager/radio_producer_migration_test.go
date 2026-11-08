package main

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRadioProducerMigrationLegacyTerminalHistory(t *testing.T) {
	for _, kind := range []string{"released", "sender-stop-only", "wrong-native-receipt", "unknown"} {
		t.Run(kind, func(t *testing.T) {
			ctx := context.Background()
			directory, err := filepath.EvalSymlinks(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			journal, _, err := openRadioProducerJournal(filepath.Join(directory, "journal"))
			if err != nil {
				t.Fatal(err)
			}
			r := radioProducerNativeRequest{Schema: "podconnect.native_radio_handoff.v1",
				Process: strings.Repeat("a", 64), Handoff: attentionUUID(), Generation: "1",
				Target: "room", Action: attentionUUID(), ExpectedRevision: "1", Token: strings.Repeat("c", 32),
				Peer: radioProducerNativePeer{Process: attentionUUID(), Epoch: "1", Digest: strings.Repeat("d", 64)}}
			proof := json.RawMessage(`{"source":"exact"}`)
			hold := nativeProducerHold(radioProducerNativeReply{radioProducerNativeRequest: r, Proof: proof})
			played := radioProducerRecord{
				Fence: radioFence{Token: r.Handoff, ActionID: r.Action, Generation: 1}, Native: hold,
				Terminal: kind != "unknown", Outcome: "committed", Stage: "terminal", SourceDrained: true,
				Observation: radioNativeObservation{State: "playing", SourceKind: "radio"},
			}
			if err := journal.write(played); err != nil {
				t.Fatal(err)
			}
			if kind == "released" || kind == "wrong-native-receipt" {
				body, _ := json.Marshal(r)
				var completion map[string]any
				_ = json.Unmarshal(body, &completion)
				completion["schema"], completion["phase"], completion["terminal"] = "podconnect.native_radio_handoff_preempted.v1", "preempted", true
				completion["effects_started"], completion["source_proof"] = true, proof
				if kind == "wrong-native-receipt" {
					completion["source_proof"] = map[string]string{"source": "different"}
				}
				drain := json.RawMessage(`{"sender":"exact"}`)
				completion["sender_drain"], completion["native_commit"] = drain, map[string]string{"receipt": "exact"}
				completion["committed_at"] = time.Now().Add(-time.Hour).UTC().Format(time.RFC3339Nano)
				raw, _ := json.Marshal(completion)
				finished := radioProducerRecord{
					Fence: radioFence{Token: attentionUUID(), ActionID: attentionUUID(), Generation: 2}, Native: hold,
					Terminal: true, Preempted: true, Outcome: "preempted", Stage: "preempted",
					DrainTicket: drain, NativeRelease: raw,
				}
				if err := journal.write(finished); err != nil {
					t.Fatal(err)
				}
			}
			f := &primaryRadioFixture{t: t, state: radioPrimaryState{
				Schema: primaryRadioSchema, Process: strings.Repeat("b", 64), Revision: "2",
				Targets: []radioProducerTarget{{radioTarget: radioTarget{ID: "room", Selectable: true, Availability: "available"}, HomePodID: "1"}},
				Observation: &radioNativeObservation{State: "idle", SourceKind: "none", OwnerGeneration: 2,
					ObservedAt: radioTestNow() - 1, ValidUntil: radioTestNow() + 20},
			}}
			p, err := newPrimaryRadioProducer(f, journal.directory)
			if err != nil {
				t.Fatal(err)
			}
			before, _, err := openRadioProducerJournal(journal.directory)
			if err != nil {
				t.Fatal(err)
			}
			_, _, err = p.Ready(ctx)
			if (err == nil) != (kind == "released") || p.active != "" || len(f.calls) != 0 {
				t.Fatalf("history was treated as live authority or blocked an exact release: %v", err)
			}
			_, reloaded, err := openRadioProducerJournal(before.directory)
			if err != nil || len(reloaded) != len(p.records) || !producerJSONEqual(reloaded[played.Fence.Token].Native.Proof, played.Native.Proof) {
				t.Fatal("migration changed durable history")
			}
		})
	}
}

func TestRadioProducerMigrationPrimaryTerminalHistory(t *testing.T) {
	for _, kind := range []string{"released", "wrong-session", "wrong-action", "wrong-observed-owner", "unknown", "playing"} {
		t.Run(kind, func(t *testing.T) {
			directory, err := filepath.EvalSymlinks(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			journal, _, err := openRadioProducerJournal(filepath.Join(directory, "journal"))
			if err != nil {
				t.Fatal(err)
			}
			nativeProcess := strings.Repeat("a", 64)
			session := radioPrimarySession{Process: attentionUUID(), Operation: attentionUUID(), DeviceID: "1", Incarnation: "2", Epoch: "3", Route: "4"}
			start := radioProducerRecord{
				Fence:    radioFence{Token: attentionUUID(), ActionID: session.Operation, Generation: 1, OwnerGeneration: 1},
				Request:  radioProducerReserve{radioProducerIdentity: radioProducerIdentity{Process: nativeProcess, Target: "room"}},
				Primary:  &radioPrimaryRecord{Device: "1", Station: "p3", Session: &session},
				Terminal: true, Outcome: "committed", Stage: "terminal", SourceDrained: kind != "playing",
				Observation: radioNativeObservation{State: "playing", SourceKind: "radio"},
			}
			fence := radioFence{Token: attentionUUID(), ActionID: attentionUUID(), Generation: 2, OwnerGeneration: 2}
			command := radioPrimaryCommand{Schema: primaryRadioSchema, Phase: "stop", Handoff: fence.Token,
				Generation: "2", Action: fence.ActionID, Process: nativeProcess, OwnerGeneration: "2",
				Target: "room", From: "room", ExpectedRevision: "2", Session: &session}
			receipt := radioPrimaryReceipt{radioPrimaryCommand: command, TeardownCSeq: 7,
				Observation: &radioNativeObservation{State: "stopped", SourceKind: "none", OwnerGeneration: 2}}
			receipt.Phase, receipt.ForPhase = "released", "stop"
			stop := radioProducerRecord{Fence: fence,
				Request: radioProducerReserve{radioProducerIdentity: radioProducerIdentity{Process: nativeProcess, Target: "room"}, From: "room",
					ExpectedIntent: radioProducerIntent{Process: nativeProcess, Revision: "2"}},
				Primary:  &radioPrimaryRecord{Device: "1", Incumbent: start.Fence.Token, LastCommand: &command, LastReceipt: &receipt},
				Terminal: kind != "unknown", Outcome: "committed", Stage: "terminal",
			}
			archived := receipt
			if kind == "wrong-session" {
				wrong := session
				wrong.Epoch = "8"
				archived.Session = &wrong
			} else if kind == "wrong-action" {
				archived.Action = attentionUUID()
			} else if kind == "wrong-observed-owner" {
				other := *archived.Observation
				other.OwnerGeneration++
				archived.Observation = &other
			}
			start.Primary.StopReceipt = &archived
			if err := journal.write(start); err != nil {
				t.Fatal(err)
			}
			if kind != "playing" {
				if err := journal.write(stop); err != nil {
					t.Fatal(err)
				}
			}
			f := &producerFixture{}
			p, err := newRadioProducer(producerNativeFixture{f}, producerSenderFixture{f}, journal.directory, true)
			if (err == nil) != (kind == "released") {
				t.Fatalf("history compatibility error: %v", err)
			}
			if err == nil {
				if _, _, err = p.Ready(context.Background()); err != nil || p.active != "" || len(f.calls) != 0 ||
					p.RadioGenerationFloor() != 2 || len(p.records) != 2 {
					t.Fatalf("historical receipt became authority or was deleted: %v", err)
				}
			}
		})
	}
}

func TestRadioProducerMigrationRejectionRequiresExactTypedOperation(t *testing.T) {
	const epoch, generation = "1", "2"
	native, sender, action := strings.Repeat("a", 64), attentionUUID(), attentionUUID()
	token, digest := strings.Repeat("b", 32), strings.Repeat("c", 64)
	legacy := radioProducerRecord{
		Terminal: true, Outcome: "rejected", Stage: "no-effects-rejected",
		Request: radioProducerReserve{
			radioProducerIdentity: radioProducerIdentity{Process: native, Target: "room", Handoff: action, Generation: 2},
			ExpectedIntent:        radioProducerIntent{Process: native, Revision: "1"},
			Peer:                  radioProducerPeer{Process: sender, OwnerGeneration: 1, ReceiverDigest: digest},
		},
	}
	legacy.Request.Peer.Proof, _ = json.Marshal(map[string]string{
		"device_id": "1", "operation_id": action, "operation_generation": generation,
		"radio_process": sender, "radio_owner_epoch": epoch, "sender_token": token,
	})
	nativeWire := nativeProducerWire(legacy.Request)
	nativeWire.Schema = "podconnect.native_radio_handoff_rejected.v1"
	noEffect := false
	legacy.NativeRejection, _ = json.Marshal(struct {
		radioProducerNativeRequest
		Rejected bool   `json:"operation_rejected"`
		Effects  *bool  `json:"effects_started"`
		Terminal bool   `json:"terminal"`
		Reason   string `json:"reason"`
	}{nativeWire, true, &noEffect, true, "stale_intent"})
	legacy.CancellationReceipt, _ = json.Marshal(radioProducerSenderReply{
		Schema: "podconnect.radio-sender.v1", OK: true, Process: sender, Device: "1", Epoch: epoch,
		Generation: generation, Operation: action, Token: token, Digest: digest, Phase: "cancelled", Cancelled: true,
	})
	if !producerLegacyHistoryTerminal(nil, legacy) {
		t.Fatal("exact receiver-free legacy rejection was blocked")
	}
	for _, garbage := range []json.RawMessage{json.RawMessage("null"), json.RawMessage(`{"not":"proof"}`)} {
		other := legacy
		other.NativeRejection = garbage
		if producerLegacyHistoryTerminal(nil, other) {
			t.Fatal("accepted unbound Native rejection")
		}
		other = legacy
		other.CancellationReceipt = garbage
		if producerLegacyHistoryTerminal(nil, other) {
			t.Fatal("accepted unbound cancellation receipt")
		}
		other.Stage = "rejected-reconciled"
		if producerLegacyHistoryTerminal(nil, other) {
			t.Fatal("accepted reconciliation without retained sender proof")
		}
	}

	predecessor := radioPrimarySession{Process: attentionUUID(), Operation: attentionUUID(), DeviceID: "1", Incarnation: "2", Epoch: "3", Route: "4"}
	cmd := radioPrimaryCommand{
		Schema: primaryRadioSchema, Phase: "prepare_replace", Process: native, Handoff: action, Action: attentionUUID(),
		Generation: "2", OwnerGeneration: "2", Target: "room", From: "room", ExpectedRevision: "1", Session: &predecessor,
	}
	receipt := radioPrimaryReceipt{radioPrimaryCommand: cmd}
	receipt.Phase = "replacement_rejected"
	record := radioProducerRecord{
		Terminal: true, Outcome: "rejected", Stage: "no-effects-rejected",
		Fence: radioFence{Token: action, ActionID: cmd.Action, Generation: 2, OwnerGeneration: 2},
		Request: radioProducerReserve{
			radioProducerIdentity: radioProducerIdentity{Process: native, Target: "room"},
			From:                  "room", ExpectedIntent: radioProducerIntent{Process: native, Revision: "1"},
		},
		Primary: &radioPrimaryRecord{IncumbentSession: &predecessor, LastCommand: &cmd, LastReceipt: &receipt},
	}
	if !producerPrimaryHistoryTerminal(nil, record) {
		t.Fatal("exact primary no-effects rejection was blocked")
	}
	record.Fence.ActionID = attentionUUID()
	if producerPrimaryHistoryTerminal(nil, record) {
		t.Fatal("accepted copied command and receipt from another journal action")
	}
}
