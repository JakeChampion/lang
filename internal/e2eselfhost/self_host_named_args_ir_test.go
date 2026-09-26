package e2eselfhost

import "testing"

// namedArgsIRCases exercise NAMED ARGUMENTS — `f(c = 9)`, `g(c = 3, a = 1)` —
// through the self-host stack-IR path (#2701). The parser encodes each
// `name = value` argument as a synthetic marker; fill_default_args_module
// reorders the markers + leading positionals into declared-parameter order and
// fills omitted defaults, exactly like the native `internal/defaultargs` pass
// that runs at the start of the Go checker. After resolution the call is an
// ordinary positional call, so the existing default-arg IR lowering handles it
// unchanged.
//
// Each value is pinned ≤ 255 and oracle-checked against `fern -interp`. They
// cover: a trailing named arg after a positional, a fully-reordered all-named
// call, a named arg that SKIPS a defaulted middle parameter, a named arg that
// OVERRIDES a default, and a string-typed parameter bound out of order.
var namedArgsIRCases = []struct {
	name     string
	src      string
	expected int
}{
	// Trailing named arg; `b` keeps its default. f(7,2,9)=729, -700 => 29.
	{"trailing-named",
		`function f(a: i32, b: i32 = 2, c: i32 = 3): i32 { return a * 100 + b * 10 + c; } function main(): i32 { return f(7, c = 9) - 700; }`, 29},
	// All-named, fully reordered: a=1,b=2,c=3 => 123.
	{"reorder-all-named",
		`function f(a: i32, b: i32, c: i32): i32 { return a * 100 + b * 10 + c; } function main(): i32 { return f(c = 3, a = 1, b = 2); }`, 123},
	// Named arg skips a defaulted middle param: g(3, h=5(default), b=9) => 3+50+9 = 62.
	{"skip-defaulted-middle",
		`function g(p: i32, h: i32 = 5, b: i32 = 7): i32 { return p + h * 10 + b; } function main(): i32 { return g(3, b = 9); }`, 62},
	// Named arg overrides a default: g(1, h=2, b=7(default)) => 1+20+7 = 28.
	{"override-default",
		`function g(p: i32, h: i32 = 5, b: i32 = 7): i32 { return p + h * 10 + b; } function main(): i32 { return g(1, h = 2); }`, 28},
	// String-typed param bound by name, out of order: "abc".len()+5 = 8.
	{"string-param-named",
		`function mk(prefix: string, n: i32 = 0): i32 { return prefix.len() + n; } function main(): i32 { return mk(n = 5, prefix = "abc"); }`, 8},
}

// TestSelfHostNamedArgsIR compiles each case with the self-host CLI for
// x86-64 and wasm and checks the exit code.
func TestSelfHostNamedArgsIR(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, target := range []string{"x86-64-linux", "wasm32-wasi"} {
		for _, tc := range namedArgsIRCases {
			t.Run(target+"/"+tc.name, func(t *testing.T) {
				if stderr, code := cli.exitOf(t, tc.src, target); code != tc.expected {
					t.Errorf("exited %d, want %d\n%s", code, tc.expected, stderr)
				}
			})
		}
	}
}
