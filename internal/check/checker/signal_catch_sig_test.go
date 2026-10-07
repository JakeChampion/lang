package checker_test

import (
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/check/checker"
	"github.com/jakechampion/lang/internal/syntax/parser"
)

// `signal_catch(sig)` answers the sigaction return signal_ignore does, and
// `signal_taken(sig)` a boolean a caller can branch on (#9243).
// `signal_catch_interrupting(sig)` answers what signal_catch does, and
// `signal_raise(sig)` kill(2)'s i32 (#11698).
func TestSignalCatchSignatures(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want string // "" = must type-check
	}{
		{"catch answers an i32", `function main(): i32 { let r: i32 = signal_catch(10); return r; }`, ""},
		{"catch is not a boolean", `function main(): i32 { if (signal_catch(10)) { return 1; } return 0; }`, "boolean"},
		{"catch takes one signal", `function main(): i32 { return signal_catch(); }`, "argument"},
		{"catch takes a number", `function main(): i32 { return signal_catch("USR1"); }`, "string"},
		{"taken answers a boolean", `function main(): i32 { if (signal_taken(10)) { return 1; } return 0; }`, ""},
		{"taken is not an i32", `function main(): i32 { let n: i32 = signal_taken(10); return n; }`, "boolean"},
		{"taken takes one signal", `function main(): i32 { if (signal_taken(10, 12)) { return 1; } return 0; }`, "argument"},
		{"interrupting catch answers an i32", `function main(): i32 { let r: i32 = signal_catch_interrupting(2); return r; }`, ""},
		{"interrupting catch is not a boolean", `function main(): i32 { if (signal_catch_interrupting(2)) { return 1; } return 0; }`, "boolean"},
		{"interrupting catch takes one signal", `function main(): i32 { return signal_catch_interrupting(2, 15); }`, "argument"},
		{"interrupting catch takes a number", `function main(): i32 { return signal_catch_interrupting("INT"); }`, "string"},
		{"raise answers an i32", `function main(): i32 { let r: i32 = signal_raise(15); return r; }`, ""},
		{"raise is not a boolean", `function main(): i32 { if (signal_raise(15)) { return 1; } return 0; }`, "boolean"},
		{"raise takes one signal", `function main(): i32 { return signal_raise(); }`, "argument"},
		{"raise takes a number", `function main(): i32 { return signal_raise("TERM"); }`, "string"},
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
