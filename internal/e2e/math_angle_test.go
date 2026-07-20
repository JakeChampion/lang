package e2e

import "testing"

// Differential coverage for std/math's floating-point angle + interpolation
// helpers — pi / tau / to_radians / to_degrees / lerp. All are pure f64
// arithmetic (no libm), so every backend must agree bit-for-bit. Checks the
// exact-representable cases directly (lerp endpoints + midpoints, tau == 2·pi,
// zero-angle conversions) and the rounding cases (degree↔radian round-trip,
// to_radians(180°) ≈ π) within a tight epsilon. Returns 42 iff every check
// holds across interp / x86-64 / wasm / arm64; each leg skips itself when its
// toolchain is absent.
const mathAngleProg = `
import "std/math";
function main(): i32 {
    // lerp: endpoints reproduce exactly, midpoints are exact for these values.
    if (math.lerp(2.0, 10.0, 0.0) != 2.0) { return 1; }
    if (math.lerp(2.0, 10.0, 1.0) != 10.0) { return 2; }
    if (math.lerp(2.0, 10.0, 0.5) != 6.0) { return 3; }
    if (math.lerp(0.0, 100.0, 0.25) != 25.0) { return 4; }
    if (math.lerp(0.0 - 4.0, 4.0, 0.5) != 0.0) { return 5; }   // spans zero
    // tau is exactly two pi (scaling an f64 by 2 only bumps the exponent).
    if (math.tau() != 2.0 * math.pi()) { return 6; }
    // pi() sits in the tight interval around the true value.
    if (math.pi() <= 3.141592653 || math.pi() >= 3.141592654) { return 7; }
    // zero-angle conversions are exact both ways.
    if (math.to_radians(0.0) != 0.0) { return 8; }
    if (math.to_degrees(0.0) != 0.0) { return 9; }
    // degree -> radian -> degree round-trips to within a tight epsilon.
    var rt: f64 = math.to_degrees(math.to_radians(90.0));
    var d: f64 = rt - 90.0;
    if (d < 0.0) { d = 0.0 - d; }
    if (d > 0.0000001) { return 10; }
    // 180 degrees is pi radians (up to rounding).
    var rad: f64 = math.to_radians(180.0);
    var dp: f64 = rad - math.pi();
    if (dp < 0.0) { dp = 0.0 - dp; }
    if (dp > 0.0000001) { return 11; }
    // to_degrees is the inverse of to_radians on a non-trivial angle.
    var back: f64 = math.to_radians(math.to_degrees(1.25));
    var db: f64 = back - 1.25;
    if (db < 0.0) { db = 0.0 - db; }
    if (db > 0.0000001) { return 12; }
    return 42;
}
`

func TestMathAngleInterp(t *testing.T) {
	if got := runInterpExit(t, mathAngleProg); got != 42 {
		t.Fatalf("interp got %d, want 42", got)
	}
}

func TestMathAngleX86_64(t *testing.T) {
	if _, got := compileAndRunX86_64(t, mathAngleProg); got != 42 {
		t.Fatalf("x86-64 got %d, want 42", got)
	}
}

func TestMathAngleWasm(t *testing.T) {
	if got := compileAndRunWasmbinMain(t, mathAngleProg); got != 42 {
		t.Fatalf("wasm got %d, want 42", got)
	}
}

func TestMathAngleArm64(t *testing.T) {
	if _, got := compileAndRunArm64(t, mathAngleProg); got != 42 {
		t.Fatalf("arm64 got %d, want 42", got)
	}
}
