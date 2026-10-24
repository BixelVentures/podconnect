package main

import (
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"testing"
)

type releaseNoEffectTransport struct{ calls int }

func (transport *releaseNoEffectTransport) RoundTrip(*http.Request) (*http.Response, error) {
	transport.calls++
	panic("manual release must not dispatch any HTTP effect")
}

func TestReleaseUnsupportedBeforeEffectsPreservesPausedOwnerAndSuccessor(t *testing.T) {
	for _, successor := range []bool{false, true} {
		t.Run(map[bool]string{false: "paused-owner", true: "delayed-request-successor"}[successor], func(t *testing.T) {
			oldDir, oldStore, oldManager, oldTransport := dataDir, store, mgr, http.DefaultTransport
			t.Cleanup(func() { dataDir, store, mgr, http.DefaultTransport = oldDir, oldStore, oldManager, oldTransport })
			setupAttentionRoom(t)
			runtime := mgr.runtime("r0")
			runtime.live.st = glStatus{Active: true, Paused: true, SelAlias: 1, AliasBinding: &aliasBinding{Ready: true, Incarnation: "old", Registry: "registry", RoomID: "r0"}}
			request := httptest.NewRequest(http.MethodPost, "/api/release?room=r0", nil)
			if successor {
				runtime.live.phaseEpoch++
				runtime.live.aliasRevision++
				runtime.live.st.AliasBinding = &aliasBinding{Ready: true, Incarnation: "successor", Registry: "registry", RoomID: "r0"}
			}
			before, intent := runtime.live.transportSnapshot()
			transport := &releaseNoEffectTransport{}
			http.DefaultTransport = transport
			response := httptest.NewRecorder()
			speakerReleaseHandler(response, request)
			var body map[string]any
			if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			if response.Code != http.StatusConflict || body["ok"] != false || body["error"] != "release_unsupported" || body["outcome"] != "rejected" || body["retryable"] != false {
				t.Fatalf("untruthful refusal: %d %v", response.Code, body)
			}
			after, afterIntent := runtime.live.transportSnapshot()
			if transport.calls != 0 || !reflect.DeepEqual(before, after) || intent != afterIntent || fileExists(releasedPath(runtime.room)) || roomByID("r0").Released {
				t.Fatal("unsupported release mutated current owner or marker")
			}
			select {
			case <-runtime.stop:
				t.Fatal("manual release stopped Native runtime")
			default:
			}
		})
	}
}

func TestReleaseUnsupportedPreservesMethodAndDoesNotWriteLegacyMarker(t *testing.T) {
	response := httptest.NewRecorder()
	speakerReleaseHandler(response, httptest.NewRequest(http.MethodGet, "/api/release", nil))
	if response.Code != http.StatusMethodNotAllowed {
		t.Fatal(response.Code)
	}
}

func TestReleaseRemovalKeepsExistingExactRuntimeTeardownWithoutDeselect(t *testing.T) {
	oldDir, oldStore, oldManager, oldTransport := dataDir, store, mgr, http.DefaultTransport
	t.Cleanup(func() { dataDir, store, mgr, http.DefaultTransport = oldDir, oldStore, oldManager, oldTransport })
	setupAttentionRoom(t)
	room, err := store.addRoom("Removed alias", "Fixture HomePod", "fixture-id")
	if err != nil {
		t.Fatal(err)
	}
	runtime := &roomRuntime{room: room, stop: make(chan struct{}), done: make(chan struct{})}
	mgr.runtimes[room.ID] = runtime
	close(runtime.done) // No child processes: the original join has already completed.
	other := &roomRuntime{room: &Room{ID: "other"}, stop: make(chan struct{}), done: make(chan struct{})}
	mgr.runtimes["other"] = other
	transport := &releaseNoEffectTransport{}
	http.DefaultTransport = transport
	if err := mgr.removeRoom(room.ID); err != nil {
		t.Fatal(err)
	}
	if transport.calls != 0 || mgr.runtime(room.ID) != nil || mgr.runtime("other") != other || fileExists(releasedPath(runtime.room)) {
		t.Fatal("removal deselected output or affected another owner")
	}
	select {
	case <-runtime.stop:
	default:
		t.Fatal("original exact runtime teardown was lost")
	}
	select {
	case <-other.stop:
		t.Fatal("removed unrelated runtime")
	default:
	}
}

func TestReleaseIdleSourceCannotDeselectOrMintReleaseAcknowledgement(t *testing.T) {
	source, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatal(err)
	}
	file, err := parser.ParseFile(token.NewFileSet(), "main.go", source, 0)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Name.Name != "roomBridge" {
			continue
		}
		found = true
		ast.Inspect(fn.Body, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if !ok {
				return true
			}
			if ident, ok := call.Fun.(*ast.Ident); ok && (ident.Name == "releaseHomePod" || ident.Name == "releaseIdleHomePod") {
				t.Error("idle bridge dispatches an unbound physical release")
			}
			if selector, ok := call.Fun.(*ast.SelectorExpr); ok && (selector.Sel.Name == "Close" || selector.Sel.Name == "Stop" || selector.Sel.Name == "WriteFile") {
				t.Error("idle bridge destroys writer/context or mints marker")
			}
			return true
		})
	}
	if !found || strings.Contains(string(source), `{"outputs":[]}`) {
		t.Fatal("unbound empty-output release remains")
	}
	if !strings.Contains(string(source), `http.HandleFunc("/api/release", speakerReleaseHandler)`) {
		t.Fatal("configured release API path changed")
	}
}
