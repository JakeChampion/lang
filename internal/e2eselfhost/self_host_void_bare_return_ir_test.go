package e2eselfhost

import "testing"

// voidBareReturnIRCases pin a VOID function with an explicit bare `return;`
// (value-less) to the self-host IR path on x86-64 + wasm. parse_expr yields an
// ExprUnknown for the missing return value, which lower_expr couldn't lower, so the
// StmtReturn arm bailed the whole module to the legacy AST emitter — affecting
// every void helper with an early `return;` (the common guard-clause shape in the
// CLI tools). #2691 detects the ExprUnknown (only reachable in a void function;
// the checker rejects a value-less return elsewhere) and emits a dummy 0 before
// the exit dec-sweep + op_return, mirroring `return 0` (a void caller ignores the
// result). Each case is oracle-checked against the interpreter and returns <= 126.
var voidBareReturnIRCases = []struct {
	name string
	main string
}{
	// Tail bare return in a void function. main returns 42 after f.
	{"tail", `function f(x: i32): void { print("hi"); return; } function main(): i32 { f(5); return 42; }`},
	// Early bare return (guard taken) — the print is skipped. 42.
	{"early-taken", `function f(x: i32): void { if (x > 0) { return; } print("neg"); } function main(): i32 { f(5); return 42; }`},
	// Early bare return (guard NOT taken) — the print runs, then fall-through. 42.
	{"early-not-taken", `function f(x: i32): void { if (x > 0) { return; } print("neg"); } function main(): i32 { f(0 - 1); return 42; }`},
	// Bare return inside a loop (continue-like exit). 42.
	{"in-loop", `function f(n: i32): void { var i: i32 = 0; while (i < n) { if (i == 2) { return; } print("x"); i = i + 1; } } function main(): i32 { f(5); return 42; }`},
	// Two void helpers, each with a bare return, both called. 42.
	{"two-helpers", `function a(x: i32): void { if (x > 0) { return; } print("a"); } function b(x: i32): void { print("b"); return; } function main(): i32 { a(1); b(2); return 42; }`},
}

// TestSelfHostVoidBareReturnIR compiles each case with the self-host CLI for
// x86-64 and wasm and checks the exit code against the interpreter.
func TestSelfHostVoidBareReturnIR(t *testing.T) {
	cli := buildSelfHostCLI(t)
	interpBin := buildLangBinForInterp(t)
	for _, tc := range voidBareReturnIRCases {
		src := tc.main + "\n"
		want := interpExit(t, interpBin, src)
		for _, target := range []string{"x86-64-linux", "wasm32-wasi"} {
			t.Run(target+"/"+tc.name, func(t *testing.T) {
				if stderr, code := cli.exitOf(t, src, target); code != want {
					t.Errorf("exited %d, want %d (interp oracle)\n%s", code, want, stderr)
				}
			})
		}
	}
}
