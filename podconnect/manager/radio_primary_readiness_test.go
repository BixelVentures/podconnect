package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
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
