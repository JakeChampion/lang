package checker_test

import (
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/check/checker"
	"github.com/jakechampion/lang/internal/syntax/parser"
)

func TestFmaF64BuiltinSignature(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want string
	}{
		{"three f64 values", `function main(): i32 { let a: f64 = 0.1; let r: f64 = __fma_f64(a, 10.0, -1.0); return r as i32; }`, ""},
		{"two arguments", `function main(): i32 { let r: f64 = __fma_f64(0.1, 10.0); return 0; }`, "argument"},
		{"a string argument is refused", `function main(): i32 { let s: string = "x"; let r: f64 = __fma_f64(s, 10.0, -1.0); return 0; }`, "string"},
		{"the result is an f64", `function main(): i32 { let r: i32 = __fma_f64(0.1, 10.0, -1.0); return r; }`, "f64"},
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
