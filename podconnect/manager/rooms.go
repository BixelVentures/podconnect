// Multi-room model: N HomePods, each its own (go-librespot + OwnTone) pair. The set of rooms is
// persisted to /data/rooms.json; everything else (ports, URLs, on-disk paths) is DERIVED from a
// room's idx so the file stays small and the layout is deterministic.
//
// Back-compat is load-bearing: room 0 (the migrated single-speaker setup) keeps the LEGACY paths and
// ports so the user's existing Spotify credentials/identity/library keep working untouched. New rooms
// (idx >= 1) get fresh paths under /data/rooms/<id> and the 37xx/38xx/39xx port ranges.
package main

import (
	"crypto/sha1"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// Legacy ports for room 0 — the existing single-room setup. HA's watchdog (config.yaml
// `watchdog: tcp://[HOST]:3689`) watches room-0 OwnTone, so these must not move.
const (
	legacyGLPort = 3678
	legacyOTPort = 3689
	legacyOTWS   = 3688
)

// Port ranges for new rooms (idx >= 1). Chosen to avoid the old +10*idx collision.
const (
	glPortBase = 3700 // go-librespot server.port = glPortBase + idx
	otPortBase = 3800 // OwnTone API/DAAP port  = otPortBase + idx
	otWSBase   = 3900 // OwnTone websocket port = otWSBase + idx
)

// Room is one (go-librespot + OwnTone) pair — a Spotify-Connect speaker bound to one HomePod.
// Only the persisted fields are stored in rooms.json; the URLs/ports/paths are rebuilt from Idx
// (and the room-0 legacy exception) on load via fill().
type Room struct {
	ID          string `json:"id"`
	Idx         int    `json:"idx"`
	Name        string `json:"name"`
	HomepodName string `json:"homepod_name"`
	HomepodID   string `json:"homepod_id,omitempty"`  // OwnTone output id — the stable binding; heals name changes.
	NameManual  bool   `json:"name_manual,omitempty"` // user explicitly renamed this room: don't auto-overwrite Name.
	DeviceID    string `json:"device_id,omitempty"`
	Released    bool   `json:"released"`

	// Per-room overrides of the global add-on defaults (UX-1b). nil/"" = inherit the global option.
	GraceMinutes *int   `json:"grace_minutes,omitempty"` // grace-release minutes; nil = inherit readGraceMinutes().
	Bitrate      string `json:"bitrate,omitempty"`       // go-librespot bitrate "96"/"160"/"320"; "" = inherit readBitrate().

	// Derived (not persisted): set by fill().
	ConfigDir string `json:"-"` // go-librespot --config_dir
	OwnConf   string `json:"-"` // OwnTone -c path
	OwnDir    string `json:"-"` // OwnTone db/cache/log dir
	Pipe      string `json:"-"` // audio_output_pipe
	Librespot string `json:"-"` // go-librespot base URL
	OwnTone   string `json:"-"` // OwnTone base URL
	GLPort    int    `json:"-"`
	OTPort    int    `json:"-"`
	OTWSPort  int    `json:"-"`
}

// roomsFile is the on-disk shape of /data/rooms.json.
type roomsFile struct {
	NextIdx int     `json:"next_idx"`
	Rooms   []*Room `json:"rooms"`
}

// allocPorts returns (go-librespot, OwnTone API, OwnTone ws) ports for an idx, honoring the room-0
// legacy exception. Pure -> unit-tested.
func allocPorts(idx int) (gl, ot, ws int) {
	if idx == 0 {
		return legacyGLPort, legacyOTPort, legacyOTWS
	}
	return glPortBase + idx, otPortBase + idx, otWSBase + idx
}

// fill derives all the non-persisted fields of a Room from its Idx + ID. Room 0 (idx 0) keeps the
// LEGACY on-disk paths so the existing creds/library are reused; new rooms use the per-room tree.
func (r *Room) fill() {
	r.GLPort, r.OTPort, r.OTWSPort = allocPorts(r.Idx)
	r.Librespot = fmt.Sprintf("http://localhost:%d", r.GLPort)
	r.OwnTone = fmt.Sprintf("http://localhost:%d", r.OTPort)
	if r.Idx == 0 {
		// Legacy paths — same creds, device_id, library, pipe the single-room setup used.
		r.ConfigDir = filepath.Join(dataDir, "go-librespot")
		r.OwnDir = filepath.Join(dataDir, "owntone")
		r.OwnConf = filepath.Join(dataDir, "rooms", r.ID, "owntone.conf")
		r.Pipe = "/srv/media/spotify"
		return
	}
	base := filepath.Join(dataDir, "rooms", r.ID)
	r.ConfigDir = filepath.Join(base, "go-librespot")
	r.OwnDir = filepath.Join(base, "owntone")
	r.OwnConf = filepath.Join(base, "owntone.conf")
	r.Pipe = filepath.Join("/srv/media/rooms", r.ID, "spotify")
}

// roomStore owns rooms.json, guarded by a mutex. All reads/writes go through it.
type roomStore struct {
	mu   sync.Mutex
	path string
}

func newRoomStore() *roomStore { return &roomStore{path: filepath.Join(dataDir, "rooms.json")} }

// loadLocked reads + parses rooms.json (caller holds mu). Returns a zero-value file if absent.
func (s *roomStore) loadLocked() (*roomsFile, error) {
	b, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return &roomsFile{}, nil
		}
		return nil, err
	}
	var rf roomsFile
	if err := json.Unmarshal(b, &rf); err != nil {
		return nil, err
	}
	for _, r := range rf.Rooms {
		r.fill()
	}
	return &rf, nil
}

// saveLocked writes rooms.json atomically (caller holds mu).
func (s *roomStore) saveLocked(rf *roomsFile) error {
	b, err := json.MarshalIndent(rf, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

// load returns the current rooms (filled). Triggers first-boot migration if rooms.json is absent.
func (s *roomStore) load() ([]*Room, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rf, err := s.loadLocked()
	if err != nil {
		return nil, err
	}
	if len(rf.Rooms) == 0 {
		if mig := s.migrateLocked(); mig != nil {
			rf = mig
		}
	}
	return rf.Rooms, nil
}

// migrateLocked builds rooms.json from the existing single-room setup if it doesn't exist yet,
// preserving the effective speaker name + picked HomePod so room 0 keeps working with the same
// Spotify identity. Returns the new roomsFile (also persisted), or nil if persistence failed.
func (s *roomStore) migrateLocked() *roomsFile {
	name := readSpeaker()
	homepod := readSaved()
	if homepod == "" {
		homepod = readHomepodName()
	}
	if name == "" {
		name = homepod
	}
	if name == "" {
		name = "PodConnect"
	}
	r0 := &Room{ID: "r0", Idx: 0, Name: name, HomepodName: homepod, Released: fileExists(legacyReleasedPath())}
	r0.fill()
	rf := &roomsFile{NextIdx: 1, Rooms: []*Room{r0}}
	if err := s.saveLocked(rf); err != nil {
		log.Printf("rooms: migration save failed: %v", err)
		return nil
	}
	log.Printf("rooms: migrated single-room setup -> r0 (name=%q homepod=%q)", name, homepod)
	return rf
}

// addRoom validates uniqueness, allocates a monotonic idx, persists, and returns the new Room.
// It does NOT spawn anything — the caller (supervisor) renders + starts it. homepodID (the live
// OwnTone output id) is the stable binding; it may be "" if the caller only has the name.
func (s *roomStore) addRoom(name, homepod, homepodID string) (*Room, error) {
	name = strings.TrimSpace(name)
	homepod = strings.TrimSpace(homepod)
	if homepod == "" {
		return nil, fmt.Errorf("homepod_name is required")
	}
	if name == "" {
		name = homepod
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	rf, err := s.loadLocked()
	if err != nil {
		return nil, err
	}
	for _, r := range rf.Rooms {
		if strings.EqualFold(r.Name, name) {
			return nil, fmt.Errorf("a speaker named %q already exists", name)
		}
		if strings.EqualFold(r.HomepodName, homepod) {
			return nil, fmt.Errorf("HomePod %q is already used by speaker %q", homepod, r.Name)
		}
	}
	idx := rf.NextIdx
	if idx < 1 {
		idx = 1
	}
	r := &Room{ID: fmt.Sprintf("r%d", idx), Idx: idx, Name: name, HomepodName: homepod, HomepodID: strings.TrimSpace(homepodID)}
	r.fill()
	rf.Rooms = append(rf.Rooms, r)
	rf.NextIdx = idx + 1
	if err := s.saveLocked(rf); err != nil {
		return nil, err
	}
	log.Printf("rooms: added %s (name=%q homepod=%q idx=%d)", r.ID, name, homepod, idx)
	return r, nil
}

// removeRoom drops a room from rooms.json (idx is NOT reused — next_idx stays monotonic). Returns the
// removed Room so the caller can tear down its children. id "r0" is rejected (can't remove the legacy
// migrated room — it owns the watchdog port).
func (s *roomStore) removeRoom(id string) (*Room, error) {
	if id == "r0" {
		return nil, fmt.Errorf("cannot remove the primary speaker (r0)")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	rf, err := s.loadLocked()
	if err != nil {
		return nil, err
	}
	var removed *Room
	kept := rf.Rooms[:0]
	for _, r := range rf.Rooms {
		if r.ID == id {
			removed = r
			continue
		}
		kept = append(kept, r)
	}
	if removed == nil {
		return nil, fmt.Errorf("no such room: %s", id)
	}
	rf.Rooms = kept
	if err := s.saveLocked(rf); err != nil {
		return nil, err
	}
	log.Printf("rooms: removed %s (name=%q)", removed.ID, removed.Name)
	return removed, nil
}

// setReleased persists a room's released flag (best-effort; the on-disk per-room flag is the source
// of truth for the bridge, this keeps rooms.json in sync for /api/rooms).
func (s *roomStore) setReleased(id string, released bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rf, err := s.loadLocked()
	if err != nil {
		return
	}
	for _, r := range rf.Rooms {
		if r.ID == id {
			if r.Released == released {
				return
			}
			r.Released = released
			_ = s.saveLocked(rf)
			return
		}
	}
}

// setHomePodBinding persists a room's chosen HomePod as an id+name pair (the panel's pick / add).
// The id is the stable binding matchOutput (the alias router) matches on first; the name is the
// human label + fallback match. Either may be ""; both are stored as given (trimmed).
func (s *roomStore) setHomePodBinding(id, homepodID, homepodName string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rf, err := s.loadLocked()
	if err != nil {
		return
	}
	for _, r := range rf.Rooms {
		if r.ID == id {
			r.HomepodID = strings.TrimSpace(homepodID)
			r.HomepodName = strings.TrimSpace(homepodName)
			_ = s.saveLocked(rf)
			return
		}
	}
}

// setName persists a room's display name (used by r0 auto-naming after the picked HomePod). Does NOT
// set NameManual — this is the automatic path.
func (s *roomStore) setName(id, name string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rf, err := s.loadLocked()
	if err != nil {
		return
	}
	for _, r := range rf.Rooms {
		if r.ID == id {
			r.Name = strings.TrimSpace(name)
			_ = s.saveLocked(rf)
			return
		}
	}
}

// setNameManual persists a user-chosen display name AND flips NameManual so a future rename-heal
// (if re-added) won't auto-overwrite it when the bound HomePod is renamed in Apple Home.
func (s *roomStore) setNameManual(id, name string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rf, err := s.loadLocked()
	if err != nil {
		return
	}
	for _, r := range rf.Rooms {
		if r.ID == id {
			r.Name = strings.TrimSpace(name)
			r.NameManual = true
			_ = s.saveLocked(rf)
			return
		}
	}
}

// setRoomSettings persists a room's per-room overrides of the global grace/bitrate defaults (UX-1b).
// Only the provided fields are touched: a nil grace clears that override (back to inherit), a non-nil
// grace must be in [0,120]; an empty bitrate is ignored (no change) and a non-empty one must be one
// of {96,160,320}. Returns whether the BITRATE changed (the caller re-renders + restarts go-librespot
// only then — grace is read live by the bridge and needs no restart).
func (s *roomStore) setRoomSettings(id string, grace *int, bitrate string) (bitrateChanged bool, err error) {
	if grace != nil && (*grace < 0 || *grace > 120) {
		return false, fmt.Errorf("grace_minutes must be 0..120")
	}
	bitrate = strings.TrimSpace(bitrate)
	if bitrate != "" && bitrate != "96" && bitrate != "160" && bitrate != "320" {
		return false, fmt.Errorf("bitrate must be one of 96, 160, 320")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	rf, lerr := s.loadLocked()
	if lerr != nil {
		return false, lerr
	}
	for _, r := range rf.Rooms {
		if r.ID != id {
			continue
		}
		// grace is always applied (nil = clear the override back to inherit).
		r.GraceMinutes = grace
		if bitrate != "" && r.Bitrate != bitrate {
			r.Bitrate = bitrate
			bitrateChanged = true
		}
		if serr := s.saveLocked(rf); serr != nil {
			return false, serr
		}
		return bitrateChanged, nil
	}
	return false, fmt.Errorf("no such room: %s", id)
}

// roomGrace is the room's effective grace-release minutes: its per-room override if set, else the
// global readGraceMinutes() default. Pure given the Room (the global fallback reads options.json).
func roomGrace(r *Room) int {
	if r.GraceMinutes != nil {
		return *r.GraceMinutes
	}
	return readGraceMinutes()
}

// roomBitrate is the room's effective go-librespot bitrate: its per-room override if set, else the
// global readBitrate() default.
func roomBitrate(r *Room) string {
	if r.Bitrate != "" {
		return r.Bitrate
	}
	return readBitrate()
}

// roomDeviceID returns the stable Spotify device_id for a room, seeding it once from
// sha1("podconnect-"+name) and persisting it under the room's config dir. Same logic the old
// init-podconnect used, now per room — so renaming never spawns a ghost Connect device.
func roomDeviceID(r *Room) string {
	idFile := filepath.Join(r.ConfigDir, "device_id")
	if b, err := os.ReadFile(idFile); err == nil && len(strings.TrimSpace(string(b))) > 0 {
		return strings.TrimSpace(string(b))
	}
	sum := sha1.Sum([]byte("podconnect-" + r.Name))
	id := fmt.Sprintf("%x", sum)[:40]
	_ = os.MkdirAll(r.ConfigDir, 0o755)
	_ = os.WriteFile(idFile, []byte(id), 0o644)
	return id
}
