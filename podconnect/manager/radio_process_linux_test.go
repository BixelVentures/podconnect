//go:build linux

package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
)

func TestRadioCgroupFakeFilesystemKillsOnlyAllocatedSubtreeAndRetainsPopulatedCustody(t *testing.T) {
	parent := t.TempDir()
	path := filepath.Join(parent, "podview-radio-fixture")
	if e := os.Mkdir(path, 0700); e != nil {
		t.Fatal(e)
	}
	for _, v := range [][2]string{{"cgroup.kill", "0"}, {"cgroup.events", "populated 1\n"}} {
		if e := os.WriteFile(filepath.Join(path, v[0]), []byte(v[1]), 0600); e != nil {
			t.Fatal(e)
		}
	}
	parentKill := filepath.Join(parent, "cgroup.kill")
	_ = os.WriteFile(parentKill, []byte("untouched"), 0600)
	fd, e := os.Open(path)
	if e != nil {
		t.Fatal(e)
	}
	defer fd.Close()
	removed := ""
	killFD, _ := os.OpenFile(filepath.Join(path, "cgroup.kill"), os.O_WRONLY, 0)
	defer killFD.Close()
	guardianDone := make(chan struct{})
	close(guardianDone)
	c := &radioCgroup{path: path, fd: fd, killFD: killFD, startGuardian: func(*os.File) (*radioGuardian, error) { return &radioGuardian{closed: true, done: guardianDone}, nil }, removeDir: func(p string) error { removed = p; return nil }}
	cmd := exec.Command("/never-launched")
	credential := &syscall.Credential{Uid: 4242, Gid: 4343, Groups: []uint32{4444}}
	cmd.SysProcAttr = &syscall.SysProcAttr{Credential: credential, Ptrace: true}
	if e := c.Configure(cmd); e != nil {
		t.Fatal(e)
	}
	a := cmd.SysProcAttr
	credential.Uid = 0
	credential.Groups[0] = 0
	if a.Credential == nil || a.Credential.Uid != 4242 || a.Credential.Gid != 4343 || a.Credential.Groups[0] != 4444 || a.Ptrace {
		t.Fatal("permanent uid lost, mutable, or unsafe caller attributes preserved")
	}
	if !a.UseCgroupFD || a.CgroupFD != int(fd.Fd()) || !a.Setpgid || a.Pdeathsig != syscall.SIGKILL {
		t.Fatal("child admission not kernel-contained")
	}
	if e := c.Kill(); e != nil {
		t.Fatal(e)
	}
	b, _ := os.ReadFile(filepath.Join(path, "cgroup.kill"))
	if string(b) != "1" {
		t.Fatal("no subtree kill")
	}
	b, _ = os.ReadFile(parentKill)
	if string(b) != "untouched" {
		t.Fatal("parent mutated")
	}
	if c.Close() == nil || removed != "" {
		t.Fatal("populated custody retired")
	}
	_ = os.WriteFile(filepath.Join(path, "cgroup.events"), []byte("populated 0\n"), 0600)
	if e := c.Close(); e != nil || removed != path {
		t.Fatal("exact empty custody not retired", e, removed)
	}
	if c.Configure(exec.Command("/never-launched")) == nil {
		t.Fatal("closed custody reused")
	}
	if e := c.Kill(); e != nil {
		t.Fatal(e)
	}
}
