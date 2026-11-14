package checker_test

import (
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/check/checker"
	"github.com/jakechampion/lang/internal/syntax/parser"
)

func TestMontMulBuiltinSignature(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want string
	}{
		{"arrays of i64", `function main(): i32 { let m: i64[] = [1, 3]; let a: i64[] = [2]; let t: i64[] = __mont_mul(a, a, m); return t.len(); }`, ""},
		{"views of i64", `function main(): i32 { let m: i64[] = [1, 3, 5]; let a: [i64] = m[0:2]; let t: i64[] = __mont_mul(a, a, m[0:3]); return t.len(); }`, ""},
		{"a u8[] limb array is refused", `function main(): i32 { let m: u8[] = __alloc_u8(16); let t: i64[] = __mont_mul(m, m, m); return 0; }`, "argument"},
		{"three arguments", `function main(): i32 { let m: i64[] = [1, 3]; let t: i64[] = __mont_mul(m, m); return 0; }`, "argument"},
		{"the result is an i64[]", `function main(): i32 { let m: i64[] = [1, 3]; let a: i64[] = [2]; let r: i32 = __mont_mul(a, a, m); return r; }`, "i64[]"},
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
