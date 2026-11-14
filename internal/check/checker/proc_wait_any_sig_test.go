package checker_test

import (
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/check/checker"
	"github.com/jakechampion/lang/internal/syntax/parser"
)

func TestProcWaitAnyBuiltinSignature(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want string
	}{
		{"no arguments and an i64 result", `function main(): i32 { let r: i64 = proc_wait_any(); return (r >> 32) as i32; }`, ""},
		{"an argument is refused", `function main(): i32 { let r: i64 = proc_wait_any(0 - 1); return 0; }`, "argument"},
		{"the result is not an i32", `function main(): i32 { let r: i32 = proc_wait_any(); return r; }`, "i64"},
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
