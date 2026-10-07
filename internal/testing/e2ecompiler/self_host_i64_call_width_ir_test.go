package e2ecompiler

import "testing"

// i64CallWidthIRCases pin an i32-returning CALL consumed in an i64 arithmetic
// context (`s64 + g()`) to the self-host IR path on x86-64 + wasm: the call
// result is sign-extended to i64 (#2691). The checker forbids i64 +
// f64/string/u32 and rejects binding a bare i32 call to an i64 (E009; it needs
// an explicit `as i64`), so such a call in a valid program returns a signed
// i32. Each case narrows the i64 result with `as i32` (valid wasm exit code in
// [0,126)) and is oracle-checked against the interpreter.
var i64CallWidthIRCases = []struct {
	name string
	main string
}{
	// i64 local + i32-returning free function. 30 + 12 = 42.
	{"call-free", `function g(): i32 { return 12; } function main(): i32 { let s: i64 = 30; return (s + g()) as i32; }`},
	// Call with an argument. 30 + (6*2) = 42.
	{"call-arg", `function g(x: i32): i32 { return x * 2; } function main(): i32 { let s: i64 = 30; return (s + g(6)) as i32; }`},
	// Sign-extension: a call returning a NEGATIVE i32 must sign-extend. 50 + (-8) = 42.
	{"call-neg", `function g(): i32 { return -8; } function main(): i32 { let s: i64 = 50; return (s + g()) as i32; }`},
	// i32-returning METHOD call. 30 + 12 = 42.
	{"call-method", `struct C { n: i32 } function (c: C) val(): i32 { return c.n; } function main(): i32 { let c: C = C { n: 12 }; let s: i64 = 30; return (s + c.val()) as i32; }`},
	// Call inside a for-range accumulating into i64. inc(0)+inc(1)+inc(2) = 1+2+3 = 6.
	{"call-loop", `function inc(x: i32): i32 { return x + 1; } function main(): i32 { let s: i64 = 0; for i in 0..3 { s = s + inc(i); } return s as i32; }`},
	// Regression: an i64-returning call still lowers as a native i64. 0 + 42 = 42.
	{"call-i64-keep", `function g(): i64 { return 42; } function main(): i32 { let s: i64 = 0; return (s + g()) as i32; }`},
}

// TestSelfHostI64CallWidthIR compiles each case with the self-host CLI for
// x86-64 and wasm and checks the exit code against the interpreter.
func TestSelfHostI64CallWidthIR(t *testing.T) {
	cli := buildSelfHostCLI(t)
	interpBin := buildLangBinForInterp(t)
	for _, tc := range i64CallWidthIRCases {
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
