package e2e

// Differential coverage for the integer division-rounding helpers —
// std/i32, std/i64, std/u32, std/u64: `div_ceil` (round toward +inf),
// `div_floor` (round toward -inf), `next_multiple_of`, and the unsigned
// `is_multiple_of` (completing the family std/i32 + std/i64 already had).
// `/` truncates toward zero, so ceil/floor differ from it for signed
// operands whenever the division is inexact; the signed forms adjust the
// truncated quotient by the sign of the remainder relative to the divisor.
// A zero divisor returns 0 (Fern's division is total, never traps). Checks
// all four sign quadrants for div_ceil/div_floor, the exact and zero-divisor
// cases, and next_multiple_of / is_multiple_of. Returns 42 iff every check
// holds across interp / x86-64 / wasm / arm64; each leg skips itself when
// its toolchain is absent.

import "testing"

const intDivRoundingProg = `
import "std/i32";
import "std/i64" as i64m;
import "std/u32" as u32m;
import "std/u64" as u64m;
function main(): i32 {
    // i32 div_ceil — the four (sign of n, sign of d) quadrants + exact.
    if ((7).div_ceil(2) != 4) { return 1; }
    if ((0 - 7).div_ceil(2) != (0 - 3)) { return 2; }
    if ((7).div_ceil(0 - 2) != (0 - 3)) { return 3; }
    if ((0 - 7).div_ceil(0 - 2) != 4) { return 4; }
    if ((8).div_ceil(2) != 4) { return 5; }
    if ((7).div_ceil(0) != 0) { return 6; }
    // i32 div_floor — same quadrants.
    if ((7).div_floor(2) != 3) { return 7; }
    if ((0 - 7).div_floor(2) != (0 - 4)) { return 8; }
    if ((7).div_floor(0 - 2) != (0 - 4)) { return 9; }
    if ((0 - 7).div_floor(0 - 2) != 3) { return 10; }
    // ceil/floor coincide with / when exact, and the identity ceil - floor
    // is 1 for an inexact division, 0 for an exact one.
    if ((7).div_ceil(2) - (7).div_floor(2) != 1) { return 11; }
    if ((8).div_ceil(2) - (8).div_floor(2) != 0) { return 12; }
    // next_multiple_of.
    if ((7).next_multiple_of(5) != 10) { return 13; }
    if ((10).next_multiple_of(5) != 10) { return 14; }
    if ((0).next_multiple_of(5) != 0) { return 15; }
    if ((7).next_multiple_of(0) != 0) { return 16; }

    // i64 siblings, including a value past the i32 range.
    if ((0 - 7 as i64).div_ceil(2 as i64) != (0 - 3 as i64)) { return 20; }
    if ((0 - 7 as i64).div_floor(2 as i64) != (0 - 4 as i64)) { return 21; }
    if ((1000000000000 as i64).div_ceil(3 as i64) != (333333333334 as i64)) { return 22; }
    if ((100 as i64).next_multiple_of(7 as i64) != (105 as i64)) { return 23; }

    // u32 / u64: unsigned floor is /, and is_multiple_of completes the family.
    if ((7 as u32).div_ceil(2 as u32) != (4 as u32)) { return 30; }
    if ((7 as u32).div_floor(2 as u32) != (3 as u32)) { return 31; }
    if ((7 as u32).next_multiple_of(5 as u32) != (10 as u32)) { return 32; }
    if (!(14 as u32).is_multiple_of(7 as u32)) { return 33; }
    if ((15 as u32).is_multiple_of(7 as u32)) { return 34; }
    if ((7 as u32).is_multiple_of(0 as u32)) { return 35; }
    if ((1000000000000 as u64).div_ceil(3 as u64) != (333333333334 as u64)) { return 36; }
    if (!(1000000000000 as u64).is_multiple_of(1000000 as u64)) { return 37; }
    return 42;
}
`

func TestIntDivRoundingInterp(t *testing.T) {
	if got := runInterpExit(t, intDivRoundingProg); got != 42 {
		t.Fatalf("interp got %d, want 42", got)
	}
}

func TestIntDivRoundingX86_64(t *testing.T) {
	if _, got := compileAndRunX86_64(t, intDivRoundingProg); got != 42 {
		t.Fatalf("x86-64 got %d, want 42", got)
	}
}

func TestIntDivRoundingWasm(t *testing.T) {
	if got := compileAndRunWasmbinMain(t, intDivRoundingProg); got != 42 {
		t.Fatalf("wasm got %d, want 42", got)
	}
}

func TestIntDivRoundingArm64(t *testing.T) {
	if _, got := compileAndRunArm64(t, intDivRoundingProg); got != 42 {
		t.Fatalf("arm64 got %d, want 42", got)
	}
}
