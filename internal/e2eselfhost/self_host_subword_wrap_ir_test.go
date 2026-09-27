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
}

// TestSelfHostSubwordWrapIR compiles each case with the self-host CLI for
// x86-64 and wasm and checks the exit code against the interpreter.
func TestSelfHostSubwordWrapIR(t *testing.T) {
	cli := buildSelfHostCLI(t)
	interpBin := buildLangBinForInterp(t)
	for _, tc := range subwordWrapIRCases {
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
