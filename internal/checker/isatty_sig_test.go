package checker_test

import (
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/checker"
	"github.com/jakechampion/lang/internal/parser"
)

// `isatty(fd)` answers a yes/no question, so it is declared `boolean` rather
// than the 0/1 number every other syscall-shaped builtin returns. That makes
// `if (isatty(1))` legal and `isatty(1) + 1` an error, which is the whole
// reason to spend a distinct return type on it.
func TestIsattyIsTypedAsAPredicate(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want string // "" = must type-check
	}{
		{"used as a condition", `function main(): i32 { if (isatty(1)) { return 1; } return 0; }`, ""},
		{"assigned to a boolean", `function main(): i32 { var b: boolean = isatty(0); if (b) { return 1; } return 0; }`, ""},
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
