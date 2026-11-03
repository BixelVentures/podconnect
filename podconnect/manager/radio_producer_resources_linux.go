//go:build linux

package main

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

// This is a read of the actual supervised process and enforced kernel custody,
// not a caller-supplied capability flag or a test-port attestation.
func (s *radioSender) productionEvidence(pid int) error {
	if s == nil || pid <= 1 {
		return errRadioUnavailable
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cmd == nil || s.cmd.Process == nil || s.cmd.Process.Pid != pid || s.done == nil {
		return errRadioUnavailable
	}
	select {
	case <-s.done:
		return errRadioUnknown
	default:
	}
	if worker, ok := s.containment.(*radioWorker); ok {
		return worker.evidence(pid, s.cmd)
	}
	cage, ok := s.containment.(*radioCgroup)
	if !ok {
		return errRadioUnavailable
	}
	cage.mu.Lock()
	defer cage.mu.Unlock()
	if cage.closed || cage.fd == nil || cage.killFD == nil || cage.guardian == nil || cage.guardian.done == nil || s.cmd.SysProcAttr == nil || !s.cmd.SysProcAttr.UseCgroupFD || s.cmd.SysProcAttr.CgroupFD != int(cage.fd.Fd()) {
		return errRadioUnknown
	}
	select {
	case <-cage.guardian.done:
		return errRadioUnknown
	default:
	}
	credential := s.cmd.SysProcAttr.Credential
	if credential == nil || credential.Uid == 0 || credential.Gid == 0 {
		return errRadioUnavailable
	}
	status, e := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/status")
	if e != nil {
		return errRadioUnknown
	}
	uidMatched, gidMatched := false, false
	zeroCapabilities := map[string]bool{"CapInh:": false, "CapPrm:": false, "CapEff:": false, "CapAmb:": false}
	for _, line := range strings.Split(string(status), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 2 {
			if _, required := zeroCapabilities[fields[0]]; required {
				value, err := strconv.ParseUint(fields[1], 16, 64)
				if err != nil || value != 0 {
					return errRadioUnavailable
				}
				zeroCapabilities[fields[0]] = true
			}
		}
		if len(fields) == 5 && (fields[0] == "Uid:" || fields[0] == "Gid:") {
			want := strconv.FormatUint(uint64(credential.Uid), 10)
			if fields[0] == "Gid:" {
				want = strconv.FormatUint(uint64(credential.Gid), 10)
			}
			for _, actual := range fields[1:] {
				if actual != want {
					return errRadioUnavailable
				}
			}
			if fields[0] == "Uid:" {
				uidMatched = true
			} else {
				gidMatched = true
			}
		}
	}
	if !uidMatched || !gidMatched {
		return errRadioUnavailable
	}
	for _, observed := range zeroCapabilities {
		if !observed {
			return errRadioUnavailable
		}
	}
	var st syscall.Statfs_t
	if syscall.Statfs(cage.path, &st) != nil || uint64(st.Type) != 0x63677270 {
		return errRadioUnavailable
	}
	actual, e := os.Stat(cage.path)
	if e != nil {
		return errRadioUnknown
	}
	opened, e := cage.fd.Stat()
	if e != nil || !os.SameFile(actual, opened) {
		return errRadioUnknown
	}
	limits := defaultRadioResourceLimits()
	for name, want := range map[string]string{"memory.max": strconv.FormatInt(limits.MemoryBytes, 10), "memory.oom.group": "1", "pids.max": strconv.Itoa(limits.PIDs), "cpu.max": strconv.FormatInt(limits.CPUQuotaMicros, 10) + " " + strconv.FormatInt(limits.CPUPeriodMicros, 10)} {
		got, e := os.ReadFile(filepath.Join(cage.path, name))
		if e != nil || strings.TrimSpace(string(got)) != want {
			return errRadioUnknown
		}
	}
	members, e := os.ReadFile(filepath.Join(cage.path, "cgroup.procs"))
	if e != nil {
		return errRadioUnknown
	}
	for _, member := range strings.Fields(string(members)) {
		if member == strconv.Itoa(pid) {
			return nil
		}
	}
	return errRadioUnknown
}
