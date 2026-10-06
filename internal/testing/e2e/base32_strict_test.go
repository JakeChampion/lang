package e2e

import "testing"

// Differential coverage for std/base32.base32_decode_strict across
// backends: round-trips of base32_encode output, padded and unpadded
// valid input, the empty string, and the None rejections (bad padding
// count, non-base32 char, junk, trailing junk after padding, an
// impossible 1-char final group, and over-long padding). Contrasts the
// lenient base32_decode, which truncates instead. Returns 42 iff every
// check holds. Each leg skips itself when its toolchain is absent.
const base32StrictProg = `
import "std/base32" as b32;
import "std/string";
function opt(o: Option[u8[]], fb: string): string {
    // The fixtures here are ASCII; a real ingest path would use
    // std/utf8.from_bytes instead of the unchecked read.
    match (o) { Some(v) => { return string_from_bytes_unchecked(v); }, None => { return fb; } }
}
function isnone(o: Option[u8[]]): boolean {
    match (o) { Some(v) => { return false; }, None => { return true; } }
}
function main(): i32 {
    if (opt(b32.base32_decode_strict(b32.base32_encode("Hi".bytes())), "X") != "Hi") { return 1; }
    if (opt(b32.base32_decode_strict(b32.base32_encode("Hello".bytes())), "X") != "Hello") { return 2; }
    if (opt(b32.base32_decode_strict(b32.base32_encode("".bytes())), "X") != "") { return 3; }
    if (opt(b32.base32_decode_strict("JBUQ===="), "X") != "Hi") { return 4; }
    if (opt(b32.base32_decode_strict("JBUQ"), "X") != "Hi") { return 5; }
    if (opt(b32.base32_decode_strict(""), "X") != "") { return 6; }
    if (!isnone(b32.base32_decode_strict("JBUQ="))) { return 7; }
    if (!isnone(b32.base32_decode_strict("JBU0===="))) { return 8; }
    if (!isnone(b32.base32_decode_strict("!!!"))) { return 9; }
    if (!isnone(b32.base32_decode_strict("JBUQ====X"))) { return 10; }
    if (!isnone(b32.base32_decode_strict("A"))) { return 11; }
    if (!isnone(b32.base32_decode_strict("JB======="))) { return 12; }
    if (string_from_bytes_unchecked(b32.base32_decode("JBU0")) != string_from_bytes_unchecked(b32.base32_decode("JBU"))) { return 13; }
    return 42;
}
`

func TestBase32StrictInterp(t *testing.T) {
	if got := runInterpExit(t, base32StrictProg); got != 42 {
		t.Fatalf("interp got %d, want 42", got)
	}
}

func TestBase32StrictX86_64(t *testing.T) {
	if _, got := compileAndRunX86_64(t, base32StrictProg); got != 42 {
		t.Fatalf("x86-64 got %d, want 42", got)
	}
}

func TestBase32StrictWasm(t *testing.T) {
	if got := compileAndRunWasmbinMain(t, base32StrictProg); got != 42 {
		t.Fatalf("wasm got %d, want 42", got)
	}
}

func TestBase32StrictArm64(t *testing.T) {
	if _, got := compileAndRunArm64(t, base32StrictProg); got != 42 {
		t.Fatalf("arm64 got %d, want 42", got)
	}
}
