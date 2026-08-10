// Regression coverage for a batch of cross-backend correctness
// bugs found by differential execution (interp vs x86-64 vs arm64
// vs wasm):
//
//   - Signed right-shift `>>` on a negative i32 must be an
//     arithmetic (sign-propagating) shift. The x86-64 / arm64
//     backends emitted a 64-bit `sar` / `asr` on a value that
//     rides zero-extended in the register, reading bit 63 (= 0)
//     as the sign and producing a logical-shift result.
//   - Runtime shift counts must be masked to the operand width
//     (0..31 for i32, 0..63 for i64), the way every codegen
//     backend and wasm do. The interpreter applied the count
//     unmasked, so `-8 >> 33` saturated to -1 instead of -4.
//   - An untyped float literal defaults to f64 (double), not f32.
//     The f32 default silently halved the precision of any literal
//     not explicitly annotated `f64`.
//
// The interp is the source of truth; each native / wasm backend
// runs in its own sub-test so it skips individually when its
// toolchain is missing.
package e2e

import "testing"

func assertBackendsAgreeWithInterp(t *testing.T, name, src string) {
	t.Helper()
	want := runInterpByte(t, src)
	t.Run(name+"/x86_64", func(t *testing.T) {
		_, code := compileAndRunX86_64(t, src)
		if code != want {
			t.Errorf("x86_64 exit=%d, interp=%d\nsrc:\n%s", code, want, src)
		}
	})
	t.Run(name+"/arm64", func(t *testing.T) {
		_, code := compileAndRunArm64(t, src)
		if code != want {
			t.Errorf("arm64 exit=%d, interp=%d\nsrc:\n%s", code, want, src)
		}
	})
	t.Run(name+"/wasmbin", func(t *testing.T) {
		got := compileAndRunWasmbinMain(t, src)
		if got != want {
			t.Errorf("wasmbin result=%d, interp=%d\nsrc:\n%s", got, want, src)
		}
	})
}

// TestBugHunt_SignedShiftParity guards the arithmetic-vs-logical
// right-shift fix and the shift-count masking fix. Each case
// returns 7 when the shift behaves correctly and 1 otherwise, so
// a backend that regresses to a logical shift (or an unmasked
// count) diverges from the interp's 7.
func TestBugHunt_SignedShiftParity(t *testing.T) {
	cases := []struct {
		name string
		src  string
	}{
		// -7 >> 1 is arithmetic: 0xFFFFFFF9 >> 1 = 0xFFFFFFFC = -4.
		// A logical shift would give 0x7FFFFFFC = 2147483644.
		{"arith_shr_neg", `function main(): i32 { if (((0 - 7) >> 1) == (0 - 4)) { return 7; } return 1; }`},
		// All-ones stays all-ones under arithmetic shift.
		{"arith_shr_allones", `function main(): i32 { if (((0 - 1) >> 5) == (0 - 1)) { return 7; } return 1; }`},
		// Runtime count 33 masks to 33 & 31 == 1: -8 >> 1 = -4.
		{"shr_count_mask", `function main(): i32 { var s: i32 = 33; if (((0 - 8) >> s) == (0 - 4)) { return 7; } return 1; }`},
		// Left-shift count masks the same way: 1 << (33 & 31) = 2.
		{"shl_count_mask", `function main(): i32 { var s: i32 = 33; if ((1 << s) == 2) { return 7; } return 1; }`},
	}
	for _, c := range cases {
		assertBackendsAgreeWithInterp(t, c.name, c.src)
	}
}

// TestBugHunt_FloatLiteralDefaultsToF64 pins the f64 default for
// untyped float literals. 16777217 (= 2^24 + 1) is exactly
// representable in f64 but rounds to 2^24 in f32, so the
// subtraction yields 1.0 under f64 and 0.0 under f32. The result
// (cast to i32) is the discriminant: 1 means the literal stayed
// f64, 0 means it silently truncated to f32.
func TestBugHunt_FloatLiteralDefaultsToF64(t *testing.T) {
	src := `function main(): i32 {
    var x = 16777217.0;
    return (x - 16777216.0) as i32;
}`
	if got := runInterpByte(t, src); got != 1 {
		t.Fatalf("interp: untyped float literal lost precision: got %d, want 1 (f64)", got)
	}
	assertBackendsAgreeWithInterp(t, "float_default_f64", src)
}

// TestBugHunt_FloatBinaryToIntCast guards the float→int cast over a
// float *expression* (not just a bare literal). The checker used to
// settle the cast's inner toward the integer target, routing a float
// binary through the integer settle path and stamping it with an int
// width; the cast then lowered as an int→int identity, leaking the
// raw float bit-pattern into the result (`(7.9 - 0.0) as i32` → 154,
// the low byte of the f64, instead of 7).
func TestBugHunt_FloatBinaryToIntCast(t *testing.T) {
	cases := []struct {
		name string
		src  string
	}{
		{"sub", `function main(): i32 { return (7.9 - 0.0) as i32; }`},
		{"add", `function main(): i32 { return (3.25 + 4.5) as i32; }`},
		{"mul", `function main(): i32 { return (2.5 * 3.0) as i32; }`},
		{"div", `function main(): i32 { return (40.0 / 5.0) as i32; }`},
	}
	for _, c := range cases {
		assertBackendsAgreeWithInterp(t, c.name, c.src)
	}
}

// TestBugHunt_FloatNaNComparisonParity guards IEEE-754 unordered
// comparison behaviour. On x86-64, `ucomisd` sets ZF=CF=PF=1 for an
// unordered (NaN) operand; the backend read ZF/CF via plain
// sete/setb/setbe and ignored the parity flag, so NaN==NaN came out
// true, NaN!=NaN false, and NaN<x / NaN<=x true — diverging from
// interp / arm64 / wasm, which all treat any ordered comparison
// against NaN as false (and != as true). It surfaced as
// `(0.0/0.0).to_string()` printing "-0" on x86-64 (the NaN check in
// std/float is `x != x`, which the buggy comparison defeated) while
// every other backend printed "NaN". Each case returns 7 when the
// NaN comparison is IEEE-correct and 1 otherwise.
func TestBugHunt_FloatNaNComparisonParity(t *testing.T) {
	cases := []struct {
		name string
		src  string
	}{
		// NaN != NaN is the one comparison that must be true.
		{"ne_true", `function main(): i32 { var z: f64 = 0.0; var n: f64 = z / z; if (n != n) { return 7; } return 1; }`},
		// NaN == NaN must be false.
		{"eq_false", `function main(): i32 { var z: f64 = 0.0; var n: f64 = z / z; if (n == n) { return 1; } return 7; }`},
		// NaN < x and NaN <= NaN must be false (the bug made them true).
		{"lt_false", `function main(): i32 { var z: f64 = 0.0; var n: f64 = z / z; if (n < 1.0) { return 1; } return 7; }`},
		{"le_false", `function main(): i32 { var z: f64 = 0.0; var n: f64 = z / z; if (n <= n) { return 1; } return 7; }`},
		// NaN > x and NaN >= x are already false on every backend.
		{"gt_false", `function main(): i32 { var z: f64 = 0.0; var n: f64 = z / z; if (n > 1.0) { return 1; } return 7; }`},
		{"ge_false", `function main(): i32 { var z: f64 = 0.0; var n: f64 = z / z; if (n >= 1.0) { return 1; } return 7; }`},
		// f32 NaN behaves the same (ucomiss path).
		{"f32_ne_true", `function main(): i32 { var z: f32 = 0.0; var n: f32 = z / z; if (n != n) { return 7; } return 1; }`},
		{"f32_eq_false", `function main(): i32 { var z: f32 = 0.0; var n: f32 = z / z; if (n == n) { return 1; } return 7; }`},
		// Ordered comparisons must still work (no regression).
		{"ordered_lt", `function main(): i32 { var a: f64 = 1.5; var b: f64 = 2.5; if (a < b) { return 7; } return 1; }`},
		{"ordered_eq", `function main(): i32 { var a: f64 = 2.5; if (a == a) { return 7; } return 1; }`},
	}
	for _, c := range cases {
		assertBackendsAgreeWithInterp(t, c.name, c.src)
	}
}

// TestBugHunt_FloatToIntSaturation pins the saturating float→int
// contract across every backend: NaN → 0, +overflow → INT_MAX,
// −overflow → INT_MIN (unsigned: < 0 / NaN → 0, overflow → the
// all-ones max). Before this the four backends each did something
// different — x86 / interp returned INT_MIN for every out-of-range
// input, arm64 saturated the i64 path but truncated f→i32 to -1/0
// (a 32-bit-dest encoding bug), and wasm trapped outright. Each
// case checks the cast result internally and returns 7 on success,
// so a backend that mis-saturates diverges from the interp's 7.
func TestBugHunt_FloatToIntSaturation(t *testing.T) {
	cases := []struct {
		name string
		src  string
	}{
		// signed i32
		{"i32_pos_ovf", `function main(): i32 { var x: f64 = 1e30; if ((x as i32) == 2147483647) { return 7; } return 1; }`},
		{"i32_neg_ovf", `function main(): i32 { var x: f64 = 0.0 - 1e30; if ((x as i32) == 0 - 2147483647 - 1) { return 7; } return 1; }`},
		{"i32_nan", `function main(): i32 { var x: f64 = 0.0 / 0.0; if ((x as i32) == 0) { return 7; } return 1; }`},
		{"i32_in_range", `function main(): i32 { var x: f64 = 0.0 - 42.9; if ((x as i32) == 0 - 42) { return 7; } return 1; }`},
		// signed i64
		{"i64_pos_ovf", `function main(): i32 { var x: f64 = 1e30; if ((x as i64) == 9223372036854775807) { return 7; } return 1; }`},
		{"i64_neg_ovf", `function main(): i32 { var x: f64 = 0.0 - 1e30; if ((x as i64) == 0 - 9223372036854775807 - 1) { return 7; } return 1; }`},
		{"i64_nan", `function main(): i32 { var x: f64 = 0.0 / 0.0; var r: i64 = x as i64; if (r == 0) { return 7; } return 1; }`},
		// f32 source (exercises the arm64 32-bit-dest encoding fix)
		{"f32_i32_ovf", `function main(): i32 { var x: f32 = 1e30; if ((x as i32) == 2147483647) { return 7; } return 1; }`},
	}
	for _, c := range cases {
		assertBackendsAgreeWithInterp(t, c.name, c.src)
	}
}

// TestBugHunt_FloatToStringLargeMagnitude guards `float.to_string`
// for magnitudes at/above 2^63: the `n as i64` cast in the formatter
// overflowed non-portably (x86 INT_MIN, arm64 INT_MAX, wasm trapped),
// so a value like 1e20 printed garbage or crashed. The float-domain
// integer formatter now renders the digits without crossing the i64
// boundary. Native backends print to stdout; we assert the interp
// produces the exact decimal and the natives match it.
func TestBugHunt_FloatToStringLargeMagnitude(t *testing.T) {
	src := `import "std/float";
function main(): i32 {
    print((1e20).to_string());
    print((0.0 - 3.0e20).to_string());
    return 0;
}`
	const want = "100000000000000000000\n-300000000000000000000"
	t.Run("x86_64", func(t *testing.T) {
		out, _ := compileAndRunX86_64(t, src)
		if got := normalizeOut(out); got != want {
			t.Errorf("x86_64 stdout = %q, want %q", got, want)
		}
	})
	t.Run("arm64-linux", func(t *testing.T) {
		out, _ := compileAndRunArm64(t, src)
		if got := normalizeOut(out); got != want {
			t.Errorf("arm64 stdout = %q, want %q", got, want)
		}
	})
}

func normalizeOut(s string) string {
	for len(s) > 0 && (s[len(s)-1] == '\n' || s[len(s)-1] == ' ') {
		s = s[:len(s)-1]
	}
	return s
}
