package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// Incumbent Native contentProcess and C re_process_json use string PID;
// C re_owner_json preserves the incumbent integer alias_revision.
func custodyNativeStateFixture() radioProducerNativeState {
	s := producerNativeStateFixture()
	s.NativeProcess = json.RawMessage(`{"boot_id":"11111111-1111-4111-8111-111111111111","pid":"42","proc_start_ticks":"100"}`)
	digest := sha256.Sum256(s.NativeProcess)
	s.ProcessID = hex.EncodeToString(digest[:])
	return s
}

// These are protocol-unit fixtures, not production capability/custody proofs.
func custodyFixture(t *testing.T) (radioProducerNativeRequest, radioProducerHold, map[string]any) {
	t.Helper()
	state := custodyNativeStateFixture()
	q := radioProducerNativeRequest{Schema: "podconnect.native_radio_handoff.v1", Process: state.ProcessID, Target: "room-A", Handoff: attentionUUID(), Generation: "7", Action: attentionUUID(), ExpectedRevision: "0", Token: strings.Repeat("1", 32), Peer: radioProducerNativePeer{Process: attentionUUID(), Epoch: "1001", Digest: strings.Repeat("2", 64)}}
	var process any
	_ = json.Unmarshal(state.NativeProcess, &process)
	cprocess := attentionUUID()
	identity := map[string]any{"process": cprocess, "device_id": "123", "device_incarnation": "1", "session_epoch": "0", "route_revision": "0", "base_revision": "1", "attention_revision": "1"}
	pipe := map[string]any{"device": "3", "inode": "4", "type": "fifo"}
	pathDigest := strings.Repeat("3", 64)
	source := map[string]any{"kind": "global", "native_process": process, "registry_digest": strings.Repeat("4", 64), "input_path": "/tmp/native-radio-unit.fifo", "input_path_digest": pathDigest, "pipe_identity": pipe}
	h := map[string]any{"schema": "podconnect.output_radio_absence.v1", "source_absence": source, "output_identity": identity, "handoff_id": q.Handoff, "token": q.Token, "generation": q.Generation, "native_revision": "1", "sender_peer": q.Peer.Process, "radio_process": q.Peer.Process, "radio_owner_epoch": q.Peer.Epoch, "receiver_key_digest": q.Peer.Digest, "initial_volume_percent": 35, "volume_provenance": "stored_manual_base"}
	owner := map[string]any{"account_user_id": "account", "target_id": "engine-B", "room_id": "room-B", "homepod_id": "456", "connection_id": "connection", "session_id": "session", "alias_incarnation": strings.Repeat("5", 32), "alias_registry": strings.Repeat("6", 64), "alias_revision": 0}
	policy := "7fc3239b1c5e65cdb49cca308ad9368c50ef0a90d4cd5fd537a9f97de488625d"
	f := map[string]any{"native_process": process, "offer_id": strings.Repeat("7", 32), "binding_id": strings.Repeat("8", 64), "content_digest": strings.Repeat("9", 64), "native_incarnation": strings.Repeat("a", 32), "native_revision": "11", "old_native_owner": nil, "intended_native_owner": owner, "expected_input_path": source["input_path"], "expected_input_path_digest": pathDigest, "policy_revision": policy, "start_volume_percent": 35}
	startToken, reservation := strings.Repeat("b", 32), strings.Repeat("c", 32)
	p := map[string]any{"schema": "podconnect.output_start_complete.v1", "ok": true, "token": startToken, "offer_id": f["offer_id"], "binding_id": f["binding_id"], "content_digest": f["content_digest"], "native_incarnation": f["native_incarnation"], "native_revision": f["native_revision"], "native_reservation_id": reservation, "policy_revision": policy, "start_volume_percent": 35, "intended_native_owner": owner, "actual_native_owner": owner, "target_identity": map[string]any{"process": cprocess, "device_id": "456", "device_incarnation": "1"}, "route_identity": map[string]any{"process": cprocess, "device_id": "456", "device_incarnation": "1", "session_epoch": "1", "route_revision": "0", "selection_revision": "0", "input_generation": "1", "input_item_id": "101", "input_path_digest": pathDigest}, "input_path_digest": pathDigest, "pipe_identity": pipe, "input_epoch": "1", "writer_source_epoch": "1", "phase": "completed", "possible_effect": true, "contexts_drained": true, "gate_closed": false, "deadline_retired": false, "pin_remaining_ms": 0, "expires_after_ms": 8000, "physical_result_verified": false}
	e := map[string]any{"serial": "2", "c_process": cprocess, "start_future": f, "start_token": startToken, "native_reservation_id": reservation, "member_set_digest": strings.Repeat("d", 64), "effect_generation": "3", "completion": p}
	now := time.Now().UTC()
	tc := map[string]any{"identity": identity, "receiver_key_digest": q.Peer.Digest, "handoff_id": q.Handoff, "generation": q.Generation, "evolution_serial": "2", "effect_generation": "3", "observed_at": now.Format(time.RFC3339Nano), "valid_until": now.Add(2 * time.Second).Format(time.RFC3339Nano)}
	sig := strings.Repeat("e", 64)
	k := map[string]any{"schema": "podconnect.output_radio_absence_custody.v1", "ok": true, "purpose": "radio_absence_custody_v1", "hold": h, "original_phase": "suspended", "original_suspended_signature": sig, "phase": "source_evolved", "evolution": e, "target_continuity": tc, "physical_result_verified": false}
	raw, _ := json.Marshal(q)
	var outer map[string]any
	_ = json.Unmarshal(raw, &outer)
	outer["schema"] = "podconnect.native_radio_handoff_custody.v1"
	outer["phase"] = "source_evolved"
	outer["terminal"] = false
	outer["custody_only"] = true
	outer["source_proof"] = h
	outer["original_suspended_signature"] = sig
	outer["custody_proof"] = k
	binding, _ := json.Marshal(h)
	native := nativeProducerHold(radioProducerNativeReply{radioProducerNativeRequest: q, Phase: "prepared", Proof: binding, ObservedAt: now.Format(time.RFC3339Nano)})
	return q, native, outer
}
func custodyRaw(t *testing.T, body map[string]any) []byte {
	t.Helper()
	raw, e := json.Marshal(body)
	if e != nil {
		t.Fatal(e)
	}
	return raw
}
func custodyMap(body map[string]any, path ...string) map[string]any {
	for _, key := range path {
		body = body[key].(map[string]any)
	}
	return body
}

func TestRadioProducerCustodyClosedUnionAndNoGrant(t *testing.T) {
	q, h, body := custodyFixture(t)
	var mode atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/radio/handoff/state" {
			_ = json.NewEncoder(w).Encode(custodyNativeStateFixture())
			return
		}
		if mode.Load() == 0 {
			_, _ = w.Write(custodyRaw(t, body))
			return
		}
		env, _ := nativeProducerEnvelope(h)
		_ = json.NewEncoder(w).Encode(radioProducerNativeReply{radioProducerNativeRequest: q, Phase: "suspended", Proof: env.Binding, ObservedAt: time.Now().UTC().Format(time.RFC3339Nano), SuspendedSignature: strings.Repeat("e", 64)})
	}))
	defer server.Close()
	n, _ := newRadioProducerNativeClient(server.URL, "inert")
	result, err := n.Result(context.Background(), radioProducerReserve{}, h)
	if err != nil || result.Custody == nil || result.Hold != nil || result.Terminal || result.Outcome != "" || result.Observation.SourceKind != "" || len(n.holds) != 0 || len(n.established) != 0 {
		t.Fatalf("historical custody became source authority: %#v %v", result, err)
	}
	if _, err = n.Reserve(context.Background(), radioProducerReserve{radioProducerIdentity: h.radioProducerIdentity}); err == nil {
		t.Fatal("custody accepted for first reserve")
	}
	if _, err = n.invoke(context.Background(), "suspend", q); err == nil {
		t.Fatal("custody accepted as suspension")
	}
	if _, err = n.Result(context.Background(), radioProducerReserve{}, radioProducerHold{}); err == nil {
		t.Fatal("custody acquired a missing known hold")
	}
	if _, err = n.retainedSuspended(context.Background(), h, result.Custody); err == nil {
		t.Fatal("custody established original suspended evidence")
	}
	mode.Store(1)
	if _, err = n.Suspended(context.Background(), h); err != nil {
		t.Fatal(err)
	}
	if len(n.established) != 1 {
		t.Fatal("actual normal signed suspension not retained")
	}
	mode.Store(0)
	result, err = n.Result(context.Background(), radioProducerReserve{}, h)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = n.retainedSuspended(context.Background(), h, result.Custody); err != nil {
		t.Fatal("established suspension plus fresh target continuity rejected", err)
	}
	if len(n.holds) != 1 || n.holds[q.Handoff].Phase != "suspended" {
		t.Fatal("custody overwrote original hold cache")
	}
}

// Evolution after the real SUSPEND acknowledgement but before FIRST Start has
// no prior sender ownership. It stays quarantined; retained reuse must not grow
// an implicit first-grant continuation protocol.
func TestRadioProducerCustodyEvolutionBeforeFirstSenderStartStaysUnknown(t *testing.T) {
	q, h, body := custodyFixture(t)
	c, err := decodeProducerNativeCustody(custodyRaw(t, body), q)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/radio/handoff/state" {
			t.Error("unexpected effect request", r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(custodyNativeStateFixture())
	}))
	defer server.Close()
	n, _ := newRadioProducerNativeClient(server.URL, "inert")
	env, _ := nativeProducerEnvelope(h)
	if err = n.rememberSuspended(h, radioProducerNativeReply{radioProducerNativeRequest: q, Phase: "suspended", Proof: env.Binding, SuspendedSignature: *c.OriginalSignature}); err != nil {
		t.Fatal(err)
	}
	s := &radioProducerSenderClient{native: n, process: q.Peer.Process}
	signature, err := producerSenderStartSignature(radioProducerNativeReply{Custody: c}, errRadioUnknown, func(c *radioProducerNativeCustody) (string, error) {
		return s.ValidateRetainedTarget(context.Background(), h, c)
	})
	if err == nil || signature != "" || len(s.owned) != 0 {
		t.Fatal("post-SUSPEND evolution silently granted first sender Start")
	}
}

func TestRadioProducerCustodyRejectsForeignMixedAndStale(t *testing.T) {
	cases := map[string]func(map[string]any){
		"numericPID": func(b map[string]any) { custodyMap(b, "source_proof", "source_absence", "native_process")["pid"] = 42 },
		"stringAliasRevision": func(b map[string]any) {
			custodyMap(b, "custody_proof", "evolution", "start_future", "intended_native_owner")["alias_revision"] = "0"
		},
		"closedGate": func(b map[string]any) {
			custodyMap(b, "custody_proof", "evolution", "completion")["gate_closed"] = true
		},
		"noEffects": func(b map[string]any) {
			custodyMap(b, "custody_proof", "evolution", "completion")["possible_effect"] = false
		},
		"notDrained": func(b map[string]any) {
			custodyMap(b, "custody_proof", "evolution", "completion")["contexts_drained"] = false
		},
		"deadlineRetired": func(b map[string]any) {
			custodyMap(b, "custody_proof", "evolution", "completion")["deadline_retired"] = true
		},
		"missing":           func(b map[string]any) { delete(b, "terminal") },
		"grantfield":        func(b map[string]any) { b["observed_at"] = time.Now().Format(time.RFC3339Nano) },
		"terminal":          func(b map[string]any) { b["terminal"] = true },
		"nullterminal":      func(b map[string]any) { b["terminal"] = nil },
		"ordinaryschema":    func(b map[string]any) { b["schema"] = "podconnect.native_radio_handoff.v1" },
		"foreignquery":      func(b map[string]any) { b["expected_native_revision"] = "44" },
		"phase":             func(b map[string]any) { b["phase"] = "evolution_unknown" },
		"rotatedsignature":  func(b map[string]any) { b["original_suspended_signature"] = strings.Repeat("f", 64) },
		"preparedsignature": func(b map[string]any) { custodyMap(b, "custody_proof")["original_phase"] = "prepared" },
		"pendingcontinuity": func(b map[string]any) {
			b["phase"] = "evolution_pending"
			custodyMap(b, "custody_proof")["phase"] = "evolution_pending"
			custodyMap(b, "custody_proof", "evolution")["completion"] = nil
		},
		"nocompletion": func(b map[string]any) { custodyMap(b, "custody_proof", "evolution")["completion"] = nil },
		"futuretoken": func(b map[string]any) {
			custodyMap(b, "custody_proof", "evolution")["start_token"] = strings.Repeat("f", 32)
		},
		"otherC":      func(b map[string]any) { custodyMap(b, "custody_proof", "evolution")["c_process"] = attentionUUID() },
		"otherNative": func(b map[string]any) { custodyMap(b, "source_proof", "source_absence", "native_process")["pid"] = 43 },
		"otherhold":   func(b map[string]any) { custodyMap(b, "custody_proof", "hold")["token"] = strings.Repeat("0", 32) },
		"firsttarget": func(b map[string]any) {
			custodyMap(b, "custody_proof", "evolution", "start_future", "intended_native_owner")["homepod_id"] = "123"
		},
		"completionextra": func(b map[string]any) {
			custodyMap(b, "custody_proof", "evolution", "completion")["new_permission"] = true
		},
		"completionnullowner": func(b map[string]any) {
			custodyMap(b, "custody_proof", "evolution", "completion")["actual_native_owner"] = nil
		},
		"wrongcompletedformatter": func(b map[string]any) {
			custodyMap(b, "custody_proof", "evolution", "completion")["schema"] = "podconnect.output_start_result.v1"
		},
		"expired": func(b map[string]any) {
			tc := custodyMap(b, "custody_proof", "target_continuity")
			tc["observed_at"] = time.Now().Add(-3 * time.Second).UTC().Format(time.RFC3339Nano)
			tc["valid_until"] = time.Now().Add(-time.Second).UTC().Format(time.RFC3339Nano)
		},
		"future": func(b map[string]any) {
			tc := custodyMap(b, "custody_proof", "target_continuity")
			tc["observed_at"] = time.Now().Add(time.Second).UTC().Format(time.RFC3339Nano)
			tc["valid_until"] = time.Now().Add(2 * time.Second).UTC().Format(time.RFC3339Nano)
		},
		"longttl": func(b map[string]any) {
			custodyMap(b, "custody_proof", "target_continuity")["valid_until"] = time.Now().Add(3 * time.Second).UTC().Format(time.RFC3339Nano)
		},
		"newincarnation": func(b map[string]any) {
			custodyMap(b, "custody_proof", "target_continuity", "identity")["device_incarnation"] = "2"
		},
		"unboundserial": func(b map[string]any) { custodyMap(b, "custody_proof", "target_continuity")["evolution_serial"] = "9" },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			q, _, original := custodyFixture(t)
			var b map[string]any
			_ = json.Unmarshal(custodyRaw(t, original), &b)
			mutate(b)
			if _, e := decodeProducerNativeCustody(custodyRaw(t, b), q); e == nil {
				t.Fatal("invalid custody branch accepted")
			}
		})
	}
	q, _, body := custodyFixture(t)
	raw := custodyRaw(t, body)
	raw = []byte(strings.Replace(string(raw), `"custody_only":true`, `"custody_only":true,"custody_only":true`, 1))
	if _, e := decodeProducerNativeCustody(raw, q); e == nil {
		t.Fatal("duplicate authority field accepted")
	}
	q.Token = ""
	if _, e := decodeProducerNativeCustody(custodyRaw(t, body), q); e == nil {
		t.Fatal("token-empty custody recovery accepted")
	}
}

func TestRadioProducerCustodyPendingAndUnknownAreAuditOnly(t *testing.T) {
	for _, phase := range []string{"evolution_pending", "evolution_unknown"} {
		t.Run(phase, func(t *testing.T) {
			q, _, b := custodyFixture(t)
			b["phase"] = phase
			k := custodyMap(b, "custody_proof")
			k["phase"] = phase
			k["target_continuity"] = nil
			custodyMap(b, "custody_proof", "evolution")["completion"] = nil
			c, e := decodeProducerNativeCustody(custodyRaw(t, b), q)
			if e != nil || c.targetCurrent(time.Now()) {
				t.Fatalf("audit state became reuse authority %v", e)
			}
		})
	}
}

func custodyOwnedFixture(t *testing.T) (*radioProducerNativeClient, radioProducerHold, radioProducerHold, radioProducerIdentity, radioProducerSenderReply, *radioProducerNativeCustody) {
	t.Helper()
	q, native, body := custodyFixture(t)
	c, e := decodeProducerNativeCustody(custodyRaw(t, body), q)
	if e != nil {
		t.Fatal(e)
	}
	env, _ := nativeProducerEnvelope(native)
	n := &radioProducerNativeClient{established: map[string]radioProducerNativeReply{q.Handoff: {radioProducerNativeRequest: q, Phase: "suspended", Proof: env.Binding, SuspendedSignature: *c.OriginalSignature}}}
	op := radioProducerIdentity{Process: q.Peer.Process, Handoff: attentionUUID(), Generation: 20, ActionID: "opaque", Target: q.Target, OwnerGeneration: 1001}
	request := producerSenderBase(c.Binding.Output.Device, op)
	request["radio_process"] = op.Process
	request["radio_owner_epoch"] = "1001"
	request["sender_token"] = strings.Repeat("f", 32)
	request["hold"] = env.Binding
	proof, _ := json.Marshal(radioProducerSenderEnvelope{Request: request, Native: native})
	hold := radioProducerHold{radioProducerIdentity: op, SourceGeneration: 3, Token: strings.Repeat("f", 32), Proof: proof}
	r := radioProducerSenderReply{Process: op.Process, Device: c.Binding.Output.Device, Epoch: "1001", SourceGeneration: "3", Generation: "20", Operation: op.Handoff, Token: hold.Token, Digest: c.Binding.Digest, Phase: "playing", Observed: true}
	return n, native, hold, op, r, c
}
func TestRadioProducerRetainedOwnershipNeedsActualPriorAndIndependentEpoch(t *testing.T) {
	n, native, hold, op, r, c := custodyOwnedFixture(t)
	s := &radioProducerSenderClient{native: n, process: op.Process, admitted: map[string]radioProducerIdentity{op.Handoff: op}}
	r.Phase = "prepared"
	s.rememberOwned(hold, op, r)
	if len(s.owned) != 0 {
		t.Fatal("new prepared peer created prior ownership")
	}
	r.Phase = "drained"
	s.rememberOwned(hold, op, r)
	if len(s.owned) != 0 {
		t.Fatal("never-owned candidate created restart archive")
	}
	r.Phase = "playing"
	s.rememberOwned(hold, op, r)
	prior, ok := s.owned[native.Handoff]
	if !ok || !retainedOwnershipMatches(native, prior, op.Process) || !retainedSenderCurrent(prior, r, c, time.Now()) {
		t.Fatal("actual established playing ownership rejected")
	}
	stop := op
	stop.Handoff = attentionUUID()
	stop.Generation++
	r.Operation = stop.Handoff
	r.Generation = "21"
	r.Phase = "drained"
	s.rememberOwned(hold, stop, r)
	prior = s.owned[native.Handoff]
	if prior.Phase != "drained" || !retainedSenderCurrent(prior, r, c, time.Now()) {
		t.Fatal("confirmed unrelinquished drain did not preserve restart authority")
	}
	for _, mutation := range []string{"source", "epoch", "token", "challenge", "signature", "process", "unknown"} {
		copy := r
		switch mutation {
		case "source":
			copy.SourceGeneration = "1001"
		case "epoch":
			copy.Epoch = "3"
		case "token":
			copy.Token = strings.Repeat("0", 32)
		case "challenge":
			copy.Challenge = strings.Repeat("1", 32)
		case "signature":
			copy.Signature = strings.Repeat("2", 64)
		case "process":
			copy.Process = attentionUUID()
		case "unknown":
			copy.Phase = "unknown"
		}
		if retainedSenderCurrent(prior, copy, c, time.Now()) {
			t.Fatal("foreign/relinquished observation admitted", mutation)
		}
	}
	newClient := &radioProducerSenderClient{native: &radioProducerNativeClient{}, process: op.Process, admitted: map[string]radioProducerIdentity{op.Handoff: op}}
	r.Operation = op.Handoff
	r.Generation = "20"
	r.Phase = "playing"
	newClient.rememberOwned(hold, op, r)
	if len(newClient.owned) != 0 {
		t.Fatal("restart lacking actual NativeSuspended evidence adopted old source")
	}
}

// A policy-port fixture exercises all orchestration call points; actual C
// state/kernel evidence remains a separate acceptance gate, never overridden.
type custodyPolicySender struct {
	producerSenderFixture
	c       *radioProducerNativeCustody
	checks  *int
	allowed *bool
}

func (s custodyPolicySender) ValidateRetainedTarget(_ context.Context, _ radioProducerHold, c *radioProducerNativeCustody) (string, error) {
	*s.checks++
	if !*s.allowed || c != s.c || !c.targetCurrent(time.Now()) {
		return "", errRadioUnknown
	}
	return *c.OriginalSignature, nil
}
func (s custodyPolicySender) Start(ctx context.Context, h, n radioProducerHold, op radioProducerIdentity, station, target string) error {
	if s.c != nil {
		if _, e := producerSenderStartSignature(radioProducerNativeReply{Custody: s.c}, errRadioUnknown, func(c *radioProducerNativeCustody) (string, error) { return s.ValidateRetainedTarget(ctx, n, c) }); e != nil {
			return e
		}
	}
	return s.producerSenderFixture.Start(ctx, h, n, op, station, target)
}
func TestRadioProducerCustodyStationReuseChecksThreeBoundariesNoExtraPause(t *testing.T) {
	p, f := producerTest(t, true)
	_, _, _ = p.Ready(context.Background())
	first := producerStart(t, p, "initial", "r1", 1)
	original := p.records[first.Token]
	q, _, body := custodyFixture(t)
	custody, e := decodeProducerNativeCustody(custodyRaw(t, body), q)
	if e != nil {
		t.Fatal(e)
	}
	checks, allowed := 0, true
	p.sender = custodyPolicySender{producerSenderFixture{f}, custody, &checks, &allowed}
	f.nativeResults[original.Native.ActionID] = radioProducerResult{Identity: original.Native.radioProducerIdentity, Custody: custody}
	f.sourceGeneration = 1002
	ctx := context.Background()
	if e = p.PreflightOperation(ctx, "p1", "r1", "station", 2); e != nil {
		t.Fatal(e)
	}
	fence, e := p.Reserve(ctx, "r1", "r1", "station", 2)
	if e != nil {
		t.Fatal(e)
	}
	if checks != 1 {
		t.Fatal("retained Reserve skipped continuity")
	}
	if e = p.Stop(ctx, fence, "r1"); e != nil {
		t.Fatal(e)
	}
	if e = p.PauseAndRelease(ctx, fence, "r1"); e != nil {
		t.Fatal(e)
	}
	if checks != 2 {
		t.Fatal("reused PauseAndRelease skipped fresh continuity")
	}
	if e = p.Start(ctx, fence, "p1", "r1"); e != nil {
		t.Fatal(e)
	}
	if checks != 3 {
		t.Fatal("concrete Start signature policy skipped fresh continuity")
	}
	if _, e = p.Finish(ctx, fence); e != nil {
		t.Fatal(e)
	}
	pauses, reserves := 0, 0
	for _, call := range f.calls {
		if call == "pause-suspend:r1" {
			pauses++
		}
		if call == "native-reserve" {
			reserves++
		}
	}
	if pauses != 1 || reserves != 1 {
		t.Fatal("station reuse reacquired or paused Spotify", f.calls)
	}
	observation, e := p.Observe(ctx)
	if e != nil || observation.SourceKind != "radio" || observation.StationID != "p1" || observation.OwnerGeneration != 1002 {
		t.Fatal("custody displaced actual sender observation", observation, e)
	}
	before := len(f.calls)
	if _, e = p.Reconcile(ctx, fence); e == nil || len(f.calls) != before || checks != 3 {
		t.Fatal("custody settled unknown or replayed effect")
	}
}
func TestRadioProducerCustodyLostContinuityCannotAdvanceStation(t *testing.T) {
	for _, at := range []int{1, 2, 3} {
		t.Run(string(rune('0'+at)), func(t *testing.T) {
			p, f := producerTest(t, true)
			_, _, _ = p.Ready(context.Background())
			first := producerStart(t, p, "initial", "r1", 1)
			original := p.records[first.Token]
			q, _, body := custodyFixture(t)
			c, e := decodeProducerNativeCustody(custodyRaw(t, body), q)
			if e != nil {
				t.Fatal(e)
			}
			checks, allowed := 0, true
			p.sender = custodyPolicySender{producerSenderFixture{f}, c, &checks, &allowed}
			f.nativeResults[original.Native.ActionID] = radioProducerResult{Identity: original.Native.radioProducerIdentity, Custody: c}
			ctx := context.Background()
			if e = p.PreflightOperation(ctx, "p1", "r1", "station", 2); e != nil {
				t.Fatal(e)
			}
			if at == 1 {
				allowed = false
			}
			fence, e := p.Reserve(ctx, "r1", "r1", "station", 2)
			if at == 1 {
				if e == nil {
					t.Fatal("lost Reserve continuity accepted")
				}
				return
			}
			if e != nil {
				t.Fatal(e)
			}
			if at == 2 {
				allowed = false
			}
			e = p.PauseAndRelease(ctx, fence, "r1")
			if at == 2 {
				if e == nil {
					t.Fatal("lost PauseAndRelease continuity accepted")
				}
				return
			}
			if e != nil {
				t.Fatal(e)
			}
			allowed = false
			before := len(f.calls)
			if e = p.Start(ctx, fence, "p1", "r1"); e == nil || len(f.calls) != before {
				t.Fatal("lost Start continuity constructed radio")
			}
		})
	}
}
