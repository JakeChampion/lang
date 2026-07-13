package e2e

import "testing"

// Differential coverage for std/float's convenience methods added atop the
// existing abs/floor/…/clamp surface: signum (sign as a float, zero at zero,
// NaN-preserving), lerp (precise a+(b-a)*t linear interpolation, incl.
// extrapolation), and to_radians/to_degrees (degree↔radian conversion via a
// high-precision π). Both f64 and f32 receivers. Returns 42 iff every check
// holds; each leg skips itself when its toolchain is absent. All comparisons
// use exact-representable values (halves / integers) so the four backends must
// agree bit-for-bit; the conversion checks use tolerance bands.
const floatSignumLerpProg = `
import "std/float" as float;
function main(): i32 {
    // ---- signum f64 ----
    if ((5.0).signum() != 1.0) { return 1; }
    if ((0.0 - 8.0).signum() != 0.0 - 1.0) { return 2; }
    if ((0.0).signum() != 0.0) { return 3; }
    // ---- lerp f64 ----
    if ((10.0).lerp(20.0, 0.5) != 15.0) { return 4; }
    if ((0.0).lerp(100.0, 0.25) != 25.0) { return 5; }
    if ((4.0).lerp(8.0, 0.0) != 4.0) { return 6; }
    if ((4.0).lerp(8.0, 1.0) != 8.0) { return 7; }
    // t outside [0,1] extrapolates
    if ((0.0).lerp(10.0, 2.0) != 20.0) { return 8; }
    // ---- to_radians / to_degrees f64 (tolerance bands) ----
    var r: f64 = (180.0).to_radians();
    if (r < 3.14 || r > 3.15) { return 9; }
    var d: f64 = (3.141592653589793).to_degrees();
    if (d < 179.9 || d > 180.1) { return 10; }
    // round trip recovers the input closely
    var rt: f64 = (90.0).to_radians().to_degrees();
    if (rt < 89.99 || rt > 90.01) { return 11; }
    // ---- f32 mirrors ----
    if ((5.0 as f32).signum() != (1.0 as f32)) { return 12; }
    if ((0.0 as f32).signum() != (0.0 as f32)) { return 13; }
    if ((10.0 as f32).lerp(20.0 as f32, 0.5 as f32) != (15.0 as f32)) { return 14; }
    var r32: f32 = (180.0 as f32).to_radians();
    if (r32 < (3.14 as f32) || r32 > (3.15 as f32)) { return 15; }
    return 42;
}
`

func TestFloatSignumLerpInterp(t *testing.T) {
	if got := runInterpExit(t, floatSignumLerpProg); got != 42 {
		t.Fatalf("interp got %d, want 42", got)
	}
}

func TestFloatSignumLerpX86_64(t *testing.T) {
	if _, got := compileAndRunX86_64(t, floatSignumLerpProg); got != 42 {
		t.Fatalf("x86-64 got %d, want 42", got)
	}
}

func TestFloatSignumLerpWasm(t *testing.T) {
	if got := compileAndRunWasmbinMain(t, floatSignumLerpProg); got != 42 {
		t.Fatalf("wasm got %d, want 42", got)
	}
}

func TestFloatSignumLerpArm64(t *testing.T) {
	if _, got := compileAndRunArm64(t, floatSignumLerpProg); got != 42 {
		t.Fatalf("arm64 got %d, want 42", got)
	}
}
