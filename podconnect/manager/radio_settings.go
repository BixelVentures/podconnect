package main

// Worker secrets belong to the manager's private state, never to the shared HA
// options file or the radio service UID. Reads never create or rotate secrets.
import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sync"
	"syscall"
)

const radioWorkerSettingsSchema = "podconnect.radio-worker-settings.v1"

var radioSettingsMu sync.Mutex

type radioWorkerSettings struct {
	Schema string `json:"schema"`
	Token  string `json:"native_token"`
	Secret string `json:"handoff_secret"`
	Admin  string `json:"sender_admin_password"`
}

func radioWorkerConfigured(o radioProducerBootstrapOptions) bool {
	return o.Enabled && o.Mode == "builtin" && o.Containment == "worker"
}

func radioRawOptions() (radioProducerBootstrapOptions, error) {
	var o radioProducerBootstrapOptions
	raw, err := os.ReadFile(filepath.Join(dataDir, "options.json"))
	if err != nil || json.Unmarshal(raw, &o) != nil {
		return o, errRadioUnavailable
	}
	return o, nil
}

func radioWorkerSettingsPath() string { return filepath.Join(dataDir, "radio-settings.json") }

func readRadioWorkerSettings() (radioWorkerSettings, error) {
	var s radioWorkerSettings
	fd, err := syscall.Open(radioWorkerSettingsPath(), syscall.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_CLOEXEC, 0)
	if err != nil {
		return s, err
	}
	file := os.NewFile(uintptr(fd), "private-radio-settings")
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return s, errRadioUnavailable
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !info.Mode().IsRegular() || info.Mode().Perm() != 0600 || !ok || stat.Nlink != 1 || stat.Uid != uint32(os.Geteuid()) || info.Size() > 4096 {
		return s, errRadioUnavailable
	}
	raw, err := io.ReadAll(io.LimitReader(file, 4097))
	if err != nil || json.Unmarshal(raw, &s) != nil || s.Schema != radioWorkerSettingsSchema || !radioBootHex(s.Token, 64, 64) || !radioBootHex(s.Secret, 64, 64) || !radioBootHex(s.Admin, 64, 64) || s.Token == s.Secret || s.Token == s.Admin || s.Secret == s.Admin {
		return s, errRadioUnavailable
	}
	return s, nil
}

// Called once before existing room configuration is rendered. Failure disables
// only radio. Existing unsafe/incomplete state is never repaired or replaced:
// changing secrets could strand an already admitted source or UNKNOWN journal.
func ensureRadioWorkerSettings() error {
	radioSettingsMu.Lock()
	defer radioSettingsMu.Unlock()
	o, err := radioRawOptions()
	if err != nil {
		return err
	}
	if !radioWorkerConfigured(o) {
		return nil
	}
	if o.NativeToken != "" || o.Secret != "" || o.AdminPassword != "" || o.Cgroup != "" {
		return errRadioUnavailable
	}
	if _, err = readRadioWorkerSettings(); err == nil {
		return nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return errRadioUnavailable
	}
	s := radioWorkerSettings{Schema: radioWorkerSettingsSchema}
	for _, value := range []*string{&s.Token, &s.Secret, &s.Admin} {
		var b [32]byte
		if _, err = rand.Read(b[:]); err != nil {
			return errRadioUnavailable
		}
		*value = hex.EncodeToString(b[:])
	}
	raw, err := json.Marshal(s)
	if err != nil {
		return errRadioUnavailable
	}
	file, err := os.OpenFile(radioWorkerSettingsPath(), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return errRadioUnavailable
	}
	// An interrupted creation remains invalid and blocks activation on restart;
	// do not delete it and silently create a different identity.
	_, writeErr := file.Write(raw)
	syncErr := file.Sync()
	closeErr := file.Close()
	if err = errors.Join(writeErr, syncErr, closeErr); err != nil {
		return err
	}
	dir, err := os.Open(dataDir)
	if err != nil {
		return err
	}
	err = dir.Sync()
	closeErr = dir.Close()
	return errors.Join(err, closeErr)
}

func readRadioOptions() (radioProducerBootstrapOptions, error) {
	o, err := radioRawOptions()
	if err != nil {
		return o, err
	}
	if !radioWorkerConfigured(o) {
		return o, nil
	}
	if o.NativeToken != "" || o.Secret != "" || o.AdminPassword != "" || o.Cgroup != "" {
		return o, errRadioUnavailable
	}
	s, err := readRadioWorkerSettings()
	if err != nil {
		return o, errRadioUnavailable
	}
	o.NativeToken, o.Secret, o.AdminPassword = s.Token, s.Secret, s.Admin
	// Activation establishes no physical acceptance claim.
	o.Physical = false
	return o, nil
}
