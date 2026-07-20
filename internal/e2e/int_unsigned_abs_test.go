package e2e

import "testing"

// Differential coverage for the integer unsigned_abs helpers — std/i32 (→ u32)
// and std/i64 (→ u64). unsigned_abs returns |n| in the same-width unsigned type,
// where the one magnitude the signed abs / checked_abs cannot represent — MIN,
// whose |n| is 2^(w-1) — fits exactly. Checks a negative, a positive, zero, and
// the MIN boundary that only the unsigned result can hold. Returns 42 iff every
// check holds across interp / x86-64 / wasm / arm64; each leg skips itself when
// its toolchain is absent.
const intUnsignedAbsProg = `
import "std/i32";
import "std/i64" as i64m;
function main(): i32 {
    var min32: i32 = 0 - 2147483647 - 1;
    var min64: i64 = (0 as i64) - 9223372036854775807 - 1;
    // i32 → u32.
    if ((0 - 5).unsigned_abs() != (5 as u32)) { return 1; }
    if ((7).unsigned_abs() != (7 as u32)) { return 2; }
    if ((0).unsigned_abs() != (0 as u32)) { return 3; }
    if (min32.unsigned_abs() != (2147483648 as u32)) { return 4; }   // |MIN| fits u32
    // i64 → u64.
    if ((0 as i64 - 5).unsigned_abs() != (5 as u64)) { return 10; }
    if ((7 as i64).unsigned_abs() != (7 as u64)) { return 11; }
    if (min64.unsigned_abs() != (9223372036854775808 as u64)) { return 12; }  // |MIN| fits u64
    return 42;
}
`

func TestIntUnsignedAbsInterp(t *testing.T) {
	if got := runInterpExit(t, intUnsignedAbsProg); got != 42 {
		t.Fatalf("interp got %d, want 42", got)
	}
}

func TestIntUnsignedAbsX86_64(t *testing.T) {
	if _, got := compileAndRunX86_64(t, intUnsignedAbsProg); got != 42 {
		t.Fatalf("x86-64 got %d, want 42", got)
	}
}

func TestIntUnsignedAbsWasm(t *testing.T) {
	if got := compileAndRunWasmbinMain(t, intUnsignedAbsProg); got != 42 {
		t.Fatalf("wasm got %d, want 42", got)
	}
}

func TestIntUnsignedAbsArm64(t *testing.T) {
	if _, got := compileAndRunArm64(t, intUnsignedAbsProg); got != 42 {
		t.Fatalf("arm64 got %d, want 42", got)
	}
}
