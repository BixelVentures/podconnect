package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestConfiguredRadioBootstrapNegotiatesWithoutEffects(t *testing.T) {
	legacy := producerNativeStateFixture()
	legacy.ControllerID = "AABBCCDDEEFF0011"
	legacy.Missing = []string{}
	legacy.Targets[0].Selectable = true
	legacy.Targets[0].Availability = "available"
	now := time.Now().Unix()
	good := radioPrimaryState{
		Schema: primaryRadioSchema, Protocol: primaryRadioProtocol, Process: legacy.ProcessID, Revision: "5",
		Missing: []string{}, Targets: []radioProducerTarget{{radioTarget: radioTarget{ID: "room-A", Availability: "available", Selectable: true}, HomePodID: "123"}},
		Observation: &radioNativeObservation{State: "idle", SourceKind: "none", ObservedAt: now, ValidUntil: now + 5},
	}
	paused := good
	paused.Observation = &radioNativeObservation{State: "stopped", SourceKind: "spotify", TargetID: "room-A", ObservedAt: now, ValidUntil: now + 5}
	olderC := radioPrimaryState{Schema: primaryRadioSchema, Protocol: "v1", Process: legacy.ProcessID, Revision: "5"}
	wrongProcess := olderC
	wrongProcess.Process = strings.Repeat("f", 64)
	incomplete := good
	incomplete.Missing = []string{"no_owner_proof"}
	malformed := good
	malformed.Schema = "malformed-fixture"
	unknown := good
	unknown.Schema = "unknown-fixture"

	fakePairing := good
	fakePairing.Targets = append([]radioProducerTarget{}, good.Targets...)
	fakePairing.Targets[0].ReceiverDigest = strings.Repeat("b", 64)
	for _, tc := range []struct {
		name                             string
		replies                          []*radioPrimaryState
		physical, wantReady, wantPrimary bool
		wantV1                           int
	}{
		{"old Native", []*radioPrimaryState{nil}, true, true, false, 1},
		{"paused retained Spotify", []*radioPrimaryState{&paused}, true, true, true, 0},
		{"old C same owner", []*radioPrimaryState{&olderC}, true, true, false, 1},
		{"old C different owner", []*radioPrimaryState{&wrongProcess}, true, false, false, 1},
		{"matched primary", []*radioPrimaryState{&good}, true, true, true, 0},
		{"primary without opt-in", []*radioPrimaryState{&good}, false, false, false, 0},
		{"no fallback after advertisement", []*radioPrimaryState{&incomplete, nil}, true, false, false, 0},
		{"no fallback after malformed HTTP 200", []*radioPrimaryState{&malformed, nil}, true, false, false, 0},
		{"no fallback after unknown field HTTP 200", []*radioPrimaryState{&unknown, nil}, true, false, false, 0},
		{"pairing digest is not primary proof", []*radioPrimaryState{&fakePairing}, true, false, false, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			reads, v1 := 0, 0
			previous := http.DefaultTransport
			defer func() { http.DefaultTransport = previous }()
			http.DefaultTransport = startTransport(func(r *http.Request) (*http.Response, error) {
				if r.Method != http.MethodGet || r.Header.Get("X-PodConnect-Token") != "control" {
					t.Fatalf("non-passive or unauthenticated request: %s %s", r.Method, r.URL.Path)
				}
				var value any
				status := http.StatusOK
				switch r.URL.Path {
				case "/radio/primary":
					state := tc.replies[reads]
					reads++
					if state == nil {
						status = http.StatusNotFound
					} else if state == &malformed {
						return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(`{"schema":"bad"`))}, nil
					} else if state == &unknown {
						return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(`{"schema":"podconnect.native_radio_primary.v1","future_owner":true}`))}, nil
					} else {
						value = state
					}
				case "/radio/handoff/state":
					v1++
					value = legacy
				default:
					t.Fatalf("unexpected path %s", r.URL.Path)
				}
				raw, err := json.Marshal(value)
				if err != nil {
					return nil, err
				}
				return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(string(raw)))}, nil
			})
			stop := errors.New("fixture exhausted")
			selected, primary, err := awaitConfiguredRadioBootstrap(context.Background(), radioProducerBootstrapOptions{
				NativeURL: "http://127.0.0.1:4001", NativeToken: "control", Physical: tc.physical,
			}, func(context.Context, time.Duration) error {
				if reads >= len(tc.replies) {
					return stop
				}
				return nil
			})
			if (err == nil) != tc.wantReady || (selected != nil) != tc.wantReady || primary != tc.wantPrimary || v1 != tc.wantV1 {
				t.Fatalf("unexpected selection ready=%t primary=%t v1=%d err=%v", selected != nil, primary, v1, err)
			}
		})
	}
}

func TestPrimaryRadioProducerRequiresFreshBootstrapOwnerOnSecondRead(t *testing.T) {
	for _, name := range []string{"unknown source", "no selectable receiver", "changed process", "missing proof list", "expired observation", "same owner"} {
		t.Run(name, func(t *testing.T) {
			radioSettingsTestOptions(t, true)
			if err := ensureRadioWorkerSettings(); err != nil {
				t.Fatal(err)
			}
			options, err := readRadioProducerBootstrapOptions()
			if err != nil || !radioWorkerConfigured(options) || options.Physical {
				t.Fatalf("expected the standard worker with physical acceptance unset: %v", err)
			}
			options.NativeURL = "http://127.0.0.1:4001"
			now := time.Now().Unix()
			pinned := strings.Repeat("a", 64)
			good := radioPrimaryState{
				Schema: primaryRadioSchema, Protocol: primaryRadioProtocol, Process: pinned, Revision: "5",
				Missing: []string{}, Targets: []radioProducerTarget{{radioTarget: radioTarget{
					ID: "room-A", Availability: "available", Selectable: true,
				}, HomePodID: "123"}},
				Observation: &radioNativeObservation{State: "idle", SourceKind: "none", ObservedAt: now, ValidUntil: now + 5},
			}
			next := good
			switch name {
			case "unknown source":
				next.Observation = &radioNativeObservation{State: "unknown", SourceKind: "unknown", ObservedAt: now, ValidUntil: now + 5}
			case "no selectable receiver":
				next.Targets = []radioProducerTarget{{radioTarget: radioTarget{ID: "room-A", Availability: "unavailable"}, HomePodID: "123"}}
			case "changed process":
				next.Process = strings.Repeat("b", 64)
			case "missing proof list":
				next.Missing = nil
			case "expired observation":
				next.Observation = &radioNativeObservation{State: "idle", SourceKind: "none", ObservedAt: now - 5, ValidUntil: now - 1}
			}
			reads := 0
			previous := http.DefaultTransport
			defer func() { http.DefaultTransport = previous }()
			http.DefaultTransport = startTransport(func(req *http.Request) (*http.Response, error) {
				if req.Method != http.MethodGet || req.URL.Path != "/radio/primary" ||
					req.Header.Get("X-PodConnect-Token") != options.NativeToken {
					t.Fatal("bootstrap must only read the authenticated primary owner")
				}
				state := good
				if reads > 0 {
					state = next
				}
				reads++
				raw, err := json.Marshal(state)
				return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(string(raw)))}, err
			})
			ctx := context.Background()
			n, primary, err := awaitConfiguredRadioBootstrap(ctx, options,
				func(context.Context, time.Duration) error { return errors.New("unexpected bootstrap retry") })
			if err != nil || !primary {
				t.Fatalf("initial read did not select the exact primary owner: %v", err)
			}
			dir, err := filepath.EvalSymlinks(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			p, err := newPrimaryRadioProducer(n, filepath.Join(dir, "private-journal"))
			if err != nil {
				t.Fatal(err)
			}
			p.evidence.NativeProcess = n.primaryBootstrapProcess
			c := newRadioCoordinator(p)
			err = c.enable(ctx)
			wantEnabled := name == "same owner"
			state := c.snapshot()
			if reads != 2 || (err == nil) != wantEnabled || state.Enabled != wantEnabled || state.PhysicalVerified {
				t.Fatalf("second bootstrap read: enabled=%t physical=%t reads=%d err=%v", state.Enabled, state.PhysicalVerified, reads, err)
			}
		})
	}
}

func TestPrimaryRadioWorkerBootstrapRejectsUnsafePrivateSettings(t *testing.T) {
	radioSettingsTestOptions(t, true)
	if err := ensureRadioWorkerSettings(); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(radioWorkerSettingsPath(), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := readRadioProducerBootstrapOptions(); err == nil {
		t.Fatal("bootstrap admitted unsafe worker credentials")
	}
}
