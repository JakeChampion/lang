package e2e

import "testing"

// Differential coverage for the integer saturating_mul helpers — std/i32,
// std/i64, std/u32, std/u64 — the clamping companion of checked_mul and the
// multiply sibling of saturating_add / saturating_sub, completing the
// saturating_ family. saturating_mul clamps to the type's MAX on positive
// overflow and (for the signed types) to MIN on negative overflow, instead of
// wrapping. The i32 / u32 versions read the clamp off the widened product; the
// i64 / u64 versions detect overflow with the inverse division and pick the
// bound from the product's sign, with i64 clamping MIN * -1 to MAX. Exercises
// both clamp directions, the sign edges, and the in-range maxima. Returns 42
// iff every check holds across interp / x86-64 / wasm / arm64; each leg skips
// itself when its toolchain is absent.
const intSaturatingMulProg = `
import "std/i32";
import "std/i64" as i64m;
import "std/u32" as u32m;
import "std/u64" as u64m;
function main(): i32 {
    var max32: i32 = 2147483647;
    var min32: i32 = 0 - 2147483647 - 1;
    var max64: i64 = 9223372036854775807;
    var min64: i64 = (0 as i64) - 9223372036854775807 - 1;
    var umax32: u32 = 4294967295 as u32;
    var umax64: u64 = 18446744073709551615 as u64;
    // i32: in-range, +overflow -> MAX, -overflow -> MIN, (-)*(-) -> +MAX,
    // MIN * -1 -> MAX, largest in-range square.
    if ((6).saturating_mul(7) != 42) { return 1; }
    if ((100000).saturating_mul(100000) != max32) { return 2; }
    if ((0 - 100000).saturating_mul(100000) != min32) { return 3; }
    if ((0 - 100000).saturating_mul(0 - 100000) != max32) { return 4; }
    if (min32.saturating_mul(0 - 1) != max32) { return 5; }
    if ((46340).saturating_mul(46340) != 2147395600) { return 6; }
    // i64: in-range, +overflow -> MAX, -overflow -> MIN, MIN * -1 -> MAX,
    // MIN * 1 -> MIN (no overflow).
    if ((6 as i64).saturating_mul(7 as i64) != (42 as i64)) { return 10; }
    if ((3037000500 as i64).saturating_mul(3037000500 as i64) != max64) { return 11; }
    if (((0 as i64) - 3037000500).saturating_mul(3037000500 as i64) != min64) { return 12; }
    if (min64.saturating_mul((0 as i64) - 1) != max64) { return 13; }
    if (min64.saturating_mul(1 as i64) != min64) { return 14; }
    // u32: in-range, overflow -> MAX, largest in-range square.
    if ((6 as u32).saturating_mul(7 as u32) != (42 as u32)) { return 20; }
    if ((65536 as u32).saturating_mul(65536 as u32) != umax32) { return 21; }
    if ((65535 as u32).saturating_mul(65535 as u32) != (4294836225 as u32)) { return 22; }
    // u64: in-range, overflow -> MAX, largest in-range square.
    if ((6 as u64).saturating_mul(7 as u64) != (42 as u64)) { return 30; }
    if ((4294967296 as u64).saturating_mul(4294967296 as u64) != umax64) { return 31; }
    if ((4294967295 as u64).saturating_mul(4294967295 as u64) != (18446744065119617025 as u64)) { return 32; }
    return 42;
}
`

func TestIntSaturatingMulInterp(t *testing.T) {
	if got := runInterpExit(t, intSaturatingMulProg); got != 42 {
		t.Fatalf("interp got %d, want 42", got)
	}
}

func TestIntSaturatingMulX86_64(t *testing.T) {
	if _, got := compileAndRunX86_64(t, intSaturatingMulProg); got != 42 {
		t.Fatalf("x86-64 got %d, want 42", got)
	}
}

func TestIntSaturatingMulWasm(t *testing.T) {
	if got := compileAndRunWasmbinMain(t, intSaturatingMulProg); got != 42 {
		t.Fatalf("wasm got %d, want 42", got)
	}
}

func TestIntSaturatingMulArm64(t *testing.T) {
	if _, got := compileAndRunArm64(t, intSaturatingMulProg); got != 42 {
		t.Fatalf("arm64 got %d, want 42", got)
	}
}
