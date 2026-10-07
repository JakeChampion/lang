package checker_test

import (
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/check/checker"
	"github.com/jakechampion/lang/internal/syntax/parser"
)

// `__ct_secret(b)` and `__ct_public(b)` take a `[u8]`, so an owned `u8[]`
// lends itself and a string's byte view passes as it is, and answer nothing.
func TestCtMarkBuiltinSignatures(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want string // "" = must type-check
	}{
		{"secret takes an owned u8[]", `function main(): i32 { let k: u8[] = __alloc_u8(4); __ct_secret(k); return 0; }`, ""},
		{"public takes a view", `function main(): i32 { let k: u8[] = __alloc_u8(4); let v: [u8] = k[0:2]; __ct_public(v); return 0; }`, ""},
		{"secret takes a string's bytes", `function main(): i32 { __ct_secret("abc".as_bytes()); return 0; }`, ""},
		{"secret refuses an i32[]", `function main(): i32 { let k: i32[] = [1, 2]; __ct_secret(k); return 0; }`, "argument"},
		{"public refuses a string", `function main(): i32 { __ct_public("abc"); return 0; }`, "argument"},
		{"secret takes one argument", `function main(): i32 { let k: u8[] = __alloc_u8(4); __ct_secret(k, k); return 0; }`, "argument"},
		{"public answers nothing", `function main(): i32 { let k: u8[] = __alloc_u8(4); let r: i32 = __ct_public(k); return r; }`, "void"},
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
