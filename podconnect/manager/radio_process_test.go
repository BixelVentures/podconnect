package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

// Test custody is deliberately not a production resource-limit proof.
type radioTestContainment struct {
	mu                       sync.Mutex
	cmd                      *exec.Cmd
	kills, closes            int
	failConfigure, failClose bool
}

func (c *radioTestContainment) Configure(cmd *exec.Cmd) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.failConfigure {
		return errRadioUnavailable
	}
	c.cmd = cmd
	return nil
}
func (c *radioTestContainment) Kill() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.kills++
	if c.cmd != nil && c.cmd.Process != nil {
		e := c.cmd.Process.Kill()
		if !errors.Is(e, os.ErrProcessDone) {
			return e
		}
	}
	return nil
}
func (c *radioTestContainment) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.closes++
	if c.failClose {
		return errRadioUnknown
	}
	return nil
}
func inertRadioSender(t *testing.T, c *radioTestContainment) *radioSender {
	t.Helper()
	s, e := newRadioSender(t.TempDir(), 4691, 4692, nil)
	if e != nil {
		t.Fatal(e)
	}
	s.prepareOwnership = func(string) error { return nil }
	s.newContainment = func() (radioProcessContainment, error) { return c, nil }
	s.makeCommand = func(ctx context.Context, _ string) *exec.Cmd {
		return exec.CommandContext(ctx, os.Args[0], "-test.run=^TestRadioInertChild$", "--radio-inert-child")
	}
	return s
}
func TestRadioInertChild(t *testing.T) {
	if role := os.Getenv("RADIO_TEST_GROUP_ROLE"); role == "guardian" {
		read := os.NewFile(3, "live")
		kill := os.NewFile(4, "kill")
		ready := os.NewFile(5, "ready")
		b, _ := io.ReadAll(kill)
		pid, e := strconv.Atoi(string(b))
		if e != nil || pid <= 0 {
			os.Exit(3)
		}
		_, _ = ready.Write([]byte{1})
		ready.Close()
		e = radioGuardianWatch(read, func() error {
			e := syscall.Kill(-pid, syscall.SIGKILL)
			if e == syscall.ESRCH {
				return nil
			}
			return e
		})
		if e != nil {
			os.Exit(4)
		}
		os.Exit(0)
	}
	if os.Getenv("RADIO_TEST_GROUP_ROLE") == "guardian-parent" {
		child := exec.Command(os.Args[0], "-test.run=^TestRadioInertChild$")
		child.Env = append(os.Environ(), "RADIO_TEST_GROUP_ROLE=leader")
		setProcGroup(child)
		if child.Start() != nil {
			os.Exit(3)
		}
		killPath := os.Getenv("RADIO_TEST_KILLFILE")
		if os.WriteFile(killPath, []byte(strconv.Itoa(child.Process.Pid)), 0600) != nil {
			os.Exit(3)
		}
		kill, _ := os.Open(killPath)
		watcher := exec.Command(os.Args[0], "-test.run=^TestRadioInertChild$")
		watcher.Env = append(os.Environ(), "RADIO_TEST_GROUP_ROLE=guardian")
		if _, e := startRadioGuardianCommand(kill, watcher); e != nil {
			_ = syscall.Kill(-child.Process.Pid, syscall.SIGKILL)
			os.Exit(3)
		}
		_ = os.WriteFile(os.Getenv("RADIO_TEST_READYFILE"), []byte("ready"), 0600)
		time.Sleep(30 * time.Second)
		os.Exit(0)
	}
	if os.Getenv("RADIO_TEST_GROUP_ROLE") == "grandchild" {
		_ = os.WriteFile(os.Getenv("RADIO_TEST_PIDFILE"), []byte(strconv.Itoa(os.Getpid())), 0600)
		time.Sleep(30 * time.Second)
		os.Exit(0)
	}
	if os.Getenv("RADIO_TEST_GROUP_ROLE") == "leader" {
		cmd := exec.Command(os.Args[0], "-test.run=^TestRadioInertChild$")
		cmd.Env = append(os.Environ(), "RADIO_TEST_GROUP_ROLE=grandchild")
		if cmd.Start() != nil {
			os.Exit(2)
		}
		time.Sleep(30 * time.Second)
		os.Exit(0)
	}
	if os.Args[len(os.Args)-1] == "--radio-inert-child" {
		time.Sleep(30 * time.Second)
		os.Exit(0)
	}
}
func TestRadioSenderUnavailableContainmentNeverConstructsCommand(t *testing.T) {
	s, _ := newRadioSender(t.TempDir(), 4691, 4692, nil)
	built := false
	s.makeCommand = func(context.Context, string) *exec.Cmd { built = true; return nil }
	if s.startIdle(context.Background(), radioRequiredProofs) == nil || built {
		t.Fatal("unenforced launch")
	}
}
func TestRadioSenderStopOwnsCleanupEvenCancelledCaller(t *testing.T) {
	c := &radioTestContainment{}
	s := inertRadioSender(t, c)
	if e := s.startIdle(context.Background(), radioRequiredProofs); e != nil {
		t.Fatal(e)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_ = s.stop(ctx)
	select {
	case <-s.done:
	case <-time.After(3 * time.Second):
		t.Fatal("child survived")
	}
	if e := s.stop(context.Background()); e != nil {
		t.Fatal(e)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.kills < 1 || c.closes < 1 {
		t.Fatal("cleanup skipped")
	}
}
func TestRadioSenderContextCancellationCleansOwnedCustody(t *testing.T) {
	c := &radioTestContainment{}
	s := inertRadioSender(t, c)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if e := s.startIdle(ctx, radioRequiredProofs); e != nil {
		t.Fatal(e)
	}
	cancel()
	select {
	case <-s.done:
	case <-time.After(3 * time.Second):
		t.Fatal("cancel did not drain")
	}
	c.mu.Lock()
	kills, closes := c.kills, c.closes
	c.mu.Unlock()
	if kills < 1 || closes < 1 {
		t.Fatal("custody leaked")
	}
	if e := s.stop(context.Background()); e != nil {
		t.Fatal(e)
	}
}
func TestRadioSenderCannotReplaceUnknownContainment(t *testing.T) {
	c := &radioTestContainment{failClose: true}
	s := inertRadioSender(t, c)
	if e := s.startIdle(context.Background(), radioRequiredProofs); e != nil {
		t.Fatal(e)
	}
	if s.stop(context.Background()) == nil {
		t.Fatal("cleanup failure forgotten")
	}
	if s.startIdle(context.Background(), radioRequiredProofs) == nil {
		t.Fatal("unknown replaced")
	}
	c.mu.Lock()
	c.failClose = false
	c.mu.Unlock()
	if e := s.stop(context.Background()); e != nil {
		t.Fatal(e)
	}
}
func TestRadioSenderConfigureFailureHasNoLaunch(t *testing.T) {
	c := &radioTestContainment{failConfigure: true}
	s := inertRadioSender(t, c)
	if s.startIdle(context.Background(), radioRequiredProofs) == nil || s.cmd != nil {
		t.Fatal("bad containment launched")
	}
}
func TestRadioCgroupSharedOrUnavailableParentRejected(t *testing.T) {
	for _, p := range []string{"/sys/fs/cgroup", "relative", "/does-not-exist-radio"} {
		if c, e := newRadioCgroup(p, defaultRadioResourceLimits()); e == nil || c != nil {
			t.Fatal("unverified parent", p)
		}
	}
}

// A test-only process-group port models cgroup member cleanup. It is never
// returned by production constructors and makes no resource-limit claim.
type radioTestGroup struct{ radioTestContainment }

func (c *radioTestGroup) Configure(cmd *exec.Cmd) error { c.cmd = cmd; setProcGroup(cmd); return nil }
func (c *radioTestGroup) Kill() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.kills++
	if c.cmd == nil || c.cmd.Process == nil {
		return nil
	}
	e := syscall.Kill(-c.cmd.Process.Pid, syscall.SIGKILL)
	if e == syscall.ESRCH {
		return nil
	}
	return e
}
func TestRadioSenderOwnGroupContainsGrandchildAndPreservesUnrelatedChild(t *testing.T) {
	cage := &radioTestGroup{}
	s := inertRadioSender(t, &cage.radioTestContainment)
	s.newContainment = func() (radioProcessContainment, error) { return cage, nil }
	pidfile := filepath.Join(t.TempDir(), "grandchild.pid")
	s.makeCommand = func(ctx context.Context, _ string) *exec.Cmd {
		cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestRadioInertChild$")
		cmd.Env = append(os.Environ(), "RADIO_TEST_GROUP_ROLE=leader", "RADIO_TEST_PIDFILE="+pidfile)
		return cmd
	}
	other := exec.Command(os.Args[0], "-test.run=^TestRadioInertChild$", "--radio-inert-child")
	if e := other.Start(); e != nil {
		t.Fatal(e)
	}
	defer func() { _ = other.Process.Kill(); _ = other.Wait() }()
	if e := s.startIdle(context.Background(), radioRequiredProofs); e != nil {
		t.Fatal(e)
	}
	defer s.stop(context.Background())
	var pid int
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		b, e := os.ReadFile(pidfile)
		if e == nil {
			pid, _ = strconv.Atoi(string(b))
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if pid <= 0 {
		t.Fatal("grandchild did not start")
	}
	if e := syscall.Kill(pid, 0); e != nil {
		t.Fatal("grandchild not live", e)
	}
	if e := s.stop(context.Background()); e != nil {
		t.Fatal(e)
	}
	if e := other.Process.Signal(syscall.Signal(0)); e != nil {
		t.Fatal("unrelated process touched", e)
	}
	// Darwin reaps orphan children promptly; Linux may retain an inert zombie
	// until PID1 reaps it. No signal or group action is sent to that PID afterward.
	alive := false
	for end := time.Now().Add(time.Second); time.Now().Before(end); {
		if syscall.Kill(pid, 0) != nil {
			alive = false
			break
		}
		alive = true
		time.Sleep(5 * time.Millisecond)
	}
	if alive {
		b, _ := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
		if !strings.Contains(string(b), ") Z ") {
			t.Fatal("grandchild survived custody kill")
		}
	}
}

func TestRadioGuardianSurvivesManagerDeathAndKillsGrandchildOnly(t *testing.T) {
	dir := t.TempDir()
	grand := filepath.Join(dir, "grand.pid")
	killfile := filepath.Join(dir, "kill")
	ready := filepath.Join(dir, "ready")
	parent := exec.Command(os.Args[0], "-test.run=^TestRadioInertChild$")
	parent.Env = append(os.Environ(), "RADIO_TEST_GROUP_ROLE=guardian-parent", "RADIO_TEST_PIDFILE="+grand, "RADIO_TEST_KILLFILE="+killfile, "RADIO_TEST_READYFILE="+ready)
	if e := parent.Start(); e != nil {
		t.Fatal(e)
	}
	defer func() { _ = parent.Process.Kill() }()
	unrelated := exec.Command(os.Args[0], "-test.run=^TestRadioInertChild$", "--radio-inert-child")
	if e := unrelated.Start(); e != nil {
		t.Fatal(e)
	}
	defer func() { _ = unrelated.Process.Kill(); _ = unrelated.Wait() }()
	deadline := time.Now().Add(4 * time.Second)
	for time.Now().Before(deadline) {
		_, e := os.Stat(ready)
		_, g := os.Stat(grand)
		if e == nil && g == nil {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	raw, e := os.ReadFile(grand)
	if e != nil {
		t.Fatal("descendant not ready", e)
	}
	pid, _ := strconv.Atoi(string(raw))
	raw, e = os.ReadFile(killfile)
	if e != nil {
		t.Fatal(e)
	}
	leader, _ := strconv.Atoi(string(raw))
	if pid <= 0 || leader <= 0 {
		t.Fatal("invalid fixture identities")
	}
	defer syscall.Kill(-leader, syscall.SIGKILL)
	_ = parent.Process.Kill()
	_ = parent.Wait()
	for end := time.Now().Add(3 * time.Second); time.Now().Before(end); {
		if syscall.Kill(pid, 0) != nil {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if syscall.Kill(pid, 0) == nil {
		b, _ := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
		if !strings.Contains(string(b), ") Z ") {
			t.Fatal("guardian failed to kill grandchild")
		}
	}
	if e := unrelated.Process.Signal(syscall.Signal(0)); e != nil {
		t.Fatal("unrelated process affected", e)
	}
}
func TestRadioGuardianRefusesUnverifiedKillFile(t *testing.T) {
	f, e := os.CreateTemp(t.TempDir(), "not-cgroup")
	if e != nil {
		t.Fatal(e)
	}
	defer f.Close()
	if validateRadioKillFD(f) == nil {
		t.Fatal("foreign descriptor accepted")
	}
}

type radioTestHealth struct {
	radioTestContainment
	ended chan struct{}
}

func (c *radioTestHealth) Failure() <-chan struct{} { return c.ended }
func TestRadioSenderGuardianFailureStopsOwnChild(t *testing.T) {
	cage := &radioTestHealth{ended: make(chan struct{})}
	s := inertRadioSender(t, &cage.radioTestContainment)
	s.newContainment = func() (radioProcessContainment, error) { return cage, nil }
	if e := s.startIdle(context.Background(), radioRequiredProofs); e != nil {
		t.Fatal(e)
	}
	close(cage.ended)
	select {
	case <-s.done:
	case <-time.After(3 * time.Second):
		t.Fatal("guardian failure left sender live")
	}
	if e := s.stop(context.Background()); e != nil {
		t.Fatal(e)
	}
}
func TestRadioSenderDeadGuardianPreventsLaunch(t *testing.T) {
	cage := &radioTestHealth{ended: make(chan struct{})}
	close(cage.ended)
	s := inertRadioSender(t, &cage.radioTestContainment)
	s.newContainment = func() (radioProcessContainment, error) { return cage, nil }
	if s.startIdle(context.Background(), radioRequiredProofs) == nil || s.cmd != nil {
		t.Fatal("dead guardian accepted")
	}
}
