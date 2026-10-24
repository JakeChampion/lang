package e2e

import "testing"

const floatHypot3Prog = `
import "std/f64";
function approx(a: f64, b: f64): boolean { let d: f64 = a - b; if (d < 0.0) { d = 0.0 - d; } return d < 0.0001; }
function main(): i32 {
    if (!approx((2.0).hypot3(3.0, 6.0), 7.0)) { return 1; }        // 2-3-6 -> 7
    if (!approx((0.0).hypot3(0.0, 0.0), 0.0)) { return 2; }
    if (!approx((1.0).hypot3(2.0, 2.0), 3.0)) { return 3; }        // 1-2-2 -> 3
    if (!approx((3.0).hypot3(4.0, 0.0), 5.0)) { return 4; }        // degenerates to 2-D
    if (!approx((0.0 - 2.0).hypot3(0.0 - 3.0, 6.0), 7.0)) { return 5; }  // signs don't matter
    if (!approx((4.0).hypot3(4.0, 7.0), 9.0)) { return 6; }        // 4-4-7 -> 9
    // f32 mirror
    if (!approx((2.0 as f32).hypot3(3.0 as f32, 6.0 as f32) as f64, 7.0)) { return 7; }
    if (!approx((1.0 as f32).hypot3(2.0 as f32, 2.0 as f32) as f64, 3.0)) { return 8; }
    return 42;
}
`

func TestFloatHypot3Interp(t *testing.T) {
	if got := runInterpExit(t, floatHypot3Prog); got != 42 {
		t.Fatalf("interp got %d, want 42", got)
	}
}

func TestFloatHypot3X86_64(t *testing.T) {
	if _, got := compileAndRunX86_64(t, floatHypot3Prog); got != 42 {
		t.Fatalf("x86-64 got %d, want 42", got)
	}
}

func TestFloatHypot3Wasm(t *testing.T) {
	if got := compileAndRunWasmbinMain(t, floatHypot3Prog); got != 42 {
		t.Fatalf("wasm got %d, want 42", got)
	}
}

func TestFloatHypot3Arm64(t *testing.T) {
	if _, got := compileAndRunArm64(t, floatHypot3Prog); got != 42 {
		t.Fatalf("arm64 got %d, want 42", got)
	}
}
