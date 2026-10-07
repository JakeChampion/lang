package checker_test

import (
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/check/checker"
	"github.com/jakechampion/lang/internal/syntax/parser"
)

// The AES-GCM kernels take `[u8]` views, so an owned `u8[]`, a slice and a
// string's bytes all pass, and each answers a fresh `u8[]`.
func TestAESGCMBuiltinSignatures(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want string // "" = must type-check
	}{
		{"expand takes an owned u8[]", `function main(): i32 { let k: u8[] = __alloc_u8(16); let rk: u8[] = __aes_expand_key(k); return rk.len(); }`, ""},
		{"ctr takes views", `function main(): i32 { let k: u8[] = __alloc_u8(16); let rk: u8[] = __aes_expand_key(k); let c: [u8] = k[0:16]; let out: u8[] = __aes_ctr32(rk, c, "abc".as_bytes()); return out.len(); }`, ""},
		{"ghash takes views", `function main(): i32 { let h: u8[] = __alloc_u8(16); let y: u8[] = __ghash(h, h[0:16], "abc".as_bytes()); return y.len(); }`, ""},
		{"expand refuses an i32[]", `function main(): i32 { let k: i32[] = [1, 2]; let rk: u8[] = __aes_expand_key(k); return 0; }`, "argument"},
		{"ctr refuses a string", `function main(): i32 { let k: u8[] = __alloc_u8(16); let out: u8[] = __aes_ctr32(k, k, "abc"); return 0; }`, "argument"},
		{"ghash takes three arguments", `function main(): i32 { let h: u8[] = __alloc_u8(16); let y: u8[] = __ghash(h, h); return 0; }`, "argument"},
		{"expand answers a u8[]", `function main(): i32 { let k: u8[] = __alloc_u8(16); let r: i32 = __aes_expand_key(k); return r; }`, "u8[]"},
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
