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
	"io"
	"net/http"
	"os/exec"
	"sync"
	"time"
)

// roomRuntime is the live process state for one room.
type roomRuntime struct {
	room *Room
	stop chan struct{}
	done chan struct{}

	mu    sync.Mutex
	glCmd *exec.Cmd
	otCmd *exec.Cmd

	tonePlaying boolFlag // per-room test-tone gate (pauses that room's bridge)
}

// roomManager owns every room's runtime, guarded by a mutex.
type roomManager struct {
	mu       sync.Mutex
	runtimes map[string]*roomRuntime
	store    *roomStore
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
	if _, ok := m.runtimes[r.ID]; ok {
		m.mu.Unlock()
		return
	}
	rt := &roomRuntime{room: r, stop: make(chan struct{}), done: make(chan struct{})}
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

// removeRoom tears a room down: deselect its HomePod, kill both child groups, stop the supervise
// goroutine, and drop it from rooms.json. Safe to call for an unknown id (just updates the store).
func (m *roomManager) removeRoom(id string) error {
	rm, err := m.store.removeRoom(id)
	if err != nil {
		return err
	}
	releaseHomePod(rm) // deselect so the HomePod is free for other senders

	m.mu.Lock()
	rt := m.runtimes[id]
	delete(m.runtimes, id)
	m.mu.Unlock()

	if rt != nil {
		close(rt.stop)
		select {
		case <-rt.done:
		case <-time.After(8 * time.Second):
			log.Printf("rooms[%s]: teardown timed out, killing anyway", id)
		}
		rt.killAll()
	}
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
	selectTick := time.NewTicker(10 * time.Second)
	defer selectTick.Stop()
	warmedUp := false
	fails := 0

	// Initial selection attempt shortly after start (OwnTone needs a moment to discover outputs).
	time.AfterFunc(8*time.Second, func() {
		select {
		case <-rt.stop:
		default:
			selectHomePod(r)
		}
	})

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
			if glHealthy(healthURL) {
				fails = 0
				continue
			}
			fails++
			log.Printf("rooms[%s]: go-librespot health check failed (%d/3)", r.ID, fails)
			if fails >= 3 {
				log.Printf("rooms[%s]: go-librespot unresponsive — restarting it", r.ID)
				rt.restartGL()
				fails = 0
				warmup.Reset(60 * time.Second)
				warmedUp = false
			}
		case <-selectTick.C:
			selectHomePod(r)
		}
	}
}

// keepAlive spawns a child via make(), waits for it, and respawns on exit until stop is closed. It
// records the running *exec.Cmd in slot (under rt.mu) so restartGL/killAll can signal it.
func (rt *roomRuntime) keepAlive(label string, slot **exec.Cmd, make func() *exec.Cmd) {
	for {
		select {
		case <-rt.stop:
			return
		default:
		}
		cmd := make()
		setProcGroup(cmd) // own process group + die-with-parent (linux)
		rt.pipeOutput(label, cmd)
		if err := cmd.Start(); err != nil {
			log.Printf("rooms[%s]: start %s: %v", rt.room.ID, label, err)
			if rt.sleepOrStop(3 * time.Second) {
				return
			}
			continue
		}
		rt.mu.Lock()
		*slot = cmd
		rt.mu.Unlock()
		log.Printf("rooms[%s]: %s started (pid %d)", rt.room.ID, label, cmd.Process.Pid)
		err := cmd.Wait()
		rt.mu.Lock()
		*slot = nil
		rt.mu.Unlock()
		select {
		case <-rt.stop:
			return
		default:
		}
		log.Printf("rooms[%s]: %s exited (%v) — respawning", rt.room.ID, label, err)
		if rt.sleepOrStop(2 * time.Second) {
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

// restartGL kills the running go-librespot child's group; keepAlive respawns it.
func (rt *roomRuntime) restartGL() {
	rt.mu.Lock()
	cmd := rt.glCmd
	rt.mu.Unlock()
	if cmd != nil && cmd.Process != nil {
		killGroup(cmd)
	}
}

// killAll kills both children's process groups (teardown).
func (rt *roomRuntime) killAll() {
	rt.mu.Lock()
	cmds := []*exec.Cmd{rt.glCmd, rt.otCmd}
	rt.mu.Unlock()
	for _, c := range cmds {
		if c != nil && c.Process != nil {
			killGroup(c)
		}
	}
}

// pipeOutput routes a child's stdout/stderr to the manager log with a room/child prefix.
func (rt *roomRuntime) pipeOutput(label string, cmd *exec.Cmd) {
	prefix := "rooms[" + rt.room.ID + "/" + label + "] "
	stdout, err := cmd.StdoutPipe()
	if err == nil {
		go logLines(prefix, stdout)
	}
	stderr, err := cmd.StderrPipe()
	if err == nil {
		go logLines(prefix, stderr)
	}
}

func logLines(prefix string, rc io.ReadCloser) {
	sc := bufio.NewScanner(rc)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		log.Printf("%s%s", prefix, sc.Text())
	}
}

// glHealthy reports whether go-librespot's /status answers within a short timeout.
func glHealthy(url string) bool {
	cl := &http.Client{Timeout: 5 * time.Second}
	resp, err := cl.Get(url)
	if err != nil {
		return false
	}
	resp.Body.Close()
	return resp.StatusCode < 500
}

// boolFlag is a tiny mutex-guarded bool — the per-room test-tone gate (replaces the global
// atomic.Bool so each room's bridge is paused independently while ITS tone plays).
type boolFlag struct {
	mu sync.Mutex
	v  bool
}

func (b *boolFlag) Store(v bool) { b.mu.Lock(); b.v = v; b.mu.Unlock() }
func (b *boolFlag) Load() bool   { b.mu.Lock(); defer b.mu.Unlock(); return b.v }
