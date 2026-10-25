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
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// Reproduce a released primary that is still playing a completed native START.
// All network reads are inert local fixtures; the ordinary bridge's writes are
// recorded by releasedURIFixture so a mistaken recovery is visible.
func releasedRoomRouteFixture(t *testing.T) (*releasedStartFixture, roomTransportScope, *roomTransportIdentity, *outputTransportIdentity) {
	t.Helper()
	f := releasedURIFixture(t)
	f.room.HomepodID = "42"
	f.room.HomepodName = "Kitchen HomePod"
	f.selected = true
	f.volume = 63 // a user's manual setting must survive a current START
	f.room.fill()
	store.mu.Lock()
	err := store.saveLocked(&roomsFile{NextIdx: 1, Rooms: []*Room{f.room}})
	store.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	entries, ok := aliasEntriesForRooms([]*Room{f.room}, []string{f.room.Name})
	if !ok {
		t.Fatal("room alias unavailable")
	}
	binding := f.rt.live.Get().AliasBinding
	binding.Registry = aliasRegistryHash(entries)
	f.rt.live.set(glStatus{Active: true, Paused: false, HasVol: true, VolPct: 63, SelAlias: 1, AliasBinding: binding})
	scope := roomTransportScope{f.id, "r0", "42", "native-user"}
	owner := &roomTransportIdentity{scope.Account, scope.Target, scope.Room, scope.Homepod, "connection", "session", binding.Incarnation, binding.Registry, 1}
	route := &outputTransportIdentity{"aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee", "42", "1", "2", "3", "4", "5", "6", configuredTransportInput(startClaim{RT: f.rt})}
	return f, scope, owner, route
}

func TestReleasedPlayingBridgeHoldsOwnedAndUnknownWithoutVolumeOrSelection(t *testing.T) {
	for _, mode := range []string{"owned", "unknown", "busy", "foreign_owner", "stale_native", "stale_output", "changed_room", "paused", "idle", "clean_absent", "unselected_clean_absent"} {
		t.Run(mode, func(t *testing.T) {
			f, scope, owner, route := releasedRoomRouteFixture(t)
			if mode == "unselected_clean_absent" {
				f.selected = false
			}
			oldRPC := roomTransportRPC
			t.Cleanup(func() { roomTransportRPC = oldRPC })
			var reads []string
			roomTransportRPC = func(_ context.Context, _, method, path string, body []byte) ([]byte, error) {
				reads = append(reads, method+" "+path)
				switch {
				case path == "/status":
					return []byte(fmt.Sprintf(`{"username":"native-user","device_id":%q}`, f.id)), nil
				case strings.HasPrefix(path, "/player/room_transport/bound-state?"):
					if mode == "stale_native" {
						f.rt.live.applyEvent("selected_alias", map[string]any{"id": 2})
					}
					if mode == "changed_room" {
						f.room.HomepodID = "43"
						store.mu.Lock()
						_ = store.saveLocked(&roomsFile{NextIdx: 1, Rooms: []*Room{f.room}})
						store.mu.Unlock()
					}
					actual := *owner
					if mode == "foreign_owner" {
						actual.Account = "foreign"
					}
					return json.Marshal(receiverTransportState{"podconnect.native_room_transport_state.v2", true, actual, []string{"pause"}, nil, false})
				case path == "/api/outputs/42/transport/bound-state":
					var supplied struct {
						Owner roomTransportIdentity `json:"native_owner"`
					}
					if json.Unmarshal(body, &supplied) != nil || supplied.Owner != *owner {
						t.Fatal("C read was not bound to the receiver owner")
					}
					state := outputTransportState{"podconnect.output_transport_state.v2", true, *route, false, []string{"pause"}, false, mode == "owned", mode == "clean_absent" || mode == "unselected_clean_absent"}
					if mode == "busy" {
						state.Busy = true
					}
					if mode == "stale_output" {
						f.rt.live.applyEvent("selected_alias", map[string]any{"id": 2})
					}
					return json.Marshal(state)
				default:
					t.Fatalf("unexpected native endpoint %s", path)
					return nil, fmt.Errorf("unexpected endpoint")
				}
			}
			if mode == "paused" || mode == "idle" {
				status := f.rt.live.Get()
				status.Paused = mode == "paused"
				status.Active = mode != "idle"
				f.rt.live.set(status)
			}
			status, intent := f.rt.live.transportSnapshot()
			hold := holdReleasedBridgeRoute(f.room, status, intent)
			if hold != (mode != "clean_absent") {
				t.Fatalf("released route %s hold=%v reads=%v", mode, hold, reads)
			}
			if len(f.effects) != 0 || f.volume != 63 || !fileExists(releasedPath(f.room)) {
				t.Fatalf("route read changed volume, selection, or released marker: %+v", f)
			}
			if mode == "owned" {
				claim, native, output, err := readRoomTransport(context.Background(), scope)
				if err != nil || !claim.Released || native.Identity != *owner || output.Identity != *route {
					t.Fatalf("current START room controls unavailable: %v", err)
				}
			}
			if (mode == "paused" || mode == "idle") && len(reads) != 0 {
				t.Fatalf("released %s unexpectedly read or mutated route: %v", mode, reads)
			}
		})
	}
}

func TestCurrentNativeAccountRequiresExactCanonicalStatus(t *testing.T) {
	oldRPC := roomTransportRPC
	t.Cleanup(func() { roomTransportRPC = oldRPC })
	engine := "a123456789012345678901234567890123456789"
	for _, raw := range []string{
		`{"username":"native-user","username":"other","device_id":"a123456789012345678901234567890123456789"}`,
		`{"username":"native-user","device_id":"wrong"}`,
		`{"username":" native-user","device_id":"a123456789012345678901234567890123456789"}`,
		`{"username":"native-user","device_id":"a123456789012345678901234567890123456789"} {}`,
	} {
		roomTransportRPC = func(context.Context, string, string, string, []byte) ([]byte, error) { return []byte(raw), nil }
		if account := currentNativeAccount(context.Background(), "http://native.invalid", engine); account != "" {
			t.Fatalf("untrusted status account accepted: %q", raw)
		}
	}
	roomTransportRPC = func(context.Context, string, string, string, []byte) ([]byte, error) {
		return []byte(`{"username":"native-user","device_id":"a123456789012345678901234567890123456789"}`), nil
	}
	if account := currentNativeAccount(context.Background(), "http://native.invalid", engine); account != "native-user" {
		t.Fatal("exact native account status rejected")
	}
}

// The real 200 ms roomBridge loop must stop before its alias pre-prime and PUT.
// A child process bounds the otherwise permanent bridge goroutine.
func TestReleasedActualBridgeSkipsWritesUntilCleanAbsence(t *testing.T) {
	mode := os.Getenv("PC25_RELEASED_BRIDGE_CHILD")
	if mode == "" {
		for _, mode := range []string{"owned", "unknown", "idle", "clean_absent"} {
			t.Run(mode, func(t *testing.T) {
				ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
				defer cancel()
				cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestReleasedActualBridgeSkipsWritesUntilCleanAbsence$", "-test.timeout=5s")
				cmd.Env = append(os.Environ(), "PC25_RELEASED_BRIDGE_CHILD="+mode)
				if output, err := cmd.CombinedOutput(); err != nil {
					t.Fatalf("actual released bridge %s: %v\n%s", mode, err, output)
				}
			})
		}
		return
	}
	f, _, owner, route := releasedRoomRouteFixture(t)
	if mode == "idle" {
		status := f.rt.live.Get()
		status.Active = false
		f.rt.live.set(status)
	}
	var writes atomic.Int32
	var primes, selections atomic.Int32
	previous := http.DefaultTransport
	http.DefaultTransport = startTransport(func(req *http.Request) (*http.Response, error) {
		if req.Method != http.MethodGet {
			writes.Add(1)
			if req.Method == http.MethodPut && req.URL.Path == "/api/player/volume" {
				primes.Add(1)
			}
			if req.Method == http.MethodPut && req.URL.Path == "/api/outputs/set" {
				selections.Add(1)
			}
		}
		return previous.RoundTrip(req)
	})
	reads := make(chan struct{}, 1)
	roomTransportRPC = func(_ context.Context, _, _, path string, _ []byte) ([]byte, error) {
		switch {
		case path == "/status":
			return []byte(fmt.Sprintf(`{"username":"native-user","device_id":%q}`, f.id)), nil
		case strings.HasPrefix(path, "/player/room_transport/bound-state?"):
			return json.Marshal(receiverTransportState{"podconnect.native_room_transport_state.v2", true, *owner, []string{"pause"}, nil, false})
		case path == "/api/outputs/42/transport/bound-state":
			select {
			case reads <- struct{}{}:
			default:
			}
			return json.Marshal(outputTransportState{"podconnect.output_transport_state.v2", true, *route, false, []string{"pause"}, false, mode == "owned", mode == "clean_absent"})
		default:
			return nil, fmt.Errorf("unexpected native endpoint %s", path)
		}
	}
	go roomBridge(f.room, &boolFlag{}, &f.rt.live, &attention{})
	if mode != "idle" {
		select {
		case <-reads:
		case <-time.After(2 * time.Second):
			t.Fatal("actual bridge did not reach joined C read")
		}
	}
	// Clean absence is a fact, not a fresh Connect credit. Bound Native routes
	// remain observation-only across subsequent bridge ticks in every mode.
	time.Sleep(650 * time.Millisecond)
	if writes.Load() != 0 || primes.Load() != 0 || selections.Load() != 0 {
		t.Fatalf("no-credit bound bridge %s wrote: all=%d prime=%d select=%d", mode, writes.Load(), primes.Load(), selections.Load())
	}
	if !fileExists(releasedPath(f.room)) {
		t.Fatal("no-credit route consumed released marker")
	}
	os.Exit(0)
}

func TestReleasedRoomTransportRejoinsExactRouteAfterEachAwait(t *testing.T) {
	for _, phase := range []string{"after_native_prepare", "after_output_prepare", "before_commit"} {
		t.Run(phase, func(t *testing.T) {
			f, scope, owner, route := releasedRoomRouteFixture(t)
			oldRPC := roomTransportRPC
			t.Cleanup(func() { roomTransportRPC = oldRPC })
			roomTransportStore.Lock()
			roomTransportStore.Busy = false
			roomTransportStore.Offer = nil
			roomTransportStore.Unlock()
			current := *route
			var outputPrepares, commits int
			roomTransportRPC = func(_ context.Context, _, _, path string, body []byte) ([]byte, error) {
				switch {
				case strings.HasPrefix(path, "/player/room_transport/bound-state?"):
					return json.Marshal(receiverTransportState{"podconnect.native_room_transport_state.v2", true, *owner, []string{"pause"}, nil, false})
				case path == "/api/outputs/42/transport/bound-state":
					var request struct {
						Owner roomTransportIdentity `json:"native_owner"`
					}
					if json.Unmarshal(body, &request) != nil || request.Owner != *owner {
						t.Fatal("C state detached from current receiver")
					}
					return json.Marshal(outputTransportState{"podconnect.output_transport_state.v2", true, current, false, []string{"pause"}, false, true, false})
				case path == "/player/room_transport/bound-prepare":
					if phase == "after_native_prepare" {
						current.Route = "9"
					}
					return json.Marshal(receiverTransportPrepare{"podconnect.native_room_transport.v2", strings.Repeat("a", 32), *owner, "pause", 0, strings.Repeat("b", 64), 5000, "1", nil, nil, ""})
				case path == "/api/outputs/42/transport/bound-prepare":
					outputPrepares++
					if phase == "after_output_prepare" {
						current.Route = "9"
					}
					return json.Marshal(outputTransportPrepare{"podconnect.output_transport_prepare.v2", true, strings.Repeat("c", 32), *route, *owner, "1", 10000})
				case path == "/player/room_transport/bound-commit":
					commits++
					return nil, fmt.Errorf("effect boundary must not be reached")
				default:
					return nil, fmt.Errorf("unexpected endpoint %s", path)
				}
			}
			request, _ := json.Marshal(roomTransportRequest{scope, *owner, *route, "pause", 0, nil, nil, ""})
			prepared := httptest.NewRecorder()
			transportPrepareHandler(prepared, httptest.NewRequest(http.MethodPost, "/api/room-transport/prepare", bytes.NewReader(request)))
			if phase == "before_commit" {
				if prepared.Code != http.StatusOK {
					t.Fatalf("current released route not prepared: %d %s", prepared.Code, prepared.Body.String())
				}
				current.Route = "9"
				if result := transportCommit(t); result.Code != http.StatusConflict {
					t.Fatalf("stale C route reached commit: %d", result.Code)
				}
			} else if prepared.Code != http.StatusConflict {
				t.Fatalf("stale %s reached offer: %d %s", phase, prepared.Code, prepared.Body.String())
			}
			if phase == "after_native_prepare" && outputPrepares != 0 {
				t.Fatal("stale native prepare contacted C effect preparation")
			}
			if commits != 0 || len(f.effects) != 0 || !fileExists(releasedPath(f.room)) {
				t.Fatalf("stale released route crossed effect or marker boundary: commits=%d effects=%v", commits, f.effects)
			}
		})
	}
}

func TestNonreleasedMoveRouteKeepsRoomTransportWithUnknownStartProof(t *testing.T) {
	f := roomTransportFixture(t)
	claim, native, route, state, err := readRoomTransportState(context.Background(), f.Scope)
	if err != nil || claim.Released || state != startRouteCleanAbsent || native.Identity != f.Native || route.Identity != f.Route {
		t.Fatalf("baseline ordinary room controls lost: %v", err)
	}
	// The C proof intentionally reports neither START-owned nor clean absence
	// for an active MOVE. Existing nonreleased transport admission still works.
	original := roomTransportRPC
	roomTransportRPC = func(ctx context.Context, base, method, path string, body []byte) ([]byte, error) {
		if path == "/api/outputs/42/transport/bound-state" {
			return json.Marshal(outputTransportState{"podconnect.output_transport_state.v2", true, f.Route, false, []string{"pause"}, false, false, false})
		}
		return original(ctx, base, method, path, body)
	}
	_, _, _, state, err = readRoomTransportState(context.Background(), f.Scope)
	if err != nil || state != startRouteUnknown {
		t.Fatalf("nonreleased MOVE owner lost at private proof: %v", err)
	}
	if _, _, _, err := readRoomTransport(context.Background(), f.Scope); err != nil {
		t.Fatalf("nonreleased MOVE room controls denied: %v", err)
	}
}

func TestReleasedPausedRoomTransportNeedsFreshJoinedStartOwner(t *testing.T) {
	f, scope, owner, route := releasedRoomRouteFixture(t)
	status := f.rt.live.Get()
	status.Paused = true
	f.rt.live.set(status)
	if claim, ok := currentStartClaimAllowReleased(scope.Room, scope.Target, true); !ok || claim.Proof.Mode != "fresh_start" {
		t.Fatal("ordinary paused resume's fresh-start contract changed")
	}
	if claim, ok := currentRoomTransportClaim(scope.Room, scope.Target); !ok || claim.Proof.Mode != "same_session" {
		t.Fatal("exact paused room-transport claim unavailable")
	}
	oldRPC := roomTransportRPC
	t.Cleanup(func() { roomTransportRPC = oldRPC })
	owned := true
	flipAtNativePrepare := true
	var outputPrepares, commits int
	roomTransportRPC = func(_ context.Context, _, _, path string, _ []byte) ([]byte, error) {
		switch {
		case strings.HasPrefix(path, "/player/room_transport/bound-state?"):
			return json.Marshal(receiverTransportState{"podconnect.native_room_transport_state.v2", true, *owner, transportActions, nil, false})
		case path == "/api/outputs/42/transport/bound-state":
			return json.Marshal(outputTransportState{"podconnect.output_transport_state.v2", true, *route, false, transportActions, false, owned, false})
		case path == "/player/room_transport/bound-prepare":
			if flipAtNativePrepare {
				current := f.rt.live.Get()
				current.Paused = false
				f.rt.live.set(current)
			}
			return json.Marshal(receiverTransportPrepare{"podconnect.native_room_transport.v2", strings.Repeat("a", 32), *owner, "resume", 0, strings.Repeat("b", 64), 5000, "1", nil, nil, ""})
		case path == "/api/outputs/42/transport/bound-prepare":
			outputPrepares++
			if flipAtNativePrepare {
				return nil, fmt.Errorf("stale paused owner contacted C prepare")
			}
			return json.Marshal(outputTransportPrepare{"podconnect.output_transport_prepare.v2", true, strings.Repeat("c", 32), *route, *owner, "1", 10000})
		case path == "/player/room_transport/bound-commit":
			commits++
			return nil, fmt.Errorf("stale paused owner reached native effect")
		default:
			return nil, fmt.Errorf("unexpected paused route endpoint %s", path)
		}
	}
	if claim, _, _, state, err := readRoomTransportState(context.Background(), scope); err != nil || state != startRouteOwned || !claim.Released {
		t.Fatalf("loaded paused START owner lost resume controls: %v state=%v claim=%+v", err, state, claim)
	}
	stateRead := httptest.NewRecorder()
	transportStateHandler(stateRead, httptest.NewRequest(http.MethodGet,
		fmt.Sprintf("/api/room-transport/state?target_id=%s&room_id=r0&homepod_id=42&account_user_id=native-user", f.id), nil))
	if stateRead.Code != http.StatusOK || !strings.Contains(stateRead.Body.String(), `"actions":["resume","next","previous","seek","shuffle","repeat"]`) {
		t.Fatalf("released paused route lost supported track actions: %d %s", stateRead.Code, stateRead.Body.String())
	}
	for _, action := range []string{"pause"} {
		request, _ := json.Marshal(roomTransportRequest{scope, *owner, *route, action, 0, nil, nil, ""})
		result := httptest.NewRecorder()
		transportPrepareHandler(result, httptest.NewRequest(http.MethodPost, "/api/room-transport/prepare", bytes.NewReader(request)))
		if result.Code != http.StatusConflict {
			t.Fatalf("released paused %s action admitted: %d", action, result.Code)
		}
	}
	request, _ := json.Marshal(roomTransportRequest{scope, *owner, *route, "resume", 0, nil, nil, ""})
	result := httptest.NewRecorder()
	transportPrepareHandler(result, httptest.NewRequest(http.MethodPost, "/api/room-transport/prepare", bytes.NewReader(request)))
	if result.Code != http.StatusConflict || outputPrepares != 0 {
		t.Fatalf("paused-to-playing change survived native await: %d C prepares=%d", result.Code, outputPrepares)
	}
	current := f.rt.live.Get()
	current.Paused = true
	f.rt.live.set(current)
	flipAtNativePrepare = false
	prepared := httptest.NewRecorder()
	transportPrepareHandler(prepared, httptest.NewRequest(http.MethodPost, "/api/room-transport/prepare", bytes.NewReader(request)))
	if prepared.Code != http.StatusOK || outputPrepares != 1 {
		t.Fatalf("current paused resume not prepared: %d C prepares=%d", prepared.Code, outputPrepares)
	}
	current = f.rt.live.Get()
	current.Paused = false
	f.rt.live.set(current)
	if result := transportCommit(t); result.Code != http.StatusConflict || commits != 0 {
		t.Fatalf("paused-to-playing after offer reached native effect: HTTP %d commits=%d", result.Code, commits)
	}
	current = f.rt.live.Get()
	current.Paused = true
	f.rt.live.set(current)
	owned = false
	if _, _, _, err := readRoomTransport(context.Background(), scope); err == nil {
		t.Fatal("released paused resume was offered without current C START custody")
	}
	status, intent := f.rt.live.transportSnapshot()
	if !holdReleasedBridgeRoute(f.room, status, intent) || len(f.effects) != 0 || !fileExists(releasedPath(f.room)) {
		t.Fatal("paused room control read allowed alias recovery or erased marker")
	}
}

func TestReleasedPausedTrackControlsKeepCurrentOwnerAndNoReplay(t *testing.T) {
	for _, action := range []string{"next", "previous"} {
		for _, unknown := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s_unknown_%v", action, unknown), func(t *testing.T) {
				f, scope, owner, route := releasedRoomRouteFixture(t)
				status := f.rt.live.Get()
				status.Paused = true
				f.rt.live.set(status)
				oldRPC := roomTransportRPC
				t.Cleanup(func() { roomTransportRPC = oldRPC })
				var effects int
				roomTransportRPC = func(_ context.Context, _, _, path string, body []byte) ([]byte, error) {
					switch {
					case strings.HasPrefix(path, "/player/room_transport/bound-state?"):
						return json.Marshal(receiverTransportState{"podconnect.native_room_transport_state.v2", true, *owner, transportActions, nil, false})
					case path == "/api/outputs/42/transport/bound-state":
						return json.Marshal(outputTransportState{"podconnect.output_transport_state.v2", true, *route, false, transportActions, false, true, false})
					case path == "/player/room_transport/bound-prepare":
						var request struct {
							Account, Target, Room, Homepod string
							Action                         string `json:"action"`
							Percent                        int    `json:"volume_percent"`
						}
						if json.Unmarshal(body, &request) != nil || request.Action != action || request.Percent != 0 {
							t.Fatal("wrong paused-track preparation", string(body))
						}
						return json.Marshal(receiverTransportPrepare{"podconnect.native_room_transport.v2", strings.Repeat("a", 32), *owner, action, 0, strings.Repeat("b", 64), 5000, "1", nil, nil, ""})
					case path == "/api/outputs/42/transport/bound-prepare":
						return json.Marshal(outputTransportPrepare{"podconnect.output_transport_prepare.v2", true, strings.Repeat("c", 32), *route, *owner, "1", 10000})
					case path == "/player/room_transport/bound-commit":
						effects++
						if unknown {
							return nil, fmt.Errorf("unknown post-send outcome")
						}
						return []byte(`{"schema":"podconnect.native_room_transport_commit.v2","accepted_native":true,"prepare_id":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","physical_result_verified":false}`), nil
					default:
						return nil, fmt.Errorf("unexpected paused track endpoint %s", path)
					}
				}
				request, _ := json.Marshal(roomTransportRequest{scope, *owner, *route, action, 0, nil, nil, ""})
				prepared := httptest.NewRecorder()
				transportPrepareHandler(prepared, httptest.NewRequest(http.MethodPost, "/api/room-transport/prepare", bytes.NewReader(request)))
				if prepared.Code != http.StatusOK {
					t.Fatalf("owned paused %s unavailable: %d %s", action, prepared.Code, prepared.Body.String())
				}
				result := transportCommit(t)
				expected := `"outcome":"accepted"`
				if unknown {
					expected = `"outcome":"unknown"`
				}
				if result.Code != http.StatusOK || !strings.Contains(result.Body.String(), expected) || effects != 1 {
					t.Fatal("wrong single guarded outcome", result.Code, result.Body.String(), effects)
				}
				if result := transportCommit(t); result.Code != http.StatusConflict || effects != 1 {
					t.Fatal("paused track command replayed", result.Code, effects)
				}
				if !f.rt.live.Get().Paused || len(f.effects) != 0 || !fileExists(releasedPath(f.room)) {
					t.Fatal("manager resumed playback or reclaimed route")
				}
			})
		}
	}
}

func TestConnectCompletedStartObservationDoesNotRouteAgain(t *testing.T) {
	for _, released := range []bool{true, false} {
		for _, mode := range []string{"owned", "unknown", "foreign", "late", "successor", "marker_change", "unselected_owned"} {
			t.Run(fmt.Sprintf("released=%v/%s", released, mode), func(t *testing.T) {
				f, _, owner, route := releasedRoomRouteFixture(t)
				if mode == "unselected_owned" {
					f.selected = false
				}
				if !released {
					if err := os.Remove(releasedPath(f.room)); err != nil {
						t.Fatal(err)
					}
				}
				old := roomTransportRPC
				t.Cleanup(func() { roomTransportRPC = old })
				var protectedPrimes int
				interceptRecoveryPrime(t, func(r *http.Request) *http.Response {
					if r.URL.Query().Get("recovery") != "connect" {
						t.Fatal("unprotected fallback")
					}
					protectedPrimes++
					return &http.Response{StatusCode: http.StatusConflict, Body: io.NopCloser(strings.NewReader(""))}
				})
				reads := 0
				roomTransportRPC = func(_ context.Context, _, method, path string, body []byte) ([]byte, error) {
					reads++
					switch {
					case path == "/status":
						return []byte(fmt.Sprintf(`{"username":"native-user","device_id":%q}`, f.id)), nil
					case strings.HasPrefix(path, "/player/room_transport/bound-state?"):
						n := *owner
						if mode == "foreign" {
							n.Session = "foreign"
							n.Account = "foreign"
						}
						return json.Marshal(receiverTransportState{"podconnect.native_room_transport_state.v2", true, n, []string{"pause"}, nil, false})
					case path == "/api/outputs/42/transport/bound-state":
						var supplied struct {
							Owner roomTransportIdentity `json:"native_owner"`
						}
						if json.Unmarshal(body, &supplied) != nil || supplied.Owner != *owner {
							t.Fatal("unbound C read")
						}
						if mode == "late" {
							f.rt.live.applyEvent("paused", nil)
						}
						if mode == "successor" {
							announceConnect(t, f, 2)
						}
						if mode == "marker_change" {
							if released {
								_ = os.Remove(releasedPath(f.room))
							} else {
								_ = os.WriteFile(releasedPath(f.room), []byte("1"), 0600)
							}
						}
						return json.Marshal(outputTransportState{"podconnect.output_transport_state.v2", true, *route, false, []string{"pause"}, false, mode != "unknown", false})
					}
					t.Fatalf("unexpected effect %s %s", method, path)
					return nil, fmt.Errorf("effect")
				}
				live := &f.rt.live
				announceConnect(t, f, 1)
				handled, receipt := tryExplicitConnectRecoveryOutcome(f.room, live, &attention{})
				if !handled || (receipt != nil) != (mode == "owned") {
					t.Fatalf("handled=%v receipt=%v reads=%d", handled, receipt != nil, reads)
				}
				if mode == "owned" && protectedPrimes != 0 {
					t.Fatal("owned START re-primed")
				}
				if protectedPrimes > 1 {
					t.Fatal("fresh credit retried")
				}
				if receipt != nil && fileExists(releasedPath(f.room)) {
					t.Fatal("completed route retained release marker")
				}
				if mode == "successor" {
					return
				}
				before := reads
				handled, receipt = tryExplicitConnectRecoveryOutcome(f.room, live, &attention{})
				if mode != "successor" && (handled || receipt != nil || reads != before) {
					t.Fatal("spent event replayed")
				}
				if mode != "owned" && !live.holdsConnectRecoveryFence() {
					t.Fatal("unknown route lost fence")
				}
			})
		}
	}
}
