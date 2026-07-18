package e2e

import "testing"

// Differential coverage for the integer checked_rem helpers — std/i32, std/i64,
// std/u32, std/u64 — the remainder sibling of checked_div, completing the
// checked_ family for %. checked_rem returns Some(n % other) except on
// divide-by-zero (all types) and MIN % -1 (the signed types, the one remainder
// that overflows). Checks a normal remainder, the sign-of-dividend result,
// divide-by-zero, and the MIN % -1 overflow. Returns 42 iff every check holds
// across interp / x86-64 / wasm / arm64; each leg skips itself when its
// toolchain is absent.
const intCheckedRemProg = `
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
    var min32: i32 = 0 - 2147483647 - 1;
    var min64: i64 = (0 as i64) - 9223372036854775807 - 1;
    // i32.
    if (!ci32((17).checked_rem(5), 2)) { return 1; }
    if (!ci32((0 - 17).checked_rem(5), 0 - 2)) { return 2; }
    if (!ni32((5).checked_rem(0))) { return 3; }
    if (!ni32(min32.checked_rem(0 - 1))) { return 4; }
    if (!ci32((10).checked_rem(2), 0)) { return 5; }
    // i64.
    if (!ci64((17 as i64).checked_rem(5 as i64), 2 as i64)) { return 10; }
    if (!ni64((5 as i64).checked_rem(0 as i64))) { return 11; }
    if (!ni64(min64.checked_rem((0 as i64) - 1))) { return 12; }
    // u32 / u64 (no overflow case, only divide-by-zero).
    if (!cu32((17 as u32).checked_rem(5 as u32), 2 as u32)) { return 20; }
    if (!nu32((5 as u32).checked_rem(0 as u32))) { return 21; }
    if (!cu64((17 as u64).checked_rem(5 as u64), 2 as u64)) { return 30; }
    if (!nu64((5 as u64).checked_rem(0 as u64))) { return 31; }
    return 42;
}
`

func TestIntCheckedRemInterp(t *testing.T) {
	if got := runInterpExit(t, intCheckedRemProg); got != 42 {
		t.Fatalf("interp got %d, want 42", got)
	}
}

func TestIntCheckedRemX86_64(t *testing.T) {
	if _, got := compileAndRunX86_64(t, intCheckedRemProg); got != 42 {
		t.Fatalf("x86-64 got %d, want 42", got)
	}
}

func TestIntCheckedRemWasm(t *testing.T) {
	if got := compileAndRunWasmbinMain(t, intCheckedRemProg); got != 42 {
		t.Fatalf("wasm got %d, want 42", got)
	}
}

func TestIntCheckedRemArm64(t *testing.T) {
	if _, got := compileAndRunArm64(t, intCheckedRemProg); got != 42 {
		t.Fatalf("arm64 got %d, want 42", got)
	}
}
