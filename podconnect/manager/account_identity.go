package main

// Diagnostic account observation only. An AP canonical username is not itself a
// Spotify Web API device/account grant, nor a proven mapping to OAuth /me.id.
import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

type nativeAccountObservation struct {
	Schema               string `json:"schema"`
	Method               string `json:"method"`
	Domain               string `json:"domain"`
	IdentifierSHA256     string `json:"identifier_sha256"`
	EngineDeviceID       string `json:"engine_device_id"`
	RunGeneration        string `json:"run_generation"`
	ConnectionGeneration string `json:"connection_generation"`
	ObservationSequence  string `json:"observation_sequence"`
	ObservedAtUnixMS     int64  `json:"observed_at_unix_ms"`
	ExpiresAtUnixMS      int64  `json:"expires_at_unix_ms"`
}

func canonicalUsernameDigest(username string) string {
	if username == "" || len(username) > 1024 || !utf8.ValidString(username) || strings.TrimSpace(username) != username {
		return ""
	}
	for _, character := range username {
		if unicode.IsControl(character) || unicode.IsSpace(character) || character == unicode.ReplacementChar {
			return ""
		}
	}
	sum := sha256.Sum256([]byte(username))
	return hex.EncodeToString(sum[:])
}

// Use a fresh bounded local /status read; retained Active/volume cannot stand in
// for an authenticated username. No account data is persisted or provider called.
func (l *glLive) observeAccountIdentity(base, engineID string) *nativeAccountObservation {
	if !engineIDPattern.MatchString(engineID) {
		return nil
	}
	l.mu.Lock()
	l.observeStopLocked()
	if l.sourceRetired || l.runEpoch == 0 || l.phaseEpoch == 0 || sourceStopped(l.sourceStop) {
		l.mu.Unlock()
		return nil
	}
	source := glSource{run: l.runEpoch, phase: l.phaseEpoch, stop: l.sourceStop}
	l.nextRequest++
	sequence, wire := l.nextRequest, l.wireRevision[0]
	l.mu.Unlock()

	observedAt := time.Now()
	digest := readNativeAccountHash(base, engineID)

	l.mu.Lock()
	defer l.mu.Unlock()
	l.observeStopLocked()
	if l.sourceRetired || !l.sourceCurrent(source) || sequence != l.nextRequest || wire != l.wireRevision[0] || digest == "" || time.Since(observedAt) >= 5*time.Second {
		return nil
	}
	now := observedAt.UnixMilli()
	return &nativeAccountObservation{
		Schema: "podconnect.native_account_observation.v1", Method: "sha256", Domain: "spotify.ap.canonical_username",
		IdentifierSHA256: digest, EngineDeviceID: engineID,
		RunGeneration: strconv.FormatUint(source.run, 10), ConnectionGeneration: strconv.FormatUint(source.phase, 10),
		ObservationSequence: strconv.FormatUint(sequence, 10), ObservedAtUnixMS: now, ExpiresAtUnixMS: now + 5000,
	}
}

// /status exposes Session.Username(), sourced from APWelcome.CanonicalUsername.
// Keep parsing separate from the routing status: diagnostics never change transport.
func readNativeAccountHash(base, engineID string) string {
	client := &http.Client{Timeout: 3 * time.Second, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}
	response, err := client.Get(base + "/status")
	if err != nil {
		return ""
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return ""
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, 65537))
	if err != nil || len(raw) > 65536 || !utf8.Valid(raw) {
		return ""
	}
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	token, err := decoder.Token()
	if err != nil || token != json.Delim('{') {
		return ""
	}
	seen := map[string]bool{}
	username, observedEngine := "", ""
	for decoder.More() {
		token, err := decoder.Token()
		key, ok := token.(string)
		if err != nil || !ok || seen[key] {
			return ""
		}
		seen[key] = true
		var value json.RawMessage
		if decoder.Decode(&value) != nil {
			return ""
		}
		if key == "username" && json.Unmarshal(value, &username) != nil {
			return ""
		}
		if key == "device_id" && json.Unmarshal(value, &observedEngine) != nil {
			return ""
		}
	}
	if token, err = decoder.Token(); err != nil || token != json.Delim('}') {
		return ""
	}
	if _, err = decoder.Token(); err != io.EOF {
		return ""
	}
	if observedEngine != engineID {
		return ""
	}
	return canonicalUsernameDigest(username)
}
