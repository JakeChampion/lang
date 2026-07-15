package e2e

import "testing"

// Differential coverage for std/array.dot_f64 / norm_f64 — the dot product and
// the Euclidean (L2) norm. Both return a plain f64 (no Option), so they lower
// on all four backends including wasmbin. dot runs to the shorter length on a
// length mismatch; norm is sqrt(dot(self,self)). Returns 42 iff every check
// holds across interp / x86-64 / wasm / arm64; each leg skips itself when its
// toolchain is absent.
const arrayVectorProg = `
import "std/array" as array;
function approx(a: f64, b: f64): boolean { var d: f64 = a - b; if (d < 0.0) { d = 0.0 - d; } return d < 0.0001; }
function main(): i32 {
    var a: f64[] = [1.0, 2.0, 3.0];
    var b: f64[] = [4.0, 5.0, 6.0];
    if (!approx(array.dot_f64(a, b), 32.0)) { return 1; }        // 4+10+18
    if (!approx(array.norm_f64([3.0, 4.0]), 5.0)) { return 2; }  // 3-4-5
    if (!approx(array.norm_f64([1.0, 2.0, 2.0]), 3.0)) { return 3; }
    // mismatched lengths -> shorter
    var c: f64[] = [1.0, 1.0];
    if (!approx(array.dot_f64(a, c), 3.0)) { return 4; }         // 1*1 + 2*1
    if (!approx(array.dot_f64(c, a), 3.0)) { return 5; }         // symmetric in length handling
    // negatives
    if (!approx(array.dot_f64([1.0, 0.0 - 2.0], [3.0, 4.0]), 0.0 - 5.0)) { return 6; }  // 3 - 8
    // empty
    var empty: f64[] = [];
    if (!approx(array.dot_f64(empty, empty), 0.0)) { return 7; }
    if (!approx(array.norm_f64(empty), 0.0)) { return 8; }
    // identity: norm^2 == dot(self, self)
    if (!approx(array.norm_f64(a) * array.norm_f64(a), array.dot_f64(a, a))) { return 9; }
    return 42;
}
`

func TestArrayVectorInterp(t *testing.T) {
	if got := runInterpExit(t, arrayVectorProg); got != 42 {
		t.Fatalf("interp got %d, want 42", got)
	}
}

func TestArrayVectorX86_64(t *testing.T) {
	if _, got := compileAndRunX86_64(t, arrayVectorProg); got != 42 {
		t.Fatalf("x86-64 got %d, want 42", got)
	}
}

func TestArrayVectorWasm(t *testing.T) {
	if got := compileAndRunWasmbinMain(t, arrayVectorProg); got != 42 {
		t.Fatalf("wasm got %d, want 42", got)
	}
}

func TestArrayVectorArm64(t *testing.T) {
	if _, got := compileAndRunArm64(t, arrayVectorProg); got != 42 {
		t.Fatalf("arm64 got %d, want 42", got)
	}
}
