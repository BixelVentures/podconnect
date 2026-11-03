package main

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRadioBuiltinPhysicalFalseDoesNotPrepareLaunchOrJournal(t *testing.T) {
	old := dataDir
	defer func() { dataDir = old }()
	dataDir = t.TempDir()
	options := radioProducerBootstrapOptions{Enabled: true, Mode: "builtin", Physical: false, NativeToken: strings.Repeat("a", 32), Secret: strings.Repeat("b", 64), AdminPassword: strings.Repeat("c", 32), Cgroup: "/unavailable/delegation"}
	raw, _ := json.Marshal(options)
	if e := os.WriteFile(filepath.Join(dataDir, "options.json"), raw, 0600); e != nil {
		t.Fatal(e)
	}
	owner := newRadioCoordinator(nil)
	startConfiguredRadioBuiltin(context.Background(), owner)
	entries, e := os.ReadDir(dataDir)
	if e != nil || len(entries) != 1 || entries[0].Name() != "options.json" || owner.backend != nil || owner.snapshot().Enabled {
		t.Fatal("physicalfalse startup performed preparation", entries, e)
	}
}
func TestRadioNativeCallbackUsesDedicatedControlToken(t *testing.T) {
	old := dataDir
	defer func() { dataDir = old }()
	dataDir = t.TempDir()
	native := strings.Repeat("a", 32)
	raw, _ := json.Marshal(map[string]any{"radio_owner_mode": "builtin", "radio_native_token": native, "attention_token": "bridge-token"})
	_ = os.WriteFile(filepath.Join(dataDir, "options.json"), raw, 0600)
	for token, want := range map[string]bool{native: true, "bridge-token": false, "": false} {
		request := httptest.NewRequest("POST", "/api/radio/native-preempt", nil)
		request.Header.Set("X-PodConnect-Token", token)
		if got := radioNativePreemptAuthed(request); got != want {
			t.Fatal("wrong callback authentication", got, want)
		}
	}
}
func TestRadioSenderRoleConfigIsPrivateAndLoopbackOnly(t *testing.T) {
	root, _ := filepath.EvalSymlinks(t.TempDir())
	sender, e := newRadioSender(root, 4989, 4990, nil)
	if e != nil {
		t.Fatal(e)
	}
	sender.radioConfig = &radioProducerBootstrapOptions{AdminPassword: strings.Repeat("a", 32), Secret: strings.Repeat("b", 64), stage: filepath.Join(sender.root, "pairing-stage.json"), controller: "AABBCCDDEEFF0011"}
	path, e := sender.prepare()
	if e != nil {
		t.Fatal(e)
	}
	raw, e := os.ReadFile(path)
	if e != nil {
		t.Fatal(e)
	}
	for _, required := range []string{`radio_controller_id = "AABBCCDDEEFF0011"`, `trusted_networks = {}`, `bind_address = "127.0.0.1"`, `radio_pairing_import_path = "` + sender.radioConfig.stage + `"`, `pipe_autostart = false`} {
		if !strings.Contains(string(raw), required) {
			t.Fatal("missing independent role setting", required)
		}
	}
	info, _ := os.Stat(path)
	if info.Mode().Perm() != 0600 {
		t.Fatal("secret config not private", info.Mode())
	}
}

func TestRadioBuiltinRefusesSharedControlTokenFileWithoutChangingIt(t *testing.T) {
	old := dataDir
	defer func() { dataDir = old }()
	dataDir = t.TempDir()
	options := radioProducerBootstrapOptions{Enabled: true, Mode: "builtin", Physical: true, NativeToken: strings.Repeat("a", 32), Secret: strings.Repeat("b", 64), AdminPassword: strings.Repeat("c", 32), Cgroup: "/dedicated/not-launched"}
	raw, _ := json.Marshal(options)
	path := filepath.Join(dataDir, "options.json")
	if err := os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := readRadioProducerBootstrapOptions(); err != nil {
		t.Fatal("private opt-in rejected", err)
	}
	if err := os.Chmod(path, 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := readRadioProducerBootstrapOptions(); err == nil {
		t.Fatal("world-readable Native control token admitted")
	}
	got, _ := os.ReadFile(path)
	info, _ := os.Stat(path)
	if string(got) != string(raw) || info.Mode().Perm() != 0644 {
		t.Fatal("radio rewrote shared options")
	}
}
