package e2eselfhost

import "testing"

// floatNanIRCases pin IEEE-754 NaN comparison semantics on the self-host IR
// path. A NaN is produced importlessly with `0.0 / 0.0`; every ordered
// comparison against it must be false (`< > <= >=` and `==`), and only `!=`
// must be true — including `NaN != NaN`. This is the subtle half of float
// comparison: an x86-64 `ucomisd` sets the parity flag on an unordered
// (NaN) operand, so the `setcc` sequence the IR backend emits has to fold
// parity in correctly (e.g. `==` must stay false when PF=1, `!=` true);
// wasm's `f64.eq`/`f64.ne`/`f64.lt`/… already follow IEEE directly. Ordered
// (non-NaN) cases are included as a sanity floor. Each case returns a small
// deterministic int; expectations verified against the native interpreter.
// FEATURE-AUDIT "Float comparison + NaN semantics" row.
var floatNanIRCases = []struct {
	name string
	main string
	want int
}{
	// NaN != NaN is the one true comparison.
	{"nan-ne-self", `var n: f64 = 0.0 / 0.0; if (n != n) { return 1; } return 0;`, 1},
	// NaN == NaN is false.
	{"nan-eq-self", `var n: f64 = 0.0 / 0.0; if (n == n) { return 1; } return 0;`, 0},
	// every ordered comparison with NaN is false.
	{"nan-lt", `var n: f64 = 0.0 / 0.0; if (n < 1.0) { return 1; } return 0;`, 0},
	{"nan-gt", `var n: f64 = 0.0 / 0.0; if (n > 1.0) { return 1; } return 0;`, 0},
	{"nan-ge", `var n: f64 = 0.0 / 0.0; if (n >= 1.0) { return 1; } return 0;`, 0},
	{"nan-le", `var n: f64 = 0.0 / 0.0; if (n <= 1.0) { return 1; } return 0;`, 0},
	// the negation: !(NaN < 1.0) is true (the else path runs).
	{"nan-lt-negated", `var n: f64 = 0.0 / 0.0; if (n < 1.0) { return 0; } return 9;`, 9},
	// ordered (non-NaN) sanity: a real f64 compares normally.
	{"ordered-eq", `var a: f64 = 1.5; if (a == a) { return 7; } return 0;`, 7},
	{"ordered-lt", `if (1.0 < 2.0) { return 5; } return 0;`, 5},
}

func floatNanIRSrc(mainBody string) string {
	return "function main(): i32 { " + mainBody + " }\n"
}

// TestSelfHostFloatNanIR compiles each case with the self-host CLI for
// x86-64 and wasm and checks the exit code.
func TestSelfHostFloatNanIR(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, target := range []string{"x86-64-linux", "wasm32-wasi"} {
		for _, tc := range floatNanIRCases {
			t.Run(target+"/"+tc.name, func(t *testing.T) {
				if stderr, code := cli.exitOf(t, floatNanIRSrc(tc.main), target); code != tc.want {
					t.Errorf("exited %d, want %d\n%s", code, tc.want, stderr)
				}
			})
		}
	}
}
