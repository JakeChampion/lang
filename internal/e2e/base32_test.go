package e2e

import "testing"

// Differential coverage for std/base32 across backends: the RFC 4648
// §10 encode vectors (every padding length), decode, and an
// arbitrary-byte round-trip. Returns 42 iff every check holds. Each leg
// skips itself when its toolchain is absent.
const base32Prog = `
import "std/base32" as b32;
function main(): i32 {
    if (b32.base32_encode("f") != "MY======") { return 1; }
    if (b32.base32_encode("fo") != "MZXQ====") { return 2; }
    if (b32.base32_encode("foo") != "MZXW6===") { return 3; }
    if (b32.base32_encode("foob") != "MZXW6YQ=") { return 4; }
    if (b32.base32_encode("fooba") != "MZXW6YTB") { return 5; }
    if (b32.base32_encode("foobar") != "MZXW6YTBOI======") { return 6; }
    if (string_from_bytes_unchecked(b32.base32_decode("MZXW6YTBOI======")) != "foobar") { return 7; }
    if (string_from_bytes_unchecked(b32.base32_decode("")) != "" || b32.base32_encode("") != "") { return 8; }
    var raw: string = string_from_bytes_unchecked([0 as u8, 255 as u8, 128 as u8, 1 as u8, 254 as u8]);
    if (string_from_bytes_unchecked(b32.base32_decode(b32.base32_encode(raw))) != raw) { return 9; }
    return 42;
}
`

func TestBase32Interp(t *testing.T) {
	if got := runInterpExit(t, base32Prog); got != 42 {
		t.Fatalf("interp got %d, want 42", got)
	}
}

func TestBase32X86_64(t *testing.T) {
	if _, got := compileAndRunX86_64(t, base32Prog); got != 42 {
		t.Fatalf("x86-64 got %d, want 42", got)
	}
}

func TestBase32Wasm(t *testing.T) {
	if got := compileAndRunWasmbinMain(t, base32Prog); got != 42 {
		t.Fatalf("wasm got %d, want 42", got)
	}
}

func TestBase32Arm64(t *testing.T) {
	if _, got := compileAndRunArm64(t, base32Prog); got != 42 {
		t.Fatalf("arm64 got %d, want 42", got)
	}
}
