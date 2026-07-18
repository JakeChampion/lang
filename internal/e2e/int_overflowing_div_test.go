package e2e

import "testing"

// Differential coverage for the integer overflowing_div / overflowing_rem
// helpers — std/i32, std/i64, std/u32, std/u64 — completing the overflowing_
// family for `/` and `%`. Each returns the wrapped quotient / remainder paired
// with a did-overflow boolean; the one overflowing pair on the signed types is
// MIN / -1 (quotient wraps to MIN, remainder is 0), and unsigned division never
// overflows. Checks a normal divide, the MIN/-1 overflow (signed), and the
// always-false unsigned flag. Returns 42 iff every check holds across interp /
// x86-64 / wasm / arm64; each leg skips itself when its toolchain is absent.
const intOverflowingDivProg = `
import "std/i32";
import "std/i64" as i64m;
import "std/u32" as u32m;
import "std/u64" as u64m;
function di32(t: (i32, boolean), w: i32, f: boolean): boolean { return t.0 == w && t.1 == f; }
function di64(t: (i64, boolean), w: i64, f: boolean): boolean { return t.0 == w && t.1 == f; }
function du32(t: (u32, boolean), w: u32, f: boolean): boolean { return t.0 == w && t.1 == f; }
function du64(t: (u64, boolean), w: u64, f: boolean): boolean { return t.0 == w && t.1 == f; }
function main(): i32 {
    var min32: i32 = 0 - 2147483647 - 1;
    var min64: i64 = (0 as i64) - 9223372036854775807 - 1;
    // i32.
    if (!di32((17).overflowing_div(5), 3, false)) { return 1; }
    if (!di32((17).overflowing_rem(5), 2, false)) { return 2; }
    if (!di32(min32.overflowing_div(0 - 1), min32, true)) { return 3; }
    if (!di32(min32.overflowing_rem(0 - 1), 0, true)) { return 4; }
    if (!di32((0 - 17).overflowing_rem(5), 0 - 2, false)) { return 5; }
    // i64.
    if (!di64((17 as i64).overflowing_div(5 as i64), 3 as i64, false)) { return 10; }
    if (!di64(min64.overflowing_div((0 as i64) - 1), min64, true)) { return 11; }
    if (!di64(min64.overflowing_rem((0 as i64) - 1), 0 as i64, true)) { return 12; }
    // u32 / u64 (flag always false).
    if (!du32((17 as u32).overflowing_div(5 as u32), 3 as u32, false)) { return 20; }
    if (!du32((17 as u32).overflowing_rem(5 as u32), 2 as u32, false)) { return 21; }
    if (!du64((17 as u64).overflowing_div(5 as u64), 3 as u64, false)) { return 30; }
    if (!du64((17 as u64).overflowing_rem(5 as u64), 2 as u64, false)) { return 31; }
    return 42;
}
`

func TestIntOverflowingDivInterp(t *testing.T) {
	if got := runInterpExit(t, intOverflowingDivProg); got != 42 {
		t.Fatalf("interp got %d, want 42", got)
	}
}

func TestIntOverflowingDivX86_64(t *testing.T) {
	if _, got := compileAndRunX86_64(t, intOverflowingDivProg); got != 42 {
		t.Fatalf("x86-64 got %d, want 42", got)
	}
}

func TestIntOverflowingDivWasm(t *testing.T) {
	if got := compileAndRunWasmbinMain(t, intOverflowingDivProg); got != 42 {
		t.Fatalf("wasm got %d, want 42", got)
	}
}

func TestIntOverflowingDivArm64(t *testing.T) {
	if _, got := compileAndRunArm64(t, intOverflowingDivProg); got != 42 {
		t.Fatalf("arm64 got %d, want 42", got)
	}
}
