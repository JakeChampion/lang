package e2e

import "testing"

// Differential coverage for std/array.product_f64 / cumsum_f64 — the product of
// all elements (empty product = 1) and the running prefix sum (same length as
// the input). product returns a plain f64; cumsum returns an f64[] (scalar
// payload). Both lower on all four backends including wasmbin. Returns 42 iff
// every check holds across interp / x86-64 / wasm / arm64; each leg skips itself
// when its toolchain is absent.
const arrayProductCumsumProg = `
import "std/array" as array;
function approx(a: f64, b: f64): boolean { var d: f64 = a - b; if (d < 0.0) { d = 0.0 - d; } return d < 0.0001; }
function main(): i32 {
    var xs: f64[] = [1.0, 2.0, 3.0, 4.0];
    if (!approx(array.product_f64(xs), 24.0)) { return 1; }
    if (!approx(array.product_f64([]), 1.0)) { return 2; }              // empty product = 1
    if (!approx(array.product_f64([2.0, 0.0 - 3.0]), 0.0 - 6.0)) { return 3; }  // negatives
    var cs: f64[] = array.cumsum_f64(xs);
    if (!approx(cs[0], 1.0)) { return 4; }
    if (!approx(cs[1], 3.0)) { return 5; }
    if (!approx(cs[2], 6.0)) { return 6; }
    if (!approx(cs[3], 10.0)) { return 7; }
    if (cs.len() != 4) { return 8; }
    // last prefix sum equals the total
    if (!approx(cs[3], array.sum_f64(xs))) { return 9; }
    // empty in -> empty out
    if (array.cumsum_f64([]).len() != 0) { return 10; }
    // running total with a negative step
    var d: f64[] = array.cumsum_f64([5.0, 0.0 - 2.0, 1.0]);
    if (!approx(d[1], 3.0)) { return 11; }
    if (!approx(d[2], 4.0)) { return 12; }
    return 42;
}
`

func TestArrayProductCumsumInterp(t *testing.T) {
	if got := runInterpExit(t, arrayProductCumsumProg); got != 42 {
		t.Fatalf("interp got %d, want 42", got)
	}
}

func TestArrayProductCumsumX86_64(t *testing.T) {
	if _, got := compileAndRunX86_64(t, arrayProductCumsumProg); got != 42 {
		t.Fatalf("x86-64 got %d, want 42", got)
	}
}

func TestArrayProductCumsumWasm(t *testing.T) {
	if got := compileAndRunWasmbinMain(t, arrayProductCumsumProg); got != 42 {
		t.Fatalf("wasm got %d, want 42", got)
	}
}

func TestArrayProductCumsumArm64(t *testing.T) {
	if _, got := compileAndRunArm64(t, arrayProductCumsumProg); got != 42 {
		t.Fatalf("arm64 got %d, want 42", got)
	}
}
