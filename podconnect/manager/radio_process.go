package main

import "os/exec"

// Implementations must place a child under enforced limits BEFORE its first
// instruction. Kill affects only that allocated custody, including descendants.
// Close is idempotent and refuses retirement while any member remains alive.
type radioProcessContainment interface {
	Configure(*exec.Cmd) error
	Kill() error
	Close() error
}

// The HA worker blocks before decoder exec until its independent guardian is
// attached. The original atomic cgroup launch needs no additional handshake.
func radioContainmentAfterStart(cmd *exec.Cmd, custody radioProcessContainment) error {
	if worker, ok := custody.(interface{ AfterStart(*exec.Cmd) error }); ok {
		return worker.AfterStart(cmd)
	}
	return nil
}

func radioContainmentAfterStartOneShot(cmd *exec.Cmd, custody radioProcessContainment) error {
	if worker, ok := custody.(interface{ AfterStartOneShot(*exec.Cmd) error }); ok {
		return worker.AfterStartOneShot(cmd)
	}
	return radioContainmentAfterStart(cmd, custody)
}
func radioConfiguredContainment(parent string) (radioProcessContainment, error) {
	if parent == "" {
		return newRadioWorker()
	}
	return newRadioCgroup(parent, defaultRadioResourceLimits())
}

type radioResourceLimits struct {
	MemoryBytes     int64
	PIDs            int
	CPUQuotaMicros  int64
	CPUPeriodMicros int64
}

func defaultRadioResourceLimits() radioResourceLimits {
	return radioResourceLimits{MemoryBytes: 192 * 1024 * 1024, PIDs: 32, CPUQuotaMicros: 50000, CPUPeriodMicros: 100000}
}
