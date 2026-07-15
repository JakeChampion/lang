package e2e

import "testing"

// Differential coverage for std/array.median_f64 / range_f64 — the median
// (averaging the two middles for even length) and the max-min spread. Both
// return Option[f64] (None for empty), so the wasmbin leg skips (the enum-i64/
// f64 gap avg_f64 / variance_f64 already carry). median_f64 rides sort_f64_asc;
// range_f64 is a single pass. Returns 42 iff every check holds across interp /
// x86-64 / wasm / arm64; each leg skips itself when its toolchain is absent.
const arrayMedianRangeProg = `
import "std/array" as array;
function approx(a: f64, b: f64): boolean { var d: f64 = a - b; if (d < 0.0) { d = 0.0 - d; } return d < 0.0001; }
function unwrap(o: Option[f64]): f64 { match (o) { Some(v) => { return v; }, None => { return 0.0 - 999.0; } } }
function main(): i32 {
    // odd length -> exact middle (unsorted input)
    var odd: f64[] = [5.0, 1.0, 3.0, 2.0, 4.0];
    if (!approx(unwrap(array.median_f64(odd)), 3.0)) { return 1; }
    // even length -> average of the two middles
    var even: f64[] = [1.0, 2.0, 3.0, 4.0];
    if (!approx(unwrap(array.median_f64(even)), 2.5)) { return 2; }
    // even, unsorted, with a fractional result
    var even2: f64[] = [10.0, 2.0, 8.0, 4.0];
    if (!approx(unwrap(array.median_f64(even2)), 6.0)) { return 3; }   // sorted 2,4,8,10 -> (4+8)/2
    if (!approx(unwrap(array.median_f64([42.0])), 42.0)) { return 4; }
    // range = max - min
    if (!approx(unwrap(array.range_f64(odd)), 4.0)) { return 5; }
    if (!approx(unwrap(array.range_f64([7.0])), 0.0)) { return 6; }
    if (!approx(unwrap(array.range_f64([0.0 - 3.0, 5.0, 1.0])), 8.0)) { return 7; }
    // empty -> None
    var empty: f64[] = [];
    match (array.median_f64(empty)) { Some(v) => { return 8; }, None => {} }
    match (array.range_f64(empty)) { Some(v) => { return 9; }, None => {} }
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
