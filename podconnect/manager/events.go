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
	"sync"
	"time"
)

// glLive is the thread-safe latest go-librespot state for one room, plus a track-change signal.
type glLive struct {
	mu             sync.Mutex
	st             glStatus
	trackURI       string
	trackChangeSeq uint64 // bumped whenever metadata.uri changes (future buffer-flush hook)
}

// Get returns a copy of the latest glStatus (safe to use without holding the lock).
func (l *glLive) Get() glStatus {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.st
}

// set replaces the whole status (used by the /status seed + fallback poll).
func (l *glLive) set(st glStatus) {
	l.mu.Lock()
	l.st = st
	l.mu.Unlock()
}

// trackSeq returns the current monotonic track-change sequence.
func (l *glLive) trackSeq() uint64 {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.trackChangeSeq
}

// applyEvent folds one decoded {type,data} event into the live state via the pure applyGLEvent, and
// bumps trackChangeSeq when the track changed.
func (l *glLive) applyEvent(typ string, data map[string]any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	next, nextURI, changed := applyGLEvent(l.st, l.trackURI, typ, data)
	l.st = next
	l.trackURI = nextURI
	if changed {
		l.trackChangeSeq++
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
	glReseedInterval  = 3 * time.Second        // /status correctness backstop while ws-connected (bounds staleness if events misbehave)
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
	for {
		select {
		case <-stop:
			return
		default:
		}

		conn, err := dialWebsocket(wsURL)
		if err != nil {
			// Couldn't connect — poll /status to keep live fresh, then retry with backoff.
			if pollUntil(room, live, stop, backoff) {
				return
			}
			backoff *= 2
			if backoff > glEventBackoffMax {
				backoff = glEventBackoffMax
			}
			continue
		}
		backoff = glEventBackoffMin // healthy connect resets the backoff

		// Seed truth from /status on (re)connect (no state replay over ws).
		live.set(librespotStatus(room.Librespot))

		// Client keepalive + correctness backstop. PING every ~20s (a write error tears the conn down
		// so we reconnect). AND re-seed from /status every ~3s while connected: events are a latency
		// optimization, but if go-librespot's event payloads ever differ from what we map, the ws stays
		// connected yet state would freeze at the seed — the periodic re-seed bounds any such staleness
		// to ~3s instead of forever, while still cutting the old 200ms poll churn ~15x.
		pingStop := make(chan struct{})
		go func() {
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
					live.set(librespotStatus(room.Librespot)) // /status truth backstop
				}
			}
		}()

		// Read loop: bound each read so a silent dead socket is noticed and we reconnect.
		readErr := false
		for {
			select {
			case <-stop:
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
			live.applyEvent(ev.Type, ev.Data)
		}
		close(pingStop)
		conn.Close()
		_ = readErr

		// Connection dropped — fall back to a short poll burst before reconnecting (keeps live fresh
		// during the gap), then loop to re-dial.
		if pollUntil(room, live, stop, backoff) {
			return
		}
	}
}

// pollUntil polls /status every glPollInterval for `d`, writing each reading into live. Returns true
// if stop fired (caller should exit). This is the fallback that keeps the bridge working whenever the
// websocket is down.
func pollUntil(room *Room, live *glLive, stop <-chan struct{}, d time.Duration) bool {
	deadline := time.Now().Add(d)
	tick := time.NewTicker(glPollInterval)
	defer tick.Stop()
	for {
		live.set(librespotStatus(room.Librespot))
		select {
		case <-stop:
			return true
		case <-tick.C:
			if time.Now().After(deadline) {
				return false
			}
		}
	}
}
