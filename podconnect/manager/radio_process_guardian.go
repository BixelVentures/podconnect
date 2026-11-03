package main

// This helper is outside the radio resource group. A manager-owned CLOEXEC pipe
// is its only liveness signal: SIGKILL of the manager also closes that pipe.
import (
	"errors"
	"io"
	"os"
	"os/exec"
	"sync"
	"time"
)

const radioGuardianFlag = "--podview-radio-cgroup-guardian"

type radioGuardian struct {
	mu     sync.Mutex
	writer *os.File
	cmd    *exec.Cmd
	done   chan struct{}
	err    error
	closed bool
}

func radioGuardianEntry() {
	if len(os.Args) != 2 || os.Args[1] != radioGuardianFlag {
		return
	}
	os.Exit(radioGuardianServe(os.NewFile(3, "radio-manager-liveness"), os.NewFile(4, "radio-cgroup-kill"), os.NewFile(5, "radio-guardian-ready")))
}
func radioGuardianServe(liveness, kill, ready *os.File) int {
	if liveness == nil || kill == nil || ready == nil {
		return 2
	}
	defer liveness.Close()
	defer kill.Close()
	defer ready.Close()
	if validateRadioKillFD(kill) != nil {
		return 2
	}
	if _, e := ready.Write([]byte{1}); e != nil {
		return 2
	}
	ready.Close()
	if e := radioGuardianWatch(liveness, func() error { _, e := kill.Write([]byte("1")); return e }); e != nil {
		return 3
	}
	return 0
}
func radioGuardianWatch(reader io.Reader, kill func() error) error {
	// Any read termination fails closed. The manager sends no bytes or commands.
	_, readErr := io.Copy(io.Discard, reader)
	return errors.Join(readErr, kill())
}
func startRadioGuardian(kill *os.File) (*radioGuardian, error) {
	executable, e := os.Executable()
	if e != nil {
		return nil, e
	}
	cmd := exec.Command(executable, radioGuardianFlag)
	return startRadioGuardianCommand(kill, cmd)
}
func startRadioGuardianCommand(kill *os.File, cmd *exec.Cmd) (*radioGuardian, error) {
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
	// Do not use setProcGroup: its Linux Pdeathsig would kill this survivor.
	setRadioGuardianGroup(cmd)
	cmd.ExtraFiles = []*os.File{reader, kill, readyWriter}
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
		_ = cmd.Process.Kill()
		<-g.done
		return nil, errRadioUnavailable
	}
	return g, nil
}
func (g *radioGuardian) Close() error {
	g.mu.Lock()
	if !g.closed {
		g.closed = true
		g.writer.Close()
	}
	g.mu.Unlock()
	select {
	case <-g.done:
		g.mu.Lock()
		defer g.mu.Unlock()
		return g.err
	case <-time.After(2 * time.Second):
		return errRadioUnknown
	}
}
