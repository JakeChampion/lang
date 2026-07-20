package e2e

import "testing"

// Differential coverage for std/array.distance_f64 / normalize_f64 — the
// Euclidean distance sqrt(sum((a-b)^2)) and the unit vector (each element / the
// norm). distance returns a plain f64; normalize returns an f64[] (scalar
// payload). Both lower on all four backends including wasmbin. Returns 42 iff
// every check holds across interp / x86-64 / wasm / arm64; each leg skips
// itself when its toolchain is absent.
const arrayDistanceNormalizeProg = `
import "std/array" as array;
function approx(a: f64, b: f64): boolean { var d: f64 = a - b; if (d < 0.0) { d = 0.0 - d; } return d < 0.0001; }
function main(): i32 {
    if (!approx(array.distance_f64([0.0, 0.0], [3.0, 4.0]), 5.0)) { return 1; }
    if (!approx(array.distance_f64([1.0, 2.0], [1.0, 2.0]), 0.0)) { return 2; }  // equal -> 0
    if (!approx(array.distance_f64([1.0, 2.0, 3.0], [4.0, 6.0, 3.0]), 5.0)) { return 3; }  // sqrt(9+16+0)
    // mismatched lengths -> shorter
    if (!approx(array.distance_f64([0.0, 0.0, 9.0], [3.0, 4.0]), 5.0)) { return 4; }
    // normalize -> unit vector
    var u: f64[] = array.normalize_f64([3.0, 4.0]);
    if (!approx(u[0], 0.6)) { return 5; }
    if (!approx(u[1], 0.8)) { return 6; }
    if (!approx(array.norm_f64(u), 1.0)) { return 7; }
    // zero vector returned unchanged (no NaN from div by zero)
    var z: f64[] = array.normalize_f64([0.0, 0.0]);
    if (!approx(z[0], 0.0)) { return 8; }
    if (!approx(z[1], 0.0)) { return 9; }
    // empty returned unchanged
    if (array.normalize_f64([]).len() != 0) { return 10; }
    return 42;
}
`

func TestArrayDistanceNormalizeInterp(t *testing.T) {
	if got := runInterpExit(t, arrayDistanceNormalizeProg); got != 42 {
		t.Fatalf("interp got %d, want 42", got)
	}
}

func TestArrayDistanceNormalizeX86_64(t *testing.T) {
	if _, got := compileAndRunX86_64(t, arrayDistanceNormalizeProg); got != 42 {
		t.Fatalf("x86-64 got %d, want 42", got)
	}
}

func TestArrayDistanceNormalizeWasm(t *testing.T) {
	if got := compileAndRunWasmbinMain(t, arrayDistanceNormalizeProg); got != 42 {
		t.Fatalf("wasm got %d, want 42", got)
	}
}

func TestArrayDistanceNormalizeArm64(t *testing.T) {
	if _, got := compileAndRunArm64(t, arrayDistanceNormalizeProg); got != 42 {
		t.Fatalf("arm64 got %d, want 42", got)
	}
}
