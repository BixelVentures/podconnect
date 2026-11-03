//go:build linux

package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

type radioKernelFixtureReady struct {
	Cgroup string
	Leader int
}

func TestRadioRealCgroupFixtureProcess(t *testing.T) {
	role := os.Getenv("PC_RADIO_KERNEL_ROLE")
	if role == "" {
		t.Skip("explicit inert kernel helper only")
	}
	switch role {
	case "manager":
		cage, err := newRadioCgroup(os.Getenv("PC_RADIO_CHAIN_CGROUP_PARENT"), defaultRadioResourceLimits())
		if err != nil {
			t.Fatal(err)
		}
		cmd := exec.Command(os.Args[0], "-test.run=^TestRadioRealCgroupFixtureProcess$")
		cmd.Env = append(os.Environ(), "PC_RADIO_KERNEL_ROLE=leader")
		if err = cage.Configure(cmd); err != nil {
			t.Fatal(err)
		}
		if err = cmd.Start(); err != nil {
			t.Fatal(err)
		}
		concrete := cage.(*radioCgroup)
		raw, _ := json.Marshal(radioKernelFixtureReady{concrete.path, cmd.Process.Pid})
		if err = os.WriteFile(os.Getenv("PC_RADIO_KERNEL_READY"), raw, 0600); err != nil {
			t.Fatal(err)
		}
	case "leader":
		child := exec.Command(os.Args[0], "-test.run=^TestRadioRealCgroupFixtureProcess$")
		child.Env = append(os.Environ(), "PC_RADIO_KERNEL_ROLE=grandchild")
		child.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
		if err := child.Start(); err != nil {
			t.Fatal(err)
		}
	case "grandchild":
		if err := os.WriteFile(os.Getenv("PC_RADIO_KERNEL_GRAND"), []byte(strconv.Itoa(os.Getpid())), 0600); err != nil {
			t.Fatal(err)
		}
	case "unrelated":
	default:
		t.Fatal("unknown kernel helper")
	}
	time.Sleep(90 * time.Second)
}

func TestRadioProductionGuardianExternalChain(t *testing.T) {
	if os.Getenv("PC_RADIO_CHAIN_RUN") != "1" {
		t.Skip("explicit real delegated-cgroup environment required")
	}
	radioChainInertNamespace(t)
	parent := radioChainPath(t, radioChainEnv(t, "CGROUP_PARENT"))
	root := t.TempDir()
	readyFile, grandFile := filepath.Join(root, "ready.json"), filepath.Join(root, "grand.pid")
	manager := exec.Command(os.Args[0], "-test.run=^TestRadioRealCgroupFixtureProcess$")
	manager.Env = append(os.Environ(), "PC_RADIO_KERNEL_ROLE=manager", "PC_RADIO_KERNEL_READY="+readyFile, "PC_RADIO_KERNEL_GRAND="+grandFile, "PC_RADIO_CHAIN_CGROUP_PARENT="+parent)
	if err := manager.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = manager.Process.Kill() }()
	unrelated := exec.Command(os.Args[0], "-test.run=^TestRadioRealCgroupFixtureProcess$")
	unrelated.Env = append(os.Environ(), "PC_RADIO_KERNEL_ROLE=unrelated")
	if err := unrelated.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = unrelated.Process.Kill(); _ = unrelated.Wait() }()
	var ready radioKernelFixtureReady
	grand := 0
	for until := time.Now().Add(4 * time.Second); time.Now().Before(until); {
		raw, err := os.ReadFile(readyFile)
		g, e := os.ReadFile(grandFile)
		if err == nil && e == nil && json.Unmarshal(raw, &ready) == nil {
			grand, _ = strconv.Atoi(string(g))
			if grand > 1 {
				break
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	if ready.Leader <= 1 || grand <= 1 || !strings.HasPrefix(ready.Cgroup, parent+string(os.PathSeparator)) {
		t.Fatal("actual descendant identities unavailable")
	}
	members, err := os.ReadFile(filepath.Join(ready.Cgroup, "cgroup.procs"))
	if err != nil {
		t.Fatal(err)
	}
	for _, pid := range []int{ready.Leader, grand} {
		found := false
		for _, member := range strings.Fields(string(members)) {
			found = found || member == strconv.Itoa(pid)
		}
		if !found {
			t.Fatal("descendant escaped exact actual cgroup before death", pid)
		}
	}
	for key, want := range map[string]string{"cpu.max": "50000 100000", "memory.max": "201326592", "pids.max": "32", "memory.oom.group": "1"} {
		raw, err := os.ReadFile(filepath.Join(ready.Cgroup, key))
		if err != nil || strings.TrimSpace(string(raw)) != want {
			t.Fatal("actual inherited kernel budget absent", key)
		}
	}
	if err = manager.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = manager.Wait()
	drained := false
	for until := time.Now().Add(4 * time.Second); time.Now().Before(until); {
		raw, err := os.ReadFile(filepath.Join(ready.Cgroup, "cgroup.events"))
		if err == nil && strings.Contains(string(raw), "populated 0\n") {
			drained = true
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !drained {
		t.Fatal("actual guardian did not empty exact radio cgroup after manager SIGKILL")
	}
	if err = unrelated.Process.Signal(syscall.Signal(0)); err != nil {
		t.Fatal("unrelated process was affected", err)
	}
	for _, pid := range []int{ready.Leader, grand} {
		raw, _ := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
		if len(raw) > 0 && !strings.Contains(string(raw), ") Z ") {
			t.Fatal("owned child survived actual cgroup.kill", pid)
		}
	}
	// The dead owner deliberately does not recreate/adopt custody. The observer
	// removes only this now-empty exact group created by the helper above.
	if err = os.Remove(ready.Cgroup); err != nil {
		t.Fatal("cannot remove empty exact fixture cgroup", err)
	}
	t.Log("VERIFIED actual clone3 budget/membership + manager SIGKILL + survivor guardian EOF/cgroup.kill retires leader and setsid grandchild; unrelated process survives")
}
