package e2e

import "testing"

// Differential coverage for std/i64.is_multiple_of / next_power_of_2 /
// ceil_div / log2_floor — scalar integer helpers ported from std/i32, exact
// into the i64 range. next_power_of_2 caps at 2^62 (the largest power of two a
// signed i64 holds) and returns 0 above it; log2_floor counts halvings (i64
// has no leading_zeros). Returns 42 iff every check holds across interp /
// x86-64 / wasm / arm64; each leg skips itself when its toolchain is absent.
const i64IntdivProg = `
import "std/i64" as i64m;
function main(): i32 {
    if (!(12 as i64).is_multiple_of(4 as i64)) { return 1; }
    if ((13 as i64).is_multiple_of(4 as i64)) { return 2; }
    if ((5 as i64).is_multiple_of(0 as i64)) { return 3; }                    // d==0 -> false
    if ((5 as i64).next_power_of_2() != (8 as i64)) { return 4; }
    if ((16 as i64).next_power_of_2() != (16 as i64)) { return 5; }           // already a power
    if ((1 as i64).next_power_of_2() != (1 as i64)) { return 6; }
    if ((4611686018427387904 as i64).next_power_of_2() != (4611686018427387904 as i64)) { return 7; }  // 2^62
    if (((4611686018427387904 as i64) + 1).next_power_of_2() != (0 as i64)) { return 8; }              // > 2^62 -> 0
    if ((10 as i64).ceil_div(3 as i64) != (4 as i64)) { return 9; }
    if ((9 as i64).ceil_div(3 as i64) != (3 as i64)) { return 10; }           // exact division
    if ((7 as i64).ceil_div(0 as i64) != (0 as i64)) { return 11; }           // d<=0 -> 0
    if ((1000000000000 as i64).log2_floor() != 39) { return 12; }            // 2^39 < 10^12 < 2^40
    if ((1 as i64).log2_floor() != 0) { return 13; }
    if ((0 as i64).log2_floor() != (0 - 1)) { return 14; }                    // sentinel
    return 42;
}
`

func TestI64IntdivInterp(t *testing.T) {
	if got := runInterpExit(t, i64IntdivProg); got != 42 {
		t.Fatalf("interp got %d, want 42", got)
	}
}

func TestI64IntdivX86_64(t *testing.T) {
	if _, got := compileAndRunX86_64(t, i64IntdivProg); got != 42 {
		t.Fatalf("x86-64 got %d, want 42", got)
	}
}

func TestI64IntdivWasm(t *testing.T) {
	if got := compileAndRunWasmbinMain(t, i64IntdivProg); got != 42 {
		t.Fatalf("wasm got %d, want 42", got)
	}
}

func TestI64IntdivArm64(t *testing.T) {
	if _, got := compileAndRunArm64(t, i64IntdivProg); got != 42 {
		t.Fatalf("arm64 got %d, want 42", got)
	}
}
