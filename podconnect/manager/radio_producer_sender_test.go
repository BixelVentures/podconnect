package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestRadioProducerSenderClaimsCannotReplaceKernelCustody(t *testing.T) {
	reply := radioProducerSenderReply{Schema: "podconnect.radio-sender.v1", OK: true, Process: attentionUUID(), PID: strconv.Itoa(os.Getpid()), ObservedAt: time.Now().UTC().Format(time.RFC3339Nano), ValidUntil: time.Now().Add(2 * time.Second).UTC().Format(time.RFC3339Nano), Device: "0", Epoch: "0", Generation: "0", DrainGeneration: "0", Phase: "unknown", IdleVerified: true}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, password, ok := r.BasicAuth()
		if !ok || user != "admin" || password != strings.Repeat("a", 32) {
			t.Error("dedicated sender BasicAuth missing")
		}
		if r.Header.Get("X-PodConnect-Token") != "" {
			t.Error("Native control token leaked to sender")
		}
		_ = json.NewEncoder(w).Encode(reply)
	}))
	defer server.Close()
	native := &radioProducerNativeClient{}
	client, e := newRadioProducerSenderClient(server.URL, strings.Repeat("a", 32), native, &radioSender{})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = client.call(context.Background(), "capabilities", map[string]any{"schema": "podconnect.radio-sender.v1"}); e == nil {
		t.Fatal("authenticated source claims bypassed missing actual supervisor/cgroup/guardian")
	}
}

// Transport fixtures prove byte-preserving tagged-union handling, not custody,
// signatures or actual C admission. Those require the composed owner fixtures.
func TestRadioProducerOpaqueProofVariantsPreserveParkedChildParentHistory(t *testing.T) {
	for _, schema := range []string{"podconnect.output_radio_handoff.v1", "podconnect.output_radio_absence.v1", "podconnect.output_radio_parked_absence.v1"} {
		t.Run(schema, func(t *testing.T) {
			binding := map[string]any{"schema": schema, "source_hold": map[string]any{"schema": "podconnect.output_radio_handoff.v1", "token": "historical-parent-token", "writer_proof": map[string]any{"process": "actual-owner-fixture", "fd": "8"}}, "parked_source": map[string]any{"process": "same-C-fixture", "device_id": "123", "device_incarnation": "1", "session_epoch": "1", "route_revision": "2", "selection_revision": "3", "input_generation": "4", "input_item_id": "5", "input_path_digest": "original-input-fixture"}, "output_identity": map[string]any{"process": "same-C-fixture", "device_id": "456", "device_incarnation": "2", "session_epoch": "0", "route_revision": "0", "base_revision": "9", "attention_revision": "0"}, "handoff_id": attentionUUID(), "token": strings.Repeat("b", 32), "generation": "7", "native_revision": "10", "sender_peer": "radio-C-fixture", "radio_process": attentionUUID(), "radio_owner_epoch": "1", "receiver_key_digest": strings.Repeat("c", 64), "initial_volume_percent": 22, "volume_provenance": "stored_manual_base"}
			raw, _ := json.Marshal(binding)
			request := radioProducerNativeRequest{Schema: "podconnect.native_radio_handoff.v1", Process: "native-process-fixture", Target: "destination-B", Handoff: attentionUUID(), Generation: "7", Action: attentionUUID(), Token: strings.Repeat("b", 32), ExpectedRevision: "10"}
			native := nativeProducerHold(radioProducerNativeReply{radioProducerNativeRequest: request, Proof: raw})
			state := map[string]any{"schema": "podconnect.radio-sender.v1", "device_id": "456", "operation_id": attentionUUID(), "operation_generation": "8", "radio_process": attentionUUID(), "radio_owner_epoch": "1", "sender_token": strings.Repeat("d", 32)}
			stateRaw, _ := json.Marshal(state)
			reserve := radioProducerReserve{Peer: radioProducerPeer{Proof: stateRaw}}
			body, err := senderProducerReserveBody(reserve, native)
			if err != nil {
				t.Fatal(err)
			}
			transported, _ := json.Marshal(body["hold"])
			if !producerJSONEqual(transported, raw) {
				t.Fatal("typed proof projected or reduced")
			}
			// Parent retirement elsewhere cannot rewrite this confirmed child's history.
			binding["source_hold"] = nil
			envelope, _ := json.Marshal(radioProducerSenderEnvelope{Request: body, Native: native})
			sender := radioProducerHold{Proof: envelope}
			operation := radioProducerIdentity{Handoff: attentionUUID(), Generation: 9}
			again, err := senderOperationBody(sender, operation)
			if err != nil {
				t.Fatal(err)
			}
			retained, _ := json.Marshal(again["hold"])
			if !producerJSONEqual(retained, raw) {
				t.Fatal("child transport depends on live parent or mutates history")
			}
			invalid := native
			invalid.Token = strings.Repeat("e", 32)
			if _, err := senderProducerReserveBody(reserve, invalid); err == nil {
				t.Fatal("new proof variant weakened exact outer token binding")
			}
		})
	}
}
