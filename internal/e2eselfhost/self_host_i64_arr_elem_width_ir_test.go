package e2eselfhost

import "testing"

// i64ArrElemWidthIRCases pin an i32-element ARRAY index consumed in an i64
// arithmetic context (`s64 + a[i]`, the canonical numeric reduction summing an
// i32[] into an i64 accumulator) to the self-host IR path on x86-64 + wasm.
// lower_i64's ExprIndex arm must not lower only i64[]/u64[] (8-byte) elements
// and bail every other array source via `return s.fail()`, dropping the whole
// module to the legacy AST emitter. #2691 widens it: a plain 32-bit
// element array (new arr_index_is_i32_scalar — an i32[] or u32[] ident slot, not
// i64[]/f64[]/string[]/T[][]/closure[]) had its element lowered via lower_expr
// (arr_get) and sign/zero-extended to i64 (op_int_extend; the checker forbids
// i64 + u32, so a plain i32[] element here is signed). Each case narrows the i64
// result with `as i32` so the wasm _start exit code is a valid i32 in [0,126),
// and is oracle-checked against the interpreter.
var i64ArrElemWidthIRCases = []struct {
	name string
	main string
}{
	// Sum an i32[] into an i64 accumulator across a for-range. 10+20+30 = 60.
	{"sum-loop", `function main(): i32 { let a: i32[] = [10,20,30]; let s: i64 = 0; for i in 0..3 { s = s + a[i]; } return s as i32; }`},
	// Direct i64 + i32 element. 36 + a[1] = 36 + 6 = 42.
	{"direct-elem", `function main(): i32 { let a: i32[] = [5,6,7]; let s: i64 = 36; return (s + a[1]) as i32; }`},
	// Sign-extension: NEGATIVE i32 elements must sign-extend. 50 + (-5) + (-3) = 42.
	{"neg-elems", `function main(): i32 { let a: i32[] = [-5, -3]; let s: i64 = 50; return (s + a[0] + a[1]) as i32; }`},
	// Element used in a multiply inside the i64 context. 0 + 6*7 = 42.
	{"elem-mul", `function main(): i32 { let a: i32[] = [6, 7]; let s: i64 = 0; return (s + a[0] * a[1]) as i32; }`},
	// Regression: an i64[] element source still uses the 8-byte read. 10 + 32 = 42.
	{"i64arr-keep", `function main(): i32 { let a: i64[] = [10, 32]; let s: i64 = 0; return (s + a[0] + a[1]) as i32; }`},
}

// TestSelfHostI64ArrElemWidthIR compiles each case with the self-host CLI for
// x86-64 and wasm and checks the exit code against the interpreter.
func TestSelfHostI64ArrElemWidthIR(t *testing.T) {
	cli := buildSelfHostCLI(t)
	interpBin := buildLangBinForInterp(t)
	for _, tc := range i64ArrElemWidthIRCases {
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
