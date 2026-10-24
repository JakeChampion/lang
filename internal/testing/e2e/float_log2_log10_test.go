package e2e

import "testing"

const floatLog2Log10Prog = `
import "std/f64";
function approx(a: f64, b: f64): boolean { let d: f64 = a - b; if (d < 0.0) { d = 0.0 - d; } return d < 0.0001; }
function main(): i32 {
    if (!approx((8.0).log2(), 3.0)) { return 1; }
    if (!approx((1024.0).log2(), 10.0)) { return 2; }
    if (!approx((0.5).log2(), 0.0 - 1.0)) { return 3; }   // < 1 -> negative
    if (!approx((1.0).log2(), 0.0)) { return 4; }
    if (!approx((1000.0).log10(), 3.0)) { return 5; }
    if (!approx((100.0).log10(), 2.0)) { return 6; }
    if (!approx((1.0).log10(), 0.0)) { return 7; }
    if (!approx((0.01).log10(), 0.0 - 2.0)) { return 8; }
    // log2(x) and log10(x) relate by log2(10) ~ 3.32193
    if (!approx((256.0).log2() / (256.0).log10(), 3.321928)) { return 9; }
    // f32 mirrors
    if (!approx((16.0 as f32).log2() as f64, 4.0)) { return 10; }
    if (!approx((10000.0 as f32).log10() as f64, 4.0)) { return 11; }
    return 42;
}
`

func TestFloatLog2Log10Interp(t *testing.T) {
	if got := runInterpExit(t, floatLog2Log10Prog); got != 42 {
		t.Fatalf("interp got %d, want 42", got)
	}
}

func TestFloatLog2Log10X86_64(t *testing.T) {
	if _, got := compileAndRunX86_64(t, floatLog2Log10Prog); got != 42 {
		t.Fatalf("x86-64 got %d, want 42", got)
	}
}

func TestFloatLog2Log10Wasm(t *testing.T) {
	if got := compileAndRunWasmbinMain(t, floatLog2Log10Prog); got != 42 {
		t.Fatalf("wasm got %d, want 42", got)
	}
}

func TestFloatLog2Log10Arm64(t *testing.T) {
	if _, got := compileAndRunArm64(t, floatLog2Log10Prog); got != 42 {
		t.Fatalf("arm64 got %d, want 42", got)
	}
}
