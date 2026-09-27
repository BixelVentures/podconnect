package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestTransportRoomsReportsFailureWithoutRetry(t *testing.T) {
	for _, status := range []int{http.StatusOK, http.StatusNoContent, http.StatusNotFound, http.StatusServiceUnavailable} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			calls := 0
			backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.Method != http.MethodPost || r.URL.Path != "/player/resume" {
					t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
				}
				w.WriteHeader(status)
			}))
			defer backend.Close()
			response := httptest.NewRecorder()
			transportRooms(response, []*Room{{ID: "kitchen", Librespot: backend.URL}}, "resume")
			want := http.StatusOK
			if status != http.StatusOK {
				want = http.StatusBadGateway
			}
			if response.Code != want {
				t.Fatalf("HTTP %d want %d: %s", response.Code, want, response.Body.String())
			}
			if calls != 1 {
				t.Fatalf("request executed %d times", calls)
			}
			if status != http.StatusOK && !strings.Contains(response.Body.String(), `"ok":false`) {
				t.Fatal(response.Body.String())
			}
		})
	}
}

func TestTransportRoomsMissingRoom(t *testing.T) {
	response := httptest.NewRecorder()
	transportRooms(response, nil, "resume")
	if response.Code != http.StatusNotFound {
		t.Fatalf("HTTP %d", response.Code)
	}
}

func TestStopSilencesAirPlayWhenSpotifyUnavailable(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusServiceUnavailable) }))
	defer backend.Close()
	calls := 0
	airplay := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Method != http.MethodPut || r.URL.Path != "/api/player/pause" {
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer airplay.Close()
	response := httptest.NewRecorder()
	transportRooms(response, []*Room{{ID: "kitchen", Librespot: backend.URL, OwnTone: airplay.URL}}, "pause")
	if response.Code != http.StatusBadGateway || calls != 1 {
		t.Fatalf("HTTP %d; AirPlay calls=%d", response.Code, calls)
	}
}

func TestTransportRoomsConnectionFailure(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	backend.Close()
	response := httptest.NewRecorder()
	transportRooms(response, []*Room{{ID: "kitchen", Librespot: backend.URL}}, "resume")
	if response.Code != http.StatusBadGateway {
		t.Fatalf("HTTP %d", response.Code)
	}
}

func TestStopReportsAirPlayFailure(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) }))
	defer backend.Close()
	airplay := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusServiceUnavailable) }))
	defer airplay.Close()
	response := httptest.NewRecorder()
	transportRooms(response, []*Room{{ID: "kitchen", Librespot: backend.URL, OwnTone: airplay.URL}}, "pause")
	if response.Code != http.StatusBadGateway {
		t.Fatalf("HTTP %d", response.Code)
	}
}
