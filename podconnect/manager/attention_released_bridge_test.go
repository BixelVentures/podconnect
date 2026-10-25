package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// The actual bridge has no stop seam. Each case owns one bounded child process,
// as in TestPC10BridgeRejectsStaleTransport.
func TestReleasedRoomRetiresOriginalAttentionLease(t *testing.T) {
	mode := os.Getenv("PC29_RELEASE_BRIDGE_CHILD")
	if mode == "" {
		for _, mode := range []string{"unreleased", "released_paused", "released_stopped", "released_idle", "released_active", "released_foreign", "released_uncertain", "released_new_owner", "released_duplicate", "released_stale_epoch", "released_changed_base"} {
			t.Run(mode, func(t *testing.T) {
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				command := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestReleasedRoomRetiresOriginalAttentionLease$", "-test.timeout=4s")
				command.Env = append(os.Environ(), "PC29_RELEASE_BRIDGE_CHILD="+mode)
				output, err := command.CombinedOutput()
				if err != nil {
					t.Fatalf("actual bridge child: %v\n%s", err, output)
				}
			})
		}
		return
	}

	setupAttentionRoom(t)
	live := &glLive{}
	source, stop := pc11Source(t, live)
	defer close(stop)
	status := glStatus{Active: true, Paused: true, HasVol: true, VolPct: 35}
	if mode == "released_stopped" {
		status.Stopped = true
	}
	if mode == "released_idle" {
		status.Active, status.Paused = false, false
	}
	if !live.acceptStatus(pc11Request(t, live, source), status) {
		t.Fatal("paused source refused")
	}

	original := nativeAttentionIdentity{testNativeProcess, "42", "1", "1", "1", "1", "1", "7", 35, 5, false}
	var releaseWrites, otherWrites atomic.Int32
	var newOwnerAdmitted, directEffectAdmitted, duplicateChangedEpoch atomic.Bool
	var pendingQueries atomic.Int32
	var att *attention
	var duplicateOnce atomic.Bool
	var staleOnce atomic.Bool
	staleSeen := make(chan struct{})
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/outputs":
			fmt.Fprint(w, `{"outputs":[{"id":"42","name":"Kitchen HomePod","type":"AirPlay","selected":true,"volume":35}]}`)
		case "/api/outputs/42/attention", "/api/outputs/42/attention/8":
			if r.Method == http.MethodGet {
				if mode == "released_uncertain" {
					if r.URL.Query().Get("request_id") == testConversationB || r.URL.Path == "/api/outputs/42/attention/8" {
						pendingQueries.Add(1)
					}
					_ = json.NewEncoder(w).Encode(nativeAttentionResult{Identity: original, RequestID: testConversationB, Operation: "8", Action: "release", Outcome: "pending", Current: true})
					return
				}
				if mode == "released_new_owner" {
					if att.engageOwned(testConversationB, att.challenge(), true, 5, "other", defaultAttentionTTL, time.Now()) {
						newOwnerAdmitted.Store(true)
					}
					if _, allowed := custodyMutationAdmission(httptest.NewRecorder()); allowed {
						directEffectAdmitted.Store(true)
					}
				}
				if mode == "released_duplicate" && duplicateOnce.CompareAndSwap(false, true) {
					beforeNative, beforeRestore := att.nativeRevision, att.restoreRevision
					if !att.releaseOwned(testConversationA, att.challenge()) || att.nativeRevision != beforeNative || att.restoreRevision != beforeRestore {
						duplicateChangedEpoch.Store(true)
					}
				}
				if mode == "released_stale_epoch" && staleOnce.CompareAndSwap(false, true) {
					att.mu.Lock()
					att.nativeRevision++
					att.restoreRevision++
					att.mu.Unlock()
					close(staleSeen)
				}
				observed := original
				if mode == "released_foreign" {
					observed.Process = testConversationB
				}
				_ = json.NewEncoder(w).Encode(nativeAttentionObservation{nativeAttentionIdentity: observed, LastRelease: "0"})
				return
			}
			var command nativeAttentionCommand
			if r.Method != http.MethodPut || json.NewDecoder(r.Body).Decode(&command) != nil || command.Action != "release" || command.Expected != original || command.Cap != 0 || command.TTL != 0 {
				otherWrites.Add(1)
				http.Error(w, "unexpected native effect", http.StatusConflict)
				return
			}
			releaseWrites.Add(1)
			result := nativeAttentionResult{Identity: original, RequestID: command.RequestID, Operation: "8", Action: "release", Outcome: "protocol_accepted", ResponseCode: 200, CSeq: 8, Current: true}
			_ = json.NewEncoder(w).Encode(result)
		default:
			if r.Method != http.MethodGet {
				otherWrites.Add(1)
			}
			http.Error(w, "not fixture-owned", http.StatusNotFound)
		}
	}))
	defer backend.Close()
	room := roomByID("r0")
	room.OwnTone, room.Librespot, room.HomepodID = backend.URL, backend.URL, "42"
	if mode != "unreleased" {
		path := releasedPath(room)
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("released"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	att = attentionFor("r0")
	challenge := att.challenge()
	if !att.engageOwned(testConversationA, challenge, true, 5, "voice", defaultAttentionTTL, time.Now()) {
		t.Fatal("original attention admission refused")
	}
	begin := nativeAttentionResult{Identity: original, RequestID: testConversationA, Operation: "7", Action: "begin", Outcome: "protocol_accepted", ResponseCode: 200, CSeq: 7, Current: true}
	grant := &nativeAttentionGrant{Identity: original, Command: nativeAttentionCommand{Expected: original, RequestID: testConversationA, Action: "begin", Cap: 5, TTL: 2000}, Result: begin, Submitted: true, Revision: att.nativeRevision}
	att.nativeGrants = map[string]*nativeAttentionGrant{"42": grant}
	att.nativePending = true
	att.nativeResults = map[string]nativeAttentionResult{"42": begin}
	if mode == "released_paused" || mode == "released_changed_base" {
		grant.Origin = nativeAttentionOrigin{testNativeProcess, testConversationA}
		originalBase := backend.URL
		if mode == "released_changed_base" {
			originalBase += "/old"
		}
		att.custody = &attentionCustody{Version: 1, Room: "r0", path: filepath.Join(t.TempDir(), "custody"), Sessions: []*custodySession{{Process: attentionManagerProcess(), Revision: fmt.Sprint(att.admissionRevision), Nonce: testConversationA, Outputs: map[string]*custodyOutput{testConversationA: {Base: originalBase, DeviceID: "42", Origin: grant.Origin, Original: original}}}}}
		if mode == "released_paused" {
			if err := att.custody.save(); err != nil {
				t.Fatalf("persist original attention custody: %v", err)
			}
		}
	}
	if mode == "released_uncertain" {
		grant.Command = nativeAttentionCommand{Expected: original, RequestID: testConversationB, Action: "release"}
		grant.Result = nativeAttentionResult{Identity: original, RequestID: testConversationB, Operation: "8", Action: "release", Outcome: "pending", Current: true}
		grant.Uncertain = true
		att.nativeResults["42"] = grant.Result
	}
	if mode == "released_active" {
		go roomBridge(room, &boolFlag{}, live, att)
		time.Sleep(350 * time.Millisecond)
		if releaseWrites.Load() != 0 || otherWrites.Load() != 0 || !att.snapshot(time.Now()).Active {
			t.Fatal("active original owner was retired or released room received another effect")
		}
		return
	}
	expected := att.challenge()
	body := fmt.Sprintf(`{"room":"r0","session":%q,"expected":{"process":%q,"revision":%q}}`, testConversationA, expected.Process, expected.Revision)
	post := callAttention(attentionReleaseHandler, http.MethodPost, "/api/attention/release", body, nil)
	var initialReply attSnapshot
	if post.Code != http.StatusOK || json.Unmarshal(post.Body.Bytes(), &initialReply) != nil || initialReply.Outcome != "pending" || initialReply.Active || initialReply.Challenge != expected {
		t.Fatalf("original release POST did not report pending ownership: status=%d body=%s", post.Code, post.Body.String())
	}
	go roomBridge(room, &boolFlag{}, live, att)
	if mode == "released_stale_epoch" {
		select {
		case <-staleSeen:
		case <-time.After(time.Second):
			t.Fatal("native GET did not reach stale epoch injection")
		}
		deadline := time.After(500 * time.Millisecond)
		for {
			att.mu.Lock()
			busy := att.nativeBusy
			att.mu.Unlock()
			if !busy {
				if releaseWrites.Load() != 0 || otherWrites.Load() != 0 || att.snapshot(time.Now()).Outcome != "pending" || !fileExists(releasedPath(room)) {
					t.Fatal("stale post-GET owner dispatched or retired the original")
				}
				return
			}
			select {
			case <-deadline:
				t.Fatal("stale bridge read did not finish")
			case <-time.After(time.Millisecond):
			}
		}
	}
	if mode == "released_foreign" || mode == "released_uncertain" || mode == "released_changed_base" {
		time.Sleep(500 * time.Millisecond)
		get := callAttention(attentionHandler, http.MethodGet, "/api/attention", "", nil)
		var state struct {
			Rooms map[string]attSnapshot `json:"rooms"`
		}
		if get.Code != http.StatusOK || json.Unmarshal(get.Body.Bytes(), &state) != nil || state.Rooms["r0"].Outcome != "pending" || releaseWrites.Load() != 0 || otherWrites.Load() != 0 {
			t.Fatalf("foreign or uncertain original was credited or replayed: status=%d release=%d other=%d", get.Code, releaseWrites.Load(), otherWrites.Load())
		}
		if mode == "released_uncertain" && pendingQueries.Load() == 0 {
			t.Fatal("uncertain release did not poll its original request")
		}
		return
	}
	deadline := time.After(1200 * time.Millisecond)
	for {
		if att.snapshot(time.Now()).Outcome == "released" {
			get := callAttention(attentionHandler, http.MethodGet, "/api/attention", "", nil)
			var state struct {
				Rooms map[string]attSnapshot `json:"rooms"`
			}
			if get.Code != http.StatusOK || json.Unmarshal(get.Body.Bytes(), &state) != nil || state.Rooms["r0"].Outcome != "released" || state.Rooms["r0"].Challenge != expected {
				t.Fatalf("original release GET lacked terminal receipt: status=%d", get.Code)
			}
			if releaseWrites.Load() != 1 || mode != "unreleased" && otherWrites.Load() != 0 {
				t.Fatalf("release effects=%d other effects=%d", releaseWrites.Load(), otherWrites.Load())
			}
			if mode != "unreleased" && !fileExists(releasedPath(room)) {
				t.Fatal("attention retirement removed the released marker")
			}
			if mode == "released_paused" {
				var loaded attention
				loaded.loadCustody(att.custody.path, "r0")
				if loaded.custody == nil || loaded.custody.fault != nil || len(loaded.custody.Sessions) != 1 || !loaded.custody.Sessions[0].Retired || !loaded.custody.Sessions[0].Outputs[testConversationA].Retired {
					t.Fatal("exact original output and session were not durably retired")
				}
				again := callAttention(attentionReleaseHandler, http.MethodPost, "/api/attention/release", body, nil)
				var receipt attSnapshot
				if again.Code != http.StatusOK || json.Unmarshal(again.Body.Bytes(), &receipt) != nil || receipt.Outcome != "released" || receipt.Challenge != expected || releaseWrites.Load() != 1 {
					t.Fatal("duplicate original release lacked durable terminal receipt")
				}
			}
			if newOwnerAdmitted.Load() || directEffectAdmitted.Load() || duplicateChangedEpoch.Load() {
				t.Fatal("new owner, direct effect, or duplicate release changed the original admission")
			}
			return
		}
		select {
		case <-deadline:
			t.Fatalf("original lease still %s after bridge ticks; native release writes=%d other writes=%d", strings.ToLower(att.snapshot(time.Now()).Outcome), releaseWrites.Load(), otherWrites.Load())
		case <-time.After(25 * time.Millisecond):
		}
	}
}

func TestReleasedPlayingSTARTStillRetiresOriginalAttention(t *testing.T) {
	if os.Getenv("PC87_PLAYING_RELEASE_CHILD") == "" {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestReleasedPlayingSTARTStillRetiresOriginalAttention$", "-test.timeout=4s")
		cmd.Env = append(os.Environ(), "PC87_PLAYING_RELEASE_CHILD=1")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("actual bridge: %v\n%s", err, out)
		}
		return
	}
	f, _, owner, route := releasedRoomRouteFixture(t)
	var reads, releases, effects atomic.Int32
	parked := make(chan struct{})
	oldRPC := roomTransportRPC
	defer func() { roomTransportRPC = oldRPC }()
	roomTransportRPC = func(_ context.Context, _, method, path string, body []byte) ([]byte, error) {
		switch {
		case path == "/status":
			return []byte(fmt.Sprintf(`{"username":"native-user","device_id":%q}`, f.id)), nil
		case strings.HasPrefix(path, "/player/room_transport/bound-state?"):
			return json.Marshal(receiverTransportState{"podconnect.native_room_transport_state.v2", true, *owner, []string{"pause"}, nil, false})
		case path == "/api/outputs/42/transport/bound-state":
			var supplied struct {
				Owner roomTransportIdentity `json:"native_owner"`
			}
			if json.Unmarshal(body, &supplied) != nil || supplied.Owner != *owner {
				t.Fatal("C state not bound to exact Native owner")
			}
			reads.Add(1)
			if releases.Load() > 0 {
				// The real bridge has no stop seam. Park this child at its next
				// inert route read before fixture cleanup restores package globals.
				close(parked)
				<-make(chan struct{})
			}
			return json.Marshal(outputTransportState{"podconnect.output_transport_state.v2", true, *route, false, []string{"pause"}, false, true, false})
		}
		return nil, fmt.Errorf("unexpected route read %s %s", method, path)
	}
	original := nativeAttentionIdentity{testNativeProcess, "42", "1", "4", "3", "310", "3", "179", 35, 5, false}
	previous := http.DefaultTransport
	http.DefaultTransport = startTransport(func(r *http.Request) (*http.Response, error) {
		var value any
		if r.URL.Path == "/api/outputs/42/attention" && r.Method == http.MethodGet {
			value = nativeAttentionObservation{nativeAttentionIdentity: original, LastRelease: "0"}
		} else if r.URL.Path == "/api/outputs/42/attention" && r.Method == http.MethodPut {
			var c nativeAttentionCommand
			if json.NewDecoder(r.Body).Decode(&c) != nil || c.Action != "release" || c.Expected != original || c.Cap != 0 || c.TTL != 0 {
				effects.Add(1)
				return nil, fmt.Errorf("not exact original release")
			}
			releases.Add(1)
			value = nativeAttentionResult{Identity: original, RequestID: c.RequestID, Operation: "311", Action: "release", Outcome: "protocol_accepted", ResponseCode: 200, CSeq: 311, Current: true}
		} else {
			if r.Method != http.MethodGet {
				effects.Add(1)
			}
			return previous.RoundTrip(r)
		}
		raw, err := json.Marshal(value)
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(bytes.NewReader(raw))}, err
	})
	att := &f.rt.att
	if !att.engageOwned(testConversationA, att.challenge(), true, 5, "voice", defaultAttentionTTL, time.Now()) {
		t.Fatal("attention admission")
	}
	update := nativeAttentionResult{Identity: original, RequestID: testConversationB, Operation: "310", Action: "update", Outcome: "desired_only", Current: true}
	g := &nativeAttentionGrant{Origin: nativeAttentionOrigin{testNativeProcess, testConversationA}, Identity: original, Command: nativeAttentionCommand{Expected: original, RequestID: testConversationB, Action: "update", Cap: 5, TTL: 2000}, Result: update, Submitted: true, Revision: att.nativeRevision}
	att.nativeGrants = map[string]*nativeAttentionGrant{"42": g}
	att.nativeResults = map[string]nativeAttentionResult{"42": update}
	att.nativePending = true
	att.custody = &attentionCustody{Version: 1, Room: "r0", path: filepath.Join(t.TempDir(), "custody"), Sessions: []*custodySession{{Process: attentionManagerProcess(), Revision: fmt.Sprint(att.admissionRevision), Nonce: testConversationA, Outputs: map[string]*custodyOutput{testConversationA: {Base: f.room.OwnTone, DeviceID: "42", Origin: g.Origin, Original: original}}}}}
	if err := att.custody.save(); err != nil {
		t.Fatal(err)
	}
	if !att.releaseOwned(testConversationA, att.challenge()) {
		t.Fatal("release admission")
	}
	go roomBridge(f.room, &boolFlag{}, &f.rt.live, att)
	deadline := time.After(1200 * time.Millisecond)
	for {
		if att.snapshot(time.Now()).Outcome == "released" {
			select {
			case <-parked:
			case <-deadline:
				t.Fatal("bridge did not reach cleanup barrier")
			}
			if releases.Load() != 1 || effects.Load() != 0 || !fileExists(releasedPath(f.room)) {
				t.Fatalf("release=%d unrelated=%d marker=%v", releases.Load(), effects.Load(), fileExists(releasedPath(f.room)))
			}
			return
		}
		select {
		case <-deadline:
			t.Fatalf("original lease remains pending: START-owned reads=%d release writes=%d unrelated writes=%d", reads.Load(), releases.Load(), effects.Load())
		case <-time.After(25 * time.Millisecond):
		}
	}
}
