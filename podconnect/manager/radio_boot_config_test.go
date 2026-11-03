package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRadioBootConfigDefaultBytesAndExplicitPrivatePrimaryOnly(t *testing.T) {
	rooms, _ := setupLocalAliasRooms(t)
	primary := *rooms[0]
	primary.fill()
	root := t.TempDir()
	primary.ConfigDir = filepath.Join(root, "native")
	primary.OwnDir = filepath.Join(root, "own")
	primary.OwnConf = filepath.Join(root, "owntone.conf")
	options := filepath.Join(dataDir, "options.json")
	render := func() (string, string) {
		t.Helper()
		if err := renderGLConfig(&primary); err != nil {
			t.Fatal(err)
		}
		if err := renderOTConfig(&primary); err != nil {
			t.Fatal(err)
		}
		gl, e := os.ReadFile(filepath.Join(primary.ConfigDir, "config.yml"))
		if e != nil {
			t.Fatal(e)
		}
		ot, e := os.ReadFile(primary.OwnConf)
		if e != nil {
			t.Fatal(e)
		}
		return string(gl), string(ot)
	}
	if err := os.WriteFile(options, []byte(`{}`), 0600); err != nil {
		t.Fatal(err)
	}
	baselineGL, baselineOT := render()
	good := map[string]any{"radio_enabled": true, "radio_owner_mode": "builtin", "radio_physical_accepted": true, "radio_native_token": strings.Repeat("a", 64), "radio_handoff_secret": strings.Repeat("b", 64), "radio_sender_admin_password": strings.Repeat("c", 64), "radio_cgroup_parent": "/sys/fs/cgroup/radio-fixture"}
	for _, mutation := range []func(map[string]any){func(o map[string]any) { o["radio_enabled"] = false }, func(o map[string]any) { o["radio_physical_accepted"] = false }, func(o map[string]any) { o["radio_owner_mode"] = "external" }, func(o map[string]any) { delete(o, "radio_native_token") }, func(o map[string]any) { o["radio_handoff_secret"] = "malformed" }, func(o map[string]any) { o["radio_native_token"] = strings.Repeat("0", 64) }, func(o map[string]any) { delete(o, "radio_sender_admin_password") }, func(o map[string]any) { o["radio_cgroup_parent"] = "" }, func(o map[string]any) { o["radio_sender_api_port"] = primary.OTPort }} {
		o := map[string]any{}
		for k, v := range good {
			o[k] = v
		}
		mutation(o)
		raw, _ := json.Marshal(o)
		if err := os.WriteFile(options, raw, 0600); err != nil {
			t.Fatal(err)
		}
		gl, ot := render()
		if gl != baselineGL || ot != baselineOT {
			t.Fatal("incomplete radio configuration changed Spotify bytes")
		}
	}
	raw, _ := json.Marshal(good)
	if err := os.WriteFile(options, raw, 0600); err != nil {
		t.Fatal(err)
	}
	gl, ot := render()
	if !strings.HasPrefix(gl, baselineGL) || !strings.Contains(gl, "radio_handoff_control_token: "+`"`+strings.Repeat("a", 64)+`"`+"\n") || !strings.Contains(gl, "radio_handoff_owner_url: "+`"http://127.0.0.1:`+port+`"`) || !strings.Contains(ot, "radio_handoff_secret = "+`"`+strings.Repeat("b", 64)+`"`) {
		t.Fatal("explicit boot configuration missing")
	}
	if !strings.Contains(ot, "radio_handoff_control_token = "+`"`+strings.Repeat("a", 64)+`"`) {
		t.Fatal("old-C radio RPC authentication missing")
	}
	for _, path := range []string{filepath.Join(primary.ConfigDir, "config.yml"), primary.OwnConf} {
		st, e := os.Stat(path)
		if e != nil || st.Mode().Perm() != 0600 {
			t.Fatal("secret configuration not private", path, e)
		}
	}
	secondary := primary
	secondary.Idx = 1
	if radioBootConfigForRoom(&secondary) != nil {
		t.Fatal("radio modified secondary Spotify engine")
	}
}
func TestRadioBootSecretWriterRefusesSymlinkWithoutChangingTarget(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "spotify.conf")
	path := filepath.Join(root, "config.yml")
	if err := os.WriteFile(target, []byte("original"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, path); err != nil {
		t.Fatal(err)
	}
	if radioBootRegularDestination(path) || writeRadioBootConfig(path, []byte("secret")) == nil {
		t.Fatal("secret writer followed symlink")
	}
	got, _ := os.ReadFile(target)
	st, _ := os.Stat(target)
	if string(got) != "original" || st.Mode().Perm() != 0644 {
		t.Fatal("symlink target changed")
	}
}

func TestRadioBootSecretWriteReplacesHardLinkWithoutTouchingPeer(t *testing.T) {
	root := t.TempDir()
	peer := filepath.Join(root, "other-engine.conf")
	path := filepath.Join(root, "config.yml")
	if err := os.WriteFile(peer, []byte("other Spotify config"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(peer, path); err != nil {
		t.Fatal(err)
	}
	if err := writeRadioBootConfig(path, []byte("radio token")); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(peer)
	st, _ := os.Stat(peer)
	fresh, _ := os.Stat(path)
	if string(got) != "other Spotify config" || st.Mode().Perm() != 0644 || fresh.Mode().Perm() != 0600 {
		t.Fatal("secret write changed linked peer")
	}
}

func TestRadioWorkerBootUsesPrivateIdentityWithoutClaimingPhysicalAcceptance(t *testing.T) {
	rooms, _ := setupLocalAliasRooms(t)
	primary := *rooms[0]
	primary.fill()
	primary.ConfigDir = filepath.Join(t.TempDir(), "native")
	options := filepath.Join(dataDir, "options.json")
	if err := os.WriteFile(options, []byte(`{"radio_enabled":true,"radio_owner_mode":"builtin","radio_containment":"worker"}`), 0644); err != nil {
		t.Fatal(err)
	}
	if radioBootConfigForRoom(&primary) != nil {
		t.Fatal("renderer created private credentials")
	}
	if err := ensureRadioWorkerSettings(); err != nil {
		t.Fatal(err)
	}
	o, err := readRadioOptions()
	if err != nil || o.Physical {
		t.Fatal("software startup asserted physical acceptance", err)
	}
	binding := radioBootConfigForRoom(&primary)
	if binding == nil || binding.Token != o.NativeToken || binding.Secret != o.Secret {
		t.Fatal("worker private identity not bound to existing Native owner")
	}
	if err := renderGLConfig(&primary); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(primary.ConfigDir, "config.yml"))
	if err != nil || !strings.Contains(string(raw), `radio_handoff_control_token: "`+o.NativeToken+`"`) {
		t.Fatal("worker token absent from private Native configuration", err)
	}
	primary.Idx = 1
	if radioBootConfigForRoom(&primary) != nil {
		t.Fatal("worker changed secondary Spotify engine")
	}
}
