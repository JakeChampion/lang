package e2e

import (
	"strings"
	"testing"
)

func TestConstFoldMatchesRuntimeAtDeclaredWidth(t *testing.T) {
	cases := []struct {
		name string
		ty   string
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
				"    let s: " + c.ty + " = " + c.seed + ";\n" +
				"    let r: " + c.ty + " = " + strings.ReplaceAll(c.expr, "@", "s") + ";\n" +
				"    if (K == r) { return 0; }\n" +
				"    return 7;\n}\n"
			assertExitsZeroEverywhere(t, src)
		})
	}
}

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
