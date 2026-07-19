package e2e

import "testing"

// Differential coverage for std/array.variance_f64 / stddev_f64 — population
// variance (mean of squared deviations, /n) and its square root. Both return
// Option[f64] (None for empty), matching the existing avg_f64. The
// wasmbin leg skips: the bare core-wasm backend doesn't emit Option over a
// 64-bit payload (the same enum-i64/f64 gap avg_f64 already carries) —
// unrelated to the math, which sqrt (a native wasm op) would otherwise cover.
// Uses the textbook {2,4,4,4,5,5,7,9} set: mean 5, variance 4, stddev 2.
// Returns 42 iff every check holds across interp / x86-64 / wasm / arm64; each
// leg skips itself when its toolchain is absent.
const arrayStatsProg = `
import "std/array" as array;
function approx(a: f64, b: f64): boolean { var d: f64 = a - b; if (d < 0.0) { d = 0.0 - d; } return d < 0.0001; }
function unwrap(o: Option[f64]): f64 { match (o) { Some(v) => { return v; }, None => { return 0.0 - 999.0; } } }
function main(): i32 {
    var xs: f64[] = [2.0, 4.0, 4.0, 4.0, 5.0, 5.0, 7.0, 9.0];
    if (!approx(unwrap(array.avg_f64(xs)), 5.0)) { return 1; }
    if (!approx(unwrap(array.variance_f64(xs)), 4.0)) { return 2; }
    if (!approx(unwrap(array.stddev_f64(xs)), 2.0)) { return 3; }
    // single element -> zero spread
    var single: f64[] = [42.0];
    if (!approx(unwrap(array.variance_f64(single)), 0.0)) { return 4; }
    if (!approx(unwrap(array.stddev_f64(single)), 0.0)) { return 5; }
    // constant data -> zero variance
    var flat: f64[] = [3.0, 3.0, 3.0];
    if (!approx(unwrap(array.variance_f64(flat)), 0.0)) { return 6; }
    // empty -> None
    var empty: f64[] = [];
    match (array.variance_f64(empty)) { Some(v) => { return 7; }, None => {} }
    match (array.stddev_f64(empty)) { Some(v) => { return 8; }, None => {} }
    return 42;
}
`

func TestArrayStatsInterp(t *testing.T) {
	if got := runInterpExit(t, arrayStatsProg); got != 42 {
		t.Fatalf("interp got %d, want 42", got)
	}
}

func TestArrayStatsX86_64(t *testing.T) {
	if _, got := compileAndRunX86_64(t, arrayStatsProg); got != 42 {
		t.Fatalf("x86-64 got %d, want 42", got)
	}
}

func TestArrayStatsWasm(t *testing.T) {
	if got := compileAndRunWasmbinMain(t, arrayStatsProg); got != 42 {
		t.Fatalf("wasm got %d, want 42", got)
	}
}

func TestArrayStatsArm64(t *testing.T) {
	if _, got := compileAndRunArm64(t, arrayStatsProg); got != 42 {
		t.Fatalf("arm64 got %d, want 42", got)
	}
}
