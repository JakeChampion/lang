package e2e

import "testing"

// Differential coverage for std/array.scale_f64 / add_f64 — the element-wise
// vector combinators: scalar multiply and element-wise sum (to the shorter
// length on a mismatch). Both return an f64[] (scalar payload), so they lower
// on all four backends including wasmbin. Returns 42 iff every check holds
// across interp / x86-64 / wasm / arm64; each leg skips itself when its
// toolchain is absent.
const arrayScaleAddProg = `
import "std/array" as array;
function approx(a: f64, b: f64): boolean { var d: f64 = a - b; if (d < 0.0) { d = 0.0 - d; } return d < 0.0001; }
function main(): i32 {
    var xs: f64[] = [1.0, 2.0, 3.0];
    var s: f64[] = array.scale_f64(xs, 2.5);
    if (!approx(s[0], 2.5)) { return 1; }
    if (!approx(s[2], 7.5)) { return 2; }
    if (!approx(xs[0], 1.0)) { return 3; }              // input untouched
    if (!approx(array.scale_f64(xs, 0.0)[1], 0.0)) { return 4; }        // scale by 0
    if (!approx(array.scale_f64(xs, 0.0 - 1.0)[2], 0.0 - 3.0)) { return 5; }  // negate
    var b: f64[] = [10.0, 20.0, 30.0];
    var sum: f64[] = array.add_f64(xs, b);
    if (!approx(sum[0], 11.0)) { return 6; }
    if (!approx(sum[2], 33.0)) { return 7; }
    // mismatched lengths -> shorter
    var c: f64[] = [100.0];
    if (array.add_f64(xs, c).len() != 1) { return 8; }
    if (!approx(array.add_f64(xs, c)[0], 101.0)) { return 9; }
    // empty
    if (array.scale_f64([], 5.0).len() != 0) { return 10; }
    if (array.add_f64([], []).len() != 0) { return 11; }
    // SAXPY: 2*xs + b
    var saxpy: f64[] = array.add_f64(array.scale_f64(xs, 2.0), b);
    if (!approx(saxpy[0], 12.0)) { return 12; }
    if (!approx(saxpy[2], 36.0)) { return 13; }
    return 42;
}
`

func TestArrayScaleAddInterp(t *testing.T) {
	if got := runInterpExit(t, arrayScaleAddProg); got != 42 {
		t.Fatalf("interp got %d, want 42", got)
	}
}

func TestArrayScaleAddX86_64(t *testing.T) {
	if _, got := compileAndRunX86_64(t, arrayScaleAddProg); got != 42 {
		t.Fatalf("x86-64 got %d, want 42", got)
	}
}

func TestArrayScaleAddWasm(t *testing.T) {
	if got := compileAndRunWasmbinMain(t, arrayScaleAddProg); got != 42 {
		t.Fatalf("wasm got %d, want 42", got)
	}
}

func TestArrayScaleAddArm64(t *testing.T) {
	if _, got := compileAndRunArm64(t, arrayScaleAddProg); got != 42 {
		t.Fatalf("arm64 got %d, want 42", got)
	}
}
