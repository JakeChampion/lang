package checker_test

import (
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/check/checker"
	"github.com/jakechampion/lang/internal/syntax/parser"
)

// __sha256_hw answers a boolean, and __sha256_hw_blocks takes two `[u8]`
// views, so an owned `u8[]`, a slice and a string's bytes all pass, and
// answers a fresh `u8[]`.
func TestSHA256HWBuiltinSignatures(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want string // "" = must type-check
	}{
		{"the predicate is a boolean", `function main(): i32 { if (__sha256_hw()) { return 1; } return 0; }`, ""},
		{"the kernel takes views", `function main(): i32 { let s: u8[] = __alloc_u8(32); let out: u8[] = __sha256_hw_blocks(s, s[0:16]); let more: u8[] = __sha256_hw_blocks(out, "abc".as_bytes()); return more.len(); }`, ""},
		{"the kernel refuses an i32[]", `function main(): i32 { let s: i32[] = [1, 2]; let out: u8[] = __sha256_hw_blocks(s, s); return 0; }`, "argument"},
		{"the kernel takes two arguments", `function main(): i32 { let s: u8[] = __alloc_u8(32); let out: u8[] = __sha256_hw_blocks(s); return 0; }`, "argument"},
		{"the predicate takes nothing", `function main(): i32 { if (__sha256_hw(1)) { return 1; } return 0; }`, "argument"},
		{"the predicate is not an i32", `function main(): i32 { let r: i32 = __sha256_hw(); return r; }`, "boolean"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			prog, err := parser.Parse(tc.src)
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			_, err = checker.Check(prog)
			if tc.want == "" {
				if err != nil {
					t.Fatalf("check: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("check accepted %s", tc.src)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("check error = %v, want it to mention %q", err, tc.want)
			}
		})
	}
}
