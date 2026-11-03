package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func radioSettingsTestOptions(t *testing.T, enabled bool) (string, []byte) {
	t.Helper()
	old := dataDir
	dataDir = t.TempDir()
	t.Cleanup(func() { dataDir = old })
	raw, err := json.Marshal(map[string]any{"radio_enabled": enabled, "radio_owner_mode": "builtin", "radio_containment": "worker", "bitrate": "320", "grace_minutes": 3})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dataDir, "options.json")
	if err = os.WriteFile(path, raw, 0644); err != nil {
		t.Fatal(err)
	}
	return path, raw
}

func TestRadioWorkerSettingsExplicitStartupPersistsIdentityAndPreservesHAOptions(t *testing.T) {
	path, original := radioSettingsTestOptions(t, true)
	if _, err := readRadioOptions(); err == nil {
		t.Fatal("read created/admitted absent settings")
	}
	if _, err := os.Stat(radioWorkerSettingsPath()); !os.IsNotExist(err) {
		t.Fatal("read mutated state", err)
	}
	if err := ensureRadioWorkerSettings(); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(radioWorkerSettingsPath())
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		if err = ensureRadioWorkerSettings(); err != nil {
			t.Fatal(err)
		}
		o, err := readRadioOptions()
		if err != nil || !radioWorkerConfigured(o) || o.Physical || !radioBootHex(o.NativeToken, 64, 64) || o.NativeToken == o.Secret || o.NativeToken == o.AdminPassword || o.Secret == o.AdminPassword {
			t.Fatal("invalid private software activation", err)
		}
	}
	after, _ := os.ReadFile(radioWorkerSettingsPath())
	options, _ := os.ReadFile(path)
	info, _ := os.Stat(path)
	settings, _ := os.Stat(radioWorkerSettingsPath())
	if string(before) != string(after) || string(options) != string(original) || info.Mode().Perm() != 0644 || settings.Mode().Perm() != 0600 {
		t.Fatal("startup rotated identity or modified shared Spotify options")
	}
}

func TestRadioWorkerSettingsDisabledDoesNotCreateAnything(t *testing.T) {
	_, _ = radioSettingsTestOptions(t, false)
	if err := ensureRadioWorkerSettings(); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(dataDir)
	if err != nil || len(entries) != 1 || entries[0].Name() != "options.json" {
		t.Fatal("disabled radio wrote state", entries, err)
	}
}

func TestRadioWorkerSettingsUnsafeExistingIdentityFailsWithoutRotation(t *testing.T) {
	for _, kind := range []string{"world-readable", "symlink", "hardlink", "corrupt", "partial"} {
		t.Run(kind, func(t *testing.T) {
			_, _ = radioSettingsTestOptions(t, true)
			if err := ensureRadioWorkerSettings(); err != nil {
				t.Fatal(err)
			}
			path := radioWorkerSettingsPath()
			good, _ := os.ReadFile(path)
			switch kind {
			case "world-readable":
				_ = os.Chmod(path, 0644)
			case "symlink":
				_ = os.Rename(path, path+".original")
				if err := os.Symlink(path+".original", path); err != nil {
					t.Fatal(err)
				}
			case "hardlink":
				if err := os.Link(path, path+".linked"); err != nil {
					t.Fatal(err)
				}
			case "corrupt":
				_ = os.WriteFile(path, []byte(`{"schema":"wrong"}`), 0600)
			case "partial":
				_ = os.WriteFile(path, nil, 0600)
			}
			before, _ := os.ReadFile(path)
			if ensureRadioWorkerSettings() == nil {
				t.Fatal("unsafe identity repaired/admitted")
			}
			if _, err := readRadioOptions(); err == nil {
				t.Fatal("unsafe identity read admitted")
			}
			after, _ := os.ReadFile(path)
			if string(before) != string(after) {
				t.Fatal("unsafe identity overwritten")
			}
			if kind == "symlink" || kind == "hardlink" {
				suffix := ".original"
				if kind == "hardlink" {
					suffix = ".linked"
				}
				peer, _ := os.ReadFile(path + suffix)
				if string(peer) != string(good) {
					t.Fatal("linked peer changed")
				}
			}
		})
	}
}

func TestRadioWorkerSettingsRefusesSecretsInSharedOptions(t *testing.T) {
	for _, field := range []string{"radio_native_token", "radio_handoff_secret", "radio_sender_admin_password", "radio_cgroup_parent"} {
		t.Run(field, func(t *testing.T) {
			path, raw := radioSettingsTestOptions(t, true)
			var options map[string]any
			_ = json.Unmarshal(raw, &options)
			options[field] = "caller-supplied"
			mutated, _ := json.Marshal(options)
			_ = os.WriteFile(path, mutated, 0644)
			if ensureRadioWorkerSettings() == nil {
				t.Fatal("shared secret/delegation accepted")
			}
			if _, err := readRadioOptions(); err == nil {
				t.Fatal("shared secret/delegation read accepted")
			}
			if _, err := os.Stat(radioWorkerSettingsPath()); !os.IsNotExist(err) {
				t.Fatal("invalid options created identity")
			}
		})
	}
}
