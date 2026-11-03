//go:build linux

package main

// The HA worker is one permanently unprivileged, seccomp-confined process.
// Its launcher forbids process creation before decoder exec. It therefore needs
// no delegated host cgroup: a pidfd identifies all and only the worker's threads.
// CPU/RSS monitoring is sampled fail-stop protection, NOT a cgroup quota.
import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"
)

const (
	radioWorkerHelper       = "/usr/libexec/podconnect-radio-worker"
	radioWorkerGuardianFlag = "--podview-radio-worker-guardian"
	radioWorkerAddressSpace = 512 * 1024 * 1024
	radioWorkerRSS          = 192 * 1024 * 1024
	radioWorkerTasks        = 32
	radioWorkerFiles        = 256
)

type radioWorker struct {
	mu                                                            sync.Mutex
	cmd                                                           *exec.Cmd
	ready, childReady, goReader, goWriter, execReader, execWriter *os.File
	pidfd                                                         *os.File
	guardian                                                      *radioGuardian
	pid                                                           int
	closed                                                        bool
	executed                                                      bool
	failure                                                       chan struct{}
	failOnce                                                      sync.Once
}

func newRadioWorker() (radioProcessContainment, error) {
	if runtime.GOARCH != "amd64" && runtime.GOARCH != "arm64" {
		return nil, errRadioUnavailable
	}
	return &radioWorker{failure: make(chan struct{})}, nil
}

func (w *radioWorker) Failure() <-chan struct{} { return w.failure }
func (w *radioWorker) fail()                    { w.failOnce.Do(func() { close(w.failure) }) }

func (w *radioWorker) Configure(cmd *exec.Cmd) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed || w.cmd != nil || cmd == nil || cmd.Process != nil || len(cmd.ExtraFiles) != 0 || len(cmd.Args) == 0 || cmd.SysProcAttr == nil || cmd.SysProcAttr.Credential == nil || cmd.SysProcAttr.Credential.Uid == 0 || cmd.SysProcAttr.Credential.Gid == 0 {
		return errRadioUnavailable
	}
	if cmd.Path != "owntone" && cmd.Path != "/usr/sbin/owntone" {
		return errRadioUnavailable
	}
	var e error
	w.ready, w.childReady, e = os.Pipe()
	if e != nil {
		return e
	}
	w.goReader, w.goWriter, e = os.Pipe()
	if e != nil {
		w.ready.Close()
		w.childReady.Close()
		return e
	}
	w.execReader, w.execWriter, e = os.Pipe()
	if e != nil {
		w.ready.Close()
		w.childReady.Close()
		w.goReader.Close()
		w.goWriter.Close()
		return e
	}
	credential := *cmd.SysProcAttr.Credential
	credential.Groups = append([]uint32(nil), credential.Groups...)
	cmd.Path = radioWorkerHelper
	cmd.Args = append([]string{radioWorkerHelper}, cmd.Args[1:]...)
	// The helper's own dynamic loader runs before its main function. Never
	// inherit LD_PRELOAD/LD_AUDIT/GLIBC_TUNABLES or arbitrary host variables.
	env := []string{"PATH=/usr/sbin:/usr/bin:/sbin:/bin", "LANG=C", "LC_ALL=C", "MALLOC_ARENA_MAX=1"}
	for _, value := range cmd.Env {
		if value == "PODCONNECT_RADIO_SENDER=1" || value == "PODCONNECT_RADIO_INITIALIZE_ONLY=1" {
			env = append(env, value)
		}
	}
	cmd.Env = env
	cmd.SysProcAttr = &syscall.SysProcAttr{Pdeathsig: syscall.SIGKILL, Credential: &credential}
	cmd.ExtraFiles = []*os.File{w.childReady, w.goReader, w.execWriter}
	w.cmd = cmd
	return nil
}

// AfterStart must complete before the sender can become visible or executable.
// The helper cannot exec its decoder until guardian custody is established.
func (w *radioWorker) AfterStart(cmd *exec.Cmd) error {
	return w.afterStart(cmd, false)
}

// Trusted initialize-only commands are joined by their caller. A successful
// exec may legitimately exit before a subsequent liveness read; only the
// one-shot call site can use this hook and must require cmd.Wait()==nil.
func (w *radioWorker) AfterStartOneShot(cmd *exec.Cmd) error {
	return w.afterStart(cmd, true)
}

func (w *radioWorker) afterStart(cmd *exec.Cmd, oneShot bool) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed || w.cmd != cmd || cmd == nil || cmd.Process == nil || w.pidfd != nil {
		return errRadioUnavailable
	}
	w.pid = cmd.Process.Pid
	w.childReady.Close()
	w.childReady = nil
	w.goReader.Close()
	w.goReader = nil
	w.execWriter.Close()
	w.execWriter = nil
	_ = w.ready.SetReadDeadline(time.Now().Add(2 * time.Second))
	var ready [1]byte
	_, e := io.ReadFull(w.ready, ready[:])
	w.ready.Close()
	w.ready = nil
	if e != nil || ready[0] != 1 {
		w.fail()
		return errRadioUnavailable
	}
	w.pidfd, e = radioOpenPIDFD(w.pid)
	if e != nil {
		w.fail()
		return errRadioUnavailable
	}
	stat, e := os.Open(fmt.Sprintf("/proc/%d/stat", w.pid))
	if e != nil {
		w.fail()
		return errRadioUnavailable
	}
	defer stat.Close()
	w.guardian, e = startRadioWorkerGuardian(w.pidfd, stat)
	if e != nil {
		w.fail()
		return e
	}
	go func(g *radioGuardian) { <-g.done; w.fail() }(w.guardian)
	if _, e = w.goWriter.Write([]byte{1}); e != nil {
		w.fail()
		return errRadioUnknown
	}
	w.goWriter.Close()
	w.goWriter = nil
	_ = w.execReader.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, e = w.execReader.Read(ready[:])
	w.execReader.Close()
	w.execReader = nil
	if e != io.EOF {
		w.fail()
		return errRadioUnavailable
	}
	// CLOEXEC EOF alone also occurs if the launcher is killed. Refuse a dead
	// pidfd or zombie rather than converting that unknown exec into readiness.
	if !oneShot {
		if e = radioPIDFDAlive(w.pidfd); e != nil {
			w.fail()
			return errRadioUnknown
		}
		status, e := os.ReadFile(fmt.Sprintf("/proc/%d/status", w.pid))
		if e != nil {
			w.fail()
			return errRadioUnknown
		}
		if e = radioWorkerStatusEvidence(string(status), cmd.SysProcAttr.Credential); e != nil {
			w.fail()
			return e
		}
	}
	// The trusted fixed-target launcher kept fd5 CLOEXEC. EOF after the go
	// barrier means exec crossed; a failure byte or timeout refuses readiness.
	w.executed = true
	return nil
}

func radioOpenPIDFD(pid int) (*os.File, error) {
	if pid <= 1 {
		return nil, errRadioUnavailable
	}
	fd, _, errno := syscall.Syscall(434, uintptr(pid), 0, 0)
	if errno != 0 {
		return nil, errno
	}
	syscall.CloseOnExec(int(fd))
	return os.NewFile(fd, "radio-worker-pidfd"), nil
}
func radioPIDFDSignal(fd *os.File, signal syscall.Signal) error {
	if fd == nil {
		return errRadioUnavailable
	}
	_, _, errno := syscall.Syscall6(424, fd.Fd(), uintptr(signal), 0, 0, 0, 0)
	if errno == syscall.ESRCH {
		return nil
	}
	if errno != 0 {
		return errno
	}
	return nil
}

func radioPIDFDAlive(fd *os.File) error {
	if fd == nil {
		return errRadioUnavailable
	}
	poll := struct {
		FD              int32
		Events, Revents int16
	}{FD: int32(fd.Fd()), Events: 1}
	zero := syscall.Timespec{}
	n, _, errno := syscall.Syscall6(syscall.SYS_PPOLL, uintptr(unsafe.Pointer(&poll)), 1, uintptr(unsafe.Pointer(&zero)), 0, 0, 0)
	if errno != 0 {
		return errno
	}
	if n != 0 || poll.Revents != 0 {
		return errRadioUnknown
	}
	return nil
}

func (w *radioWorker) Kill() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return nil
	}
	if w.pidfd != nil {
		return radioPIDFDSignal(w.pidfd, syscall.SIGKILL)
	}
	// Before helper readiness the only child is the exact unreaped exec.Cmd
	// process. It has not been released to decoder exec, and cannot fork.
	if w.cmd != nil && w.cmd.Process != nil {
		e := w.cmd.Process.Kill()
		if errors.Is(e, os.ErrProcessDone) {
			return nil
		}
		return e
	}
	return nil
}

func (w *radioWorker) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return nil
	}
	// Close never turns an unjoined worker into permission for a fresh launch.
	if w.cmd != nil && w.cmd.Process != nil && w.cmd.ProcessState == nil {
		return errRadioUnknown
	}
	if w.guardian != nil {
		if e := w.guardian.Close(); e != nil {
			return e
		}
	}
	for _, f := range []*os.File{w.ready, w.childReady, w.goReader, w.goWriter, w.execReader, w.execWriter, w.pidfd} {
		if f != nil {
			f.Close()
		}
	}
	w.closed = true
	return nil
}

func startRadioWorkerGuardian(pidfd, stat *os.File) (*radioGuardian, error) {
	executable, e := os.Executable()
	if e != nil {
		return nil, e
	}
	reader, writer, e := os.Pipe()
	if e != nil {
		return nil, e
	}
	readyReader, readyWriter, e := os.Pipe()
	if e != nil {
		reader.Close()
		writer.Close()
		return nil, e
	}
	cmd := exec.Command(executable, radioWorkerGuardianFlag)
	// This helper deliberately survives manager death. Only the inherited
	// liveness pipe, with no writers in the radio process, determines custody.
	cmd.ExtraFiles = []*os.File{reader, pidfd, readyWriter, stat}
	if e = cmd.Start(); e != nil {
		reader.Close()
		writer.Close()
		readyReader.Close()
		readyWriter.Close()
		return nil, e
	}
	reader.Close()
	readyWriter.Close()
	g := &radioGuardian{writer: writer, cmd: cmd, done: make(chan struct{})}
	go func() { e := cmd.Wait(); g.mu.Lock(); g.err = e; g.mu.Unlock(); close(g.done) }()
	_ = readyReader.SetReadDeadline(time.Now().Add(2 * time.Second))
	var b [1]byte
	_, e = io.ReadFull(readyReader, b[:])
	readyReader.Close()
	if e != nil || b[0] != 1 {
		writer.Close()
		cmd.Process.Kill()
		<-g.done
		return nil, errRadioUnavailable
	}
	return g, nil
}

func radioWorkerGuardianEntry() {
	if len(os.Args) != 2 || os.Args[1] != radioWorkerGuardianFlag {
		return
	}
	os.Exit(radioWorkerGuardianServe(os.NewFile(3, "manager-liveness"), os.NewFile(4, "worker-pidfd"), os.NewFile(5, "ready"), os.NewFile(6, "worker-stat")))
}

func radioWorkerGuardianServe(liveness, pidfd, ready, stat *os.File) int {
	if liveness == nil || pidfd == nil || ready == nil || stat == nil {
		return 2
	}
	defer liveness.Close()
	defer pidfd.Close()
	defer ready.Close()
	defer stat.Close()
	// Validate both inherited handles before claiming custody; pidfd does not
	// permit a caller-controlled PID to redirect a subsequent kill.
	target, e := os.Readlink(fmt.Sprintf("/proc/self/fd/%d", pidfd.Fd()))
	if e != nil || target != "anon_inode:[pidfd]" {
		return 2
	}
	initial, e := radioReadWorkerStat(stat)
	if e != nil {
		return 2
	}
	if e = radioPIDFDSignal(pidfd, 0); e != nil {
		return 2
	}
	if _, e = ready.Write([]byte{1}); e != nil {
		return 2
	}
	ready.Close()
	ended := make(chan struct{})
	go func() { _, _ = io.Copy(io.Discard, liveness); close(ended) }()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	last := time.Now()
	prior := initial.cpuTicks
	credit := float64(100)
	for {
		select {
		case <-ended:
			if radioPIDFDSignal(pidfd, syscall.SIGKILL) != nil {
				return 3
			}
			return 0
		case now := <-ticker.C:
			current, err := radioReadWorkerStat(stat)
			if err != nil || current.start != initial.start || current.cpuTicks < prior {
				if radioPIDFDSignal(pidfd, syscall.SIGKILL) != nil {
					return 3
				}
				return 0
			}
			// Linux proc accounting uses USER_HZ=100 on supported amd64/arm64.
			// One CPU-second burst, refilled at half a CPU-second per second.
			credit += now.Sub(last).Seconds()*50 - float64(current.cpuTicks-prior)
			if credit > 100 {
				credit = 100
			}
			last = now
			prior = current.cpuTicks
			if credit < 0 || current.rssBytes > radioWorkerRSS || current.tasks > radioWorkerTasks {
				if radioPIDFDSignal(pidfd, syscall.SIGKILL) != nil {
					return 3
				}
				return 0
			}
		}
	}
}

type radioWorkerStat struct {
	cpuTicks, start, rssBytes uint64
	tasks                     uint64
}

func radioReadWorkerStat(file *os.File) (radioWorkerStat, error) {
	if _, e := file.Seek(0, 0); e != nil {
		return radioWorkerStat{}, e
	}
	b, e := io.ReadAll(io.LimitReader(file, 8192))
	if e != nil {
		return radioWorkerStat{}, e
	}
	return radioParseWorkerStat(string(b))
}
func radioParseWorkerStat(raw string) (radioWorkerStat, error) {
	end := strings.LastIndex(raw, ") ")
	if end < 0 {
		return radioWorkerStat{}, errRadioUnavailable
	}
	f := strings.Fields(raw[end+2:])
	if len(f) < 22 {
		return radioWorkerStat{}, errRadioUnavailable
	}
	values := make([]uint64, 0, 5)
	for _, index := range []int{11, 12, 17, 19, 21} {
		v, e := strconv.ParseUint(f[index], 10, 64)
		if e != nil {
			return radioWorkerStat{}, e
		}
		values = append(values, v)
	}
	if values[4] > ^uint64(0)/uint64(os.Getpagesize()) {
		return radioWorkerStat{}, errRadioUnavailable
	}
	return radioWorkerStat{cpuTicks: values[0] + values[1], tasks: values[2], start: values[3], rssBytes: values[4] * uint64(os.Getpagesize())}, nil
}

func (w *radioWorker) evidence(pid int, cmd *exec.Cmd) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed || !w.executed || w.pid != pid || w.cmd != cmd || w.pidfd == nil || w.guardian == nil || cmd.SysProcAttr == nil || cmd.SysProcAttr.Credential == nil {
		return errRadioUnavailable
	}
	select {
	case <-w.failure:
		return errRadioUnknown
	default:
	}
	select {
	case <-w.guardian.done:
		return errRadioUnknown
	default:
	}
	if e := radioPIDFDAlive(w.pidfd); e != nil {
		return errRadioUnknown
	}
	status, e := os.ReadFile(fmt.Sprintf("/proc/%d/status", pid))
	if e != nil {
		return errRadioUnknown
	}
	if e = radioWorkerStatusEvidence(string(status), cmd.SysProcAttr.Credential); e != nil {
		return e
	}
	limits, e := os.ReadFile(fmt.Sprintf("/proc/%d/limits", pid))
	if e != nil {
		return errRadioUnknown
	}
	return radioWorkerLimitEvidence(string(limits))
}

func radioWorkerStatusEvidence(raw string, credential *syscall.Credential) error {
	if credential == nil || credential.Uid == 0 || credential.Gid == 0 {
		return errRadioUnavailable
	}
	required := map[string]bool{"State:": false, "Uid:": false, "Gid:": false, "CapInh:": false, "CapPrm:": false, "CapEff:": false, "CapAmb:": false, "NoNewPrivs:": false, "Seccomp:": false}
	for _, line := range strings.Split(raw, "\n") {
		f := strings.Fields(line)
		if len(f) < 2 {
			continue
		}
		if _, ok := required[f[0]]; !ok {
			continue
		}
		switch f[0] {
		case "State:":
			if f[1] == "Z" || f[1] == "X" || f[1] == "x" {
				return errRadioUnknown
			}
		case "Uid:", "Gid:":
			if len(f) != 5 {
				return errRadioUnavailable
			}
			want := uint64(credential.Uid)
			if f[0] == "Gid:" {
				want = uint64(credential.Gid)
			}
			for _, s := range f[1:] {
				v, e := strconv.ParseUint(s, 10, 32)
				if e != nil || v != want {
					return errRadioUnavailable
				}
			}
		case "NoNewPrivs:":
			if f[1] != "1" {
				return errRadioUnavailable
			}
		case "Seccomp:":
			if f[1] != "2" {
				return errRadioUnavailable
			}
		default:
			v, e := strconv.ParseUint(f[1], 16, 64)
			if e != nil || v != 0 {
				return errRadioUnavailable
			}
		}
		required[f[0]] = true
	}
	for _, ok := range required {
		if !ok {
			return errRadioUnavailable
		}
	}
	return nil
}
func radioWorkerLimitEvidence(raw string) error {
	required := map[string]string{"Max address space": "536870912", "Max processes": "32", "Max open files": "256", "Max core file size": "0"}
	for label, want := range required {
		found := false
		for _, line := range strings.Split(raw, "\n") {
			if strings.HasPrefix(line, label+" ") {
				f := strings.Fields(strings.TrimPrefix(line, label))
				if len(f) < 2 || f[0] != want || f[1] != want {
					return errRadioUnavailable
				}
				found = true
				break
			}
		}
		if !found {
			return errRadioUnavailable
		}
	}
	return nil
}
