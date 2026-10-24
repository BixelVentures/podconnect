package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestConnectRecoveryDiagnosticsFiniteAndClosed(t *testing.T) {
	f, _, owner, route := releasedRoomRouteFixture(t)
	installConnectRouteRead(t, f, owner, route)
	f.selected = false
	var out bytes.Buffer
	previous := log.Writer()
	log.SetOutput(&out)
	t.Cleanup(func() { log.SetOutput(previous) })
	for i := 0; i < 10; i++ {
		tryExplicitConnectRecovery(f.room, &f.rt.live, nil)
	}
	if out.Len() != 0 {
		t.Fatal("idle loop logged")
	}
	announceConnect(t, f, 1)
	f.failPrime = true
	tryExplicitConnectRecovery(f.room, &f.rt.live, nil)
	before := out.String()
	for i := 0; i < 10; i++ {
		tryExplicitConnectRecovery(f.room, &f.rt.live, nil)
	}
	if out.String() != before {
		t.Fatal("spent attempt repeated diagnostics")
	}
	for _, wanted := range []string{"manager reason=event_accepted", "manager reason=intent_consumed", "volume reason=http_rejected", "route reason=prime_rejected_or_stale", "manager reason=attempt_finished"} {
		if !strings.Contains(before, wanted) {
			t.Fatalf("missing closed phase %s", wanted)
		}
	}
	for _, line := range strings.Split(strings.TrimSpace(before), "\n") {
		at := strings.Index(line, "connect-recovery ")
		if at < 0 {
			t.Fatal("unexpected log")
		}
		fields := strings.Fields(line[at:])
		if len(fields) != 3 || !strings.HasPrefix(fields[2], "reason=") {
			t.Fatal("unbounded diagnostic fields")
		}
	}
	if len(f.effects) != 1 || !fileExists(releasedPath(f.room)) {
		t.Fatal("diagnostics changed failure/replay outcome")
	}
}

func TestConnectRecoveryCancellationLoggedOnce(t *testing.T) {
	for _, event := range []string{"selected_alias", "paused", "stopped"} {
		t.Run(event, func(t *testing.T) {
			f, _, _, _ := releasedRoomRouteFixture(t)
			var out bytes.Buffer
			previous := log.Writer()
			log.SetOutput(&out)
			t.Cleanup(func() { log.SetOutput(previous) })
			announceConnect(t, f, 1)
			f.rt.live.applyEvent(event, nil)
			f.rt.live.applyEvent(event, nil)
			if strings.Count(out.String(), "manager reason=event_cancelled") != 1 {
				t.Fatal("cancellation missing or repeated")
			}
			if f.rt.live.connectRecovery != nil || len(f.effects) != 0 {
				t.Fatal("cancellation changed authority/effects")
			}
		})
	}
}
func TestConnectRecoveryOwnershipChangeHasTerminalPhase(t *testing.T) {
	f, _, owner, route := releasedRoomRouteFixture(t)
	installConnectRouteRead(t, f, owner, route)
	f.selected = false
	var out bytes.Buffer
	previous := log.Writer()
	log.SetOutput(&out)
	t.Cleanup(func() { log.SetOutput(previous) })
	announceConnect(t, f, 1)
	f.onPrime = func() { f.rt.live.applyEvent("paused", nil) }
	tryExplicitConnectRecovery(f.room, &f.rt.live, nil)
	if !strings.Contains(out.String(), "route reason=pre_send_stale") || !strings.Contains(out.String(), "manager reason=route_not_committed") {
		t.Fatal("ownership change has no terminal phase")
	}
	if len(f.effects) != 1 || f.selected || !fileExists(releasedPath(f.room)) {
		t.Fatal("stale owner changed effects")
	}
}
