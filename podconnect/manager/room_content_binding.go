package main

// Selected-content ownership starts from the configured engine and its current
// destination catalogue, including an idle engine. It deliberately does not use
// currentStartClaim, whose same-session/selected-output predicates cannot prove
// an explicit transition to a different owned output. This is configuration
// validation only; native and OwnTone still own all effect admission.
import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
)

type roomContentBinding struct {
	Scope        roomTransportScope
	RT           *roomRuntime
	Registry     string
	PipeDigest   string
	ConfigDigest string
	NativeURL    string
	OutputURL    string
	RunEpoch     uint64
	PhaseEpoch   uint64
}

func roomContentBindingFor(primary *Room, rt *roomRuntime, rooms []*Room, scope roomTransportScope) (roomContentBinding, bool) {
	if !validTransportScope(scope) || primary == nil || rt == nil || rt.room == nil || rt.room.ID != primary.ID || configuredEngineID(primary) != scope.Target || primary.Pipe == "" || primary.Librespot == "" || primary.OwnTone == "" {
		return roomContentBinding{}, false
	}
	entries, ok := aliasEntriesForRooms(rooms, connectAliasesForRooms(rooms))
	if !ok || !transportDigest.MatchString(aliasRegistryHash(entries)) {
		return roomContentBinding{}, false
	}
	matches := 0
	for _, entry := range entries {
		if entry.RoomID == scope.Room && entry.HomepodID == scope.Homepod {
			matches++
		}
	}
	if matches != 1 {
		return roomContentBinding{}, false
	}
	raw, err := os.ReadFile(filepath.Join(primary.ConfigDir, "config.yml"))
	if err != nil {
		return roomContentBinding{}, false
	}
	config := sha256.Sum256(raw)
	pipe := sha256.Sum256([]byte(primary.Pipe))
	rt.live.mu.Lock()
	rt.live.observeStopLocked()
	run, phase, retired := rt.live.runEpoch, rt.live.phaseEpoch, rt.live.sourceRetired
	rt.live.mu.Unlock()
	if retired {
		return roomContentBinding{}, false
	}
	return roomContentBinding{scope, rt, aliasRegistryHash(entries), hex.EncodeToString(pipe[:]), hex.EncodeToString(config[:]), primary.Librespot, primary.OwnTone, run, phase}, true
}

func currentRoomContentBinding(scope roomTransportScope) (roomContentBinding, bool) {
	if mgr == nil || store == nil {
		return roomContentBinding{}, false
	}
	rooms := loadRooms()
	var primary *Room
	for _, room := range rooms {
		if room != nil && room.Idx == 0 {
			if primary != nil {
				return roomContentBinding{}, false
			}
			primary = room
		}
	}
	if primary == nil {
		return roomContentBinding{}, false
	}
	return roomContentBindingFor(primary, mgr.runtime(primary.ID), rooms, scope)
}

var roomContentBindingNow = currentRoomContentBinding

func roomContentBindingCurrent(binding roomContentBinding) bool {
	current, ok := roomContentBindingNow(binding.Scope)
	return ok && current == binding
}
