package e2e

import "testing"

const i64AbsDiffRangeProg = `
import "std/i64";
function main(): i32 {
    let a: i64 = 5000000000 as i64;
    let b: i64 = 3000000000 as i64;
    // ---- abs_diff ----
    if (a.abs_diff(b) != (2000000000 as i64)) { return 1; }
    if (b.abs_diff(a) != (2000000000 as i64)) { return 2; }
    if ((0 as i64).abs_diff(0 - (7 as i64)) != (7 as i64)) { return 3; }
    if ((0 - (100 as i64)).abs_diff(0 - (40 as i64)) != (60 as i64)) { return 4; }
    if ((10 as i64).abs_diff(10 as i64) != (0 as i64)) { return 5; }
    // ---- is_in_range (half-open) ----
    if (!(5 as i64).is_in_range(1 as i64, 10 as i64)) { return 6; }
    if ((10 as i64).is_in_range(1 as i64, 10 as i64)) { return 7; }   // hi excluded
    if ((0 as i64).is_in_range(1 as i64, 10 as i64)) { return 8; }
    if (!(4000000000 as i64).is_in_range(0 as i64, 5000000000 as i64)) { return 9; }
    // ---- is_between (inclusive) ----
    if (!(10 as i64).is_between(1 as i64, 10 as i64)) { return 10; }   // hi included
    if (!(1 as i64).is_between(1 as i64, 10 as i64)) { return 11; }    // lo included
    if ((11 as i64).is_between(1 as i64, 10 as i64)) { return 12; }
    return 42;
}
`

func TestI64AbsDiffRangeInterp(t *testing.T) {
	if got := runInterpExit(t, i64AbsDiffRangeProg); got != 42 {
		t.Fatalf("interp got %d, want 42", got)
	}
}

func TestI64AbsDiffRangeX86_64(t *testing.T) {
	if _, got := compileAndRunX86_64(t, i64AbsDiffRangeProg); got != 42 {
		t.Fatalf("x86-64 got %d, want 42", got)
	}
}

func TestI64AbsDiffRangeWasm(t *testing.T) {
	if got := compileAndRunWasmbinMain(t, i64AbsDiffRangeProg); got != 42 {
		t.Fatalf("wasm got %d, want 42", got)
	}
}

func TestI64AbsDiffRangeArm64(t *testing.T) {
	if _, got := compileAndRunArm64(t, i64AbsDiffRangeProg); got != 42 {
		t.Fatalf("arm64 got %d, want 42", got)
	}
}
