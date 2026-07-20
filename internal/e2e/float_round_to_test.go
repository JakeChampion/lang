package e2e

import "testing"

// Differential coverage for std/float.round_to(digits) — round to N decimal
// places, half away from zero (like round). Positive digits round to the
// fraction, negative digits round to tens / hundreds, and the whole thing scales
// / rounds / unscales. Both f64 and f32. Uses tolerance bands (the result is the
// nearest f64 to the rounded decimal). Returns 42 iff every check holds across
// interp / x86-64 / wasm / arm64; each leg skips itself when its toolchain is
// absent.
const floatRoundToProg = `
import "std/float" as float;
function approx(a: f64, b: f64): boolean { var d: f64 = a - b; if (d < 0.0) { d = 0.0 - d; } return d < 0.0001; }
function main(): i32 {
    if (!approx((3.14159).round_to(2), 3.14)) { return 1; }
    if (!approx((3.14159).round_to(0), 3.0)) { return 2; }
    if (!approx((2.5).round_to(0), 3.0)) { return 3; }               // half away from zero
    if (!approx((3.14159).round_to(4), 3.1416)) { return 4; }
    if (!approx((0.0 - 2.567).round_to(1), 0.0 - 2.6)) { return 5; } // negative value
    if (!approx((1234.0).round_to(0 - 2), 1200.0)) { return 6; }     // negative digits (hundreds)
    if (!approx((1250.0).round_to(0 - 2), 1300.0)) { return 7; }     // half away, hundreds
    if (!approx((5.0).round_to(3), 5.0)) { return 8; }               // whole number unchanged
    if (!approx((0.0).round_to(2), 0.0)) { return 9; }
    // f32 mirror
    if (!approx((3.14159 as f32).round_to(2) as f64, 3.14)) { return 10; }
    if (!approx((2.71828 as f32).round_to(3) as f64, 2.718)) { return 11; }
    return 42;
}
`

func TestFloatRoundToInterp(t *testing.T) {
	if got := runInterpExit(t, floatRoundToProg); got != 42 {
		t.Fatalf("interp got %d, want 42", got)
	}
}

func TestFloatRoundToX86_64(t *testing.T) {
	if _, got := compileAndRunX86_64(t, floatRoundToProg); got != 42 {
		t.Fatalf("x86-64 got %d, want 42", got)
	}
}

func TestFloatRoundToWasm(t *testing.T) {
	if got := compileAndRunWasmbinMain(t, floatRoundToProg); got != 42 {
		t.Fatalf("wasm got %d, want 42", got)
	}
}

func TestFloatRoundToArm64(t *testing.T) {
	if _, got := compileAndRunArm64(t, floatRoundToProg); got != 42 {
		t.Fatalf("arm64 got %d, want 42", got)
	}
}
