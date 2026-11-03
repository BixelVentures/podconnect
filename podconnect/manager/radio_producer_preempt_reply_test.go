package main

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

// Export bytes from the production reply marshal path for the independent
// Native closed decoder. Synthetic protocol data is never a custody proof.
func TestRadioProducerNativePreemptReplyClosedElevenFields(t *testing.T) {
	r := radioNativePreemptReply{Schema: "podconnect.radio-preempt.v1", NativeProcess: strings.Repeat("1", 64), Handoff: "11111111-1111-4111-8111-111111111111", Generation: "7", Token: strings.Repeat("2", 32), Target: "room-A", ActionID: "22222222-2222-4222-8222-222222222222", Cause: "play"}
	ticket := json.RawMessage(`{"drain_generation":"3","drain_signature":"3333333333333333333333333333333333333333333333333333333333333333","reacquire_challenge":"44444444444444444444444444444444","final_volume_kind":"receiver_acknowledged","final_volume_percent":20,"final_volume_revision":"9"}`)
	if e := r.setDrainTicket(ticket); e != nil {
		t.Fatal(e)
	}
	raw, e := json.Marshal(r)
	if e != nil {
		t.Fatal(e)
	}
	var fields map[string]json.RawMessage
	_ = json.Unmarshal(raw, &fields)
	keys := []string{"schema", "native_process_id", "handoff_id", "generation", "token", "target_id", "action_id", "cause", "reacquire_challenge", "terminal", "drain_ticket"}
	if len(fields) != len(keys) {
		t.Fatal("reply field count", len(fields))
	}
	for _, k := range keys {
		if _, ok := fields[k]; !ok {
			t.Fatal("missing", k)
		}
	}
	if r.Challenge != strings.Repeat("4", 32) || !producerJSONEqual(r.DrainTicket, ticket) {
		t.Fatal("ticket or source nonce changed")
	}
	for _, bad := range []json.RawMessage{json.RawMessage(`{}`), json.RawMessage(`{"reacquire_challenge":3}`), json.RawMessage(`{"reacquire_challenge":"invalid"}`)} {
		invalid := radioNativePreemptReply{}
		if invalid.setDrainTicket(bad) == nil || invalid.Terminal {
			t.Fatal("invented drain authority")
		}
	}
	if path := os.Getenv("PC_RADIO_PREEMPT_REPLY_ARTIFACT"); path != "" {
		if e = os.WriteFile(path, raw, 0600); e != nil {
			t.Fatal(e)
		}
	}
}
