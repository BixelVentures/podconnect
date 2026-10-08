// Wave 3 (push-state): feed each room's go-librespot transport/volume state from its /events
// websocket instead of polling /status every 200ms — ADDITIVELY. Polling stays as (a) the seed on
// every (re)connect and (b) the fallback whenever the websocket is down. A websocket bug degrades to
// polling, never to breakage.
//
// glLive holds the latest glStatus per room, written by runGLEvents (from ws events or fallback
// polls) and read in-memory by roomBridge's 200ms tick. The reconcile cadence + volume-cap guards in
// roomBridge are unchanged — it just reads live.Get() instead of hitting the network each tick.
//
// Event catalog (go-librespot master): every ws message is JSON {"type":"<name>","data":{...}}.
// No state replay on connect (we GET /status to seed). No server heartbeat (we client-PING + bound
// the read deadline, reconnecting on any read error).
package main

import (
	"encoding/json"
	"math"
	"sync"
	"time"
)

// glLive is the thread-safe latest go-librespot state for one room, plus a track-change signal.
type glLive struct {
	mu               sync.Mutex
	st               glStatus
	trackURI         string
	trackChangeSeq   uint64 // bumped whenever metadata.uri changes (future buffer-flush hook)
	aliasRevision    uint64 // observed alias/source intent; unchanged status polls do not bump it
	routeDispatchSeq uint64 // mutex-owned command admission, not native acceptance
	sourceStop       <-chan struct{}
	sourceRetired    bool
	runEpoch         uint64
	phaseEpoch       uint64
	nextRequest      uint64
	acceptedRequest  uint64
	wireRevision     [3]uint64 // transport, volume, selected alias
}

// Get returns a copy of the latest glStatus (safe to use without holding the lock).
func (l *glLive) Get() glStatus {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.st
}

// routeSnapshot binds routing to the same mutex-owned alias intent as the status.
func (l *glLive) routeSnapshot() (glStatus, uint64) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.observeStopLocked()
	return l.st, l.aliasRevision
}

// admitRoute linearizes an already prepared command against current intent.
// The caller performs HTTP only after unlocking; later intent cannot unsend this
// admitted command, but its completion must not clear the newer pending route.
func (l *glLive) admitRoute(alias int, revision uint64) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.observeStopLocked()
	if l.sourceRetired || l.st.SelAlias != alias || l.aliasRevision != revision || (l.st.AliasBinding != nil && !l.st.AliasBinding.valid()) {
		return false
	}
	l.routeDispatchSeq++
	return true
}

// Route admission observes an external Stop under the same mutex. A stop cannot
// retroactively revoke an already admitted command; future claims are refused.
func (l *glLive) observeStopLocked() {
	if !l.sourceRetired && sourceStopped(l.sourceStop) {
		l.sourceRetired = true
		l.runEpoch++
		l.phaseEpoch++
		l.acceptedRequest = 0
		l.aliasRevision++
	}
}

// set remains the direct fixture initialization path. Production status requests
// use refreshStatus, which fences their producer and intervening wire observations.
func (l *glLive) set(st glStatus) {
	l.mu.Lock()
	if st.SelAlias != l.st.SelAlias {
		l.aliasRevision++
	}
	l.st = st
	l.mu.Unlock()
}

// trackSeq returns the current monotonic track-change sequence.
func (l *glLive) trackSeq() uint64 {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.trackChangeSeq
}

type glSource struct {
	run, phase uint64
	stop       <-chan struct{}
}

type glStatusRequest struct {
	source   glSource
	sequence uint64
	wire     [3]uint64
}

func sourceStopped(stop <-chan struct{}) bool {
	select {
	case <-stop:
		return true
	default:
		return false
	}
}

func (l *glLive) beginRun(stop <-chan struct{}) glSource {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.runEpoch++
	l.phaseEpoch++
	l.acceptedRequest = 0
	l.aliasRevision++
	l.sourceStop = stop
	l.sourceRetired = false
	return glSource{run: l.runEpoch, stop: stop}
}

func (l *glLive) beginPhase(run glSource) (glSource, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if run.run != l.runEpoch || sourceStopped(run.stop) {
		return glSource{}, false
	}
	l.phaseEpoch++
	l.aliasRevision++ // A claim captured during retirement cannot cross into this source.
	l.acceptedRequest = 0
	l.sourceRetired = false
	return glSource{run: run.run, phase: l.phaseEpoch, stop: run.stop}, true
}

func (l *glLive) sourceCurrent(source glSource) bool {
	return source.run == l.runEpoch && source.phase == l.phaseEpoch && !sourceStopped(source.stop)
}

func (l *glLive) retirePhase(source glSource) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if source.run == l.runEpoch && source.phase == l.phaseEpoch {
		l.sourceRetired = true
		l.aliasRevision++
		l.phaseEpoch++
		l.acceptedRequest = 0
	}
}

func (l *glLive) retireRun(run glSource) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if run.run == l.runEpoch {
		l.aliasRevision++
		l.sourceRetired = true
		l.runEpoch++
		l.phaseEpoch++
		l.acceptedRequest = 0
	}
}

func (l *glLive) beginStatus(source glSource) (glStatusRequest, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if !l.sourceCurrent(source) {
		return glStatusRequest{}, false
	}
	l.nextRequest++
	return glStatusRequest{source: source, sequence: l.nextRequest, wire: l.wireRevision}, true
}

// acceptStatus publishes only groups not superseded by wire input since request
// start. Any accepted group advances response order, not merely request start.
func (l *glLive) acceptStatus(request glStatusRequest, st glStatus) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if !l.sourceCurrent(request.source) || request.sequence <= l.acceptedRequest {
		return false
	}
	accepted := false
	if request.wire[0] == l.wireRevision[0] {
		l.st.Active, l.st.Paused, l.st.Stopped = st.Active, st.Paused, st.Stopped
		accepted = true
	}
	if request.wire[1] == l.wireRevision[1] {
		l.st.HasVol, l.st.VolPct = st.HasVol, st.VolPct
		accepted = true
	}
	if request.wire[2] == l.wireRevision[2] {
		if l.st.AliasBinding != nil && st.AliasBinding == nil {
			copy := *l.st.AliasBinding
			copy.Ready = false
			st.AliasBinding = &copy
			st.SelAlias = l.st.SelAlias
		}
		if l.st.SelAlias != st.SelAlias || !sameAliasBinding(l.st.AliasBinding, st.AliasBinding) {
			l.aliasRevision++
		}
		l.st.SelAlias = st.SelAlias
		l.st.AliasBinding = st.AliasBinding
		accepted = true
	}
	if accepted {
		l.acceptedRequest = request.sequence
	}
	return accepted
}

func (l *glLive) refreshStatus(source glSource, base string) bool {
	request, ok := l.beginStatus(source)
	if !ok {
		return false
	}
	st := librespotStatus(base) // No mutex is held during HTTP or parsing.
	l.acceptStatus(request, st)
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.sourceCurrent(source)
}

// applyEvent is retained for direct owner tests; the production reader supplies
// its source through applySourceEvent so retired readers cannot publish either.
func (l *glLive) applyEvent(typ string, data map[string]any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.applyEventLocked(typ, data)
}

func (l *glLive) applySourceEvent(source glSource, typ string, data map[string]any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.sourceCurrent(source) {
		l.applyEventLocked(typ, data)
	}
}

func (l *glLive) applyEventLocked(typ string, data map[string]any) {
	if typ == "selected_alias" {
		_, hasBinding := data["alias_binding"]
		if l.st.AliasBinding != nil || hasBinding {
			id, validID := numField(data, "id")
			if !validID || id < 1 || id > float64(^uint32(0)) || id != math.Trunc(id) {
				return
			}
		}
		if incoming, present := eventAliasBinding(data); present {
			current := l.st.AliasBinding
			// Events cannot mint a new player identity. A differing/invalid owner
			// only vetoes routing until an authoritative fenced status re-seeds it.
			if current == nil || !current.valid() || !incoming.valid() || current.Incarnation != incoming.Incarnation || current.Registry != incoming.Registry {
				if current == nil {
					l.st.AliasBinding = &aliasBinding{}
				} else {
					copy := *current
					copy.Ready = false
					l.st.AliasBinding = &copy
				}
				l.wireRevision[2]++
				l.aliasRevision++
				return
			}
			l.st.AliasBinding = incoming
		} else if l.st.AliasBinding != nil {
			copy := *l.st.AliasBinding
			copy.Ready = false
			l.st.AliasBinding = &copy
			l.wireRevision[2]++
			l.aliasRevision++
			return
		}
	}
	next, nextURI, changed := applyGLEvent(l.st, l.trackURI, typ, data)
	l.st = next
	l.trackURI = nextURI
	if changed {
		l.trackChangeSeq++
	}
	switch typ {
	case "playing", "paused", "stopped", "not_playing", "active", "playback_ready", "inactive":
		l.wireRevision[0]++
	case "volume":
		if _, ok := numField(data, "value"); ok {
			l.wireRevision[1]++
		}
	case "selected_alias":
		if _, ok := numField(data, "id"); ok {
			l.wireRevision[2]++
			l.aliasRevision++ // Valid equal-value observations also fence ABA.
		}
	}
}

// applyGLEvent is the PURE event→glStatus mapping (no I/O -> unit-tested). It folds one go-librespot
// ws event into the previous glStatus, carrying the previous track URI, and reports whether the track
// changed (metadata.uri differs). Conservative by design: when an event is ambiguous we leave fields
// as-is and let the next reconnect's /status re-seed truth.
//
//	volume   -> HasVol=true, VolPct=clamp(data.value 0..100)
//	playing  -> Active=true, Paused=false, Stopped=false
//	paused   -> Paused=true
//	stopped  -> Stopped=true (Active left per /status semantics)
//	not_playing -> Paused=false (track ended; Active left as-is, /status re-seeds)
//	active   -> Active=true
//	inactive -> Active=false
//	metadata -> if data.uri changed: set trackURI + changed=true
//
// Unknown/irrelevant types (seek, shuffle_context, repeat_*, playback_ready, …) pass through unchanged.
func applyGLEvent(prev glStatus, prevURI string, typ string, data map[string]any) (glStatus, string, bool) {
	out := prev
	uri := prevURI
	changed := false
	switch typ {
	case "volume":
		if v, ok := numField(data, "value"); ok {
			out.VolPct = clampPct(int(v))
			out.HasVol = true
		}
	case "playing":
		out.Active = true
		out.Paused = false
		out.Stopped = false
	case "paused":
		out.Paused = true
	case "stopped":
		out.Stopped = true
	case "not_playing":
		// Track ended on its own. Not a user pause; leave Active to /status semantics.
		out.Paused = false
	case "active", "playback_ready":
		out.Active = true
	case "inactive":
		out.Active = false
	case "metadata":
		if s, ok := data["uri"].(string); ok && s != "" && s != prevURI {
			uri = s
			changed = true
		}
	case "selected_alias":
		// device-aliases instant path: the fork pushes the picked alias here so room routing fires
		// without waiting on the /status backstop. (The backstop still re-asserts it as a safety net.)
		if v, ok := numField(data, "id"); ok {
			out.SelAlias = int(v)
		}
	default:
		// seek, shuffle_context, repeat_context, repeat_track, … — no transport/volume impact here.
	}
	return out, uri, changed
}

// numField pulls a numeric field out of a decoded JSON object, tolerating both float64 (default
// decode) and json.Number.
func numField(data map[string]any, key string) (float64, bool) {
	switch v := data[key].(type) {
	case float64:
		return v, true
	case json.Number:
		f, err := v.Float64()
		return f, err == nil
	case int:
		return float64(v), true
	}
	return 0, false
}

// glEventBackoff caps the reconnect backoff for the ws connect retry loop.
const (
	glEventBackoffMin = 1 * time.Second
	glEventBackoffMax = 5 * time.Second
	glPollInterval    = 200 * time.Millisecond // fallback poll cadence (matches the old hot loop)
	glPingInterval    = 20 * time.Second       // client keepalive PING cadence
	glReseedInterval  = 1 * time.Second        // /status backstop while ws-connected. ALSO carries the device-aliases selected_alias_id (no ws event for it), so this bounds room-switch latency — kept at 1s so picking a room in Spotify routes within ~1s (cheap local poll).
	glReadDeadline    = 30 * time.Second       // bound each read so a dead socket is noticed (>10s server write timeout)
)

// runGLEvents keeps `live` fresh for one room: connect ws://localhost:<glPort>/events, seed from
// /status on every (re)connect, then push events. On ANY read/connect error it falls back to polling
// /status every 200ms while retrying the ws connect with backoff. Exits when stop is closed.
func runGLEvents(room *Room, live *glLive, stop <-chan struct{}) {
	wsURL := room.Librespot + "/events"
	// go-librespot serves http on r.Librespot; turn http:// into ws:// for the events endpoint.
	if len(wsURL) > 7 && wsURL[:7] == "http://" {
		wsURL = "ws://" + wsURL[7:]
	}
	backoff := glEventBackoffMin
	run := live.beginRun(stop)
	defer live.retireRun(run)
	for {
		select {
		case <-stop:
			return
		default:
		}

		phase, current := live.beginPhase(run)
		if !current {
			return
		}
		conn, err := dialWebsocket(wsURL)
		if err != nil {
			// Couldn't connect — poll /status to keep live fresh, then retry with backoff.
			if pollUntil(room, live, phase, backoff) {
				live.retirePhase(phase)
				return
			}
			live.retirePhase(phase)
			backoff *= 2
			if backoff > glEventBackoffMax {
				backoff = glEventBackoffMax
			}
			continue
		}
		backoff = glEventBackoffMin // healthy connect resets the backoff

		// Seed truth from /status on (re)connect (no state replay over ws).
		if !live.refreshStatus(phase, room.Librespot) {
			live.retirePhase(phase)
			conn.Close()
			return
		}

		// Client keepalive + correctness backstop. PING every ~20s (a write error tears the conn down
		// so we reconnect). AND re-seed from /status every ~3s while connected: events are a latency
		// optimization, but if go-librespot's event payloads ever differ from what we map, the ws stays
		// connected yet state would freeze at the seed — the periodic re-seed bounds any such staleness
		// to ~3s instead of forever, while still cutting the old 200ms poll churn ~15x.
		pingStop := make(chan struct{})
		go func(source glSource) {
			ping := time.NewTicker(glPingInterval)
			reseed := time.NewTicker(glReseedInterval)
			defer ping.Stop()
			defer reseed.Stop()
			for {
				select {
				case <-pingStop:
					return
				case <-stop:
					return
				case <-ping.C:
					if err := conn.Ping(); err != nil {
						conn.Close() // unblock the reader -> reconnect
						return
					}
				case <-reseed.C:
					if !live.refreshStatus(source, room.Librespot) {
						return
					} // /status truth backstop
				}
			}
		}(phase)

		// Read loop: bound each read so a silent dead socket is noticed and we reconnect.
		readErr := false
		for {
			select {
			case <-stop:
				live.retirePhase(phase)
				close(pingStop)
				conn.Close()
				return
			default:
			}
			_ = conn.SetReadDeadline(time.Now().Add(glReadDeadline))
			payload, err := conn.ReadMessage()
			if err != nil {
				readErr = true
				break
			}
			var ev struct {
				Type string         `json:"type"`
				Data map[string]any `json:"data"`
			}
			if json.Unmarshal(payload, &ev) != nil || ev.Type == "" {
				continue // ignore malformed / non-event frames
			}
			live.applySourceEvent(phase, ev.Type, ev.Data)
		}
		live.retirePhase(phase)
		close(pingStop)
		conn.Close()
		_ = readErr

		// Connection dropped — fall back to a short poll burst before reconnecting (keeps live fresh
		// during the gap), then loop to re-dial.
		phase, current = live.beginPhase(run)
		if !current {
			return
		}
		if pollUntil(room, live, phase, backoff) {
			live.retirePhase(phase)
			return
		}
		live.retirePhase(phase)
	}
}

// pollUntil polls /status every glPollInterval for `d`, writing each reading into live. Returns true
// if stop fired (caller should exit). This is the fallback that keeps the bridge working whenever the
// websocket is down.
func pollUntil(room *Room, live *glLive, source glSource, d time.Duration) bool {
	deadline := time.Now().Add(d)
	tick := time.NewTicker(glPollInterval)
	defer tick.Stop()
	for {
		if !live.refreshStatus(source, room.Librespot) {
			return true
		}
		select {
		case <-source.stop:
			return true
		case <-tick.C:
			if time.Now().After(deadline) {
				return false
			}
		}
	}
}
