//go:build !linux

package main

import (
	"os/exec"
	"syscall"
	"time"
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

// gracefulKillGroup (non-linux dev stub): SIGTERM then a delayed SIGKILL on the old group, mirroring
// the linux version so restartGL compiles + behaves on the dev host.
func gracefulKillGroup(cmd *exec.Cmd) {
	if cmd.Process == nil {
		return
	}
	pgid, err := syscall.Getpgid(cmd.Process.Pid)
	if err != nil {
		_ = cmd.Process.Kill()
		return
	}
	_ = syscall.Kill(-pgid, syscall.SIGTERM)
	go func(pg int) {
		time.Sleep(3 * time.Second)
		_ = syscall.Kill(-pg, syscall.SIGKILL)
	}(pgid)
}
