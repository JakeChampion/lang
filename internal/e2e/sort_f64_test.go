package e2e

import "testing"

// Differential coverage for core/cmp.sort / sort_desc over an f64[] — bottom-up
// merge sort, the floating-point exercise of the generic `Ord` sorts (the
// retired per-width `sort_f64_asc` / `sort_f64_desc`, #5397). f64 arrays are
// scalar-payload, so this lowers on all four backends. Checks ascending /
// descending order, the single / empty edge cases, and negatives. Returns 42
// iff every check holds across interp / x86-64 / wasm / arm64; each leg skips
// itself when its toolchain is absent.
const sortF64Prog = `
import "core/cmp" as cmp;
function approx(a: f64, b: f64): boolean { var d: f64 = a - b; if (d < 0.0) { d = 0.0 - d; } return d < 0.0001; }
function main(): i32 {
    var xs: f64[] = [3.5, 1.2, 4.8, 1.1, 5.9, 2.6];
    var asc: f64[] = cmp.sort(xs);
    if (!approx(asc[0], 1.1)) { return 1; }
    if (!approx(asc[1], 1.2)) { return 2; }
    if (!approx(asc[5], 5.9)) { return 3; }
    var desc: f64[] = cmp.sort_desc(xs);
    if (!approx(desc[0], 5.9)) { return 4; }
    if (!approx(desc[5], 1.1)) { return 5; }
    // input not mutated
    if (!approx(xs[0], 3.5)) { return 6; }
    // single / empty edge cases
    var one: f64[] = [42.0];
    if (!approx(cmp.sort(one)[0], 42.0)) { return 7; }
    var empty: f64[] = [];
    if (cmp.sort(empty).len() != 0) { return 8; }
    // negatives sort below zero
    var neg: f64[] = [0.0 - 1.0, 2.0, 0.0 - 3.0, 0.0];
    var nasc: f64[] = cmp.sort(neg);
    if (!approx(nasc[0], 0.0 - 3.0)) { return 9; }
    if (!approx(nasc[1], 0.0 - 1.0)) { return 10; }
    if (!approx(nasc[3], 2.0)) { return 11; }
    return 42;
}
`

func TestSortF64Interp(t *testing.T) {
	if got := runInterpExit(t, sortF64Prog); got != 42 {
		t.Fatalf("interp got %d, want 42", got)
	}
}

func TestSortF64X86_64(t *testing.T) {
	if _, got := compileAndRunX86_64(t, sortF64Prog); got != 42 {
		t.Fatalf("x86-64 got %d, want 42", got)
	}
}

func TestSortF64Wasm(t *testing.T) {
	if got := compileAndRunWasmbinMain(t, sortF64Prog); got != 42 {
		t.Fatalf("wasm got %d, want 42", got)
	}
}

func TestSortF64Arm64(t *testing.T) {
	if _, got := compileAndRunArm64(t, sortF64Prog); got != 42 {
		t.Fatalf("arm64 got %d, want 42", got)
	}
}
