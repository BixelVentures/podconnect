package main

import (
	"context"
	"strconv"
	"strings"
	"testing"
)

// Native bound owners and OwnTone output_native_owner already carry255 bytes.
// Assert read joins the exact returned receiver to C; never issue a mutation.
func TestRoomReadRetainsNativeOwner255ByteContract(t *testing.T) {
	for _, field := range []string{"connection", "session"} {
		for _, size := range []int{160, 161, 255, 256} {
			t.Run(field+"/"+strconv.Itoa(size), func(t *testing.T) {
				f := roomTransportFixture(t)
				if field == "connection" {
					f.Native.Connection = strings.Repeat("c", size)
				} else {
					f.Native.Session = strings.Repeat("s", size)
				}
				f.Native.Incarnation = strings.Repeat("a", 32)
				f.Claim.Proof.AliasBinding.Incarnation = f.Native.Incarnation
				status := f.Claim.RT.live.Get()
				status.AliasBinding.Incarnation = f.Native.Incarnation
				f.Claim.RT.live.set(status)
				_, f.Claim.Intent = f.Claim.RT.live.transportSnapshot()
				_, native, route, _, err := readRoomTransportState(context.Background(), f.Scope)
				if size <= 255 {
					if err != nil || native.Identity != f.Native || route.Identity != f.Route {
						t.Fatalf("valid canonical owner rejected: %v", err)
					}
				} else if err != errTransportReceiver {
					t.Fatalf("oversized owner accepted: %v", err)
				}
				if len(f.Writes) != 0 {
					t.Fatal("read caused effect", f.Writes)
				}
			})
		}
	}
}
func TestRoomReadLongNativeOwnerRetainsScopeAndRegistryFences(t *testing.T) {
	for _, mutation := range []string{"account", "target", "room", "homepod", "incarnation", "registry"} {
		t.Run(mutation, func(t *testing.T) {
			f := roomTransportFixture(t)
			f.Native.Connection = strings.Repeat("c", 255)
			f.Native.Session = strings.Repeat("s", 255)
			switch mutation {
			case "account":
				f.Native.Account = "other"
			case "target":
				f.Native.Target = strings.Repeat("b", 40)
			case "room":
				f.Native.Room = "other"
			case "homepod":
				f.Native.Homepod = "99"
			case "incarnation":
				f.Native.Incarnation = strings.Repeat("b", 32)
			case "registry":
				f.Native.Registry = strings.Repeat("b", 64)
			}
			assertReadReason(t, f.Scope, "receiver_proof_unavailable")
			if len(f.Writes) != 0 {
				t.Fatal("effect")
			}
		})
	}
}
