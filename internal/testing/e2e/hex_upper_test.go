package e2e

import "testing"

// Differential coverage for std/hex.encode_upper across backends:
// uppercase A-F output (vs the lowercase encode), multi-byte UTF-8
// input, the empty string, and a round-trip through decode (which
// accepts either case). Returns 42 iff every check holds. Each leg skips
// itself when its toolchain is absent.
const hexUpperProg = `
import "std/hex" as hex;
import "std/codec";
import "std/string";
function main(): i32 {
    if (hex.encode_upper("Hi".bytes()) != "4869") { return 1; }
    if (hex.encode_upper("z".bytes()) != "7A") { return 2; }
    if (hex.encode("z".bytes()) != "7a") { return 3; }
    if (hex.encode_upper("ÿ".bytes()) != "C3BF") { return 4; }
    if (hex.encode("ÿ".bytes()) != "c3bf") { return 5; }
    if (hex.encode_upper("".bytes()) != "") { return 6; }
    if (string_from_bytes_unchecked(__codec_bytes(hex.decode(hex.encode_upper("hello".bytes())))) != "hello") { return 7; }
    return 42;
}

// Decode a valid fixture. Report a malformed fixture before ending the test.
function __codec_bytes(value: Result[u8[], codec.DecodeError]): u8[] {
  match (value) {
    Ok(bytes) => { return bytes; },
    Err(problem) => { eprint(problem.message()); exit(98); return []; }
  }
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
