package e2e

import "testing"

const floatExp2Exp10Prog = `
import "std/f64";
function approx(a: f64, b: f64): boolean { let d: f64 = a - b; if (d < 0.0) { d = 0.0 - d; } return d < 0.0001; }
function main(): i32 {
    if (!approx((3.0).exp2(), 8.0)) { return 1; }
    if (!approx((10.0).exp2(), 1024.0)) { return 2; }
    if (!approx((0.0).exp2(), 1.0)) { return 3; }
    if (!approx((0.0 - 1.0).exp2(), 0.5)) { return 4; }        // negative exponent
    if (!approx((3.0).exp10(), 1000.0)) { return 5; }
    if (!approx((2.0).exp10(), 100.0)) { return 6; }
    if (!approx((0.0).exp10(), 1.0)) { return 7; }
    // round-trips with the logs
    if (!approx((8.0).log2().exp2(), 8.0)) { return 8; }
    if (!approx((5.0).exp2().log2(), 5.0)) { return 9; }
    if (!approx((1000.0).log10().exp10(), 1000.0)) { return 10; }
    // f32 mirrors
    if (!approx((16.0 as f32).exp2() as f64, 65536.0)) { return 11; }
    if (!approx((2.0 as f32).exp10() as f64, 100.0)) { return 12; }
    return 42;
}
`

func TestFloatExp2Exp10Interp(t *testing.T) {
	if got := runInterpExit(t, floatExp2Exp10Prog); got != 42 {
		t.Fatalf("interp got %d, want 42", got)
	}
}

func TestFloatExp2Exp10X86_64(t *testing.T) {
	if _, got := compileAndRunX86_64(t, floatExp2Exp10Prog); got != 42 {
		t.Fatalf("x86-64 got %d, want 42", got)
	}
}

func TestFloatExp2Exp10Wasm(t *testing.T) {
	if got := compileAndRunWasmbinMain(t, floatExp2Exp10Prog); got != 42 {
		t.Fatalf("wasm got %d, want 42", got)
	}
}

func TestFloatExp2Exp10Arm64(t *testing.T) {
	if _, got := compileAndRunArm64(t, floatExp2Exp10Prog); got != 42 {
		t.Fatalf("arm64 got %d, want 42", got)
	}
}
