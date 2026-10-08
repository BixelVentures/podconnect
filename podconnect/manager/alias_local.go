package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"time"
)

type aliasEntry struct {
	ID     uint32 `json:"id"`
	RoomID string `json:"room_id"`
	Name   string `json:"name"`
}

type aliasBinding struct {
	Incarnation string `json:"incarnation"`
	Registry    string `json:"registry"`
	RoomID      string `json:"room_id"`
	Ready       bool   `json:"ready"`
}

func (b *aliasBinding) valid() bool {
	return b != nil && b.Ready && b.Incarnation != "" && b.Registry != "" && b.RoomID != ""
}

// Only binding value changes re-route a completed equal numeric alias; repeated
// unchanged status/events preserve the existing cadence and failed-route throttle.
func aliasRouteChanged(alias, last int, binding, previous *aliasBinding) bool {
	return alias != last || !sameAliasBinding(binding, previous)
}

func copyAliasBinding(binding *aliasBinding) *aliasBinding {
	if binding == nil {
		return nil
	}
	copied := *binding
	return &copied
}

func sameAliasBinding(a, b *aliasBinding) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}

func eventAliasBinding(data map[string]any) (*aliasBinding, bool) {
	raw, present := data["alias_binding"]
	if !present {
		return nil, false
	}
	encoded, err := json.Marshal(raw)
	if err != nil {
		return nil, true
	}
	var b aliasBinding
	if json.Unmarshal(encoded, &b) != nil {
		return nil, true
	}
	return &b, true
}

func aliasEntriesForRooms(rooms []*Room, names []string) ([]aliasEntry, bool) {
	if len(rooms) == 0 || len(rooms) != len(names) {
		return nil, false
	}
	seen := make(map[string]bool, len(rooms))
	entries := make([]aliasEntry, len(rooms))
	for i, room := range rooms {
		if room == nil || room.ID == "" || names[i] == "" || seen[room.ID] {
			return nil, false
		}
		seen[room.ID] = true
		entries[i] = aliasEntry{ID: uint32(i + 1), RoomID: room.ID, Name: names[i]}
	}
	return entries, true
}

func aliasRegistryHash(entries []aliasEntry) string {
	raw, err := json.Marshal(entries)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

// The lock order is always roomStore then glLive, with no network inside.
// Room mutation before this claim cannot admit an obsolete prepared target.
func admitRoomAliasRoute(prepared *Room, binding *aliasBinding, live *glLive, alias int, revision uint64) bool {
	if binding == nil {
		return live.admitRoute(alias, revision)
	} // old Spotify engine metadata
	if !binding.valid() || prepared == nil || prepared.ID != binding.RoomID {
		return false
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	rf, err := store.loadLocked()
	if err != nil {
		return false
	}
	entries, ok := aliasEntriesForRooms(rf.Rooms, connectAliasesForRooms(rf.Rooms))
	if !ok || aliasRegistryHash(entries) != binding.Registry {
		return false
	}
	matched := false
	for _, entry := range entries {
		if entry.ID == uint32(alias) && entry.RoomID == prepared.ID {
			matched = true
		}
	}
	if !matched {
		return false
	}
	for _, room := range rf.Rooms {
		if room.ID == prepared.ID && room.HomepodID == prepared.HomepodID && room.HomepodName == prepared.HomepodName {
			return live.admitRoute(alias, revision)
		}
	}
	return false
}

type aliasSelectionRequest struct {
	RoomID              string `json:"room_id"`
	ExpectedIncarnation string `json:"expected_incarnation"`
	ExpectedRegistry    string `json:"expected_registry"`
}

// This is a relay to the existing engine, never a second output selector.
func roomSwitchHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	primary := primaryRoom()
	if primary == nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		return
	}
	roomSwitchAt(w, r, primary.Librespot)
}

func roomSwitchAt(w http.ResponseWriter, r *http.Request, base string) {
	var body aliasSelectionRequest
	if r.Method == http.MethodPost {
		r.Body = http.MaxBytesReader(w, r.Body, 4096)
		decoder := json.NewDecoder(r.Body)
		decoder.DisallowUnknownFields()
		if decoder.Decode(&body) != nil || body.RoomID == "" || body.ExpectedIncarnation == "" || body.ExpectedRegistry == "" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		var trailing any
		if decoder.Decode(&trailing) != io.EOF {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		// This local claim does not promise that later configuration can unsend it.
		store.mu.Lock()
		rf, err := store.loadLocked()
		valid := err == nil
		if valid {
			entries, ok := aliasEntriesForRooms(rf.Rooms, connectAliasesForRooms(rf.Rooms))
			valid = ok && aliasRegistryHash(entries) == body.ExpectedRegistry
			found := false
			for _, entry := range entries {
				if entry.RoomID == body.RoomID {
					found = true
				}
			}
			valid = valid && found
		}
		store.mu.Unlock()
		if !valid {
			w.WriteHeader(http.StatusConflict)
			return
		}
	}
	encoded, _ := json.Marshal(body)
	request, err := http.NewRequestWithContext(r.Context(), r.Method, base+"/player/aliases", bytes.NewReader(encoded))
	if err != nil {
		w.WriteHeader(http.StatusBadGateway)
		return
	}
	request.Header.Set("Content-Type", "application/json")
	client := &http.Client{Timeout: 4 * time.Second}
	response, err := client.Do(request)
	if err != nil {
		w.WriteHeader(http.StatusBadGateway)
		return
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(response.Body, 16385))
	if err != nil || len(raw) > 16384 {
		w.WriteHeader(http.StatusBadGateway)
		return
	}
	if response.StatusCode == http.StatusOK {
		// Refuse misleading success from an old engine/default root route.
		var reply struct {
			AcceptedLocal bool          `json:"accepted_local"`
			Binding       *aliasBinding `json:"binding"`
			Aliases       []aliasEntry  `json:"aliases"`
		}
		if json.Unmarshal(raw, &reply) != nil || reply.Binding == nil || !reply.Binding.valid() {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		if r.Method == http.MethodPost && (!reply.AcceptedLocal || reply.Binding.RoomID != body.RoomID || reply.Binding.Registry != body.ExpectedRegistry || reply.Binding.Incarnation != body.ExpectedIncarnation) {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		if r.Method == http.MethodGet && (len(reply.Aliases) == 0 || aliasRegistryHash(reply.Aliases) != reply.Binding.Registry) {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(response.StatusCode)
	_, _ = w.Write(raw)
}
