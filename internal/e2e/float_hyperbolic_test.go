package e2e

import "testing"

// Differential coverage for std/float.sinh / cosh / tanh — the hyperbolic trig
// functions, built on the natural exp. Like the other exp/log-derived helpers,
// the wasmbin leg skips (the legacy AST backend doesn't wire libm exp).
// Tolerance-banded; checks the reference values at x=1, the odd/even symmetry,
// the cosh²-sinh²==1 identity, and tanh's ±1 saturation. Both f64 and f32.
// Returns 42 iff every check holds across interp / x86-64 / wasm / arm64; each
// leg skips itself when its toolchain is absent.
const floatHyperbolicProg = `
import "std/float" as float;
function approx(a: f64, b: f64): boolean { var d: f64 = a - b; if (d < 0.0) { d = 0.0 - d; } return d < 0.001; }
function main(): i32 {
    if (!approx((0.0).sinh(), 0.0)) { return 1; }
    if (!approx((0.0).cosh(), 1.0)) { return 2; }
    if (!approx((0.0).tanh(), 0.0)) { return 3; }
    if (!approx((1.0).sinh(), 1.1752011936)) { return 4; }
    if (!approx((1.0).cosh(), 1.5430806348)) { return 5; }
    if (!approx((1.0).tanh(), 0.7615941560)) { return 6; }
    if (!approx((0.0 - 1.0).sinh(), 0.0 - 1.1752011936)) { return 7; }   // sinh is odd
    if (!approx((0.0 - 1.0).cosh(), 1.5430806348)) { return 8; }         // cosh is even
    // identity cosh^2 - sinh^2 == 1
    if (!approx((2.0).cosh() * (2.0).cosh() - (2.0).sinh() * (2.0).sinh(), 1.0)) { return 9; }
    // tanh saturates to +/-1 past the guard
    if (!approx((50.0).tanh(), 1.0)) { return 10; }
    if (!approx((0.0 - 50.0).tanh(), 0.0 - 1.0)) { return 11; }
    // f32 mirrors
    if (!approx((1.0 as f32).sinh() as f64, 1.1752011936)) { return 12; }
    if (!approx((1.0 as f32).tanh() as f64, 0.7615941560)) { return 13; }
    return 42;
}
`

func TestFloatHyperbolicInterp(t *testing.T) {
	if got := runInterpExit(t, floatHyperbolicProg); got != 42 {
		t.Fatalf("interp got %d, want 42", got)
	}
}

func TestFloatHyperbolicX86_64(t *testing.T) {
	if _, got := compileAndRunX86_64(t, floatHyperbolicProg); got != 42 {
		t.Fatalf("x86-64 got %d, want 42", got)
	}
}

func TestFloatHyperbolicWasm(t *testing.T) {
	if got := compileAndRunWasmbinMain(t, floatHyperbolicProg); got != 42 {
		t.Fatalf("wasm got %d, want 42", got)
	}
}

func TestFloatHyperbolicArm64(t *testing.T) {
	if _, got := compileAndRunArm64(t, floatHyperbolicProg); got != 42 {
		t.Fatalf("arm64 got %d, want 42", got)
	}
}
