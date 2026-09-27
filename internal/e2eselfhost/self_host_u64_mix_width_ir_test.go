package e2eselfhost

import "testing"

// u64MixWidthIRCases pin a u32 scalar leaf consumed in a u64 arithmetic context
// (`s64u + u`) to the self-host IR path on x86-64, arm64 + wasm. This is the unsigned
// mirror of the i64 mixed-width family: u64 is the ONLY context an unsigned
// 32-bit leaf can appear, since the checker forbids i64 + u32 (E009). The i32
// ident widening (is_i32_scalar_slot) wrongly REJECTED a u32 scalar: some
// var-decl paths record a u32 local's type tag in local_struct_type ("u32"), and
// is_i32_scalar_slot's struct-type exclusion fired on that bogus tag, leaving
// `u64 + u32` unlowerable. #2691 admits a u32 scalar (the authoritative
// is_u32_slot signal) before the heap-type exclusions, and the widen zero-extends
// it (op_int_extend(is_u32_slot)). Each case narrows the u64 result with `as i32`
// (valid wasm exit code in [0,126)) and is oracle-checked against the interpreter.
var u64MixWidthIRCases = []struct {
	name string
	main string
}{
	// u64 local + u32 local. 40 + 2 = 42.
	{"u32-ident", `function main(): i32 { var u: u32 = 2; var s: u64 = 40; return (s + u) as i32; }`},
	// u32 local accumulated into a u64 across a for-range. 7*3 = 21.
	{"u32-loop", `function main(): i32 { var s: u64 = 0; var u: u32 = 7; for i in 0..3 { s = s + u; } return s as i32; }`},
	// u32 in a multiply inside the u64 context. 0 + 6*6 = 36.
	{"u32-mul", `function main(): i32 { var u: u32 = 6; var s: u64 = 0; return (s + u * u) as i32; }`},
	// u64 + u32 struct field. 30 + 12 = 42.
	{"u32-field", `struct P { x: u32 } function main(): i32 { var p: P = P { x: 12 }; var s: u64 = 30; return (s + p.x) as i32; }`},
	// An all-constant record is a static box (#10446); its u32 words are the
	// literal's text, not a zero (#10490). A value past 2^31, a hex literal, and
	// a u32 among i32 and boolean fields. 4000000000 - 3999999958 = 42.
	{"u32-field-static-high", `struct P { x: u32 } function main(): i32 { var p: P = P { x: 4000000000 }; var s: u64 = 0; return ((s + p.x) - 3999999958) as i32; }`},
	{"u32-field-static-hex", `struct P { x: u32 } function main(): i32 { var p: P = P { x: 0x1E }; var s: u64 = 12; return (s + p.x) as i32; }`},
	{"u32-field-static-mixed", `struct P { a: i32, x: u32, on: boolean } function main(): i32 { var p: P = P { a: 0 - 5, x: 40, on: true }; var s: u64 = 7; if (p.on) { return (s + p.x) as i32 + p.a; } return 0; }`},
	// u64 + u32 tuple element. 30 + 12 = 42.
	{"u32-tuple", `function main(): i32 { var t: (u32, u32) = (12, 7); var s: u64 = 30; return (s + t.0) as i32; }`},
	// u64 + u32[] element across a reduction. 10+20+30 = 60.
	{"u32-arr", `function main(): i32 { var a: u32[] = [10,20,30]; var s: u64 = 0; for i in 0..3 { s = s + a[i]; } return s as i32; }`},
	// Regression: the i64 + i32 family is unchanged by the u32 admission. 40 + 2 = 42.
	{"i64-i32-keep", `function main(): i32 { var i: i32 = 2; var s: i64 = 40; return (s + i) as i32; }`},
}

// TestSelfHostU64MixWidthIR compiles each case with the self-host CLI for
// x86-64, arm64 and wasm and checks the exit code against the interpreter.
func TestSelfHostU64MixWidthIR(t *testing.T) {
	cli := buildSelfHostCLI(t)
	interpBin := buildLangBinForInterp(t)
	for _, tc := range u64MixWidthIRCases {
		src := tc.main + "\n"
		want := interpExit(t, interpBin, src)
		for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
			t.Run(target+"/"+tc.name, func(t *testing.T) {
				if stderr, code := cli.exitOf(t, src, target); code != want {
					t.Errorf("exited %d, want %d (interp oracle)\n%s", code, want, stderr)
				}
			})
		}
	}
}
