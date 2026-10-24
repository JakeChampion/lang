package e2e

import "testing"

const arrayMedianRangeProg = `
import "std/array" as array;
function approx(a: f64, b: f64): boolean { let d: f64 = a - b; if (d < 0.0) { d = 0.0 - d; } return d < 0.0001; }
function unwrap(o: Option[f64]): f64 { match (o) { Some(v) => { return v; }, None => { return 0.0 - 999.0; } } }
function main(): i32 {
    // odd length -> exact middle (unsorted input)
    let odd: f64[] = [5.0, 1.0, 3.0, 2.0, 4.0];
    if (!approx(unwrap(array.median_f64(odd)), 3.0)) { return 1; }
    // even length -> average of the two middles
    let even: f64[] = [1.0, 2.0, 3.0, 4.0];
    if (!approx(unwrap(array.median_f64(even)), 2.5)) { return 2; }
    // even, unsorted, with a fractional result
    let even2: f64[] = [10.0, 2.0, 8.0, 4.0];
    if (!approx(unwrap(array.median_f64(even2)), 6.0)) { return 3; }   // sorted 2,4,8,10 -> (4+8)/2
    if (!approx(unwrap(array.median_f64([42.0])), 42.0)) { return 4; }
    // range = max - min
    if (!approx(unwrap(odd.range()), 4.0)) { return 5; }
    if (!approx(unwrap([7.0].range()), 0.0)) { return 6; }
    if (!approx(unwrap([0.0 - 3.0, 5.0, 1.0].range()), 8.0)) { return 7; }
    // empty -> None
    let empty: f64[] = [];
    match (array.median_f64(empty)) { Some(v) => { return 8; }, None => {} }
    match (empty.range()) { Some(v) => { return 9; }, None => {} }
    return 42;
}
`

func TestArrayMedianRangeInterp(t *testing.T) {
	if got := runInterpExit(t, arrayMedianRangeProg); got != 42 {
		t.Fatalf("interp got %d, want 42", got)
	}
}

func TestArrayMedianRangeX86_64(t *testing.T) {
	if _, got := compileAndRunX86_64(t, arrayMedianRangeProg); got != 42 {
		t.Fatalf("x86-64 got %d, want 42", got)
	}
}

func TestArrayMedianRangeWasm(t *testing.T) {
	if got := compileAndRunWasmbinMain(t, arrayMedianRangeProg); got != 42 {
		t.Fatalf("wasm got %d, want 42", got)
	}
}

func TestArrayMedianRangeArm64(t *testing.T) {
	if _, got := compileAndRunArm64(t, arrayMedianRangeProg); got != 42 {
		t.Fatalf("arm64 got %d, want 42", got)
	}
}
