package e2e

import "testing"

// Differential coverage for std/base64's URL-safe variant
// (encode_url / decode_url) across backends. Returns 42 iff
// the url-safe alphabet is used (no + / =), padding is dropped on encode
// but tolerated on decode, and encode/decode round-trips arbitrary
// bytes. Each leg skips itself when its toolchain is absent.
const base64urlProg = `
import "std/base64" as b64;
import "std/codec";
import "std/string";
function main(): i32 {
    let enc: string = b64.encode_url("Hello, World!".bytes());
    if (enc.contains("+") || enc.contains("/") || enc.contains("=")) { return 1; }
    if (string_from_bytes_unchecked(__codec_bytes(b64.decode_url(enc))) != "Hello, World!") { return 2; }
    // 0xFFFFFF is not valid UTF-8 — since #5730 it stays u8[] on both
    // sides and never has to pose as a string.
    let s2: u8[] = [255 as u8, 255 as u8, 255 as u8];
    if (b64.encode_url(s2) != "____") { return 3; }
    if (b64.encode_url(__codec_bytes(b64.decode_url("____"))) != "____") { return 4; }
    if (b64.encode_url("Hi".bytes()) != "SGk") { return 5; }
    if (string_from_bytes_unchecked(__codec_bytes(b64.decode_url("SGk"))) != "Hi" || string_from_bytes_unchecked(__codec_bytes(b64.decode_url("SGk="))) != "Hi") { return 6; }
    if (b64.encode_url("".bytes()) != "" || string_from_bytes_unchecked(__codec_bytes(b64.decode_url(""))) != "") { return 7; }
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

func TestBase64urlInterp(t *testing.T) {
	if got := runInterpExit(t, base64urlProg); got != 42 {
		t.Fatalf("interp got %d, want 42", got)
	}
}

func TestBase64urlX86_64(t *testing.T) {
	if _, got := compileAndRunX86_64(t, base64urlProg); got != 42 {
		t.Fatalf("x86-64 got %d, want 42", got)
	}
}

func TestBase64urlWasm(t *testing.T) {
	if got := compileAndRunWasmbinMain(t, base64urlProg); got != 42 {
		t.Fatalf("wasm got %d, want 42", got)
	}
}

func TestBase64urlArm64(t *testing.T) {
	if _, got := compileAndRunArm64(t, base64urlProg); got != 42 {
		t.Fatalf("arm64 got %d, want 42", got)
	}
}
