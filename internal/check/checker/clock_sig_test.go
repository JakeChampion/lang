package checker_test

import (
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/check/checker"
	"github.com/jakechampion/lang/internal/syntax/parser"
)

// `clock_resolution()` is one i64 of nanoseconds like every other clock
// builtin, and `clock_set(sec, nsec)` answers a Result[void, IoError] the
// caller has to look at: the usual answer is EPERM.
func TestClockBuiltinSignatures(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want string // "" = must type-check
	}{
		{"resolution is an i64", `function main(): i32 { let r: i64 = clock_resolution(); if (r > 0) { return 0; } return 1; }`, ""},
		{"resolution is not a string", `function main(): i32 { let r: string = clock_resolution(); return 0; }`, "i64"},
		{"resolution takes nothing", `function main(): i32 { let r: i64 = clock_resolution(1); return 0; }`, "argument"},
		{"set answers a Result", `function main(): i32 { match (clock_set(0, 0)) { Ok(_) => { return 0; }, Err(e) => { return 1; } } }`, ""},
		{"set takes seconds and nanoseconds", `function main(): i32 { match (clock_set(0)) { Ok(_) => { return 0; }, Err(e) => { return 1; } } }`, "argument"},
		{"set takes numbers", `function main(): i32 { match (clock_set("0", 0)) { Ok(_) => { return 0; }, Err(e) => { return 1; } } }`, "string"},
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
