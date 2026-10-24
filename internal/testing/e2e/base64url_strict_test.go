package e2e

import "testing"

const base64urlStrictProg = `
import "std/base64" as b64;
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
// Re-encodes decoded bytes so a non-UTF-8 payload can be compared
// without ever being read back as text.
function reenc(o: Result[u8[], codec.DecodeError]): string {
    match (o) { Ok(v) => { return b64.encode_url(v); }, Err(_) => { return "X"; } }
}
function main(): i32 {
    if (opt(b64.decode_url(b64.encode_url("Hello".bytes())), "X") != "Hello") { return 1; }
    if (opt(b64.decode_url(b64.encode_url("".bytes())), "X") != "") { return 2; }
    if (opt(b64.decode_url("SGVsbG8"), "X") != "Hello") { return 3; }
    if (opt(b64.decode_url("SGVsbG8="), "X") != "Hello") { return 4; }
    // Bytes that use the url-safe -/_ alphabet round-trip via encode.
    // FF FE FD is not valid UTF-8, so it stays u8[] (#5730) and the
    // comparison goes through the encoded text.
    let raw: u8[] = __codec_bytes(b64.decode("//79"));
    let rawenc: string = b64.encode_url(raw);
    if (reenc(b64.decode_url(rawenc)) != rawenc) { return 5; }
    if (opt(b64.decode_url(""), "X") != "") { return 6; }
    if (!isnone(b64.decode_url("SGVsbG8+"))) { return 7; }
    if (!isnone(b64.decode_url("SGVs/bG8"))) { return 8; }
    if (!isnone(b64.decode_url("SG!sbG8"))) { return 9; }
    if (!isnone(b64.decode_url("SGVsbG8=="))) { return 10; }
    if (!isnone(b64.decode_url("A"))) { return 11; }
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

func TestBase64urlStrictInterp(t *testing.T) {
	if got := runInterpExit(t, base64urlStrictProg); got != 42 {
		t.Fatalf("interp got %d, want 42", got)
	}
}

func TestBase64urlStrictX86_64(t *testing.T) {
	if _, got := compileAndRunX86_64(t, base64urlStrictProg); got != 42 {
		t.Fatalf("x86-64 got %d, want 42", got)
	}
}

func TestBase64urlStrictWasm(t *testing.T) {
	if got := compileAndRunWasmbinMain(t, base64urlStrictProg); got != 42 {
		t.Fatalf("wasm got %d, want 42", got)
	}
}

func TestBase64urlStrictArm64(t *testing.T) {
	if _, got := compileAndRunArm64(t, base64urlStrictProg); got != 42 {
		t.Fatalf("arm64 got %d, want 42", got)
	}
}
