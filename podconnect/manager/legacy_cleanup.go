package main

// Startup cleanup of everything the retired radio custody design persisted.
//
// Speakers 0.26.65-0.26.94 kept radio holds, parks, custody slots, handoff
// journals and a primary owner lock across restarts. Any one of them could make
// Native or OwnTone refuse an ordinary Spotify pause, volume or start. Radio is
// now a plain stream that never persists custody, so all of it is removed on
// every start, unconditionally, before an engine is launched. Nothing here
// touches Spotify credentials, the room list or ordinary attention (ducking).
import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// legacyRadioPaths are data-root entries only the retired radio design wrote.
var legacyRadioPaths = []string{
	"radio-sender",
	"radio-owner-journal",
	"radio-settings.json",
	".podconnect-primary-owner",
}

// legacyRadioSQL drops radio holds, parks and custody slots from an OwnTone
// database, plus a start journal written by the radio-era OwnTone. The latter
// carries fields the current OwnTone rejects, which would otherwise close
// Spotify's START gate for good. Each statement runs on its own, so a database
// without the start-guard table still has its admin rows removed.
var legacyRadioSQL = []string{
	"DELETE FROM admin WHERE key LIKE 'podconnect.radio%' OR key LIKE 'podconnect.primary-%';",
	`DELETE FROM native_start_guard_v1 WHERE payload LIKE '%"primary_owner_fence"%' OR payload LIKE '%"radio%';`,
}

// sqliteCommand is replaced in tests.
var sqliteCommand = func(ctx context.Context, db, statement string) error {
	return exec.CommandContext(ctx, "sqlite3", "-batch", db, statement).Run()
}

func cleanupLegacyRadioState(root string, rooms []*Room) {
	if root == "" || !filepath.IsAbs(root) {
		return
	}
	removed := 0
	remove := func(path string) {
		if _, err := os.Lstat(path); err != nil {
			return
		}
		if err := os.RemoveAll(path); err != nil {
			log.Printf("radio: legacy cleanup could not remove a stale entry: %v", err)
			return
		}
		removed++
	}
	for _, name := range legacyRadioPaths {
		remove(filepath.Join(root, name))
	}
	if entries, err := os.ReadDir(root); err == nil {
		for _, entry := range entries {
			if strings.HasPrefix(entry.Name(), ".radio-pair-export-") {
				remove(filepath.Join(root, entry.Name()))
			}
		}
	}
	databases := 0
	for _, room := range rooms {
		if room == nil {
			continue
		}
		if room.ConfigDir != "" {
			if entries, err := os.ReadDir(room.ConfigDir); err == nil {
				for _, entry := range entries {
					if strings.HasPrefix(entry.Name(), "radio-handoff") {
						remove(filepath.Join(room.ConfigDir, entry.Name()))
					}
				}
			}
		}
		if room.OwnDir == "" {
			continue
		}
		db := filepath.Join(room.OwnDir, "songs3.db")
		if info, err := os.Stat(db); err != nil || !info.Mode().IsRegular() {
			continue
		}
		cleaned := true
		for i, statement := range legacyRadioSQL {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			// The start-guard table may not exist yet; only the admin rows count.
			if err := sqliteCommand(ctx, db, statement); err != nil && i == 0 {
				cleaned = false
			}
			cancel()
		}
		if cleaned {
			databases++
		}
	}
	log.Printf("radio: legacy custody cleanup done (stale entries removed: %d, databases cleaned: %d)", removed, databases)
}
