package e2e

import "testing"

// Differential coverage for std/i32.midpoint / std/i64.midpoint — the
// overflow-safe average via (a & b) + ((a ^ b) >> 1), rounded toward negative
// infinity. The naive (a+b)/2 overflows for large same-sign operands (the
// classic binary-search bug); this form never does, so the near-MAX cases are
// the crux. Scalar bit-ops, so this lowers on all four backends. Returns 42 iff
// every check holds across interp / x86-64 / wasm / arm64; each leg skips
// itself when its toolchain is absent.
const intMidpointProg = `
import "std/i32" as i32m;
import "std/i64" as i64m;
function main(): i32 {
    if ((4).midpoint(6) != 5) { return 1; }
    if ((2).midpoint(4) != 3) { return 2; }
    if ((7).midpoint(7) != 7) { return 3; }
    if ((0 - 4).midpoint(0 - 2) != (0 - 3)) { return 4; }        // negatives, floor
    if ((0 - 2).midpoint(4) != 1) { return 5; }                  // straddling zero
    if ((2147483647).midpoint(2147483645) != 2147483646) { return 6; }  // near i32::MAX, no overflow
    if ((2000000000).midpoint(2000000000) != 2000000000) { return 7; }  // would overflow (a+b)/2
    // i64
    if ((4 as i64).midpoint(6 as i64) != (5 as i64)) { return 8; }
    if (((0 as i64) - 100).midpoint(50 as i64) != ((0 as i64) - 25)) { return 9; }
    var mx: i64 = (9223372036854775807 as i64);
    if (mx.midpoint(mx - (2 as i64)) != (mx - (1 as i64))) { return 10; }   // near i64::MAX
    if ((5000000000 as i64).midpoint(5000000000 as i64) != (5000000000 as i64)) { return 11; }
    return 42;
}
`

func TestIntMidpointInterp(t *testing.T) {
	if got := runInterpExit(t, intMidpointProg); got != 42 {
		t.Fatalf("interp got %d, want 42", got)
	}
}

func TestIntMidpointX86_64(t *testing.T) {
	if _, got := compileAndRunX86_64(t, intMidpointProg); got != 42 {
		t.Fatalf("x86-64 got %d, want 42", got)
	}
}

func TestIntMidpointWasm(t *testing.T) {
	if got := compileAndRunWasmbinMain(t, intMidpointProg); got != 42 {
		t.Fatalf("wasm got %d, want 42", got)
	}
}

func TestIntMidpointArm64(t *testing.T) {
	if _, got := compileAndRunArm64(t, intMidpointProg); got != 42 {
		t.Fatalf("arm64 got %d, want 42", got)
	}
}
