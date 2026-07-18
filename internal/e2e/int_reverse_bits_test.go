package e2e

import "testing"

// Differential coverage for the integer reverse_bits helpers — std/i32,
// std/i64, std/u32, std/u64 — the one member the bit-ops family (count_ones /
// leading_zeros / trailing_zeros / byte_swap / rotate_left / rotate_right) was
// missing. reverse_bits reverses the ORDER of the width's bits (bit i -> bit
// width-1-i), operating on the raw two's-complement pattern via the unsigned
// twin so signed shifts don't sign-extend. Checks the low-bit -> top-bit
// mapping (reverse_bits(1) == the type's MIN / 2^(w-1)), the all-ones fixpoint,
// a known nibble reversal (0x0000000F <-> 0xF0000000), and the round-trip
// involution. Returns 42 iff every check holds across interp / x86-64 / wasm /
// arm64; each leg skips itself when its toolchain is absent.
const intReverseBitsProg = `
import "std/i32";
import "std/i64" as i64m;
import "std/u32" as u32m;
import "std/u64" as u64m;
function main(): i32 {
    var i32min: i32 = 0 - 2147483647 - 1;
    var i64min: i64 = (0 as i64) - 9223372036854775807 - 1;
    // i32: bit 0 -> bit 31 (== MIN), the involution, a nibble reversal, the
    // all-ones fixpoint, and an arbitrary round-trip.
    if ((1).reverse_bits() != i32min) { return 1; }
    if (i32min.reverse_bits() != 1) { return 2; }
    if ((15).reverse_bits() != (0 - 268435456)) { return 3; }        // 0x0000000F -> 0xF0000000
    if ((0).reverse_bits() != 0) { return 4; }
    if ((0 - 1).reverse_bits() != (0 - 1)) { return 5; }             // all-ones fixpoint
    if ((12345).reverse_bits().reverse_bits() != 12345) { return 6; }
    // u32: unsigned, so bit 0 -> 2^31.
    if ((1 as u32).reverse_bits() != (2147483648 as u32)) { return 10; }
    if ((2147483648 as u32).reverse_bits() != (1 as u32)) { return 11; }
    if ((15 as u32).reverse_bits() != (4026531840 as u32)) { return 12; }  // 0xF0000000
    if ((3735928559 as u32).reverse_bits().reverse_bits() != (3735928559 as u32)) { return 13; }  // 0xDEADBEEF
    // i64: bit 0 -> bit 63 (== MIN).
    if ((1 as i64).reverse_bits() != i64min) { return 20; }
    if (i64min.reverse_bits() != (1 as i64)) { return 21; }
    if ((987654321 as i64).reverse_bits().reverse_bits() != (987654321 as i64)) { return 22; }
    // u64: bit 0 -> 2^63.
    if ((1 as u64).reverse_bits() != (9223372036854775808 as u64)) { return 30; }
    if ((9223372036854775808 as u64).reverse_bits() != (1 as u64)) { return 31; }
    if ((1311768467294899695 as u64).reverse_bits().reverse_bits() != (1311768467294899695 as u64)) { return 32; }  // 0x123456789ABCDEF
    return 42;
}
`

func TestIntReverseBitsInterp(t *testing.T) {
	if got := runInterpExit(t, intReverseBitsProg); got != 42 {
		t.Fatalf("interp got %d, want 42", got)
	}
}

func TestIntReverseBitsX86_64(t *testing.T) {
	if _, got := compileAndRunX86_64(t, intReverseBitsProg); got != 42 {
		t.Fatalf("x86-64 got %d, want 42", got)
	}
}

func TestIntReverseBitsWasm(t *testing.T) {
	if got := compileAndRunWasmbinMain(t, intReverseBitsProg); got != 42 {
		t.Fatalf("wasm got %d, want 42", got)
	}
}

func TestIntReverseBitsArm64(t *testing.T) {
	if _, got := compileAndRunArm64(t, intReverseBitsProg); got != 42 {
		t.Fatalf("arm64 got %d, want 42", got)
	}
}
