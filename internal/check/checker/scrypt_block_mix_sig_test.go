package checker_test

import (
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/check/checker"
	"github.com/jakechampion/lang/internal/syntax/parser"
)

func TestScryptBlockMixBuiltinSignature(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want string
	}{
		{"an owned i64[] and four offsets", `function main(): i32 { let b: i64[] = __alloc_i64(48); b = __scrypt_block_mix(b, 0, 16, 32, 1); return b.len(); }`, ""},
		{"a view is refused", `function main(): i32 { let b: i64[] = __alloc_i64(48); let v: [i64] = b[0:48]; let t: i64[] = __scrypt_block_mix(v, 0, 16, 32, 1); return 0; }`, "argument"},
		{"a u8[] is refused", `function main(): i32 { let t: i64[] = __scrypt_block_mix(__alloc_u8(48), 0, 16, 32, 1); return 0; }`, "argument"},
		{"five arguments", `function main(): i32 { let t: i64[] = __scrypt_block_mix(__alloc_i64(48), 0, 16, 32); return 0; }`, "argument"},
		{"the result is an i64[]", `function main(): i32 { let r: i32 = __scrypt_block_mix(__alloc_i64(48), 0, 16, 32, 1); return r; }`, "i64[]"},
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
