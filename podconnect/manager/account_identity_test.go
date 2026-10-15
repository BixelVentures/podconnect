package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const accountTestEngine = "a123456789012345678901234567890123456789"

func accountHTTP(t *testing.T, body string, status int, before func()) {
	t.Helper()
	previous := http.DefaultTransport
	http.DefaultTransport = startTransport(func(request *http.Request) (*http.Response, error) {
		if request.Method != "GET" || request.URL.Path != "/status" {
			t.Fatalf("diagnostic attempted effect: %s %s", request.Method, request.URL)
		}
		if before != nil {
			before()
		}
		return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}, nil
	})
	t.Cleanup(func() { http.DefaultTransport = previous })
}

func accountLive(t *testing.T) (*glLive, glSource, glSource) {
	t.Helper()
	live := &glLive{}
	run := live.beginRun(nil)
	phase, ok := live.beginPhase(run)
	if !ok {
		t.Fatal("source missing")
	}
	return live, run, phase
}

func TestAccountDiagnosticReadsActualUsernameAndEngineWithoutActions(t *testing.T) {
	live, _, _ := accountLive(t)
	username := "actual-canonical-user"
	accountHTTP(t, `{"device_id":"`+accountTestEngine+`","username":"`+username+`","volume":90}`, 200, nil)
	observed := live.observeAccountIdentity("http://native.invalid", accountTestEngine)
	sum := sha256.Sum256([]byte(username))
	if observed == nil || observed.IdentifierSHA256 != hex.EncodeToString(sum[:]) || observed.EngineDeviceID != accountTestEngine || observed.Domain != "spotify.ap.canonical_username" || observed.ExpiresAtUnixMS-observed.ObservedAtUnixMS != 5000 || observed.RunGeneration == "0" || observed.ConnectionGeneration == "0" {
		t.Fatalf("missing exact source observation: %+v", observed)
	}
	raw, _ := json.Marshal(observed)
	if strings.Contains(string(raw), username) || strings.Contains(string(raw), "token") {
		t.Fatal("raw identity or credential leaked")
	}
	if live.Get().Active || live.Get().HasVol {
		t.Fatal("diagnostic mutated transport")
	}
}

func TestAccountDiagnosticRejectsMissingForeignMalformedOrNon200Status(t *testing.T) {
	for _, body := range []string{
		`{}`, `{"volume":35}`, `{"username":"user"}`, `{"device_id":"foreign","username":"user"}`,
		`{"device_id":"` + accountTestEngine + `","username":""}`,
		`{"device_id":"` + accountTestEngine + `","username":4}`,
		`{"device_id":"` + accountTestEngine + `","username":"\ud800"}`,
		`{"device_id":"` + accountTestEngine + `","username":"` + string([]byte{0xff}) + `"}`,
		`{"device_id":"` + accountTestEngine + `","username":"user","username":"other"}`,
		`{"device_id":"` + accountTestEngine + `","username":"user"} {}`,
		`{"device_id":"` + accountTestEngine + `","username":"user","padding":"` + strings.Repeat("x", 65537) + `"}`,
	} {
		t.Run(body[:min(len(body), 65)], func(t *testing.T) {
			accountHTTP(t, body, 200, nil)
			if readNativeAccountHash("http://native.invalid", accountTestEngine) != "" {
				t.Fatal("invalid account observed")
			}
		})
	}
	t.Run("http failure", func(t *testing.T) {
		accountHTTP(t, `{"device_id":"`+accountTestEngine+`","username":"user"}`, 500, nil)
		if readNativeAccountHash("http://native.invalid", accountTestEngine) != "" {
			t.Fatal("error body observed")
		}
	})
	for _, name := range []string{"", " user", "user ", "a b", "a\nb", string([]byte{0xff}), strings.Repeat("x", 1025)} {
		if canonicalUsernameDigest(name) != "" {
			t.Fatalf("invalid username admitted %q", name)
		}
	}
	if canonicalUsernameDigest("User") == canonicalUsernameDigest("user") {
		t.Fatal("identity was case folded")
	}
}

func TestAccountDiagnosticRejectsSourceConnectionWireOrRequestSupersession(t *testing.T) {
	for _, mutation := range []string{"phase", "run", "wire", "newer_read", "stop"} {
		t.Run(mutation, func(t *testing.T) {
			live, run, phase := accountLive(t)
			accountHTTP(t, `{"device_id":"`+accountTestEngine+`","username":"user"}`, 200, func() {
				switch mutation {
				case "phase":
					live.retirePhase(phase)
				case "run":
					live.retireRun(run)
				case "wire":
					live.applySourceEvent(phase, "inactive", nil)
				case "newer_read":
					live.beginStatus(phase)
				case "stop":
					stop := make(chan struct{})
					live.mu.Lock()
					live.sourceStop = stop
					live.mu.Unlock()
					close(stop)
				}
			})
			if live.observeAccountIdentity("http://native.invalid", accountTestEngine) != nil {
				t.Fatal("stale diagnostic survived owner change")
			}
		})
	}
}

func TestAccountIdentityEndpointKeepsEngineProtocolAndNeverMintsStartLease(t *testing.T) {
	fixture := releasedURIFixture(t)
	accountHTTP(t, `{"device_id":"`+fixture.id+`","username":"actual-user"}`, 200, nil)
	recorder := httptest.NewRecorder()
	startIdentityRequest(recorder, httptest.NewRequest(http.MethodGet, "/api/start-volume/identity?account=1", nil))
	var response struct {
		Schema  string                    `json:"schema"`
		Engine  string                    `json:"engine_device_id"`
		Account *nativeAccountObservation `json:"account_observation"`
	}
	if recorder.Code != 200 || json.Unmarshal(recorder.Body.Bytes(), &response) != nil || response.Schema != "podconnect.start_identity.v1" || response.Engine != fixture.id || response.Account == nil || recorder.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("identity response %d %s", recorder.Code, recorder.Body.String())
	}
	startLeases.Lock()
	lease := startLeases.Current
	startLeases.Unlock()
	if lease != nil || len(fixture.effects) != 0 {
		t.Fatal("read created playback preparation/effect")
	}
}

func TestAccountDiagnosticDoesNotFollowRedirects(t *testing.T) {
	previous := http.DefaultTransport
	calls := 0
	http.DefaultTransport = startTransport(func(request *http.Request) (*http.Response, error) {
		calls++
		return &http.Response{StatusCode: 302, Header: http.Header{"Location": []string{"http://foreign.invalid/status"}}, Body: io.NopCloser(strings.NewReader(""))}, nil
	})
	t.Cleanup(func() { http.DefaultTransport = previous })
	if readNativeAccountHash("http://native.invalid", accountTestEngine) != "" || calls != 1 {
		t.Fatal("redirect was followed or admitted")
	}
}

func TestAccountDiagnosticOptInLeavesOrdinaryIdentityReadUnchanged(t *testing.T) {
	fixture := releasedURIFixture(t)
	old := http.DefaultTransport
	http.DefaultTransport = startTransport(func(request *http.Request) (*http.Response, error) {
		t.Fatal("ordinary identity read acquired diagnostic I/O")
		return nil, nil
	})
	t.Cleanup(func() { http.DefaultTransport = old })
	recorder := httptest.NewRecorder()
	startIdentityRequest(recorder, httptest.NewRequest(http.MethodGet, "/api/start-volume/identity", nil))
	var response map[string]any
	if recorder.Code != 200 || json.Unmarshal(recorder.Body.Bytes(), &response) != nil || len(response) != 2 || response["engine_device_id"] != fixture.id {
		t.Fatalf("ordinary identity changed: %d %s", recorder.Code, recorder.Body.String())
	}
}

func TestAccountIdentityUnknownUsernameCannotCreateReadyDiagnostic(t *testing.T) {
	fixture := releasedURIFixture(t)
	accountHTTP(t, `{"device_id":"`+fixture.id+`","volume":35}`, 200, nil)
	recorder := httptest.NewRecorder()
	startIdentityRequest(recorder, httptest.NewRequest(http.MethodGet, "/api/start-volume/identity?account=1", nil))
	var response map[string]any
	if recorder.Code != 200 || json.Unmarshal(recorder.Body.Bytes(), &response) != nil || response["account_observation"] != nil {
		t.Fatalf("volume-only session minted account diagnostic: %d %s", recorder.Code, recorder.Body.String())
	}
	if len(fixture.effects) != 0 {
		t.Fatal("unknown session caused effect")
	}
}

func TestAccountIdentityEndpointRejectsRouteReplacementDuringRead(t *testing.T) {
	fixture := releasedURIFixture(t)
	accountHTTP(t, `{"device_id":"`+fixture.id+`","username":"user"}`, 200, func() {
		fixture.rt.live.mu.Lock()
		fixture.rt.live.st.AliasBinding = nil
		fixture.rt.live.mu.Unlock()
	})
	recorder := httptest.NewRecorder()
	startIdentityRequest(recorder, httptest.NewRequest(http.MethodGet, "/api/start-volume/identity?account=1", nil))
	if recorder.Code != 409 || recorder.Body.Len() != 0 {
		t.Fatalf("obsolete route survived account read: %d %s", recorder.Code, recorder.Body.String())
	}
	if len(fixture.effects) != 0 {
		t.Fatal("route replacement caused effect")
	}
}
