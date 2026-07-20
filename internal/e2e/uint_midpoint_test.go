package e2e

import "testing"

// Differential coverage for std/u32.midpoint / std/u64.midpoint — the
// overflow-safe unsigned average via (a & b) + ((a ^ b) >> 1) (logical shift).
// The naive (a+b)/2 wraps for large unsigned operands (the classic
// binary-search bug); this form never does, so the near-MAX cases are the crux.
// Scalar bit-ops, no wrap-around dependency, so this lowers on all four
// backends. Returns 42 iff every check holds across interp / x86-64 / wasm /
// arm64; each leg skips itself when its toolchain is absent.
const uintMidpointProg = `
import "std/u32" as u32m;
import "std/u64" as u64m;
function main(): i32 {
    if ((4 as u32).midpoint(6 as u32) != (5 as u32)) { return 1; }
    if ((2 as u32).midpoint(4 as u32) != (3 as u32)) { return 2; }
    if ((7 as u32).midpoint(7 as u32) != (7 as u32)) { return 3; }
    // near u32::MAX (4294967295): (a+b)/2 would wrap
    if ((4294967295 as u32).midpoint(4294967293 as u32) != (4294967294 as u32)) { return 4; }
    if ((3000000000 as u32).midpoint(3000000000 as u32) != (3000000000 as u32)) { return 5; }
    if ((3000000000 as u32).midpoint(3000000002 as u32) != (3000000001 as u32)) { return 6; }
    // u64
    if ((4 as u64).midpoint(6 as u64) != (5 as u64)) { return 7; }
    if ((10000000000 as u64).midpoint(10000000000 as u64) != (10000000000 as u64)) { return 8; }
    if ((10000000000 as u64).midpoint(10000000004 as u64) != (10000000002 as u64)) { return 9; }
    return 42;
}
`

func TestUintMidpointInterp(t *testing.T) {
	if got := runInterpExit(t, uintMidpointProg); got != 42 {
		t.Fatalf("interp got %d, want 42", got)
	}
}

func TestUintMidpointX86_64(t *testing.T) {
	if _, got := compileAndRunX86_64(t, uintMidpointProg); got != 42 {
		t.Fatalf("x86-64 got %d, want 42", got)
	}
}

func TestUintMidpointWasm(t *testing.T) {
	if got := compileAndRunWasmbinMain(t, uintMidpointProg); got != 42 {
		t.Fatalf("wasm got %d, want 42", got)
	}
}

func TestUintMidpointArm64(t *testing.T) {
	if _, got := compileAndRunArm64(t, uintMidpointProg); got != 42 {
		t.Fatalf("arm64 got %d, want 42", got)
	}
}
