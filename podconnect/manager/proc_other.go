//go:build !linux

package main

import (
	"os/exec"
	"syscall"
)

// setProcGroup (non-linux, e.g. the macOS dev host): own process group only — Pdeathsig is a Linux
// feature. The production image is Linux, where proc_linux.go adds die-with-parent. This file exists
// so `go build`/`go test`/`go vet` work on the dev host.
func setProcGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

// killGroup SIGKILLs the child's process group (or the process if the group lookup fails).
func killGroup(cmd *exec.Cmd) {
	if cmd.Process == nil {
		return
	}
	if pgid, err := syscall.Getpgid(cmd.Process.Pid); err == nil {
		_ = syscall.Kill(-pgid, syscall.SIGKILL)
		return
	}
	_ = cmd.Process.Kill()
}
