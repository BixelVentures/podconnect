package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestRadioSenderIndependentConfigurationAndPortCollision(t *testing.T) {
	root := t.TempDir()
	s, e := newRadioSender(root, 4691, 4692, []*Room{{OTPort: 3689}})
	if e != nil {
		t.Fatal(e)
	}
	p, e := s.prepare()
	if e != nil {
		t.Fatal(e)
	}
	b, e := os.ReadFile(p)
	if e != nil {
		t.Fatal(e)
	}
	text := string(b)
	if !strings.Contains(text, "/radio-sender/radio-songs.db") || !strings.Contains(text, "pipe_autostart = false") || strings.Contains(text, "spotify") {
		t.Fatal(text)
	}
	if _, e := newRadioSender(root, 3689, 4692, []*Room{{OTPort: 3689}}); e == nil {
		t.Fatal("Spotify port accepted")
	}
}
func TestRadioSenderNoProofNoLaunchAndNoAutoRestart(t *testing.T) {
	s, _ := newRadioSender(t.TempDir(), 4691, 4692, nil)
	s.newContainment = func() (radioProcessContainment, error) { return &radioTestContainment{}, nil }
	launches := 0
	s.newContainment = func() (radioProcessContainment, error) { return &radioTestContainment{}, nil }
	s.prepareOwnership = func(string) error { return nil }
	s.makeCommand = func(ctx context.Context, _ string) *exec.Cmd {
		launches++
		return exec.CommandContext(ctx, "/usr/bin/true")
	}
	if s.startIdle(context.Background(), nil) == nil || launches != 0 {
		t.Fatal("unproved sender launched")
	}
	if e := s.startIdle(context.Background(), radioRequiredProofs); e != nil {
		t.Fatal(e)
	}
	select {
	case <-s.done:
	case <-time.After(time.Second):
		t.Fatal("test process did not exit")
	}
	if launches != 1 {
		t.Fatal("sender restarted")
	}
	if e := s.stop(context.Background()); e != nil {
		t.Fatal(e)
	}
}
func TestRadioSenderRefusesSymlinkIntoOtherState(t *testing.T) {
	root := t.TempDir()
	other := t.TempDir()
	if e := os.Symlink(other, filepath.Join(root, "radio-sender")); e != nil {
		t.Fatal(e)
	}
	s, _ := newRadioSender(root, 4691, 4692, nil)
	if _, e := s.prepare(); e == nil {
		t.Fatal("aliased sender state accepted")
	}
}

func TestRadioSenderRejectsDescendantAliasesWithoutTouchingSpotifyBytes(t *testing.T) {
	for _, path := range []string{"owntone.conf", "radio-songs.db", "radio-songs.db-wal", "radio.log", "media", "cache", "cache/nested.db"} {
		t.Run(path, func(t *testing.T) {
			root := t.TempDir()
			other := t.TempDir()
			target := filepath.Join(other, "spotify.conf")
			original := []byte("existing spotify bytes must survive")
			if e := os.WriteFile(target, original, 0600); e != nil {
				t.Fatal(e)
			}
			s, _ := newRadioSender(root, 4691, 4692, nil)
			if e := os.MkdirAll(filepath.Dir(filepath.Join(s.root, path)), 0700); e != nil {
				t.Fatal(e)
			}
			linkTarget := target
			if path == "media" || path == "cache" {
				linkTarget = other
			}
			if e := os.Symlink(linkTarget, filepath.Join(s.root, path)); e != nil {
				t.Fatal(e)
			}
			launched := false
			s.newContainment = func() (radioProcessContainment, error) { return &radioTestContainment{}, nil }
			s.prepareOwnership = func(string) error { return nil }
			s.makeCommand = func(ctx context.Context, _ string) *exec.Cmd {
				launched = true
				return exec.CommandContext(ctx, "/usr/bin/true")
			}
			if e := s.startIdle(context.Background(), radioRequiredProofs); e == nil || launched {
				t.Fatal("descendant alias admitted", e)
			}
			actual, e := os.ReadFile(target)
			if e != nil || string(actual) != string(original) {
				t.Fatal("Spotify target bytes changed", e, string(actual))
			}
		})
	}
}
func TestRadioSenderNeverTruncatesExistingConfig(t *testing.T) {
	s, _ := newRadioSender(t.TempDir(), 4691, 4692, nil)
	if e := os.MkdirAll(s.root, 0700); e != nil {
		t.Fatal(e)
	}
	path := filepath.Join(s.root, "owntone.conf")
	original := []byte("foreign config")
	if e := os.WriteFile(path, original, 0600); e != nil {
		t.Fatal(e)
	}
	if _, e := s.prepare(); e == nil {
		t.Fatal("foreign config reused")
	}
	actual, _ := os.ReadFile(path)
	if string(actual) != string(original) {
		t.Fatal("existing inode truncated")
	}
}

func TestRadioSenderRefusesHardlinkedDatabaseBeforeOwnershipOrInitialization(t *testing.T) {
	root := t.TempDir()
	original := filepath.Join(root, "spotify.db")
	if err := os.WriteFile(original, []byte("unchanged Spotify sentinel"), 0640); err != nil {
		t.Fatal(err)
	}
	before, _ := os.Stat(original)
	beforeStat := before.Sys().(*syscall.Stat_t)
	sender, err := newRadioSender(root, 4691, 4692, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.MkdirAll(sender.root, 0700); err != nil {
		t.Fatal(err)
	}
	if err = os.Link(original, filepath.Join(sender.root, "radio-songs.db")); err != nil {
		t.Fatal(err)
	}
	touched := false
	sender.prepareOwnership = func(string) error { touched = true; return nil }
	sender.newContainment = func() (radioProcessContainment, error) { return &radioTestContainment{}, nil }
	sender.makeCommand = func(context.Context, string) *exec.Cmd { t.Fatal("constructed command for aliased DB"); return nil }
	if sender.startIdleProcess(context.Background()) == nil {
		t.Fatal("hardlink accepted")
	}
	raw, _ := os.ReadFile(original)
	after, _ := os.Stat(original)
	afterStat := after.Sys().(*syscall.Stat_t)
	if touched || string(raw) != "unchanged Spotify sentinel" || after.Mode() != before.Mode() || afterStat.Uid != beforeStat.Uid || afterStat.Gid != beforeStat.Gid {
		t.Fatal("radio changed aliased Spotify state before refusing it")
	}
}
