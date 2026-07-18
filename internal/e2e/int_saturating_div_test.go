package e2e

import "testing"

// Differential coverage for the integer saturating_div / saturating_neg /
// saturating_abs helpers, completing the saturating_ family. saturating_div is on
// all four types (std/i32, std/i64, std/u32, std/u64); saturating_neg /
// saturating_abs are signed-only (std/i32, std/i64), matching Rust. These clamp
// the one overflowing case — MIN / -1, -MIN, |MIN| — to MAX instead of wrapping
// it to MIN. Checks a normal divide, the MIN/-1 → MAX clamp, MIN negation /
// magnitude → MAX, and the always-exact unsigned divide. Returns 42 iff every
// check holds across interp / x86-64 / wasm / arm64; each leg skips itself when
// its toolchain is absent.
const intSaturatingDivProg = `
import "std/i32";
import "std/i64" as i64m;
import "std/u32" as u32m;
import "std/u64" as u64m;
function main(): i32 {
    var max32: i32 = 2147483647;
    var min32: i32 = 0 - 2147483647 - 1;
    var max64: i64 = 9223372036854775807;
    var min64: i64 = (0 as i64) - 9223372036854775807 - 1;
    // i32.
    if ((17).saturating_div(5) != 3) { return 1; }
    if (min32.saturating_div(0 - 1) != max32) { return 2; }        // MIN / -1 clamps to MAX
    if (min32.saturating_neg() != max32) { return 3; }             // -MIN clamps to MAX
    if ((0 - 7).saturating_neg() != 7) { return 4; }
    if (min32.saturating_abs() != max32) { return 5; }             // |MIN| clamps to MAX
    if ((0 - 7).saturating_abs() != 7) { return 6; }
    // i64.
    if ((17 as i64).saturating_div(5 as i64) != (3 as i64)) { return 10; }
    if (min64.saturating_div((0 as i64) - 1) != max64) { return 11; }
    if (min64.saturating_neg() != max64) { return 12; }
    if (min64.saturating_abs() != max64) { return 13; }
    // u32 / u64 (division never overflows).
    if ((17 as u32).saturating_div(5 as u32) != (3 as u32)) { return 20; }
    if ((17 as u64).saturating_div(5 as u64) != (3 as u64)) { return 30; }
    return 42;
}
`

func TestIntSaturatingDivInterp(t *testing.T) {
	if got := runInterpExit(t, intSaturatingDivProg); got != 42 {
		t.Fatalf("interp got %d, want 42", got)
	}
}

func TestIntSaturatingDivX86_64(t *testing.T) {
	if _, got := compileAndRunX86_64(t, intSaturatingDivProg); got != 42 {
		t.Fatalf("x86-64 got %d, want 42", got)
	}
}

func TestIntSaturatingDivWasm(t *testing.T) {
	if got := compileAndRunWasmbinMain(t, intSaturatingDivProg); got != 42 {
		t.Fatalf("wasm got %d, want 42", got)
	}
}

func TestIntSaturatingDivArm64(t *testing.T) {
	if _, got := compileAndRunArm64(t, intSaturatingDivProg); got != 42 {
		t.Fatalf("arm64 got %d, want 42", got)
	}
}
