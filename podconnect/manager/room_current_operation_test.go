package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func currentWitness(scope roomTransportScope, offer, binding string) roomCurrentWitness {
	return roomCurrentWitness{
		Schema: "podconnect.native_completed_current.v1", Offer: offer, Binding: binding, Operation: offer,
		Native: roomTransportIdentity{scope.Account, scope.Target, scope.Room, scope.Homepod, "connection", "session", strings.Repeat("a", 32), strings.Repeat("b", 64), 1},
		Route:  outputTransportIdentity{"aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee", scope.Homepod, "1", "2", "3", "4", "5", "6", strings.Repeat("c", 64)},
		Target: roomCurrentTarget{"aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee", scope.Homepod, "1"},
	}
}

func TestCompletedCurrentRequiresExactRetainedOperationAfterNativeRead(t *testing.T) {
	for _, kind := range []string{"content", "move"} {
		t.Run(kind, func(t *testing.T) {
			var offer, binding string
			var scope roomTransportScope
			var handler http.HandlerFunc
			var rpc *func(context.Context, string, string, string, []byte) ([]byte, error)
			if kind == "content" {
				f := contentOperationSetup(t)
				f.Stage = "staged"
				if f.prepare(t).Code != 200 || f.operation(t, "commit").Code != 200 {
					t.Fatal("content did not complete")
				}
				offer, binding, scope = f.Request.Offer.ID, f.Request.Offer.Binding, f.Request.Scope
				handler, rpc = roomContentResultHandler, &roomContentRPC
			} else {
				f := moveOperationSetup(t)
				f.Stage = "staged"
				if f.prepare(t).Code != 200 || f.operation(t, "commit").Code != 200 {
					t.Fatal("move did not complete")
				}
				offer, binding, scope = f.Request.Offer.ID, f.Request.Offer.Binding, f.Request.Scope
				handler, rpc = roomMoveResultHandler, &roomMoveRPC
			}
			witness := currentWitness(scope, offer, binding)
			path := "/api/room-" + kind + "/result?" + url.Values{"offer_id": {offer}, "binding_id": {binding}, "include_current": {"1"}}.Encode()
			call := func() *httptest.ResponseRecorder {
				w := httptest.NewRecorder()
				handler(w, httptest.NewRequest(http.MethodGet, path, nil))
				return w
			}
			original := *rpc
			retireDuringRead := false
			*rpc = func(ctx context.Context, base, method, path string, body []byte) ([]byte, error) {
				if method != http.MethodGet || !strings.Contains(path, "include_current=1") {
					t.Fatal("current witness used wrong read", method, path)
				}
				if retireDuringRead {
					if kind == "content" {
						roomContentStore.Lock()
						delete(roomContentStore.Records, offer)
						roomContentStore.Unlock()
					} else {
						roomMoveStore.Lock()
						delete(roomMoveStore.Records, offer)
						roomMoveStore.Unlock()
					}
				}
				return json.Marshal(witness)
			}
			t.Cleanup(func() { *rpc = original })
			if w := call(); w.Code != 200 || !strings.Contains(w.Body.String(), offer) {
				t.Fatal("current completed operation not read", w.Code, w.Body.String())
			}
			witness.Operation = strings.Repeat("e", 32)
			if w := call(); w.Code != 409 {
				t.Fatal("foreign operation admitted", w.Code, w.Body.String())
			}
			witness.Operation = offer
			retireDuringRead = true
			if w := call(); w.Code != 409 {
				t.Fatal("operation retired during native read was admitted", w.Code, w.Body.String())
			}
		})
	}
}
