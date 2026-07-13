package e2e

import "testing"

// Differential coverage for the unsigned range/diff helpers added to std/u32 and
// std/u64: abs_diff (larger minus smaller — always fits, no wrap) and the range
// predicates is_in_range (half-open) / is_between (inclusive). The MAX-value
// cases are load-bearing: they only pass if every backend uses UNSIGNED
// comparison for u32/u64 `<` / `>=` (a signed compare would treat u32::MAX /
// u64::MAX as negative and mis-answer the range checks). Returns 42 iff every
// check holds across interp / x86-64 / wasm / arm64; results are unsigned /
// boolean (no Option), so the wasmbin enum-i64 gap doesn't apply.
const uintAbsDiffRangeProg = `
import "std/u32";
import "std/u64";
function main(): i32 {
    // ---- u32 abs_diff ----
    if ((5 as u32).abs_diff(8 as u32) != (3 as u32)) { return 1; }
    if ((8 as u32).abs_diff(5 as u32) != (3 as u32)) { return 2; }
    if ((7 as u32).abs_diff(7 as u32) != (0 as u32)) { return 3; }
    // u32::MAX via wrap; abs_diff to 0 is MAX itself (unsigned, no overflow)
    var big: u32 = (0 as u32) - (1 as u32);
    if (big.abs_diff(0 as u32) != big) { return 4; }
    // ---- u32 range (unsigned compare across the sign-bit boundary) ----
    if (!big.is_between(0 as u32, big)) { return 5; }        // MAX inside inclusive
    if (big.is_in_range(0 as u32, big)) { return 6; }        // half-open excludes hi
    if (!(5 as u32).is_in_range(1 as u32, 10 as u32)) { return 7; }
    if ((10 as u32).is_in_range(1 as u32, 10 as u32)) { return 8; }
    // ---- u64 abs_diff ----
    if ((5000000000 as u64).abs_diff(3000000000 as u64) != (2000000000 as u64)) { return 9; }
    if ((3000000000 as u64).abs_diff(5000000000 as u64) != (2000000000 as u64)) { return 10; }
    // ---- u64 range (unsigned compare) ----
    var big64: u64 = (0 as u64) - (1 as u64);
    if (!big64.is_between(0 as u64, big64)) { return 11; }
    if (big64.is_in_range(0 as u64, big64)) { return 12; }
    if (!(5 as u64).is_in_range(1 as u64, 10 as u64)) { return 13; }
    if (!(10 as u64).is_between(1 as u64, 10 as u64)) { return 14; }
    return 42;
}
`

func TestUintAbsDiffRangeInterp(t *testing.T) {
	if got := runInterpExit(t, uintAbsDiffRangeProg); got != 42 {
		t.Fatalf("interp got %d, want 42", got)
	}
}

func TestUintAbsDiffRangeX86_64(t *testing.T) {
	if _, got := compileAndRunX86_64(t, uintAbsDiffRangeProg); got != 42 {
		t.Fatalf("x86-64 got %d, want 42", got)
	}
}

func TestUintAbsDiffRangeWasm(t *testing.T) {
	if got := compileAndRunWasmbinMain(t, uintAbsDiffRangeProg); got != 42 {
		t.Fatalf("wasm got %d, want 42", got)
	}
}

func TestUintAbsDiffRangeArm64(t *testing.T) {
	if _, got := compileAndRunArm64(t, uintAbsDiffRangeProg); got != 42 {
		t.Fatalf("arm64 got %d, want 42", got)
	}
}
