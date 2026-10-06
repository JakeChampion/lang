package e2e

import "testing"

// Differential coverage for the integer checked_pow helpers — std/i32, std/i64,
// std/u32, std/u64 — the overflow-checked exponentiation that completes the
// checked_ family for the last arithmetic op. checked_pow does
// exponentiation-by-squaring, routing every intermediate product through
// checked_mul, and returns None on the first overflow (or for a negative
// exponent). Checks exact results straddling each type's max (2^30/2^31,
// 2^62/2^63, 2^31/2^32, 2^63/2^64, 10^k boundaries), the exp==0 -> 1 identity,
// a zero and a negative base, and the negative-exponent -> None case. Returns
// 42 iff every check holds across interp / x86-64 / wasm / arm64; each leg skips
// itself when its toolchain is absent.
const intCheckedPowProg = `
import "std/i32";
import "std/i64" as i64m;
import "std/u32" as u32m;
import "std/u64" as u64m;
function ci32(o: Option[i32], want: i32): boolean { match (o) { Some(v) => { return v == want; }, None => { return false; } } }
function ni32(o: Option[i32]): boolean { match (o) { Some(v) => { return false; }, None => { return true; } } }
function ci64(o: Option[i64], want: i64): boolean { match (o) { Some(v) => { return v == want; }, None => { return false; } } }
function ni64(o: Option[i64]): boolean { match (o) { Some(v) => { return false; }, None => { return true; } } }
function cu32(o: Option[u32], want: u32): boolean { match (o) { Some(v) => { return v == want; }, None => { return false; } } }
function nu32(o: Option[u32]): boolean { match (o) { Some(v) => { return false; }, None => { return true; } } }
function cu64(o: Option[u64], want: u64): boolean { match (o) { Some(v) => { return v == want; }, None => { return false; } } }
function nu64(o: Option[u64]): boolean { match (o) { Some(v) => { return false; }, None => { return true; } } }
function main(): i32 {
    // i32: exact, the overflow boundary (2^30 fits, 2^31 does not), exp 0, a
    // zero and a negative base, and a negative exponent.
    if (!ci32((2).checked_pow(10), 1024)) { return 1; }
    if (!ci32((10).checked_pow(9), 1000000000)) { return 2; }
    if (!ni32((10).checked_pow(10))) { return 3; }
    if (!ni32((2).checked_pow(31))) { return 4; }
    if (!ci32((2).checked_pow(30), 1073741824)) { return 5; }
    if (!ci32((7).checked_pow(0), 1)) { return 6; }
    if (!ci32((0).checked_pow(5), 0)) { return 7; }
    if (!ci32((0 - 3).checked_pow(3), 0 - 27)) { return 8; }
    if (!ni32((5).checked_pow(0 - 1))) { return 9; }
    // i64: 2^62 / 2^63 and 10^18 / 10^19 boundaries.
    if (!ci64((2 as i64).checked_pow(62), 4611686018427387904 as i64)) { return 10; }
    if (!ni64((2 as i64).checked_pow(63))) { return 11; }
    if (!ci64((10 as i64).checked_pow(18), 1000000000000000000 as i64)) { return 12; }
    if (!ni64((10 as i64).checked_pow(19))) { return 13; }
    // u32: 2^31 fits (unsigned), 2^32 overflows.
    if (!cu32((2 as u32).checked_pow(31), 2147483648 as u32)) { return 20; }
    if (!nu32((2 as u32).checked_pow(32))) { return 21; }
    if (!cu32((3 as u32).checked_pow(0), 1 as u32)) { return 22; }
    // u64: 2^63 fits (unsigned), 2^64 overflows; 10^19 fits.
    if (!cu64((2 as u64).checked_pow(63), 9223372036854775808 as u64)) { return 30; }
    if (!nu64((2 as u64).checked_pow(64))) { return 31; }
    if (!cu64((10 as u64).checked_pow(19), 10000000000000000000 as u64)) { return 32; }
    return 42;
}
`

func TestIntCheckedPowInterp(t *testing.T) {
	if got := runInterpExit(t, intCheckedPowProg); got != 42 {
		t.Fatalf("interp got %d, want 42", got)
	}
}

func TestIntCheckedPowX86_64(t *testing.T) {
	if _, got := compileAndRunX86_64(t, intCheckedPowProg); got != 42 {
		t.Fatalf("x86-64 got %d, want 42", got)
	}
}

func TestIntCheckedPowWasm(t *testing.T) {
	if got := compileAndRunWasmbinMain(t, intCheckedPowProg); got != 42 {
		t.Fatalf("wasm got %d, want 42", got)
	}
}

func TestIntCheckedPowArm64(t *testing.T) {
	if _, got := compileAndRunArm64(t, intCheckedPowProg); got != 42 {
		t.Fatalf("arm64 got %d, want 42", got)
	}
}
