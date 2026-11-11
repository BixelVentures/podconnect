package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The radio-era stack persisted holds for Svend's and Frida's HomePods that made
// OwnTone answer every /radio-handoff/reacquire with 409. Startup must drop them
// (and every other radio custody artifact) before any engine starts.
func TestStartupCleanupDropsLegacyRadioHoldsFromOwnToneDatabase(t *testing.T) {
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("sqlite3 CLI not installed")
	}
	root := t.TempDir()
	room := &Room{ID: "r0", Idx: 0, ConfigDir: filepath.Join(root, "go-librespot"), OwnDir: filepath.Join(root, "owntone")}
	for _, dir := range []string{room.ConfigDir, room.OwnDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	db := filepath.Join(room.OwnDir, "songs3.db")
	seed := `CREATE TABLE admin (key TEXT PRIMARY KEY, value TEXT);
CREATE TABLE native_start_guard_v1 (singleton INTEGER PRIMARY KEY CHECK(singleton=1),token TEXT NOT NULL,c_process TEXT NOT NULL,boot_id TEXT NOT NULL,pid TEXT NOT NULL,start_ticks TEXT NOT NULL,payload TEXT NOT NULL);
INSERT INTO admin VALUES('podconnect.radio_handoff.v1','{"holds":{"47226606268305":{},"226915153478190":{}}}');
INSERT INTO admin VALUES('podconnect.radio-primary-custody.v1.47226606268305','{}');
INSERT INTO admin VALUES('podconnect.radio-primary-custody.v1.226915153478190','{}');
INSERT INTO admin VALUES('podconnect.radio_absence.v1','{}');
INSERT INTO admin VALUES('podconnect.primary-local-park.v1','{}');
INSERT INTO admin VALUES('schema_version','22');
INSERT INTO admin VALUES('podconnect.attention.v1','keep');
INSERT INTO native_start_guard_v1 VALUES(1,'t','c','b','1','2','{"schema":"podconnect.output_start_journal.v1","primary_owner_fence":{"device":1,"inode":2,"generation":"x"}}');`
	if out, err := exec.Command("sqlite3", db, seed).CombinedOutput(); err != nil {
		t.Fatalf("seed: %v %s", err, out)
	}
	stale := []string{
		filepath.Join(root, "radio-sender", "radio-songs.db"),
		filepath.Join(root, "radio-owner-journal", "journal"),
		filepath.Join(root, "radio-settings.json"),
		filepath.Join(root, ".podconnect-primary-owner"),
		filepath.Join(root, ".radio-pair-export-123", "pairing.json"),
		filepath.Join(room.ConfigDir, "radio-handoff.json"),
	}
	for _, path := range stale {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	keep := []string{filepath.Join(room.ConfigDir, "credentials.json"), filepath.Join(root, "rooms.json"), filepath.Join(root, "rooms", "r0", "attention-custody.json")}
	for _, path := range keep {
		_ = os.MkdirAll(filepath.Dir(path), 0o755)
		if err := os.WriteFile(path, []byte("{}"), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	cleanupLegacyRadioState(root, []*Room{room})
	cleanupLegacyRadioState(root, []*Room{room}) // idempotent

	out, err := exec.Command("sqlite3", db, "SELECT key FROM admin ORDER BY key; SELECT count(*) FROM native_start_guard_v1;").CombinedOutput()
	if err != nil {
		t.Fatal(err, string(out))
	}
	if got := strings.TrimSpace(string(out)); got != "podconnect.attention.v1\nschema_version\n0" {
		t.Fatalf("remaining rows:\n%s", got)
	}
	for _, path := range stale {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("stale radio state survived: %s", strings.TrimPrefix(path, root))
		}
	}
	for _, path := range keep {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("cleanup removed unrelated state: %s", strings.TrimPrefix(path, root))
		}
	}
}

func TestStartupCleanupKeepsCurrentStartJournal(t *testing.T) {
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("sqlite3 CLI not installed")
	}
	root := t.TempDir()
	room := &Room{ID: "r0", OwnDir: filepath.Join(root, "owntone")}
	_ = os.MkdirAll(room.OwnDir, 0o755)
	db := filepath.Join(room.OwnDir, "songs3.db")
	seed := `CREATE TABLE admin (key TEXT PRIMARY KEY, value TEXT);
CREATE TABLE native_start_guard_v1 (singleton INTEGER PRIMARY KEY CHECK(singleton=1),token TEXT NOT NULL,c_process TEXT NOT NULL,boot_id TEXT NOT NULL,pid TEXT NOT NULL,start_ticks TEXT NOT NULL,payload TEXT NOT NULL);
INSERT INTO native_start_guard_v1 VALUES(1,'t','c','b','1','2','{"schema":"podconnect.output_start_journal.v1","token":"t"}');`
	if out, err := exec.Command("sqlite3", db, seed).CombinedOutput(); err != nil {
		t.Fatalf("seed: %v %s", err, out)
	}
	cleanupLegacyRadioState(root, []*Room{room})
	out, _ := exec.Command("sqlite3", db, "SELECT count(*) FROM native_start_guard_v1;").CombinedOutput()
	if strings.TrimSpace(string(out)) != "1" {
		t.Fatal("cleanup dropped a current (non-radio) start journal")
	}
}

func TestStartupCleanupToleratesMissingSqlite(t *testing.T) {
	old := sqliteCommand
	t.Cleanup(func() { sqliteCommand = old })
	var statements []string
	sqliteCommand = func(_ context.Context, db, statement string) error {
		statements = append(statements, statement)
		return exec.ErrNotFound
	}
	root := t.TempDir()
	room := &Room{ID: "r0", OwnDir: filepath.Join(root, "owntone")}
	_ = os.MkdirAll(room.OwnDir, 0o755)
	_ = os.WriteFile(filepath.Join(room.OwnDir, "songs3.db"), nil, 0o644)
	_ = os.WriteFile(filepath.Join(root, ".podconnect-primary-owner"), nil, 0o600)
	cleanupLegacyRadioState(root, []*Room{room})
	if len(statements) != len(legacyRadioSQL) {
		t.Fatal("cleanup did not attempt every statement")
	}
	if _, err := os.Stat(filepath.Join(root, ".podconnect-primary-owner")); !os.IsNotExist(err) {
		t.Fatal("file cleanup depended on sqlite")
	}
}

// Spotify control must not pass through any radio custody gate again: the
// image builds Native and OwnTone without a single radio patch.
func TestImageBuildsNativeAndOwnToneWithoutRadioPatches(t *testing.T) {
	for _, path := range []string{"../Dockerfile", "../patches/native-stack.sha256"} {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(raw), "radio") {
			t.Fatalf("%s still references a radio patch", path)
		}
	}
	matches, _ := filepath.Glob("../patches/radio-*")
	if len(matches) != 0 {
		t.Fatalf("radio patches still present: %v", matches)
	}
}
