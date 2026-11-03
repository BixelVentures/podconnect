package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func producerNativeStateFixture() radioProducerNativeState {
	raw := json.RawMessage(`{"boot_id":"inert","pid":42,"proc_start_ticks":"100"}`)
	digest := sha256.Sum256(raw)
	return radioProducerNativeState{Schema: "podconnect.native_radio_handoff_state.v1", ProcessID: hex.EncodeToString(digest[:]), NativeProcess: raw, Revision: "7", Targets: []radioProducerTarget{{radioTarget: radioTarget{ID: "room-A", Availability: "unavailable"}, HomePodID: "123"}}}
}
func TestRadioProducerNativeWireUsesInternalUUIDAndIntentCAS(t *testing.T) {
	state := producerNativeStateFixture()
	var requests []radioProducerNativeRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-PodConnect-Token") != "inert-secret" {
			t.Error("missing owner auth")
		}
		if r.URL.Path == "/radio/handoff/state" {
			_ = json.NewEncoder(w).Encode(state)
			return
		}
		var q radioProducerNativeRequest
		if json.NewDecoder(r.Body).Decode(&q) != nil {
			t.Error("invalid body")
			return
		}
		requests = append(requests, q)
		phase := "reserved"
		signature := ""
		if strings.HasSuffix(r.URL.Path, "suspend") {
			phase = "suspended"
			signature = strings.Repeat("a", 64)
		}
		q.Token = strings.Repeat("a", 32)
		_ = json.NewEncoder(w).Encode(radioProducerNativeReply{radioProducerNativeRequest: q, Phase: phase, Proof: json.RawMessage(`{"real_owner_fixture":true,"initial_volume_percent":22}`), ObservedAt: time.Now().UTC().Format(time.RFC3339Nano), SuspendedSignature: signature})
	}))
	defer server.Close()
	client, e := newRadioProducerNativeClient(server.URL, "inert-secret")
	if e != nil {
		t.Fatal(e)
	}
	intent, e := client.CaptureIntent(context.Background(), "room-A")
	if e != nil {
		t.Fatal(e)
	}
	id := attentionUUID()
	q := radioProducerReserve{radioProducerIdentity: radioProducerIdentity{Process: state.ProcessID, Handoff: id, Generation: 10, ActionID: "opaque-not-a-uuid", Target: "room-A"}, ExpectedIntent: intent, Peer: radioProducerPeer{Process: attentionUUID(), OwnerGeneration: 1001, ReceiverDigest: strings.Repeat("b", 64)}}
	h, e := client.Reserve(context.Background(), q)
	if e != nil {
		t.Fatal(e)
	}
	if e = client.PauseSuspend(context.Background(), h, q.radioProducerIdentity); e != nil {
		t.Fatal(e)
	}
	if len(requests) != 2 || requests[0].Action != id || requests[1].Action != id || requests[0].ExpectedRevision != "7" || requests[0].Generation != "10" || requests[0].Peer.Epoch != "1001" {
		t.Fatalf("wrong wire %#v", requests)
	}
	if requests[1].Token != h.Token {
		t.Fatal("source token lost")
	}
}
func TestRadioProducerHTTPStrictBoundsAndRedirect(t *testing.T) {
	for _, body := range []string{`{"ok":true}` + strings.Repeat(" ", 65536), `{"ok":true} {}`, `{"ok":true,"extra":1}`} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(body)) }))
		client, _ := newRadioProducerHTTP(server.URL, "inert")
		var reply struct {
			OK bool `json:"ok"`
		}
		e := client.call(context.Background(), "/", nil, &reply)
		server.Close()
		if e == nil {
			t.Fatal("accepted oversized/trailing/unknown response")
		}
	}
	redirected := false
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { redirected = true }))
	defer target.Close()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusTemporaryRedirect)
	}))
	defer server.Close()
	client, _ := newRadioProducerHTTP(server.URL, "inert")
	var reply any
	if client.call(context.Background(), "/", nil, &reply) == nil || redirected {
		t.Fatal("redirect leaked authority")
	}
}
func TestRadioProducerNativeProcessReplacementRejected(t *testing.T) {
	state := producerNativeStateFixture()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _ = json.NewEncoder(w).Encode(state) }))
	defer server.Close()
	client, _ := newRadioProducerNativeClient(server.URL, "inert")
	if _, e := client.Evidence(context.Background()); e != nil {
		t.Fatal(e)
	}
	state.NativeProcess = json.RawMessage(`{"boot_id":"replacement","pid":42,"proc_start_ticks":"200"}`)
	digest := sha256.Sum256(state.NativeProcess)
	state.ProcessID = hex.EncodeToString(digest[:])
	if _, e := client.Evidence(context.Background()); e == nil {
		t.Fatal("source process replacement adopted")
	}
}

func TestRadioProducerNativeRejectsRotatedTokenAndEnvelopeAction(t *testing.T) {
	request := radioProducerNativeRequest{Schema: "podconnect.native_radio_handoff.v1", Process: strings.Repeat("a", 64), Target: "room-A", Handoff: attentionUUID(), Generation: "1", Action: attentionUUID(), Token: strings.Repeat("a", 32), ExpectedRevision: "7", Peer: radioProducerNativePeer{Process: attentionUUID(), Epoch: "1001", Digest: strings.Repeat("b", 64)}}
	reply := radioProducerNativeReply{radioProducerNativeRequest: request, Phase: "suspended", Proof: json.RawMessage(`{"fixture":true}`), ObservedAt: time.Now().UTC().Format(time.RFC3339Nano), SuspendedSignature: strings.Repeat("b", 64)}
	reply.Token = strings.Repeat("b", 32)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _ = json.NewEncoder(w).Encode(reply) }))
	defer server.Close()
	client, _ := newRadioProducerNativeClient(server.URL, "inert-secret")
	for _, action := range []string{"suspend", "result"} {
		if _, e := client.invoke(context.Background(), action, request); e == nil {
			t.Fatal("foreign source token accepted", action)
		}
	}
	if len(client.holds) != 0 {
		t.Fatal("invalid response poisoned source cache")
	}
	reply.Token = request.Token
	hold := nativeProducerHold(reply)
	hold.ActionID = attentionUUID()
	if _, e := nativeProducerEnvelope(hold); e == nil {
		t.Fatal("envelope action differs from immutable source hold")
	}
}

func TestRadioProducerNativeClosedOperationRejectionIsNotSourceHold(t *testing.T) {
	q := radioProducerNativeRequest{Schema: "podconnect.native_radio_handoff.v1", Process: strings.Repeat("a", 64), Target: "room-A", Handoff: attentionUUID(), Generation: "1", Action: attentionUUID(), ExpectedRevision: "7", Peer: radioProducerNativePeer{Process: attentionUUID(), Epoch: "1001", Digest: strings.Repeat("b", 64)}}
	body := map[string]any{}
	raw, _ := json.Marshal(q)
	_ = json.Unmarshal(raw, &body)
	body["schema"] = "podconnect.native_radio_handoff_rejected.v1"
	body["operation_rejected"] = true
	body["effects_started"] = false
	body["terminal"] = true
	body["reason"] = "stale_intent"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _ = json.NewEncoder(w).Encode(body) }))
	defer server.Close()
	client, _ := newRadioProducerNativeClient(server.URL, "inert-secret")
	for _, action := range []string{"reserve", "result"} {
		_, e := client.invoke(context.Background(), action, q)
		var rejected *radioProducerOperationRejected
		if !errors.As(e, &rejected) || len(client.holds) != 0 {
			t.Fatal("no-effect receipt became source hold", action, e)
		}
	}
	body["effects_started"] = true
	if _, e := client.invoke(context.Background(), "reserve", q); e == nil {
		t.Fatal("effectful rejection accepted")
	}
	body["effects_started"] = false
	body["proof"] = map[string]any{"forged": true}
	_, e := client.invoke(context.Background(), "result", q)
	var rejected *radioProducerOperationRejected
	if errors.As(e, &rejected) {
		t.Fatal("sourceproof injected into closed operation receipt")
	}
}

func TestRadioProducerNativePreemptedCompletionIsPassiveHistoricalOnly(t *testing.T) {
	q := radioProducerNativeRequest{Schema: "podconnect.native_radio_handoff.v1", Process: strings.Repeat("a", 64), Target: "room-A", Handoff: attentionUUID(), Generation: "1", Action: attentionUUID(), ExpectedRevision: "4", Token: strings.Repeat("b", 32), Peer: radioProducerNativePeer{Process: attentionUUID(), Epoch: "1001", Digest: strings.Repeat("c", 64)}}
	for _, mode := range []string{"valid", "reserve", "rotated", "missing-commit", "future", "extra"} {
		t.Run(mode, func(t *testing.T) {
			raw, _ := json.Marshal(q)
			var response map[string]any
			_ = json.Unmarshal(raw, &response)
			response["schema"] = "podconnect.native_radio_handoff_preempted.v1"
			response["phase"] = "preempted"
			response["terminal"] = true
			response["effects_started"] = true
			response["source_proof"] = map[string]any{"original": "binding"}
			response["sender_drain"] = map[string]any{"actual": "ticket"}
			response["native_commit"] = map[string]any{"actual": "commit"}
			response["committed_at"] = time.Now().Add(-time.Hour).UTC().Format(time.RFC3339Nano)
			switch mode {
			case "rotated":
				response["token"] = strings.Repeat("d", 32)
			case "missing-commit":
				delete(response, "native_commit")
			case "future":
				response["committed_at"] = time.Now().Add(time.Hour).UTC().Format(time.RFC3339Nano)
			case "extra":
				response["observed_at"] = time.Now().UTC().Format(time.RFC3339Nano)
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _ = json.NewEncoder(w).Encode(response) }))
			defer server.Close()
			client, _ := newRadioProducerNativeClient(server.URL, "inert")
			action := "result"
			if mode == "reserve" {
				action = "reserve"
			}
			result, err := client.invoke(context.Background(), action, q)
			if mode == "valid" {
				if err != nil || result.Phase != "preempted" || len(result.TerminalDrain) == 0 || result.ObservedAt != "" {
					t.Fatalf("historical completion lost: %#v %v", result, err)
				}
				if len(client.holds) != 0 {
					t.Fatal("terminal completion became custody grant")
				}
			} else if err == nil {
				t.Fatal("accepted invalid terminal receipt")
			}
		})
	}
}

func TestRadioProducerAuthenticationWitnessBindsActualSourceAndExpires(t *testing.T) {
	now := time.Now().UTC()
	r := radioProducerSenderReply{Device: "123", SourceGeneration: "9", ControllerID: "1234567890ABCDEF", Digest: strings.Repeat("a", 64), TargetAuthenticated: true}
	base := radioProducerTargetAuth{Schema: "podconnect.radio-target-auth.v1", Device: r.Device, Incarnation: "2", Route: "0", Source: "9", Controller: r.ControllerID, Digest: r.Digest, ObservedAt: now.Add(-time.Second).Format(time.RFC3339Nano), ValidUntil: now.Add(4 * time.Second).Format(time.RFC3339Nano)}
	for _, mode := range []string{"valid", "caps-pinned-controller", "metadata-only", "wrong-controller", "other-source", "expired", "future", "extended"} {
		t.Run(mode, func(t *testing.T) {
			copy := r
			auth := base
			copy.TargetAuth = &auth
			switch mode {
			case "caps-pinned-controller":
				copy.ControllerID = ""
			case "metadata-only":
				copy.TargetAuthenticated = false
			case "wrong-controller":
				auth.Controller = "ABCDEF1234567890"
			case "other-source":
				auth.Source = "8"
			case "expired":
				auth.ValidUntil = now.Add(-time.Second).Format(time.RFC3339Nano)
			case "future":
				auth.ObservedAt = now.Add(time.Second).Format(time.RFC3339Nano)
			case "extended":
				auth.ValidUntil = now.Add(6 * time.Second).Format(time.RFC3339Nano)
			}
			if producerTargetAuthenticated(copy, r.ControllerID, now) != (mode == "valid" || mode == "caps-pinned-controller") {
				t.Fatal("pair witness accepted wrong identity/freshness")
			}
		})
	}
}

func TestRadioProducerKnownNativeHoldRejectsRotatedSourceProof(t *testing.T) {
	q := radioProducerNativeRequest{Schema: "podconnect.native_radio_handoff.v1", Process: strings.Repeat("a", 64), Target: "room-A", Handoff: attentionUUID(), Generation: "1", Action: attentionUUID(), ExpectedRevision: "4", Token: strings.Repeat("b", 32), Peer: radioProducerNativePeer{Process: attentionUUID(), Epoch: "1001", Digest: strings.Repeat("c", 64)}}
	original := radioProducerNativeReply{radioProducerNativeRequest: q, Phase: "reserved", Proof: json.RawMessage(`{"source":"original"}`)}
	changed := original
	changed.Phase = "suspended"
	changed.Proof = json.RawMessage(`{"source":"foreign"}`)
	changed.ObservedAt = time.Now().UTC().Format(time.RFC3339Nano)
	changed.SuspendedSignature = strings.Repeat("d", 64)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _ = json.NewEncoder(w).Encode(changed) }))
	defer server.Close()
	client, _ := newRadioProducerNativeClient(server.URL, "inert")
	h := nativeProducerHold(original)
	if _, err := client.Result(context.Background(), radioProducerReserve{}, h); err == nil {
		t.Fatal("rotated source proof accepted on passive result")
	}
	if err := client.PauseSuspend(context.Background(), h, h.radioProducerIdentity); err == nil {
		t.Fatal("rotated source proof accepted on suspension")
	}
}

func TestRadioProducerPreparedAuthenticationWitnessCannotRotateTogether(t *testing.T) {
	now := time.Now().UTC()
	r := radioProducerSenderReply{Device: "123", SourceGeneration: "9", ControllerID: "1234567890ABCDEF", Digest: strings.Repeat("a", 64), TargetAuthenticated: true}
	prepared := radioProducerTargetAuth{Schema: "podconnect.radio-target-auth.v1", Device: "123", Incarnation: "1", Route: "0", Source: "9", Controller: r.ControllerID, Digest: r.Digest, ObservedAt: now.Add(-time.Second).Format(time.RFC3339Nano), ValidUntil: now.Add(4 * time.Second).Format(time.RFC3339Nano)}
	for _, change := range []string{"source", "route", "incarnation", "fresh-time", "key"} {
		t.Run(change, func(t *testing.T) {
			reply := r
			auth := prepared
			reply.TargetAuth = &auth
			switch change {
			case "source":
				reply.SourceGeneration = "10"
				auth.Source = "10"
			case "route":
				auth.Route = "1"
			case "incarnation":
				auth.Incarnation = "2"
			case "fresh-time":
				auth.ObservedAt = now.Format(time.RFC3339Nano)
				auth.ValidUntil = now.Add(5 * time.Second).Format(time.RFC3339Nano)
			case "key":
				reply.Digest = strings.Repeat("b", 64)
				auth.Digest = reply.Digest
			}
			if !producerTargetAuthenticated(reply, r.ControllerID, now) {
				t.Fatal("fixture should be internally valid fresh source reply")
			}
			if producerSameTargetAuth(&prepared, reply.TargetAuth) {
				t.Fatal("prepared authentication authority silently rotated")
			}
		})
	}
}

func TestRadioProducerNativeSuspendWaitsOnlyPassiveExactDrain(t *testing.T) {
	for _, mode := range []string{"drained", "deadline", "rotated", "unknown"} {
		t.Run(mode, func(t *testing.T) {
			q := radioProducerNativeRequest{Schema: "podconnect.native_radio_handoff.v1", Process: strings.Repeat("a", 64), Target: "room-A", Handoff: attentionUUID(), Generation: "3", Action: attentionUUID(), ExpectedRevision: "4", Token: strings.Repeat("b", 32), Peer: radioProducerNativePeer{Process: attentionUUID(), Epoch: "1001", Digest: strings.Repeat("c", 64)}}
			original := radioProducerNativeReply{radioProducerNativeRequest: q, Phase: "prepared", Proof: json.RawMessage(`{"source":"original"}`)}
			suspend, reads := 0, 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var actual radioProducerNativeRequest
				if json.NewDecoder(r.Body).Decode(&actual) != nil || actual != q {
					t.Error("operation or hold changed")
				}
				reply := original
				reply.ObservedAt = time.Now().UTC().Format(time.RFC3339Nano)
				reply.Phase = "stopping"
				switch r.URL.Path {
				case "/radio/handoff/suspend":
					suspend++
				case "/radio/handoff/result":
					reads++
					reply.Phase = "suspended"
					reply.SuspendedSignature = strings.Repeat("d", 64)
				default:
					t.Error("unexpected effect path")
				}
				if mode == "unknown" {
					reply.Phase = "unknown"
				}
				if mode == "rotated" {
					reply.Proof = json.RawMessage(`{"source":"foreign"}`)
				}
				_ = json.NewEncoder(w).Encode(reply)
			}))
			defer server.Close()
			client, _ := newRadioProducerNativeClient(server.URL, "inert")
			ctx := context.Background()
			if mode == "deadline" {
				var cancel context.CancelFunc
				ctx, cancel = context.WithTimeout(ctx, 25*time.Millisecond)
				defer cancel()
			}
			h := nativeProducerHold(original)
			err := client.PauseSuspend(ctx, h, h.radioProducerIdentity)
			if (err == nil) != (mode == "drained") {
				t.Fatalf("unexpected drain outcome %v", err)
			}
			if suspend != 1 || (mode == "drained" && reads != 1) || (mode != "drained" && reads != 0) {
				t.Fatalf("effect replay or unexpected reads suspend=%d reads=%d", suspend, reads)
			}
		})
	}
}
