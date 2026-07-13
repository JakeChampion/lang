package e2e

import "testing"

// Differential coverage for std/float's hypot (Euclidean length via the
// overflow-safe scaled form), fract (signed fractional part x - trunc(x)), and
// tan (sin/cos). Both f64 and f32 receivers. Uses Pythagorean triples and
// exactly-representable fractions so the checks are tolerance-banded but stable
// across backends. Returns 42 iff every check holds across interp / x86-64 /
// wasm / arm64; each leg skips itself when its toolchain is absent.
const floatHypotFractTanProg = `
import "std/float" as float;
function approx(a: f64, b: f64): boolean { var d: f64 = a - b; if (d < 0.0) { d = 0.0 - d; } return d < 0.001; }
function main(): i32 {
    // ---- hypot (f64) ----
    if (!approx((3.0).hypot(4.0), 5.0)) { return 1; }
    if (!approx((5.0).hypot(12.0), 13.0)) { return 2; }
    if (!approx((0.0).hypot(0.0), 0.0)) { return 3; }
    if (!approx((0.0 - 3.0).hypot(4.0), 5.0)) { return 4; }   // sign-insensitive
    if (!approx((8.0).hypot(0.0), 8.0)) { return 5; }         // one axis zero
    // ---- fract (f64) ----
    if (!approx((3.75).fract(), 0.75)) { return 6; }
    if (!approx((0.0 - 3.75).fract(), 0.0 - 0.75)) { return 7; }  // keeps sign
    if (!approx((5.0).fract(), 0.0)) { return 8; }
    // ---- tan (f64) ----
    if (!approx((0.0).tan(), 0.0)) { return 9; }
    if (!approx((0.7853981633974483).tan(), 1.0)) { return 10; }  // tan(pi/4)=1
    // ---- f32 mirrors ----
    if (!approx((3.0 as f32).hypot(4.0 as f32) as f64, 5.0)) { return 11; }
    if (!approx((3.5 as f32).fract() as f64, 0.5)) { return 12; }
    if (!approx((0.0 as f32).tan() as f64, 0.0)) { return 13; }
    return 42;
}
`

func TestFloatHypotFractTanInterp(t *testing.T) {
	if got := runInterpExit(t, floatHypotFractTanProg); got != 42 {
		t.Fatalf("interp got %d, want 42", got)
	}
}

func TestFloatHypotFractTanX86_64(t *testing.T) {
	if _, got := compileAndRunX86_64(t, floatHypotFractTanProg); got != 42 {
		t.Fatalf("x86-64 got %d, want 42", got)
	}
}

func TestFloatHypotFractTanWasm(t *testing.T) {
	if got := compileAndRunWasmbinMain(t, floatHypotFractTanProg); got != 42 {
		t.Fatalf("wasm got %d, want 42", got)
	}
}

func TestFloatHypotFractTanArm64(t *testing.T) {
	if _, got := compileAndRunArm64(t, floatHypotFractTanProg); got != 42 {
		t.Fatalf("arm64 got %d, want 42", got)
	}
}
