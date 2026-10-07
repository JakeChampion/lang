package e2ecompiler

import "testing"

// i64MixWidthIRCases pin an i32-width scalar leaf consumed in an i64 ARITHMETIC
// context (`s64 + i32`, a mixed-width local/param, a for-range loop var
// accumulated into an i64) to the self-host IR path on x86-64 + wasm (#2691).
// The i32 leaf is lowered as an i32 and sign/zero-extended to i64
// (op_int_extend; zero-extend for u32, sign-extend for signed i32 / subword),
// including the i32 leaves of nested sub-expressions. Each case narrows the
// i64 result with `as i32` so the wasm `_start` exit code is a valid i32 in
// [0,126); each is oracle-checked against the interpreter.
var i64MixWidthIRCases = []struct {
	name string
	main string
}{
	// for-range loop var (i32) accumulated into an i64. 0+1+2+3+4 = 10.
	{"range-acc", `function main(): i32 { let s: i64 = 0; for i in 0..5 { s = s + i; } return s as i32; }`},
	// Inclusive range. 1+2+3+4+5 = 15.
	{"range-incl", `function main(): i32 { let s: i64 = 0; for i in 1..=5 { s = s + i; } return s as i32; }`},
	// i64 local + i32 local. 40 + 2 = 42.
	{"mix-local", `function main(): i32 { let i: i32 = 2; let s: i64 = 40; return (s + i) as i32; }`},
	// i64 local + i32 PARAM. 39 + 3 = 42.
	{"mix-param", `function f(a: i32): i64 { let s: i64 = 39; return s + a; } function main(): i32 { return f(3) as i32; }`},
	// i32 leaf inside a nested sub-expression (`s + (i + 2)`). 30 + (5+2) = 37.
	{"mix-nested", `function main(): i32 { let i: i32 = 5; let s: i64 = 30; return (s + (i + 2)) as i32; }`},
	// i64 * i32. 14 * 3 = 42.
	{"mix-mul", `function main(): i32 { let i: i32 = 3; let s: i64 = 14; return (s * i) as i32; }`},
	// Sign-extension: a NEGATIVE i32 must sign-extend (not zero-extend). 50 + (-8) = 42.
	{"neg-sign", `function main(): i32 { let i: i32 = -8; let s: i64 = 50; return (s + i) as i32; }`},
	// Accumulate over 100 iterations in the i64 domain, then narrow. sum(0..99)=4950, /100 = 49.
	{"big-acc", `function main(): i32 { let s: i64 = 0; for i in 0..100 { s = s + i; } return (s / 100) as i32; }`},
	// An UNANNOTATED literal-only compound whose width comes from a literal past
	// i32 range (#8668): the binding is i64 on both compilers, and the value is
	// the one the source wrote, not the truncated i32 default. 2^62 / 10^18 = 4,
	// +40 = 44.
	{"wide-literal-compound", `function main(): i32 { let t = 3 - 4611686018427387904; let u = 4611686018427387904 - 3; if (t != 0 - u) { return 1; } return ((u / 1000000000000000000) as i32) + 40; }`},
	// The same literal as a generic call's only T argument and on either side
	// of a comparison: each shape adds its own bit, 15 when all four widen.
	{"wide-literal-generic-compare", `function id[T](v: T): T { return v; } function main(): i32 { let t = id(4611686018427387904); let c = 0; if (t > 0) { c = c + 1; } if (4611686018427387904 > 1) { c = c + 2; } let b = 1 < 4611686018427387904; if (b) { c = c + 4; } if (4611686018427387904 != 0) { c = c + 8; } return c; }`},
	// The literal pins a T carried inside a tuple result: the binding is
	// (i64, string) on both compilers. 3 when both bits hold.
	{"wide-literal-generic-tuple", `function pair[A, B](a: A, b: B): (A, B) { return (a, b); } function main(): i32 { let p = pair(4611686018427387904, "hello"); let c = 0; if (p.0 == 4611686018427387904 && p.1 == "hello") { c = c + 1; } if (p.0 / 1000000000000000000 == 4) { c = c + 2; } return c; }`},
}

// TestSelfHostI64MixWidthIR compiles each case with the self-host CLI for
// x86-64 and wasm and checks the exit code against the interpreter.
func TestSelfHostI64MixWidthIR(t *testing.T) {
	cli := buildSelfHostCLI(t)
	interpBin := buildLangBinForInterp(t)
	for _, tc := range i64MixWidthIRCases {
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
