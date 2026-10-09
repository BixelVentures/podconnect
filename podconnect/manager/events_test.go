package main

import (
	"bufio"
	"bytes"
	"crypto/sha1"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"testing"
)

// TestApplyGLEvent covers the pure event→glStatus mapping: volume, transport (playing/paused/
// stopped/not_playing), active/inactive, and metadata-uri-change (changed=true only on a new uri).
func TestApplyGLEvent(t *testing.T) {
	cases := []struct {
		name        string
		prev        glStatus
		prevURI     string
		typ         string
		data        map[string]any
		want        glStatus
		wantURI     string
		wantChanged bool
	}{
		{
			name: "volume sets HasVol+VolPct (clamped)",
			typ:  "volume", data: map[string]any{"value": float64(150), "max": float64(100)},
			want: glStatus{HasVol: true, VolPct: 100},
		},
		{
			name: "volume json.Number tolerated",
			typ:  "volume", data: map[string]any{"value": json.Number("42")},
			want: glStatus{HasVol: true, VolPct: 42},
		},
		{
			name: "playing -> active, not paused/stopped",
			prev: glStatus{Paused: true, Stopped: true},
			typ:  "playing", data: map[string]any{},
			want: glStatus{Active: true, Paused: false, Stopped: false},
		},
		{
			name: "paused sets Paused",
			prev: glStatus{Active: true},
			typ:  "paused", data: map[string]any{},
			want: glStatus{Active: true, Paused: true},
		},
		{
			name: "stopped sets Stopped",
			prev: glStatus{Active: true},
			typ:  "stopped", data: map[string]any{},
			want: glStatus{Active: true, Stopped: true},
		},
		{
			name: "not_playing clears Paused, leaves Active",
			prev: glStatus{Active: true, Paused: true},
			typ:  "not_playing", data: map[string]any{},
			want: glStatus{Active: true, Paused: false},
		},
		{
			name: "active -> Active=true",
			typ:  "active", data: nil,
			want: glStatus{Active: true},
		},
		{
			name: "inactive -> Active=false",
			prev: glStatus{Active: true, VolPct: 30, HasVol: true},
			typ:  "inactive", data: nil,
			want: glStatus{Active: false, VolPct: 30, HasVol: true},
		},
		{
			name:    "metadata new uri -> changed=true, uri updated",
			prevURI: "spotify:track:OLD",
			typ:     "metadata", data: map[string]any{"uri": "spotify:track:NEW"},
			want: glStatus{}, wantURI: "spotify:track:NEW", wantChanged: true,
		},
		{
			name:    "metadata same uri -> no change",
			prevURI: "spotify:track:SAME",
			typ:     "metadata", data: map[string]any{"uri": "spotify:track:SAME"},
			want: glStatus{}, wantURI: "spotify:track:SAME", wantChanged: false,
		},
		{
			name:    "unknown type passes through unchanged",
			prev:    glStatus{Active: true, Paused: true, VolPct: 50, HasVol: true},
			prevURI: "u",
			typ:     "seek", data: map[string]any{"value": float64(1234)},
			want: glStatus{Active: true, Paused: true, VolPct: 50, HasVol: true}, wantURI: "u",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, gotURI, changed := applyGLEvent(c.prev, c.prevURI, c.typ, c.data)
			if got != c.want {
				t.Fatalf("status got %+v want %+v", got, c.want)
			}
			if gotURI != c.wantURI {
				t.Fatalf("uri got %q want %q", gotURI, c.wantURI)
			}
			if changed != c.wantChanged {
				t.Fatalf("changed got %v want %v", changed, c.wantChanged)
			}
		})
	}
}

// TestApplyEventLiveTrackSeq verifies the glLive wrapper bumps trackChangeSeq exactly on a uri change.
func TestApplyEventLiveTrackSeq(t *testing.T) {
	l := &glLive{}
	l.applyEvent("metadata", map[string]any{"uri": "a"})
	if l.trackSeq() != 1 {
		t.Fatalf("seq after first track got %d want 1", l.trackSeq())
	}
	l.applyEvent("metadata", map[string]any{"uri": "a"}) // same uri -> no bump
	if l.trackSeq() != 1 {
		t.Fatalf("seq after same track got %d want 1", l.trackSeq())
	}
	l.applyEvent("metadata", map[string]any{"uri": "b"})
	if l.trackSeq() != 2 {
		t.Fatalf("seq after new track got %d want 2", l.trackSeq())
	}
	l.applyEvent("volume", map[string]any{"value": float64(20)})
	if l.Get().VolPct != 20 || !l.Get().HasVol {
		t.Fatalf("volume not applied: %+v", l.Get())
	}
}

// TestWSReadTextFrame hand-builds an unmasked single TEXT frame (RFC 6455 §5.2) and verifies the
// parser returns its payload. Server→client frames are unmasked, so no mask key here.
func TestWSReadTextFrame(t *testing.T) {
	payload := []byte(`{"type":"volume","data":{"value":42}}`)
	frame := buildServerFrame(wsOpText, true, payload)
	c := &wsConn{br: bufio.NewReader(bytes.NewReader(frame))}
	got, err := c.ReadMessage()
	if err != nil {
		t.Fatalf("ReadMessage: %v", err)
	}
	if string(got) != string(payload) {
		t.Fatalf("got %q want %q", got, payload)
	}
}

// TestWSReadFragmentedFrame builds a TEXT(no FIN) + CONTINUATION(FIN) pair and verifies reassembly.
func TestWSReadFragmentedFrame(t *testing.T) {
	part1, part2 := []byte(`{"type":"meta`), []byte(`data":{}}`)
	var frame []byte
	frame = append(frame, buildServerFrame(wsOpText, false, part1)...)
	frame = append(frame, buildServerFrame(wsOpContinuation, true, part2)...)
	c := &wsConn{br: bufio.NewReader(bytes.NewReader(frame))}
	got, err := c.ReadMessage()
	if err != nil {
		t.Fatalf("ReadMessage: %v", err)
	}
	if string(got) != string(part1)+string(part2) {
		t.Fatalf("reassembly got %q", got)
	}
}

// TestWSRejectsMaskedServerFrame: a masked server→client frame is a protocol error (§5.1).
func TestWSRejectsMaskedServerFrame(t *testing.T) {
	// FIN+TEXT, MASK bit set, len 1, 4-byte mask, 1 masked byte.
	frame := []byte{0x81, 0x81, 0x00, 0x00, 0x00, 0x00, 0x41}
	c := &wsConn{br: bufio.NewReader(bytes.NewReader(frame))}
	if _, err := c.ReadMessage(); err == nil {
		t.Fatal("expected error on masked server frame")
	}
}

// buildServerFrame builds an UNMASKED server→client frame with the given opcode/FIN/payload
// (RFC 6455 §5.2), using the extended 16-bit length form when needed.
func buildServerFrame(opcode byte, fin bool, payload []byte) []byte {
	var b0 byte = opcode
	if fin {
		b0 |= 0x80
	}
	out := []byte{b0}
	n := len(payload)
	switch {
	case n <= 125:
		out = append(out, byte(n))
	case n <= 0xffff:
		out = append(out, 126)
		var ext [2]byte
		binary.BigEndian.PutUint16(ext[:], uint16(n))
		out = append(out, ext[:]...)
	default:
		out = append(out, 127)
		var ext [8]byte
		binary.BigEndian.PutUint64(ext[:], uint64(n))
		out = append(out, ext[:]...)
	}
	out = append(out, payload...)
	return out
}

// TestWSAcceptKey sanity-checks the handshake accept derivation against the RFC 6455 §1.3 example
// (key "dGhlIHNhbXBsZSBub25jZQ==" -> "s3pPLMBiTxaQ9kYGzzhZRbK+xOo="), the exact formula dialWebsocket
// verifies the server's response against.
func TestWSAcceptKey(t *testing.T) {
	sum := sha1.Sum([]byte("dGhlIHNhbXBsZSBub25jZQ==" + wsMagicGUID))
	got := base64.StdEncoding.EncodeToString(sum[:])
	if got != "s3pPLMBiTxaQ9kYGzzhZRbK+xOo=" {
		t.Fatalf("accept got %q", got)
	}
}

// A lifetime readiness event from a retired AppPlayer can arrive on the same
// current WebSocket. Exercise the current phase (not the retired-source veto).
func TestPC10HistoricalPlaybackReadyCannotOwnCurrentSession(t *testing.T) {
	cases := []struct {
		name                    string
		active, paused, stopped bool
	}{
		{"current_B_paused", true, true, false},
		{"idle", false, false, true},
		{"active", true, false, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			live := &glLive{}
			oldSource, stop := pc11Source(t, live)
			live.retirePhase(oldSource)
			current, ok := live.beginPhase(glSource{run: oldSource.run, stop: stop})
			if !ok {
				t.Fatal("fresh current phase refused")
			}
			binding := &aliasBinding{Incarnation: "player-B", Registry: "registry-B", RoomID: "room-B", Ready: true}
			initial := glStatus{Active: c.active, Paused: c.paused, Stopped: c.stopped, HasVol: true, VolPct: 65, SelAlias: 2, AliasBinding: binding}
			if !live.acceptStatus(pc11Request(t, live, current), initial) {
				t.Fatal("current B status refused")
			}
			live.applySourceEvent(current, "metadata", map[string]any{"uri": "spotify:track:B"})
			held := pc11Request(t, live, current)
			before := live.Get()
			beforeBinding := *before.AliasBinding
			beforeWire, beforeAlias, beforeDispatch := live.wireRevision, live.aliasRevision, live.routeDispatchSeq
			beforeTrack, beforeURI := live.trackChangeSeq, live.trackURI
			beforeNext, beforeAccepted := live.nextRequest, live.acceptedRequest
			beforeRun, beforePhase, beforeRetired := live.runEpoch, live.phaseEpoch, live.sourceRetired
			beforeManual, beforeIncarnation := live.manualOrdinal, live.manualIncarnation
			beforeAction, beforeObservation := live.manualAction, live.manualObservation
			// The old event carries no producer identity. Deliberately dispatch it through
			// the current source, just as the retained same-server socket reader does.
			live.applySourceEvent(current, "playback_ready", map[string]any{"uri": "spotify:track:A", "id": 1, "value": 5})
			if live.Get() != before || *live.Get().AliasBinding != beforeBinding ||
				live.wireRevision != beforeWire || live.aliasRevision != beforeAlias || live.routeDispatchSeq != beforeDispatch ||
				live.trackChangeSeq != beforeTrack || live.trackURI != beforeURI ||
				live.nextRequest != beforeNext || live.acceptedRequest != beforeAccepted ||
				live.runEpoch != beforeRun || live.phaseEpoch != beforePhase || live.sourceRetired != beforeRetired ||
				live.manualOrdinal != beforeManual || live.manualIncarnation != beforeIncarnation ||
				live.manualAction != beforeAction || live.manualObservation != beforeObservation {
				t.Fatal("historical ready changed current state or an owner ledger")
			}
			// Transport must actually be accepted, not merely an unrelated volume group.
			refreshed := glStatus{Active: !c.active, Paused: !c.paused, Stopped: !c.stopped, HasVol: true, VolPct: 42, SelAlias: 2, AliasBinding: binding}
			if !live.acceptStatus(held, refreshed) || live.Get() != refreshed || live.acceptedRequest != held.sequence {
				t.Fatalf("ready vetoed current status groups: got %+v want %+v", live.Get(), refreshed)
			}
			if live.wireRevision != beforeWire || live.aliasRevision != beforeAlias || live.trackChangeSeq != beforeTrack || live.trackURI != beforeURI {
				t.Fatal("status refresh changed unrelated alias intent, wire revisions or track")
			}
		})
	}
}

func TestPC10PlaybackReadyPureMappingIsOnlyHistorical(t *testing.T) {
	for _, active := range []bool{false, true} {
		binding := &aliasBinding{Incarnation: "player-B", Registry: "registry-B", RoomID: "room-B", Ready: true}
		before := glStatus{Active: active, Paused: true, Stopped: true, HasVol: true, VolPct: 65, SelAlias: 2, AliasBinding: binding}
		got, uri, changed := applyGLEvent(before, "spotify:track:B", "playback_ready", map[string]any{"id": 1, "value": 5, "uri": "spotify:track:A"})
		if got != before || uri != "spotify:track:B" || changed {
			t.Fatalf("historical ready mutated pure mapping: %+v %q %v", got, uri, changed)
		}
	}
}
