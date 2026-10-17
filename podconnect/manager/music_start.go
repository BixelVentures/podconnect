package main

// A local, short-lived start barrier for one configured Spotify engine and one
// configured AirPlay output. The Spotify Web API caller owns its account and
// command replay fence; this owner proves only its native route and start level.

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

var engineIDPattern = regexp.MustCompile(`^[a-f0-9]{40}$`)

type startProof struct {
	Schema             string        `json:"schema"`
	EngineDeviceID     string        `json:"engine_device_id"`
	RoomID             string        `json:"room_id"`
	HomepodID          string        `json:"homepod_id"`
	PolicyRevision     string        `json:"policy_revision"`
	StartVolumePercent int           `json:"start_volume_percent"`
	AliasBinding       *aliasBinding `json:"alias_binding"`
	Mode               string        `json:"mode"`
	OutputVolume       int           `json:"output_volume_percent"`
	OutputSelected     bool          `json:"output_selected"`
	Ready              bool          `json:"ready"`
}

type startClaim struct {
	Proof    startProof
	Intent   glTransportIntent
	Room     *Room
	RT       *roomRuntime
	Released bool
	Paused   bool
}

type startLease struct {
	ID        string
	Claim     startClaim
	Expires   time.Time
	Committed bool
}

var startLeases struct {
	sync.Mutex
	Current *startLease
}

// The manager rendered this exact ID into the supervised primary engine's
// config. Absence, a changed config, or an unbound engine fails closed.
func configuredEngineID(primary *Room) string {
	if primary == nil || primary.Idx != 0 {
		return ""
	}
	raw, err := os.ReadFile(filepath.Join(primary.ConfigDir, "device_id"))
	if err != nil {
		return ""
	}
	id := string(bytes.TrimSpace(raw))
	if !engineIDPattern.MatchString(id) {
		return ""
	}
	cfg, err := os.ReadFile(filepath.Join(primary.ConfigDir, "config.yml"))
	if err != nil || !bytes.Contains(cfg, []byte(fmt.Sprintf("device_id: %q", id))) ||
		!bytes.Contains(cfg, []byte(fmt.Sprintf("initial_volume: %d", startVolumePercent))) {
		return ""
	}
	return id
}

// Native selected-output observation is one bounded OwnTone read. It neither
// falls back to a name nor chooses the first discovered device.
func knownStartOutput(base, exactID string) (int, bool, bool) {
	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Get(base + "/api/outputs")
	if err != nil {
		return 0, false, false
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return 0, false, false
	}
	var doc struct {
		Outputs []map[string]any `json:"outputs"`
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 65537))
	if err != nil || len(raw) > 65536 {
		return 0, false, false
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	if dec.Decode(&doc) != nil || len(doc.Outputs) > 128 {
		return 0, false, false
	}
	var trailing any
	if dec.Decode(&trailing) != io.EOF {
		return 0, false, false
	}
	count, volume, selected, otherSelected := 0, 0, false, false
	for _, row := range doc.Outputs {
		if fmt.Sprint(row["id"]) != exactID {
			if row["selected"] == true {
				otherSelected = true
			}
			continue
		}
		count++
		typ, _ := row["type"].(string)
		if !strings.HasPrefix(strings.ToLower(typ), "airplay") ||
			row["needs_auth_key"] == true || row["requires_auth"] == true {
			return 0, false, false
		}
		var n float64
		if value, ok := row["volume"].(json.Number); ok {
			n, err = value.Float64()
		} else {
			err = fmt.Errorf("volume absent")
		}
		if err != nil || math.IsNaN(n) || math.IsInf(n, 0) || n < 0 || n > 100 {
			return 0, false, false
		}
		// Round upward for the safety check: a reported 35.1 is above 35.
		volume = int(math.Ceil(n))
		selected = row["selected"] == true
	}
	return volume, selected, count == 1 && !(selected && otherSelected)
}

func selectedStartOutput(base, exactID string) (int, bool) {
	volume, selected, ok := knownStartOutput(base, exactID)
	return volume, selected && ok
}

func startRevision(id string, room *Room, binding *aliasBinding) string {
	if room == nil || binding == nil {
		return ""
	}
	sum := sha256.Sum256([]byte(fmt.Sprintf("start-v1\x00%d\x00%s\x00%s\x00%s\x00%s", startVolumePercent, id, room.ID, room.HomepodID, binding.Registry)))
	return hex.EncodeToString(sum[:])
}

func currentStartClaim(roomID, targetID string) (startClaim, bool) {
	return currentStartClaimAllowReleased(roomID, targetID, false)
}

// Only the existing room-transport owner may inspect a released, still-playing
// route. Its caller must join the current receiver and C owners before use.
func currentRoomTransportClaim(roomID, targetID string) (startClaim, bool) {
	return currentStartClaimWithRelease(roomID, targetID, true, true)
}

func currentStartClaimAllowReleased(roomID, targetID string, allowReleased bool) (startClaim, bool) {
	return currentStartClaimWithRelease(roomID, targetID, allowReleased, false)
}

func currentStartClaimWithRelease(roomID, targetID string, allowReleased, allowPlayingReleased bool) (startClaim, bool) {
	if mgr == nil || store == nil || roomID == "" || !engineIDPattern.MatchString(targetID) {
		return startClaim{}, false
	}
	rooms := loadRooms()
	var primary *Room
	for _, room := range rooms {
		if room.Idx == 0 {
			primary = room
		}
	}
	if primary == nil || configuredEngineID(primary) != targetID {
		return startClaim{}, false
	}
	rt := mgr.runtime(primary.ID)
	if rt == nil {
		return startClaim{}, false
	}
	return startClaimFor(primary, rt, rooms, roomID, targetID, allowReleased, allowPlayingReleased)
}

func startClaimFor(primary *Room, rt *roomRuntime, rooms []*Room, roomID, targetID string, allowReleased ...bool) (startClaim, bool) {
	if primary == nil || rt == nil || configuredEngineID(primary) != targetID {
		return startClaim{}, false
	}
	status, intent := rt.live.transportSnapshot()
	if intent.run == 0 || !rt.live.startIntentCurrent(intent) || status.AliasBinding == nil || !status.AliasBinding.valid() ||
		status.SelAlias < 1 {
		return startClaim{}, false
	}
	names := connectAliasesForRooms(rooms)
	entries, ok := aliasEntriesForRooms(rooms, names)
	if !ok || aliasRegistryHash(entries) != status.AliasBinding.Registry {
		return startClaim{}, false
	}
	var selected *Room
	currentRoomValid := false
	for _, entry := range entries {
		if entry.ID == uint32(status.SelAlias) && entry.RoomID == status.AliasBinding.RoomID {
			currentRoomValid = true
		}
		if entry.RoomID == roomID {
			for _, room := range rooms {
				if room.ID == roomID && room.HomepodID != "" {
					selected = room
				}
			}
		}
	}
	if selected == nil || !currentRoomValid || (status.Active && status.AliasBinding.RoomID != roomID) {
		return startClaim{}, false
	}
	released := fileExists(releasedPath(primary))
	volume, outputSelected, ok := knownStartOutput(primary.OwnTone, selected.HomepodID)
	if !ok {
		return startClaim{}, false
	}
	// A room edit or source event during the network read cannot mint a proof.
	check, next := rt.live.transportSnapshot()
	latest := roomByID(roomID)
	if next != intent || !rt.live.startIntentCurrent(intent) || !sameAliasBinding(check.AliasBinding, status.AliasBinding) ||
		latest == nil || latest.HomepodID != selected.HomepodID ||
		configuredEngineID(primary) != targetID || fileExists(releasedPath(primary)) != released {
		return startClaim{}, false
	}
	mode := "fresh_start"
	if status.Active {
		// Login/volume makes Active true even after the native route was
		// released. Only an explicitly paused, exact-current-alias route may
		// reclaim as a fresh URI start. Unselected alone is not permission.
		if released && status.Paused && status.AliasBinding.RoomID == roomID &&
			(len(allowReleased) < 2 || !allowReleased[1]) {
			mode = "fresh_start"
		} else {
			mode = "same_session"
		}
	}
	if status.Active && released && !status.Paused && (len(allowReleased) < 2 || !allowReleased[1]) {
		return startClaim{}, false
	}
	if mode == "same_session" && (!outputSelected || fileExists(releasedPath(primary))) &&
		(len(allowReleased) == 0 || !allowReleased[0]) {
		return startClaim{}, false
	}
	proof := startProof{Schema: "podconnect.start_volume.v1", EngineDeviceID: targetID,
		RoomID: roomID, HomepodID: selected.HomepodID, PolicyRevision: startRevision(targetID, selected, status.AliasBinding),
		StartVolumePercent: startVolumePercent, AliasBinding: copyAliasBinding(status.AliasBinding), Mode: mode,
		OutputVolume: volume, OutputSelected: outputSelected, Ready: true}
	return startClaim{Proof: proof, Intent: intent, Room: selected, RT: rt, Released: released, Paused: status.Paused}, true
}

// A local resume has no Spotify URI and preserves manual volume in an ordinary
// paused session. A released AirPlay route is a fresh output claim: prime that
// exact saved output before selecting it and before asking the engine to resume.
func preflightReleasedResume(primary *Room) bool {
	if primary == nil || primary.Idx != 0 || !fileExists(releasedPath(primary)) {
		return false
	}
	id := configuredEngineID(primary)
	if id == "" || mgr == nil {
		return false
	}
	rt := mgr.runtime(primary.ID)
	if rt == nil {
		return false
	}
	status, _ := rt.live.transportSnapshot()
	if !status.Active || !status.Paused || status.AliasBinding == nil {
		return false
	}
	roomID := status.AliasBinding.RoomID
	claim, ok := currentStartClaimAllowReleased(roomID, id, true)
	if !ok || claim.RT != rt || !fileExists(releasedPath(primary)) ||
		!primeConfiguredStartVolume(primary.OwnTone, claim.Proof.HomepodID, false) {
		return false
	}
	if !claim.Proof.OutputSelected && !selectOnOwntoneAt(primary.OwnTone, claim.Proof.HomepodID) {
		return false
	}
	if !checkedStartVolume(primary.OwnTone, claim.Proof.HomepodID) {
		return false
	}
	check, valid := currentStartClaimAllowReleased(roomID, id, true)
	if !valid || check.Intent != claim.Intent ||
		!sameAliasBinding(check.Proof.AliasBinding, claim.Proof.AliasBinding) ||
		check.Proof.PolicyRevision != claim.Proof.PolicyRevision ||
		!check.Proof.OutputSelected || check.Proof.OutputVolume > startVolumePercent {
		return false
	}
	// Keep the release marker until the bridge observes playing and finishes its
	// own protected reclaim. A rejected or unknown local resume must force the
	// next explicit call through this same preflight, never bypass it.
	final, valid := currentStartClaimAllowReleased(roomID, id, true)
	return valid && final.Intent == claim.Intent && final.Proof.OutputSelected &&
		final.Proof.PolicyRevision == claim.Proof.PolicyRevision &&
		final.Proof.OutputVolume <= startVolumePercent && final.Proof.OutputVolume >= startVolumePercent-volTol
}

func primeConfiguredStartVolume(base, id string, requireSelected bool) bool {
	client := &http.Client{Timeout: 3 * time.Second}
	query := url.Values{"volume": {fmt.Sprint(startVolumePercent)}, "output_id": {id}}
	req, err := http.NewRequest(http.MethodPut, base+"/api/player/volume?"+query.Encode(), nil)
	if err != nil {
		return false
	}
	resp, err := client.Do(req)
	if err != nil {
		return false
	}
	resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return false
	}
	vol, selected, ok := knownStartOutput(base, id)
	return ok && (!requireSelected || selected) && vol <= startVolumePercent && vol >= startVolumePercent-volTol
}

func checkedStartVolume(base, id string) bool {
	return primeConfiguredStartVolume(base, id, true)
}

// A non-current room may be chosen only while the engine is idle. The engine's
// own incarnation/registry validates the alias write; the manager waits for
// its live observation before any output prime or provider permission exists.
func selectIdleStartAlias(base, roomID string, binding *aliasBinding) bool {
	if binding == nil || !binding.valid() || roomID == "" {
		return false
	}
	body, _ := json.Marshal(aliasSelectionRequest{RoomID: roomID,
		ExpectedIncarnation: binding.Incarnation, ExpectedRegistry: binding.Registry})
	req, err := http.NewRequest(http.MethodPost, base+"/player/aliases", bytes.NewReader(body))
	if err != nil {
		return false
	}
	req.Header.Set("Content-Type", "application/json")
	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return false
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 16385))
	if err != nil || len(raw) > 16384 {
		return false
	}
	var reply struct {
		AcceptedLocal bool          `json:"accepted_local"`
		Binding       *aliasBinding `json:"binding"`
	}
	return json.Unmarshal(raw, &reply) == nil && reply.AcceptedLocal && reply.Binding != nil && reply.Binding.valid() &&
		reply.Binding.RoomID == roomID && reply.Binding.Registry == binding.Registry && reply.Binding.Incarnation == binding.Incarnation
}

func startRequest(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		roomID, targetID := r.URL.Query().Get("room_id"), r.URL.Query().Get("target_id")
		claim, ok := currentStartClaim(roomID, targetID)
		if !ok {
			w.WriteHeader(http.StatusConflict)
			return
		}
		writeJSON(w, claim.Proof)
		return
	}
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	var body struct {
		RoomID           string `json:"room_id"`
		TargetID         string `json:"target_id"`
		PolicyRevision   string `json:"policy_revision"`
		AliasIncarnation string `json:"alias_incarnation"`
	}
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if decoder.Decode(&body) != nil || body.RoomID == "" || body.TargetID == "" || body.PolicyRevision == "" || body.AliasIncarnation == "" {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	var trailing any
	if decoder.Decode(&trailing) != io.EOF {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	claim, ok := currentStartClaim(body.RoomID, body.TargetID)
	if !ok || claim.Proof.PolicyRevision != body.PolicyRevision || claim.Proof.AliasBinding.Incarnation != body.AliasIncarnation {
		w.WriteHeader(http.StatusConflict)
		return
	}
	if claim.Proof.Mode == "fresh_start" {
		if claim.Proof.AliasBinding.RoomID != body.RoomID {
			// Prime the future output while the old alias is idle, then recheck
			// source ownership before the one local alias write.
			latest, valid := currentStartClaim(body.RoomID, body.TargetID)
			if !valid || latest.Proof.Mode != "fresh_start" || latest.Intent != claim.Intent || latest.Released != claim.Released ||
				latest.Proof.PolicyRevision != body.PolicyRevision || !sameAliasBinding(latest.Proof.AliasBinding, claim.Proof.AliasBinding) ||
				!primeConfiguredStartVolume(claim.RT.room.OwnTone, claim.Proof.HomepodID, false) {
				w.WriteHeader(http.StatusConflict)
				return
			}
			latest, valid = currentStartClaim(body.RoomID, body.TargetID)
			if !valid || latest.Proof.Mode != "fresh_start" || latest.Intent != claim.Intent || latest.Released != claim.Released ||
				latest.Proof.PolicyRevision != body.PolicyRevision || !sameAliasBinding(latest.Proof.AliasBinding, claim.Proof.AliasBinding) ||
				!selectIdleStartAlias(claim.RT.room.Librespot, body.RoomID, claim.Proof.AliasBinding) {
				w.WriteHeader(http.StatusConflict)
				return
			}
			deadline := time.Now().Add(2 * time.Second)
			for time.Now().Before(deadline) {
				claim, valid = currentStartClaim(body.RoomID, body.TargetID)
				if valid && claim.Proof.AliasBinding.RoomID == body.RoomID && claim.Proof.Mode == "fresh_start" &&
					claim.Proof.PolicyRevision == body.PolicyRevision && claim.Proof.AliasBinding.Incarnation == body.AliasIncarnation {
					break
				}
				time.Sleep(50 * time.Millisecond)
			}
			if claim.Proof.AliasBinding == nil || claim.Proof.AliasBinding.RoomID != body.RoomID || claim.Proof.Mode != "fresh_start" {
				w.WriteHeader(http.StatusConflict)
				return
			}
		}
		if !claim.Proof.OutputSelected {
			// A saved output may have been left loud by another sender. Seat
			// its own start level before selection can trigger pipe autostart.
			if !primeConfiguredStartVolume(claim.RT.room.OwnTone, claim.Proof.HomepodID, false) {
				w.WriteHeader(http.StatusConflict)
				return
			}
			latest, valid := currentStartClaim(body.RoomID, body.TargetID)
			if !valid || latest.Intent != claim.Intent || latest.Released != claim.Released ||
				latest.Proof.Mode != claim.Proof.Mode || latest.Proof.PolicyRevision != claim.Proof.PolicyRevision ||
				!sameAliasBinding(latest.Proof.AliasBinding, claim.Proof.AliasBinding) {
				w.WriteHeader(http.StatusConflict)
				return
			}
			if !selectOnOwntoneAt(claim.RT.room.OwnTone, claim.Proof.HomepodID) {
				w.WriteHeader(http.StatusConflict)
				return
			}
			if _, ok := selectedStartOutput(claim.RT.room.OwnTone, claim.Proof.HomepodID); !ok {
				w.WriteHeader(http.StatusConflict)
				return
			}
		}
		if !checkedStartVolume(claim.RT.room.OwnTone, claim.Proof.HomepodID) {
			w.WriteHeader(http.StatusConflict)
			return
		}
		// Keep a release marker through prepare/commit. The existing bridge
		// clears it only after observing playing and completing protected
		// reclaim; rejected or unknown provider sends never bypass reclaim.
		before := claim
		claim, ok = currentStartClaim(body.RoomID, body.TargetID)
		if !ok || claim.Intent != before.Intent || claim.Released != before.Released ||
			!sameAliasBinding(claim.Proof.AliasBinding, before.Proof.AliasBinding) ||
			claim.Proof.Mode != "fresh_start" || !claim.Proof.OutputSelected || claim.Proof.PolicyRevision != body.PolicyRevision ||
			claim.Proof.AliasBinding.Incarnation != body.AliasIncarnation ||
			claim.Proof.OutputVolume > startVolumePercent || claim.Proof.OutputVolume < startVolumePercent-volTol {
			w.WriteHeader(http.StatusConflict)
			return
		}
	}
	var nonce [16]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		return
	}
	lease := &startLease{ID: hex.EncodeToString(nonce[:]), Claim: claim, Expires: time.Now().Add(5 * time.Second)}
	startLeases.Lock()
	startLeases.Current = lease // replacement invalidates any prior offer
	startLeases.Unlock()
	writeJSON(w, map[string]any{"schema": "podconnect.start_lease.v1", "lease_id": lease.ID, "expires_in_ms": 5000, "proof": claim.Proof})
}

// The generic HA media-player paths need to distinguish our supervised engine
// from another Spotify device without guessing from a name or a room label.
func startIdentityRequest(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	rooms := loadRooms()
	for _, room := range rooms {
		if room.Idx != 0 {
			continue
		}
		id := configuredEngineID(room)
		if id == "" || mgr == nil || mgr.runtime(room.ID) == nil {
			break
		}
		rt := mgr.runtime(room.ID)
		status, intent := rt.live.transportSnapshot()
		if intent.run != 0 && rt.live.startIntentCurrent(intent) &&
			status.AliasBinding != nil && status.AliasBinding.valid() {
			reply := map[string]any{"schema": "podconnect.start_identity.v1", "engine_device_id": id}
			// Explicit diagnostic opt-in keeps existing start-path reads unchanged.
			if r.URL.Query().Get("account") == "1" {
				w.Header().Set("Cache-Control", "no-store")
				account := rt.live.observeAccountIdentity(rt.room.Librespot, id)
				// A local read cannot carry an obsolete engine/route into the response.
				if configuredEngineID(room) != id || mgr.runtime(room.ID) != rt || !rt.live.startIntentCurrent(intent) {
					w.WriteHeader(http.StatusConflict)
					return
				}
				reply["account_observation"] = account
			}
			writeJSON(w, reply)
			return
		}
		break
	}
	w.WriteHeader(http.StatusConflict)
}

func commitStartRequest(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1024)
	var body struct {
		LeaseID   string `json:"lease_id"`
		MaxVolume *int   `json:"max_volume,omitempty"`
	}
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if decoder.Decode(&body) != nil || len(body.LeaseID) != 32 ||
		(body.MaxVolume != nil && (*body.MaxVolume < 0 || *body.MaxVolume > 100)) {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	var trailing any
	if decoder.Decode(&trailing) != io.EOF {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	startLeases.Lock()
	lease := startLeases.Current
	if lease == nil || lease.ID != body.LeaseID || lease.Committed || !time.Now().Before(lease.Expires) {
		startLeases.Unlock()
		w.WriteHeader(http.StatusConflict)
		return
	}
	lease.Committed = true // one use, including unknown/rejected downstream results
	startLeases.Unlock()
	claim, ok := currentStartClaim(lease.Claim.Proof.RoomID, lease.Claim.Proof.EngineDeviceID)
	if !ok || !claim.Proof.OutputSelected || claim.Intent != lease.Claim.Intent || claim.Released != lease.Claim.Released || claim.Proof.PolicyRevision != lease.Claim.Proof.PolicyRevision ||
		!sameAliasBinding(claim.Proof.AliasBinding, lease.Claim.Proof.AliasBinding) || claim.Proof.Mode != lease.Claim.Proof.Mode ||
		(claim.Proof.Mode == "fresh_start" &&
			(claim.Proof.OutputVolume > startVolumePercent || claim.Proof.OutputVolume < startVolumePercent-volTol)) ||
		(body.MaxVolume != nil && claim.Proof.OutputVolume > *body.MaxVolume) {
		w.WriteHeader(http.StatusConflict)
		return
	}
	startLeases.Lock()
	stillCurrent := startLeases.Current == lease && time.Now().Before(lease.Expires)
	startLeases.Unlock()
	if !stillCurrent {
		w.WriteHeader(http.StatusConflict)
		return
	}
	writeJSON(w, map[string]any{"ok": true, "schema": "podconnect.start_commit.v1", "policy_revision": claim.Proof.PolicyRevision})
}
