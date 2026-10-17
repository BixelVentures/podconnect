package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

type transportFixture struct {
	Scope               roomTransportScope
	Native              roomTransportIdentity
	Route               outputTransportIdentity
	Writes              []string
	NativePrepareMutate func(*receiverTransportPrepare)
	OutputPrepareMutate func(*outputTransportPrepare)
	CommitBad           bool
	Claim               startClaim
}

func roomTransportFixture(t *testing.T) *transportFixture {
	t.Helper()
	claim, id, _ := queueFixture(t)
	claim.Proof.HomepodID = "42"
	claim.Proof.OutputVolume = 27
	f := &transportFixture{Scope: roomTransportScope{id, "r0", "42", "native-user"}, Native: roomTransportIdentity{"native-user", id, "r0", "42", "connection", "session", claim.Proof.AliasBinding.Incarnation, claim.Proof.AliasBinding.Registry, 1}, Route: outputTransportIdentity{"aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee", "42", "1", "2", "3", "4", "5", "6", configuredTransportInput(claim)}, Claim: claim}
	oldClaim, oldRPC := roomTransportClaim, roomTransportRPC
	roomTransportStore.Lock()
	roomTransportStore.Busy = false
	roomTransportStore.Offer = nil
	roomTransportStore.Unlock()
	t.Cleanup(func() {
		roomTransportClaim = oldClaim
		roomTransportRPC = oldRPC
		roomTransportStore.Lock()
		roomTransportStore.Busy = false
		roomTransportStore.Offer = nil
		roomTransportStore.Unlock()
	})
	roomTransportClaim = func(room, target string) (startClaim, bool) {
		return f.Claim, room == f.Scope.Room && target == f.Scope.Target
	}
	roomTransportRPC = func(ctx context.Context, base, method, path string, body []byte) ([]byte, error) {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if method == http.MethodPost && !strings.HasSuffix(path, "/bound-state") {
			f.Writes = append(f.Writes, path)
		}
		switch {
		case strings.HasPrefix(path, "/player/room_transport/bound-state?"):
			return json.Marshal(receiverTransportState{"podconnect.native_room_transport_state.v2", true, f.Native, []string{"pause", "resume", "next", "previous"}, false})
		case path == "/api/outputs/42/transport/bound-state":
			var request struct {
				Owner roomTransportIdentity `json:"native_owner"`
			}
			if method != http.MethodPost || decodeQueueManagerJSON(body, &request) != nil || request.Owner != f.Native {
				t.Errorf("Ccompatibility probe did not use actual native owner: %s", body)
			}
			return json.Marshal(outputTransportState{"podconnect.output_transport_state.v2", true, f.Route, false, []string{"pause", "resume", "next", "previous"}, false, false, true})
		case path == "/player/room_transport/bound-prepare":
			var request map[string]any
			if json.Unmarshal(body, &request) != nil {
				t.Fatal("invalid native preparation")
			}
			if len(request) != 6 || request["homepod_id"] != "42" || request["action"] != "resume" || request["volume_percent"] != float64(0) {
				t.Fatalf("wrong native request %s", body)
			}
			p := receiverTransportPrepare{"podconnect.native_room_transport.v2", strings.Repeat("a", 32), f.Native, "resume", 0, strings.Repeat("b", 64), 5000, "1"}
			if f.NativePrepareMutate != nil {
				f.NativePrepareMutate(&p)
			}
			return json.Marshal(p)
		case path == "/api/outputs/42/transport/bound-prepare":
			var request map[string]any
			_ = json.Unmarshal(body, &request)
			if len(request) != 5 || request["native_prepare_id"] != strings.Repeat("a", 32) || request["args_digest"] != strings.Repeat("b", 64) || request["native_revision"] != "1" {
				t.Fatalf("unbound output preparation %s", body)
			}
			p := outputTransportPrepare{"podconnect.output_transport_prepare.v2", true, strings.Repeat("c", 32), f.Route, f.Native, "1", 10000}
			if f.OutputPrepareMutate != nil {
				f.OutputPrepareMutate(&p)
			}
			return json.Marshal(p)
		case path == "/player/room_transport/bound-commit":
			var request map[string]any
			_ = json.Unmarshal(body, &request)
			if len(request) != 2 || request["prepare_id"] != strings.Repeat("a", 32) || request["own_tone_token"] != strings.Repeat("c", 32) {
				t.Fatalf("manager echoed invented pin %s", body)
			}
			if f.CommitBad {
				return []byte(`{"schema":"wrong"}`), nil
			}
			return []byte(`{"schema":"podconnect.native_room_transport_commit.v2","accepted_native":true,"prepare_id":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","physical_result_verified":false}`), nil
		default:
			t.Fatalf("unexpected transport path %s %s", method, path)
			return nil, fmt.Errorf("unexpected path")
		}
	}
	return f
}

func TestRoomTransportBoundRevisionAndOwnerCannotBeAssertedOrDowngraded(t *testing.T) {
	for _, revision := range []string{"", "0", "01", "+1", "18446744073709551616"} {
		t.Run("revision_"+revision, func(t *testing.T) {
			f := roomTransportFixture(t)
			f.NativePrepareMutate = func(p *receiverTransportPrepare) { p.IntentRevision = revision }
			w := f.prepare(t)
			if w.Code != 409 {
				t.Fatal("invalid actor revision accepted", revision, w.Code)
			}
		})
	}
	for name, change := range map[string]func(*outputTransportPrepare){
		"otherRoom":       func(p *outputTransportPrepare) { p.Owner.Room = "r6" },
		"aliasABA":        func(p *outputTransportPrepare) { p.Owner.Revision += 2 },
		"replacedSession": func(p *outputTransportPrepare) { p.Owner.Session = "replacement" },
		"revision":        func(p *outputTransportPrepare) { p.Revision = "2" },
		"oldSchema":       func(p *outputTransportPrepare) { p.Schema = "podconnect.output_transport_prepare.v1" },
	} {
		t.Run(name, func(t *testing.T) {
			f := roomTransportFixture(t)
			f.OutputPrepareMutate = change
			if w := f.prepare(t); w.Code != 409 {
				t.Fatal("foreign/old Ccustody accepted", w.Code, w.Body.String())
			}
		})
	}
	t.Run("noPrivateProofLeak", func(t *testing.T) {
		f := roomTransportFixture(t)
		w := f.prepare(t)
		if w.Code != 200 {
			t.Fatal(w.Code, w.Body.String())
		}
		for _, key := range []string{`"native_owner"`, `"native_revision"`, `"token"`, `"own_tone_token"`} {
			if strings.Contains(w.Body.String(), key) {
				t.Fatal("private Cauthority leaked", key)
			}
		}
	})
}
func (f *transportFixture) prepare(t *testing.T) *httptest.ResponseRecorder {
	t.Helper()
	body, _ := json.Marshal(roomTransportRequest{f.Scope, f.Native, f.Route, "resume", 0})
	out := httptest.NewRecorder()
	transportPrepareHandler(out, httptest.NewRequest("POST", "/api/room-transport/prepare", bytes.NewReader(body)))
	return out
}
func transportCommit(t *testing.T) *httptest.ResponseRecorder {
	t.Helper()
	out := httptest.NewRecorder()
	transportCommitHandler(out, httptest.NewRequest("POST", "/api/room-transport/commit", strings.NewReader(`{"prepare_id":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}`)))
	return out
}
func TestRoomTransportStateJoinsActualOwnersWithoutTokensOrEffects(t *testing.T) {
	f := roomTransportFixture(t)
	q := url.Values{"target_id": {f.Scope.Target}, "room_id": {"r0"}, "homepod_id": {"42"}, "account_user_id": {"native-user"}}
	out := httptest.NewRecorder()
	transportStateHandler(out, httptest.NewRequest("GET", "/api/room-transport/state?"+q.Encode(), nil))
	if out.Code != 200 || !strings.Contains(out.Body.String(), `"homepod_id":"42"`) {
		t.Fatal(out.Code, out.Body.String())
	}
	if strings.Contains(out.Body.String(), "start_owned") || strings.Contains(out.Body.String(), "clean_absent") {
		t.Fatal("private C proof leaked into Control's existing public schema")
	}
	if len(f.Writes) != 0 {
		t.Fatal("read minted lease or wrote", f.Writes)
	}
}
func TestRoomTransportCommitConsumesOfferAndUsesNativeOwnerNotWebAPI(t *testing.T) {
	f := roomTransportFixture(t)
	p := f.prepare(t)
	if p.Code != 200 {
		t.Fatal(p.Code, p.Body.String())
	}
	c := transportCommit(t)
	if c.Code != 200 || !strings.Contains(c.Body.String(), `"outcome":"accepted"`) {
		t.Fatal(c.Code, c.Body.String())
	}
	if transportCommit(t).Code != 409 {
		t.Fatal("replay admitted")
	}
	want := []string{"/player/room_transport/bound-prepare", "/api/outputs/42/transport/bound-prepare", "/player/room_transport/bound-commit"}
	if fmt.Sprint(f.Writes) != fmt.Sprint(want) {
		t.Fatal(f.Writes)
	}
	if f.Claim.Proof.OutputVolume != 27 {
		t.Fatal("same session volume reset")
	}
}
func TestRoomTransportAliasABAOrOutputSessionChangeDuringPrepareNeverCommit(t *testing.T) {
	for _, mutation := range []string{"alias_revision", "alias_room", "session", "output_route", "output_session", "output_input"} {
		t.Run(mutation, func(t *testing.T) {
			f := roomTransportFixture(t)
			if strings.HasPrefix(mutation, "output") {
				f.OutputPrepareMutate = func(p *outputTransportPrepare) {
					if mutation == "output_route" {
						p.Identity.Route = "9"
					} else if mutation == "output_input" {
						p.Identity.InputDigest = strings.Repeat("f", 64)
					} else {
						p.Identity.Session = "9"
					}
				}
			} else {
				f.NativePrepareMutate = func(p *receiverTransportPrepare) {
					switch mutation {
					case "alias_revision":
						p.Revision++
					case "alias_room":
						p.Room = "r6"
					case "session":
						p.Session = "replacement"
					}
				}
			}
			p := f.prepare(t)
			if p.Code != 409 {
				t.Fatal(p.Code, p.Body.String())
			}
			if transportCommit(t).Code != 409 {
				t.Fatal("stale offer survived")
			}
			for _, path := range f.Writes {
				if path == "/player/room_transport/bound-commit" {
					t.Fatal("stale native effect sent")
				}
			}
		})
	}
}
func TestRoomTransportUnknownAfterPossibleSendIsConsumedWithoutRetry(t *testing.T) {
	f := roomTransportFixture(t)
	if f.prepare(t).Code != 200 {
		t.Fatal("prepare")
	}
	f.CommitBad = true
	c := transportCommit(t)
	if c.Code != 200 || !strings.Contains(c.Body.String(), `"outcome":"unknown"`) || !strings.Contains(c.Body.String(), `"retry_allowed":false`) {
		t.Fatal(c.Code, c.Body.String())
	}
	if transportCommit(t).Code != 409 {
		t.Fatal("unknown replay")
	}
	count := 0
	for _, path := range f.Writes {
		if path == "/player/room_transport/bound-commit" {
			count++
		}
	}
	if count != 1 {
		t.Fatal(count)
	}
}
func TestRoomTransportInvalidFlatNativeFieldsAndUnsupportedVolumeFailClosed(t *testing.T) {
	for _, raw := range []string{`null`, `{}`, `{"schema":null}`, `{"schema":"x","schema":"y"}`} {
		if decodeTransportPrepare([]byte(raw), new(receiverTransportPrepare)) == nil {
			t.Fatal("malformed native offer accepted", raw)
		}
	}
	f := roomTransportFixture(t)
	body, _ := json.Marshal(roomTransportRequest{f.Scope, f.Native, f.Route, "volume", 27})
	out := httptest.NewRecorder()
	transportPrepareHandler(out, httptest.NewRequest("POST", "/api/room-transport/prepare", bytes.NewReader(body)))
	if out.Code != 400 || len(f.Writes) != 0 {
		t.Fatal(out.Code, f.Writes)
	}
}

func TestRoomTransportForeignOwnToneInputCannotMintOffer(t *testing.T) {
	f := roomTransportFixture(t)
	f.Route.InputDigest = strings.Repeat("f", 64)
	out := f.prepare(t)
	if out.Code != 409 || len(f.Writes) != 0 {
		t.Fatalf("foreign OwnTone source admitted: %d %v", out.Code, f.Writes)
	}
}
