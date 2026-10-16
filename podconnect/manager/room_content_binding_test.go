package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func contentBindingFixture(t *testing.T) (*Room, *roomRuntime, []*Room, roomTransportScope) {
	t.Helper()
	primary := &Room{ID: "r0", Idx: 0, Name: "Kitchen", HomepodID: "42", ConfigDir: t.TempDir(), Pipe: "/config/native.fifo", Librespot: "http://127.0.0.1:24879", OwnTone: "http://127.0.0.1:3689"}
	id := strings.Repeat("a", 40)
	if err := os.WriteFile(filepath.Join(primary.ConfigDir, "device_id"), []byte(id), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(primary.ConfigDir, "config.yml"), []byte(fmt.Sprintf("device_id: %q\ninitial_volume: 35\n", id)), 0600); err != nil {
		t.Fatal(err)
	}
	other := &Room{ID: "r6", Idx: 1, Name: "Other", HomepodID: "66"}
	return primary, &roomRuntime{room: primary}, []*Room{primary, other}, roomTransportScope{id, "r6", "66", "actual-account"}
}

func TestContentBindingAdmitsExplicitOwnedDestinationWithoutFabricatingActiveSession(t *testing.T) {
	primary, runtime, rooms, scope := contentBindingFixture(t)
	// This genuine runtime has no source run, session, selected alias or route.
	// Native authentication/effect admission is separate from manager mapping.
	got, ok := roomContentBindingFor(primary, runtime, rooms, scope)
	if !ok || got.Scope.Room != "r6" || got.Scope.Homepod != "66" || got.RT != runtime || got.Registry == "" || got.PipeDigest == "" {
		t.Fatalf("explicit actual catalogue destination rejected: %+v %v", got, ok)
	}
	for name, invalid := range map[string]roomTransportScope{
		"foreign engine": {strings.Repeat("b", 40), "r6", "66", "actual-account"},
		"unknown room":   {scope.Target, "tesla", "66", "actual-account"},
		"wrong output":   {scope.Target, "r6", "42", "actual-account"},
	} {
		t.Run(name, func(t *testing.T) {
			if _, ok := roomContentBindingFor(primary, runtime, rooms, invalid); ok {
				t.Fatal("foreign destination admitted")
			}
		})
	}
}

func TestContentBindingInvalidatesHeldCatalogueConfigAndRuntime(t *testing.T) {
	primary, runtime, rooms, scope := contentBindingFixture(t)
	old := roomContentBindingNow
	t.Cleanup(func() { roomContentBindingNow = old })
	roomContentBindingNow = func(s roomTransportScope) (roomContentBinding, bool) {
		return roomContentBindingFor(primary, runtime, rooms, s)
	}
	prepared, ok := roomContentBindingNow(scope)
	if !ok || !roomContentBindingCurrent(prepared) {
		t.Fatal("actual initial binding unavailable")
	}
	rooms[1].HomepodID = "77"
	if roomContentBindingCurrent(prepared) {
		t.Fatal("room/output replacement preserved stale binding")
	}
	rooms[1].HomepodID = "66"
	if !roomContentBindingCurrent(prepared) {
		t.Fatal("unchanged current mapping unavailable")
	}
	runtime = &roomRuntime{room: primary}
	if roomContentBindingCurrent(prepared) {
		t.Fatal("runtime replacement preserved stale binding")
	}
	runtime = prepared.RT
	runtime.live.beginRun(nil)
	if roomContentBindingCurrent(prepared) {
		t.Fatal("same runtime's native process replacement preserved stale binding")
	}
	prepared, ok = roomContentBindingNow(scope)
	if !ok {
		t.Fatal("new actual source unavailable")
	}
	if err := os.WriteFile(filepath.Join(primary.ConfigDir, "config.yml"), []byte(fmt.Sprintf("device_id: %q\ninitial_volume: 35\nchanged: true\n", scope.Target)), 0600); err != nil {
		t.Fatal(err)
	}
	if roomContentBindingCurrent(prepared) {
		t.Fatal("source configuration replacement preserved stale binding")
	}
}
