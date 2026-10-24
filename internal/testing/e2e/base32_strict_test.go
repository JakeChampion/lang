package e2e

import "testing"

// Differential coverage for std/base32.decode across
// backends: round-trips of encode output, padded and unpadded
// valid input, the empty string, and typed rejections (bad padding
// count, non-base32 char, junk, trailing junk after padding, an
// impossible final group, and over-long padding). Returns 42 iff every
// check holds. Each leg skips itself when its toolchain is absent.
const base32StrictProg = `
import "std/base32" as b32;
import "std/codec";
import "std/string";
function opt(o: Result[u8[], codec.DecodeError], fb: string): string {
    // The fixtures here are ASCII; a real ingest path would use
    // std/utf8.from_bytes instead of the unchecked read.
    match (o) { Ok(v) => { return string_from_bytes_unchecked(v); }, Err(_) => { return fb; } }
}
function isnone(o: Result[u8[], codec.DecodeError]): boolean {
    match (o) { Ok(v) => { return false; }, Err(_) => { return true; } }
}
function main(): i32 {
    if (opt(b32.decode(b32.encode("Hi".bytes())), "X") != "Hi") { return 1; }
    if (opt(b32.decode(b32.encode("Hello".bytes())), "X") != "Hello") { return 2; }
    if (opt(b32.decode(b32.encode("".bytes())), "X") != "") { return 3; }
    if (opt(b32.decode("JBUQ===="), "X") != "Hi") { return 4; }
    if (opt(b32.decode("JBUQ"), "X") != "Hi") { return 5; }
    if (opt(b32.decode(""), "X") != "") { return 6; }
    if (!isnone(b32.decode("JBUQ="))) { return 7; }
    if (!isnone(b32.decode("JBU0===="))) { return 8; }
    if (!isnone(b32.decode("!!!"))) { return 9; }
    if (!isnone(b32.decode("JBUQ====X"))) { return 10; }
    if (!isnone(b32.decode("A"))) { return 11; }
    if (!isnone(b32.decode("JB======="))) { return 12; }
    if (!isnone(b32.decode("JBU0")) || !isnone(b32.decode("JBU"))) { return 13; }
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
