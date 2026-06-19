// PodConnect manager — a tiny stdlib HTTP service that powers the HA Ingress panel AND forks +
// supervises each room's (go-librespot + OwnTone) child processes.
//
// Multi-room: N HomePods, each its own (go-librespot + OwnTone) pair, added live from the panel with
// no add-on restart. The set of rooms is persisted to /data/rooms.json (room 0 migrated from the
// legacy single-room setup, keeping its creds/identity/library). The manager renders each room's
// configs, spawns + supervises its children (replacing the s6 services go-librespot/owntone/
// gl-watchdog/select-homepod), runs the volume/transport bridge, and serves the picker UI.
//
// No third-party deps on purpose. Keep it boring and dependency-free.
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	stdlog "log"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// log is the package-wide logger (aliased so the other files can call log.Printf without each
// importing "log"). Same behavior as the stdlib default logger.
var log = stdlog.Default()

var (
	dataDir = envOr("DATA_DIR", "/data")
	port    = envOr("PORT", "8099")
)

func envOr(k, d string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return d
}

type device struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Selected  bool   `json:"selected"`
	NeedsAuth bool   `json:"needs_auth"`
}

type stateResp struct {
	Speaker    string   `json:"speaker"`
	Saved      string   `json:"saved"`
	OwntoneUp  bool     `json:"owntone_up"`
	Devices    []device `json:"devices"`
	Playing    bool     `json:"playing"`
	Released   bool     `json:"released"`
	NowPlaying string   `json:"now_playing"`
	Volume     int      `json:"volume"` // 0..100, or -1 if unknown
}

func selectionPath() string { return filepath.Join(dataDir, "selected_output.json") }

func readSaved() string {
	b, err := os.ReadFile(selectionPath())
	if err != nil {
		return ""
	}
	var s struct {
		Name string `json:"name"`
	}
	_ = json.Unmarshal(b, &s)
	return s.Name
}

func writeSaved(name string) error {
	b, _ := json.Marshal(map[string]string{"name": name})
	return os.WriteFile(selectionPath(), b, 0o644)
}

// readSpeaker returns the speaker's effective display name: the explicit speaker_name option, or —
// in auto-name mode (option empty) — the picked HomePod's name. This is what the Connect device and
// the HA entity end up called.
func readSpeaker() string {
	if s := speakerNameOpt(); s != "" {
		return s
	}
	return readSaved()
}

// speakerNameOpt is the raw speaker_name option ("" => auto-name mode).
func speakerNameOpt() string {
	b, err := os.ReadFile(filepath.Join(dataDir, "options.json"))
	if err != nil {
		return ""
	}
	var o struct {
		SpeakerName string `json:"speaker_name"`
	}
	_ = json.Unmarshal(b, &o)
	return strings.TrimSpace(o.SpeakerName)
}

const defaultGraceMinutes = 3 // grace-release default if the option is unset

// readGraceMinutes is the configurable grace-release period (minutes) before an idle HomePod is
// freed for other apps. Defaults to 3; 0 = release as soon as playback stops.
func readGraceMinutes() int {
	b, err := os.ReadFile(filepath.Join(dataDir, "options.json"))
	if err != nil {
		return defaultGraceMinutes
	}
	var o struct {
		GraceMinutes *json.Number `json:"grace_minutes"`
	}
	if json.Unmarshal(b, &o) != nil || o.GraceMinutes == nil {
		return defaultGraceMinutes
	}
	n, err := o.GraceMinutes.Int64()
	if err != nil || n < 0 {
		return defaultGraceMinutes
	}
	return int(n)
}

func readHomepodName() string {
	b, err := os.ReadFile(filepath.Join(dataDir, "options.json"))
	if err != nil {
		return ""
	}
	var o struct {
		HomepodName string `json:"homepod_name"`
	}
	_ = json.Unmarshal(b, &o)
	return o.HomepodName
}

// fetchOutputsFrom returns the AirPlay outputs a given OwnTone instance has discovered, and whether
// it answered. Parsed defensively (map + UseNumber) so a field OwnTone serializes as a number rather
// than a string — notably the 64-bit output "id" — can't make a strict decoder drop every device.
func fetchOutputsFrom(base string) ([]device, bool) {
	cl := &http.Client{Timeout: 4 * time.Second}
	resp, err := cl.Get(base + "/api/outputs")
	if err != nil {
		return nil, false
	}
	defer resp.Body.Close()
	dec := json.NewDecoder(resp.Body)
	dec.UseNumber()
	var raw struct {
		Outputs []map[string]any `json:"outputs"`
	}
	if err := dec.Decode(&raw); err != nil {
		log.Printf("outputs decode error: %v", err)
		return nil, true
	}
	out := []device{}
	for _, o := range raw.Outputs {
		typ, _ := o["type"].(string)
		// OwnTone reports HomePods as "AirPlay 2" (not "AirPlay"), so match the prefix — an exact
		// "airplay" compare silently dropped every device and left the picker empty.
		if !strings.HasPrefix(strings.ToLower(typ), "airplay") {
			continue
		}
		name, _ := o["name"].(string)
		out = append(out, device{
			ID:        fmt.Sprint(o["id"]), // works whether id is a JSON string or number
			Name:      name,
			Selected:  asBool(o["selected"]),
			NeedsAuth: asBool(o["needs_auth_key"]) || asBool(o["requires_auth"]),
		})
	}
	return out, true
}

func asBool(v any) bool { b, _ := v.(bool); return b }

// selectOnOwntoneAt activates exactly one output on a given OwnTone (the proven call select-homepod
// uses).
func selectOnOwntoneAt(base, id string) {
	cl := &http.Client{Timeout: 4 * time.Second}
	req, _ := http.NewRequest(http.MethodPut, base+"/api/outputs/set", bytes.NewBufferString(`{"outputs":["`+id+`"]}`))
	if resp, err := cl.Do(req); err == nil {
		resp.Body.Close()
	}
}

func clampPct(p int) int {
	if p < 0 {
		return 0
	}
	if p > 100 {
		return 100
	}
	return p
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

// owntoneOutputVolume returns the volume (0-100) and id of the room's active HomePod — the first
// SELECTED AirPlay output. ok=false when none is selected. Reading/writing the specific output
// (not OwnTone's master) is what makes the sync deterministic on multi-output setups.
func owntoneOutputVolume(base string) (vol int, id string, ok bool) {
	cl := &http.Client{Timeout: 3 * time.Second}
	resp, err := cl.Get(base + "/api/outputs")
	if err != nil {
		return 0, "", false
	}
	defer resp.Body.Close()
	dec := json.NewDecoder(resp.Body)
	dec.UseNumber()
	var raw struct {
		Outputs []map[string]any `json:"outputs"`
	}
	if err := dec.Decode(&raw); err != nil {
		return 0, "", false
	}
	for _, o := range raw.Outputs {
		typ, _ := o["type"].(string)
		if !strings.HasPrefix(strings.ToLower(typ), "airplay") {
			continue
		}
		if sel, _ := o["selected"].(bool); !sel {
			continue
		}
		v := 0
		if n, ok2 := o["volume"].(json.Number); ok2 {
			f, _ := n.Float64()
			v = int(f)
		}
		return clampPct(v), fmt.Sprint(o["id"]), true
	}
	return 0, "", false
}

// setOwntoneOutputVolume sets one specific HomePod output's volume (0-100).
func setOwntoneOutputVolume(base, id string, pct int) {
	cl := &http.Client{Timeout: 3 * time.Second}
	req, _ := http.NewRequest(http.MethodPut,
		fmt.Sprintf("%s/api/player/volume?volume=%d&output_id=%s", base, clampPct(pct), id), nil)
	if resp, err := cl.Do(req); err == nil {
		resp.Body.Close()
	}
}

// glStatus is the slice of go-librespot's /status the bridge needs.
type glStatus struct {
	Active  bool // a Spotify session is present (status returned data)
	HasVol  bool
	VolPct  int // 0-100
	Paused  bool
	Stopped bool
}

// librespotStatus reads go-librespot's /status once (volume + transport). With external_volume:true
// go-librespot reports the volume (0..volume_steps) instead of scaling the PCM. Active=false when
// there's no session (idle /status is empty).
func librespotStatus(base string) glStatus {
	cl := &http.Client{Timeout: 3 * time.Second}
	resp, err := cl.Get(base + "/status")
	if err != nil {
		return glStatus{}
	}
	defer resp.Body.Close()
	dec := json.NewDecoder(resp.Body)
	dec.UseNumber()
	var st struct {
		Username    string       `json:"username"`
		Volume      *json.Number `json:"volume"`
		VolumeSteps *json.Number `json:"volume_steps"`
		Paused      bool         `json:"paused"`
		Stopped     bool         `json:"stopped"`
	}
	if err := dec.Decode(&st); err != nil {
		return glStatus{} // empty body / no session
	}
	out := glStatus{Active: st.Username != "" || st.Volume != nil, Paused: st.Paused, Stopped: st.Stopped}
	if st.Volume != nil && st.VolumeSteps != nil {
		v, _ := st.Volume.Float64()
		m, _ := st.VolumeSteps.Float64()
		if m > 0 {
			out.VolPct, out.HasVol = clampPct(int(math.Round(v/m*100))), true
		}
	}
	return out
}

// setLibrespotVolumePct best-effort sets go-librespot's volume from a percent (it reports
// volume_steps as the max). Used by the test so the gentle level holds even if a session is active
// and the syncer would otherwise mirror a louder level.
func setLibrespotVolumePct(base string, pct int) {
	cl := &http.Client{Timeout: 3 * time.Second}
	resp, err := cl.Get(base + "/status")
	if err != nil {
		return
	}
	dec := json.NewDecoder(resp.Body)
	dec.UseNumber()
	var st struct {
		VolumeSteps json.Number `json:"volume_steps"`
	}
	_ = dec.Decode(&st)
	resp.Body.Close()
	max, _ := st.VolumeSteps.Float64()
	if max <= 0 {
		return
	}
	raw := int(math.Round(float64(clampPct(pct)) / 100 * max))
	req, _ := http.NewRequest(http.MethodPost, base+"/player/volume", bytes.NewBufferString(fmt.Sprintf(`{"volume":%d}`, raw)))
	req.Header.Set("Content-Type", "application/json")
	if r, e := cl.Do(req); e == nil {
		r.Body.Close()
	}
}

// owntonePlayerState returns OwnTone's player state ("play"/"pause"/"stop"), or "" on error.
func owntonePlayerState(base string) string {
	cl := &http.Client{Timeout: 3 * time.Second}
	resp, err := cl.Get(base + "/api/player")
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	var p struct {
		State string `json:"state"`
	}
	if json.NewDecoder(resp.Body).Decode(&p) != nil {
		return ""
	}
	return p.State
}

// owntoneTransport issues a player transport command ("play" or "pause").
func owntoneTransport(base, action string) {
	cl := &http.Client{Timeout: 3 * time.Second}
	req, _ := http.NewRequest(http.MethodPut, base+"/api/player/"+action, nil)
	if r, e := cl.Do(req); e == nil {
		r.Body.Close()
	}
}

// librespotTransport issues a go-librespot transport command ("pause" or "resume").
func librespotTransport(base, action string) {
	cl := &http.Client{Timeout: 3 * time.Second}
	req, _ := http.NewRequest(http.MethodPost, base+"/player/"+action, nil)
	if r, e := cl.Do(req); e == nil {
		r.Body.Close()
	}
}

func glConfigPath(r *Room) string { return filepath.Join(r.ConfigDir, "config.yml") }

// setGLDeviceName rewrites the device_name line in a room's go-librespot config.yml. Returns true if
// it actually changed (so the caller only restarts when needed). The device_id is persisted
// separately, so renaming never spawns a ghost Connect device.
func setGLDeviceName(r *Room, name string) bool {
	p := glConfigPath(r)
	b, err := os.ReadFile(p)
	if err != nil {
		return false
	}
	want := `device_name: "` + name + `"`
	lines := strings.Split(string(b), "\n")
	for i, ln := range lines {
		if strings.HasPrefix(strings.TrimSpace(ln), "device_name:") {
			if strings.TrimSpace(ln) == want {
				return false
			}
			lines[i] = want
			return os.WriteFile(p, []byte(strings.Join(lines, "\n")), 0o644) == nil
		}
	}
	return false
}

// nowPlaying returns a best-effort "Artist — Track" from go-librespot's /status ("" if idle or the
// field shape differs — it's only a status-line nicety).
func nowPlaying(base string) string {
	cl := &http.Client{Timeout: 2 * time.Second}
	resp, err := cl.Get(base + "/status")
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	var st struct {
		Track struct {
			Name        string   `json:"name"`
			ArtistNames []string `json:"artist_names"`
		} `json:"track"`
	}
	if json.NewDecoder(resp.Body).Decode(&st) != nil || st.Track.Name == "" {
		return ""
	}
	if len(st.Track.ArtistNames) > 0 && st.Track.ArtistNames[0] != "" {
		return st.Track.ArtistNames[0] + " — " + st.Track.Name
	}
	return st.Track.Name
}

const volTol = 2 // ±%, absorbs 0-100 <-> 0-volume_steps rounding so our own writes aren't seen as input

// decideVolume is the pure bidirectional volume reconcile (no I/O -> unit-tested). One canonical
// percent: whichever side drifted from canon beyond the tolerance becomes the new canon (Spotify
// wins ties), then canon is pushed to any side that still differs. Comparing to canon with a
// tolerance is what prevents echo/ping-pong between the two writes.
func decideVolume(canon, gl int, glOk bool, ot int, otOk bool) (newCanon int, setGl, setOt bool) {
	if !glOk && !otOk {
		return canon, false, false
	}
	if canon < 0 {
		if glOk {
			canon = gl
		} else {
			canon = ot
		}
	} else {
		glCh := glOk && abs(gl-canon) > volTol
		otCh := otOk && abs(ot-canon) > volTol
		switch {
		case glCh: // Spotify wins ties
			canon = gl
		case otCh: // HomePod button moved it
			canon = ot
		}
	}
	setGl = glOk && abs(gl-canon) > volTol
	setOt = otOk && abs(ot-canon) > volTol
	return canon, setGl, setOt
}

// decideTransport is the pure bidirectional play/pause reconcile. canon: -1 unknown, 0 paused,
// 1 playing. Same canonical loop-protection as decideVolume — a HomePod top-tap (otPlaying change)
// propagates to Spotify and vice-versa, without ping-pong. (For a binary value at most one side can
// differ from canon at a time, so this is naturally conflict-free.)
func b(p bool) int {
	if p {
		return 1
	}
	return 0
}

// transState tracks play/pause sync with awareness of OwnTone's startup lag. OwnTone reaches "play"
// ~1-2s after a command (buffering), so its state can't be trusted for the back-direction until it
// has CONFIRMED our last command — otherwise the lag reads as a HomePod pause (the flicker), and a
// blanket time delay would also stall rapid toggling. So: forward (Spotify->OwnTone) always runs;
// back (HomePod->Spotify) only fires once OwnTone has confirmed our command, so a genuine top-tap (a
// divergence AFTER confirmation) is caught but startup lag is not. Rapid play/pause works because the
// forward path is never delayed.
type transState struct {
	canon       int  // -1 unknown, 0 paused, 1 playing
	otTarget    int  // -1 none, else the last state we commanded OwnTone
	otConfirmed bool // has OwnTone reached otTarget since we set it
}

// decide consumes one tick's readings and returns the state to command each side (-1 = no command).
func (ts *transState) decide(glPlaying, otPlaying, otValid bool) (setGl, setOt int) {
	gp, op := b(glPlaying), b(otPlaying)
	if ts.canon < 0 {
		ts.canon = gp
	}
	if otValid && ts.otTarget >= 0 && op == ts.otTarget {
		ts.otConfirmed = true
	}
	switch {
	case gp != ts.canon: // Spotify changed -> wins
		ts.canon = gp
	case otValid && ts.otConfirmed && op != ts.canon: // genuine HomePod tap (post-confirmation divergence)
		ts.canon = op
	}
	setGl, setOt = -1, -1
	if gp != ts.canon {
		setGl = ts.canon
	}
	if otValid && op != ts.canon {
		setOt = ts.canon
		if ts.otTarget != ts.canon {
			ts.otTarget, ts.otConfirmed = ts.canon, false
		}
	}
	return
}

const initialVolumeCap = 35 // "never full blast": cap the FIRST session's volume after a (re)start

func playWord(c int) string {
	if c == 1 {
		return "play"
	}
	return "pause"
}

// legacyReleasedPath is the single-room flag the migration reads to seed room 0's Released field.
func legacyReleasedPath() string { return filepath.Join(dataDir, "released") }

// releasedPath is a room's per-room release flag at /data/rooms/<id>/released. The bridge honors it
// so a freed HomePod isn't re-grabbed until Spotify resumes.
func releasedPath(r *Room) string { return filepath.Join(dataDir, "rooms", r.ID, "released") }

func fileExists(p string) bool { _, err := os.Stat(p); return err == nil }

// releaseHomePod frees the HomePod for other AirPlay senders (Mofibo, Apple Music, …): deselect all
// of the room's OwnTone outputs and drop the room's flag so selection won't immediately re-grab it.
func releaseHomePod(room *Room) {
	cl := &http.Client{Timeout: 3 * time.Second}
	req, _ := http.NewRequest(http.MethodPut, room.OwnTone+"/api/outputs/set", bytes.NewBufferString(`{"outputs":[]}`))
	if r, e := cl.Do(req); e == nil {
		r.Body.Close()
	}
	_ = os.MkdirAll(filepath.Dir(releasedPath(room)), 0o755)
	_ = os.WriteFile(releasedPath(room), []byte("1"), 0o644)
}

// reclaimHomePod takes the HomePod back: clear the room's flag and re-select its target output now,
// for a snappy resume instead of waiting on the selection tick.
func reclaimHomePod(room *Room) {
	_ = os.Remove(releasedPath(room))
	devs, _ := fetchOutputsFrom(room.OwnTone)
	target := ""
	if idx, _ := matchOutput(devs, room.HomepodID, room.HomepodName); idx >= 0 {
		target = devs[idx].ID
	} else if len(devs) > 0 {
		target = devs[0].ID
	}
	if target != "" {
		selectOnOwntoneAt(room.OwnTone, target)
	}
}

// roomBridge keeps one room's HomePod and go-librespot in step in BOTH directions, for volume and
// transport — so Spotify/HA, the HomePod buttons and the HomePod top-tap all converge on one state.
//
//   - Volume (decideVolume): Spotify/HA slider <-> HomePod hardware buttons. external_volume:true
//     makes OwnTone apply volume at the AirPlay output (responsive). OwnTone 29.2 was verified live
//     to surface the receiver's volume on this hardware, so the back-direction works here.
//   - Transport (transState): a Spotify pause pauses the HomePod instantly (beating the buffer),
//     and a HomePod top-tap pauses/resumes Spotify (OwnTone forwards HomePod MediaRemote events —
//     best-effort, firmware-dependent). Startup-aware so it never flickers or stalls rapid taps.
//
// Loop-protected via canonical values. initialVolumeCap stops a session Spotify remembers at 100%
// from starting at full blast (once per manager start, so it doesn't fight reconnects). Skipped
// while THIS room's test tone plays. Per-room. Uses only verified APIs.
func roomBridge(room *Room, tone *boolFlag) {
	volCanon := -1
	trans := transState{canon: -1, otTarget: -1}
	capped := false
	grace := time.Duration(readGraceMinutes()) * time.Minute // options change restarts the add-on
	var idleSince time.Time
	for {
		if !tone.Load() {
			gl := librespotStatus(room.Librespot)
			playing := gl.Active && !gl.Paused && !gl.Stopped
			released := fileExists(releasedPath(room))

			// Grace-release ("deling"): hold the HomePod through brief interruptions (still
			// playing, so Siri/notifications recover), but free it after sustained idle so other
			// apps can use it — and reclaim it the moment Spotify resumes.
			if playing {
				idleSince = time.Time{}
				if released {
					reclaimHomePod(room)
					log.Printf("[%s]: reclaimed HomePod (playback resumed)", room.Name)
					released = false
				}
			} else {
				if idleSince.IsZero() {
					idleSince = time.Now()
				}
				if !released && time.Since(idleSince) > grace {
					releaseHomePod(room)
					log.Printf("[%s]: released HomePod after %v idle — free for other apps", room.Name, grace)
					released = true
				}
			}

			if gl.Active && !released {
				if !capped {
					capped = true
					if gl.HasVol && gl.VolPct > initialVolumeCap {
						setLibrespotVolumePct(room.Librespot, initialVolumeCap)
						gl.VolPct = initialVolumeCap
						log.Printf("volume [%s]: capped fresh start to %d%%", room.Name, initialVolumeCap)
					}
				}
				otVol, otID, otVolOk := owntoneOutputVolume(room.OwnTone)
				otState := owntonePlayerState(room.OwnTone)

				// Volume — both directions.
				vc, vGl, vOt := decideVolume(volCanon, gl.VolPct, gl.HasVol, otVol, otVolOk)
				volCanon = vc
				if vGl {
					setLibrespotVolumePct(room.Librespot, vc)
					log.Printf("volume [%s]: -> Spotify %d%%", room.Name, vc)
				}
				if vOt && otID != "" {
					setOwntoneOutputVolume(room.OwnTone, otID, vc)
					log.Printf("volume [%s]: -> HomePod %d%%", room.Name, vc)
				}

				// Transport — both directions, OwnTone-startup-aware (no flicker, handles rapid taps).
				sg, so := trans.decide(!gl.Paused && !gl.Stopped, otState == "play", otState != "")
				if sg >= 0 {
					if sg == 1 {
						librespotTransport(room.Librespot, "resume")
					} else {
						librespotTransport(room.Librespot, "pause")
					}
					log.Printf("transport [%s]: -> Spotify %s", room.Name, playWord(sg))
				}
				if so >= 0 {
					owntoneTransport(room.OwnTone, playWord(so))
					log.Printf("transport [%s]: -> HomePod %s", room.Name, playWord(so))
				}
			}
		}
		time.Sleep(200 * time.Millisecond)
	}
}

var toneMu sync.Mutex // serializes tone writes across rooms (one writer at a time is plenty)

// playTestTone writes ~2.5s of a soft sine (s16le/44100/stereo) into the room's pipe. OwnTone's
// pipe_autostart picks it up and plays it to the selected output — proving the OwnTone→AirPlay→
// HomePod leg with zero dependency on Spotify or go-librespot. Runs in a goroutine (the write
// drains at real time). tone gates THIS room's bridge so it doesn't cut the tone off; the lock
// prevents overlapping tones.
func playTestTone(pipePath string, tone *boolFlag) {
	if !toneMu.TryLock() {
		return
	}
	defer toneMu.Unlock()
	tone.Store(true)
	defer tone.Store(false)
	f, err := os.OpenFile(pipePath, os.O_WRONLY, 0)
	if err != nil {
		log.Printf("test tone: open pipe: %v", err)
		return
	}
	defer f.Close()
	// A soft, calming ~330 Hz sine with a gentle fade-in and a long fade-out (no startling
	// clicks). Deliberately quiet — meant to be just audible in a silent house at night.
	const rate = 44100
	const n = rate * 5 / 2 // 2.5 s
	const freq = 330.0
	const peak = 0.12 * 32767.0
	const attack = n * 15 / 100 // fade in over the first 15%
	const release = n / 2       // fade out over the last 50%
	buf := make([]byte, 0, n*4)
	for i := 0; i < n; i++ {
		env := 1.0
		if i < attack {
			env = float64(i) / float64(attack)
		} else if i > n-release {
			env = float64(n-i) / float64(release)
		}
		u := uint16(int16(peak * env * math.Sin(2*math.Pi*freq*float64(i)/float64(rate))))
		lo, hi := byte(u), byte(u>>8)
		buf = append(buf, lo, hi, lo, hi) // little-endian s16, L+R
	}
	if _, err := f.Write(buf); err != nil {
		log.Printf("test tone: write: %v", err)
	}
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

// roomInfo is the per-room status row returned by /api/rooms (and embedded in the panel).
type roomInfo struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	HomepodName string `json:"homepod_name"`
	OwntoneUp   bool   `json:"owntone_up"`
	Playing     bool   `json:"playing"`
	Released    bool   `json:"released"`
	NowPlaying  string `json:"now_playing"`
	Volume      int    `json:"volume"` // 0..100, or -1 if unknown
}

// mgr is the global room supervisor + store. main() builds it; the HTTP handlers read through it.
var (
	store *roomStore
	mgr   *roomManager
)

// loadRooms is the single entry to the current room set (migrates on first boot).
func loadRooms() []*Room {
	rs, err := store.load()
	if err != nil {
		log.Printf("rooms: load failed: %v", err)
		return nil
	}
	return rs
}

// roomByID returns the current room with the given id, or nil.
func roomByID(id string) *Room {
	for _, r := range loadRooms() {
		if r.ID == id {
			return r
		}
	}
	return nil
}

// primaryRoom is room r0 (back-compat target for the legacy single-room panel calls + /api/state).
func primaryRoom() *Room {
	rs := loadRooms()
	for _, r := range rs {
		if r.ID == "r0" {
			return r
		}
	}
	if len(rs) > 0 {
		return rs[0]
	}
	return nil
}

func toneFor(id string) *boolFlag {
	if rt := mgr.runtime(id); rt != nil {
		return &rt.tonePlaying
	}
	return &boolFlag{} // detached gate if the room isn't supervised (tests / race on add)
}

func main() {
	store = newRoomStore()
	mgr = newRoomManager(store)

	// Build rooms (migrating the single-room setup on first boot), then start each one's children +
	// bridge. The supervise loop owns the children; reconcile == just (re)spawn everything we know of.
	for _, rm := range loadRooms() {
		mgr.ensureRunning(rm)
		go roomBridge(rm, toneFor(rm.ID))
	}

	lastCount := -1

	// /api/state — back-compat single-speaker view reflecting room 0, so the existing panel/HA calls
	// keep working unchanged. Multi-room consumers should use /api/rooms.
	http.HandleFunc("/api/state", func(w http.ResponseWriter, r *http.Request) {
		rm := primaryRoom()
		var (
			devs []device
			up   bool
		)
		if rm != nil {
			devs, up = fetchOutputsFrom(rm.OwnTone)
		}
		if devs == nil {
			devs = []device{}
		}
		if up && len(devs) != lastCount {
			log.Printf("AirPlay devices visible to picker: %d", len(devs))
			lastCount = len(devs)
		}
		resp := stateResp{OwntoneUp: up, Devices: devs, Volume: -1}
		if rm != nil {
			gl := librespotStatus(rm.Librespot)
			resp.Speaker = rm.Name
			resp.Saved = rm.HomepodName
			resp.Playing = gl.Active && !gl.Paused && !gl.Stopped
			resp.Released = fileExists(releasedPath(rm))
			resp.NowPlaying = nowPlaying(rm.Librespot)
			if gl.HasVol {
				resp.Volume = gl.VolPct
			}
		}
		writeJSON(w, resp)
	})

	// /api/rooms — GET full multi-room status (one row per speaker); POST adds a speaker.
	http.HandleFunc("/api/rooms", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			roomsItemHandler(w, r)
			return
		}
		out := []roomInfo{}
		for _, rm := range loadRooms() {
			_, up := fetchOutputsFrom(rm.OwnTone)
			gl := librespotStatus(rm.Librespot)
			info := roomInfo{
				ID: rm.ID, Name: rm.Name, HomepodName: rm.HomepodName,
				OwntoneUp:  up,
				Playing:    gl.Active && !gl.Paused && !gl.Stopped,
				Released:   fileExists(releasedPath(rm)),
				NowPlaying: nowPlaying(rm.Librespot),
				Volume:     -1,
			}
			if gl.HasVol {
				info.Volume = gl.VolPct
			}
			out = append(out, info)
		}
		writeJSON(w, map[string]any{"rooms": out})
	})

	// /api/discover — live AirPlay scan (from any running OwnTone) minus HomePods already claimed by
	// a room, so the "Add speaker" picker only offers free HomePods.
	http.HandleFunc("/api/discover", func(w http.ResponseWriter, r *http.Request) {
		claimed := map[string]bool{}
		var scanBase string
		for _, rm := range loadRooms() {
			if rm.HomepodName != "" {
				claimed[strings.ToLower(rm.HomepodName)] = true
			}
			if scanBase == "" {
				scanBase = rm.OwnTone // any running OwnTone sees the whole LAN's AirPlay devices
			}
		}
		devs, up := []device{}, false
		if scanBase != "" {
			devs, up = fetchOutputsFrom(scanBase)
		}
		free := []device{}
		for _, d := range devs {
			if !claimed[strings.ToLower(d.Name)] {
				free = append(free, d)
			}
		}
		writeJSON(w, map[string]any{"owntone_up": up, "devices": free})
	})

	// POST /api/rooms {homepod_name, name?} — add a speaker: validate uniqueness, allocate, render,
	// spawn, select. DELETE /api/rooms/<id> — remove a speaker.
	http.HandleFunc("/api/rooms/", roomsItemHandler)

	http.HandleFunc("/api/select", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		var body struct {
			Name string `json:"name"`
			Room string `json:"room"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		name := strings.TrimSpace(body.Name)
		rm := primaryRoom()
		if body.Room != "" {
			rm = roomByID(body.Room)
		}
		if rm == nil {
			http.Error(w, "no such room", http.StatusNotFound)
			return
		}
		// Capture the chosen output's live id (the stable binding) alongside its name, so a later
		// Apple-Home rename can't break the match. Apply immediately for instant feedback; the
		// selection tick also keeps it locked + heals drift.
		homepodID := ""
		if name != "" {
			if devs, _ := fetchOutputsFrom(rm.OwnTone); devs != nil {
				for _, d := range devs {
					if strings.EqualFold(d.Name, name) {
						homepodID = d.ID
						selectOnOwntoneAt(rm.OwnTone, d.ID)
						break
					}
				}
			}
		}
		// Persist the chosen HomePod (id+name) on the room + keep legacy selected_output.json for r0.
		store.setHomePodBinding(rm.ID, homepodID, name)
		if rm.ID == "r0" {
			_ = writeSaved(name)
		}
		rm.HomepodName = name
		rm.HomepodID = homepodID
		// Auto-name forwarding (r0 only, where speaker_name may be empty): the Connect device adopts
		// the picked HomePod's name. Rewrite device_name + restart THAT room's go-librespot.
		if name != "" && rm.ID == "r0" && speakerNameOpt() == "" && setGLDeviceName(rm, name) {
			store.setName(rm.ID, name)
			log.Printf("name-forward: Connect device -> %q (restarting go-librespot)", name)
			if rt := mgr.runtime(rm.ID); rt != nil {
				go rt.restartGL()
			}
		}
		log.Printf("selection saved: room=%s %q", rm.ID, name)
		writeJSON(w, map[string]bool{"ok": true})
	})

	http.HandleFunc("/api/test", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		var body struct {
			Room string `json:"room"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		rm := primaryRoom()
		if body.Room != "" {
			rm = roomByID(body.Room)
		}
		if rm == nil {
			http.Error(w, "no such room", http.StatusNotFound)
			return
		}
		devs, _ := fetchOutputsFrom(rm.OwnTone)
		// Resolve the target: the room's HomePod, then the first discovered device — and report the
		// NAME back so the user can see exactly which HomePod the tone went to.
		want := rm.HomepodName
		target, targetName := "", ""
		for _, d := range devs {
			if want != "" && strings.EqualFold(d.Name, want) {
				target, targetName = d.ID, d.Name
				break
			}
		}
		if target == "" && len(devs) > 0 {
			target, targetName = devs[0].ID, devs[0].Name // fall back to the first discovered device
		}
		if target != "" {
			selectOnOwntoneAt(rm.OwnTone, target)
			setOwntoneOutputVolume(rm.OwnTone, target, 13) // gentle, on the specific HomePod
		}
		// Also nudge go-librespot down so the reconciler can't bump the test up to a louder level.
		setLibrespotVolumePct(rm.Librespot, 13)
		go playTestTone(rm.Pipe, toneFor(rm.ID))
		log.Printf("test tone requested (room=%s target=%q id=%q)", rm.ID, targetName, target)
		writeJSON(w, map[string]any{"ok": true, "playing": target != "", "target": targetName})
	})

	http.HandleFunc("/api/release", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		for _, rm := range targetRooms(r) {
			librespotTransport(rm.Librespot, "pause") // stop Spotify so the bridge doesn't instantly reclaim
			releaseHomePod(rm)
			store.setReleased(rm.ID, true)
		}
		log.Printf("HomePod released on request — free for other apps")
		writeJSON(w, map[string]bool{"ok": true})
	})

	// /api/stop pauses whatever is playing on a speaker WITHOUT giving the HomePod away. It talks to
	// go-librespot LOCALLY, so it stops playback regardless of which Spotify account owns the session
	// (e.g. a family member's) — the account-agnostic "stop the music here" the Web API can't do
	// across accounts. The bridge's transState then mirrors the pause onto OwnTone.
	http.HandleFunc("/api/stop", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		for _, rm := range targetRooms(r) {
			librespotTransport(rm.Librespot, "pause") // local pause — stops any account's session
			owntoneTransport(rm.OwnTone, "pause")     // silence the AirPlay leg immediately
		}
		log.Printf("stop requested — paused playback (account-agnostic)")
		writeJSON(w, map[string]bool{"ok": true})
	})

	// /api/play resumes playback (go-librespot resume). The bridge's transState then mirrors the
	// resume onto OwnTone. Counterpart to /api/stop for the HA integration.
	http.HandleFunc("/api/play", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		for _, rm := range targetRooms(r) {
			librespotTransport(rm.Librespot, "resume")
		}
		log.Printf("play requested — resumed playback")
		writeJSON(w, map[string]bool{"ok": true})
	})

	// /api/volume sets the speaker volume (0..100) via go-librespot; the bridge mirrors the change to
	// the HomePod's OwnTone output. PUT with {"volume":<int>, "room"?:"<id>"}.
	http.HandleFunc("/api/volume", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		var body struct {
			Volume int    `json:"volume"`
			Room   string `json:"room"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		pct := clampPct(body.Volume)
		rooms := loadRooms()
		if body.Room != "" {
			if rm := roomByID(body.Room); rm != nil {
				rooms = []*Room{rm}
			}
		}
		for _, rm := range rooms {
			setLibrespotVolumePct(rm.Librespot, pct)
		}
		log.Printf("volume set to %d%% (via API)", pct)
		writeJSON(w, map[string]bool{"ok": true})
	})

	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = io.WriteString(w, indexHTML)
	})

	log.Printf("podconnect-manager listening on :%s", port)
	log.Fatal(http.ListenAndServe(":"+port, nil))
}

// targetRooms resolves which rooms a transport/release request applies to: a "room" query param
// selects one; otherwise all rooms (preserves the legacy "all speakers" behavior).
func targetRooms(r *http.Request) []*Room {
	if id := r.URL.Query().Get("room"); id != "" {
		if rm := roomByID(id); rm != nil {
			return []*Room{rm}
		}
		return nil
	}
	return loadRooms()
}

// roomsItemHandler serves POST /api/rooms (add) and DELETE /api/rooms/<id> (remove). It's registered
// on "/api/rooms/" but POST /api/rooms (no trailing slash) is routed here too via the explicit check.
func roomsItemHandler(w http.ResponseWriter, r *http.Request) {
	// POST /api/rooms/<id>/rename is the per-room rename override (distinct from POST /api/rooms add).
	if strings.HasSuffix(strings.TrimRight(r.URL.Path, "/"), "/rename") {
		renameRoomHandler(w, r)
		return
	}
	switch r.Method {
	case http.MethodPost:
		var body struct {
			HomepodName string `json:"homepod_name"`
			HomepodID   string `json:"homepod_id"`
			Name        string `json:"name"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		// Capture the chosen HomePod's live OwnTone id (the stable binding) so an Apple-Home rename
		// can't later break the match. Resolve from any running OwnTone if the client didn't send it.
		homepodID := strings.TrimSpace(body.HomepodID)
		if homepodID == "" && strings.TrimSpace(body.HomepodName) != "" {
			for _, rm := range loadRooms() {
				if devs, _ := fetchOutputsFrom(rm.OwnTone); devs != nil {
					for _, d := range devs {
						if strings.EqualFold(d.Name, body.HomepodName) {
							homepodID = d.ID
							break
						}
					}
				}
				if homepodID != "" {
					break
				}
			}
		}
		rm, err := store.addRoom(body.Name, body.HomepodName, homepodID)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		mgr.ensureRunning(rm)
		go roomBridge(rm, toneFor(rm.ID))
		writeJSON(w, map[string]any{"ok": true, "id": rm.ID, "name": rm.Name})
	case http.MethodDelete:
		id := strings.TrimPrefix(r.URL.Path, "/api/rooms/")
		id = strings.Trim(id, "/")
		if id == "" {
			http.Error(w, "missing room id", http.StatusBadRequest)
			return
		}
		if err := mgr.removeRoom(id); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		writeJSON(w, map[string]bool{"ok": true})
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// renameRoomHandler serves POST /api/rooms/<id>/rename {name}. It records a user override (NameManual)
// so the self-heal loop won't auto-rename the room back, then re-renders + restarts that room's
// go-librespot so the Connect device + HA entity adopt the new name.
func renameRoomHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	rest := strings.TrimPrefix(r.URL.Path, "/api/rooms/")
	id := strings.TrimSuffix(strings.Trim(rest, "/"), "/rename")
	id = strings.Trim(id, "/")
	if id == "" {
		http.Error(w, "missing room id", http.StatusBadRequest)
		return
	}
	var body struct {
		Name string `json:"name"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	name := strings.TrimSpace(body.Name)
	if name == "" {
		http.Error(w, "name is required", http.StatusBadRequest)
		return
	}
	rm := roomByID(id)
	if rm == nil {
		http.Error(w, "no such room", http.StatusNotFound)
		return
	}
	store.setNameManual(id, name)
	rm.Name = name
	if setGLDeviceName(rm, name) {
		log.Printf("rooms[%s]: renamed to %q (restarting go-librespot)", id, name)
		if rt := mgr.runtime(id); rt != nil {
			go rt.restartGL()
		}
	}
	writeJSON(w, map[string]any{"ok": true, "id": id, "name": name})
}

const indexHTML = `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>PodConnect</title>
<style>
  :root { color-scheme: light dark; }
  * { box-sizing: border-box; }
  body { font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, sans-serif;
         margin: 0; padding: 24px; color: #1c1c1e; background: transparent; }
  @media (prefers-color-scheme: dark) { body { color: #f2f2f7; } }
  .wrap { max-width: 560px; margin: 0 auto; }
  h1 { font-size: 1.35rem; margin: 0 0 4px; }
  .sub { margin: 0 0 16px; opacity: .65; font-size: .9rem; }
  .status { padding: 10px 14px; border-radius: 10px; font-size: .9rem; margin-bottom: 14px; }
  .status.ok { background: rgba(48,209,88,.15); }
  .status.warn { background: rgba(255,159,10,.15); }
  .list { display: flex; flex-direction: column; gap: 8px; }
  .row { display: flex; align-items: center; gap: 12px; padding: 14px 16px; border-radius: 12px;
         border: 1px solid rgba(120,120,128,.25); cursor: pointer; transition: border-color .15s; }
  .row:hover { border-color: rgba(10,132,255,.6); }
  .row.active { border-color: rgba(48,209,88,.7); }
  .row input { width: 18px; height: 18px; accent-color: #0a84ff; }
  .name { flex: 1; font-weight: 600; }
  .badge { font-size: .72rem; padding: 3px 8px; border-radius: 999px; background: rgba(255,159,10,.2); }
  .badge.live { background: rgba(48,209,88,.22); }
  .actions { display: flex; gap: 10px; margin-top: 18px; }
  button { font: inherit; font-weight: 600; padding: 11px 18px; border-radius: 10px; border: 0;
           cursor: pointer; background: #0a84ff; color: #fff; }
  button:disabled { opacity: .4; cursor: default; }
  button.ghost { background: transparent; color: inherit; border: 1px solid rgba(120,120,128,.4); }
  .hint { margin-top: 18px; font-size: .82rem; opacity: .6; line-height: 1.4; }
  .section { margin-top: 28px; }
  .section h2 { font-size: 1.05rem; margin: 0 0 10px; }
  .room { padding: 14px 16px; border-radius: 12px; border: 1px solid rgba(120,120,128,.25); margin-bottom: 10px; }
  .room .top { display: flex; align-items: center; gap: 10px; }
  .room .rname { flex: 1; font-weight: 700; }
  .room .meta { margin-top: 6px; font-size: .82rem; opacity: .7; }
  .room .ctl { display: flex; gap: 8px; margin-top: 10px; flex-wrap: wrap; }
  .room button { padding: 8px 12px; font-size: .82rem; }
  button.danger { background: transparent; color: #ff453a; border: 1px solid rgba(255,69,58,.5); }
  .err { color: #ff453a; font-size: .82rem; margin-top: 8px; }
</style>
</head>
<body>
<div class="wrap">
  <h1>PodConnect speakers</h1>
  <p class="sub" id="speaker"></p>

  <div class="section">
    <h2>Your speakers</h2>
    <div id="rooms" class="list"></div>
    <div class="actions">
      <button id="addbtn">＋ Add speaker</button>
    </div>
    <div id="addpanel" style="display:none">
      <div id="addstatus" class="status warn">Scanning for free HomePods…</div>
      <div id="addlist" class="list"></div>
      <div class="actions">
        <button id="addsave" disabled>Add this HomePod</button>
        <button id="addcancel" class="ghost">Cancel</button>
      </div>
      <p id="adderr" class="err"></p>
    </div>
  </div>

  <div class="section">
    <h2>Primary speaker — pick its HomePod</h2>
    <div id="status" class="status warn">Loading…</div>
    <div id="playstate" class="status" style="display:none"></div>
    <div id="list" class="list"></div>
    <div class="actions">
      <button id="save" disabled>Save selection</button>
      <button id="auto" class="ghost">Auto (clear choice)</button>
    </div>
    <div class="actions">
      <button id="test" class="ghost">🔊 Play test sound on HomePod</button>
      <button id="stop" class="ghost">⏹ Stop music (any account)</button>
      <button id="release" class="ghost">⏏ Release HomePod (for other apps)</button>
    </div>
    <p id="testmsg" class="hint"></p>
    <p id="stopmsg" class="hint"></p>
    <p id="releasemsg" class="hint"></p>
  </div>

  <p class="hint">These lists are a live network scan (OwnTone AirPlay discovery) — the same one that
  feeds Spotify Connect. No typing: pick a device and Save. Each speaker keeps playing to its HomePod
  across restarts. <b>Add speaker</b> spins up a new Spotify Connect speaker bound to a free HomePod,
  live — no restart.<br><br><b>Play test sound</b> sends a soft tone straight to the HomePod via
  AirPlay — no Spotify needed. Hear it = the HomePod audio path works.</p>
</div>
<script>
var chosen = null;
var addChosen = null;
var addChosenId = null;

// --- Multi-room list (/api/rooms) ---
async function loadRooms() {
  var data;
  try { data = await (await fetch('api/rooms', {cache:'no-store'})).json(); }
  catch (e) { return; }
  var wrap = document.getElementById('rooms');
  wrap.innerHTML = '';
  (data.rooms || []).forEach(function (rm) {
    var box = document.createElement('div'); box.className = 'room';
    var top = document.createElement('div'); top.className = 'top';
    var nm = document.createElement('span'); nm.className = 'rname'; nm.textContent = rm.name;
    top.appendChild(nm);
    var badge = document.createElement('span'); badge.className = 'badge';
    if (rm.released) { badge.textContent = 'released'; }
    else if (rm.playing) { badge.className = 'badge live'; badge.textContent = 'playing'; }
    else if (rm.owntone_up) { badge.textContent = 'idle'; }
    else { badge.textContent = 'starting…'; }
    top.appendChild(badge);
    box.appendChild(top);
    var meta = document.createElement('div'); meta.className = 'meta';
    meta.textContent = 'HomePod: ' + (rm.homepod_name || '(auto)') + (rm.now_playing ? ('  •  ' + rm.now_playing) : '');
    box.appendChild(meta);
    var ctl = document.createElement('div'); ctl.className = 'ctl';
    var t = document.createElement('button'); t.className = 'ghost'; t.textContent = '🔊 Test';
    t.onclick = function () { fetch('api/test', { method:'POST', headers:{'Content-Type':'application/json'}, body: JSON.stringify({ room: rm.id }) }); };
    var st = document.createElement('button'); st.className = 'ghost'; st.textContent = '⏹ Stop';
    st.onclick = function () { fetch('api/stop?room=' + encodeURIComponent(rm.id), { method:'POST' }); };
    var ren = document.createElement('button'); ren.className = 'ghost'; ren.textContent = '✎ Rename';
    ren.onclick = async function () {
      var nv = prompt('Rename this speaker (the Spotify Connect device + HA entity follow):', rm.name);
      if (nv === null) return;
      nv = nv.trim();
      if (!nv || nv === rm.name) return;
      try {
        var r = await fetch('api/rooms/' + encodeURIComponent(rm.id) + '/rename', { method:'POST', headers:{'Content-Type':'application/json'}, body: JSON.stringify({ name: nv }) });
        if (!r.ok) { alert(await r.text()); return; }
      } catch (e) { alert('Could not rename the speaker.'); return; }
      await loadRooms();
    };
    ctl.appendChild(t); ctl.appendChild(st); ctl.appendChild(ren);
    if (rm.id !== 'r0') {
      var rmv = document.createElement('button'); rmv.className = 'danger'; rmv.textContent = '🗑 Remove';
      rmv.onclick = async function () {
        if (!confirm('Remove speaker "' + rm.name + '"? Its HomePod is freed for other apps.')) return;
        await fetch('api/rooms/' + encodeURIComponent(rm.id), { method:'DELETE' });
        await loadRooms();
      };
      ctl.appendChild(rmv);
    }
    box.appendChild(ctl);
    wrap.appendChild(box);
  });
}

// --- Add-speaker flow (/api/discover + POST /api/rooms) ---
async function loadDiscover() {
  var data;
  try { data = await (await fetch('api/discover', {cache:'no-store'})).json(); }
  catch (e) { return; }
  var st = document.getElementById('addstatus');
  var list = document.getElementById('addlist');
  list.innerHTML = '';
  if (!data.owntone_up) { st.textContent = 'Audio engine starting…'; st.className = 'status warn'; return; }
  if (!data.devices.length) { st.textContent = 'No free HomePods found (all claimed, or still scanning).'; st.className = 'status warn'; return; }
  st.textContent = data.devices.length + ' free HomePod(s) — pick one'; st.className = 'status ok';
  data.devices.forEach(function (d) {
    var row = document.createElement('label'); row.className = 'row';
    var rb = document.createElement('input'); rb.type = 'radio'; rb.name = 'addhp'; rb.value = d.name;
    rb.onchange = function () { addChosen = d.name; addChosenId = d.id; document.getElementById('addsave').disabled = false; };
    var nm = document.createElement('span'); nm.className = 'name'; nm.textContent = d.name;
    row.appendChild(rb); row.appendChild(nm);
    if (d.needs_auth) { var b = document.createElement('span'); b.className = 'badge'; b.textContent = 'needs verification'; row.appendChild(b); }
    list.appendChild(row);
  });
}
document.getElementById('addbtn').onclick = function () {
  document.getElementById('addpanel').style.display = '';
  this.disabled = true;
  loadDiscover();
};
document.getElementById('addcancel').onclick = function () {
  document.getElementById('addpanel').style.display = 'none';
  document.getElementById('addbtn').disabled = false;
  document.getElementById('adderr').textContent = '';
  addChosen = null; addChosenId = null;
};
document.getElementById('addsave').onclick = async function () {
  if (addChosen === null) return;
  this.disabled = true;
  document.getElementById('adderr').textContent = '';
  try {
    var r = await fetch('api/rooms', { method:'POST', headers:{'Content-Type':'application/json'}, body: JSON.stringify({ homepod_name: addChosen, homepod_id: addChosenId || '' }) });
    if (!r.ok) { document.getElementById('adderr').textContent = await r.text(); this.disabled = false; return; }
  } catch (e) { document.getElementById('adderr').textContent = 'Could not add the speaker.'; this.disabled = false; return; }
  addChosen = null; addChosenId = null;
  document.getElementById('addpanel').style.display = 'none';
  document.getElementById('addbtn').disabled = false;
  await loadRooms();
};

// --- Primary speaker (room 0) picker — back-compat /api/state ---
async function load() {
  var s;
  try { s = await (await fetch('api/state', {cache:'no-store'})).json(); }
  catch (e) { return; }
  document.getElementById('speaker').textContent = s.speaker ? ('Primary speaker: ' + s.speaker) : '';
  var ps = document.getElementById('playstate');
  if (s.released) {
    ps.textContent = '⏏ Released — the HomePod is free for other AirPlay apps. Press play in Spotify to take it back.';
    ps.className = 'status warn'; ps.style.display = '';
  } else if (s.playing) {
    ps.textContent = s.now_playing ? ('▶ Playing: ' + s.now_playing) : '▶ Playing';
    ps.className = 'status ok'; ps.style.display = '';
  } else if (s.owntone_up) {
    ps.textContent = '⏸ Idle';
    ps.className = 'status'; ps.style.display = '';
  } else {
    ps.style.display = 'none';
  }
  var st = document.getElementById('status');
  var list = document.getElementById('list');
  list.innerHTML = '';
  if (!s.owntone_up) { st.textContent = 'Audio engine starting…'; st.className = 'status warn'; return; }
  if (!s.devices.length) { st.textContent = 'Scanning the network for HomePods… (this can take a moment)'; st.className = 'status warn'; }
  else { st.textContent = s.devices.length + ' AirPlay device(s) found on your network'; st.className = 'status ok'; }
  var current = (chosen !== null) ? chosen : s.saved;
  s.devices.forEach(function (d) {
    var row = document.createElement('label');
    row.className = 'row' + (d.selected ? ' active' : '');
    var rb = document.createElement('input');
    rb.type = 'radio'; rb.name = 'hp'; rb.value = d.name;
    if (d.name === current) rb.checked = true;
    rb.onchange = function () { chosen = d.name; document.getElementById('save').disabled = false; };
    var nm = document.createElement('span'); nm.className = 'name'; nm.textContent = d.name;
    row.appendChild(rb); row.appendChild(nm);
    if (d.needs_auth) { var b = document.createElement('span'); b.className = 'badge'; b.textContent = 'needs verification'; row.appendChild(b); }
    if (d.selected) { var p = document.createElement('span'); p.className = 'badge live'; p.textContent = 'playing here'; row.appendChild(p); }
    list.appendChild(row);
  });
}
document.getElementById('save').onclick = async function () {
  if (chosen === null) return;
  this.disabled = true;
  await fetch('api/select', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ name: chosen }) });
  chosen = null;
  await load();
};
document.getElementById('auto').onclick = async function () {
  await fetch('api/select', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ name: '' }) });
  chosen = null;
  document.getElementById('save').disabled = true;
  await load();
};
document.getElementById('test').onclick = async function () {
  this.disabled = true;
  var m = document.getElementById('testmsg');
  m.textContent = 'Sending a soft, low-volume tone to the HomePod — listen closely…';
  try {
    var r = await (await fetch('api/test', { method: 'POST' })).json();
    m.textContent = r.playing
      ? 'Playing a soft tone on: ' + (r.target || 'the selected HomePod') + ' — listen now. No sound? Then it is the AirPlay/volume leg, not Spotify.'
      : 'No HomePod discovered/selected yet — wait for the list above, or pick one and Save first.';
  } catch (e) { m.textContent = 'Could not send the test.'; }
  var btn = this;
  setTimeout(function () { btn.disabled = false; }, 4000);
};
document.getElementById('stop').onclick = async function () {
  this.disabled = true;
  var m = document.getElementById('stopmsg');
  m.textContent = 'Stopping playback on the speaker — this works no matter whose Spotify is playing.';
  try { await fetch('api/stop', { method: 'POST' }); } catch (e) {}
  var btn = this;
  setTimeout(function () { btn.disabled = false; }, 3000);
};
document.getElementById('release').onclick = async function () {
  this.disabled = true;
  var m = document.getElementById('releasemsg');
  m.textContent = 'Released — the HomePod is now free to AirPlay from other apps (Mofibo, Apple Music…). Press play in Spotify to take it back.';
  try { await fetch('api/release', { method: 'POST' }); } catch (e) {}
  var btn = this;
  setTimeout(function () { btn.disabled = false; }, 3000);
};
function tick() { load(); loadRooms(); }
tick();
setInterval(tick, 5000);
</script>
</body>
</html>`
