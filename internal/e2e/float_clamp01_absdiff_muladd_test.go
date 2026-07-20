package e2e

import "testing"

// Differential coverage for std/float.clamp01 / abs_diff / mul_add — three
// purely-arithmetic helpers (no libm primitive), so they lower on all four
// backends including wasmbin. clamp01 restricts to [0,1], abs_diff is |a-b|,
// mul_add is a*b+c (not a fused FMA — the multiply rounds first). Both f64 and
// f32. Returns 42 iff every check holds across interp / x86-64 / wasm / arm64;
// each leg skips itself when its toolchain is absent.
const floatClamp01AbsDiffMulAddProg = `
import "std/float" as float;
function approx(a: f64, b: f64): boolean { var d: f64 = a - b; if (d < 0.0) { d = 0.0 - d; } return d < 0.0001; }
function main(): i32 {
    if (!approx((0.5).clamp01(), 0.5)) { return 1; }
    if (!approx((0.0 - 0.3).clamp01(), 0.0)) { return 2; }          // below 0 -> 0
    if (!approx((1.7).clamp01(), 1.0)) { return 3; }                // above 1 -> 1
    if (!approx((0.0).clamp01(), 0.0)) { return 4; }
    if (!approx((3.0).abs_diff(7.0), 4.0)) { return 5; }
    if (!approx((7.0).abs_diff(3.0), 4.0)) { return 6; }            // symmetric
    if (!approx((0.0 - 2.0).abs_diff(2.0), 4.0)) { return 7; }
    if (!approx((5.0).abs_diff(5.0), 0.0)) { return 8; }
    if (!approx((3.0).mul_add(4.0, 5.0), 17.0)) { return 9; }       // 3*4+5
    if (!approx((2.0).mul_add(0.0 - 1.5, 1.0), 0.0 - 2.0)) { return 10; }
    // f32 mirrors
    if (!approx((1.7 as f32).clamp01() as f64, 1.0)) { return 11; }
    if (!approx((3.0 as f32).abs_diff(7.0 as f32) as f64, 4.0)) { return 12; }
    if (!approx((3.0 as f32).mul_add(4.0 as f32, 5.0 as f32) as f64, 17.0)) { return 13; }
    return 42;
}
`

func TestFloatClamp01AbsDiffMulAddInterp(t *testing.T) {
	if got := runInterpExit(t, floatClamp01AbsDiffMulAddProg); got != 42 {
		t.Fatalf("interp got %d, want 42", got)
	}
}

func TestFloatClamp01AbsDiffMulAddX86_64(t *testing.T) {
	if _, got := compileAndRunX86_64(t, floatClamp01AbsDiffMulAddProg); got != 42 {
		t.Fatalf("x86-64 got %d, want 42", got)
	}
}

func TestFloatClamp01AbsDiffMulAddWasm(t *testing.T) {
	if got := compileAndRunWasmbinMain(t, floatClamp01AbsDiffMulAddProg); got != 42 {
		t.Fatalf("wasm got %d, want 42", got)
	}
}

func TestFloatClamp01AbsDiffMulAddArm64(t *testing.T) {
	if _, got := compileAndRunArm64(t, floatClamp01AbsDiffMulAddProg); got != 42 {
		t.Fatalf("arm64 got %d, want 42", got)
	}
}
