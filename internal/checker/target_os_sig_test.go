package checker_test

import (
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/checker"
	"github.com/jakechampion/lang/internal/parser"
)

// `target_os()` is a zero-argument string: the compile target's environment,
// declared here so a bare `-check` — which names no target and so folds
// nothing — types it exactly as a compile does.
func TestTargetOSIsTypedAsAString(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want string // "" = must type-check
	}{
		{"assigned to a string", `function main(): i32 { var os: string = target_os(); if (os == "linux") { return 1; } return 0; }`, ""},
		{"compared to a literal", `function main(): i32 { if (target_os() == "darwin") { return 1; } return 0; }`, ""},
		{"not a number", `function main(): i32 { var n: i32 = target_os(); return n; }`, "string"},
		{"takes no arguments", `function main(): i32 { var os: string = target_os(1); return 0; }`, "argument"},
		{"cannot be redeclared", `function target_os(): string { return "here"; } function main(): i32 { return 0; }`, "redeclared"},
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
