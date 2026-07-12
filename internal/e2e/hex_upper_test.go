package e2e

import "testing"

// Differential coverage for std/hex.hex_encode_upper across backends:
// uppercase A-F output (vs the lowercase hex_encode), multi-byte UTF-8
// input, the empty string, and a round-trip through hex_decode (which
// accepts either case). Returns 42 iff every check holds. Each leg skips
// itself when its toolchain is absent.
const hexUpperProg = `
import "std/hex" as hex;
function main(): i32 {
    if (hex.hex_encode_upper("Hi") != "4869") { return 1; }
    if (hex.hex_encode_upper("z") != "7A") { return 2; }
    if (hex.hex_encode("z") != "7a") { return 3; }
    if (hex.hex_encode_upper("ÿ") != "C3BF") { return 4; }
    if (hex.hex_encode("ÿ") != "c3bf") { return 5; }
    if (hex.hex_encode_upper("") != "") { return 6; }
    if (hex.hex_decode(hex.hex_encode_upper("hello")) != "hello") { return 7; }
    return 42;
}
`

func TestHexUpperInterp(t *testing.T) {
	if got := runInterpExit(t, hexUpperProg); got != 42 {
		t.Fatalf("interp got %d, want 42", got)
	}
}

func TestHexUpperX86_64(t *testing.T) {
	if _, got := compileAndRunX86_64(t, hexUpperProg); got != 42 {
		t.Fatalf("x86-64 got %d, want 42", got)
	}
}

func TestHexUpperWasm(t *testing.T) {
	if got := compileAndRunWasmbinMain(t, hexUpperProg); got != 42 {
		t.Fatalf("wasm got %d, want 42", got)
	}
}

func TestHexUpperArm64(t *testing.T) {
	if _, got := compileAndRunArm64(t, hexUpperProg); got != 42 {
		t.Fatalf("arm64 got %d, want 42", got)
	}
}
