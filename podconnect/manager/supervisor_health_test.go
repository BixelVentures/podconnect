package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestGLHealthPreservesIdleAndUnpairedButRejectsFailedSession(t *testing.T) {
	for _, code := range []int{200, 204, 400, 401, 404, 429, 500, 503} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(code) }))
		got := glHealthy(server.URL)
		server.Close()
		if got != (code == 200 || code == 204) {
			t.Fatalf("status=%d healthy=%v", code, got)
		}
	}
}
