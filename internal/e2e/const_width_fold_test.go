package e2e

import (
	"strings"
	"testing"
)

// A const initialiser and the identical expression written as code gave
// different values: `internal/constfold` evaluated in int64 and stamped the
// declared width only afterwards, so an INTERMEDIATE overflow was carried in
// 64 bits and, when the final value happened to land back in range, nothing
// caught it (#8444).
//
// `docs/INTEGER-SEMANTICS.md` makes wrapping the defined behaviour and masks
// shift counts to the operand width, so the two forms have to agree. This is
// not a backend divergence — every backend agreed, and all four were right;
// the const was the odd one out — so each case computes the expression twice
// in ONE program, once as a const and once from a `var` seeded with the same
// literal, and exits 7 when they disagree.
func TestConstFoldMatchesRuntimeAtDeclaredWidth(t *testing.T) {
	cases := []struct {
		name string
		ty   string
		// expr is instantiated twice: with the literal seed for the const,
		// with `s` for the runtime form.
		expr string
		seed string
	}{
		{"i32_add_overflow_then_halve", "i32", "(@ + 1) / 2", "2147483647"},
		{"i32_shift_count_over_width", "i32", "(@ << 33) & 255", "1"},
		{"u8_add_wraps_at_8_bits", "u8", "(@ + 1) / 2", "255"},
		{"u32_subtract_wraps", "u32", "@ - 1", "0"},
		{"i32_multiply_wraps", "i32", "@ * 100000", "100000"},
		{"u32_divide_is_unsigned", "u32", "(@ - 2) / 2", "0"},
		{"u32_shift_right_is_logical", "u32", "(@ - 1) >> 1", "0"},
		{"i32_shift_right_is_arithmetic", "i32", "(@ - 8) >> 33", "0"},
		{"i64_shift_count_masks_to_63", "i64", "@ << 64", "1"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			src := "const K: " + c.ty + " = " + strings.ReplaceAll(c.expr, "@", c.seed) + ";\n" +
				"function main(): i32 {\n" +
				"    var s: " + c.ty + " = " + c.seed + ";\n" +
				"    var r: " + c.ty + " = " + strings.ReplaceAll(c.expr, "@", "s") + ";\n" +
				"    if (K == r) { return 0; }\n" +
				"    return 7;\n}\n"
			assertExitsZeroEverywhere(t, src)
		})
	}
}

// assertExitsZeroEverywhere runs src on every available backend and requires
// exit 0; the program reports a mismatch it found itself as a non-zero status.
// Exit code rather than stdout because the wasm runner returns only a status,
// and wasm is one of the engines these cases have to cover.
//
// The interp leg goes through the DRIVER rather than the numeric-property
// harness's interpStdout: that one calls modload → checker directly and never
// folds consts, so a const never reaches it.
func assertExitsZeroEverywhere(t *testing.T, src string) {
	t.Helper()
	if code := runInterpExit(t, src); code != 0 {
		t.Errorf("interp exited %d, want 0 (non-zero = the program found a mismatch)\nsrc:\n%s", code, src)
	}
	t.Run("x86_64", func(t *testing.T) {
		if out, code := compileAndRunX86_64(t, src); code != 0 {
			t.Errorf("x86_64 exited %d, want 0; out=%q\nsrc:\n%s", code, out, src)
		}
	})
	t.Run("arm64-linux", func(t *testing.T) {
		if out, code := compileAndRunArm64(t, src); code != 0 {
			t.Errorf("arm64 exited %d, want 0; out=%q\nsrc:\n%s", code, out, src)
		}
	})
	t.Run("wasm", func(t *testing.T) {
		if code := compileAndRunWasmbinMain(t, src); code != 0 {
			t.Errorf("wasm exited %d, want 0\nsrc:\n%s", code, src)
		}
	})
}
