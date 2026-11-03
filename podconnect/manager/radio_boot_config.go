package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
)

// Radio configuration is a boot-only explicit opt-in. Invalid or incomplete
// radio options leave the existing Spotify configuration exactly unchanged.
type radioBootConfig struct{ Token, Secret, Callback, State string }

func radioBootConfigForRoom(r *Room) *radioBootConfig {
	if r == nil || r.Idx != 0 || !filepath.IsAbs(r.ConfigDir) {
		return nil
	}
	o, err := readRadioOptions()
	if err != nil || !o.Enabled || o.Mode != "builtin" || !radioBootHex(o.NativeToken, 32, 256) || !radioBootHex(o.Secret, 64, 64) || !radioBootHex(o.AdminPassword, 32, 256) {
		return nil
	}
	if !radioWorkerConfigured(o) && (!o.Physical || !filepath.IsAbs(o.Cgroup) || filepath.Clean(o.Cgroup) != o.Cgroup || o.Containment != "" && o.Containment != "cgroup") {
		return nil
	}
	number, err := strconv.Atoi(port)
	if err != nil || number < 1 || number > 65535 {
		return nil
	}
	if o.API == 0 {
		o.API = 3989
	}
	if o.WS == 0 {
		o.WS = 3990
	}
	if o.API < 1 || o.API > 65535 || o.WS < 1 || o.WS > 65535 || o.API == o.WS || o.API == number || o.WS == number {
		return nil
	}
	for _, room := range loadRooms() {
		if room != nil {
			for _, used := range []int{room.GLPort, room.OTPort, room.OTWSPort} {
				if o.API == used || o.WS == used {
					return nil
				}
			}
		}
	}
	return &radioBootConfig{o.NativeToken, o.Secret, fmt.Sprintf("http://127.0.0.1:%d", number), filepath.Join(r.ConfigDir, "radio-handoff.json")}
}
func radioBootHex(s string, min, max int) bool {
	if len(s) < min || len(s) > max {
		return false
	}
	nonzero := false
	for _, r := range s {
		if !(r >= '0' && r <= '9' || r >= 'a' && r <= 'f') {
			return false
		}
		nonzero = nonzero || r != '0'
	}
	return nonzero
}
func radioBootRegularDestination(path string) bool {
	st, err := os.Lstat(path)
	return os.IsNotExist(err) || err == nil && st.Mode().IsRegular()
}

// Secret-bearing boot configuration is atomically replaced with a private
// inode. A symlink or hard link can never redirect the secret write elsewhere.
// Default Spotify rendering retains its existing write/mode behavior.
func writeRadioBootConfig(path string, raw []byte) error {
	if !radioBootRegularDestination(path) {
		return fmt.Errorf("radio configuration destination is not regular")
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".radio-config-")
	if err != nil {
		return err
	}
	temp := file.Name()
	defer os.Remove(temp)
	if _, err = file.Write(raw); err != nil {
		file.Close()
		return err
	}
	if err = file.Sync(); err != nil {
		file.Close()
		return err
	}
	if err = file.Close(); err != nil {
		return err
	}
	if !radioBootRegularDestination(path) {
		return fmt.Errorf("radio configuration destination changed")
	}
	return os.Rename(temp, path)
}
