package main

import (
	"context"
	"errors"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type radioFake struct {
	calls       []string
	fail        string
	gates       []string
	observation radioNativeObservation
}

func (f *radioFake) call(s string) error {
	f.calls = append(f.calls, s)
	if s == f.fail {
		return errors.New("unknown")
	}
	return nil
}
func (f *radioFake) Ready(context.Context) ([]radioTarget, []string, error) {
	return []radioTarget{{ID: "r1", Name: "Stuen", Availability: "available", Selectable: true}, {ID: "r2", Name: "Køkken", Availability: "available", Selectable: true}}, f.gates, nil
}
func (f *radioFake) Preflight(_ context.Context, s, t string) error {
	return f.call("preflight:" + s + ":" + t)
}
func (f *radioFake) Reserve(_ context.Context, a, b, id string, g uint64) (radioFence, error) {
	return radioFence{Token: "token", ActionID: id, Generation: g, OwnerGeneration: g + 1000}, f.call("reserve:" + a + ":" + b)
}
func (f *radioFake) PauseAndRelease(_ context.Context, _ radioFence, t string) error {
	return f.call("pause-release:" + t)
}
func (f *radioFake) Start(_ context.Context, _ radioFence, s, t string) error {
	f.observation = radioNativeObservation{State: "playing", SourceKind: "radio", StationID: s, TargetID: t, ObservedAt: time.Now().Unix() - 2, ValidUntil: time.Now().Unix() + 30}
	return f.call("start:" + s + ":" + t)
}
func (f *radioFake) Stop(_ context.Context, _ radioFence, t string) error {
	f.observation = radioNativeObservation{State: "stopped", SourceKind: "none", ObservedAt: time.Now().Unix() - 2, ValidUntil: time.Now().Unix() + 30}
	return f.call("stop:" + t)
}
func (f *radioFake) Volume(_ context.Context, _ radioFence, t string, v int) error {
	f.observation.VolumePercent = &v
	return f.call("volume:" + t)
}
func (f *radioFake) Finish(_ context.Context, fence radioFence) (radioNativeObservation, error) {
	f.observation.OwnerGeneration = fence.OwnerGeneration
	return f.observation, f.call("finish")
}
func radioTestOwner(t *testing.T) (*radioCoordinator, *radioFake) {
	t.Helper()
	f := &radioFake{}
	c := newRadioCoordinator(f)
	if e := c.enable(context.Background()); e != nil {
		t.Fatal(e)
	}
	return c, f
}
func radioQ(c *radioCoordinator, id, a, s, t string) radioCommand {
	x := c.snapshot()
	return radioCommand{ActionID: id, Action: a, StationID: s, TargetID: t, ExpectedRevision: x.Revision, ExpectedProcessID: x.ProcessID, ExpectedGeneration: x.Generation, ExpectedOwnerGeneration: x.OwnerGeneration}
}
func radioDo(t *testing.T, c *radioCoordinator, id, a, s, target string) radioState {
	t.Helper()
	x, e := c.execute(context.Background(), radioQ(c, id, a, s, target))
	if e != nil {
		t.Fatal(e)
	}
	return x
}
func TestRadioUnavailable(t *testing.T) {
	c := newRadioCoordinator(nil)
	s, e := c.execute(context.Background(), radioQ(c, "x", "start", "p3", "r1"))
	if e == nil || s.Enabled || len(s.BlockedReasons) != 6 || s.PhysicalVerified {
		t.Fatal(s, e)
	}
}
func TestRadioCapabilitiesGate(t *testing.T) {
	c := newRadioCoordinator(&radioFake{gates: []string{"missing"}})
	if c.enable(context.Background()) == nil || c.snapshot().Enabled {
		t.Fatal("unsafe enable")
	}
}
func TestRadioStartStopLeavesSpotifyPaused(t *testing.T) {
	c, f := radioTestOwner(t)
	radioDo(t, c, "a", "start", "p3", "r1")
	radioDo(t, c, "b", "stop", "", "")
	want := []string{"preflight:p3:r1", "reserve::r1", "pause-release:r1", "start:p3:r1", "finish", "reserve:r1:", "stop:r1", "finish"}
	if !reflect.DeepEqual(f.calls, want) {
		t.Fatal(f.calls)
	}
}
func TestRadioMoveOrdersEffects(t *testing.T) {
	c, f := radioTestOwner(t)
	radioDo(t, c, "a", "start", "p3", "r1")
	f.calls = nil
	radioDo(t, c, "b", "move", "", "r2")
	want := []string{"preflight:p3:r2", "reserve:r1:r2", "stop:r1", "pause-release:r2", "start:p3:r2", "finish"}
	if !reflect.DeepEqual(f.calls, want) {
		t.Fatal(f.calls)
	}
}
func TestRadioUnknownHasNoRetry(t *testing.T) {
	for _, fail := range []string{"reserve::r1", "pause-release:r1", "start:p3:r1", "finish"} {
		t.Run(fail, func(t *testing.T) {
			c, f := radioTestOwner(t)
			f.fail = fail
			s, e := c.execute(context.Background(), radioQ(c, "a", "start", "p3", "r1"))
			if e == nil || s.State != "unknown" {
				t.Fatal(s, e)
			}
			n := len(f.calls)
			_, e = c.execute(context.Background(), radioQ(c, "b", "start", "p1", "r2"))
			if e == nil || n != len(f.calls) {
				t.Fatal("replayed")
			}
		})
	}
}
func TestRadioPreflightPreservesPlayback(t *testing.T) {
	c, f := radioTestOwner(t)
	radioDo(t, c, "a", "start", "p3", "r1")
	f.fail = "preflight:p1:r1"
	f.calls = nil
	s, e := c.execute(context.Background(), radioQ(c, "b", "start", "p1", "r1"))
	if e == nil || s.State != "playing" || s.StationID != "p3" || len(f.calls) != 1 {
		t.Fatal(s, e, f.calls)
	}
}
func TestRadioDuplicatesDoNotRestart(t *testing.T) {
	c, f := radioTestOwner(t)
	radioDo(t, c, "a", "start", "p3", "r1")
	n := len(f.calls)
	radioDo(t, c, "b", "start", "p3", "r1")
	_, e := c.execute(context.Background(), radioQ(c, "a", "stop", "", ""))
	if e == nil || n != len(f.calls) {
		t.Fatal("duplicate")
	}
}
func TestRadioInvalidIDsAndRestartBinding(t *testing.T) {
	for _, change := range []func(*radioCommand){func(q *radioCommand) { q.TargetID = "all" }, func(q *radioCommand) { q.StationID = "https://evil" }, func(q *radioCommand) { q.ExpectedProcessID = "old" }, func(q *radioCommand) { q.ExpectedRevision = 0 }, func(q *radioCommand) { q.ExpectedGeneration = 42 }} {
		c, f := radioTestOwner(t)
		q := radioQ(c, "x", "start", "p3", "r1")
		change(&q)
		_, e := c.execute(context.Background(), q)
		if e == nil || len(f.calls) != 0 {
			t.Fatal("invalid accepted")
		}
	}
}
func TestRadioPreemptExactGeneration(t *testing.T) {
	c, _ := radioTestOwner(t)
	s := radioDo(t, c, "a", "start", "p3", "r1")
	if c.observeNativePreempt("r2", s.Generation) || c.observeNativePreempt("r1", s.Generation-1) || !c.observeNativePreempt("r1", s.Generation) || c.snapshot().State != "unknown" {
		t.Fatal("preempt")
	}
}
func TestRadioHTTPAuthAndStrictJSON(t *testing.T) {
	old := dataDir
	defer func() { dataDir = old }()
	dataDir = t.TempDir()
	w := httptest.NewRecorder()
	radioStateHandler(w, httptest.NewRequest("GET", "/api/radio/state", nil))
	if w.Code != 401 {
		t.Fatal(w.Code)
	}
	if e := os.WriteFile(filepath.Join(dataDir, "options.json"), []byte(`{"attention_token":"secret"}`), 0600); e != nil {
		t.Fatal(e)
	}
	for _, body := range []string{`{"evil":true}`, `{} {}`, strings.Repeat("x", 5000)} {
		r := httptest.NewRequest("POST", "/api/radio/action", strings.NewReader(body))
		r.Header.Set("X-PodConnect-Token", "secret")
		w = httptest.NewRecorder()
		radioActionHandler(w, r)
		if w.Code != 400 {
			t.Fatal(w.Code, w.Body.String())
		}
	}
}

func TestRadioDoesNotStopObservedSpotifySource(t *testing.T) {
	c, f := radioTestOwner(t)
	c.state.State = "playing"
	c.state.SourceKind = "spotify"
	c.state.TargetID = "r1"
	if _, e := c.execute(context.Background(), radioQ(c, "a", "stop", "", "")); e == nil {
		t.Fatal("Spotify source offered radio stop")
	}
	if len(f.calls) != 0 {
		t.Fatal("radio stopped Spotify", f.calls)
	}
	radioDo(t, c, "b", "start", "p3", "r1")
	for _, call := range f.calls {
		if call == "stop:r1" {
			t.Fatal("radio generic stopped Spotify", f.calls)
		}
	}
}

type radioReconFake struct {
	*radioFake
	result      radioReconciliation
	readErr     error
	reads       int
	observation radioNativeObservation
}

func (f *radioReconFake) Reconcile(context.Context, radioFence) (radioReconciliation, error) {
	f.reads++
	return f.result, f.readErr
}
func (f *radioReconFake) Observe(context.Context) (radioNativeObservation, error) {
	f.reads++
	return f.observation, f.readErr
}
func TestRadioKeepsSourceObservationTime(t *testing.T) {
	c, f := radioTestOwner(t)
	s := radioDo(t, c, "a", "start", "p3", "r1")
	if s.ObservedAt == nil || *s.ObservedAt != f.observation.ObservedAt || *s.ValidUntil != f.observation.ValidUntil || *s.ObservedAt == time.Now().Unix() {
		t.Fatal("manager manufactured source time", s)
	}
}
func TestRadioUnknownReconcilesOnlyMatchingDurableTerminal(t *testing.T) {
	for _, failure := range []string{"reserve::r1", "start:p3:r1", "finish"} {
		t.Run(failure, func(t *testing.T) {
			c, base := radioTestOwner(t)
			f := &radioReconFake{radioFake: base}
			c.backend = f
			base.fail = failure
			s, e := c.execute(context.Background(), radioQ(c, "a", "start", "p3", "r1"))
			if e == nil || s.State != "unknown" {
				t.Fatal(s, e)
			}
			calls := len(base.calls)
			o := radioNativeObservation{State: "playing", SourceKind: "radio", StationID: "p3", TargetID: "r1", OwnerGeneration: 1000 + s.Generation, ObservedAt: time.Now().Unix() - 1, ValidUntil: time.Now().Unix() + 30}
			f.result = radioReconciliation{ActionID: "foreign", Generation: s.Generation, Token: c.fence.Token, Terminal: true, Outcome: "committed", Observation: o}
			c.refresh(context.Background())
			if c.snapshot().State != "unknown" {
				t.Fatal("foreign unlocked")
			}
			f.result.ActionID = "a"
			f.result.Terminal = false
			c.refresh(context.Background())
			if c.snapshot().State != "unknown" {
				t.Fatal("nonterminal unlocked")
			}
			f.result.Terminal = true
			f.readErr = context.DeadlineExceeded
			c.refresh(context.Background())
			if c.snapshot().State != "unknown" {
				t.Fatal("timeout unlocked")
			}
			f.readErr = nil
			f.result.Observation.TargetID = "r2"
			c.refresh(context.Background())
			if c.snapshot().State != "unknown" {
				t.Fatal("wrong output unlocked")
			}
			f.result.Observation = o
			c.refresh(context.Background())
			if c.snapshot().State != "playing" || calls != len(base.calls) {
				t.Fatal("reconciliation did not settle read-only", c.snapshot(), base.calls)
			}
		})
	}
}
func TestRadioOldSameOwnerObservationCannotRollBack(t *testing.T) {
	c, base := radioTestOwner(t)
	s := radioDo(t, c, "a", "start", "p3", "r1")
	f := &radioReconFake{radioFake: base, observation: base.observation}
	f.observation.OwnerGeneration = s.OwnerGeneration
	f.observation.ObservedAt = *s.ObservedAt - 1
	v := 99
	f.observation.VolumePercent = &v
	c.backend = f
	c.refresh(context.Background())
	after := c.snapshot()
	if *after.ObservedAt != *s.ObservedAt || after.VolumePercent != nil {
		t.Fatal("old read rolled state backward", after)
	}
}

type radioBadFinish struct{ *radioFake }

func (f *radioBadFinish) Finish(_ context.Context, fence radioFence) (radioNativeObservation, error) {
	o := f.observation
	o.OwnerGeneration = fence.OwnerGeneration
	o.TargetID = "r2"
	return o, nil
}
func TestRadioWrongOutputReceiptIsUnknown(t *testing.T) {
	c, base := radioTestOwner(t)
	c.backend = &radioBadFinish{base}
	s, e := c.execute(context.Background(), radioQ(c, "a", "start", "p3", "r1"))
	if e == nil || s.State != "unknown" || s.ObservedAt != nil {
		t.Fatal("wrong native output accepted", s, e)
	}
}

type radioBlockedPreflight struct {
	*radioFake
	entered chan struct{}
	release chan struct{}
}

func (f *radioBlockedPreflight) Preflight(ctx context.Context, s, target string) error {
	close(f.entered)
	select {
	case <-f.release:
		return f.radioFake.Preflight(ctx, s, target)
	case <-ctx.Done():
		return ctx.Err()
	}
}
func TestRadioConcurrentCommandAndPreemptDuringPreflight(t *testing.T) {
	c, base := radioTestOwner(t)
	radioDo(t, c, "a", "start", "p3", "r1")
	base.calls = nil
	f := &radioBlockedPreflight{base, make(chan struct{}), make(chan struct{})}
	c.backend = f
	done := make(chan error, 1)
	q := radioQ(c, "b", "start", "p1", "r1")
	go func() { _, e := c.execute(context.Background(), q); done <- e }()
	<-f.entered
	pending := c.snapshot()
	if _, e := c.execute(context.Background(), radioQ(c, "c", "start", "p8-jazz", "r2")); e == nil {
		t.Fatal("concurrent command admitted")
	}
	if !c.observeNativePreempt("r1", pending.Generation) {
		t.Fatal("exact native preempt rejected")
	}
	close(f.release)
	if <-done == nil {
		t.Fatal("stale preempted operation succeeded")
	}
	if len(base.calls) != 1 || base.calls[0] != "preflight:p1:r1" || c.snapshot().SourceKind != "unknown" {
		t.Fatal("preempted operation performed effects", base.calls, c.snapshot())
	}
}
func TestRadioCancelledPreflightHasNoReceiverEffects(t *testing.T) {
	c, base := radioTestOwner(t)
	f := &radioBlockedPreflight{base, make(chan struct{}), make(chan struct{})}
	c.backend = f
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	s, e := c.execute(ctx, radioQ(c, "a", "start", "p3", "r1"))
	if e == nil || len(base.calls) != 0 || s.State != "idle" {
		t.Fatal(s, e, base.calls)
	}
}

type radioOrderedReads struct {
	*radioFake
	entered      chan struct{}
	release      chan struct{}
	older, newer radioNativeObservation
	reads        atomic.Int32
}

func (f *radioOrderedReads) Observe(context.Context) (radioNativeObservation, error) {
	if f.reads.Add(1) == 1 {
		close(f.entered)
		<-f.release
		return f.older, nil
	}
	return f.newer, nil
}
func TestRadioOutOfOrderConcurrentReadsKeepNewestSource(t *testing.T) {
	c, base := radioTestOwner(t)
	s := radioDo(t, c, "a", "start", "p3", "r1")
	older := base.observation
	older.OwnerGeneration = s.OwnerGeneration
	oldVolume, newVolume := 10, 20
	older.VolumePercent = &oldVolume
	newer := older
	newer.ObservedAt++
	newer.VolumePercent = &newVolume
	f := &radioOrderedReads{radioFake: base, entered: make(chan struct{}), release: make(chan struct{}), older: older, newer: newer}
	c.backend = f
	done := make(chan struct{})
	go func() { c.refresh(context.Background()); close(done) }()
	<-f.entered
	c.refresh(context.Background())
	close(f.release)
	<-done
	after := c.snapshot()
	if *after.ObservedAt != newer.ObservedAt || after.VolumePercent == nil || *after.VolumePercent != newVolume {
		t.Fatal("late old HTTP read rolled volume back", after)
	}
}

func TestRadioEqualTimestampConflictingReadCannotRollBack(t *testing.T) {
	c, base := radioTestOwner(t)
	s := radioDo(t, c, "a", "start", "p3", "r1")
	v := 10
	f := &radioReconFake{radioFake: base, observation: base.observation}
	f.observation.OwnerGeneration = s.OwnerGeneration
	f.observation.VolumePercent = &v
	c.backend = f
	c.refresh(context.Background())
	after := c.snapshot()
	if after.VolumePercent != nil || *after.ObservedAt != *s.ObservedAt || after.Revision != s.Revision {
		t.Fatal("equal-second conflicting read applied", after)
	}
}

func TestRadioActionsMatchCurrentStateAndExecutionAdmission(t *testing.T) {
	for _, tc := range []struct {
		name, state, source        string
		enabled, pending, measured bool
		want                       []string
	}{{"disabled", "idle", "unknown", false, false, false, []string{}}, {"idle", "idle", "none", true, false, false, []string{"start"}}, {"stopped", "stopped", "none", true, false, false, []string{"start"}}, {"error", "error", "none", true, false, false, []string{"start"}}, {"spotify", "playing", "spotify", true, false, true, []string{"start"}}, {"unknown", "unknown", "unknown", true, false, true, []string{}}, {"preparing", "preparing", "radio", true, false, true, []string{}}, {"busy", "playing", "radio", true, true, true, []string{}}, {"radio-no-volume", "playing", "radio", true, false, false, []string{"start", "stop", "move"}}, {"radio-volume", "playing", "radio", true, false, true, []string{"start", "stop", "move", "volume"}}} {
		t.Run(tc.name, func(t *testing.T) {
			c, f := radioTestOwner(t)
			c.state.State = tc.state
			c.state.SourceKind = tc.source
			c.state.Enabled = tc.enabled
			c.pending = tc.pending
			c.state.StationID = "p3"
			c.state.TargetID = "r1"
			observed, until := time.Now().Unix()-1, time.Now().Unix()+30
			c.state.ObservedAt = &observed
			c.state.ValidUntil = &until
			if tc.measured {
				v := 30
				c.state.VolumePercent = &v
			}
			snapshot := c.snapshot()
			if !reflect.DeepEqual(snapshot.Actions, tc.want) || !reflect.DeepEqual(snapshot.Capabilities, []string{"start", "stop", "move", "volume"}) {
				t.Fatal(snapshot.Actions, snapshot.Capabilities)
			}
			for _, action := range []string{"start", "stop", "move", "volume"} {
				offered := false
				for _, a := range tc.want {
					offered = offered || a == action
				}
				if offered {
					continue
				}
				q := radioQ(c, "blocked-"+action, action, "", "")
				if action == "start" {
					q.StationID = "p1"
					q.TargetID = "r2"
				}
				if action == "move" {
					q.TargetID = "r2"
				}
				if action == "volume" {
					v := 20
					q.VolumePercent = &v
				}
				before := len(f.calls)
				if _, e := c.execute(context.Background(), q); e == nil || len(f.calls) != before {
					t.Fatal("unoffered action admitted", action, f.calls)
				}
			}
		})
	}
}
func TestRadioStaleOrMissingTargetOffersCannotExposeRouteControls(t *testing.T) {
	c, _ := radioTestOwner(t)
	radioDo(t, c, "a", "start", "p3", "r1")
	expired := time.Now().Unix() - 1
	c.state.ValidUntil = &expired
	v := 30
	c.state.VolumePercent = &v
	if got := c.snapshot().Actions; !reflect.DeepEqual(got, []string{"start"}) {
		t.Fatal(got)
	}
	c.state.State = "stopped"
	c.state.Targets = []radioTarget{{ID: "r1", Availability: "unavailable", Selectable: false}}
	if len(c.snapshot().Actions) != 0 {
		t.Fatal(c.snapshot().Actions)
	}
}
func TestRadioMeasuredVolumeOfferActuallyExecutes(t *testing.T) {
	c, f := radioTestOwner(t)
	radioDo(t, c, "a", "start", "p3", "r1")
	v := 30
	c.state.VolumePercent = &v
	q := radioQ(c, "b", "volume", "", "")
	next := 20
	q.VolumePercent = &next
	s, e := c.execute(context.Background(), q)
	if e != nil || s.VolumePercent == nil || *s.VolumePercent != 20 {
		t.Fatal(s, e, f.calls)
	}
}

func TestRadioNativePreemptDoesNotInventSourceOrOwnerEpoch(t *testing.T) {
	c, base := radioTestOwner(t)
	before := radioDo(t, c, "play", "start", "p3", "r1")
	if !c.observeNativePreempt("r1", before.Generation) {
		t.Fatal("preempt rejected")
	}
	after := c.snapshot()
	if after.OwnerGeneration != before.OwnerGeneration || after.SourceKind != "unknown" || after.State != "unknown" || after.VolumePercent != nil || after.ObservedAt != nil || after.ValidUntil != nil || len(after.Actions) != 0 {
		t.Fatal("preempt invented actual source", after)
	}
	// The next actual observation may use the SAME source epoch; local command
	// invalidation must not manufacture a higher owner that rejects that proof.
	actual := radioNativeObservation{State: "playing", SourceKind: "spotify", TargetID: "r1", OwnerGeneration: before.OwnerGeneration, ObservedAt: time.Now().Unix(), ValidUntil: time.Now().Unix() + 2}
	reconciler := &radioReconFake{radioFake: base, result: radioReconciliation{ActionID: c.fence.ActionID, Generation: c.fence.Generation, Token: c.fence.Token, Terminal: true, Outcome: "preempted", Observation: actual}}
	c.backend = reconciler
	c.refresh(context.Background())
	if got := c.snapshot(); got.OwnerGeneration != before.OwnerGeneration || got.SourceKind != "spotify" {
		t.Fatal("next actual source rejected", got)
	}
}
