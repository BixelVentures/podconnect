package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestGLHealthPreservesIdleAndUnpairedButRejectsFailedSession(t *testing.T) {
	for _, code := range []int{200, 204, 400, 401, 404, 429, 500, 503} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("X-PodConnect-Health-Probe") != "1" {
				t.Error("missing local health marker")
			}
			w.WriteHeader(code)
		}))
		got := probeGLHealth(server.Client(), server.URL)
		server.Close()
		kind := "http"
		if code == 200 || code == 204 {
			kind = "ok"
		}
		if got != (glHealthResult{kind: kind, status: code}) {
			t.Fatalf("status=%d health=%v", code, got)
		}
	}
}

func TestGLHealthDistinguishesTimeoutAndTransport(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	client := &http.Client{Timeout: 50 * time.Millisecond}
	if got := probeGLHealth(client, server.URL); got != (glHealthResult{kind: "timeout"}) {
		t.Fatalf("blocked status: %v", got)
	}
	server.Close()
	if got := probeGLHealth(client, server.URL); got != (glHealthResult{kind: "transport"}) {
		t.Fatalf("closed listener: %v", got)
	}
}
