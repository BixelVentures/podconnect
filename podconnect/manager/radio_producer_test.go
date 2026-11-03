package main

// These are orchestration/journal unit fixtures. They are not native custody,
// resource, pairing, decoder, RTP or physical acceptance evidence.
import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

type producerFixture struct {
	mu                sync.Mutex
	calls             []string
	fail              string
	p                 *radioProducer
	observation       radioNativeObservation
	nativeResults     map[string]radioProducerResult
	senderResults     map[string]radioProducerResult
	last              radioProducerRecord
	preemptChallenges []string
	sourceGeneration  uint64
	targetEpochs      map[string]uint64
}
type producerNativeFixture struct{ f *producerFixture }
type producerSenderFixture struct{ f *producerFixture }

func (f *producerFixture) call(name string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, name)
	if f.fail == name {
		return errors.New("inert lost reply")
	}
	return nil
}
func (n producerNativeFixture) CaptureIntent(context.Context, string) (radioProducerIntent, error) {
	return radioProducerIntent{Process: "native-process", Revision: "1"}, nil
}
func (n producerNativeFixture) Evidence(context.Context) (radioProducerEvidence, error) {
	return radioProducerEvidence{NativeProcess: "native-process"}, nil
}
func (s producerSenderFixture) Evidence(context.Context, radioProducerEvidence) (radioProducerEvidence, error) {
	return radioProducerEvidence{SenderProcess: "sender-process", Targets: []radioTarget{{ID: "r1", Selectable: true, Availability: "available"}, {ID: "r2", Selectable: true, Availability: "available"}}}, nil
}
func (n producerNativeFixture) Reserve(_ context.Context, q radioProducerReserve) (radioProducerHold, error) {
	if e := n.f.call("native-reserve"); e != nil {
		return radioProducerHold{}, e
	}
	// The journal must already contain the exact admitted call before any peer.
	raw, e := os.ReadFile(filepath.Join(n.f.p.journal.directory, producerRecordName(q.Handoff)))
	if e != nil || !json.Valid(raw) {
		return radioProducerHold{}, errors.New("unjournaled effect")
	}
	h := radioProducerHold{radioProducerIdentity: q.radioProducerIdentity, Token: "native-secret", Proof: json.RawMessage(`{"native_owner":"fixture"}`)}
	if q.Prior != nil && q.Prior.Target == q.Target {
		h = q.Prior.Native
	}
	n.f.nativeResults[q.ActionID] = radioProducerResult{Identity: q.radioProducerIdentity, Terminal: true, Outcome: "committed", Hold: &h}
	return h, nil
}
func (n producerNativeFixture) PauseSuspend(_ context.Context, h radioProducerHold, op radioProducerIdentity) error {
	return n.f.call("pause-suspend:" + h.Target)
}
func (n producerNativeFixture) Result(_ context.Context, q radioProducerReserve, h radioProducerHold) (radioProducerResult, error) {
	i := q.radioProducerIdentity
	if h.Handoff != "" {
		i = h.radioProducerIdentity
	}
	r, ok := n.f.nativeResults[i.ActionID]
	if !ok {
		return r, errRadioUnknown
	}
	return r, nil
}
func (s producerSenderFixture) Preflight(_ context.Context, station, target string, operation radioProducerIdentity) (radioProducerPeer, error) {
	if _, ok := radioStreamURLs[station]; !ok {
		return radioProducerPeer{}, errRadioUnavailable
	}
	sourceGeneration := uint64(1001)
	if s.f.sourceGeneration > 0 {
		sourceGeneration = s.f.sourceGeneration
	}
	ownerEpoch := uint64(1001)
	if value := s.f.targetEpochs[target]; value > 0 {
		ownerEpoch = value
	}
	return radioProducerPeer{Process: "sender-process", SourceGeneration: sourceGeneration, OwnerGeneration: ownerEpoch, Target: target, ReceiverDigest: strings.Repeat("a", 64), CandidateID: operation.Handoff, Proof: json.RawMessage(`{"fixture":"prepared"}`)}, s.f.call("preflight:" + target)
}
func (s producerSenderFixture) Reserve(_ context.Context, q radioProducerReserve, n radioProducerHold) (radioProducerHold, error) {
	if e := s.f.call("sender-reserve"); e != nil {
		return radioProducerHold{}, e
	}
	h := radioProducerHold{radioProducerIdentity: q.radioProducerIdentity, Token: "sender-secret", Proof: json.RawMessage(`{"peer":"fixture"}`)}
	h.Process = "sender-process"
	h.OwnerGeneration = q.Peer.OwnerGeneration
	h.SourceGeneration = q.Peer.SourceGeneration
	return h, nil
}
func (s producerSenderFixture) Start(_ context.Context, h, n radioProducerHold, op radioProducerIdentity, station, target string) error {
	if e := s.f.call("start:" + target); e != nil {
		return e
	}
	s.f.observation = radioNativeObservation{State: "playing", SourceKind: "radio", StationID: station, TargetID: target, OwnerGeneration: h.SourceGeneration, ObservedAt: radioTestNow() - 1, ValidUntil: radioTestNow() + 20}
	return nil
}
func (s producerSenderFixture) StopDrain(_ context.Context, h radioProducerHold, op radioProducerIdentity, target string) (json.RawMessage, error) {
	if e := s.f.call("stop:" + target); e != nil {
		return nil, e
	}
	s.f.observation = radioNativeObservation{State: "stopped", SourceKind: "none", OwnerGeneration: h.SourceGeneration, ObservedAt: radioTestNow() - 1, ValidUntil: radioTestNow() + 20}
	return json.RawMessage(`{"signed_fixture":"old-drain"}`), nil
}
func (s producerSenderFixture) PreemptDrain(_ context.Context, h, n radioProducerHold, q radioNativePreemptRequest) (json.RawMessage, error) {
	if h.SourceGeneration == 0 {
		return nil, errors.New("actual prepared source generation lost")
	}
	if e := s.f.call("preempt:" + q.Target); e != nil {
		return nil, e
	}
	s.f.preemptChallenges = append(s.f.preemptChallenges, string(q.Challenge))
	return json.Marshal(map[string]any{"fixture_only": true, "challenge": q.Challenge, "target": q.Target, "reacquire_challenge": strings.Repeat("a", 32)})
}
func (s producerSenderFixture) Volume(_ context.Context, h radioProducerHold, op radioProducerIdentity, target string, v int) error {
	s.f.observation.VolumePercent = &v
	return s.f.call("volume:" + target)
}
func (s producerSenderFixture) Finish(_ context.Context, h radioProducerHold, op radioProducerIdentity) (radioProducerResult, error) {
	r := radioProducerResult{Identity: op, Terminal: true, Outcome: "committed", Observation: s.f.observation}
	s.f.senderResults[op.ActionID] = r
	return r, s.f.call("finish")
}
func (s producerSenderFixture) Observe(context.Context) (radioNativeObservation, error) {
	return s.f.observation, nil
}
func (s producerSenderFixture) Result(_ context.Context, i radioProducerIdentity) (radioProducerResult, error) {
	r, ok := s.f.senderResults[i.ActionID]
	if !ok {
		return r, errRadioUnknown
	}
	return r, nil
}
func producerTest(t *testing.T, physical bool) (*radioProducer, *producerFixture) {
	t.Helper()
	f := &producerFixture{nativeResults: map[string]radioProducerResult{}, senderResults: map[string]radioProducerResult{}}
	directory, _ := filepath.EvalSymlinks(t.TempDir())
	p, e := newRadioProducer(producerNativeFixture{f}, producerSenderFixture{f}, filepath.Join(directory, "private-journal"), physical)
	if e != nil {
		t.Fatal(e)
	}
	f.p = p
	return p, f
}
func producerStart(t *testing.T, p *radioProducer, action, target string, g uint64) radioFence {
	t.Helper()
	ctx := context.Background()
	if e := p.PreflightOperation(ctx, "p3", target, action, g); e != nil {
		t.Fatal(e)
	}
	f, e := p.Reserve(ctx, "", target, action, g)
	if e != nil {
		t.Fatal(e)
	}
	if e = p.PauseAndRelease(ctx, f, target); e != nil {
		t.Fatal(e)
	}
	if e = p.Start(ctx, f, "p3", target); e != nil {
		t.Fatal(e)
	}
	if _, e = p.Finish(ctx, f); e != nil {
		t.Fatal(e)
	}
	return f
}

func radioTestNow() int64 { return time.Now().Unix() }
func TestRadioProducerPhysicalGateAndNilPorts(t *testing.T) {
	p, _ := producerTest(t, false)
	if _, _, e := p.Ready(context.Background()); e == nil {
		t.Fatal("unit ports bypass physical gate")
	}
	if _, e := newRadioProducer(nil, nil, t.TempDir(), true); e == nil {
		t.Fatal("missing ports accepted")
	}
}
func TestRadioProducerJournalBeforeEffectsAndNoRepeatedStage(t *testing.T) {
	p, f := producerTest(t, true)
	if _, _, e := p.Ready(context.Background()); e != nil {
		t.Fatal(e)
	}
	ctx := context.Background()
	if e := p.PreflightOperation(ctx, "p3", "r1", "a", 1); e != nil {
		t.Fatal(e)
	}
	hold, e := p.Reserve(ctx, "", "r1", "a", 1)
	if e != nil {
		t.Fatal(e)
	}
	if e = p.Start(ctx, hold, "p3", "r1"); e == nil {
		t.Fatal("start before actual suspend")
	}
	if e = p.PauseAndRelease(ctx, hold, "r1"); e != nil {
		t.Fatal(e)
	}
	if e = p.Start(ctx, hold, "p3", "r1"); e != nil {
		t.Fatal(e)
	}
	before := len(f.calls)
	if e = p.Start(ctx, hold, "p3", "r1"); e == nil || len(f.calls) != before {
		t.Fatal("source effect repeated")
	}
	if _, e = p.Finish(ctx, hold); e != nil {
		t.Fatal(e)
	}
	if _, e = p.Reserve(ctx, "r1", "r1", "a", 2); e == nil {
		t.Fatal("action ID reused")
	}
}
func TestRadioProducerUnknownNeverReplaysAfterRestart(t *testing.T) {
	p, f := producerTest(t, true)
	_, _, _ = p.Ready(context.Background())
	f.fail = "native-reserve"
	_ = p.PreflightOperation(context.Background(), "p3", "r1", "a", 1)
	_, e := p.Reserve(context.Background(), "", "r1", "a", 1)
	if e == nil {
		t.Fatal("lost reply accepted")
	}
	before := len(f.calls)
	if _, e = p.Reserve(context.Background(), "", "r1", "b", 2); e == nil || len(f.calls) != before {
		t.Fatal("unknown command replay")
	}
	restored, e := newRadioProducer(producerNativeFixture{f}, producerSenderFixture{f}, p.journal.directory, true)
	if e != nil {
		t.Fatal(e)
	}
	if _, _, e = restored.Ready(context.Background()); e == nil {
		t.Fatal("restart adopted unresolved lease")
	}
	if len(f.calls) != before {
		t.Fatal("restart effects")
	}
}
func TestRadioProducerSourceLeaseAndOperationRemainDistinct(t *testing.T) {
	p, f := producerTest(t, true)
	_, _, _ = p.Ready(context.Background())
	initial := producerStart(t, p, "start-a", "r1", 1)
	op, e := p.Reserve(context.Background(), "r1", "", "volume-a", 2)
	if e != nil {
		t.Fatal(e)
	}
	r := p.records[op.Token]
	if r.Sender.Handoff != initial.Token || r.Request.Handoff != op.Token || r.Sender.Generation == r.Request.Generation {
		t.Fatal("source lease replaced by operation")
	}
	if e = p.Volume(context.Background(), op, "r1", 23); e != nil {
		t.Fatal(e)
	}
	if _, e = p.Finish(context.Background(), op); e != nil {
		t.Fatal(e)
	}
	if f.observation.VolumePercent == nil || *f.observation.VolumePercent != 23 {
		t.Fatal("no measured volume fixture")
	}
}
func TestRadioProducerMovedOldTargetPreemptionRequiresFreshChallengeAndLeavesNewTarget(t *testing.T) {
	p, f := producerTest(t, true)
	_, _, _ = p.Ready(context.Background())
	old := producerStart(t, p, "a", "r1", 1)
	ctx := context.Background()
	_ = p.PreflightOperation(ctx, "p3", "r2", "b", 2)
	moving, e := p.Reserve(ctx, "r1", "r2", "b", 2)
	if e != nil {
		t.Fatal(e)
	}
	if e = p.Stop(ctx, moving, "r1"); e != nil {
		t.Fatal(e)
	}
	if e = p.PauseAndRelease(ctx, moving, "r2"); e != nil {
		t.Fatal(e)
	}
	if e = p.Start(ctx, moving, "p3", "r2"); e != nil {
		t.Fatal(e)
	}
	if _, e = p.Finish(ctx, moving); e != nil {
		t.Fatal(e)
	}
	before := len(f.calls)
	q := radioNativePreemptRequest{Schema: "podconnect.radio-preempt.v1", NativeProcess: "native-process", Handoff: old.Token, Generation: "1", Token: "native-secret", Target: "r1", ActionID: "explicit-play-old", Cause: "resume", Challenge: json.RawMessage(`{"fresh":"challenge-1"}`)}
	reply, e := p.preempt(ctx, q)
	if e != nil || !reply.Terminal {
		t.Fatal(e)
	}
	if len(f.calls) != before+1 || f.calls[len(f.calls)-1] != "preempt:r1" {
		t.Fatal("touched new target", f.calls[before:])
	}
	if p.active != moving.Token {
		t.Fatal("old target replaced new radio owner")
	}
	if string(reply.DrainTicket) == `{"signed_fixture":"old-drain"}` {
		t.Fatal("cached old ticket replayed")
	}
	q.Challenge = json.RawMessage(`{"fresh":"challenge-2"}`)
	if _, e = p.preempt(ctx, q); e == nil {
		t.Fatal("same action rebound to different challenge")
	}
	if len(f.preemptChallenges) != 1 {
		t.Fatal("preempt effect repeated")
	}
}
func TestRadioProducerJournalRejectsAliasedAndTruncatedRecords(t *testing.T) {
	dir := t.TempDir()
	other := t.TempDir()
	if e := os.Symlink(other, filepath.Join(dir, "alias")); e != nil {
		t.Fatal(e)
	}
	if _, _, e := openRadioProducerJournal(filepath.Join(dir, "alias", "journal")); e == nil {
		t.Fatal("aliased journal")
	}
	dir, _ = filepath.EvalSymlinks(t.TempDir())
	dir = filepath.Join(dir, "private-journal")
	if e := os.Mkdir(dir, 0700); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(filepath.Join(dir, "broken.json"), []byte(`{"Fence":`), 0600); e != nil {
		t.Fatal(e)
	}
	if _, _, e := openRadioProducerJournal(dir); e == nil {
		t.Fatal("truncated journal accepted")
	}
}

// The actual native gate can callback while Reserve's HTTP reply is still in
// flight. Its passive durable result establishes the hold; the late ACK cannot
// create a fresh radio continuation.
type producerHeldReserve struct {
	producerNativeFixture
	entered chan struct{}
	release chan struct{}
}

func (n producerHeldReserve) Reserve(ctx context.Context, q radioProducerReserve) (radioProducerHold, error) {
	h, e := n.producerNativeFixture.Reserve(ctx, q)
	close(n.entered)
	<-n.release
	return h, e
}
func TestRadioProducerPreemptBeforeNativeReserveReply(t *testing.T) {
	p, f := producerTest(t, true)
	_, _, _ = p.Ready(context.Background())
	held := producerHeldReserve{producerNativeFixture{f}, make(chan struct{}), make(chan struct{})}
	p.native = held
	if e := p.PreflightOperation(context.Background(), "p3", "r1", "opaque-action", 1); e != nil {
		t.Fatal(e)
	}
	done := make(chan error, 1)
	go func() { _, e := p.Reserve(context.Background(), "", "r1", "opaque-action", 1); done <- e }()
	<-held.entered
	p.mu.Lock()
	var r radioProducerRecord
	for _, v := range p.records {
		r = v
	}
	p.mu.Unlock()
	q := radioNativePreemptRequest{Schema: "podconnect.radio-preempt.v1", NativeProcess: r.Request.Process, Handoff: r.Request.Handoff, Generation: "1", Token: "native-secret", Target: "r1", ActionID: attentionUUID(), Cause: "play", Challenge: json.RawMessage(`{"challenge":"fresh-fixture"}`)}
	reply, e := p.preempt(context.Background(), q)
	close(held.release)
	if e != nil || !reply.Terminal {
		t.Fatalf("preempt: %v %#v", e, reply)
	}
	if e = <-done; e == nil {
		t.Fatal("late reserve ACK revived radio")
	}
	for _, call := range f.calls {
		if call == "sender-reserve" || strings.HasPrefix(call, "start:") || strings.HasPrefix(call, "pause-suspend:") {
			t.Fatal("radio continued after preemption", f.calls)
		}
	}
}

func TestRadioProducerStationChangeRetainsNativeHoldWithoutSecondPause(t *testing.T) {
	p, f := producerTest(t, true)
	_, _, _ = p.Ready(context.Background())
	first := producerStart(t, p, "first", "r1", 1)
	p.mu.Lock()
	oldNative := p.records[first.Token].Native
	p.mu.Unlock()
	if e := p.PreflightOperation(context.Background(), "p1", "r1", "change", 2); e != nil {
		t.Fatal(e)
	}
	fence, e := p.Reserve(context.Background(), "r1", "r1", "change", 2)
	if e != nil {
		t.Fatal(e)
	}
	if e = p.Stop(context.Background(), fence, "r1"); e != nil {
		t.Fatal(e)
	}
	if e = p.PauseAndRelease(context.Background(), fence, "r1"); e != nil {
		t.Fatal(e)
	}
	if e = p.Start(context.Background(), fence, "p1", "r1"); e != nil {
		t.Fatal(e)
	}
	if _, e = p.Finish(context.Background(), fence); e != nil {
		t.Fatal(e)
	}
	reserves, pauses := 0, 0
	for _, call := range f.calls {
		if call == "native-reserve" {
			reserves++
		}
		if call == "pause-suspend:r1" {
			pauses++
		}
	}
	p.mu.Lock()
	newNative := p.records[fence.Token].Native
	p.mu.Unlock()
	if reserves != 1 || pauses != 1 || oldNative.Handoff != newNative.Handoff {
		t.Fatalf("station change touched Spotify again: %v", f.calls)
	}
}

type producerIntentCAS struct {
	producerNativeFixture
	mu       sync.Mutex
	revision string
}

func (n *producerIntentCAS) CaptureIntent(context.Context, string) (radioProducerIntent, error) {
	n.mu.Lock()
	defer n.mu.Unlock()
	return radioProducerIntent{Process: "native-process", Revision: n.revision}, nil
}
func (n *producerIntentCAS) Reserve(ctx context.Context, q radioProducerReserve) (radioProducerHold, error) {
	n.mu.Lock()
	ok := q.ExpectedIntent.Revision == n.revision
	n.mu.Unlock()
	if !ok {
		return radioProducerHold{}, errRadioUnavailable
	}
	return n.producerNativeFixture.Reserve(ctx, q)
}

type producerHeldPreflight struct {
	producerSenderFixture
	entered, release chan struct{}
}

func (s producerHeldPreflight) Preflight(ctx context.Context, station, target string, op radioProducerIdentity) (radioProducerPeer, error) {
	close(s.entered)
	<-s.release
	return s.producerSenderFixture.Preflight(ctx, station, target, op)
}
func TestRadioProducerIntentDuringDecodeCannotBePaused(t *testing.T) {
	p, f := producerTest(t, true)
	_, _, _ = p.Ready(context.Background())
	native := &producerIntentCAS{producerNativeFixture: producerNativeFixture{f}, revision: "1"}
	p.native = native
	held := producerHeldPreflight{producerSenderFixture{f}, make(chan struct{}), make(chan struct{})}
	p.sender = held
	done := make(chan error, 1)
	go func() { done <- p.PreflightOperation(context.Background(), "p3", "r1", "radio", 1) }()
	<-held.entered
	native.mu.Lock()
	native.revision = "2"
	native.mu.Unlock() // same-track explicit Spotify intent
	close(held.release)
	if e := <-done; e != nil {
		t.Fatal(e)
	}
	if _, e := p.Reserve(context.Background(), "", "r1", "radio", 1); e == nil {
		t.Fatal("stale radio request admitted")
	}
	for _, call := range f.calls {
		if call == "native-reserve" || call == "sender-reserve" || strings.HasPrefix(call, "pause-suspend:") || strings.HasPrefix(call, "start:") {
			t.Fatal("new Spotify intent affected", f.calls)
		}
	}
}
func TestRadioProducerRestartRecoversOnlyPassiveExactRejection(t *testing.T) {
	p, f := producerTest(t, true)
	_, _, _ = p.Ready(context.Background())
	f.fail = "preflight:r1"
	if e := p.PreflightOperation(context.Background(), "p3", "r1", "lost-prepare", 1); e == nil {
		t.Fatal("fixture loss missing")
	}
	var record radioProducerRecord
	for _, r := range p.records {
		record = r
	}
	op := record.Request.Peer.Operation
	f.senderResults[op.ActionID] = radioProducerResult{Identity: op, Terminal: true, Outcome: "rejected"}
	restored, e := newRadioProducer(producerNativeFixture{f}, producerSenderFixture{f}, p.journal.directory, true)
	if e != nil {
		t.Fatal(e)
	}
	before := len(f.calls)
	if _, _, e = restored.Ready(context.Background()); e != nil {
		t.Fatal("passive rejection did not unblock readiness", e)
	}
	if len(f.calls) != before || restored.active != "" {
		t.Fatal("recovery replayed or adopted playback")
	}
}

func TestRadioProducerJournalRejectsFIFOWithoutBlocking(t *testing.T) {
	directory, _ := filepath.EvalSymlinks(t.TempDir())
	directory = filepath.Join(directory, "private-journal")
	if e := os.Mkdir(directory, 0700); e != nil {
		t.Fatal(e)
	}
	if e := syscall.Mkfifo(filepath.Join(directory, "special.json"), 0600); e != nil {
		t.Fatal(e)
	}
	done := make(chan error, 1)
	go func() { _, _, e := openRadioProducerJournal(directory); done <- e }()
	select {
	case e := <-done:
		if e == nil {
			t.Fatal("FIFO accepted")
		}
	case <-time.After(time.Second):
		t.Fatal("journal startup blocked opening FIFO")
	}
}

type producerStartAckLost struct{ producerSenderFixture }

func (s producerStartAckLost) Start(ctx context.Context, h, n radioProducerHold, op radioProducerIdentity, station, target string) error {
	if e := s.producerSenderFixture.Start(ctx, h, n, op, station, target); e != nil {
		return e
	}
	s.f.senderResults[op.ActionID] = radioProducerResult{Identity: op, Terminal: true, Outcome: "committed", Observation: s.f.observation}
	return errRadioUnknown
}
func TestRadioProducerLostStartACKPassiveSettlementCapturesStopCustody(t *testing.T) {
	p, f := producerTest(t, true)
	_, _, _ = p.Ready(context.Background())
	p.sender = producerStartAckLost{producerSenderFixture{f}}
	ctx := context.Background()
	if e := p.PreflightOperation(ctx, "p3", "r1", "play", 1); e != nil {
		t.Fatal(e)
	}
	fence, e := p.Reserve(ctx, "", "r1", "play", 1)
	if e != nil {
		t.Fatal(e)
	}
	if e = p.PauseAndRelease(ctx, fence, "r1"); e != nil {
		t.Fatal(e)
	}
	if e = p.Start(ctx, fence, "p3", "r1"); e == nil {
		t.Fatal("lost ACK accepted")
	}
	if p.active != "" {
		t.Fatal("unobserved start adopted")
	}
	if _, e = p.Reconcile(ctx, fence); e != nil {
		t.Fatal(e)
	}
	if p.active != fence.Token {
		t.Fatal("exact live settlement lost source custody")
	}
	if o, e := p.Observe(ctx); e != nil || o.SourceKind != "radio" || o.StationID != "p3" {
		t.Fatal("fresh observed source unavailable", o, e)
	}
	stop, e := p.Reserve(ctx, "r1", "", "stop", 2)
	if e != nil {
		t.Fatal(e)
	}
	if e = p.Stop(ctx, stop, "r1"); e != nil {
		t.Fatal(e)
	}
	if _, e = p.Finish(ctx, stop); e != nil {
		t.Fatal(e)
	}
	stops := 0
	for _, call := range f.calls {
		if call == "stop:r1" {
			stops++
		}
	}
	if stops != 1 {
		t.Fatal("stop effect replayed", f.calls)
	}
}
func TestRadioProducerRestartNeverAdoptsOldTerminalPlayingRecord(t *testing.T) {
	p, f := producerTest(t, true)
	_, _, _ = p.Ready(context.Background())
	producerStart(t, p, "play", "r1", 1)
	restored, e := newRadioProducer(producerNativeFixture{f}, producerSenderFixture{f}, p.journal.directory, true)
	if e != nil {
		t.Fatal(e)
	}
	if _, _, e = restored.Ready(context.Background()); e == nil || restored.active != "" {
		t.Fatal("startup adopted old live session")
	}
}

func (s producerSenderFixture) CancelCandidate(context.Context, radioProducerPeer) (json.RawMessage, error) {
	if e := s.f.call("cancel-candidate"); e != nil {
		return nil, e
	}
	return json.RawMessage(`{"fixture_cancelled":true}`), nil
}

type producerRejectedNative struct {
	producerNativeFixture
	lost bool
}

func (n producerRejectedNative) Reserve(context.Context, radioProducerReserve) (radioProducerHold, error) {
	if n.lost {
		return radioProducerHold{}, errRadioUnknown
	}
	return radioProducerHold{}, &radioProducerOperationRejected{Receipt: json.RawMessage(`{"unit_fixture_only":"stale_no_effect"}`)}
}
func (n producerRejectedNative) Result(context.Context, radioProducerReserve, radioProducerHold) (radioProducerResult, error) {
	return radioProducerResult{}, &radioProducerOperationRejected{Receipt: json.RawMessage(`{"unit_fixture_only":"stale_no_effect"}`)}
}
func TestRadioProducerImmediateNoEffectsRejectionConsumesOnlyCandidate(t *testing.T) {
	p, f := producerTest(t, true)
	_, _, _ = p.Ready(context.Background())
	p.native = producerRejectedNative{producerNativeFixture{f}, false}
	if e := p.PreflightOperation(context.Background(), "p3", "r1", "a", 1); e != nil {
		t.Fatal(e)
	}
	_, e := p.Reserve(context.Background(), "", "r1", "a", 1)
	var rejected *radioProducerNoReceiverEffects
	if !errors.As(e, &rejected) {
		t.Fatal("not definitively rejected", e)
	}
	if strings.Join(f.calls, ",") != "preflight:r1,cancel-candidate" {
		t.Fatal("receiver effect on rejection", f.calls)
	}
	if e = p.PreflightOperation(context.Background(), "p3", "r1", "fresh", 2); e != nil {
		t.Fatal("fresh explicit request blocked after proved no-effects", e)
	}
}
func TestRadioProducerLostRejectionRecoversWithoutSendingCancellation(t *testing.T) {
	p, f := producerTest(t, true)
	_, _, _ = p.Ready(context.Background())
	p.native = producerRejectedNative{producerNativeFixture{f}, true}
	if e := p.PreflightOperation(context.Background(), "p3", "r1", "a", 1); e != nil {
		t.Fatal(e)
	}
	fence, e := p.Reserve(context.Background(), "", "r1", "a", 1)
	if e == nil {
		t.Fatal("lost reply accepted")
	}
	before := len(f.calls)
	if _, e = p.Reconcile(context.Background(), fence); e == nil {
		t.Fatal("unretired candidate treated cancelled")
	}
	r := p.records[fence.Token]
	op := r.Request.Peer.Operation
	// Actual owner expiry is represented by this explicitly unit-only result.
	f.senderResults[op.ActionID] = radioProducerResult{Identity: op, Terminal: true, Outcome: "rejected"}
	f.observation = radioNativeObservation{State: "idle", SourceKind: "none", ObservedAt: radioTestNow() - 1, ValidUntil: radioTestNow() + 20}
	settled, e := p.Reconcile(context.Background(), fence)
	if e != nil || settled.Outcome != "rejected" {
		t.Fatal("exact no-effect rejection not settled", settled, e)
	}
	if len(f.calls) != before {
		t.Fatal("passive recovery sent a cancellation/effect", f.calls)
	}
}

func TestRadioProducerStopResumeReusesRetainedNativeCustody(t *testing.T) {
	p, f := producerTest(t, true)
	_, _, _ = p.Ready(context.Background())
	producerStart(t, p, "first", "r1", 1)
	ctx := context.Background()
	stop, e := p.Reserve(ctx, "r1", "", "stop", 2)
	if e != nil {
		t.Fatal(e)
	}
	if e = p.Stop(ctx, stop, "r1"); e != nil {
		t.Fatal(e)
	}
	if _, e = p.Finish(ctx, stop); e != nil {
		t.Fatal(e)
	}
	if p.active != "" || p.retained["r1"].Native.Handoff == "" {
		t.Fatal("stop lost retained paused Spotify hold")
	}
	if e = p.PreflightOperation(ctx, "p3", "r1", "resume", 3); e != nil {
		t.Fatal(e)
	}
	next, e := p.Reserve(ctx, "", "r1", "resume", 3)
	if e != nil {
		t.Fatal(e)
	}
	if e = p.PauseAndRelease(ctx, next, "r1"); e != nil {
		t.Fatal(e)
	}
	if e = p.Start(ctx, next, "p3", "r1"); e != nil {
		t.Fatal(e)
	}
	if _, e = p.Finish(ctx, next); e != nil {
		t.Fatal(e)
	}
	reserves, pauses := 0, 0
	for _, call := range f.calls {
		if call == "native-reserve" {
			reserves++
		}
		if call == "pause-suspend:r1" {
			pauses++
		}
	}
	if reserves != 1 || pauses != 1 {
		t.Fatal("resume paused/replaced Spotify again", f.calls)
	}
}

type producerUnavailableNativeResult struct{ producerNativeFixture }

func (producerUnavailableNativeResult) Result(context.Context, radioProducerReserve, radioProducerHold) (radioProducerResult, error) {
	return radioProducerResult{}, errRadioUnknown
}
func TestRadioProducerOwnStopDoesNotDependOnNativeProcessAvailability(t *testing.T) {
	p, f := producerTest(t, true)
	_, _, _ = p.Ready(context.Background())
	producerStart(t, p, "play", "r1", 1)
	p.native = producerUnavailableNativeResult{producerNativeFixture{f}}
	ctx := context.Background()
	stop, e := p.Reserve(ctx, "r1", "", "stop", 2)
	if e != nil {
		t.Fatal("own stop depended on Native availability", e)
	}
	if e = p.Stop(ctx, stop, "r1"); e != nil {
		t.Fatal(e)
	}
	if _, e = p.Finish(ctx, stop); e != nil {
		t.Fatal(e)
	}
	if p.active != "" || f.observation.SourceKind != "none" {
		t.Fatal("own radio did not stop")
	}
}

func (s producerSenderFixture) PreemptObservation(_ context.Context, _ radioProducerRecord) (radioNativeObservation, error) {
	if s.f.fail == "preempt-observation" {
		return radioNativeObservation{}, errRadioUnknown
	}
	return s.f.observation, nil
}
func TestRadioProducerPreemptSettlementRequiresNativeCommitExactTicketAndFreshSender(t *testing.T) {
	p, f := producerTest(t, true)
	_, _, _ = p.Ready(context.Background())
	fence := producerStart(t, p, "a", "r1", 1)
	r := p.records[fence.Token]
	q := radioNativePreemptRequest{Schema: "podconnect.radio-preempt.v1", NativeProcess: r.Native.Process, Handoff: r.Native.Handoff, Generation: "1", Token: r.Native.Token, Target: "r1", ActionID: attentionUUID(), Cause: "resume", Challenge: json.RawMessage(`{"inert":"fresh"}`)}
	reply, err := p.preempt(context.Background(), q)
	if err != nil {
		t.Fatal(err)
	}
	before := len(f.calls)
	if _, err = p.Reconcile(context.Background(), fence); err == nil {
		t.Fatal("radio drain alone settled before actual Native commit")
	}
	native := f.nativeResults[r.Native.ActionID]
	native.Outcome = "preempted"
	native.DrainTicket = json.RawMessage(`{"foreign":"ticket"}`)
	f.nativeResults[r.Native.ActionID] = native
	if _, err = p.Reconcile(context.Background(), fence); err == nil {
		t.Fatal("foreign drain settled")
	}
	native.DrainTicket = reply.DrainTicket
	f.nativeResults[r.Native.ActionID] = native
	f.fail = "preempt-observation"
	if _, err = p.Reconcile(context.Background(), fence); err == nil {
		t.Fatal("missing fresh sender proof settled")
	}
	f.fail = ""
	f.observation = radioNativeObservation{State: "stopped", SourceKind: "none", OwnerGeneration: r.Sender.SourceGeneration, ObservedAt: radioTestNow() - 1, ValidUntil: radioTestNow() + 20}
	result, err := p.Reconcile(context.Background(), fence)
	if err != nil || !result.Terminal || result.Outcome != "preempted" {
		t.Fatalf("exact actual completion did not settle: %#v %v", result, err)
	}
	if len(f.calls) != before {
		t.Fatal("passive settlement replayed an effect")
	}
}

func TestRadioProducerMoveBackUsesGlobalSourceGenerationAndRetainsTargetEpoch(t *testing.T) {
	p, f := producerTest(t, true)
	_, _, _ = p.Ready(context.Background())
	f.targetEpochs = map[string]uint64{"r1": 1, "r2": 2}
	f.sourceGeneration = 1
	first := producerStart(t, p, "a", "r1", 1)
	move := func(from, to, action string, g uint64) radioFence {
		t.Helper()
		f.sourceGeneration = g
		ctx := context.Background()
		if err := p.PreflightOperation(ctx, "p3", to, action, g); err != nil {
			t.Fatal(err)
		}
		fence, err := p.Reserve(ctx, from, to, action, g)
		if err != nil {
			t.Fatal(err)
		}
		if err = p.Stop(ctx, fence, from); err != nil {
			t.Fatal(err)
		}
		if err = p.PauseAndRelease(ctx, fence, to); err != nil {
			t.Fatal(err)
		}
		if err = p.Start(ctx, fence, "p3", to); err != nil {
			t.Fatal(err)
		}
		o, err := p.Finish(ctx, fence)
		if err != nil || o.OwnerGeneration != g {
			t.Fatalf("source epoch lost: %#v %v", o, err)
		}
		return fence
	}
	second := move("r1", "r2", "b", 2)
	third := move("r2", "r1", "c", 3)
	for _, pair := range []struct {
		f              radioFence
		source, target uint64
	}{{first, 1, 1}, {second, 2, 2}, {third, 3, 1}} {
		record := p.records[pair.f.Token]
		if pair.f.OwnerGeneration != pair.source || record.Sender.SourceGeneration != pair.source || record.Sender.OwnerGeneration != pair.target || record.Request.Peer.OwnerGeneration != pair.target {
			t.Fatal("source counter conflated with retained target custody", record)
		}
	}
	// A target epoch is never a replacement for the actual source observation.
	record := p.records[third.Token]
	result := f.senderResults["c"]
	result.Observation.OwnerGeneration = 1
	f.senderResults["c"] = result
	if _, err := p.Reconcile(context.Background(), third); err == nil {
		t.Fatal("target epoch settled as current source epoch")
	}
	result.Observation.OwnerGeneration = 3
	f.senderResults["c"] = result
	if _, err := p.Reconcile(context.Background(), third); err != nil {
		t.Fatal(err)
	}
	// Explicit Spotify on the historical B target must not retire current A3.
	old := p.records[second.Token]
	q := radioNativePreemptRequest{Schema: "podconnect.radio-preempt.v1", NativeProcess: old.Native.Process, Handoff: old.Native.Handoff, Generation: "2", Token: old.Native.Token, Target: "r2", ActionID: attentionUUID(), Cause: "resume", Challenge: json.RawMessage(`{"inert":"B-fresh"}`)}
	receipt, err := p.preempt(context.Background(), q)
	if err != nil {
		t.Fatal(err)
	}
	native := f.nativeResults[old.Native.ActionID]
	native.Outcome = "preempted"
	native.DrainTicket = receipt.DrainTicket
	f.nativeResults[old.Native.ActionID] = native
	settled, err := p.Reconcile(context.Background(), second)
	if err != nil || settled.Observation.TargetID != "r1" || settled.Observation.OwnerGeneration != 3 || p.active != third.Token {
		t.Fatalf("historical B preemption disturbed A3: %#v %v", settled, err)
	}
	_ = record
}
