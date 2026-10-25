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
	ShuffleEnabled      *bool
	RepeatMode          string
	AfterCommit         func()
	Scope               roomTransportScope
	Native              roomTransportIdentity
	Route               outputTransportIdentity
	Writes              []string
	NativePrepareMutate func(*receiverTransportPrepare)
	OutputPrepareMutate func(*outputTransportPrepare)
	CommitBad           bool
	Claim               startClaim
	Action              string
	PositionMS          *int64
}

func TestRoomEffectWireIsBoundToExactNativeOwnerAndRevision(t *testing.T) {
	owner := roomTransportIdentity{Account: "account", Target: "target", Room: "room", Homepod: "42", Connection: "connection", Session: "session", Incarnation: strings.Repeat("a", 32), Registry: strings.Repeat("b", 64), Revision: 1}
	effect := &receiverRoomEffect{strings.Repeat("c", 32), "pause", "1", "2", owner}
	if !validReceiverRoomEffect(effect, owner) {
		t.Fatal("exact native effect envelope rejected")
	}
	for _, mutated := range []*receiverRoomEffect{
		{effect.PrepareID, effect.Action, "18446744073709551616", effect.Intent, owner},
		{effect.PrepareID, effect.Action, effect.Revision, "0", owner},
		{effect.PrepareID, "play", effect.Revision, effect.Intent, owner},
		{effect.PrepareID, effect.Action, effect.Revision, effect.Intent, roomTransportIdentity{Account: "foreign"}},
	} {
		if validReceiverRoomEffect(mutated, owner) {
			t.Fatal("malformed or foreign effect crossed manager read", mutated)
		}
	}
}

func roomTransportFixture(t *testing.T) *transportFixture {
	t.Helper()
	claim, id, _ := queueFixture(t)
	claim.Proof.HomepodID = "42"
	claim.Proof.OutputVolume = 27
	f := &transportFixture{Scope: roomTransportScope{id, "r0", "42", "native-user"}, Native: roomTransportIdentity{"native-user", id, "r0", "42", "connection", "session", claim.Proof.AliasBinding.Incarnation, claim.Proof.AliasBinding.Registry, 1}, Route: outputTransportIdentity{"aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee", "42", "1", "2", "3", "4", "5", "6", configuredTransportInput(claim)}, Claim: claim}
	f.Action = "resume"
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
			return json.Marshal(receiverTransportState{"podconnect.native_room_transport_state.v2", true, f.Native, []string{"pause", "resume", "next", "previous", "seek", "shuffle", "repeat"}, nil, false})
		case path == "/api/outputs/42/transport/bound-state":
			var request struct {
				Owner roomTransportIdentity `json:"native_owner"`
			}
			if method != http.MethodPost || decodeQueueManagerJSON(body, &request) != nil || request.Owner != f.Native {
				t.Errorf("Ccompatibility probe did not use actual native owner: %s", body)
			}
			return json.Marshal(outputTransportState{"podconnect.output_transport_state.v2", true, f.Route, false, []string{"pause", "resume", "next", "previous", "seek"}, false, false, true})
		case path == "/player/room_transport/bound-prepare":
			var request map[string]any
			if json.Unmarshal(body, &request) != nil {
				t.Fatal("invalid native preparation")
			}
			fields := 6
			if f.PositionMS != nil {
				fields++
				if request["position_ms"] != float64(*f.PositionMS) {
					t.Fatalf("seek position changed: %s", body)
				}
			}
			if transportOptionAction(f.Action) {
				fields += 2
				route, _ := json.Marshal(request["options_route"])
				var actual outputTransportIdentity
				if decodeQueueManagerJSON(route, &actual) != nil || actual != f.Route {
					t.Fatal("options route not exact", string(route))
				}
			}
			if len(request) != fields || request["homepod_id"] != "42" || request["action"] != f.Action || request["volume_percent"] != float64(0) {
				t.Fatalf("wrong native request %s", body)
			}
			p := receiverTransportPrepare{"podconnect.native_room_transport.v2", strings.Repeat("a", 32), f.Native, f.Action, 0, strings.Repeat("b", 64), 5000, "1", f.PositionMS, nil, ""}
			p.ShuffleEnabled = f.ShuffleEnabled
			p.RepeatMode = f.RepeatMode
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
			token := strings.Repeat("c", 32)
			if transportOptionAction(f.Action) {
				token = ""
			}
			if len(request) != 2 || request["prepare_id"] != strings.Repeat("a", 32) || request["own_tone_token"] != token {
				t.Fatalf("manager echoed invented pin %s", body)
			}
			if f.AfterCommit != nil {
				f.AfterCommit()
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
	body, _ := json.Marshal(roomTransportRequest{f.Scope, f.Native, f.Route, f.Action, 0, f.PositionMS, f.ShuffleEnabled, f.RepeatMode})
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
	body, _ := json.Marshal(roomTransportRequest{f.Scope, f.Native, f.Route, "volume", 27, nil, nil, ""})
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

func TestRoomTransportSeekBindsExactPositionWithoutAddingCField(t *testing.T) {
	for _, position := range []int64{0, 12345} {
		t.Run(fmt.Sprint(position), func(t *testing.T) {
			f := roomTransportFixture(t)
			f.Action, f.PositionMS = "seek", &position
			p := f.prepare(t)
			if p.Code != 200 {
				t.Fatal(p.Code, p.Body.String())
			}
			var reply map[string]any
			if json.Unmarshal(p.Body.Bytes(), &reply) != nil || reply["position_ms"] != float64(position) {
				t.Fatal("position not bound", p.Body.String())
			}
			if c := transportCommit(t); c.Code != 200 || !strings.Contains(c.Body.String(), `"outcome":"accepted"`) {
				t.Fatal(c.Code, c.Body.String())
			}
			if transportCommit(t).Code != 409 {
				t.Fatal("seek replay admitted")
			}
			if len(f.Writes) != 3 {
				t.Fatal("unexpected seek effects", f.Writes)
			}
		})
	}
}
func TestRoomTransportSeekRejectsMissingForeignOrMisplacedPosition(t *testing.T) {
	for _, mode := range []string{"missing", "negative", "tooLarge", "otherAction", "nativeMissing", "nativeChanged"} {
		t.Run(mode, func(t *testing.T) {
			f := roomTransportFixture(t)
			position := int64(12345)
			f.Action, f.PositionMS = "seek", &position
			switch mode {
			case "missing":
				f.PositionMS = nil
			case "negative":
				position = -1
			case "tooLarge":
				position = 1 << 53
			case "otherAction":
				f.Action = "resume"
			case "nativeMissing":
				f.NativePrepareMutate = func(p *receiverTransportPrepare) { p.PositionMS = nil }
			case "nativeChanged":
				f.NativePrepareMutate = func(p *receiverTransportPrepare) { other := position + 1; p.PositionMS = &other }
			}
			p := f.prepare(t)
			want := 400
			if strings.HasPrefix(mode, "native") {
				want = 409
			}
			if p.Code != want {
				t.Fatal(p.Code, p.Body.String())
			}
			if transportCommit(t).Code != 409 {
				t.Fatal("invalid seek offer survived")
			}
			for _, path := range f.Writes {
				if strings.Contains(path, "/api/outputs/") || strings.HasSuffix(path, "bound-commit") {
					t.Fatal("invalid seek reached effect admission", f.Writes)
				}
			}
		})
	}
}
func TestRoomTransportSeekUnknownIsConsumedWithoutRetry(t *testing.T) {
	f := roomTransportFixture(t)
	position := int64(12)
	f.Action, f.PositionMS, f.CommitBad = "seek", &position, true
	if p := f.prepare(t); p.Code != 200 {
		t.Fatal(p.Code, p.Body.String())
	}
	c := transportCommit(t)
	if c.Code != 200 || !strings.Contains(c.Body.String(), `"outcome":"unknown"`) || transportCommit(t).Code != 409 {
		t.Fatal(c.Code, c.Body.String())
	}
	if len(f.Writes) != 3 {
		t.Fatal("unknown seek repeated", f.Writes)
	}
}

func TestRoomTransportOptionsUseNativeOnlyAndRetainOneUse(t *testing.T) {
	for _, action := range []string{"shuffle", "repeat"} {
		t.Run(action, func(t *testing.T) {
			f := roomTransportFixture(t)
			f.Action = action
			enabled := true
			if action == "shuffle" {
				f.ShuffleEnabled = &enabled
			} else {
				f.RepeatMode = "track"
			}
			out := f.prepare(t)
			if out.Code != 200 {
				t.Fatal(out.Code, out.Body.String())
			}
			out = transportCommit(t)
			if out.Code != 200 || !strings.Contains(out.Body.String(), `"outcome":"accepted"`) {
				t.Fatal(out.Code, out.Body.String())
			}
			if transportCommit(t).Code != 409 {
				t.Fatal("option replay")
			}
			if fmt.Sprint(f.Writes) != fmt.Sprint([]string{"/player/room_transport/bound-prepare", "/player/room_transport/bound-commit"}) {
				t.Fatal("options mutated C", f.Writes)
			}
		})
	}
}
func TestRoomTransportOptionEchoAndPostEffectRouteChangeAreUnknownOrDenied(t *testing.T) {
	for _, boundary := range []string{"echo", "route-after", "unknown"} {
		t.Run(boundary, func(t *testing.T) {
			f := roomTransportFixture(t)
			f.Action = "repeat"
			f.RepeatMode = "context"
			if boundary == "echo" {
				f.NativePrepareMutate = func(p *receiverTransportPrepare) { p.RepeatMode = "track" }
			}
			if out := f.prepare(t); boundary == "echo" {
				if out.Code != 409 {
					t.Fatal(out.Code)
				}
				return
			} else if out.Code != 200 {
				t.Fatal(out.Code, out.Body.String())
			}
			if boundary == "route-after" {
				f.AfterCommit = func() { f.Route.Route = "999" }
			} else {
				f.CommitBad = true
			}
			out := transportCommit(t)
			if out.Code != 200 || !strings.Contains(out.Body.String(), `"outcome":"unknown"`) {
				t.Fatal(out.Code, out.Body.String())
			}
			if transportCommit(t).Code != 409 {
				t.Fatal("unknown option replayed")
			}
		})
	}
}
func TestRoomTransportOptionClosedArgumentsFailBeforeNativeWrite(t *testing.T) {
	for _, extra := range []string{`"action":"shuffle"`, `"action":"shuffle","shuffle_enabled":null`, `"action":"shuffle","shuffle_enabled":1`, `"action":"repeat","repeat_mode":"bad"`, `"action":"pause","repeat_mode":""`, `"action":"repeat","repeat_mode":"off","shuffle_enabled":false`} {
		f := roomTransportFixture(t)
		base, _ := json.Marshal(roomTransportRequest{Scope: f.Scope, Native: f.Native, Route: f.Route})
		var fields map[string]json.RawMessage
		_ = json.Unmarshal(base, &fields)
		delete(fields, "action")
		raw, _ := json.Marshal(fields)
		raw = append(raw[:len(raw)-1], []byte(","+extra+"}")...)
		out := httptest.NewRecorder()
		transportPrepareHandler(out, httptest.NewRequest("POST", "/api/room-transport/prepare", bytes.NewReader(raw)))
		if out.Code != 400 || len(f.Writes) != 0 {
			t.Fatal(string(raw), out.Code, f.Writes)
		}
	}
}
