package main

import (
	"path/filepath"
	"testing"
)

// TestAllocPorts covers the port allocator, including the room-0 legacy exception (HA's watchdog
// watches room-0 OwnTone on 3689, so idx 0 must keep the legacy ports).
func TestAllocPorts(t *testing.T) {
	cases := []struct {
		idx        int
		gl, ot, ws int
	}{
		{0, 3678, 3689, 3688}, // legacy exception
		{1, 3701, 3801, 3901},
		{2, 3702, 3802, 3902},
		{5, 3705, 3805, 3905},
	}
	for _, c := range cases {
		gl, ot, ws := allocPorts(c.idx)
		if gl != c.gl || ot != c.ot || ws != c.ws {
			t.Fatalf("allocPorts(%d) = (%d,%d,%d) want (%d,%d,%d)", c.idx, gl, ot, ws, c.gl, c.ot, c.ws)
		}
	}
}

// TestRoomFillLegacyVsNew checks the room-0 legacy paths vs a new room's per-room tree.
func TestRoomFillLegacyVsNew(t *testing.T) {
	dataDir = "/data"
	r0 := &Room{ID: "r0", Idx: 0, Name: "Kitchen"}
	r0.fill()
	if r0.ConfigDir != "/data/go-librespot" || r0.OwnDir != "/data/owntone" || r0.Pipe != "/srv/media/spotify" {
		t.Fatalf("r0 legacy paths wrong: %+v", r0)
	}
	if r0.Librespot != "http://localhost:3678" || r0.OwnTone != "http://localhost:3689" {
		t.Fatalf("r0 legacy urls wrong: %s %s", r0.Librespot, r0.OwnTone)
	}
	if r0.OwnConf != "/data/rooms/r0/owntone.conf" {
		t.Fatalf("r0 owntone.conf should be under /data/rooms/r0 but point db at legacy: %s", r0.OwnConf)
	}
	r1 := &Room{ID: "r1", Idx: 1, Name: "Office"}
	r1.fill()
	if r1.ConfigDir != "/data/rooms/r1/go-librespot" || r1.OwnDir != "/data/rooms/r1/owntone" {
		t.Fatalf("r1 fresh paths wrong: %+v", r1)
	}
	if r1.Pipe != "/srv/media/rooms/r1/spotify" {
		t.Fatalf("r1 pipe wrong: %s", r1.Pipe)
	}
	if r1.Librespot != "http://localhost:3701" || r1.OwnTone != "http://localhost:3801" {
		t.Fatalf("r1 urls wrong: %s %s", r1.Librespot, r1.OwnTone)
	}
}

// TestRoomsJSONRoundTrip exercises save -> load and verifies derived fields are rebuilt and
// next_idx is preserved.
func TestRoomsJSONRoundTrip(t *testing.T) {
	dataDir = t.TempDir()
	s := newRoomStore()
	rf := &roomsFile{
		NextIdx: 3,
		Rooms: []*Room{
			{ID: "r0", Idx: 0, Name: "Kitchen", HomepodName: "Kitchen HomePod"},
			{ID: "r2", Idx: 2, Name: "Office", HomepodName: "Office HomePod", Released: true},
		},
	}
	s.mu.Lock()
	if err := s.saveLocked(rf); err != nil {
		s.mu.Unlock()
		t.Fatalf("save: %v", err)
	}
	got, err := s.loadLocked()
	s.mu.Unlock()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if got.NextIdx != 3 {
		t.Fatalf("next_idx not preserved: %d", got.NextIdx)
	}
	if len(got.Rooms) != 2 {
		t.Fatalf("want 2 rooms, got %d", len(got.Rooms))
	}
	// Derived fields must be rebuilt on load.
	if got.Rooms[0].OwnTone != "http://localhost:3689" {
		t.Fatalf("r0 derived OwnTone not rebuilt: %s", got.Rooms[0].OwnTone)
	}
	if got.Rooms[1].Idx != 2 || got.Rooms[1].Librespot != "http://localhost:3702" {
		t.Fatalf("r2 derived fields wrong: %+v", got.Rooms[1])
	}
	if !got.Rooms[1].Released {
		t.Fatalf("released flag lost in round-trip")
	}
}

// TestAddRoomUniqueness verifies name + HomePod uniqueness guards and monotonic idx allocation.
func TestAddRoomUniqueness(t *testing.T) {
	dataDir = t.TempDir()
	s := newRoomStore()
	// Seed a room-0 so the store isn't empty (skip migration).
	s.mu.Lock()
	_ = s.saveLocked(&roomsFile{NextIdx: 1, Rooms: []*Room{{ID: "r0", Idx: 0, Name: "Kitchen", HomepodName: "Kitchen HomePod"}}})
	s.mu.Unlock()

	r1, err := s.addRoom("Office", "Office HomePod", "")
	if err != nil {
		t.Fatalf("addRoom: %v", err)
	}
	if r1.Idx != 1 || r1.ID != "r1" {
		t.Fatalf("first new room idx/id wrong: %+v", r1)
	}
	if _, err := s.addRoom("office", "Other HomePod", ""); err == nil {
		t.Fatalf("duplicate name (case-insensitive) should be rejected")
	}
	if _, err := s.addRoom("Living Room", "kitchen homepod", ""); err == nil {
		t.Fatalf("duplicate HomePod (case-insensitive) should be rejected")
	}
	if _, err := s.addRoom("Bedroom", "", ""); err == nil {
		t.Fatalf("missing HomePod should be rejected")
	}
	r2, err := s.addRoom("Bedroom", "Bedroom HomePod", "")
	if err != nil {
		t.Fatalf("addRoom r2: %v", err)
	}
	if r2.Idx != 2 {
		t.Fatalf("idx not monotonic: %d", r2.Idx)
	}
	// Remove r1, then add again — idx must NOT be reused.
	if _, err := s.removeRoom("r1"); err != nil {
		t.Fatalf("removeRoom: %v", err)
	}
	r3, err := s.addRoom("Garage", "Garage HomePod", "")
	if err != nil {
		t.Fatalf("addRoom r3: %v", err)
	}
	if r3.Idx != 3 {
		t.Fatalf("idx reused after delete (want 3): %d", r3.Idx)
	}
	if _, err := s.removeRoom("r0"); err == nil {
		t.Fatalf("removing r0 should be rejected")
	}
}

// TestRoomDeviceIDStable verifies the per-room device_id seeds once and is reused thereafter.
func TestRoomDeviceIDStable(t *testing.T) {
	dataDir = t.TempDir()
	r := &Room{ID: "r1", Idx: 1, Name: "Office"}
	r.fill()
	id1 := roomDeviceID(r)
	if len(id1) != 40 {
		t.Fatalf("device_id not 40 hex chars: %q", id1)
	}
	// A rename must NOT change the persisted id.
	r.Name = "Renamed Office"
	id2 := roomDeviceID(r)
	if id2 != id1 {
		t.Fatalf("device_id changed after rename: %q -> %q", id1, id2)
	}
	if _, err := filepath.Abs(r.ConfigDir); err != nil {
		t.Fatalf("config dir: %v", err)
	}
}
