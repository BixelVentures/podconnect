package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestRadioNativeConcreteProtocol(t *testing.T) {
	var calls []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-PodConnect-Token") != "secret" {
			t.Error("auth missing")
		}
		path := strings.TrimPrefix(r.URL.Path, "/api/radio-owner/")
		calls = append(calls, path)
		reply := radioNativeReply{Schema: "podconnect.radio-owner.v1", Process: "native-1", OK: true, Proofs: radioRequiredProofs, Targets: []radioTarget{{ID: "r1", Name: "Stuen", Availability: "available", Selectable: true}}}
		if path != "capabilities" {
			var args map[string]any
			if e := json.NewDecoder(r.Body).Decode(&args); e != nil {
				t.Error(e)
			}
			if args["process_id"] != "native-1" {
				t.Error("unbound process")
			}
			if path == "reserve" {
				reply.Token = "fence"
				reply.OwnerGeneration = 1001
				reply.Generation = uint64(args["generation"].(float64))
			} else if path != "preflight" {
				reply.Token = args["token"].(string)
				reply.Generation = uint64(args["generation"].(float64))
			}
			reply.Observed = true
			if path == "start" || path == "finish" {
				reply.ActionID = "a"
				reply.Terminal = true
				reply.Observation = &radioNativeObservation{State: "playing", SourceKind: "radio", StationID: "p3", TargetID: "r1", OwnerGeneration: 1001, ObservedAt: time.Now().Unix() - 2, ValidUntil: time.Now().Unix() + 30}
			}
		}
		json.NewEncoder(w).Encode(reply)
	}))
	defer srv.Close()
	n, e := newRadioNative(srv.URL, "secret", true)
	if e != nil {
		t.Fatal(e)
	}
	c := newRadioCoordinator(n)
	if e = c.enable(context.Background()); e != nil {
		t.Fatal(e)
	}
	radioDo(t, c, "a", "start", "p3", "r1")
	want := []string{"capabilities", "preflight", "reserve", "pause-release", "start", "finish"}
	if strings.Join(calls, ",") != strings.Join(want, ",") {
		t.Fatal(calls)
	}
}
func TestRadioNativeCannotEnableWithoutPhysicalAcceptance(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(radioNativeReply{Schema: "podconnect.radio-owner.v1", Process: "p", OK: true, Proofs: radioRequiredProofs, Targets: []radioTarget{{ID: "r1"}}})
	}))
	defer srv.Close()
	n, _ := newRadioNative(srv.URL, "secret", false)
	c := newRadioCoordinator(n)
	if c.enable(context.Background()) == nil || c.snapshot().Enabled {
		t.Fatal("physical gate bypassed")
	}
}
func TestRadioNativeRefusesNonLocalAndCredentialURL(t *testing.T) {
	for _, base := range []string{"https://example.com", "http://127.0.0.1/path", "http://user@localhost", "http://localhost?x=1"} {
		if _, e := newRadioNative(base, "x", true); e == nil {
			t.Fatal(base)
		}
	}
}

func TestRadioNativeProcessReplacementCannotReconcile(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(radioNativeReply{Schema: "podconnect.radio-owner.v1", Process: "replacement", OK: true, Terminal: true, ActionID: "a", Generation: 1, Token: "f", Outcome: "committed", Observation: &radioNativeObservation{State: "playing", SourceKind: "radio", StationID: "p3", TargetID: "r1", OwnerGeneration: 1001, ObservedAt: time.Now().Unix() - 1, ValidUntil: time.Now().Unix() + 30}})
	}))
	defer srv.Close()
	n, _ := newRadioNative(srv.URL, "secret", true)
	n.process = "original"
	if _, e := n.Reconcile(context.Background(), radioFence{Token: "f", ActionID: "a", Generation: 1, OwnerGeneration: 1001}); e == nil {
		t.Fatal("replacement process settled original custody")
	}
}

func TestRadioNativeRejectsOversizedValidPrefixWithoutEnabling(t *testing.T) {
	valid, err := json.Marshal(radioNativeReply{Schema: "podconnect.radio-owner.v1", Process: "native-1", OK: true, Proofs: radioRequiredProofs, Targets: []radioTarget{{ID: "r1", Name: "Stuen", Availability: "available", Selectable: true}}})
	if err != nil {
		t.Fatal(err)
	}
	for _, suffix := range []string{"", "invalid-suffix"} {
		t.Run("suffix-"+suffix, func(t *testing.T) {
			payload := string(valid) + strings.Repeat(" ", 65537-len(valid)) + suffix
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = w.Write([]byte(payload))
			}))
			defer srv.Close()
			n, err := newRadioNative(srv.URL, "secret", true)
			if err != nil {
				t.Fatal(err)
			}
			owner := newRadioCoordinator(n)
			if owner.enable(context.Background()) == nil || owner.snapshot().Enabled || n.process != "" {
				t.Fatal("oversized native reply enabled radio or bound a native process")
			}
		})
	}
	t.Run("exact-limit-remains-valid", func(t *testing.T) {
		payload := string(valid) + strings.Repeat(" ", 65536-len(valid))
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte(payload))
		}))
		defer srv.Close()
		n, _ := newRadioNative(srv.URL, "secret", true)
		owner := newRadioCoordinator(n)
		if err := owner.enable(context.Background()); err != nil || !owner.snapshot().Enabled || n.process != "native-1" {
			t.Fatal("valid bounded native reply rejected", err)
		}
	})
}
