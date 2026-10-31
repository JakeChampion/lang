package e2e

import "testing"

const numGcdLcmAllProg = `
import "std/array";
function main(): i32 {
    let none: i32[] = [];
    if ([12, 18, 24].gcd_all() != 6) { return 1; }
    if ([13, 17].gcd_all() != 1) { return 2; }        // coprime
    if ([7].gcd_all() != 7) { return 3; }
    if (none.gcd_all() != 0) { return 4; }            // gcd identity
    if ([2, 3, 4].lcm_all() != 12) { return 5; }
    if (none.lcm_all() != 1) { return 6; }            // lcm identity
    if ([5].lcm_all() != 5) { return 7; }
    if ([0 - 6, 10, 15].gcd_all() != 1) { return 8; }
    // i64 width, past the i32 range.
    let wide: i64[] = [100000000000 as i64, 250000000000 as i64];
    if (wide.gcd_all() != 50000000000) { return 9; }
    let mid: i64[] = [1000000 as i64, 1500000 as i64];
    if (mid.lcm_all() != 3000000) { return 10; }
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
