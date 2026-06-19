//go:build linux

package main

import (
	"os/exec"
	"syscall"
)

// setProcGroup puts the child in its own process group (so we can signal the whole group on
// teardown) and asks the kernel to SIGKILL it if the manager dies (Pdeathsig) — so children never
// orphan when the manager is PID 1's child rather than s6-supervised.
func setProcGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Setpgid:   true,
		Pdeathsig: syscall.SIGKILL,
	}
}

// killGroup SIGKILLs the child's entire process group (negative pid). go-librespot/OwnTone don't
// fork helpers today, but killing the group is robust if they ever do.
func killGroup(cmd *exec.Cmd) {
	if cmd.Process == nil {
		return
	}
	pgid, err := syscall.Getpgid(cmd.Process.Pid)
	if err == nil {
		_ = syscall.Kill(-pgid, syscall.SIGKILL)
		return
	}
	_ = cmd.Process.Kill()
}
