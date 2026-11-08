package main

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	playbackRevisionHeader = "X-Podconnect-Playback-Revision"
	playbackRevisionWait   = 20 * time.Second
	playbackRevisionCap    = 8
)

var playbackRevisionSlots = make(chan struct{}, playbackRevisionCap)
var playbackRevisionKey = sync.OnceValues(func() ([32]byte, error) {
	var key [32]byte
	_, err := rand.Read(key[:])
	return key, err
})

type playbackRevisionReply struct {
	Schema  string `json:"schema"`
	Cursor  string `json:"cursor"`
	Changed bool   `json:"changed"`
}

type playbackRevisionCursor struct {
	scope string
	seq   uint64
}

func parsePlaybackRevisionCursor(value string) (playbackRevisionCursor, bool) {
	if value == "seed" {
		return playbackRevisionCursor{}, true
	}
	scope, raw, found := strings.Cut(value, ":")
	if !found || !queueObservationPattern.MatchString(scope) || !transportDecimal.MatchString(raw) {
		return playbackRevisionCursor{}, false
	}
	seq, err := strconv.ParseUint(raw, 10, 64)
	return playbackRevisionCursor{scope, seq}, err == nil
}

type playbackRevisionItem struct {
	uri          string
	paused       bool
	seekPosition float64
	seekDuration float64
	seekAt       time.Time
}

func validPlaybackRevisionURI(uri string) bool {
	return contentTrackURI.MatchString(uri) || contentEpisodeURI.MatchString(uri)
}

// Called after the established wire-source and transport admission. These facts
// are hints to read Spotify again; they never publish metadata or change routing.
func (l *glLive) observePlaybackRevisionEventLocked(typ string, data map[string]any) {
	uri, _ := data["uri"].(string)
	item := &l.playbackItem
	switch typ {
	case "metadata":
		if !validPlaybackRevisionURI(uri) || uri == item.uri {
			return
		}
		*item = playbackRevisionItem{uri: uri, paused: l.st.Paused}
	case "playing", "paused":
		if _, validResume := data["resume"].(bool); typ == "playing" && !validResume {
			return
		}
		paused := typ == "paused"
		if uri == "" || uri != item.uri || !l.st.Active || l.st.Stopped || paused == item.paused {
			return
		}
		item.paused = paused
		item.seekAt = time.Time{}
	case "seek":
		position, hasPosition := numField(data, "position")
		duration, hasDuration := numField(data, "duration")
		if uri == "" || uri != item.uri || !l.st.Active || l.st.Stopped ||
			!hasPosition || !hasDuration || !(0 <= position && position <= duration && duration > 0 && duration < 1<<53) ||
			position != math.Trunc(position) || duration != math.Trunc(duration) {
			return
		}
		if position == item.seekPosition && duration == item.seekDuration && time.Since(item.seekAt) < 250*time.Millisecond {
			return // A duplicate frame is quiet; a later explicit seek may use the same target.
		}
		item.seekPosition, item.seekDuration, item.seekAt = position, duration, time.Now()
	case "stopped", "inactive":
		*item = playbackRevisionItem{}
		return
	default:
		return
	}
	l.playbackRevision++
	l.signalPlaybackRevisionLocked()
}

func (l *glLive) signalPlaybackRevisionLocked() {
	if l.playbackRevisionWake != nil {
		close(l.playbackRevisionWake)
		l.playbackRevisionWake = nil
	}
}

type playbackRevisionSnapshot struct {
	status glStatus
	intent glTransportIntent
	seq    uint64
	wake   <-chan struct{}
}

func (l *glLive) playbackRevisionSnapshot() (playbackRevisionSnapshot, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.observeStopLocked()
	if l.sourceRetired || l.runEpoch == 0 || l.phaseEpoch == 0 || !l.st.AliasBinding.valid() {
		return playbackRevisionSnapshot{}, false
	}
	if l.playbackRevisionWake == nil {
		l.playbackRevisionWake = make(chan struct{})
	}
	intent := glTransportIntent{l.runEpoch, l.phaseEpoch, l.aliasRevision, l.transportRevision, l.st.SelAlias}
	return playbackRevisionSnapshot{l.st, intent, l.playbackRevision, l.playbackRevisionWake}, true
}

func samePlaybackRevisionSource(a, b glTransportIntent) bool {
	return a.run == b.run && a.phase == b.phase && a.aliasRevision == b.aliasRevision && a.alias == b.alias
}

func currentPlaybackRevision(ctx context.Context, target, account string) (playbackRevisionCursor, <-chan struct{}, <-chan struct{}, bool) {
	if ctx.Err() != nil || mgr == nil || store == nil {
		return playbackRevisionCursor{}, nil, nil, false
	}
	primary := primaryRoom()
	if configuredEngineID(primary) != target {
		return playbackRevisionCursor{}, nil, nil, false
	}
	runtime := mgr.runtime(primary.ID)
	if runtime == nil {
		return playbackRevisionCursor{}, nil, nil, false
	}
	for range 2 {
		before, ok := runtime.live.playbackRevisionSnapshot()
		if !ok {
			return playbackRevisionCursor{}, nil, nil, false
		}
		selected := roomByID(before.status.AliasBinding.RoomID)
		if selected == nil || selected.HomepodID == "" {
			return playbackRevisionCursor{}, nil, nil, false
		}
		claim, native, output, err := readRoomTransport(ctx, roomTransportScope{target, selected.ID, selected.HomepodID, account})
		after, ok := runtime.live.playbackRevisionSnapshot()
		if !ok || ctx.Err() != nil || !samePlaybackRevisionSource(before.intent, after.intent) {
			return playbackRevisionCursor{}, nil, nil, false
		}
		if after.seq != before.seq {
			// A native event can supersede a held probe. Only this same source may
			// receive one fresh owner/route check; a different owner retires it.
			continue
		}
		if err != nil || claim.RT != runtime || claim.Intent != before.intent || after.intent != before.intent {
			return playbackRevisionCursor{}, nil, nil, false
		}
		key, err := playbackRevisionKey()
		if err != nil {
			return playbackRevisionCursor{}, nil, nil, false
		}
		// The read above joins the exact current OwnTone route, including its input.
		// A new item may change its input and route revisions within the same output
		// session, so only stable route custody belongs in the cursor scope.
		route := output.Identity
		material, _ := json.Marshal([]any{
			before.intent.run, before.intent.phase, before.intent.aliasRevision, native.Identity,
			route.Process, route.Device, route.Incarnation, route.Session, route.Selection, route.InputDigest,
		})
		digest := hmac.New(sha256.New, key[:])
		_, _ = digest.Write(material)
		scope := hex.EncodeToString(digest.Sum(nil)[:16])
		return playbackRevisionCursor{scope, before.seq}, before.wake, runtime.stop, true
	}
	return playbackRevisionCursor{}, nil, nil, false
}

func awaitPlaybackRevision(ctx context.Context, target, account string, previous playbackRevisionCursor) (int, playbackRevisionReply) {
	for {
		current, wake, stopped, ok := currentPlaybackRevision(ctx, target, account)
		if !ok {
			return http.StatusConflict, playbackRevisionReply{}
		}
		if previous != current {
			return http.StatusOK, playbackRevisionReply{
				"podconnect.playback_revision.v1",
				current.scope + ":" + strconv.FormatUint(current.seq, 10),
				previous.scope == current.scope && previous.seq < current.seq,
			}
		}
		select {
		case <-ctx.Done():
			return http.StatusNoContent, playbackRevisionReply{}
		case <-stopped:
			return http.StatusConflict, playbackRevisionReply{}
		case <-wake:
			// Wake is a hint. Prove the live owner and route again before replying.
		}
	}
}

// The opt-in stays within the existing configured Speakers read boundary.
// No account, playback data, or authority is returned from this route.
func playbackRevisionRequest(w http.ResponseWriter, r *http.Request) bool {
	headers := r.Header.Values(playbackRevisionHeader)
	if len(headers) == 0 {
		return false
	}
	servePlaybackRevision(w, r, headers, playbackRevisionWait)
	return true
}

func servePlaybackRevision(w http.ResponseWriter, r *http.Request, headers []string, wait time.Duration) {
	w.Header().Set("Cache-Control", "no-store")
	query, err := url.ParseQuery(r.URL.RawQuery)
	target, account := query.Get("target_id"), query.Get("account_user_id")
	if r.Method != http.MethodGet || len(headers) != 1 || err != nil || len(query) != 2 ||
		len(query["target_id"]) != 1 || len(query["account_user_id"]) != 1 ||
		!engineIDPattern.MatchString(target) || len(account) > 160 || canonicalUsernameDigest(account) == "" {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	previous, ok := parsePlaybackRevisionCursor(headers[0])
	if !ok {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	select {
	case playbackRevisionSlots <- struct{}{}:
	default:
		w.WriteHeader(http.StatusTooManyRequests)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), wait)
	defer cancel()
	type outcome struct {
		code  int
		reply playbackRevisionReply
	}
	done := make(chan outcome, 1)
	// Keep the slot until the existing join finishes even if its bounded legacy
	// output preflight is still unwinding after the caller leaves.
	go func() {
		defer func() { <-playbackRevisionSlots }()
		code, reply := awaitPlaybackRevision(ctx, target, account, previous)
		done <- outcome{code, reply}
	}()
	select {
	case result := <-done:
		if r.Context().Err() == nil {
			if result.code == http.StatusOK {
				writeJSON(w, result.reply)
			} else {
				w.WriteHeader(result.code)
			}
		}
	case <-ctx.Done():
		if r.Context().Err() == nil {
			w.WriteHeader(http.StatusNoContent)
		}
	}
}
