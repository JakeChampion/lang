package e2e

import "testing"

// Differential coverage for std/float.cbrt — the real cube root, defined for
// negative inputs (unlike pow with a fractional exponent). Built on __pow_f64
// around a peeled-off sign, so like the other transcendentals its wasmbin leg
// skips (the legacy AST backend doesn't wire libm pow). Tolerance-banded. Both
// f64 and f32. Returns 42 iff every check holds across interp / x86-64 / wasm /
// arm64; each leg skips itself when its toolchain is absent.
const floatCbrtProg = `
import "std/float" as float;
function approx(a: f64, b: f64): boolean { var d: f64 = a - b; if (d < 0.0) { d = 0.0 - d; } return d < 0.001; }
function main(): i32 {
    if (!approx((27.0).cbrt(), 3.0)) { return 1; }
    if (!approx((8.0).cbrt(), 2.0)) { return 2; }
    if (!approx((0.0 - 8.0).cbrt(), 0.0 - 2.0)) { return 3; }     // negative input
    if (!approx((0.0).cbrt(), 0.0)) { return 4; }
    if (!approx((1000.0).cbrt(), 10.0)) { return 5; }
    if (!approx((0.125).cbrt(), 0.5)) { return 6; }               // fractional
    // cbrt(x)^3 round-trips
    if (!approx((5.0).cbrt().pow(3.0), 5.0)) { return 7; }
    // f32 mirror
    if (!approx((27.0 as f32).cbrt() as f64, 3.0)) { return 8; }
    if (!approx((0.0 - 64.0 as f32).cbrt() as f64, 0.0 - 4.0)) { return 9; }
    return 42;
}
`

func TestFloatCbrtInterp(t *testing.T) {
	if got := runInterpExit(t, floatCbrtProg); got != 42 {
		t.Fatalf("interp got %d, want 42", got)
	}
}

func TestFloatCbrtX86_64(t *testing.T) {
	if _, got := compileAndRunX86_64(t, floatCbrtProg); got != 42 {
		t.Fatalf("x86-64 got %d, want 42", got)
	}
}

func TestFloatCbrtWasm(t *testing.T) {
	if got := compileAndRunWasmbinMain(t, floatCbrtProg); got != 42 {
		t.Fatalf("wasm got %d, want 42", got)
	}
}

func TestFloatCbrtArm64(t *testing.T) {
	if _, got := compileAndRunArm64(t, floatCbrtProg); got != 42 {
		t.Fatalf("arm64 got %d, want 42", got)
	}
}
