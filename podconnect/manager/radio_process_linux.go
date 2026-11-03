//go:build linux

package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
)

type radioCgroup struct {
	mu            sync.Mutex
	path          string
	fd            *os.File
	closed        bool
	removeDir     func(string) error
	killFD        *os.File
	guardian      *radioGuardian
	startGuardian func(*os.File) (*radioGuardian, error)
}

func newRadioCgroup(parent string, l radioResourceLimits) (radioProcessContainment, error) {
	// The installer must supply a dedicated delegated parent. Never create or
	// enable controllers at the shared HA/add-on/system cgroup root.
	if !filepath.IsAbs(parent) || filepath.Clean(parent) != parent || parent == "/sys/fs/cgroup" || l.MemoryBytes < 32*1024*1024 || l.PIDs < 2 || l.PIDs > 64 || l.CPUPeriodMicros != 100000 || l.CPUQuotaMicros < 1000 || l.CPUQuotaMicros > 100000 {
		return nil, errRadioUnavailable
	}
	canonical, e := filepath.EvalSymlinks(parent)
	if e != nil || canonical != parent {
		return nil, errRadioUnavailable
	}
	var st syscall.Statfs_t
	if syscall.Statfs(parent, &st) != nil || uint64(st.Type) != 0x63677270 {
		return nil, errRadioUnavailable
	}
	controls, e := os.ReadFile(filepath.Join(parent, "cgroup.subtree_control"))
	if e != nil {
		return nil, e
	}
	for _, v := range []string{"cpu", "memory", "pids"} {
		found := false
		for _, c := range strings.Fields(string(controls)) {
			found = found || c == v
		}
		if !found {
			return nil, errRadioUnavailable
		}
	}
	path, e := os.MkdirTemp(parent, "podview-radio-")
	if e != nil {
		return nil, e
	}
	fail := func(err error) (radioProcessContainment, error) { os.Remove(path); return nil, err }
	for _, v := range [][2]string{{"memory.max", strconv.FormatInt(l.MemoryBytes, 10)}, {"memory.oom.group", "1"}, {"pids.max", strconv.Itoa(l.PIDs)}, {"cpu.max", fmt.Sprintf("%d %d", l.CPUQuotaMicros, l.CPUPeriodMicros)}} {
		if e = os.WriteFile(filepath.Join(path, v[0]), []byte(v[1]), 0600); e != nil {
			return fail(e)
		}
	}
	// cgroup.kill is required; no process-ID or best-effort fallback.
	kill, e := os.OpenFile(filepath.Join(path, "cgroup.kill"), os.O_WRONLY, 0)
	if e != nil {
		return fail(e)
	}

	fd, e := os.Open(path)
	if e != nil {
		kill.Close()
		return fail(e)
	}
	return &radioCgroup{path: path, fd: fd, killFD: kill, startGuardian: startRadioGuardian, removeDir: os.Remove}, nil
}
func (c *radioCgroup) Configure(cmd *exec.Cmd) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed || cmd.Process != nil || c.guardian != nil {
		return errRadioUnavailable
	}
	if c.startGuardian == nil || c.killFD == nil {
		return errRadioUnavailable
	}
	g, e := c.startGuardian(c.killFD)
	if e != nil {
		return e
	}
	c.guardian = g
	var credential *syscall.Credential
	if cmd.SysProcAttr != nil && cmd.SysProcAttr.Credential != nil {
		copy := *cmd.SysProcAttr.Credential
		copy.Groups = append([]uint32(nil), copy.Groups...)
		credential = &copy
	}
	// Only the explicit permanent uid is retained. No caller-supplied namespace,
	// ptrace or parent-death override may replace this custody boundary.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true, Pdeathsig: syscall.SIGKILL, UseCgroupFD: true, CgroupFD: int(c.fd.Fd()), Credential: credential}
	return nil
}
func (c *radioCgroup) Kill() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return nil
	}
	return os.WriteFile(filepath.Join(c.path, "cgroup.kill"), []byte("1"), 0600)
}
func (c *radioCgroup) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return nil
	}
	b, e := os.ReadFile(filepath.Join(c.path, "cgroup.events"))
	if e != nil {
		return e
	}
	if !strings.Contains(string(b), "populated 0\n") {
		return errRadioUnknown
	}
	if c.guardian != nil {
		if e = c.guardian.Close(); e != nil {
			return e
		}
	}
	remove := c.removeDir
	if remove == nil {
		remove = os.Remove
	}
	if e = remove(c.path); e != nil {
		return e
	}
	c.fd.Close()
	if c.killFD != nil {
		c.killFD.Close()
	}
	c.closed = true
	return nil
}

func validateRadioKillFD(file *os.File) error {
	path, e := os.Readlink(fmt.Sprintf("/proc/self/fd/%d", file.Fd()))
	if e != nil || !strings.HasSuffix(path, "/cgroup.kill") {
		return errRadioUnavailable
	}
	var st syscall.Statfs_t
	if syscall.Fstatfs(int(file.Fd()), &st) != nil || uint64(st.Type) != 0x63677270 {
		return errRadioUnavailable
	}
	info, e := file.Stat()
	if e != nil || !info.Mode().IsRegular() {
		return errRadioUnavailable
	}
	return nil
}
func setRadioGuardianGroup(cmd *exec.Cmd) { cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true} }

func (c *radioCgroup) Failure() <-chan struct{} {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.guardian == nil {
		return nil
	}
	return c.guardian.done
}
