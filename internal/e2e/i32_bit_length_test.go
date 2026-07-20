package e2e

import "testing"

// Differential coverage for std/i32.bit_length — the number of bits needed to
// represent the MAGNITUDE |n| (highest set bit + 1): 0 -> 0, 1 -> 1, 5 (101) ->
// 3, 255 -> 8, 256 -> 9. Negative n uses its magnitude, and i32::MIN's magnitude
// 2^31 needs 32 bits (the widen-to-i64 edge). Returns 42 iff every check holds
// across interp / x86-64 / wasm / arm64; each leg skips itself when its
// toolchain is absent.
const i32BitLengthProg = `
import "std/i32";
function main(): i32 {
    if ((0).bit_length() != 0) { return 1; }
    if ((1).bit_length() != 1) { return 2; }
    if ((5).bit_length() != 3) { return 3; }        // 101
    if ((7).bit_length() != 3) { return 4; }        // 111
    if ((8).bit_length() != 4) { return 5; }        // 1000
    if ((255).bit_length() != 8) { return 6; }
    if ((256).bit_length() != 9) { return 7; }
    if ((0 - 5).bit_length() != 3) { return 8; }    // magnitude
    if ((0 - 1).bit_length() != 1) { return 9; }
    if ((2147483647).bit_length() != 31) { return 10; } // i32::MAX
    // i32::MIN magnitude is 2^31 -> 32 bits (the widen-to-i64 edge). Built as
    // (MAX - 1) since the bare 2147483648 literal is out of i32 range.
    var min: i32 = (0 - 2147483647) - 1;
    if (min.bit_length() != 32) { return 11; }
    return 42;
}
`

func TestI32BitLengthInterp(t *testing.T) {
	if got := runInterpExit(t, i32BitLengthProg); got != 42 {
		t.Fatalf("interp got %d, want 42", got)
	}
}

func TestI32BitLengthX86_64(t *testing.T) {
	if _, got := compileAndRunX86_64(t, i32BitLengthProg); got != 42 {
		t.Fatalf("x86-64 got %d, want 42", got)
	}
}

func TestI32BitLengthWasm(t *testing.T) {
	if got := compileAndRunWasmbinMain(t, i32BitLengthProg); got != 42 {
		t.Fatalf("wasm got %d, want 42", got)
	}
}

func TestI32BitLengthArm64(t *testing.T) {
	if _, got := compileAndRunArm64(t, i32BitLengthProg); got != 42 {
		t.Fatalf("arm64 got %d, want 42", got)
	}
}
