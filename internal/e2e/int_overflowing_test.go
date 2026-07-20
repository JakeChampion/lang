package e2e

import "testing"

// Differential coverage for the integer overflowing_add / overflowing_sub /
// overflowing_mul helpers — std/i32, std/i64, std/u32, std/u64 — Rust's
// overflowing_* family: each returns (wrapped_result, did_overflow), the
// two's-complement wrapped value (identical to the bare operator) paired with a
// boolean that is true iff the op overflowed. Checks a non-overflowing and an
// overflowing case for each, verifying BOTH the wrapped value and the flag: the
// signed MAX+1 -> MIN / MIN-1 -> MAX wraps, the unsigned add/sub wrap and
// underflow, the 2^32 / 2^64 mul wraps, and the i64 MIN * -1 -> MIN case.
// Returns 42 iff every check holds across interp / x86-64 / wasm / arm64; each
// leg skips itself when its toolchain is absent.
const intOverflowingProg = `
import "std/i32";
import "std/i64" as i64m;
import "std/u32" as u32m;
import "std/u64" as u64m;
function main(): i32 {
    var max32: i32 = 2147483647;
    var min32: i32 = 0 - 2147483647 - 1;
    // i32 add: clean, then MAX+1 wraps to MIN.
    var (a1, o1) = (2).overflowing_add(3);
    if (a1 != 5 || o1) { return 1; }
    var (a2, o2) = max32.overflowing_add(1);
    if (a2 != min32 || !o2) { return 2; }
    // i32 sub: MIN-1 wraps to MAX.
    var (a3, o3) = min32.overflowing_sub(1);
    if (a3 != max32 || !o3) { return 3; }
    // i32 mul: clean, then 10^10 wraps (value + flag).
    var (a4, o4) = (46340).overflowing_mul(46340);
    if (a4 != 2147395600 || o4) { return 4; }
    var (a5, o5) = (100000).overflowing_mul(100000);
    if (!o5 || a5 != 1410065408) { return 5; }         // 10^10 mod 2^32
    // u32 add (wrap past 2^32), sub (underflow), mul (2^32 -> 0).
    var (b1, p1) = (4000000000 as u32).overflowing_add(1000000000 as u32);
    if (!p1) { return 10; }
    var (b2, p2) = (3 as u32).overflowing_sub(5 as u32);
    if (!p2 || b2 != (4294967294 as u32)) { return 11; }
    var (b3, p3) = (65536 as u32).overflowing_mul(65536 as u32);
    if (!p3 || b3 != (0 as u32)) { return 12; }
    // i64 mul: overflow, MIN * -1 -> MIN, and a clean case.
    var min64: i64 = (0 as i64) - 9223372036854775807 - 1;
    var (c1, q1) = (3037000500 as i64).overflowing_mul(3037000500 as i64);
    if (!q1) { return 20; }
    var (c2, q2) = min64.overflowing_mul((0 as i64) - 1);
    if (!q2 || c2 != min64) { return 21; }
    var (c3, q3) = (1000 as i64).overflowing_mul(1000 as i64);
    if (q3 || c3 != (1000000 as i64)) { return 22; }
    // u64 mul: 2^64 -> 0, and a clean case.
    var (d1, r1) = (4294967296 as u64).overflowing_mul(4294967296 as u64);
    if (!r1 || d1 != (0 as u64)) { return 30; }
    var (d2, r2) = (1000 as u64).overflowing_mul(1000 as u64);
    if (r2 || d2 != (1000000 as u64)) { return 31; }
    return 42;
}
`

func TestIntOverflowingInterp(t *testing.T) {
	if got := runInterpExit(t, intOverflowingProg); got != 42 {
		t.Fatalf("interp got %d, want 42", got)
	}
}

func TestIntOverflowingX86_64(t *testing.T) {
	if _, got := compileAndRunX86_64(t, intOverflowingProg); got != 42 {
		t.Fatalf("x86-64 got %d, want 42", got)
	}
}

func TestIntOverflowingWasm(t *testing.T) {
	if got := compileAndRunWasmbinMain(t, intOverflowingProg); got != 42 {
		t.Fatalf("wasm got %d, want 42", got)
	}
}

func TestIntOverflowingArm64(t *testing.T) {
	if _, got := compileAndRunArm64(t, intOverflowingProg); got != 42 {
		t.Fatalf("arm64 got %d, want 42", got)
	}
}
