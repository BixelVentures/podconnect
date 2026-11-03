package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"
)

// This is an archived actual C receipt wrapped by the actual Native serializer.
// The Native read correlation is synthetic (direct C had no Native query); this
// is serializer parity, not an actual Native API response. Direct C fixture
// aliases use C bounded identifiers; real Native grants still require hex aliases.
// Only schema-at-capture is positive. Original timestamps are never restamped;
// the production live decoder must deny it, and no source grant is established.
const radioC41NativeCustodySHA = "333bac20fa5f0ff5fe5ade73995160ef97e468108a9b630dc158a039297a08c9"

func TestRadioProducerArchivedActualNativeCustody(t *testing.T) {
	raw, err := os.ReadFile("testdata/radio-c41-native-custody.json")
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(raw)
	if hex.EncodeToString(sum[:]) != radioC41NativeCustodySHA {
		t.Fatal("archived artifact bytes changed")
	}
	if custodyUniqueJSON(raw) != nil {
		t.Fatal("invalid closed JSON")
	}
	var c radioProducerNativeCustody
	fields, err := custodyObject(raw, &c, []string{"schema", "process_id", "target_id", "handoff_id", "generation", "action_id", "sender_peer", "token", "expected_native_revision", "phase", "terminal", "custody_only", "source_proof", "original_suspended_signature", "custody_proof"}, "original_suspended_signature")
	if err != nil || c.Schema != "podconnect.native_radio_handoff_custody.v1" || c.Terminal || !c.CustodyOnly {
		t.Fatal("Native closed envelope", err)
	}
	_, err = custodyObject(fields["custody_proof"], &c.Proof, []string{"schema", "ok", "purpose", "hold", "original_phase", "original_suspended_signature", "phase", "evolution", "target_continuity", "physical_result_verified"}, "original_suspended_signature", "target_continuity")
	if err != nil || c.Proof.Schema != "podconnect.output_radio_absence_custody.v1" || !c.Proof.OK || c.Proof.Physical || !producerJSONEqual(c.Source, c.Proof.Hold) {
		t.Fatal("C closed custody", err)
	}
	hfields, err := custodyObject(c.Source, &c.Binding, []string{"schema", "source_absence", "output_identity", "handoff_id", "token", "generation", "native_revision", "sender_peer", "radio_process", "radio_owner_epoch", "receiver_key_digest", "initial_volume_percent", "volume_provenance"})
	if err != nil {
		t.Fatal(err)
	}
	var source struct {
		Process json.RawMessage `json:"native_process"`
		Pipe    json.RawMessage `json:"pipe_identity"`
		Digest  string          `json:"input_path_digest"`
	}
	if json.Unmarshal(hfields["source_absence"], &source) != nil {
		t.Fatal("absence")
	}
	process, err := custodyProcess(source.Process)
	if err != nil || process != c.Process {
		t.Fatal("incumbent string PID process hash", err)
	}
	var f radioCustodyFuture
	if _, err = custodyObject(c.Proof.Evolution.Future, &f, []string{"native_process", "offer_id", "binding_id", "content_digest", "native_incarnation", "native_revision", "old_native_owner", "intended_native_owner", "expected_input_path", "expected_input_path_digest", "policy_revision", "start_volume_percent"}, "old_native_owner"); err != nil || custodyOwner(f.Intended) != nil {
		t.Fatal("actual future/owner scalar shape", err)
	}
	if err = custodyCompletion(c.Proof.Evolution.Completion, f, c.Proof.Evolution, source.Pipe, source.Digest); err != nil {
		t.Fatal("actual frozen P rejected", err)
	}
	if c.Proof.Continuity == nil {
		t.Fatal("missing captured target continuity")
	}
	captured, err := time.Parse(time.RFC3339Nano, c.Proof.Continuity.ObservedAt)
	if err != nil || !c.targetCurrent(captured) {
		t.Fatal("historical schema-at-capture", err)
	}
	if c.targetCurrent(time.Now()) {
		t.Fatal("archived continuity became fresh")
	}
	q := c.radioProducerNativeRequest
	q.Schema = "podconnect.native_radio_handoff.v1"
	if _, err = decodeProducerNativeCustody(raw, q); err == nil {
		t.Fatal("archived artifact granted live custody")
	}
}

func TestRadioProducerCustodyHistoricalAliasBounds(t *testing.T) {
	for _, key := range []string{"alias_incarnation", "alias_registry"} {
		for _, bad := range []string{"", "bad\nvalue", "bad\x7fvalue", "bad\u00e6value", "bad\\value", "bad\"value", strings.Repeat("a", 256)} {
			_, _, body := custodyFixture(t)
			owner := custodyMap(body, "custody_proof", "evolution", "start_future", "intended_native_owner")
			owner[key] = bad
			raw, _ := json.Marshal(owner)
			if custodyOwner(raw) == nil {
				t.Fatal("invalid historical alias", key)
			}
		}
	}
}
