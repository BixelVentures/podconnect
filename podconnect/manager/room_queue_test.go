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
	"time"
)

type roomQueueFixture struct {
	Scope              roomTransportScope
	Owner              roomTransportIdentity
	Route              outputTransportIdentity
	Writer             queueWriterProof
	Claim              startClaim
	Writes             []string
	OldC               bool
	ForeignC           bool
	BadCommit          bool
	RetireStage        bool
	BusyReadAfterStage bool
	CExpires           int
	CDelay             time.Duration
}

func newRoomQueueFixture(t *testing.T) *roomQueueFixture {
	t.Helper()
	claim, target, _ := queueFixture(t)
	claim.Proof.HomepodID = "42"
	claim.Proof.OutputVolume = 27
	claim.Proof.AliasBinding.Incarnation = strings.Repeat("e", 32)
	f := &roomQueueFixture{
		Scope:    roomTransportScope{target, "r0", "42", "native-user"},
		Owner:    roomTransportIdentity{"native-user", target, "r0", "42", "connection", "session", claim.Proof.AliasBinding.Incarnation, claim.Proof.AliasBinding.Registry, 1},
		Route:    outputTransportIdentity{"aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee", "42", "1", "2", "3", "4", "5", "6", configuredTransportInput(claim)},
		Writer:   queueWriterProof{queueProcessProof{"aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee", "123", "456"}, "0", queuePipeProof{"7", "8", "fifo"}, "0", "1", configuredTransportInput(claim)},
		Claim:    claim,
		CExpires: 10000,
	}
	oldClaim, oldRPC := roomQueueClaim, roomQueueRPC
	roomTransportStore.Lock()
	oldTransportBusy := roomTransportStore.Busy
	roomTransportStore.Busy = false
	roomTransportStore.Unlock()
	roomQueueStore.Lock()
	roomQueueStore.Read = nil
	roomQueueStore.Prepared = nil
	roomQueueStore.Unlock()
	t.Cleanup(func() {
		roomQueueClaim, roomQueueRPC = oldClaim, oldRPC
		roomTransportStore.Lock()
		roomTransportStore.Busy = oldTransportBusy
		roomTransportStore.Unlock()
		roomQueueStore.Lock()
		roomQueueStore.Read = nil
		roomQueueStore.Prepared = nil
		roomQueueStore.Unlock()
	})
	roomQueueClaim = func(room, target string) (startClaim, bool) {
		return f.Claim, room == f.Scope.Room && target == f.Scope.Target
	}
	roomQueueRPC = func(ctx context.Context, base, method, path string, body []byte) ([]byte, error) {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if method == http.MethodPost && !strings.HasSuffix(path, "/bound-state") {
			f.Writes = append(f.Writes, path)
		}
		switch {
		case strings.HasPrefix(path, "/player/room_transport/bound-queue?"):
			return json.Marshal(nativeRoomQueueState{"podconnect.native_room_queue_state.v1", true, roomQueuePurpose, f.Owner, strings.Repeat("a", 32), 7, 10000, []queueNativeItem{{strings.Repeat("1", 24), 0, "spotify:track:AAAAAAAAAAAAAAAAAAAAAA", true}, {strings.Repeat("2", 24), 1, "spotify:track:AAAAAAAAAAAAAAAAAAAAAA", false}}, false, f.Writer, false})
		case path == "/api/outputs/42/transport/bound-state":
			var request struct {
				Owner  roomTransportIdentity `json:"native_owner"`
				Writer queueWriterProof      `json:"writer_proof"`
			}
			if queueClosedJSON(body, &request) != nil || request.Owner != f.Owner || request.Writer != f.Writer {
				t.Fatalf("C queue state omitted exact owner/writer: %s", body)
			}
			if f.OldC {
				return json.Marshal(outputTransportState{"podconnect.output_transport_state.v2", true, f.Route, false, []string{"next"}, false})
			}
			if f.BusyReadAfterStage && len(f.Writes) > 1 {
				return json.Marshal(outputRoomQueueState{"podconnect.output_queue_state.v1", true, roomQueuePurpose, f.Route, true, []string{}, f.Owner, f.Writer, false})
			}
			return json.Marshal(outputRoomQueueState{"podconnect.output_queue_state.v1", true, roomQueuePurpose, f.Route, false, []string{"queue"}, f.Owner, f.Writer, false})
		case path == "/player/room_transport/bound-prepare":
			var request struct {
				Account string         `json:"account_user_id"`
				Target  string         `json:"target_id"`
				Room    string         `json:"room_id"`
				Homepod string         `json:"homepod_id"`
				Action  string         `json:"action"`
				Percent int            `json:"volume_percent"`
				Queue   queueSelection `json:"queue"`
			}
			if queueClosedJSON(body, &request) != nil || request.Action != "queue" || request.Percent != 0 || request.Queue.Occurrence != strings.Repeat("2", 24) || request.Room != "r0" || request.Homepod != "42" {
				t.Fatalf("native selected exact occurrence incorrectly: %s", body)
			}
			if f.RetireStage {
				f.Claim.RT.live.beginRun(nil)
			}
			return json.Marshal(nativeRoomQueuePrepare{"podconnect.native_room_queue_prepare.v1", roomQueuePurpose, strings.Repeat("b", 32), f.Owner, "1", "queue", 0, strings.Repeat("c", 64), 5000, request.Queue, f.Writer})
		case path == "/api/outputs/42/transport/bound-prepare":
			var request struct {
				PrepareID string                `json:"native_prepare_id"`
				Digest    string                `json:"args_digest"`
				Action    string                `json:"action"`
				Owner     roomTransportIdentity `json:"native_owner"`
				Revision  string                `json:"native_revision"`
				Queue     queueSelection        `json:"queue"`
				Writer    queueWriterProof      `json:"writer_proof"`
			}
			if queueClosedJSON(body, &request) != nil || request.Action != "queue" || request.Writer != f.Writer || request.Queue.Occurrence != strings.Repeat("2", 24) || request.Digest != strings.Repeat("c", 64) {
				t.Fatalf("C pin omitted exact queue/writer/digest: %s", body)
			}
			route := f.Route
			if f.ForeignC {
				route.Input = "99"
			}
			if f.CDelay > 0 {
				time.Sleep(f.CDelay)
			}
			return json.Marshal(outputRoomQueuePrepare{"podconnect.output_queue_prepare.v1", true, roomQueuePurpose, strings.Repeat("d", 32), route, f.Owner, "1", strings.Repeat("b", 32), strings.Repeat("c", 64), request.Queue, f.Writer, f.CExpires})
		case path == "/player/room_transport/bound-commit":
			var request struct {
				PrepareID string `json:"prepare_id"`
				Token     string `json:"own_tone_token"`
			}
			if queueClosedJSON(body, &request) != nil || request.PrepareID != strings.Repeat("b", 32) || request.Token != strings.Repeat("d", 32) {
				t.Fatalf("manager commit lacked private C token: %s", body)
			}
			if f.BadCommit {
				return []byte(`{"schema":"wrong"}`), nil
			}
			return json.Marshal(map[string]any{"schema": "podconnect.native_room_queue_commit.v1", "accepted_native": true, "purpose": roomQueuePurpose, "prepare_id": request.PrepareID, "observation_id": strings.Repeat("a", 32), "occurrence_id": strings.Repeat("2", 24), "queue_revision": 7, "native_owner": f.Owner, "native_revision": "1", "args_digest": strings.Repeat("c", 64), "physical_result_verified": false})
		default:
			t.Fatalf("unexpected queue RPC %s %s", method, path)
			return nil, fmt.Errorf("unexpected queue RPC")
		}
	}
	return f
}
func (f *roomQueueFixture) read(t *testing.T) *httptest.ResponseRecorder {
	t.Helper()
	q := url.Values{"account_user_id": {f.Scope.Account}, "target_id": {f.Scope.Target}, "room_id": {f.Scope.Room}, "homepod_id": {f.Scope.Homepod}}
	out := httptest.NewRecorder()
	roomQueueStateHandler(out, httptest.NewRequest(http.MethodGet, "/api/room-queue/state?"+q.Encode(), nil))
	return out
}
func (f *roomQueueFixture) prepare(t *testing.T, occurrence string) *httptest.ResponseRecorder {
	t.Helper()
	selection := queueSelection{strings.Repeat("a", 32), "session", 7, occurrence}
	body, _ := json.Marshal(roomQueuePrepareRequest{f.Scope, f.Owner, f.Route, selection})
	out := httptest.NewRecorder()
	roomQueuePrepareHandler(out, httptest.NewRequest(http.MethodPost, "/api/room-queue/prepare", bytes.NewReader(body)))
	return out
}
func roomQueueCommit(t *testing.T) *httptest.ResponseRecorder {
	t.Helper()
	out := httptest.NewRecorder()
	roomQueueCommitHandler(out, httptest.NewRequest(http.MethodPost, "/api/room-queue/commit", strings.NewReader(`{"prepare_id":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"}`)))
	return out
}
func TestRoomQueueExactDuplicateOccurrenceStagesThenCommitsOnce(t *testing.T) {
	f := newRoomQueueFixture(t)
	if read := f.read(t); read.Code != 200 || !strings.Contains(read.Body.String(), `"play_queue_occurrence"`) {
		t.Fatal(read.Code, read.Body.String())
	}
	if len(f.Writes) != 0 {
		t.Fatal("read caused write", f.Writes)
	}
	stage := f.prepare(t, strings.Repeat("2", 24))
	if stage.Code != 200 || !strings.Contains(stage.Body.String(), `"writer_proof"`) {
		t.Fatal(stage.Code, stage.Body.String())
	}
	if strings.Contains(fmt.Sprint(f.Writes), "bound-commit") {
		t.Fatal("stage caused effect", f.Writes)
	}
	if f.prepare(t, strings.Repeat("2", 24)).Code != 409 {
		t.Fatal("same observation staged twice")
	}
	commit := roomQueueCommit(t)
	if commit.Code != 200 || !strings.Contains(commit.Body.String(), `"outcome":"accepted"`) {
		t.Fatal(commit.Code, commit.Body.String())
	}
	if roomQueueCommit(t).Code != 409 {
		t.Fatal("commit replay admitted")
	}
	want := []string{"/player/room_transport/bound-prepare", "/api/outputs/42/transport/bound-prepare", "/player/room_transport/bound-commit"}
	if fmt.Sprint(f.Writes) != fmt.Sprint(want) || f.Claim.Proof.OutputVolume != 27 {
		t.Fatal("effect path or retained manual volume changed", f.Writes, f.Claim.Proof.OutputVolume)
	}
}
func TestRoomQueueOldCAndForeignOccurrenceNeverStage(t *testing.T) {
	f := newRoomQueueFixture(t)
	f.OldC = true
	if read := f.read(t); read.Code != 409 || len(f.Writes) != 0 {
		t.Fatal("old C advertised queue", read.Code, f.Writes)
	}
	f.OldC = false
	if f.read(t).Code != 200 || f.prepare(t, strings.Repeat("3", 24)).Code != 409 || len(f.Writes) != 0 {
		t.Fatal("foreign occurrence reached Native", f.Writes)
	}
}
func TestRoomQueueChangedRouteOrRetiredClaimCannotCommit(t *testing.T) {
	for _, change := range []string{"route", "claim"} {
		t.Run(change, func(t *testing.T) {
			f := newRoomQueueFixture(t)
			if f.read(t).Code != 200 {
				t.Fatal("read")
			}
			if change == "route" {
				f.ForeignC = true
			} else {
				f.RetireStage = true
			}
			if stage := f.prepare(t, strings.Repeat("2", 24)); stage.Code != 409 {
				t.Fatal("stale stage admitted", stage.Code)
			}
			if roomQueueCommit(t).Code != 409 {
				t.Fatal("stale stage committed")
			}
			for _, path := range f.Writes {
				if strings.HasSuffix(path, "/bound-commit") {
					t.Fatal("effect sent", f.Writes)
				}
			}
		})
	}
}
func TestRoomQueueUnknownCommitConsumed(t *testing.T) {
	f := newRoomQueueFixture(t)
	if f.read(t).Code != 200 || f.prepare(t, strings.Repeat("2", 24)).Code != 200 {
		t.Fatal("prepare")
	}
	f.BadCommit = true
	if commit := roomQueueCommit(t); commit.Code != 200 || !strings.Contains(commit.Body.String(), `"outcome":"unknown"`) || !strings.Contains(commit.Body.String(), `"retry_allowed":false`) {
		t.Fatal(commit.Code, commit.Body.String())
	}
	if roomQueueCommit(t).Code != 409 {
		t.Fatal("unknown replay")
	}
}
func TestRoomQueueRefreshCannotErasePreparedCommit(t *testing.T) {
	f := newRoomQueueFixture(t)
	if f.read(t).Code != 200 || f.prepare(t, strings.Repeat("2", 24)).Code != 200 {
		t.Fatal("prepare")
	}
	f.BusyReadAfterStage = true
	if refreshed := f.read(t); refreshed.Code != 409 {
		t.Fatal("busy C unexpectedly admitted a new read", refreshed.Code)
	}
	if committed := roomQueueCommit(t); committed.Code != 200 || !strings.Contains(committed.Body.String(), `"outcome":"accepted"`) {
		t.Fatal("refresh erased prepared selection", committed.Code, committed.Body.String())
	}
}
func TestRoomQueueNewReadCannotReplaceUnconsumedPreparation(t *testing.T) {
	f := newRoomQueueFixture(t)
	if f.read(t).Code != 200 || f.prepare(t, strings.Repeat("2", 24)).Code != 200 {
		t.Fatal("prepare")
	}
	if refreshed := f.read(t); refreshed.Code != 200 {
		t.Fatal("observational refresh", refreshed.Code)
	}
	if next := f.prepare(t, strings.Repeat("2", 24)); next.Code != 409 {
		t.Fatal("new staging replaced unconsumed preparation", next.Code)
	}
	if committed := roomQueueCommit(t); committed.Code != 200 || !strings.Contains(committed.Body.String(), `"outcome":"accepted"`) {
		t.Fatal("original preparation lost", committed.Code, committed.Body.String())
	}
}
func TestRoomQueuePrepareBoundsReportedLifetimeByCLease(t *testing.T) {
	f := newRoomQueueFixture(t)
	f.CExpires = 100
	f.CDelay = 30 * time.Millisecond
	if f.read(t).Code != 200 {
		t.Fatal("read")
	}
	stage := f.prepare(t, strings.Repeat("2", 24))
	if stage.Code != 200 {
		t.Fatal(stage.Code, stage.Body.String())
	}
	var response struct {
		Expires int `json:"expires_after_ms"`
	}
	if json.Unmarshal(stage.Body.Bytes(), &response) != nil || response.Expires <= 0 || response.Expires >= f.CExpires {
		t.Fatal("C await failed to age advertised lease", response.Expires)
	}
	roomQueueStore.Lock()
	remaining := time.Until(roomQueueStore.Prepared.Expires)
	roomQueueStore.Unlock()
	if remaining > time.Duration(f.CExpires)*time.Millisecond {
		t.Fatal("stored deadline exceeds C lease", remaining)
	}
}
func TestRoomQueueExpiredCLeaseCannotMintPreparedCommit(t *testing.T) {
	f := newRoomQueueFixture(t)
	f.CExpires = 1
	f.CDelay = 10 * time.Millisecond
	if f.read(t).Code != 200 || f.prepare(t, strings.Repeat("2", 24)).Code != 409 || roomQueueCommit(t).Code != 409 {
		t.Fatal("expired C lease reached commit")
	}
}
func TestRoomQueueUsesExistingTransportAdmission(t *testing.T) {
	_ = newRoomQueueFixture(t)
	if !acquireRoomQueue() {
		t.Fatal("queue failed shared admission")
	}
	if acquireRoomTransport() {
		releaseRoomTransport()
		t.Fatal("ordinary transport admitted during queue operation")
	}
	releaseRoomQueue()
	if !acquireRoomTransport() {
		t.Fatal("shared admission not released")
	}
	if acquireRoomQueue() {
		releaseRoomQueue()
		t.Fatal("queue admitted during ordinary transport")
	}
	releaseRoomTransport()
}
func TestRoomQueueRejectsUnsafeAccountBeforeCProbe(t *testing.T) {
	for _, test := range []struct {
		account string
		status  int
	}{{"native\nuser", 409}, {"native\"user", 409}, {"native\\user", 409}, {"nativé-user", 409}, {strings.Repeat("a", 161), 400}} {
		t.Run(fmt.Sprintf("%q", test.account), func(t *testing.T) {
			f := newRoomQueueFixture(t)
			f.Scope.Account = test.account
			f.Owner.Account = test.account
			if read := f.read(t); read.Code != test.status || len(f.Writes) != 0 {
				t.Fatal("unsafe account reached C queue capability", read.Code, f.Writes)
			}
		})
	}
}
func TestRoomQueueAcceptsZeroRouteAndSelectionRevision(t *testing.T) {
	f := newRoomQueueFixture(t)
	f.Route.Route = "0"
	f.Route.Selection = "0"
	if read := f.read(t); read.Code != 200 {
		t.Fatal("valid initial route revision rejected", read.Code, read.Body.String())
	}
	if stage := f.prepare(t, strings.Repeat("2", 24)); stage.Code != 200 {
		t.Fatal("valid initial selection revision rejected", stage.Code, stage.Body.String())
	}
	if commit := roomQueueCommit(t); commit.Code != 200 {
		t.Fatal("valid initial route denied at commit", commit.Code, commit.Body.String())
	}
	f.Route.Input = "0"
	if queueRouteValid(f.Route, f.Scope.Homepod) {
		t.Fatal("missing live input generation admitted")
	}
}
func TestRoomQueueRejectsNestedDuplicateAndMissingProof(t *testing.T) {
	for _, raw := range []string{`{"scope":{"room_id":"r0","room_id":"r1"}}`, `{"writer_proof":{"native_process":{"pid":"1","pid":"2"}}}`, `null`} {
		var value map[string]any
		if queueClosedJSON([]byte(raw), &value) == nil {
			t.Fatal("accepted malformed queue JSON", raw)
		}
	}
}
