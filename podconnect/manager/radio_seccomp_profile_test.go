package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// clone3 is required for atomic UseCgroupFD. The fixture must not silently
// replace Docker's syscall policy with unconfined or allow unrelated syscalls.
func TestRadioClone3ProfilePreservesEveryUnrelatedDockerRule(t *testing.T) {
	root := filepath.Join("testdata", "radio-seccomp")
	original, err := os.ReadFile(filepath.Join(root, "moby-default-seccomp.json"))
	if err != nil {
		t.Fatal(err)
	}
	hash := sha256.Sum256(original)
	if hex.EncodeToString(hash[:]) != "6416b47770785a41ac59073cdc77d9fe98517df2799dc83ef207e622de3053f6" {
		t.Fatal("unreviewed Docker policy upstream replacement")
	}
	derived, err := os.ReadFile(filepath.Join(root, "radio-clone3-seccomp.json"))
	if err != nil {
		t.Fatal(err)
	}
	var before, after map[string]any
	if json.Unmarshal(original, &before) != nil || json.Unmarshal(derived, &after) != nil {
		t.Fatal("invalid policy")
	}
	clean := func(policy map[string]any, verify bool) {
		rules := policy["syscalls"].([]any)
		kept := []any{}
		allows := 0
		for _, item := range rules {
			rule := item.(map[string]any)
			names := rule["names"].([]any)
			remaining := []any{}
			contains := false
			for _, name := range names {
				if name == "clone3" {
					contains = true
				} else {
					remaining = append(remaining, name)
				}
			}
			if contains && verify {
				if rule["action"] != "SCMP_ACT_ALLOW" || len(rule) != 2 || len(names) != 1 {
					t.Fatal("clone3 allowance gained unrelated policy")
				}
				allows++
			}
			if len(remaining) > 0 {
				rule["names"] = remaining
				kept = append(kept, rule)
			}
		}
		if verify && allows != 1 {
			t.Fatal("expected exactly one unconditional clone3 allowance")
		}
		policy["syscalls"] = kept
	}
	clean(before, false)
	clean(after, true)
	if !reflect.DeepEqual(before, after) {
		t.Fatal("radio seccomp profile changed unrelated Docker defaults")
	}
}
