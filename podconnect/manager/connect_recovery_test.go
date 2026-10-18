package main

import (
	"encoding/json"
	"net/http"
	"os"
	"testing"
	"time"
)

func announceConnect(t *testing.T, f *releasedStartFixture, ordinal float64) glSource {
	t.Helper()
	l := &f.rt.live
	raw, _ := json.Marshal(l.Get().AliasBinding)
	var b map[string]any
	_ = json.Unmarshal(raw, &b)
	source := glSource{run: l.runEpoch, phase: l.phaseEpoch}
	l.applySourceEvent(source, "connect_transfer", map[string]any{"id": float64(1), "ordinal": ordinal, "alias_binding": b})
	return source
}
func TestExplicitConnectRecoveryOneShot(t *testing.T) {
	f, _, _, _ := releasedRoomRouteFixture(t)
	f.selected = false
	old := http.DefaultTransport
	http.DefaultTransport = startTransport(func(r *http.Request) (*http.Response, error) {
		if r.Method == "PUT" && r.URL.Query().Get("recovery") != "connect" {
			t.Fatal("unguarded recovery mutation")
		}
		return old.RoundTrip(r)
	})
	announceConnect(t, f, 1)
	if !tryExplicitConnectRecovery(f.room, &f.rt.live, nil) || !f.selected || f.volume != 35 || fileExists(releasedPath(f.room)) {
		t.Fatal("fresh recovery failed", f.effects)
	}
	n := len(f.effects)
	_ = os.WriteFile(releasedPath(f.room), []byte("1"), 0600)
	announceConnect(t, f, 1)
	if tryExplicitConnectRecovery(f.room, &f.rt.live, nil) || len(f.effects) != n {
		t.Fatal("replayed transfer recovered")
	}
}
func TestExplicitConnectRecoveryInvalidations(t *testing.T) {
	for _, mode := range []string{"no_event", "poll", "old_source", "reseed_changed", "paused", "expired", "failed_prime", "superseded_during_read", "superseded_during_prime", "attention"} {
		t.Run(mode, func(t *testing.T) {
			f, _, _, _ := releasedRoomRouteFixture(t)
			f.selected = false
			l := &f.rt.live
			if mode != "no_event" && mode != "poll" {
				announceConnect(t, f, 1)
			}
			switch mode {
			case "poll":
				source := glSource{run: l.runEpoch, phase: l.phaseEpoch}
				r, _ := l.beginStatus(source)
				l.acceptStatus(r, l.Get())
			case "old_source":
				l.retirePhase(glSource{run: l.runEpoch, phase: l.phaseEpoch})
			case "reseed_changed":
				s := l.Get()
				s.AliasBinding = copyAliasBinding(s.AliasBinding)
				s.AliasBinding.Ready = false
				source := glSource{run: l.runEpoch, phase: l.phaseEpoch}
				r, _ := l.beginStatus(source)
				l.acceptStatus(r, s)
			case "paused":
				l.applyEvent("paused", nil)
			case "expired":
				l.connectRecovery.until = time.Now().Add(-time.Second)
			case "failed_prime":
				f.failPrime = true
			case "superseded_during_read":
				f.onRead = func() { l.applyEvent("paused", nil) }
			case "superseded_during_prime":
				f.onPrime = func() { l.applyEvent("paused", nil) }
			}
			var att *attention
			if mode == "attention" {
				att = &attention{nativeBusy: true}
			}
			tryExplicitConnectRecovery(f.room, l, att)
			if !fileExists(releasedPath(f.room)) || f.selected {
				t.Fatal("invalid recovery selected or cleared marker", f.effects)
			}
			allowed := 0
			if mode == "failed_prime" || mode == "superseded_during_prime" {
				allowed = 1
			}
			if len(f.effects) != allowed {
				t.Fatalf("unexpected effects %v", f.effects)
			}
			n := len(f.effects)
			tryExplicitConnectRecovery(f.room, l, att)
			if len(f.effects) != n {
				t.Fatal("automatic replay")
			}
		})
	}
}
func TestConnectRecoveryConsumedTransportOwnerCannotChange(t *testing.T) {
	f, _, _, _ := releasedRoomRouteFixture(t)
	l := &f.rt.live
	announceConnect(t, f, 1)
	credit, ok := l.consumeConnectRecovery()
	if !ok {
		t.Fatal("missing credit")
	}
	l.applyEvent("paused", nil)
	if l.startIntentCurrent(credit.transport) {
		t.Fatal("consume captured later owner")
	}
	if len(f.effects) != 0 {
		t.Fatal("effect")
	}
}
