// Supervisor: the manager forks & supervises each room's two child processes (go-librespot +
// OwnTone) via os/exec — replacing the s6 services go-librespot/owntone/gl-watchdog/select-homepod.
//
// Per room a supervise goroutine:
//   - (re)spawns either child if it exits (mirrors s6 longrun restart)
//   - HTTP-polls go-librespot /status and restarts it if unresponsive ~90s (the old gl-watchdog hang
//     guard for issues #300/#240, where the process lives but the Spotify session is stuck)
//   - runs the per-room HomePod selection on a slow tick (the old select-homepod loop)
//
// Children get their own process GROUP (Setpgid) so teardown kills the whole group, and Pdeathsig
// SIGKILL (linux) so they die with the manager rather than orphaning. On manager startup we RECONCILE
// every room in rooms.json by (re)spawning — the supervise loop owns the children regardless of who
// started them.
package main

import (
	"bufio"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
)

// roomRuntime is the live process state for one room.
type roomRuntime struct {
	room        *Room
	stop        chan struct{}
	done        chan struct{}
	stopOnce    sync.Once
	lifecycleMu sync.Mutex
	replacing   chan struct{}
	stopContext context.Context
	glDone      chan struct{}
	otDone      chan struct{}

	mu    sync.Mutex
	glCmd *exec.Cmd
	otCmd *exec.Cmd

	freshNativePending bool      // a fresh Native process was requested; respawn without the 2 s backoff
	nativeHealing      bool      // one fresh-Native replacement at a time
	lastNativeHeal     time.Time // wedge self-heal rate limit

	tonePlaying boolFlag  // per-room test-tone gate (pauses that room's bridge)
	live        glLive    // Wave 3: latest go-librespot state, pushed by runGLEvents (poll fallback)
	att         attention // Wave 4: external duck/attention state (voice gatekeeper)
}

// roomManager owns every room's runtime, guarded by a mutex.
type roomManager struct {
	mu       sync.Mutex
	runtimes map[string]*roomRuntime
	store    *roomStore
	stopping bool
}

func newRoomManager(store *roomStore) *roomManager {
	return &roomManager{runtimes: map[string]*roomRuntime{}, store: store}
}

// runtime returns a room's runtime (or nil), for the bridge/HTTP layer to read its tone gate.
func (m *roomManager) runtime(id string) *roomRuntime {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.runtimes[id]
}

// ensureRunning renders the room's configs, makes its pipes, and starts its supervise goroutine if
// not already running. Idempotent — calling it for an already-supervised room is a no-op.
func (m *roomManager) ensureRunning(r *Room) {
	m.mu.Lock()
	if _, ok := m.runtimes[r.ID]; ok || m.stopping {
		m.mu.Unlock()
		return
	}
	rt := &roomRuntime{room: r, stop: make(chan struct{}), done: make(chan struct{})}
	rt.att.loadCustody(filepath.Join(dataDir, "rooms", r.ID, "attention-custody.json"), r.ID)
	m.runtimes[r.ID] = rt
	m.mu.Unlock()

	if err := renderGLConfig(r); err != nil {
		log.Printf("rooms[%s]: render go-librespot config: %v", r.ID, err)
	}
	if err := renderOTConfig(r); err != nil {
		log.Printf("rooms[%s]: render owntone config: %v", r.ID, err)
	}
	if err := ensurePipes(r); err != nil {
		log.Printf("rooms[%s]: ensure pipes: %v", r.ID, err)
	}
	chownOwnDir(r)
	go rt.supervise()
}

// removeRoom tears a room down: kill its exact runtime child groups, stop the supervise
// goroutine, and drop it from rooms.json. Safe to call for an unknown id (just updates the store).
func (m *roomManager) removeRoom(id string) error {
	_, err := m.store.removeRoom(id)
	if err != nil {
		return err
	}
	// Existing runtime teardown owns retirement; never send an unbound output PUT.

	m.mu.Lock()
	rt := m.runtimes[id]
	m.mu.Unlock()

	if rt != nil {
		ctx, cancel := context.WithTimeout(context.Background(), roomShutdownTimeout)
		defer cancel()
		rt.requestStop(ctx)
		<-rt.done
	}
	m.mu.Lock()
	if m.runtimes[id] == rt {
		delete(m.runtimes, id)
	}
	m.mu.Unlock()
	return nil
}

// supervise is the per-room loop: keep both children alive, watchdog go-librespot, and run HomePod
// selection. It exits when stop is closed (room removal or shutdown).
func (rt *roomRuntime) supervise() {
	defer close(rt.done)
	r := rt.room

	go rt.keepAlive("go-librespot", &rt.glCmd, func() *exec.Cmd {
		return exec.Command("go-librespot", "--config_dir", r.ConfigDir)
	})
	go rt.keepAlive("owntone", &rt.otCmd, func() *exec.Cmd {
		return exec.Command("owntone", "-f", "-c", r.OwnConf)
	})

	// go-librespot hang watchdog (folded gl-watchdog): poll /status; restart after ~90s unresponsive.
	healthURL := r.Librespot + "/status"
	warmup := time.NewTimer(90 * time.Second)
	defer warmup.Stop()
	healthTick := time.NewTicker(30 * time.Second)
	defer healthTick.Stop()
	warmedUp := false
	fails := 0

	// Output selection is owned by the primary bridge's alias-router (routeAliasOutput): on startup the
	// engine reports its default selected alias, which the router maps to the right HomePod. (The old
	// per-room HomePod pin via selectHomePod is not used in alias mode — it would fight the router.)

	for {
		select {
		case <-rt.stop:
			rt.killAll()
			return
		case <-warmup.C:
			warmedUp = true
		case <-healthTick.C:
			if !warmedUp {
				continue
			}
			health := probeGLHealth(&http.Client{Timeout: 5 * time.Second}, healthURL)
			if health.kind == "ok" {
				fails = 0
				continue
			}
			fails++
			log.Printf("rooms[%s]: go-librespot health check failed (%d/3) class=%s http_status=%d",
				r.ID, fails, health.kind, health.status)
			if fails >= 3 {
				log.Printf("rooms[%s]: go-librespot unresponsive — restarting it", r.ID)
				rt.restartGL()
				fails = 0
				warmup.Reset(60 * time.Second)
				warmedUp = false
			}
		}
	}
}

// keepAlive spawns a child via make(), waits for it, and respawns on exit until stop is closed. It
// records the running *exec.Cmd in slot (under rt.mu) so restartGL/killAll can signal it.
func (rt *roomRuntime) keepAlive(label string, slot **exec.Cmd, build func() *exec.Cmd) {
	for {
		select {
		case <-rt.stop:
			return
		default:
		}
		cmd := build()
		rt.mu.Lock()
		select {
		case <-rt.stop:
			rt.mu.Unlock()
			return
		default:
		}
		if replacing := rt.replacing; replacing != nil {
			rt.mu.Unlock()
			select {
			case <-rt.stop:
				return
			case <-replacing:
			}
			continue
		}
		setProcGroup(cmd) // own process group + die-with-parent (linux)
		rt.pipeOutput(label, cmd)
		if err := cmd.Start(); err != nil {
			rt.mu.Unlock()
			log.Printf("rooms[%s]: start %s: %v", rt.room.ID, label, err)
			if rt.sleepOrStop(3 * time.Second) {
				return
			}
			continue
		}
		joined := make(chan struct{})
		*slot = cmd
		if slot == &rt.glCmd {
			rt.glDone = joined
		} else {
			rt.otDone = joined
		}
		rt.mu.Unlock()
		log.Printf("rooms[%s]: %s started (pid %d)", rt.room.ID, label, cmd.Process.Pid)
		err := cmd.Wait()
		rt.mu.Lock()
		*slot = nil
		close(joined)
		rt.mu.Unlock()
		select {
		case <-rt.stop:
			return
		default:
		}
		log.Printf("rooms[%s]: %s exited (%v) — respawning", rt.room.ID, label, err)
		backoff := 2 * time.Second
		rt.mu.Lock()
		if slot == &rt.glCmd && rt.freshNativePending {
			// A requested fresh Native is wanted now: Spotify Connect comes back at once.
			rt.freshNativePending = false
			backoff = 50 * time.Millisecond
		}
		rt.mu.Unlock()
		if rt.sleepOrStop(backoff) {
			return
		}
	}
}

// sleepOrStop sleeps d, returning true early if stop fires.
func (rt *roomRuntime) sleepOrStop(d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-rt.stop:
		return true
	case <-t.C:
		return false
	}
}

// roomShutdownTimeout covers the existing Native retirement prepare12s,
// complete12s/result2s and local writer joins. The outer s6/HA caps are longer.
const roomShutdownTimeout = 55 * time.Second

func (rt *roomRuntime) requestStop(ctx context.Context) {
	rt.stopOnce.Do(func() { rt.mu.Lock(); rt.stopContext = ctx; close(rt.stop); rt.mu.Unlock() })
}

// shutdownAll stops admission/respawn before taking any child snapshot. Each
// runtime owns its existing cmd.Wait; shutdown only waits for that owner's join.
func (m *roomManager) shutdownAll(ctx context.Context) {
	m.mu.Lock()
	m.stopping = true
	runtimes := make([]*roomRuntime, 0, len(m.runtimes))
	for _, rt := range m.runtimes {
		rt.requestStop(ctx)
		runtimes = append(runtimes, rt)
	}
	m.mu.Unlock()
	for _, rt := range runtimes {
		<-rt.done
	}
}

// stopChild never calls Wait itself: keepAlive remains its sole owner. A forced
// exit is explicitly UNKNOWN; process join does not attest durable retirement.
func (rt *roomRuntime) stopChild(ctx context.Context, cmd *exec.Cmd, joined <-chan struct{}, label string) bool {
	if cmd == nil || cmd.Process == nil {
		return true
	}
	if joined == nil {
		log.Printf("rooms[%s]: %s has no Wait owner; retirement UNKNOWN", rt.room.ID, label)
		return false
	}
	select {
	case <-joined:
		return true
	default:
	}
	// These two supervised daemons do not fork helpers. Use Go's captured
	// process handle rather than resolving a numeric process group after reap.
	_ = cmd.Process.Signal(syscall.SIGTERM)
	select {
	case <-joined:
		return true
	case <-ctx.Done():
		select {
		case <-joined:
			return true
		default:
		}
		log.Printf("rooms[%s]: %s shutdown deadline; forcing captured child, retirement UNKNOWN", rt.room.ID, label)
		_ = cmd.Process.Kill()
		<-joined
		return false
	}
}

// replaceChildren serializes current-child capture, retirement and replacement.
// A rescan replaces the pair: killing C alone could strand a live Native journal.
func (rt *roomRuntime) replaceChildren(pair bool) bool {
	ctx, cancel := context.WithTimeout(context.Background(), roomShutdownTimeout)
	defer cancel()
	rt.mu.Lock()
	expectedNative, expectedRenderer := rt.glCmd, rt.otCmd
	rt.mu.Unlock()
	rt.lifecycleMu.Lock()
	defer rt.lifecycleMu.Unlock()
	rt.mu.Lock()
	if rt.glCmd != expectedNative || (pair && rt.otCmd != expectedRenderer) {
		rt.mu.Unlock()
		return false
	}
	select {
	case <-rt.stop:
		rt.mu.Unlock()
		return false
	default:
	}
	barrier := make(chan struct{})
	rt.replacing = barrier
	native, nativeDone, renderer, rendererDone := rt.glCmd, rt.glDone, rt.otCmd, rt.otDone
	rt.mu.Unlock()
	defer func() { rt.mu.Lock(); rt.replacing = nil; close(barrier); rt.mu.Unlock() }()
	joined := rt.stopChild(ctx, native, nativeDone, "Native")
	if pair {
		joined = rt.stopChild(ctx, renderer, rendererDone, "C") && joined
	}
	return joined
}

func (rt *roomRuntime) restartGL() {
	if !rt.replaceChildren(false) {
		log.Printf("rooms[%s]: Native replacement incomplete or superseded; retirement UNKNOWN", rt.room.ID)
	}
}

// freshNative replaces only the Spotify engine (Native) with a fresh process.
// The Spotify OwnTone treats a new Native process as "old writer retired", so
// every Spotify custody/retirement record of the old process is gone at once:
// the next transfer from the phone starts clean. It never touches OwnTone or
// the radio, and it never starts audio (a fresh Native is idle).
func (rt *roomRuntime) freshNative(reason string) {
	rt.mu.Lock()
	if rt.nativeHealing {
		rt.mu.Unlock()
		return
	}
	rt.nativeHealing = true
	rt.freshNativePending = true
	rt.mu.Unlock()
	log.Printf("rooms[%s]: fresh Spotify engine (%s)", rt.room.ID, reason)
	go func() {
		defer func() { rt.mu.Lock(); rt.nativeHealing = false; rt.mu.Unlock() }()
		rt.restartNative()
	}()
}

// restartNative is replaced in tests.
var restartNativeHook func(rt *roomRuntime)

func (rt *roomRuntime) restartNative() {
	if restartNativeHook != nil {
		restartNativeHook(rt)
		return
	}
	rt.restartGL()
}

// nativeWedgeLine recognises Native's fixed, content-free log lines that mean
// its process-wide Spotify custody is stuck ("unknown"): from then on every
// transfer, play and seek would be refused until the process is replaced.
func nativeWedgeLine(line string) bool {
	return strings.Contains(line, "custody_unknown=true") ||
		strings.Contains(line, "output admission custody_unknown") ||
		strings.Contains(line, "retirement_prepare outcome=request_failed") ||
		strings.Contains(line, "retirement_prepare outcome=receipt_rejected") ||
		strings.Contains(line, "retirement_prepare outcome=skipped reason=custody_uncertain")
}

const nativeHealInterval = 20 * time.Second

// nativeHealPoll is how often a pending self-heal re-checks that Spotify audio stopped.
var nativeHealPoll = 500 * time.Millisecond

// healNativeWedge is Spotify-always-wins insurance: a stuck Native is replaced
// as soon as it plays nothing (current audio is never cut), at most once per
// nativeHealInterval.
func (rt *roomRuntime) healNativeWedge() {
	rt.mu.Lock()
	if rt.nativeHealing || time.Since(rt.lastNativeHeal) < nativeHealInterval {
		rt.mu.Unlock()
		return
	}
	rt.lastNativeHeal = time.Now()
	rt.mu.Unlock()
	go func() {
		for {
			st := rt.live.Get()
			if !(st.Active && !st.Paused && !st.Stopped) {
				break
			}
			if rt.sleepOrStop(nativeHealPoll) {
				return
			}
		}
		rt.freshNative("Spotify custody was stuck")
	}()
}

func (rt *roomRuntime) restartOT() bool { return rt.replaceChildren(true) }

// killAll is the ordered teardown used by the runtime stop owner. Keep C alive
// through Native's actual process join so its completed writer can retire there.
func (rt *roomRuntime) killAll() {
	rt.lifecycleMu.Lock()
	defer rt.lifecycleMu.Unlock()
	rt.mu.Lock()
	ctx := rt.stopContext
	native, nativeDone, renderer, rendererDone := rt.glCmd, rt.glDone, rt.otCmd, rt.otDone
	rt.mu.Unlock()
	if ctx == nil {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(context.Background(), roomShutdownTimeout)
		defer cancel()
	}
	rt.stopChild(ctx, native, nativeDone, "Native")
	rt.stopChild(ctx, renderer, rendererDone, "C")
}

// pipeOutput routes a child's stdout/stderr to the manager log with a room/child prefix.
func (rt *roomRuntime) pipeOutput(label string, cmd *exec.Cmd) {
	prefix := "rooms[" + rt.room.ID + "/" + label + "] "
	stdout, err := cmd.StdoutPipe()
	var onLine func(string)
	if label == "go-librespot" {
		onLine = func(line string) {
			if nativeWedgeLine(line) {
				rt.healNativeWedge()
			}
		}
	}
	if err == nil {
		go logLines(prefix, stdout, onLine)
	}
	stderr, err := cmd.StderrPipe()
	if err == nil {
		go logLines(prefix, stderr, onLine)
	}
}

func logLines(prefix string, rc io.ReadCloser, onLine func(string)) {
	sc := bufio.NewScanner(rc)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		log.Printf("%s%s", prefix, sc.Text())
		if onLine != nil {
			onLine(sc.Text())
		}
	}
}

type glHealthResult struct {
	kind   string
	status int
}

func probeGLHealth(client *http.Client, url string) glHealthResult {
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return glHealthResult{kind: "transport"}
	}
	req.Header.Set("X-PodConnect-Health-Probe", "1")
	resp, err := client.Do(req)
	if err != nil {
		var timeout net.Error
		if errors.As(err, &timeout) && timeout.Timeout() {
			return glHealthResult{kind: "timeout"}
		}
		return glHealthResult{kind: "transport"}
	}
	resp.Body.Close()
	// An authenticated idle player is 200; an unpaired engine is 204.
	if resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusNoContent {
		return glHealthResult{kind: "ok", status: resp.StatusCode}
	}
	return glHealthResult{kind: "http", status: resp.StatusCode}
}

// boolFlag is a tiny mutex-guarded bool — the per-room test-tone gate (replaces the global
// atomic.Bool so each room's bridge is paused independently while ITS tone plays).
type boolFlag struct {
	mu sync.Mutex
	v  bool
}

func (b *boolFlag) Store(v bool) { b.mu.Lock(); b.v = v; b.mu.Unlock() }
func (b *boolFlag) Load() bool   { b.mu.Lock(); defer b.mu.Unlock(); return b.v }
