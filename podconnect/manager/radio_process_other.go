//go:build !linux

package main

import (
	"os"
	"os/exec"
	"syscall"
)

// Resource isolation is a production requirement, not a best-effort fallback.
func newRadioCgroup(parent string, limits radioResourceLimits) (radioProcessContainment, error) {
	return nil, errRadioUnavailable
}

func validateRadioKillFD(*os.File) error  { return errRadioUnavailable }
func setRadioGuardianGroup(cmd *exec.Cmd) { cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true} }
