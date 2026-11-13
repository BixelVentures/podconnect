package main

// Radio is a plain DR stream on its own OwnTone player ("radio player").
//
// It follows the AirPlay rule "last command wins": a radio start takes the
// chosen speaker (pausing Spotify first when Spotify plays there), and any
// Spotify command or Spotify playback for that speaker stops the radio. Radio
// keeps no custody, writes nothing durable and never asks Native or Spotify's
// OwnTone for permission, so it can never block Spotify. It only "holds" a
// speaker while its own player is selected on it and playing.
import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type radioStation struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

var radioStations = []radioStation{{"p3", "P3"}, {"p1", "P1"}, {"p4-ostjylland", "P4 Østjylland"}, {"p6-beat", "P6 Beat"}, {"p8-jazz", "P8 Jazz"}}

var radioStreamURLs = map[string]string{"p3": "https://live-icy.dr.dk/A/A05H.mp3", "p1": "https://live-icy.dr.dk/A/A03H.mp3", "p4-ostjylland": "https://live-icy.dr.dk/A/A14H.mp3", "p6-beat": "https://live-icy.dr.dk/A/A29H.mp3", "p8-jazz": "https://live-icy.dr.dk/A/A22H.mp3"}

type radioTarget struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	Availability string `json:"availability"`
	Selectable   bool   `json:"selectable"`
	Default      bool   `json:"default"`
}

// radioState keeps the podconnect.radio.v1 wire shape PodVoice already reads.
type radioState struct {
	Schema           string         `json:"schema"`
	ProcessID        string         `json:"process_id"`
	Actions          []string       `json:"actions"`
	SourceKind       string         `json:"source_kind"`
	OwnerGeneration  uint64         `json:"owner_generation"`
	RetrievedAt      *int64         `json:"retrieved_at"`
	ValidUntil       *int64         `json:"valid_until"`
	ObservedAt       *int64         `json:"observed_at"`
	VolumePercent    *int           `json:"volume_percent"`
	Enabled          bool           `json:"enabled"`
	BlockedReasons   []string       `json:"blocked_reasons"`
	Stations         []radioStation `json:"stations"`
	Targets          []radioTarget  `json:"targets"`
	Capabilities     []string       `json:"capabilities"`
	State            string         `json:"state"`
	StationID        string         `json:"station_id"`
	TargetID         string         `json:"target_id"`
	Generation       uint64         `json:"generation"`
	Revision         uint64         `json:"revision"`
	Error            string         `json:"error,omitempty"`
	PhysicalVerified bool           `json:"physical_result_verified"`
}

// radioCommand keeps the podconnect.radio-action.v1 request shape. The
// expected_* fields are accepted for compatibility; last command wins, so they
// no longer reject a command.
type radioCommand struct {
	ActionID                string `json:"action_id"`
	Action                  string `json:"action"`
	StationID               string `json:"station_id,omitempty"`
	TargetID                string `json:"target_id,omitempty"`
	ExpectedRevision        uint64 `json:"expected_revision"`
	ExpectedProcessID       string `json:"expected_process_id"`
	ExpectedOwnerGeneration uint64 `json:"expected_owner_generation"`
	ExpectedGeneration      uint64 `json:"expected_generation"`
	VolumePercent           *int   `json:"volume_percent,omitempty"`
}

type radioObservation struct {
	State           string `json:"state"`
	StationID       string `json:"station_id"`
	TargetID        string `json:"target_id"`
	SourceKind      string `json:"source_kind"`
	OwnerGeneration uint64 `json:"owner_generation"`
	ObservedAt      int64  `json:"observed_at"`
	ValidUntil      int64  `json:"valid_until"`
	VolumePercent   *int   `json:"volume_percent"`
}

type radioActionResult struct {
	Schema      string           `json:"schema"`
	ProcessID   string           `json:"process_id"`
	ActionID    string           `json:"action_id"`
	Generation  uint64           `json:"generation"`
	Terminal    bool             `json:"terminal"`
	Outcome     string           `json:"outcome"`
	Observation radioObservation `json:"observation"`
	err         error
}

var (
	errRadioUnavailable = errors.New("radio unavailable")
	errRadioInvalid     = errors.New("invalid radio command")
)

// radioOutput is one AirPlay output as the radio player sees it.
type radioOutput struct {
	ID       string
	Name     string
	Selected bool
	Volume   int
}

// radioSpotify is how the radio reaches Spotify: only to hand the speaker
// over when the radio takes the speaker Spotify currently holds. Replaced in tests.
type radioSpotify interface {
	routeRoom() (roomID string, playing bool)
	// routeState is one read of Spotify's route room, whether it plays and
	// whether it has a session at all.
	routeState() (roomID string, playing, active bool)
	pause() error
	// endSession ends Spotify's session in the background (fresh, idle
	// Spotify engine) so the speaker carries no Spotify leftovers. It never
	// blocks the radio and never starts audio.
	endSession()
}

type radioPlayerConfig struct {
	Enabled bool
	Root    string
	APIPort int
	WSPort  int
}

const (
	radioObservationTTL = 30 * time.Second
	radioYieldTimeout   = 3 * time.Second
	radioResultHistory  = 256
)

// radioStartWait bounds how long a start waits for the stream to play.
var radioStartWait = 8 * time.Second

// radioPlayPoll is how often a start reads whether the stream plays yet.
const radioPlayPoll = 100 * time.Millisecond

type radioPlayer struct {
	cfg     radioPlayerConfig
	base    string
	client  *http.Client
	rooms   func() []*Room
	spotify radioSpotify
	launch  func(context.Context) error // nil: the player process is managed elsewhere (tests)

	op sync.Mutex // one effect at a time; a newer command waits, then wins

	mu       sync.Mutex
	state    radioState
	pending  bool
	outputs  map[string]string // room id -> radio player output id while selected
	starting string            // room a start is taking right now (Spotify is being handed over)
	results  map[string]radioActionResult
	order    []string
	up       bool
}

var radioOwner = newRadioPlayer(radioPlayerConfig{})

func newRadioPlayer(cfg radioPlayerConfig) *radioPlayer {
	if cfg.APIPort == 0 {
		cfg.APIPort = 3989
	}
	if cfg.WSPort == 0 {
		cfg.WSPort = 3990
	}
	p := &radioPlayer{
		cfg:     cfg,
		base:    fmt.Sprintf("http://127.0.0.1:%d", cfg.APIPort),
		client:  &http.Client{Timeout: 4 * time.Second},
		rooms:   loadRooms,
		spotify: managerSpotify{},
		outputs: map[string]string{},
		results: map[string]radioActionResult{},
		state: radioState{
			Schema: "podconnect.radio.v1", ProcessID: attentionUUID(), OwnerGeneration: 1,
			Actions: []string{}, SourceKind: "none", State: "idle", Revision: 1,
			Stations: append([]radioStation(nil), radioStations...), Targets: []radioTarget{},
			Capabilities: []string{"start", "stop", "move", "volume"}, BlockedReasons: []string{},
		},
	}
	if cfg.Root != "" {
		p.launch = p.runProcess
	}
	return p
}

// radioPlayerConfigFromOptions reads the add-on options. Radio is on unless
// radio_enabled is explicitly false.
func radioPlayerConfigFromOptions() radioPlayerConfig {
	cfg := radioPlayerConfig{Enabled: true, Root: filepath.Join(dataDir, "radio"), APIPort: 3989, WSPort: 3990}
	raw, err := os.ReadFile(filepath.Join(dataDir, "options.json"))
	if err != nil {
		return cfg
	}
	var o struct {
		Enabled *bool `json:"radio_enabled"`
		API     int   `json:"radio_sender_api_port"`
		WS      int   `json:"radio_sender_ws_port"`
	}
	if json.Unmarshal(raw, &o) != nil {
		return cfg
	}
	if o.Enabled != nil {
		cfg.Enabled = *o.Enabled
	}
	if o.API >= 1024 && o.API <= 65535 {
		cfg.APIPort = o.API
	}
	if o.WS >= 1024 && o.WS <= 65535 && o.WS != cfg.APIPort {
		cfg.WSPort = o.WS
	}
	return cfg
}

// run supervises the radio player process and keeps the observed state fresh.
// It returns when ctx ends.
func (p *radioPlayer) run(ctx context.Context) {
	if !p.cfg.Enabled {
		log.Printf("radio: disabled by option")
		return
	}
	if p.launch != nil {
		go p.launch(ctx)
	}
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	reset := false
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		if !reset {
			// A fresh radio player starts without any selected speaker.
			if p.reachable(ctx) {
				p.op.Lock()
				_ = p.release(ctx)
				p.op.Unlock()
				reset = true
			}
			continue
		}
		p.watch(ctx)
	}
}

func (p *radioPlayer) reachable(ctx context.Context) bool {
	_, err := p.player(ctx)
	p.mu.Lock()
	p.up = err == nil
	p.mu.Unlock()
	return err == nil
}

// watch notices when the radio stopped on its own (stream ended, HomePod taken
// by another sender) and then lets go of the speaker. It is one bounded read
// while radio plays and changes nothing otherwise.
func (p *radioPlayer) watch(ctx context.Context) {
	p.mu.Lock()
	playing := p.state.State == "playing" && p.state.SourceKind == "radio" && !p.pending
	p.mu.Unlock()
	up := p.reachable(ctx)
	if !playing {
		return
	}
	if up {
		if status, err := p.player(ctx); err == nil && status.State == "play" {
			return
		}
	}
	if !p.op.TryLock() {
		return
	}
	defer p.op.Unlock()
	p.mu.Lock()
	still := p.state.State == "playing" && p.state.SourceKind == "radio"
	p.mu.Unlock()
	if !still {
		return
	}
	log.Printf("radio: playback ended by the speaker or stream; releasing the speaker")
	_ = p.release(ctx)
	p.setStopped("")
}

// --- radio player (OwnTone JSON API) ------------------------------------------

type radioPlayerStatus struct {
	State  string `json:"state"`
	Volume int    `json:"volume"`
}

func (p *radioPlayer) call(ctx context.Context, method, path string, body any, out any) error {
	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(encoded)
	}
	req, err := http.NewRequestWithContext(ctx, method, p.base+path, reader)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := p.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("radio player %s %s returned HTTP %d", method, strings.SplitN(path, "?", 2)[0], resp.StatusCode)
	}
	if out == nil {
		return nil
	}
	return json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(out)
}

func (p *radioPlayer) player(ctx context.Context) (radioPlayerStatus, error) {
	var status radioPlayerStatus
	err := p.call(ctx, http.MethodGet, "/api/player", nil, &status)
	return status, err
}

func (p *radioPlayer) playerOutputs(ctx context.Context) ([]radioOutput, error) {
	var raw struct {
		Outputs []struct {
			ID       json.RawMessage `json:"id"`
			Name     string          `json:"name"`
			Selected bool            `json:"selected"`
			Volume   int             `json:"volume"`
		} `json:"outputs"`
	}
	if err := p.call(ctx, http.MethodGet, "/api/outputs", nil, &raw); err != nil {
		return nil, err
	}
	out := make([]radioOutput, 0, len(raw.Outputs))
	for _, o := range raw.Outputs {
		id := strings.Trim(string(o.ID), `"`)
		if id == "" || o.Name == "" {
			continue
		}
		out = append(out, radioOutput{ID: id, Name: o.Name, Selected: o.Selected, Volume: o.Volume})
	}
	return out, nil
}

func radioOutputFor(outputs []radioOutput, room *Room) (radioOutput, bool) {
	if room == nil {
		return radioOutput{}, false
	}
	if room.HomepodID != "" {
		for _, o := range outputs {
			if o.ID == room.HomepodID {
				return o, true
			}
		}
	}
	if room.HomepodName != "" {
		for _, o := range outputs {
			if strings.EqualFold(o.Name, room.HomepodName) {
				return o, true
			}
		}
	}
	return radioOutput{}, false
}

// release stops the radio player and deselects every speaker, so the HomePod
// is free for Spotify. It is safe to repeat.
func (p *radioPlayer) release(ctx context.Context) error {
	errStop := p.call(ctx, http.MethodPut, "/api/player/stop", nil, nil)
	errClear := p.call(ctx, http.MethodPut, "/api/queue/clear", nil, nil)
	errSet := p.call(ctx, http.MethodPut, "/api/outputs/set", map[string]any{"outputs": []string{}}, nil)
	p.mu.Lock()
	p.outputs = map[string]string{}
	p.mu.Unlock()
	return errors.Join(errStop, errClear, errSet)
}

func (p *radioPlayer) setVolume(ctx context.Context, outputID string, volume int) error {
	return p.call(ctx, http.MethodPut, "/api/outputs/"+url.PathEscape(outputID), map[string]any{"volume": volume}, nil)
}

// play selects exactly one speaker and starts the stream there.
func (p *radioPlayer) play(ctx context.Context, station string, room *Room) (level int, err error) {
	// One content-free timing line per start (milliseconds per step) so a
	// device test shows exactly where a start spends its time.
	began := time.Now()
	var marks []string
	mark := func(step string) { marks = append(marks, fmt.Sprintf("%s=%d", step, time.Since(began).Milliseconds())) }
	defer func() {
		outcome := "playing"
		if err != nil {
			outcome = "failed"
		}
		log.Printf("radio: start timing outcome=%s %s", outcome, strings.Join(marks, " "))
	}()
	outputs, err := p.playerOutputs(ctx)
	mark("outputs_read")
	if err != nil {
		return 0, err
	}
	output, ok := radioOutputFor(outputs, room)
	if !ok {
		return 0, errRadioUnavailable
	}
	if err := p.call(ctx, http.MethodPut, "/api/outputs/set", map[string]any{"outputs": []string{output.ID}}, nil); err != nil {
		return 0, err
	}
	mark("output_selected")
	p.mu.Lock()
	p.outputs = map[string]string{room.ID: output.ID}
	p.mu.Unlock()
	// Every new start uses the room's start level (startLevelForRoom), the same
	// single rule as Spotify: the shared level, or the room's lower chosen one.
	level = startLevelForRoom(room.ID)
	if err := p.setVolume(ctx, output.ID, level); err != nil {
		return 0, err
	}
	mark("volume_set")
	query := url.Values{"uris": {radioStreamURLs[station]}, "clear": {"true"}, "playback": {"start"}}
	if err := p.call(ctx, http.MethodPost, "/api/queue/items/add?"+query.Encode(), nil, nil); err != nil {
		return 0, err
	}
	mark("stream_queued")
	deadline := time.Now().Add(radioStartWait)
	for {
		status, err := p.player(ctx)
		if err == nil && status.State == "play" {
			mark("playing")
			return level, nil
		}
		if time.Now().After(deadline) || ctx.Err() != nil {
			return 0, errors.New("radio stream did not start")
		}
		select {
		case <-ctx.Done():
		case <-time.After(radioPlayPoll):
		}
	}
}

// --- process ----------------------------------------------------------------

func (p *radioPlayer) config() string {
	root := p.cfg.Root
	return fmt.Sprintf(`general {
	uid = "owntone"
	db_path = "%s/radio.db"
	cache_dir = "%s"
	logfile = "%s/radio.log"
	websocket_port = %d
	# Same output pre-buffer as Spotify's OwnTone (buffer_ms, default 500 ms).
	# OwnTone's own default is 2250 ms, which delayed every radio start ~1.75 s.
	start_buffer_ms = %d
}
library {
	port = %d
	name = "PodConnect Radio"
	directories = { "%s/media" }
	pipe_autostart = false
}
audio {
	type = "disabled"
}
mpd {
	port = 0
}
`, root, root, root, p.cfg.WSPort, readBufferMs(), p.cfg.APIPort, root)
}

// runProcess keeps one radio player process alive, like the room engines.
func (p *radioPlayer) runProcess(ctx context.Context) error {
	if err := os.MkdirAll(filepath.Join(p.cfg.Root, "media"), 0o755); err != nil {
		log.Printf("radio: player directory unavailable: %v", err)
		return err
	}
	conf := filepath.Join(p.cfg.Root, "owntone.conf")
	if err := os.WriteFile(conf, []byte(p.config()), 0o644); err != nil {
		log.Printf("radio: player config unavailable: %v", err)
		return err
	}
	_ = exec.Command("chown", "-R", "owntone:owntone", p.cfg.Root).Run()
	backoff := 2 * time.Second
	for ctx.Err() == nil {
		cmd := exec.CommandContext(ctx, "owntone", "-f", "-c", conf)
		started := time.Now()
		if err := cmd.Start(); err != nil {
			log.Printf("radio: player could not start: %v", err)
		} else {
			_ = cmd.Wait()
			if ctx.Err() == nil {
				log.Printf("radio: player exited; restarting")
			}
		}
		if time.Since(started) > time.Minute {
			backoff = 2 * time.Second
		}
		select {
		case <-ctx.Done():
		case <-time.After(backoff):
		}
		if backoff < 30*time.Second {
			backoff *= 2
		}
	}
	return ctx.Err()
}

// --- state ------------------------------------------------------------------

func (p *radioPlayer) snapshot() radioState {
	p.mu.Lock()
	defer p.mu.Unlock()
	s := p.state
	s.Actions = p.actionsLocked()
	s.Stations = append([]radioStation{}, s.Stations...)
	s.Targets = append([]radioTarget{}, s.Targets...)
	s.Capabilities = append([]string{}, s.Capabilities...)
	s.BlockedReasons = append([]string{}, s.BlockedReasons...)
	if s.VolumePercent != nil {
		v := *s.VolumePercent
		s.VolumePercent = &v
	}
	return s
}

func (p *radioPlayer) actionsLocked() []string {
	actions := []string{}
	if !p.state.Enabled || p.pending {
		return actions
	}
	selectable, movable := false, false
	for _, t := range p.state.Targets {
		if t.Selectable && t.Availability == "available" {
			selectable = true
			if t.ID != p.state.TargetID {
				movable = true
			}
		}
	}
	playing := p.state.State == "playing" && p.state.SourceKind == "radio"
	if selectable {
		actions = append(actions, "start")
	}
	if playing {
		actions = append(actions, "stop")
		if movable {
			actions = append(actions, "move")
		}
		actions = append(actions, "volume")
	}
	return actions
}

func (p *radioPlayer) observationLocked() radioObservation {
	o := radioObservation{State: p.state.State, StationID: p.state.StationID, TargetID: p.state.TargetID, SourceKind: p.state.SourceKind, OwnerGeneration: p.state.OwnerGeneration}
	if p.state.ObservedAt != nil {
		o.ObservedAt = *p.state.ObservedAt
	}
	if p.state.ValidUntil != nil {
		o.ValidUntil = *p.state.ValidUntil
	}
	if p.state.VolumePercent != nil {
		v := *p.state.VolumePercent
		o.VolumePercent = &v
	}
	return o
}

func (p *radioPlayer) stampLocked() {
	now := time.Now()
	observed, valid := now.Unix(), now.Add(radioObservationTTL).Unix()
	p.state.ObservedAt, p.state.RetrievedAt, p.state.ValidUntil = &observed, &observed, &valid
}

// refresh is a read: it observes the radio player and the configured rooms and
// never selects, starts, stops or reacquires anything.
func (p *radioPlayer) refresh(ctx context.Context) {
	rooms := p.rooms()
	var outputs []radioOutput
	up := false
	if p.cfg.Enabled {
		read, cancel := context.WithTimeout(ctx, 2*time.Second)
		var err error
		outputs, err = p.playerOutputs(read)
		cancel()
		up = err == nil
	}
	targets := make([]radioTarget, 0, len(rooms))
	for _, room := range rooms {
		t := radioTarget{ID: room.ID, Name: room.Name, Availability: "unavailable", Default: room.Idx == 0}
		if _, ok := radioOutputFor(outputs, room); ok && up {
			t.Availability, t.Selectable = "available", true
		}
		targets = append(targets, t)
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.up = up
	p.state.Targets = targets
	p.state.Enabled = p.cfg.Enabled && up
	switch {
	case !p.cfg.Enabled:
		p.state.BlockedReasons = []string{"radio_disabled"}
	case !up:
		p.state.BlockedReasons = []string{"radio_player_starting"}
	default:
		p.state.BlockedReasons = []string{}
	}
	if !p.pending {
		p.stampLocked()
	}
}

func (p *radioPlayer) setStopped(errText string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.state.State, p.state.SourceKind, p.state.StationID, p.state.TargetID = "stopped", "none", "", ""
	p.state.VolumePercent = nil
	p.state.Error = errText
	p.state.Revision++
	p.stampLocked()
}

// --- commands ---------------------------------------------------------------

func (p *radioPlayer) remember(result radioActionResult) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if _, ok := p.results[result.ActionID]; !ok {
		p.order = append(p.order, result.ActionID)
	}
	p.results[result.ActionID] = result
	for len(p.order) > radioResultHistory {
		delete(p.results, p.order[0])
		p.order = p.order[1:]
	}
}

func (p *radioPlayer) execute(ctx context.Context, q radioCommand) (radioState, error) {
	if q.ActionID == "" || len(q.ActionID) > 128 {
		return p.snapshot(), errRadioInvalid
	}
	p.mu.Lock()
	previous, seen := p.results[q.ActionID]
	p.mu.Unlock()
	if seen {
		return p.snapshot(), previous.err
	}
	p.refresh(ctx)
	if !p.snapshot().Enabled {
		return p.snapshot(), errRadioUnavailable
	}
	room, err := p.validate(q)
	if err != nil {
		return p.snapshot(), err
	}

	p.op.Lock()
	defer p.op.Unlock()
	p.mu.Lock()
	p.pending = true
	p.state.Generation++
	generation := p.state.Generation
	p.state.Revision++
	before := p.state
	p.mu.Unlock()

	effect, cancel := context.WithTimeout(ctx, 12*time.Second)
	defer cancel()
	err = p.apply(effect, q, room, before)

	p.mu.Lock()
	p.pending = false
	p.state.Revision++
	p.stampLocked()
	result := radioActionResult{Schema: "podconnect.radio-result.v1", ProcessID: p.state.ProcessID, ActionID: q.ActionID, Generation: generation, Terminal: true, Outcome: "committed", err: err}
	if err != nil {
		result.Outcome = "rejected"
	}
	result.Observation = p.observationLocked()
	p.mu.Unlock()
	p.remember(result)
	return p.snapshot(), err
}

func (p *radioPlayer) validate(q radioCommand) (*Room, error) {
	s := p.snapshot()
	playing := s.State == "playing" && s.SourceKind == "radio"
	switch q.Action {
	case "start", "move":
		station := q.StationID
		if q.Action == "move" {
			if !playing || q.StationID != "" || q.VolumePercent != nil {
				return nil, errRadioInvalid
			}
			station = s.StationID
		}
		if _, ok := radioStreamURLs[station]; !ok {
			return nil, errRadioInvalid
		}
		// The start level is the shared 35 % rule; a start never carries its own.
		if q.VolumePercent != nil {
			return nil, errRadioInvalid
		}
		for _, t := range s.Targets {
			if t.ID == q.TargetID && t.Selectable && t.Availability == "available" {
				for _, room := range p.rooms() {
					if room.ID == q.TargetID {
						return room, nil
					}
				}
			}
		}
		return nil, errRadioUnavailable
	case "stop":
		if q.StationID != "" || q.TargetID != "" || q.VolumePercent != nil {
			return nil, errRadioInvalid
		}
		return nil, nil
	case "volume":
		if !playing || q.VolumePercent == nil || *q.VolumePercent < 0 || *q.VolumePercent > 100 || q.StationID != "" || q.TargetID != "" {
			return nil, errRadioInvalid
		}
		return nil, nil
	}
	return nil, errRadioInvalid
}

func (p *radioPlayer) apply(ctx context.Context, q radioCommand, room *Room, before radioState) error {
	switch q.Action {
	case "stop":
		err := p.release(ctx)
		p.setStoppedLocked()
		return err
	case "volume":
		p.mu.Lock()
		output := p.outputs[before.TargetID]
		p.mu.Unlock()
		if output == "" {
			return errRadioUnavailable
		}
		if err := p.setVolume(ctx, output, *q.VolumePercent); err != nil {
			return err
		}
		v := *q.VolumePercent
		p.mu.Lock()
		p.state.VolumePercent = &v
		p.mu.Unlock()
		return nil
	}
	station := q.StationID
	if q.Action == "move" {
		station = before.StationID
	}
	// Last command wins: Spotify on this speaker is paused and its Connect
	// session ends, the same way choosing another AirPlay source takes the
	// speaker from Spotify. Without ending the session, the radio's AirPlay
	// connection replaced Spotify's HomePod session underneath Spotify's
	// OwnTone, and Spotify's engine then refused every later transfer from the
	// phone ("alias registry unavailable") until the add-on restarted.
	// The hand-over runs in parallel with the radio's own start, so a start
	// waits for the slower of the two, not for both.
	p.mu.Lock()
	p.starting = room.ID
	p.mu.Unlock()
	defer func() { p.mu.Lock(); p.starting = ""; p.mu.Unlock() }()
	handover := make(chan struct{})
	go func() {
		defer close(handover)
		route, playing, active := p.spotify.routeState()
		if route != room.ID || !(playing || active) {
			return
		}
		if playing {
			if err := p.spotify.pause(); err != nil {
				log.Printf("radio: pausing Spotify before radio failed: %v", err)
			}
		}
		p.spotify.endSession()
	}()
	defer func() {
		select {
		case <-handover:
		case <-time.After(radioYieldTimeout):
		}
	}()
	level, err := p.play(ctx, station, room)
	if err != nil {
		// A failed start leaves nothing behind: no selected speaker, no queue.
		cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		_ = p.release(cleanup)
		cancel()
		p.setStoppedLocked()
		log.Printf("radio: start failed: %v", err)
		return err
	}
	p.mu.Lock()
	p.state.State, p.state.SourceKind, p.state.StationID, p.state.TargetID, p.state.Error = "playing", "radio", station, room.ID, ""
	p.state.VolumePercent = &level
	p.mu.Unlock()
	return nil
}

// setStoppedLocked records "stopped" during an effect (the op lock is held,
// not the state mutex).
func (p *radioPlayer) setStoppedLocked() {
	p.mu.Lock()
	p.state.State, p.state.SourceKind, p.state.StationID, p.state.TargetID = "stopped", "none", "", ""
	p.state.VolumePercent = nil
	p.mu.Unlock()
}

// yield stops the radio when it plays on roomID ("" = any speaker). Spotify
// commands call it before they act; it never fails a Spotify command.
func (p *radioPlayer) yield(ctx context.Context, roomID string) bool {
	if !p.playingOn(roomID) {
		return false
	}
	ctx, cancel := context.WithTimeout(ctx, radioYieldTimeout)
	defer cancel()
	p.op.Lock()
	defer p.op.Unlock()
	if !p.playingOn(roomID) {
		return false
	}
	if err := p.release(ctx); err != nil {
		log.Printf("radio: releasing the speaker for Spotify was incomplete: %v", err)
	}
	p.setStopped("")
	log.Printf("radio: stopped because Spotify took the speaker")
	return true
}

func (p *radioPlayer) playingOn(roomID string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.state.SourceKind != "radio" || p.state.State != "playing" && !p.pending {
		return false
	}
	return roomID == "" || p.state.TargetID == roomID
}

// spotifyPlaying is the non-blocking hook for the Spotify bridge: Spotify
// audio on a speaker stops radio on that speaker.
func (p *radioPlayer) spotifyPlaying(roomID string) {
	if roomID == "" || !p.playingOn(roomID) {
		return
	}
	p.mu.Lock()
	handingOver := p.starting == roomID
	p.mu.Unlock()
	if handingOver {
		return // The radio start is the newer command; Spotify is being paused now.
	}
	go p.yield(context.Background(), roomID)
}

// --- Spotify side -------------------------------------------------------------

type managerSpotify struct{}

func (managerSpotify) routeRoom() (string, bool) {
	room := primaryRoom()
	if room == nil {
		return "", false
	}
	gl := librespotStatus(room.Librespot)
	return spotifyRouteRoomID(gl, loadRooms()), gl.Active && !gl.Paused && !gl.Stopped
}

func (managerSpotify) routeState() (string, bool, bool) {
	room := primaryRoom()
	if room == nil {
		return "", false, false
	}
	gl := librespotStatus(room.Librespot)
	return spotifyRouteRoomID(gl, loadRooms()), gl.Active && !gl.Paused && !gl.Stopped, gl.Active
}

func (managerSpotify) endSession() {
	room := primaryRoom()
	if room == nil || mgr == nil {
		return
	}
	if rt := mgr.runtime(room.ID); rt != nil {
		rt.freshNative("radio took the speaker")
	}
}

func (managerSpotify) pause() error {
	room := primaryRoom()
	if room == nil {
		return nil
	}
	return librespotTransport(room.Librespot, "pause")
}

// spotifyRouteRoomID names the room the single Spotify engine currently plays
// to, from its selected alias (or bound alias room).
func spotifyRouteRoomID(gl glStatus, rooms []*Room) string {
	if gl.AliasBinding != nil && gl.AliasBinding.valid() {
		for _, room := range rooms {
			if room.ID == gl.AliasBinding.RoomID {
				return room.ID
			}
		}
		return ""
	}
	if gl.SelAlias >= 1 && gl.SelAlias <= len(rooms) {
		return rooms[gl.SelAlias-1].ID
	}
	return ""
}

// spotifyCommandRooms lists every room a Spotify command names in its query
// or JSON body ("room", "room_id", at any depth). The body is restored.
func spotifyCommandRooms(r *http.Request) []string {
	var rooms []string
	for _, key := range []string{"room", "room_id"} {
		for _, v := range r.URL.Query()[key] {
			if v != "" {
				rooms = append(rooms, v)
			}
		}
	}
	if r.Body == nil {
		return rooms
	}
	raw, err := io.ReadAll(io.LimitReader(r.Body, 1<<16))
	r.Body.Close()
	r.Body = io.NopCloser(bytes.NewReader(raw))
	if err != nil || len(raw) == 0 {
		return rooms
	}
	var decoded any
	if json.Unmarshal(raw, &decoded) != nil {
		return rooms
	}
	var walk func(any)
	walk = func(v any) {
		switch t := v.(type) {
		case map[string]any:
			for key, value := range t {
				if s, ok := value.(string); ok && (key == "room" || key == "room_id") && s != "" {
					rooms = append(rooms, s)
				} else {
					walk(value)
				}
			}
		case []any:
			for _, value := range t {
				walk(value)
			}
		}
	}
	walk(decoded)
	return rooms
}

// spotifyTakesOver wraps a Spotify command handler: radio on the speaker that
// the command touches (named room or Spotify's current route) stops first.
func spotifyTakesOver(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			radioYieldForCommand(r)
		}
		next(w, r)
	}
}

func radioYieldForCommand(r *http.Request) {
	owner := radioOwner
	if owner == nil || !owner.playingOn("") {
		return
	}
	rooms := spotifyCommandRooms(r)
	if route, _ := owner.spotify.routeRoom(); route != "" {
		rooms = append(rooms, route)
	}
	if len(rooms) == 0 {
		rooms = append(rooms, "") // unknown speaker: Spotify wins
	}
	for _, id := range rooms {
		if owner.yield(r.Context(), id) {
			return
		}
	}
}
