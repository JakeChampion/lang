package e2e

import "testing"

// Differential coverage for the integer checked_mul helpers — std/i32,
// std/i64, std/u32, std/u64 — completing the checked_ arithmetic family
// (add / sub / div already shipped) with the most overflow-prone op.
// checked_mul returns Some(n*other) on no overflow and None on overflow.
// The i32 / u32 versions widen to i64 / u64 and range-check (exact); the
// i64 / u64 versions have no wider type and detect overflow with the inverse
// division (p/n == other), with i64 rejecting the MIN * -1 wrap explicitly.
// This exercises the sign edges (MIN * -1), the 2^32 / 2^64 boundaries, and
// the in-range maxima ((2^16-1)^2, (2^32-1)^2). Returns 42 iff every check
// holds across interp / x86-64 / wasm / arm64; each leg skips itself when its
// toolchain is absent.
const intCheckedMulProg = `
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
    var min32: i32 = 0 - 2147483647 - 1;
    var min64: i64 = (0 as i64) - 9223372036854775807 - 1;
    // i32: in-range, negative, overflow, MIN * -1, the largest exact square, zero.
    if (!ci32((6).checked_mul(7), 42)) { return 1; }
    if (!ci32((0 - 6).checked_mul(7), 0 - 42)) { return 2; }
    if (!ni32((100000).checked_mul(100000))) { return 3; }              // 10^10 > i32::MAX
    if (!ni32(min32.checked_mul(0 - 1))) { return 4; }                  // MIN * -1
    if (!ci32((46340).checked_mul(46340), 2147395600)) { return 5; }    // largest in-range square
    if (!ci32((0).checked_mul(min32), 0)) { return 6; }
    // i64: in-range wide, overflow, MIN * -1 (both orders), MIN * 1.
    if (!ci64((6 as i64).checked_mul(7 as i64), 42 as i64)) { return 10; }
    if (!ci64((3037000499 as i64).checked_mul(3000000000 as i64), 9111001497000000000 as i64)) { return 11; }
    if (!ni64((3037000500 as i64).checked_mul(3037000500 as i64))) { return 12; }   // ~9.22e18 > i64::MAX
    if (!ni64(min64.checked_mul((0 as i64) - 1))) { return 13; }        // MIN * -1
    if (!ni64(((0 as i64) - 1).checked_mul(min64))) { return 14; }      // -1 * MIN
    if (!ci64(min64.checked_mul(1 as i64), min64)) { return 15; }       // MIN * 1 ok
    // u32: in-range, the largest exact square, overflow.
    if (!cu32((6 as u32).checked_mul(7 as u32), 42 as u32)) { return 20; }
    if (!cu32((65535 as u32).checked_mul(65535 as u32), 4294836225 as u32)) { return 21; }  // (2^16-1)^2
    if (!nu32((65536 as u32).checked_mul(65536 as u32))) { return 22; }  // 2^32
    // u64: in-range, the largest exact square, overflow.
    if (!cu64((6 as u64).checked_mul(7 as u64), 42 as u64)) { return 30; }
    if (!cu64((4294967295 as u64).checked_mul(4294967295 as u64), 18446744065119617025 as u64)) { return 31; }  // (2^32-1)^2
    if (!nu64((4294967296 as u64).checked_mul(4294967296 as u64))) { return 32; }  // 2^64
    return 42;
}
`

func TestIntCheckedMulInterp(t *testing.T) {
	if got := runInterpExit(t, intCheckedMulProg); got != 42 {
		t.Fatalf("interp got %d, want 42", got)
	}
}

func TestIntCheckedMulX86_64(t *testing.T) {
	if _, got := compileAndRunX86_64(t, intCheckedMulProg); got != 42 {
		t.Fatalf("x86-64 got %d, want 42", got)
	}
}

func TestIntCheckedMulWasm(t *testing.T) {
	if got := compileAndRunWasmbinMain(t, intCheckedMulProg); got != 42 {
		t.Fatalf("wasm got %d, want 42", got)
	}
}

func TestIntCheckedMulArm64(t *testing.T) {
	if _, got := compileAndRunArm64(t, intCheckedMulProg); got != 42 {
		t.Fatalf("arm64 got %d, want 42", got)
	}
}
