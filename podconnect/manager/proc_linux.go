//go:build linux

package main

import (
	"os/exec"
	"syscall"
	"time"
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

// gracefulKillGroup SIGTERMs the group first so go-librespot can withdraw its zeroconf/avahi
// registration cleanly (mDNS goodbye) — without this, a SIGKILL leaves stale Connect/alias entries
// lingering in clients' caches until TTL, which shows up as duplicate devices in the Spotify app on
// every restart. A hard SIGKILL on the SAME (old) group follows after a short grace if it hasn't
// exited; the respawned child has a new group, so this never touches it.
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
