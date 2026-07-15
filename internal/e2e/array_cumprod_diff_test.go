package e2e

import "testing"

// Differential coverage for std/array.cumprod_f64 / diff_f64 — two scan-style
// operations that need sequential context (so map can't express them):
// cumprod is the running product; diff is the successive differences (one
// shorter than the input, the discrete derivative and left-inverse of cumsum).
// Both return an f64[] (scalar payload), so they lower on all four backends.
// Returns 42 iff every check holds across interp / x86-64 / wasm / arm64; each
// leg skips itself when its toolchain is absent.
const arrayCumprodDiffProg = `
import "std/array" as array;
function approx(a: f64, b: f64): boolean { var d: f64 = a - b; if (d < 0.0) { d = 0.0 - d; } return d < 0.0001; }
function main(): i32 {
    var xs: f64[] = [1.0, 2.0, 3.0, 4.0];
    var cp: f64[] = array.cumprod_f64(xs);
    if (!approx(cp[0], 1.0)) { return 1; }
    if (!approx(cp[2], 6.0)) { return 2; }
    if (!approx(cp[3], 24.0)) { return 3; }
    if (cp.len() != 4) { return 4; }
    var df: f64[] = array.diff_f64([1.0, 3.0, 6.0, 10.0]);
    if (df.len() != 3) { return 5; }                                // one shorter
    if (!approx(df[0], 2.0)) { return 6; }
    if (!approx(df[2], 4.0)) { return 7; }
    // diff(cumsum(xs)) recovers xs (minus its first element)
    var d2: f64[] = array.diff_f64(array.cumsum_f64(xs));
    if (!approx(d2[0], 2.0)) { return 8; }
    if (!approx(d2[2], 4.0)) { return 9; }
    // negative differences
    if (!approx(array.diff_f64([10.0, 4.0])[0], 0.0 - 6.0)) { return 10; }
    // edge cases
    if (array.diff_f64([5.0]).len() != 0) { return 11; }            // < 2 -> empty
    if (array.diff_f64([]).len() != 0) { return 12; }
    if (array.cumprod_f64([]).len() != 0) { return 13; }
    return 42;
}
`

func TestArrayCumprodDiffInterp(t *testing.T) {
	if got := runInterpExit(t, arrayCumprodDiffProg); got != 42 {
		t.Fatalf("interp got %d, want 42", got)
	}
}

func TestArrayCumprodDiffX86_64(t *testing.T) {
	if _, got := compileAndRunX86_64(t, arrayCumprodDiffProg); got != 42 {
		t.Fatalf("x86-64 got %d, want 42", got)
	}
}

func TestArrayCumprodDiffWasm(t *testing.T) {
	if got := compileAndRunWasmbinMain(t, arrayCumprodDiffProg); got != 42 {
		t.Fatalf("wasm got %d, want 42", got)
	}
}

func TestArrayCumprodDiffArm64(t *testing.T) {
	if _, got := compileAndRunArm64(t, arrayCumprodDiffProg); got != 42 {
		t.Fatalf("arm64 got %d, want 42", got)
	}
}
