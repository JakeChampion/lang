package e2e

import "testing"

// Differential coverage for std/base64.base64url_decode_strict across
// backends: round-trips of base64url_encode output (unpadded), padded
// url input, the empty string, url-safe -/_ acceptance, and the None
// rejections — the non-url-safe +/ characters, junk, misaligned
// padding, and an impossible n%4==1 run. Returns 42 iff every check
// holds. Each leg skips itself when its toolchain is absent.
const base64urlStrictProg = `
import "std/base64" as b64;
import "std/string";
function opt(o: Option[u8[]], fb: string): string {
    // The fixtures here are ASCII; a real ingest path would use
    // std/utf8.from_bytes instead of the unchecked read.
    match (o) { Some(v) => { return string_from_bytes_unchecked(v); }, None => { return fb; } }
}
function isnone(o: Option[u8[]]): boolean {
    match (o) { Some(v) => { return false; }, None => { return true; } }
}
// Re-encodes decoded bytes so a non-UTF-8 payload can be compared
// without ever being read back as text.
function reenc(o: Option[u8[]]): string {
    match (o) { Some(v) => { return b64.base64url_encode(v); }, None => { return "X"; } }
}
function main(): i32 {
    if (opt(b64.base64url_decode_strict(b64.base64url_encode("Hello".bytes())), "X") != "Hello") { return 1; }
    if (opt(b64.base64url_decode_strict(b64.base64url_encode("".bytes())), "X") != "") { return 2; }
    if (opt(b64.base64url_decode_strict("SGVsbG8"), "X") != "Hello") { return 3; }
    if (opt(b64.base64url_decode_strict("SGVsbG8="), "X") != "Hello") { return 4; }
    // Bytes that use the url-safe -/_ alphabet round-trip via encode.
    // FF FE FD is not valid UTF-8, so it stays u8[] (#5730) and the
    // comparison goes through the encoded text.
    var raw: u8[] = b64.base64_decode("//79");
    var rawenc: string = b64.base64url_encode(raw);
    if (reenc(b64.base64url_decode_strict(rawenc)) != rawenc) { return 5; }
    if (opt(b64.base64url_decode_strict(""), "X") != "") { return 6; }
    if (!isnone(b64.base64url_decode_strict("SGVsbG8+"))) { return 7; }
    if (!isnone(b64.base64url_decode_strict("SGVs/bG8"))) { return 8; }
    if (!isnone(b64.base64url_decode_strict("SG!sbG8"))) { return 9; }
    if (!isnone(b64.base64url_decode_strict("SGVsbG8=="))) { return 10; }
    if (!isnone(b64.base64url_decode_strict("A"))) { return 11; }
    return 42;
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
