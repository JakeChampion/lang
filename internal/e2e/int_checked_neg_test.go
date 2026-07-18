package e2e

import "testing"

// Differential coverage for the unary integer overflow helpers on the signed
// types — std/i32 and std/i64: checked_neg / checked_abs (Option) and
// overflowing_neg ((wrapped, did_overflow)). MIN is the one value where unary
// `-` and `|·|` overflow (the magnitude 2^(w-1) has no representation), so
// abs() / bare `-` wrap MIN to itself; these give the overflow-safe forms.
// Checks the clean cases (both signs, zero) and the MIN overflow for each.
// Returns 42 iff every check holds across interp / x86-64 / wasm / arm64; each
// leg skips itself when its toolchain is absent.
const intCheckedNegProg = `
import "std/i32";
import "std/i64" as i64m;
function ci32(o: Option[i32], w: i32): boolean { match (o) { Some(v) => { return v == w; }, None => { return false; } } }
function ni32(o: Option[i32]): boolean { match (o) { Some(v) => { return false; }, None => { return true; } } }
function ci64(o: Option[i64], w: i64): boolean { match (o) { Some(v) => { return v == w; }, None => { return false; } } }
function ni64(o: Option[i64]): boolean { match (o) { Some(v) => { return false; }, None => { return true; } } }
function main(): i32 {
    var min32: i32 = 0 - 2147483647 - 1;
    var min64: i64 = (0 as i64) - 9223372036854775807 - 1;
    // i32 checked_neg / checked_abs.
    if (!ci32((5).checked_neg(), 0 - 5)) { return 1; }
    if (!ci32((0 - 5).checked_neg(), 5)) { return 2; }
    if (!ni32(min32.checked_neg())) { return 3; }
    if (!ci32((0).checked_neg(), 0)) { return 4; }
    if (!ci32((0 - 7).checked_abs(), 7)) { return 5; }
    if (!ci32((7).checked_abs(), 7)) { return 6; }
    if (!ni32(min32.checked_abs())) { return 7; }
    // i32 overflowing_neg.
    var (a1, o1) = (5).overflowing_neg();
    if (a1 != (0 - 5) || o1) { return 8; }
    var (a2, o2) = min32.overflowing_neg();
    if (a2 != min32 || !o2) { return 9; }
    // i64.
    if (!ci64((5 as i64).checked_neg(), (0 as i64) - 5)) { return 10; }
    if (!ni64(min64.checked_neg())) { return 11; }
    if (!ci64((0 - 9 as i64).checked_abs(), 9 as i64)) { return 12; }
    if (!ni64(min64.checked_abs())) { return 13; }
    var (b1, p1) = min64.overflowing_neg();
    if (b1 != min64 || !p1) { return 14; }
    var (b2, p2) = (100 as i64).overflowing_neg();
    if (b2 != ((0 as i64) - 100) || p2) { return 15; }
    return 42;
}
`

func TestIntCheckedNegInterp(t *testing.T) {
	if got := runInterpExit(t, intCheckedNegProg); got != 42 {
		t.Fatalf("interp got %d, want 42", got)
	}
}

func TestIntCheckedNegX86_64(t *testing.T) {
	if _, got := compileAndRunX86_64(t, intCheckedNegProg); got != 42 {
		t.Fatalf("x86-64 got %d, want 42", got)
	}
}

func TestIntCheckedNegWasm(t *testing.T) {
	if got := compileAndRunWasmbinMain(t, intCheckedNegProg); got != 42 {
		t.Fatalf("wasm got %d, want 42", got)
	}
}

func TestIntCheckedNegArm64(t *testing.T) {
	if _, got := compileAndRunArm64(t, intCheckedNegProg); got != 42 {
		t.Fatalf("arm64 got %d, want 42", got)
	}
}
