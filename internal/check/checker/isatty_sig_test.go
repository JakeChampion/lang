package checker_test

import (
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/check/checker"
	"github.com/jakechampion/lang/internal/syntax/parser"
)

func TestIsattyIsTypedAsAPredicate(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want string
	}{
		{"used as a condition", `function main(): i32 { if (isatty(1)) { return 1; } return 0; }`, ""},
		{"assigned to a boolean", `function main(): i32 { let b: boolean = isatty(0); if (b) { return 1; } return 0; }`, ""},
		{"not a number", `function main(): i32 { return isatty(1) + 1; }`, "boolean"},
		{"takes an fd", `function main(): i32 { if (isatty()) { return 1; } return 0; }`, "argument"},
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
