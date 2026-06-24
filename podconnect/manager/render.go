// Config rendering: write a room's go-librespot config.yml and OwnTone owntone.conf from its Room.
// These templates were heredocs in init-podconnect; moving them here lets the manager spawn rooms
// live (no add-on restart). The go-librespot + OwnTone settings are byte-for-byte the same as the
// single-room setup (external_volume, volume_steps:100, initial_volume:35, pipe backend, AirPlay-2
// disabled local audio, start_buffer_ms from buffer_ms) — only the ports/paths/names vary per room.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

// readBitrate is the bitrate option ("320" default), shared by every room's go-librespot config.
func readBitrate() string {
	b, err := os.ReadFile(filepath.Join(dataDir, "options.json"))
	if err != nil {
		return "320"
	}
	var o struct {
		Bitrate string `json:"bitrate"`
	}
	if json.Unmarshal(b, &o) != nil || o.Bitrate == "" {
		return "320"
	}
	return o.Bitrate
}

// persistentConnect is the EXPERIMENTAL opt-in option. When true, a room's go-librespot runs with
// zeroconf discovery OFF and relies on its persisted credentials to reconnect as a stable, registered
// Connect device — the model real hardware (Sonos etc.) uses, which avoids the multi-instance
// same-account discovery race (librespot #793). Default false = today's zeroconf behavior unchanged.
// NOTE: a room must have been claimed once (so credentials are cached) BEFORE this helps; an
// unclaimed room with discovery off can't be picked. Toggle off to revert instantly.
func persistentConnect() bool {
	b, err := os.ReadFile(filepath.Join(dataDir, "options.json"))
	if err != nil {
		return false
	}
	var o struct {
		PersistentConnect bool `json:"persistent_connect"`
	}
	return json.Unmarshal(b, &o) == nil && o.PersistentConnect
}

// experimentAliases is the PROBE opt-in (Stage A). When true, the PRIMARY room's go-librespot
// advertises connectAliases() as Spotify device-aliases (multiroom zones) — one session, N selectable
// devices in the Connect menu. Requires the GL_ALIASES=1 image (the device-aliases fork). Default
// false = unchanged. See podconnect/patches/aliases-v0.7.3.patch.
func experimentAliases() bool {
	b, err := os.ReadFile(filepath.Join(dataDir, "options.json"))
	if err != nil {
		return false
	}
	var o struct {
		ExperimentAliases bool `json:"experiment_aliases"`
	}
	return json.Unmarshal(b, &o) == nil && o.ExperimentAliases
}

// connectAliases is the list of room names to advertise as aliases in the Spotify Connect menu when
// experimentAliases() is on. Empty = no aliases (falls back to the normal single device name).
func connectAliases() []string {
	b, err := os.ReadFile(filepath.Join(dataDir, "options.json"))
	if err != nil {
		return nil
	}
	var o struct {
		ConnectAliases []string `json:"connect_aliases"`
	}
	if json.Unmarshal(b, &o) != nil {
		return nil
	}
	out := make([]string, 0, len(o.ConnectAliases))
	for _, s := range o.ConnectAliases {
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, s)
		}
	}
	return out
}

// renderGLConfig writes the room's go-librespot config.yml. Identical settings to the single-room
// build; the device_id is seeded/reused per room so renaming never spawns a ghost Connect device.
func renderGLConfig(r *Room) error {
	if err := os.MkdirAll(r.ConfigDir, 0o755); err != nil {
		return err
	}
	devID := roomDeviceID(r)
	// EXPERIMENT (persistent_connect): the documented headless model — discovery OFF + credentials
	// type "interactive", which REUSES the credentials persisted during the earlier zeroconf claim (no
	// browser prompt). That makes the room a stable, registered Connect device like real hardware,
	// avoiding the multi-instance same-account discovery race (librespot #793). Default = zeroconf
	// discovery ON (unchanged). A room must be claimed once (persist_credentials caches) BEFORE this.
	zeroconfEnabled := "true"
	credsBlock := "credentials:\n  type: zeroconf\n  zeroconf:\n    persist_credentials: true"
	if persistentConnect() {
		zeroconfEnabled = "false"
		credsBlock = "credentials:\n  type: interactive"
	}
	// PROBE (Stage A): the primary room's engine advertises device-aliases (multiroom zones). Only the
	// primary room (Idx 0) carries them — one session, N selectable rooms. No-op without the fork image.
	aliasesBlock := ""
	if experimentAliases() && r.Idx == 0 {
		if al := connectAliases(); len(al) > 0 {
			var b strings.Builder
			b.WriteString("device_aliases:\n")
			for _, name := range al {
				fmt.Fprintf(&b, "  - %q\n", name)
			}
			aliasesBlock = b.String()
		}
	}
	cfg := fmt.Sprintf(`device_name: "%s"
device_id: "%s"
device_type: speaker
bitrate: %s
zeroconf_enabled: %s
zeroconf_backend: avahi
%s
%saudio_backend: pipe
audio_output_pipe: %s
audio_output_pipe_format: s16le
external_volume: true
volume_steps: 100
initial_volume: 35
server:
  enabled: true
  address: 0.0.0.0
  port: %d
`, r.Name, devID, roomBitrate(r), zeroconfEnabled, credsBlock, aliasesBlock, r.Pipe, r.GLPort)
	return os.WriteFile(filepath.Join(r.ConfigDir, "config.yml"), []byte(cfg), 0o644)
}

// renderOTConfig writes the room's owntone.conf. db/cache/log live under r.OwnDir (legacy
// /data/owntone for room 0, per-room tree otherwise). Unique ports + library.name per instance,
// MPD disabled, local audio disabled (AirPlay only), start_buffer_ms from the buffer_ms option.
func renderOTConfig(r *Room) error {
	if err := os.MkdirAll(filepath.Dir(r.OwnConf), 0o755); err != nil {
		return err
	}
	if err := os.MkdirAll(r.OwnDir, 0o755); err != nil {
		return err
	}
	mediaDir := filepath.Dir(r.Pipe) // /srv/media or /srv/media/rooms/<id>
	cfg := fmt.Sprintf(`general {
	uid = "owntone"
	db_path = "%s/songs3.db"
	cache_dir = "%s"
	logfile = "%s/owntone.log"
	websocket_port = %d
	# OwnTone's default output pre-buffer is 2250 ms. We default to 500 ms (snappier skips) and expose
	# it as the buffer_ms option: LOWER = snappier but more underruns; HIGHER (700-1000) = rock-solid.
	# The bulk of the ~2 s HomePod skip latency is AirPlay 2's inherent sync buffer, NOT this knob.
	start_buffer_ms = %d
}
library {
	port = %d
	name = "%s"
	directories = { "%s" }
	pipe_autostart = true
}
audio {
	type = "disabled"
}
mpd {
	port = 0
}
`, r.OwnDir, r.OwnDir, r.OwnDir, r.OTWSPort, readBufferMs(), r.OTPort, r.Name, mediaDir)
	return os.WriteFile(r.OwnConf, []byte(cfg), 0o644)
}

// ensurePipes creates the room's audio pipe(s) (FIFO) if absent. OwnTone's pipe_autostart picks up
// writes to <media>/<name> and writes to <name>.metadata are read for now-playing.
func ensurePipes(r *Room) error {
	if err := os.MkdirAll(filepath.Dir(r.Pipe), 0o755); err != nil {
		return err
	}
	for _, p := range []string{r.Pipe, r.Pipe + ".metadata"} {
		if !isFIFO(p) {
			if err := mkfifo(p, 0o666); err != nil {
				return fmt.Errorf("mkfifo %s: %w", p, err)
			}
		}
	}
	return nil
}

// chownOwnDir hands the room's OwnTone data dir to the owntone user (it drops privileges to that
// uid). Best-effort — logged, not fatal — to mirror the old `chown ... || true`.
func chownOwnDir(r *Room) {
	if err := exec.Command("chown", "-R", "owntone:owntone", r.OwnDir).Run(); err != nil {
		log.Printf("rooms[%s]: chown owntone dir failed (non-fatal): %v", r.ID, err)
	}
}

// isFIFO reports whether p exists and is a named pipe.
func isFIFO(p string) bool {
	fi, err := os.Stat(p)
	if err != nil {
		return false
	}
	return fi.Mode()&os.ModeNamedPipe != 0
}

// mkfifo shells out to the coreutils mkfifo (present in the image) — stdlib has no portable wrapper
// and we avoid pulling in golang.org/x/sys just for unix.Mkfifo.
func mkfifo(path string, mode os.FileMode) error {
	return exec.Command("mkfifo", "-m", strconv.FormatInt(int64(mode.Perm()), 8), path).Run()
}
