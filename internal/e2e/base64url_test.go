package e2e

import "testing"

// Differential coverage for std/base64's URL-safe variant
// (base64url_encode / base64url_decode) across backends. Returns 42 iff
// the url-safe alphabet is used (no + / =), padding is dropped on encode
// but tolerated on decode, and encode/decode round-trips arbitrary
// bytes. Each leg skips itself when its toolchain is absent.
const base64urlProg = `
import "std/base64" as b64;
import "std/string";
function main(): i32 {
    var enc: string = b64.base64url_encode("Hello, World!");
    if (enc.contains("+") || enc.contains("/") || enc.contains("=")) { return 1; }
    if (b64.base64url_decode(enc) != "Hello, World!") { return 2; }
    var s2: string = string_from_bytes_unchecked([255 as u8, 255 as u8, 255 as u8]);
    if (b64.base64url_encode(s2) != "____") { return 3; }
    if (b64.base64url_decode("____") != s2) { return 4; }
    if (b64.base64url_encode("Hi") != "SGk") { return 5; }
    if (b64.base64url_decode("SGk") != "Hi" || b64.base64url_decode("SGk=") != "Hi") { return 6; }
    if (b64.base64url_encode("") != "" || b64.base64url_decode("") != "") { return 7; }
    return 42;
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
