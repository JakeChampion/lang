package e2e

import "testing"

// Differential coverage for std/i64's bit ops (the wider counterparts to
// std/i32's): count_ones — set bits in the 64-bit two's-complement
// representation (a negative n counts sign-extended high bits) — and bit_length
// — bits to represent the magnitude |n| (highest set bit + 1, i64::MIN special-
// cased). Values past the i32 range (2^32, i64::MAX/MIN) exercise the full i64
// width. Both return i32, so the wasmbin enum-i64 gap doesn't apply. Returns 42
// iff every check holds across interp / x86-64 / wasm / arm64; each leg skips
// itself when its toolchain is absent.
const i64BitOpsProg = `
import "std/i64";
function main(): i32 {
    // ---- count_ones ----
    if ((0 as i64).count_ones() != 0) { return 1; }
    if ((7 as i64).count_ones() != 3) { return 2; }          // 111
    if ((255 as i64).count_ones() != 8) { return 3; }
    if (((0 as i64) - 1).count_ones() != 64) { return 4; }   // all ones
    if ((4294967296 as i64).count_ones() != 1) { return 5; } // 2^32, one set bit
    // ---- bit_length ----
    if ((0 as i64).bit_length() != 0) { return 6; }
    if ((1 as i64).bit_length() != 1) { return 7; }
    if ((255 as i64).bit_length() != 8) { return 8; }
    if ((4294967296 as i64).bit_length() != 33) { return 9; }  // 2^32 -> 33 bits
    if (((0 as i64) - 5).bit_length() != 3) { return 10; }     // magnitude
    if ((9223372036854775807 as i64).bit_length() != 63) { return 11; } // i64::MAX
    var mn: i64 = (0 as i64) - (9223372036854775807 as i64) - (1 as i64);
    if (mn.bit_length() != 64) { return 12; }                  // i64::MIN
    return 42;
}
`

func TestI64BitOpsInterp(t *testing.T) {
	if got := runInterpExit(t, i64BitOpsProg); got != 42 {
		t.Fatalf("interp got %d, want 42", got)
	}
}

func TestI64BitOpsX86_64(t *testing.T) {
	if _, got := compileAndRunX86_64(t, i64BitOpsProg); got != 42 {
		t.Fatalf("x86-64 got %d, want 42", got)
	}
}

func TestI64BitOpsWasm(t *testing.T) {
	if got := compileAndRunWasmbinMain(t, i64BitOpsProg); got != 42 {
		t.Fatalf("wasm got %d, want 42", got)
	}
}

func TestI64BitOpsArm64(t *testing.T) {
	if _, got := compileAndRunArm64(t, i64BitOpsProg); got != 42 {
		t.Fatalf("arm64 got %d, want 42", got)
	}
}
