package e2e

import "testing"

// Differential coverage for std/i32's abs_diff (absolute difference, ordering-
// branched so it's the non-negative magnitude directly) and count_zeros (the
// complement of the existing count_ones — the two sum to 32). Returns 42 iff
// every check holds across interp / x86-64 / wasm / arm64; each leg skips
// itself when its toolchain is absent.
const i32AbsDiffCountZerosProg = `
import "std/i32";
function main(): i32 {
    // ---- abs_diff ----
    if ((5).abs_diff(8) != 3) { return 1; }
    if ((8).abs_diff(5) != 3) { return 2; }
    if ((0 - 3).abs_diff(4) != 7) { return 3; }
    if ((0 - 10).abs_diff(0 - 2) != 8) { return 4; }
    if ((7).abs_diff(7) != 0) { return 5; }
    if ((0).abs_diff(0 - 6) != 6) { return 6; }
    // ---- count_zeros ----
    if ((0).count_zeros() != 32) { return 7; }
    if ((0 - 1).count_zeros() != 0) { return 8; }   // all ones
    if ((1).count_zeros() != 31) { return 9; }
    if ((7).count_zeros() != 29) { return 10; }
    // ---- invariant: count_ones + count_zeros == 32 ----
    if ((5).count_ones() + (5).count_zeros() != 32) { return 11; }
    if ((305419896).count_ones() + (305419896).count_zeros() != 32) { return 12; }
    return 42;
}
`

func TestI32AbsDiffCountZerosInterp(t *testing.T) {
	if got := runInterpExit(t, i32AbsDiffCountZerosProg); got != 42 {
		t.Fatalf("interp got %d, want 42", got)
	}
}

func TestI32AbsDiffCountZerosX86_64(t *testing.T) {
	if _, got := compileAndRunX86_64(t, i32AbsDiffCountZerosProg); got != 42 {
		t.Fatalf("x86-64 got %d, want 42", got)
	}
}

func TestI32AbsDiffCountZerosWasm(t *testing.T) {
	if got := compileAndRunWasmbinMain(t, i32AbsDiffCountZerosProg); got != 42 {
		t.Fatalf("wasm got %d, want 42", got)
	}
}

func TestI32AbsDiffCountZerosArm64(t *testing.T) {
	if _, got := compileAndRunArm64(t, i32AbsDiffCountZerosProg); got != 42 {
		t.Fatalf("arm64 got %d, want 42", got)
	}
}
