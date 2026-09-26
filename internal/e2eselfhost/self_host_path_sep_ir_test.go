package e2eselfhost

import "testing"

// pathSepIRCases pin the `::` path separator (`Type::f(args)`, #2700) to the
// self-host IR path. Native already lexes/parses `::` (path_sep_test.go); the
// self-host lexer now normalises `::` to a `.` token, so every `.`-handling
// parser site (postfix access, qualified names) treats it identically — the
// self-host AST carries no record of the separator. These cases prove `::`
// resolves to the same associated-function / method dispatch as `.` end to end
// on the self-host compiler. Exit codes are the oracle. Mirrors
// self_host_assoc_fn_ir_test.go (the `.` form).
var pathSepIRCases = []struct {
	name     string
	src      string
	expected int
}{
	// Associated function (inherent impl) called via `::`. 3 + 4 = 7.
	{"assoc-call",
		`struct Pt { x: i32, y: i32 } impl Pt { function make(a: i32, b: i32): Pt { return Pt { x: a, y: b }; } } function main(): i32 { var p: Pt = Pt::make(3, 4); return p.x + p.y; }`, 7},
	// Associated function via `::`, chaining a field read off the result. 3 + 20 = 23.
	{"assoc-chained",
		`struct Pt { x: i32, y: i32 } impl Pt { function make(a: i32, b: i32): Pt { return Pt { x: a, y: b }; } } function main(): i32 { return Pt::make(3, 4).x + Pt::make(10, 20).y; }`, 23},
	// `::` and `.` are interchangeable in one program — same dispatch. 0 + 9 = 9.
	{"mixed-sep",
		`struct Pt { x: i32, y: i32 } impl Pt { function origin(): Pt { return Pt { x: 0, y: 0 }; } function sum(self: Self): i32 { return self.x + self.y; } } function main(): i32 { var p: Pt = Pt::origin(); return p.sum() + 9; }`, 9},
	// Trait-impl associated function (the original #2778 form) via `::`. 42.
	{"trait-assoc",
		`trait Mk { function of(n: i32): Self; } struct Wrap { v: i32 } impl Mk for Wrap { function of(n: i32): Wrap { return Wrap { v: n }; } } function main(): i32 { var w: Wrap = Wrap::of(42); return w.v; }`, 42},
	// Enum associated constructor (nominal return) via `::`. 7.
	{"enum-ctor",
		`enum E { A(i32), B } impl E { function tag(n: i32): E { if (n > 0) { return A(n); } return B; } } function val(e: E): i32 { match (e) { A(n) => { return n; }, B => { return 99; } } return 0; } function main(): i32 { return val(E::tag(7)); }`, 7},
}

// TestSelfHostPathSepIR compiles each case with the self-host CLI for
// x86-64 and wasm and checks the exit code.
func TestSelfHostPathSepIR(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, target := range []string{"x86-64-linux", "wasm32-wasi"} {
		for _, tc := range pathSepIRCases {
			t.Run(target+"/"+tc.name, func(t *testing.T) {
				if stderr, code := cli.exitOf(t, tc.src, target); code != tc.expected {
					t.Errorf("exited %d, want %d\n%s", code, tc.expected, stderr)
				}
			})
		}
	}
}
