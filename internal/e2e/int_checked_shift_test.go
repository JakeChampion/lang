package e2e

import "testing"

// Differential coverage for the integer checked_shl / checked_shr helpers —
// std/i32, std/i64, std/u32, std/u64. The bare << / >> mask the shift amount to
// the type width (1 << 32 silently yields 1, not 0), so checked_shl / checked_shr
// return Some(shifted) only when the amount is in range [0, width) and None
// otherwise. Checks an in-range shift, the top-bit shift, an out-of-range and a
// negative amount, and the signed-arithmetic vs unsigned-logical >> distinction.
// Returns 42 iff every check holds across interp / x86-64 / wasm / arm64; each
// leg skips itself when its toolchain is absent.
const intCheckedShiftProg = `
import "std/i32";
import "std/i64" as i64m;
import "std/u32" as u32m;
import "std/u64" as u64m;
function ci32(o: Option[i32], w: i32): boolean { match (o) { Some(v) => { return v == w; }, None => { return false; } } }
function ni32(o: Option[i32]): boolean { match (o) { Some(v) => { return false; }, None => { return true; } } }
function ci64(o: Option[i64], w: i64): boolean { match (o) { Some(v) => { return v == w; }, None => { return false; } } }
function ni64(o: Option[i64]): boolean { match (o) { Some(v) => { return false; }, None => { return true; } } }
function cu32(o: Option[u32], w: u32): boolean { match (o) { Some(v) => { return v == w; }, None => { return false; } } }
function nu32(o: Option[u32]): boolean { match (o) { Some(v) => { return false; }, None => { return true; } } }
function cu64(o: Option[u64], w: u64): boolean { match (o) { Some(v) => { return v == w; }, None => { return false; } } }
function nu64(o: Option[u64]): boolean { match (o) { Some(v) => { return false; }, None => { return true; } } }
function main(): i32 {
    // i32: in-range, 1<<31 = MIN, out-of-range, negative, arithmetic shr.
    if (!ci32((1).checked_shl(4), 16)) { return 1; }
    if (!ci32((1).checked_shl(31), 0 - 2147483647 - 1)) { return 2; }
    if (!ni32((1).checked_shl(32))) { return 3; }
    if (!ni32((1).checked_shl(0 - 1))) { return 4; }
    if (!ci32((0 - 8).checked_shr(2), 0 - 2)) { return 5; }
    if (!ci32((16).checked_shr(2), 4)) { return 6; }
    if (!ni32((16).checked_shr(32))) { return 7; }
    // i64.
    if (!ci64((1 as i64).checked_shl(40), (1099511627776 as i64))) { return 10; }
    if (!ni64((1 as i64).checked_shl(64))) { return 11; }
    if (!ci64(((0 as i64) - 64).checked_shr(2), (0 as i64) - 16)) { return 12; }
    // u32: 2^31 fits (unsigned), out-of-range, logical shr.
    if (!cu32((1 as u32).checked_shl(31), (2147483648 as u32))) { return 20; }
    if (!nu32((1 as u32).checked_shl(32))) { return 21; }
    if (!cu32((4294967295 as u32).checked_shr(28), (15 as u32))) { return 22; }
    // u64: 2^63 fits, out-of-range, logical shr.
    if (!cu64((1 as u64).checked_shl(63), (9223372036854775808 as u64))) { return 30; }
    if (!nu64((1 as u64).checked_shl(64))) { return 31; }
    if (!cu64((18446744073709551615 as u64).checked_shr(60), (15 as u64))) { return 32; }
    return 42;
}
`

func TestIntCheckedShiftInterp(t *testing.T) {
	if got := runInterpExit(t, intCheckedShiftProg); got != 42 {
		t.Fatalf("interp got %d, want 42", got)
	}
}

func TestIntCheckedShiftX86_64(t *testing.T) {
	if _, got := compileAndRunX86_64(t, intCheckedShiftProg); got != 42 {
		t.Fatalf("x86-64 got %d, want 42", got)
	}
}

func TestIntCheckedShiftWasm(t *testing.T) {
	if got := compileAndRunWasmbinMain(t, intCheckedShiftProg); got != 42 {
		t.Fatalf("wasm got %d, want 42", got)
	}
}

func TestIntCheckedShiftArm64(t *testing.T) {
	if _, got := compileAndRunArm64(t, intCheckedShiftProg); got != 42 {
		t.Fatalf("arm64 got %d, want 42", got)
	}
}
