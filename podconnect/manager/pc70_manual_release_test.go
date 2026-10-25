package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func manualReleaseFixture(t *testing.T) (*transportFixture, nativeReleaseOwner, *[]string) {
	t.Helper()
	f := roomTransportFixture(t)
	f.Claim.Proof.AliasBinding.Incarnation = strings.Repeat("a", 32)
	f.Native.Incarnation = f.Claim.Proof.AliasBinding.Incarnation
	rooms := loadRooms()
	rooms[0].HomepodID = f.Scope.Homepod
	store.mu.Lock()
	err := store.saveLocked(&roomsFile{NextIdx: 1, Rooms: rooms})
	store.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	owner := nativeReleaseOwner{"podconnect.native_release_owner.v1", f.Native, queueWriterProof{queueProcessProof{"11111111-2222-3333-4444-555555555555", "123", "456"}, "7", queuePipeProof{"8", "9", "fifo"}, "10", "11", f.Route.InputDigest}}
	writes := []string{}
	roomTransportStore.Lock()
	roomTransportStore.ReleasePresent = false
	roomTransportStore.Unlock()
	roomTransportRPC = func(ctx context.Context, base, method, path string, body []byte) ([]byte, error) {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if method != http.MethodGet && !strings.HasSuffix(path, "/bound-state") {
			writes = append(writes, path)
		}
		switch {
		case path == "/status":
			return json.Marshal(map[string]string{"username": f.Scope.Account, "device_id": f.Scope.Target})
		case strings.HasPrefix(path, "/player/room_transport/bound-state?"):
			if !strings.Contains(path, "include_release=true") {
				t.Fatal("release read lost opt-in")
			}
			return json.Marshal(nativeReleaseState{"podconnect.native_room_transport_state.v2", true, f.Native, []string{"pause", "resume"}, false, owner})
		case path == "/api/outputs/42/transport/bound-state":
			return json.Marshal(outputTransportState{Schema: "podconnect.output_transport_state.v2", OK: true, Identity: f.Route, Actions: []string{"pause", "resume"}, CleanAbsent: true})
		case path == "/player/room_transport/bound-prepare":
			return json.Marshal(nativeReleasePrepare{receiverTransportPrepare{"podconnect.native_room_transport.v2", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", f.Native, "release", 0, strings.Repeat("c", 64), 5000, "1"}, roomReleasePurpose, owner.Writer})
		case path == "/api/outputs/42/transport/bound-prepare":
			return json.Marshal(outputTransportPrepare{"podconnect.output_transport_prepare.v2", true, "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", f.Route, f.Native, "1", 10000})
		case path == "/player/room_transport/bound-commit":
			return json.Marshal(roomReleaseResult{Schema: "podconnect.release_result.v1", OK: true, PrepareID: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", Outcome: "retired", Terminal: true, ContextsDrained: true})
		case strings.HasPrefix(path, "/player/room_transport/bound-result?"):
			return json.Marshal(roomReleaseResult{Schema: "podconnect.release_result.v1", PrepareID: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", Outcome: "unknown"})
		default:
			t.Fatalf("unexpected RPC %s %s", method, path)
			return nil, errors.New("unexpected")
		}
	}
	return f, owner, &writes
}

func releaseRequest(t *testing.T, request roomReleaseRequest) *httptest.ResponseRecorder {
	t.Helper()
	raw, _ := json.Marshal(request)
	out := httptest.NewRecorder()
	speakerReleaseHandler(out, httptest.NewRequest(http.MethodPost, "/api/release", bytes.NewReader(raw)))
	return out
}

func TestPC70ManualReleaseReadBindingAndOneUseReceipt(t *testing.T) {
	f, owner, writes := manualReleaseFixture(t)
	read, err := readRoomRelease(context.Background(), f.Scope)
	if err != nil {
		t.Fatal(err)
	}
	binding := releaseBinding(read)
	other := read
	other.Owner.Writer.OpenGeneration = "12"
	if !transportDigest.MatchString(binding) || releaseBinding(other) == binding || read.Owner != owner {
		t.Fatal("writer/route binding missing")
	}
	request := roomReleaseRequest{f.Scope.Room, binding, strings.Repeat("d", 32)}
	first := releaseRequest(t, request)
	if first.Code != 200 || !strings.Contains(first.Body.String(), `"outcome":"retired"`) {
		t.Fatal(first.Code, first.Body.String())
	}
	second := releaseRequest(t, request)
	if second.Body.String() != first.Body.String() || len(*writes) != 3 {
		t.Fatalf("replay or wrong operation count: %v", *writes)
	}
	request.Binding = strings.Repeat("e", 64)
	if releaseRequest(t, request).Code != 409 || len(*writes) != 3 {
		t.Fatal("conflicting request reexecuted")
	}
}

func TestPC70ManualReleaseUnknownACKNeverReplays(t *testing.T) {
	f, _, writes := manualReleaseFixture(t)
	read, err := readRoomRelease(context.Background(), f.Scope)
	if err != nil {
		t.Fatal(err)
	}
	rpc := roomTransportRPC
	roomTransportRPC = func(ctx context.Context, base, method, path string, body []byte) ([]byte, error) {
		if path == "/player/room_transport/bound-commit" {
			*writes = append(*writes, path)
			return nil, errors.New("unknown")
		}
		return rpc(ctx, base, method, path, body)
	}
	request := roomReleaseRequest{f.Scope.Room, releaseBinding(read), strings.Repeat("d", 32)}
	first := releaseRequest(t, request)
	if first.Code != 200 || !strings.Contains(first.Body.String(), `"outcome":"unknown"`) {
		t.Fatal(first.Code, first.Body.String())
	}
	if releaseRequest(t, request).Body.String() != first.Body.String() || len(*writes) != 3 {
		t.Fatal("UNKNOWN replay")
	}
}

func TestPC70ManualReleaseChangedOwnerAfterPrepareNeverCommits(t *testing.T) {
	f, _, writes := manualReleaseFixture(t)
	read, err := readRoomRelease(context.Background(), f.Scope)
	if err != nil {
		t.Fatal(err)
	}
	rpc := roomTransportRPC
	roomTransportRPC = func(ctx context.Context, base, method, path string, body []byte) ([]byte, error) {
		raw, err := rpc(ctx, base, method, path, body)
		if path == "/player/room_transport/bound-prepare" {
			f.Route.Route = "99"
		}
		return raw, err
	}
	out := releaseRequest(t, roomReleaseRequest{f.Scope.Room, releaseBinding(read), strings.Repeat("d", 32)})
	if out.Code != 409 || len(*writes) != 1 {
		t.Fatalf("changed route committed: %d %v", out.Code, *writes)
	}
}

func TestPC70ManualReleaseSameOperationReadRecoversWithoutCommitReplay(t *testing.T) {
	f, _, writes := manualReleaseFixture(t)
	read, err := readRoomRelease(context.Background(), f.Scope)
	if err != nil {
		t.Fatal(err)
	}
	rpc := roomTransportRPC
	reads := 0
	roomTransportRPC = func(ctx context.Context, base, method, path string, body []byte) ([]byte, error) {
		if path == "/player/room_transport/bound-commit" {
			*writes = append(*writes, path)
			return nil, errors.New("unknown")
		}
		if strings.HasPrefix(path, "/player/room_transport/bound-result?") {
			reads++
			if method != http.MethodGet || path != "/player/room_transport/bound-result?prepare_id=aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa" {
				t.Fatal("not exact operation read")
			}
			return json.Marshal(roomReleaseResult{Schema: "podconnect.release_result.v1", OK: true, PrepareID: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", Outcome: "retired", Terminal: true, ContextsDrained: true})
		}
		return rpc(ctx, base, method, path, body)
	}
	request := roomReleaseRequest{f.Scope.Room, releaseBinding(read), strings.Repeat("d", 32)}
	if !strings.Contains(releaseRequest(t, request).Body.String(), `"outcome":"unknown"`) {
		t.Fatal("unknown lost")
	}
	if !strings.Contains(releaseRequest(t, request).Body.String(), `"outcome":"retired"`) || reads != 1 || len(*writes) != 3 {
		t.Fatal("recovery reexecuted operation")
	}
}

func TestPC70ManualReleaseClosedOwnerAndReceipt(t *testing.T) {
	f, _, writes := manualReleaseFixture(t)
	rpc := roomTransportRPC
	roomTransportRPC = func(ctx context.Context, base, method, path string, body []byte) ([]byte, error) {
		raw, err := rpc(ctx, base, method, path, body)
		if strings.HasPrefix(path, "/player/room_transport/bound-state?") {
			raw = append(raw[:len(raw)-1], []byte(`,"secret":"must reject"}`)...)
		}
		return raw, err
	}
	if _, err := readRoomRelease(context.Background(), f.Scope); err == nil || len(*writes) != 0 {
		t.Fatal("unknown descriptor accepted")
	}
	for _, result := range []roomReleaseResult{
		{Schema: "podconnect.release_result.v1", OK: true, PrepareID: "a", Outcome: "retired", Terminal: true},
		{Schema: "podconnect.release_result.v1", PrepareID: "a", Outcome: "unknown", RetryAllowed: true},
		{Schema: "podconnect.release_result.v1", OK: true, PrepareID: "a", Outcome: "retired", Terminal: true, ContextsDrained: true, Physical: true},
	} {
		if releaseResultValid(result, "a") {
			t.Fatal("false terminal/authority accepted")
		}
	}
}

func TestPC70ManualReleaseAdminAdapterUsesHeldBindingOnce(t *testing.T) {
	start := strings.Index(indexHTML, "hrel.onclick = async function () {")
	end := strings.Index(indexHTML[start:], "hrow.appendChild(hsave)")
	if start < 0 || end < 0 {
		t.Fatal("existing release adapter missing")
	}
	adapter := indexHTML[start : start+end]
	for _, required := range []string{"rm.release.owner_binding_id", "room_id:rm.id, expected_binding_id:binding, request_id:requestId", "hrel.disabled = true", "window.crypto.getRandomValues", "result.contexts_drained === true", "result.retry_allowed === false", "Release outcome is unknown"} {
		if !strings.Contains(adapter, required) {
			t.Fatalf("closed release adapter missing %q", required)
		}
	}
	if strings.Count(adapter, "fetch(") != 2 || strings.Count(adapter, "method:'POST'") != 1 || !strings.Contains(adapter, "api/release?request_id=") || !strings.Contains(adapter, "method:'GET'") || strings.Contains(adapter, "load()") || strings.Contains(adapter, "api/select") {
		t.Fatal("release adapter retries or changes route")
	}
}

func TestPC70ManualReleaseCorrelatedGETAndLateUnknown(t *testing.T) {
	f, _, writes := manualReleaseFixture(t)
	read, err := readRoomRelease(context.Background(), f.Scope)
	if err != nil {
		t.Fatal(err)
	}
	rpc := roomTransportRPC
	reads := 0
	roomTransportRPC = func(ctx context.Context, base, method, path string, body []byte) ([]byte, error) {
		if path == "/player/room_transport/bound-commit" {
			*writes = append(*writes, path)
			return nil, errors.New("unknown")
		}
		if strings.HasPrefix(path, "/player/room_transport/bound-result?") {
			reads++
			if method != http.MethodGet {
				t.Fatal("read replayed mutation")
			}
			return json.Marshal(roomReleaseResult{Schema: "podconnect.release_result.v1", OK: true, PrepareID: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", Outcome: "retired", Terminal: true, ContextsDrained: true})
		}
		return rpc(ctx, base, method, path, body)
	}
	request := roomReleaseRequest{f.Scope.Room, releaseBinding(read), strings.Repeat("d", 32)}
	releaseRequest(t, request)
	foreign := httptest.NewRecorder()
	speakerReleaseHandler(foreign, httptest.NewRequest(http.MethodGet, "/api/release?request_id="+strings.Repeat("e", 32), nil))
	if foreign.Code != 404 || reads != 0 {
		t.Fatal("foreign request observed an operation")
	}
	out := httptest.NewRecorder()
	speakerReleaseHandler(out, httptest.NewRequest(http.MethodGet, "/api/release?request_id="+request.RequestID, nil))
	if out.Code != 200 || !strings.Contains(out.Body.String(), `"outcome":"retired"`) || reads != 1 || len(*writes) != 3 {
		t.Fatalf("read replayed commit: %d %v", out.Code, *writes)
	}
	late := storeRoomReleaseResult(request, roomReleaseResult{Schema: "podconnect.release_result.v1", PrepareID: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", Outcome: "unknown"})
	if late.Outcome != "retired" {
		t.Fatal("late UNKNOWN erased terminal receipt")
	}
}

func TestPC70RoomsReleaseProjectionIsOptInAndDoesNotGateLegacyRead(t *testing.T) {
	f, _, _ := manualReleaseFixture(t)
	oldManager, oldTransport := mgr, http.DefaultTransport
	mgr = &roomManager{runtimes: map[string]*roomRuntime{"r0": f.Claim.RT}}
	t.Cleanup(func() { mgr, http.DefaultTransport = oldManager, oldTransport })
	http.DefaultTransport = startTransport(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"outputs":[]}`))}, nil
	})
	held := make(chan struct{})
	roomTransportRPC = func(ctx context.Context, base, method, path string, body []byte) ([]byte, error) {
		close(held)
		<-ctx.Done()
		return nil, ctx.Err()
	}
	plain := httptest.NewRecorder()
	roomsStateHandler(plain, httptest.NewRequest(http.MethodGet, "/api/rooms", nil))
	if plain.Code != 200 || strings.Contains(plain.Body.String(), `"release"`) {
		t.Fatal("legacy rooms data changed")
	}
	select {
	case <-held:
		t.Fatal("legacy rooms read called release provider")
	default:
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() {
		defer close(done)
		roomsStateHandler(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/api/rooms?include_release=1", nil).WithContext(ctx))
	}()
	select {
	case <-held:
	case <-time.After(time.Second):
		t.Fatal("opt-in did not reach release provider")
	}
	legacyDone := make(chan struct{})
	go func() {
		defer close(legacyDone)
		roomsStateHandler(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/api/rooms", nil))
	}()
	select {
	case <-legacyDone:
	case <-time.After(time.Second):
		t.Fatal("held release provider blocked unrelated rooms read")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("opt-in cancellation was not bounded")
	}
	bad := httptest.NewRecorder()
	roomsStateHandler(bad, httptest.NewRequest(http.MethodGet, "/api/rooms?include_release=1&include_release=1", nil))
	if bad.Code != 400 {
		t.Fatal("ambiguous release opt-in accepted")
	}
}

func TestPC70ManualReleaseNullPrepareFieldCannotMintCOperation(t *testing.T) {
	f, _, writes := manualReleaseFixture(t)
	read, err := readRoomRelease(context.Background(), f.Scope)
	if err != nil {
		t.Fatal(err)
	}
	rpc := roomTransportRPC
	roomTransportRPC = func(ctx context.Context, base, method, path string, body []byte) ([]byte, error) {
		raw, err := rpc(ctx, base, method, path, body)
		if path == "/player/room_transport/bound-prepare" {
			raw = bytes.Replace(raw, []byte(`"volume_percent":0`), []byte(`"volume_percent":null`), 1)
		}
		return raw, err
	}
	out := releaseRequest(t, roomReleaseRequest{f.Scope.Room, releaseBinding(read), strings.Repeat("d", 32)})
	if out.Code != 409 || len(*writes) != 1 {
		t.Fatalf("null native field minted C operation: %d %v", out.Code, *writes)
	}
}

func TestPC70MixedOldCPrepareRejectsBeforeNativeCommit(t *testing.T) {
	f, _, writes := manualReleaseFixture(t)
	read, err := readRoomRelease(context.Background(), f.Scope)
	if err != nil {
		t.Fatal(err)
	}
	rpc := roomTransportRPC
	roomTransportRPC = func(ctx context.Context, base, method, path string, body []byte) ([]byte, error) {
		if path == "/api/outputs/42/transport/bound-prepare" {
			*writes = append(*writes, path)
			return nil, errors.New("old C rejects unsupported release purpose")
		}
		return rpc(ctx, base, method, path, body)
	}
	out := releaseRequest(t, roomReleaseRequest{f.Scope.Room, releaseBinding(read), strings.Repeat("d", 32)})
	if out.Code != 409 || len(*writes) != 2 {
		t.Fatalf("unsupported mixed C reached Native commit: %d %v", out.Code, *writes)
	}
	if !releaseResultValid(roomReleaseResult{Schema: "podconnect.release_result.v1", PrepareID: "a", Outcome: "unknown", ContextsDrained: true}, "a") {
		t.Fatal("known C drain with pending handoff cannot be represented")
	}
}
