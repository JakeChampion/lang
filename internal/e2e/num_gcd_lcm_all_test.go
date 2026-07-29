package e2e

// Differential coverage for the whole-array gcd / lcm folds `gcd_all` /
// `lcm_all` on std/i32 and std/i64. `gcd_all` seeds at 0 (so empty -> 0),
// `lcm_all` at 1 (empty -> 1); both fold the pairwise `gcd` / `lcm`. Returns
// 42 iff every check holds across interp / x86-64 / wasm / arm64.

import "testing"

const numGcdLcmAllProg = `
import "std/i32";
import "std/i64" as i64m;
function main(): i32 {
    if (i32.gcd_all([12, 18, 24]) != 6) { return 1; }
    if (i32.gcd_all([13, 17]) != 1) { return 2; }        // coprime
    if (i32.gcd_all([7]) != 7) { return 3; }
    if (i32.gcd_all([]) != 0) { return 4; }              // gcd identity
    if (i32.lcm_all([2, 3, 4]) != 12) { return 5; }
    if (i32.lcm_all([]) != 1) { return 6; }              // lcm identity
    if (i32.lcm_all([5]) != 5) { return 7; }
    // gcd_all(x) * lcm-style relationship spot check.
    if (i32.gcd_all([6, 10, 15]) != 1) { return 8; }
    // i64 width, past the i32 range.
    if (i64m.gcd_all([100000000000 as i64, 250000000000 as i64]) != 50000000000) { return 9; }
    if (i64m.lcm_all([1000000 as i64, 1500000 as i64]) != 3000000) { return 10; }
    return 42;
}
`

func TestNumGcdLcmAllInterp(t *testing.T) {
	if got := runInterpExit(t, numGcdLcmAllProg); got != 42 {
		t.Fatalf("interp got %d, want 42", got)
	}
}

func TestNumGcdLcmAllX86_64(t *testing.T) {
	if _, got := compileAndRunX86_64(t, numGcdLcmAllProg); got != 42 {
		t.Fatalf("x86-64 got %d, want 42", got)
	}
}

func TestNumGcdLcmAllWasm(t *testing.T) {
	if got := compileAndRunWasmbinMain(t, numGcdLcmAllProg); got != 42 {
		t.Fatalf("wasm got %d, want 42", got)
	}
}

func TestNumGcdLcmAllArm64(t *testing.T) {
	if _, got := compileAndRunArm64(t, numGcdLcmAllProg); got != 42 {
		t.Fatalf("arm64 got %d, want 42", got)
	}
}
