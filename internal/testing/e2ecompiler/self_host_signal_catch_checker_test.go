package e2ecompiler

import (
	"bytes"
	"os/exec"
	"testing"
)

// signalCatchCheckerCases are the signatures of signal_catch / signal_taken
// (#9243) and signal_catch_interrupting / signal_raise (#11698) from the
// caller's side: a catch answers the i32 sigaction return signal_ignore does,
// the poll a boolean a caller branches on, the raise kill(2)'s i32. `ok` rows must
// type-check; the rest must be refused.
var signalCatchCheckerCases = []struct {
	name string
	src  string
	ok   bool
}{
	{"catch answers an i32", `function main(): i32 { let r: i32 = signal_catch(10); return r; }`, true},
	{"catch is not a boolean", `function main(): i32 { if (signal_catch(10)) { return 1; } return 0; }`, false},
	{"catch takes a number", `function main(): i32 { return signal_catch("USR1"); }`, false},
	{"taken answers a boolean", `function main(): i32 { if (signal_taken(10)) { return 1; } return 0; }`, true},
	{"taken is not an i32", `function main(): i32 { let n: i32 = signal_taken(10); return n; }`, false},
	{"taken takes a number", `function main(): i32 { if (signal_taken(true)) { return 1; } return 0; }`, false},
	{"interrupting catch answers an i32", `function main(): i32 { let r: i32 = signal_catch_interrupting(2); return r; }`, true},
	{"interrupting catch is not a boolean", `function main(): i32 { if (signal_catch_interrupting(2)) { return 1; } return 0; }`, false},
	{"interrupting catch takes a number", `function main(): i32 { return signal_catch_interrupting("INT"); }`, false},
	{"raise answers an i32", `function main(): i32 { let r: i32 = signal_raise(15); return r; }`, true},
	{"raise is not a boolean", `function main(): i32 { if (signal_raise(15)) { return 1; } return 0; }`, false},
	{"raise takes a number", `function main(): i32 { return signal_raise("TERM"); }`, false},
}

// TestSelfHostCheckerSignalCatch holds the self-host checker's signatures for
// the four builtins to the Go checker's, diagnostic for diagnostic.
func TestSelfHostCheckerSignalCatch(t *testing.T) {
	checkerBin, runner, dir := buildCheckerCodesBin(t)
	for _, tc := range signalCatchCheckerCases {
		t.Run(tc.name, func(t *testing.T) {
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(checkerBin)
			} else {
				cmd = exec.Command(runner[0], append(runner[1:], checkerBin)...)
			}
			cmd.Stdin = bytes.NewReader([]byte(tc.src))
			got := diagLines(driverDiags(runCheckerDriver(t, cmd, tc.name)))
			want := diagLines(goCheckerDiags(t, dir, tc.src))
			if !equalStrings(got, want) {
				t.Errorf("the two checkers disagree.\nnative:    %s\nself-host: %s\nsrc: %s", joinOrNone(want), joinOrNone(got), tc.src)
			}
			if tc.ok != (len(got) == 0) {
				t.Errorf("self-host accepted = %v, want %v: %s\nsrc: %s", len(got) == 0, tc.ok, joinOrNone(got), tc.src)
			}
		})
	}
}
