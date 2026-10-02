package e2eselfhost

import "testing"

// subwordWrapIRCases guard a real correctness bug: u8 arithmetic (`+` `-` `*`
// `<<`) was NOT masked back to its width on the self-host IR path, so an
// overflowing result (e.g. `255u8 + 1`) kept its full value on every IR
// backend (256) instead of wrapping (0). The interpreter and the native Go
// backends wrap per the declared width (`signExtend` by IntWidth), so this
// was a silent miscompile — the program routed through "ir" and computed the
// wrong answer. The fix records each slot's sub-word kind (local_subword, the
// sub-32-bit sibling of local_is_u32) and emits an int_cast after
// `+`/`-`/`*`/`<<` whose result is sub-word, masking exactly as `as u8`
// already does.
//
// This originally also covered i8/u16/i16, but those types were removed from
// the language (#4408); u8 is the only sub-word type left, so the i8
// (sign-extend) and u16/i16 (wider sub-word) cases are gone rather than
// force-substituted onto a type that would test something different — u8's
// add/mul/shift/sub coverage below still exercises the same masking bug.
//
// Each case is oracle-checked against the interpreter and returns a value in
// [0,126] (an equality branch reduces the wrapped result to a small code, cf.
// the wasmtime exit-code gap #2908).
var subwordWrapIRCases = []struct {
	name string
	main string
}{
	// u8 add overflow: 255 + 1 = 0 (wrap).
	{"u8-add-wrap", `function main(): i32 { var a: u8 = 255 as u8; var b: u8 = 1 as u8; var s: u8 = a + b; if ((s as i32) == 0) { return 5; } return 9; }`},
	// u8 mul overflow: 16 * 16 = 256 -> 0.
	{"u8-mul-wrap", `function main(): i32 { var a: u8 = 16 as u8; var b: u8 = 16 as u8; var s: u8 = a * b; if ((s as i32) == 0) { return 5; } return 9; }`},
	// u8 shift overflow: 1 << 8 = 256 -> 0.
	{"u8-shl-wrap", `function main(): i32 { var a: u8 = 1 as u8; var s: u8 = a << (8 as u8); if ((s as i32) == 0) { return 5; } return 9; }`},
	// u8 no overflow: 60 + 40 = 100 stays exact (kept <126 for wasmtime, #2908).
	{"u8-add-exact", `function main(): i32 { var a: u8 = 60 as u8; var b: u8 = 40 as u8; return (a + b) as i32; }`},
	// u8 subtract underflow: 0 - 1 = 255.
	{"u8-sub-wrap", `function main(): i32 { var a: u8 = 0 as u8; var b: u8 = 1 as u8; var s: u8 = a - b; if ((s as i32) == 255) { return 5; } return 9; }`},

	// A u8 field or tuple element wraps like a u8 local.
	{"u8-field-add-wrap", `struct P { x: u8, y: u8 } function main(): i32 { var p: P = P { x: 200, y: 100 }; var r: u8 = p.x + p.y; if ((r as i32) == 44) { return 5; } return 9; }`},
	{"u8-tuple-shl-wrap", `function main(): i32 { var t: (u8, u8) = (200, 1); if (((t.0 << t.1) as i32) == 144) { return 5; } return 9; }`},

	// u8 arithmetic inside a u64 context wraps at 8 bits before it widens
	// (#10574): 200 * 2 = 144, 200 + 100 = 44, 1 - 2 = 255, 200 << 1 = 144.
	{"u8-local-mul-in-u64", `function main(): i32 { var b: u8 = 200; var s: u64 = 0; if ((s + b * 2u8) == 144) { return 5; } return 9; }`},
	{"u8-local-add-in-u64", `function main(): i32 { var b: u8 = 200; var c: u8 = 100; var s: u64 = 0; if ((s + (b + c)) == 44) { return 5; } return 9; }`},
	{"u8-local-sub-in-u64", `function main(): i32 { var b: u8 = 1; var c: u8 = 2; var s: u64 = 0; if ((s + (b - c)) == 255) { return 5; } return 9; }`},
	{"u8-local-shl-in-u64", `function main(): i32 { var b: u8 = 200; var s: u64 = 0; if ((s + (b << 1u8)) == 144) { return 5; } return 9; }`},
	{"u8-field-mul-in-u64", `struct P { x: u8 } function main(): i32 { var p: P = P { x: 200 }; var s: u64 = 0; if ((s + p.x * 2u8) == 144) { return 5; } return 9; }`},
	{"u8-field-add-in-u64", `struct P { x: u8, y: u8 } function main(): i32 { var p: P = P { x: 200, y: 100 }; var s: u64 = 0; if ((s + (p.x + p.y)) == 44) { return 5; } return 9; }`},
	{"u8-tuple-mul-in-u64", `function main(): i32 { var t: (u8, u8) = (200, 7); var s: u64 = 0; if ((s + t.0 * 2u8) == 144) { return 5; } return 9; }`},
	{"u8-tuple-sub-in-u64", `function main(): i32 { var t: (u8, u8) = (1, 2); var s: u64 = 0; if ((s + (t.0 - t.1)) == 255) { return 5; } return 9; }`},
	// No overflow: the u64 sum itself is not narrowed. 1000 + 30 = 1030.
	{"u8-exact-in-u64", `function main(): i32 { var b: u8 = 20; var c: u8 = 10; var s: u64 = 1000; if ((s + (b + c)) == 1030) { return 5; } return 9; }`},

	// The same inside a u32 context.
	{"u8-local-mul-in-u32", `function main(): i32 { var b: u8 = 200; var s: u32 = 0; if ((s + b * 2u8) == 144) { return 5; } return 9; }`},
	{"u8-local-shl-in-u32", `function main(): i32 { var b: u8 = 200; var s: u32 = 0; if ((s + (b << 1u8)) == 144) { return 5; } return 9; }`},
	{"u8-field-add-in-u32", `struct P { x: u8, y: u8 } function main(): i32 { var p: P = P { x: 200, y: 100 }; var s: u32 = 0; if ((s + (p.x + p.y)) == 44) { return 5; } return 9; }`},
	{"u8-tuple-sub-in-u32", `function main(): i32 { var t: (u8, u8) = (1, 2); var s: u32 = 0; if ((s + (t.0 - t.1)) == 255) { return 5; } return 9; }`},
	// A u32 operand makes the sum a u32, which a u8 operand must not narrow.
	{"u8-exact-in-u32", `function main(): i32 { var b: u8 = 20; var c: u8 = 10; var s: u32 = 1000; if ((s + (b + c)) == 1030) { return 5; } return 9; }`},

	// A 32-bit binary inside a 64-bit context wraps at 32 bits.
	{"i32-add-wrap-in-i64", `function main(): i32 { var i: i32 = 2147483647; var s: i64 = 0; if ((s + (i + 1)) == (0 - 2147483648)) { return 5; } return 9; }`},
	{"u32-add-wrap-in-u64", `function main(): i32 { var i: u32 = 4294967295; var s: u64 = 0; if ((s + (i + 1)) == 0) { return 5; } return 9; }`},
	// A narrow checked binary is an Option, not a narrow value: its unwrapped
	// payload is what widens, sign-extended for a negative i32.
	{"narrow-checked-in-i64", `function narrow_chk(s: i64, a: i32, b: i32): Option[i64] { return Some(s + ((a +? b)?)); } function main(): i32 { match (narrow_chk(10, 2147483647, 1)) { Some(_) => { return 9; }, None => {} } match (narrow_chk(10, 0 - 30, 0 - 40)) { Some(v) => { if (v == (0 - 60)) { return 5; } return 9; }, None => { return 9; } } }`},
}

// TestSelfHostSubwordWrapIR compiles each case with the self-host CLI for
// x86-64, arm64 and wasm, and checks the exit code against the interpreter.
func TestSelfHostSubwordWrapIR(t *testing.T) {
	cli := buildSelfHostCLI(t)
	interpBin := buildLangBinForInterp(t)
	for _, tc := range subwordWrapIRCases {
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
