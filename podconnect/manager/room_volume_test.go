package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"
)

type volumeFixture struct {
	*transportFixture
	Volume        roomVolumeState
	Mu            sync.Mutex
	Calls         []string
	PrepareMutate func(*nativeRoomVolumePrepare)
	Commit        func(context.Context) ([]byte, error)
}

func managerVolumeFixture(t *testing.T) *volumeFixture {
	t.Helper()
	base := roomTransportFixture(t)
	f := &volumeFixture{transportFixture: base, Volume: roomVolumeState{"7", "8", 27, 27}}
	roomVolumeStore.Lock()
	roomVolumeStore.Preparing = false
	roomVolumeStore.Records = map[string]*roomVolumeRecord{}
	roomVolumeStore.Unlock()
	t.Cleanup(func() {
		roomVolumeStore.Lock()
		roomVolumeStore.Preparing = false
		roomVolumeStore.Records = map[string]*roomVolumeRecord{}
		roomVolumeStore.Unlock()
	})
	roomTransportRPC = func(ctx context.Context, endpoint, method, path string, body []byte) ([]byte, error) {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		f.Mu.Lock()
		f.Calls = append(f.Calls, method+" "+path)
		f.Mu.Unlock()
		switch {
		case strings.HasPrefix(path, "/player/room_volume/bound-state?"):
			return json.Marshal(receiverTransportState{"podconnect.native_room_volume_state.v2", true, f.Native, []string{"volume"}, nil, false})
		case path == "/api/outputs/42/volume/bound-state":
			var request struct {
				Owner roomTransportIdentity `json:"native_owner"`
			}
			if method != "POST" || decodeQueueManagerJSON(body, &request) != nil || request.Owner != f.Native {
				t.Errorf("volume owner probe is not actualnative9: %s", body)
			}
			return json.Marshal(outputRoomVolumeState{"podconnect.output_room_volume_state.v2", true, f.Route, f.Volume, false, []string{"volume"}, false})
		case path == "/player/room_volume/bound-prepare":
			var b roomVolumeRequest
			if decodeQueueManagerJSON(body, &b) != nil || b.Scope != f.Scope || b.Action != "volume" {
				t.Errorf("invalid bound native request %s", body)
			}
			p := nativeRoomVolumePrepare{"podconnect.native_room_volume_prepare.v2", strings.Repeat("a", 32), 5000, b.Scope, b.Native, b.Route, b.Volume, b.Action, b.Percent, false}
			if f.PrepareMutate != nil {
				f.PrepareMutate(&p)
			}
			return json.Marshal(p)
		case path == "/player/room_volume/bound-commit":
			if string(body) != `{"native_prepare_id":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}` {
				t.Errorf("caller Cauthority leaked %s", body)
			}
			if f.Commit != nil {
				return f.Commit(ctx)
			}
			return json.Marshal(nativeRoomVolumeResult{Schema: "podconnect.native_room_volume_commit.v2", ID: strings.Repeat("a", 32), OK: true, State: "protocol_accepted", Reserved: true, Protocol: true, Possible: true, Terminal: true, Drained: true})
		case path == "/player/room_volume/bound-cancel":
			return json.Marshal(nativeRoomVolumeResult{Schema: "podconnect.native_room_volume_cancel.v2", ID: strings.Repeat("a", 32), State: "cancelled_before_admission", Terminal: true, Drained: true})
		default:
			t.Errorf("unexpected effect/read %s %s", method, path)
			return nil, fmt.Errorf("unexpected route")
		}
	}
	return f
}
func (f *volumeFixture) prepare(t *testing.T, percent int) *httptest.ResponseRecorder {
	t.Helper()
	raw, _ := json.Marshal(roomVolumeRequest{f.Scope, f.Native, f.Route, f.Volume, "volume", percent})
	w := httptest.NewRecorder()
	roomVolumePrepareHandler(w, httptest.NewRequest("POST", "/api/room-volume/prepare", bytes.NewReader(raw)))
	return w
}

func TestRoomVolumeEffectRequiresExactAcceptedOperationAndFreshCRevision(t *testing.T) {
	f := managerVolumeFixture(t)
	id := strings.Repeat("a", 32)
	request := roomVolumeRequest{f.Scope, f.Native, f.Route, f.Volume, "volume", 36}
	roomVolumeStore.Lock()
	roomVolumeStore.Records[id] = &roomVolumeRecord{Request: request, Claim: f.Claim, Dispatched: true, Result: &nativeRoomVolumeResult{ID: id, OK: true, State: "protocol_accepted", Reserved: true, Protocol: true, Possible: true, Terminal: true, Drained: true}}
	roomVolumeStore.Unlock()
	effect := &receiverRoomEffect{id, "volume", "1", "2", f.Native}
	after := roomVolumeState{"8", "8", 36, 36}
	if currentRoomVolumeEffect(effect, f.Scope, f.Claim, f.Native, f.Route, after) == nil {
		t.Fatal("exact accepted operation and C revision must remain attributable")
	}
	for _, changed := range []roomVolumeState{{"9", "8", 36, 36}, {"8", "9", 36, 36}, {"8", "8", 35, 35}} {
		if currentRoomVolumeEffect(effect, f.Scope, f.Claim, f.Native, f.Route, changed) != nil {
			t.Fatal("foreign or later C mutation cannot confirm old volume effect")
		}
	}
	roomVolumeStore.Lock()
	roomVolumeStore.Records[id].Result.Protocol = false
	roomVolumeStore.Unlock()
	if currentRoomVolumeEffect(effect, f.Scope, f.Claim, f.Native, f.Route, after) != nil {
		t.Fatal("unknown or rejected receipt cannot confirm volume")
	}
}
func volumePost(kind string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/api/room-volume/"+kind, strings.NewReader(`{"prepare_id":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}`))
	if kind == "commit" {
		roomVolumeCommitHandler(w, r)
	} else {
		roomVolumeCancelHandler(w, r)
	}
	return w
}
func TestRoomVolumeReadAndPrepareNeverMintCtokenOrWriteVolume(t *testing.T) {
	f := managerVolumeFixture(t)
	q := url.Values{"target_id": {f.Scope.Target}, "room_id": {"r0"}, "homepod_id": {"42"}, "account_user_id": {"native-user"}}
	w := httptest.NewRecorder()
	roomVolumeStateHandler(w, httptest.NewRequest("GET", "/api/room-volume/state?"+q.Encode(), nil))
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"base_percent":27`) {
		t.Fatal(w.Code, w.Body.String())
	}
	p := f.prepare(t, 0)
	if p.Code != 200 || !strings.Contains(p.Body.String(), `"volume_percent":0`) {
		t.Fatal(p.Code, p.Body.String())
	}
	for _, call := range f.Calls {
		if strings.HasPrefix(call, "POST ") && call != "POST /player/room_volume/bound-prepare" && !strings.Contains(call, "/bound-state") {
			t.Fatal("Ctoken or effect minted", call)
		}
	}
	if f.Volume.BasePercent != 27 {
		t.Fatal("read/prepare changed manual27")
	}
}
func TestRoomVolumeCommitOnlyNativeOneUseAndNoImplicit35(t *testing.T) {
	f := managerVolumeFixture(t)
	p := f.prepare(t, 44)
	if p.Code != 200 {
		t.Fatal(p.Code, p.Body.String())
	}
	w := volumePost("commit")
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"protocol_accepted":true`) {
		t.Fatal(w.Code, w.Body.String())
	}
	if w := volumePost("commit"); w.Code != 409 {
		t.Fatal("duplicate dispatched", w.Code)
	}
	n := 0
	for _, call := range f.Calls {
		if call == "POST /player/room_volume/bound-commit" {
			n++
		}
		if strings.Contains(call, "spotify") || strings.Contains(call, "attention") || strings.Contains(call, "/volume/commit") {
			t.Fatal("wrong effect owner", call)
		}
	}
	if n != 1 || f.Volume.BasePercent != 27 {
		t.Fatal(n, f.Volume)
	}
}
func TestRoomVolumePreparedOwnerOrVolumeRevisionChangeRejects(t *testing.T) {
	for name, change := range map[string]func(*nativeRoomVolumePrepare){
		"aliasABA":          func(p *nativeRoomVolumePrepare) { p.Native.Revision += 2 },
		"session":           func(p *nativeRoomVolumePrepare) { p.Native.Session = "replacement" },
		"otherRoom":         func(p *nativeRoomVolumePrepare) { p.Native.Room = "r6" },
		"otherFIFO":         func(p *nativeRoomVolumePrepare) { p.Route.InputDigest = strings.Repeat("e", 64) },
		"manualRevision":    func(p *nativeRoomVolumePrepare) { p.Volume.BaseRevision = "9" },
		"attentionRevision": func(p *nativeRoomVolumePrepare) { p.Volume.AttentionRevision = "9" },
		"changedPercent":    func(p *nativeRoomVolumePrepare) { p.Percent = 35 },
	} {
		t.Run(name, func(t *testing.T) {
			f := managerVolumeFixture(t)
			f.PrepareMutate = change
			w := f.prepare(t, 44)
			if w.Code != 409 {
				t.Fatal(w.Code, w.Body.String())
			}
			if w := volumePost("commit"); w.Code != 409 {
				t.Fatal("stale prepared authority admitted")
			}
		})
	}
}
func TestRoomVolumeUnknownConsumedAndReadCannotUpgrade(t *testing.T) {
	f := managerVolumeFixture(t)
	if w := f.prepare(t, 44); w.Code != 200 {
		t.Fatal(w.Code)
	}
	f.Commit = func(context.Context) ([]byte, error) {
		return []byte(`{"schema":"podconnect.native_room_volume_commit.v2","retry_allowed":true}`), nil
	}
	w := volumePost("commit")
	if !strings.Contains(w.Body.String(), `"state":"unknown"`) || !strings.Contains(w.Body.String(), `"possible_effect":true`) {
		t.Fatal(w.Body.String())
	}
	f.Commit = nil
	r := httptest.NewRecorder()
	roomVolumeResultHandler(r, httptest.NewRequest("GET", "/api/room-volume/result?prepare_id="+strings.Repeat("a", 32), nil))
	if !strings.Contains(r.Body.String(), `"state":"unknown"`) {
		t.Fatal("receipt upgraded", r.Body.String())
	}
	if w := volumePost("commit"); w.Code != 409 {
		t.Fatal("unknown replayed")
	}
}
func TestRoomVolumeCancelBeforeCommitAndWhileCommitAwaits(t *testing.T) {
	t.Run("before", func(t *testing.T) {
		f := managerVolumeFixture(t)
		if w := f.prepare(t, 44); w.Code != 200 {
			t.Fatal(w.Code)
		}
		if w := volumePost("cancel"); w.Code != 200 {
			t.Fatal(w.Code)
		}
		if w := volumePost("commit"); w.Code != 409 {
			t.Fatal("cancelled offer committed")
		}
		for _, s := range f.Calls {
			if s == "POST /player/room_volume/bound-commit" {
				t.Fatal("late effect sent")
			}
		}
	})
	t.Run("held", func(t *testing.T) {
		f := managerVolumeFixture(t)
		if w := f.prepare(t, 44); w.Code != 200 {
			t.Fatal(w.Code)
		}
		entered, release := make(chan struct{}), make(chan struct{})
		f.Commit = func(ctx context.Context) ([]byte, error) {
			close(entered)
			select {
			case <-release:
				return nil, fmt.Errorf("consumed uncertain")
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
		done := make(chan *httptest.ResponseRecorder, 1)
		go func() { done <- volumePost("commit") }()
		<-entered
		if w := volumePost("cancel"); w.Code != 200 {
			t.Fatal(w.Code, w.Body.String())
		}
		close(release)
		w := <-done
		if !strings.Contains(w.Body.String(), `"state":"unknown"`) {
			t.Fatal(w.Body.String())
		}
		if w := volumePost("commit"); w.Code != 409 {
			t.Fatal("unknown replay")
		}
	})
}
func TestRoomVolumeClosedBodiesAndInconsistentTerminalReceiptReject(t *testing.T) {
	f := managerVolumeFixture(t)
	for _, raw := range []string{`{"prepare_id":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","pin":true}`, `{"prepare_id":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","prepare_id":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}`, `{"prepare_id":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"} {}`} {
		w := httptest.NewRecorder()
		roomVolumeCommitHandler(w, httptest.NewRequest("POST", "/api/room-volume/commit", strings.NewReader(raw)))
		if w.Code != 400 {
			t.Fatal(w.Code, raw)
		}
	}
	for _, bad := range []nativeRoomVolumeResult{{State: "protocol_accepted", OK: true, Protocol: true}, {State: "unknown", Terminal: true, Drained: false}, {State: "cancelled_before_admission", Possible: true}, {State: "pending", Terminal: true, Drained: true}} {
		bad.Schema = "podconnect.native_room_volume_commit.v2"
		bad.ID = strings.Repeat("a", 32)
		if validNativeRoomVolumeResult(bad, bad.ID, "commit") {
			t.Fatal("invalid owner proof accepted", bad)
		}
	}
	f.Route.InputDigest = strings.Repeat("e", 64)
	if w := f.prepare(t, 44); w.Code != 409 {
		t.Fatal("foreignFIFO prepared", w.Code)
	}
}

func TestRoomVolumeCapacityNeverEvictsUnprovenCleanup(t *testing.T) {
	f := managerVolumeFixture(t)
	roomVolumeStore.Lock()
	for i := 1; i <= 128; i++ {
		roomVolumeStore.Records[fmt.Sprintf("%032x", i)] = &roomVolumeRecord{Dispatched: true, Result: &nativeRoomVolumeResult{State: "unknown", Possible: true}}
	}
	roomVolumeStore.Unlock()
	if w := f.prepare(t, 44); w.Code != 409 {
		t.Fatal("active unknown cleanup evicted", w.Code)
	}
	if len(f.Calls) != 0 {
		t.Fatal("capacity miss issued new authority", f.Calls)
	}
	roomVolumeStore.Lock()
	roomVolumeStore.Records[fmt.Sprintf("%032x", 1)].CleanupTerminal = true
	roomVolumeStore.Unlock()
	if w := f.prepare(t, 44); w.Code != 200 {
		t.Fatal("drained record could not retire", w.Code, w.Body.String())
	}
	roomVolumeStore.Lock()
	defer roomVolumeStore.Unlock()
	if len(roomVolumeStore.Records) != 128 {
		t.Fatal("unbounded receipts", len(roomVolumeStore.Records))
	}
	if _, ok := roomVolumeStore.Records[fmt.Sprintf("%032x", 1)]; ok {
		t.Fatal("did not remove certified cleanup")
	}
}

func TestRoomVolumeCapacityRetiresExpiredUndispatchedOffers(t *testing.T) {
	f := managerVolumeFixture(t)
	roomVolumeStore.Lock()
	for i := 1; i <= 128; i++ {
		roomVolumeStore.Records[fmt.Sprintf("%032x", i)] = &roomVolumeRecord{Expires: time.Now().Add(-time.Second)}
	}
	roomVolumeStore.Unlock()
	if w := f.prepare(t, 44); w.Code != 200 {
		t.Fatal("expired abandoned preparations permanently exhausted capacity", w.Code, w.Body.String())
	}
	roomVolumeStore.Lock()
	if len(roomVolumeStore.Records) != 128 {
		t.Error("capacity changed", len(roomVolumeStore.Records))
	}
	evicted := ""
	for i := 1; i <= 128; i++ {
		id := fmt.Sprintf("%032x", i)
		if _, present := roomVolumeStore.Records[id]; !present {
			evicted = id
			break
		}
	}
	roomVolumeStore.Unlock()
	if evicted == "" {
		t.Fatal("no expired offer retired")
	}
	w := httptest.NewRecorder()
	roomVolumeCommitHandler(w, httptest.NewRequest("POST", "/api/room-volume/commit", strings.NewReader(`{"prepare_id":"`+evicted+`"}`)))
	if w.Code != 409 {
		t.Fatal("expired offer became replayable", w.Code)
	}
	for _, call := range f.Calls {
		if call == "POST /player/room_volume/bound-commit" {
			t.Fatal("old expired offer sent effect")
		}
	}
}

func TestRoomVolumeRejectsOverflowAndMissingCapability(t *testing.T) {
	f := managerVolumeFixture(t)
	f.Volume.BaseRevision = "18446744073709551616"
	if w := f.prepare(t, 44); w.Code != 400 {
		t.Fatal("overflow revision accepted", w.Code)
	}
	f.Volume.BaseRevision = "7"
	prior := roomTransportRPC
	roomTransportRPC = func(ctx context.Context, base, method, path string, body []byte) ([]byte, error) {
		if strings.HasPrefix(path, "/player/room_volume/bound-state?") {
			return nil, fmt.Errorf("old receiver has no volume reservation contract")
		}
		return prior(ctx, base, method, path, body)
	}
	if w := f.prepare(t, 44); w.Code != 409 {
		t.Fatal("audio-only receiver implied volume", w.Code)
	}
}
