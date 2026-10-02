package e2eselfhost

import "testing"

// whileIRCases pin the standalone `while`-loop construct to the self-host IR path
// on x86-64 + wasm. The while lowering emits a
// wasm-style block/loop/br_if and is IR-eligible for any i32-condition loop — it
// bails only on a 64-bit-width condition. while loops are exercised for exit
// codes throughout self_host_asm_run_test.go (while-sum, while-early-return, the
// print loops, …); these cases check each exit code against the interp oracle,
// mirroring self_host_block_expr_ir_test.go.
//
// Every condition is i32 and every result is small (<= 126, wasmtime exit-code
// truncation, cf. #2908).
var whileIRCases = []struct {
	name string
	main string
}{
	// Accumulate a sum: 1+2+3+4+5 = 15.
	{"while-sum", `function main(): i32 { var i: i32 = 1; var s: i32 = 0; while (i <= 5) { s = s + i; i = i + 1; } return s; }`},
	// Early return out of the loop body: returns at i == 7.
	{"while-early-return", `function main(): i32 { var i: i32 = 0; while (i < 100) { if (i == 7) { return i; } i = i + 1; } return 0 - 1; }`},
	// Zero-iteration loop (false at entry): body never runs, s stays 7.
	{"while-zero-iter", `function main(): i32 { var s: i32 = 7; var i: i32 = 5; while (i < 5) { s = s + 1; } return s; }`},
	// Compound step (i += 2): s = 0+2+4 = 6.
	{"while-compound-step", `function main(): i32 { var s: i32 = 0; var i: i32 = 0; while (i < 6) { s = s + i; i = i + 2; } return s; }`},
	// Nested while loops: outer 4 × inner 3 = 12 increments.
	{"while-nested", `function main(): i32 { var c: i32 = 0; var i: i32 = 0; while (i < 4) { var j: i32 = 0; while (j < 3) { c = c + 1; j = j + 1; } i = i + 1; } return c; }`},
}

// TestSelfHostWhileIR compiles each case with the self-host CLI for
// x86-64 and wasm and checks the exit code against the interpreter.
func TestSelfHostWhileIR(t *testing.T) {
	cli := buildSelfHostCLI(t)
	interpBin := buildLangBinForInterp(t)
	for _, tc := range whileIRCases {
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
