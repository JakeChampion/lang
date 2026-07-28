package e2e

import "testing"

// Differential coverage for std/crypto's HKDF-SHA256 (RFC 5869) across
// backends. Checks Test Case 1 (13-byte salt, 10-byte info, L=42), the
// extract PRK, and Test Case 3 (zero-length salt/info). Returns 42 iff
// every vector matches. Each leg skips itself when its toolchain is
// absent.
const hkdfProg = `
import "std/crypto" as crypto;
import "std/hex" as hex;
function rep(b: i32, n: i32): u8[] {
    var a: u8[] = [];
    var i: i32 = 0;
    while (i < n) { a = a.append(b as u8); i = i + 1; }
    return a;
}
function main(): i32 {
    var ikm: u8[] = rep(11, 22);
    var salt: u8[] = [0 as u8,1 as u8,2 as u8,3 as u8,4 as u8,5 as u8,6 as u8,7 as u8,8 as u8,9 as u8,10 as u8,11 as u8,12 as u8];
    var info: u8[] = [240 as u8,241 as u8,242 as u8,243 as u8,244 as u8,245 as u8,246 as u8,247 as u8,248 as u8,249 as u8];
    if (hex.hex_encode(crypto.hkdf_extract(salt, ikm)) !=
        "077709362c2e32df0ddc3f0dc47bba6390b6c73bb50f9c3122ec844ad7c2b3e5") { return 1; }
    if (crypto.hkdf_sha256_hex(salt, ikm, info, 42) !=
        "3cb25f25faacd57a90434f64d0362f2a2d2d0a90cf1a5a4c5db02d56ecc4c5bf34007208d5b887185865") { return 2; }
    var e: u8[] = [];
    if (crypto.hkdf_sha256_hex(e, ikm, e, 42) !=
        "8da4e775a563c18f715f802a063c5a31b8a11f5c5ee1879ec3454e5f3c738d2d9d201395faa4b61a96c8") { return 3; }
    return 42;
}
`

func TestHkdfInterp(t *testing.T) {
	if got := runInterpExit(t, hkdfProg); got != 42 {
		t.Fatalf("interp got %d, want 42", got)
	}
}

func TestHkdfX86_64(t *testing.T) {
	if _, got := compileAndRunX86_64(t, hkdfProg); got != 42 {
		t.Fatalf("x86-64 got %d, want 42", got)
	}
}

func TestHkdfWasm(t *testing.T) {
	if got := compileAndRunWasmbinMain(t, hkdfProg); got != 42 {
		t.Fatalf("wasm got %d, want 42", got)
	}
}

func TestHkdfArm64(t *testing.T) {
	if _, got := compileAndRunArm64(t, hkdfProg); got != 42 {
		t.Fatalf("arm64 got %d, want 42", got)
	}
}
