package e2eselfhost

import "testing"

// labeledBreakIRCases widen the self-host IR subset to labeled break/continue —
// `outer: while/for { … break outer; … continue outer; … }`. The parser records a
// loop label on StmtWhile/StmtFor and a target label on break/continue; a
// resolve_labels pass (run at the shared parse entry, before any desugar) bakes
// each labeled break/continue's RELATIVE loop depth into its `tag`; and irlower's
// break/continue lowering targets loop_blk[len-1-tag] (tag 0 = innermost =
// unchanged behaviour for unlabeled). Verified to match the interpreter on
// x86-64 + wasm (and arm64 via qemu).
//
// Each case is oracle-checked against the interpreter and returns a value
// <= 120 (cf. the wasmtime exit-code gap #2908).
var labeledBreakIRCases = []struct {
	name string
	main string
}{
	// break to the outer loop from a nested loop.
	{"break-outer", `function main(): i32 { var i = 0; var c = 0; outer: while (i < 5) { i = i + 1; var j = 0; while (j < 5) { j = j + 1; c = c + 1; if (j == 2) { break outer; } } } return c; }`},
	// continue the outer loop, skipping the rest of the inner body.
	{"continue-outer", `function main(): i32 { var i = 0; var c = 0; outer: while (i < 3) { i = i + 1; var j = 0; while (j < 3) { j = j + 1; if (j == 1) { continue outer; } c = c + 1; } } return c; }`},
	// labeled for loops.
	{"labeled-for-break", `function main(): i32 { var c = 0; outer: for i in 0..3 { for j in 0..3 { c = c + 1; if (j == 1) { break outer; } } } return c; }`},
	// A labeled C-style `for`. The Stmt union has no node for it — the parser
	// desugars it to `if (true) { INIT; var __forc_L_C = true; while (true)
	// { … } }` — so the label has to land on the while inside that scoping if,
	// not on the if. It did not, so the name named no loop and `break outer`
	// left the INNER loop instead: the same program, one exit level short.
	{"labeled-c-for-break", `function main(): i32 { var c = 0; outer: for (var i = 0; i < 3; i = i + 1) { for j in 0..3 { c = c + 1; if (j == 1) { break outer; } } } return c; }`},
	{"labeled-c-for-continue", `function main(): i32 { var c = 0; outer: for (var i = 0; i < 3; i = i + 1) { for j in 0..3 { c = c + 1; if (j == 0) { continue outer; } } } return c; }`},
	// triple nesting, continue the MIDDLE loop (depth 1 from the innermost).
	{"triple-continue-mid", `function main(): i32 { var c = 0; mid: for i in 0..3 { for j in 0..3 { for k in 0..3 { c = c + 1; if (k == 0) { continue mid; } } } } return c; }`},
	// labeled break out of a match arm inside the inner loop (exercises the
	// match-arm recursion in resolve_labels).
	{"break-from-match", `function main(): i32 { var c = 0; outer: for i in 0..4 { for j in 0..4 { match (j) { 2 => { break outer; }, _ => { c = c + 1; } } } } return c; }`},
	// regression: plain (unlabeled) break still targets the innermost loop.
	{"plain-break", `function main(): i32 { var i = 0; var c = 0; while (i < 3) { i = i + 1; var j = 0; while (j < 3) { j = j + 1; c = c + 1; if (j == 1) { break; } } } return c; }`},
	// regression: plain continue.
	{"plain-continue", `function main(): i32 { var i = 0; var s = 0; while (i < 5) { i = i + 1; if (i == 3) { continue; } s = s + i; } return s; }`},
}

// TestSelfHostLabeledBreakIR compiles each case with the self-host CLI for
// x86-64 and wasm and checks the exit code against the interpreter.
func TestSelfHostLabeledBreakIR(t *testing.T) {
	cli := buildSelfHostCLI(t)
	interpBin := buildLangBinForInterp(t)
	for _, tc := range labeledBreakIRCases {
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
