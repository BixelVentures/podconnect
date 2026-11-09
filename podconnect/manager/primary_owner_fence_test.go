package main

import (
	"bufio"
	"io"
	"os"
	"os/exec"
	"testing"
)

func TestPrimaryOwnerFenceChild(t *testing.T) {
	if os.Getenv("PODCONNECT_FENCE_TEST_CHILD") != "1" {
		return
	}
	file := os.NewFile(3, "inherited owner fence")
	if _, err := file.Stat(); err != nil || len(os.Getenv(primaryOwnerFenceEnv)) != 32 {
		t.Fatal("primary child has no inherited fence")
	}
	_, _ = os.Stdout.WriteString("held\n")
	_, _ = io.Copy(io.Discard, os.Stdin)
}

func TestPrimaryOwnerFenceLivesWithBothChildren(t *testing.T) {
	dir := t.TempDir()
	owner, err := openPrimaryOwnerFence(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer owner.file.Close()

	start := func() (*exec.Cmd, io.WriteCloser) {
		cmd := owner.bind(exec.Command(os.Args[0], "-test.run=^TestPrimaryOwnerFenceChild$"))
		cmd.Env = append(cmd.Env, "PODCONNECT_FENCE_TEST_CHILD=1")
		release, err := cmd.StdinPipe()
		if err != nil {
			t.Fatal(err)
		}
		ready, err := cmd.StdoutPipe()
		if err != nil {
			t.Fatal(err)
		}
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			_ = release.Close()
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
		})
		if msg, err := bufio.NewReader(ready).ReadString('\n'); err != nil || msg != "held\n" {
			t.Fatalf("child did not inherit owner fence: %q %v", msg, err)
		}
		return cmd, release
	}
	one, releaseOne := start()
	two, releaseTwo := start()
	if err := owner.file.Close(); err != nil {
		t.Fatal(err)
	}

	mustStayHeld := func() {
		t.Helper()
		if other, err := openPrimaryOwnerFence(dir); err == nil {
			_ = other.file.Close()
			t.Fatal("successor acquired the old primary owner's live fence")
		}
	}
	mustStayHeld()
	_ = releaseOne.Close()
	if err := one.Wait(); err != nil {
		t.Fatal(err)
	}
	mustStayHeld()
	_ = releaseTwo.Close()
	if err := two.Wait(); err != nil {
		t.Fatal(err)
	}
	next, err := openPrimaryOwnerFence(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer next.file.Close()
	if next.generation == owner.generation {
		t.Fatal("successor reused the previous acquisition generation")
	}
}
