package e2eselfhost

import "testing"

// u64MixWidthIRCases pin a u32 (or u8) scalar leaf consumed in a u64 arithmetic
// context (`s64u + u`) to the self-host IR path on x86-64, arm64 + wasm. This is the unsigned
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
	{"u32-ident", `function main(): i32 { let u: u32 = 2; let s: u64 = 40; return (s + u) as i32; }`},
	// u32 local accumulated into a u64 across a for-range. 7*3 = 21.
	{"u32-loop", `function main(): i32 { let s: u64 = 0; let u: u32 = 7; for i in 0..3 { s = s + u; } return s as i32; }`},
	// u32 in a multiply inside the u64 context. 0 + 6*6 = 36.
	{"u32-mul", `function main(): i32 { let u: u32 = 6; let s: u64 = 0; return (s + u * u) as i32; }`},
	// An all-constant record is a static box (#10446) whose u32 words are the
	// literal's text, not a zero (#10490, #10535): u64 + u32 field with the
	// field on either side, a u32 among i32 and boolean fields, and two records
	// differing only in a u32 field, which are two boxes (#10501). 30 + 12 = 42.
	{"u32-field", `struct P { x: u32 } function main(): i32 { let p: P = P { x: 12 }; let s: u64 = 30; return (s + p.x) as i32; }`},
	{"u32-field-lhs", `struct P { x: u32 } function main(): i32 { let p: P = P { x: 12 }; let s: u64 = 30; return (p.x + s) as i32; }`},
	{"u32-field-static-mixed", `struct P { a: i32, x: u32, on: boolean } function main(): i32 { let p: P = P { a: 0 - 5, x: 40, on: true }; let s: u64 = 7; if (p.on) { return (s + p.x) as i32 + p.a; } return 0; }`},
	{"u32-field-static-distinct", `struct P { x: u32 } function main(): i32 { let p: P = P { x: 30 }; let q: P = P { x: 12 }; let s: u64 = 0; return (s + p.x + q.x) as i32; }`},
	// A u32 literal past 2^31 or in hex is not a static word, so its record is
	// on the heap, as is one built from a parameter. 4000000000 - 3999999958 = 42.
	{"u32-field-heap-high", `struct P { x: u32 } function main(): i32 { let p: P = P { x: 4000000000 }; let s: u64 = 0; if (s + p.x == 4000000000) { return 42; } return 7; }`},
	{"u32-field-heap-hex", `struct P { x: u32 } function main(): i32 { let p: P = P { x: 0x1E }; let s: u64 = 12; return (s + p.x) as i32; }`},
	{"u32-field-heap-param", `struct P { x: u32 } @noinline function mk(v: u32): P { return P { x: v }; } function main(): i32 { let p: P = mk(12); let s: u64 = 30; return (s + p.x) as i32; }`},
	{"u32-field-heap-param-high", `struct P { x: u32 } @noinline function mk(v: u32): P { return P { x: v }; } function main(): i32 { let p: P = mk(4000000000); let s: u64 = 0; if (p.x + s == 4000000000) { return 42; } return 7; }`},
	// A u32-returning call past 2^31 zero-extends too.
	{"u32-call-high", `@noinline function f(): u32 { return 4000000000; } function main(): i32 { let s: u64 = 5; if (s + f() == 4000000005) { return 42; } return 7; }`},
	// u64 + u32 tuple element. 30 + 12 = 42.
	{"u32-tuple", `function main(): i32 { let t: (u32, u32) = (12, 7); let s: u64 = 30; return (s + t.0) as i32; }`},
	// A u8 local, field, tuple element or variant payload widens like a u32
	// one, into a u64 or a u32. A value of 128 or more compares against its
	// zero-extended sum, since an exit code cannot tell it from the
	// sign-extended one: the two differ by exactly 256.
	{"u8-ident", `function main(): i32 { let b: u8 = 200; let s: u64 = 30; if (s + b == 230) { return 42; } return 7; }`},
	{"u8-ident-u32-high", `function main(): i32 { let b: u8 = 200; let s: u32 = 4000000000; if (s + b == 4000000200) { return 42; } return 7; }`},
	{"u8-field-u32", `struct P { x: u8 } function main(): i32 { let p: P = P { x: 12 }; let s: u32 = 30; return (s + p.x) as i32; }`},
	{"u8-payload", `@noinline function f(b: u8): Result[u64, string] { return Ok(b); } function main(): i32 { match (f(42)) { Ok(v) => { return v as i32; }, Err(_) => { return 1; } } }`},
	{"u8-field", `struct P { x: u8 } function main(): i32 { let p: P = P { x: 12 }; let s: u64 = 30; return (s + p.x) as i32; }`},
	{"u8-field-lhs-bound", `struct P { x: u8 } function main(): i32 { let p: P = P { x: 12 }; let s: u64 = 30; let r: u64 = p.x + s; return r as i32; }`},
	{"u8-field-heap-max", `struct P { x: u8 } @noinline function mk(v: u8): P { return P { x: v }; } function main(): i32 { let p: P = mk(255); let s: u64 = 0; if (s + p.x == 255) { return 42; } return 7; }`},
	{"u8-tuple", `function main(): i32 { let t: (u8, u8) = (200, 7); let s: u64 = 30; if (s + t.0 == 230) { return 42; } return 7; }`},
	// u64 + u32[] element across a reduction. 10+20+30 = 60.
	{"u32-arr", `function main(): i32 { let a: u32[] = [10,20,30]; let s: u64 = 0; for i in 0..3 { s = s + a[i]; } return s as i32; }`},
	// Regression: the i64 + i32 family is unchanged by the u32 admission, and a
	// negative i32 field still sign-extends. 40 + 2 = 42; 50 - 8 = 42.
	{"i64-i32-keep", `function main(): i32 { let i: i32 = 2; let s: i64 = 40; return (s + i) as i32; }`},
	{"i64-i32-field-neg", `struct P { x: i32 } @noinline function mk(v: i32): P { return P { x: v }; } function main(): i32 { let p: P = mk(0 - 8); let s: i64 = 50; return (s + p.x) as i32; }`},
}

// TestSelfHostU64MixWidthIR compiles each case with the self-host CLI for
// x86-64, arm64 and wasm under both lowerings, treating an IR bail as an
// error, and checks the exit code against the interpreter.
func TestSelfHostU64MixWidthIR(t *testing.T) {
	cli := buildSelfHostCLI(t)
	interpBin := buildLangBinForInterp(t)
	for _, tc := range u64MixWidthIRCases {
		src := tc.main + "\n"
		want := interpExit(t, interpBin, src)
		for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
			t.Run(target+"/"+tc.name, func(t *testing.T) {
				if stderr, code := cli.exitOf(t, src, target, "FERN_STRICT_IR=1"); code != want {
					t.Errorf("exited %d, want %d (interp oracle)\n%s", code, want, stderr)
				}
			})
		}
	}
}
