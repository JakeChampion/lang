package e2eselfhost

import "testing"

// nestedMatchIRCases pin a `match` nested inside another `match` arm's body to the
// self-host IR path on x86-64 + wasm. lower_stmt's StmtMatch arm lowers each arm
// body via lower_block, which recurses through lower_stmt for every statement —
// including a nested StmtMatch — with no guard against nesting (the only bails are
// per-arm pattern/payload shapes, which apply identically at any depth). The
// self-hosted compiler emits this construct itself: @derive(Eq) codegen builds an
// inner match as an outer arm body, and @derive(Eq) already routes IR. No other
// self-host test nests a match in an arm (the block-expr `match-arm-block` case
// only nests a block).
//
// Each case is oracle-checked against the interpreter, mirroring
// self_host_block_expr_ir_test.go.
// Patterns stay in the lowerable subset at every level (scalar variant payloads,
// i32/string literals, trailing _/variant); every result is <= 126 (wasmtime
// exit-code truncation, cf. #2908).
var nestedMatchIRCases = []struct {
	name string
	main string
}{
	// match on a variant, nested match on another variant in the A arm.
	{"nested-variant-in-variant", `enum E { A(i32), B }
function f(x: E, y: E): i32 { match (x) { A(n) => { match (y) { A(m) => { return n + m; }, B => { return n; }, } }, B => { return 0; }, } }
function main(): i32 { return f(A(20), A(22)); }`},
	// Outer variant match, inner literal (i32) match.
	{"nested-lit-in-variant", `enum E { A(i32), B }
function f(x: E, k: i32): i32 { match (x) { A(n) => { match (k) { 0 => { return n; }, _ => { return n + k; }, } }, B => { return 99; }, } }
function main(): i32 { return f(A(5), 3); }`},
	// Outer literal match, inner variant match.
	{"nested-variant-in-lit", `enum E { A(i32), B }
function f(k: i32, y: E): i32 { match (k) { 0 => { match (y) { A(m) => { return m; }, B => { return 1; }, } }, _ => { return 7; }, } }
function main(): i32 { return f(0, A(8)); }`},
	// Three levels of nesting.
	{"nested-3deep", `enum E { A(i32), B }
function f(a: E, b: E, c: E): i32 { match (a) { A(x) => { match (b) { A(y) => { match (c) { A(z) => { return x + y + z; }, B => { return x + y; }, } }, B => { return x; }, } }, B => { return 0; }, } }
function main(): i32 { return f(A(1), A(2), A(3)); }`},
	// Inner match scrutinises a STRING.
	{"nested-string-inner", `function f(k: i32, s: string): i32 { match (k) { 0 => { match (s) { "hi" => { return 2; }, _ => { return 0; }, } }, _ => { return 9; }, } }
function main(): i32 { return f(0, "hi"); }`},
	// Nested match in EXPRESSION-value (tail) position — exercises lower_value_tail.
	{"nested-expr-value", `enum E { A(i32), B }
function f(x: E, k: i32): i32 { let r: i32 = match (x) { A(n) => match (k) { 0 => n, _ => n + k }, B => 0 }; return r; }
function main(): i32 { return f(A(7), 3); }`},
	// Nested match inside a while-loop body, composing with surrounding control flow.
	{"nested-in-while", `enum E { A(i32), B }
function classify(x: E): i32 { match (x) { A(n) => { match (n) { 0 => { return 1; }, _ => { return 2; }, } }, B => { return 0; }, } }
function main(): i32 { let s: i32 = 0; let i: i32 = 0; while (i < 5) { s = s + classify(A(i)); i = i + 1; } return s; }`},
	// Regression guard: a flat single-level match.
	{"single-match-regress", `function f(k: i32): i32 { match (k) { 0 => { return 5; }, _ => { return 9; }, } }
function main(): i32 { return f(0); }`},
}

// TestSelfHostNestedMatchIR compiles each case with the self-host CLI for
// x86-64 and wasm and checks the exit code against the interpreter.
func TestSelfHostNestedMatchIR(t *testing.T) {
	cli := buildSelfHostCLI(t)
	interpBin := buildLangBinForInterp(t)
	for _, tc := range nestedMatchIRCases {
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
