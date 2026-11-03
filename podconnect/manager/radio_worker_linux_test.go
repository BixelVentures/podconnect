//go:build linux

package main

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"testing"
	"time"
)

func init() { radioWorkerGuardianEntry() }

func TestRadioWorkerEvidenceRejectsWeakOrDeadWorker(t *testing.T) {
	credential := &syscall.Credential{Uid: 4242, Gid: 4343}
	status := "State:\tS (sleeping)\nUid:\t4242 4242 4242 4242\nGid:\t4343 4343 4343 4343\nCapInh:\t00000000\nCapPrm:\t00000000\nCapEff:\t00000000\nCapAmb:\t00000000\nNoNewPrivs:\t1\nSeccomp:\t2\n"
	if e := radioWorkerStatusEvidence(status, credential); e != nil {
		t.Fatal(e)
	}
	for _, v := range [][2]string{{"State:\tS", "State:\tZ"}, {"4242 4242 4242 4242", "4242 0 4242 4242"}, {"4343 4343 4343 4343", "0 4343 4343 4343"}, {"CapEff:\t00000000", "CapEff:\t00000001"}, {"NoNewPrivs:\t1", "NoNewPrivs:\t0"}, {"Seccomp:\t2", "Seccomp:\t0"}, {"CapAmb:\t00000000\n", ""}} {
		if radioWorkerStatusEvidence(strings.Replace(status, v[0], v[1], 1), credential) == nil {
			t.Fatalf("accepted weakened evidence: %q", v)
		}
	}
	limits := "Max address space         536870912            536870912            bytes\nMax processes             32                   32                   processes\nMax open files            256                  256                  files\nMax core file size        0                    0                    bytes\n"
	if e := radioWorkerLimitEvidence(limits); e != nil {
		t.Fatal(e)
	}
	for _, mutation := range []string{strings.Replace(limits, "536870912", "unlimited", 1), strings.ReplaceAll(limits, "32 ", "64 "), strings.Replace(limits, "Max open files", "Other files", 1)} {
		if radioWorkerLimitEvidence(mutation) == nil {
			t.Fatal("accepted weaker or missing limit")
		}
	}
}

func TestRadioWorkerStatHandlesParenthesizedNameAndRejectsMissingFields(t *testing.T) {
	f := strings.Fields("S 1 2 3 4 5 6 7 8 9 10 12 13 0 0 0 0 4 0 98765 0 7")
	s, e := radioParseWorkerStat("123 (odd ) name) " + strings.Join(f, " "))
	if e != nil || s.cpuTicks != 25 || s.tasks != 4 || s.start != 98765 || s.rssBytes != 7*uint64(os.Getpagesize()) {
		t.Fatalf("bad stat %+v %v", s, e)
	}
	for _, raw := range []string{"", "12 name) S", "12 (x) S nope", strings.Replace("12 (x) "+strings.Join(f, " "), "98765", "-1", 1)} {
		if _, e := radioParseWorkerStat(raw); e == nil {
			t.Fatalf("accepted malformed stat: %q", raw)
		}
	}
}

func TestRadioWorkerGuardianManagerDeathKillsExactWorkerAndLeavesOtherProcess(t *testing.T) {
	worker := exec.Command("/bin/sleep", "30")
	if e := worker.Start(); e != nil {
		t.Fatal(e)
	}
	defer worker.Process.Kill()
	other := exec.Command("/bin/sleep", "30")
	if e := other.Start(); e != nil {
		t.Fatal(e)
	}
	defer func() { other.Process.Kill(); other.Wait() }()
	fd, e := radioOpenPIDFD(worker.Process.Pid)
	if e != nil {
		worker.Wait()
		t.Fatal(e)
	}
	defer fd.Close()
	stat, e := os.Open(fmt.Sprintf("/proc/%d/stat", worker.Process.Pid))
	if e != nil {
		t.Fatal(e)
	}
	defer stat.Close()
	g, e := startRadioWorkerGuardian(fd, stat)
	if e != nil {
		t.Fatal(e)
	}
	if e = g.Close(); e != nil {
		t.Fatal(e)
	}
	done := make(chan error, 1)
	go func() { done <- worker.Wait() }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("worker survived manager liveness EOF")
	}
	if radioPIDFDAlive(fd) == nil {
		t.Fatal("dead worker pidfd accepted as successful exec")
	}
	if e := other.Process.Signal(syscall.Signal(0)); e != nil {
		t.Fatalf("unrelated Spotify stand-in affected: %v", e)
	}
}

func TestRadioWorkerGuardianStopsSustainedCPUOverbudget(t *testing.T) {
	worker := exec.Command("/bin/sh", "-c", "while :; do :; done")
	if e := worker.Start(); e != nil {
		t.Fatal(e)
	}
	defer worker.Process.Kill()
	fd, e := radioOpenPIDFD(worker.Process.Pid)
	if e != nil {
		t.Fatal(e)
	}
	defer fd.Close()
	stat, e := os.Open(fmt.Sprintf("/proc/%d/stat", worker.Process.Pid))
	if e != nil {
		t.Fatal(e)
	}
	defer stat.Close()
	g, e := startRadioWorkerGuardian(fd, stat)
	if e != nil {
		t.Fatal(e)
	}
	defer g.Close()
	done := make(chan error, 1)
	go func() { done <- worker.Wait() }()
	select {
	case <-done:
	case <-time.After(8 * time.Second):
		t.Fatal("sustained CPU worker not stopped")
	}
}

func TestRadioWorkerRefusesUnboundedOrMutableLaunch(t *testing.T) {
	for _, cmd := range []*exec.Cmd{exec.Command("/bin/other"), exec.Command("owntone")} {
		w, _ := newRadioWorker()
		if w.Configure(cmd) == nil {
			t.Fatal("accepted missing permanent uid or unknown binary")
		}
		w.Close()
	}
	w, _ := newRadioWorker()
	cmd := exec.Command("/usr/sbin/owntone", "-f")
	cmd.Env = []string{"LD_PRELOAD=/evil.so", "GLIBC_TUNABLES=evil", "SECRET=never", "PODCONNECT_RADIO_SENDER=1"}
	cmd.SysProcAttr = &syscall.SysProcAttr{Credential: &syscall.Credential{Uid: 4242, Gid: 4343, Groups: []uint32{4343}}, Ptrace: true}
	if e := w.Configure(cmd); e != nil {
		t.Fatal(e)
	}
	defer w.Close()
	if cmd.Path != radioWorkerHelper || cmd.SysProcAttr.Ptrace || cmd.SysProcAttr.Credential.Uid != 4242 || len(cmd.ExtraFiles) != 3 {
		t.Fatal("fixed launcher or safe credentials not retained")
	}
	if strings.Contains(strings.Join(cmd.Env, "\n"), "evil") || strings.Contains(strings.Join(cmd.Env, "\n"), "SECRET=") || !strings.Contains(strings.Join(cmd.Env, "\n"), "PODCONNECT_RADIO_SENDER=1") {
		t.Fatal("untrusted pre-helper loader environment retained")
	}
	if w.Configure(cmd) == nil {
		t.Fatal("reused custody")
	}
}
