package e2e

import "testing"

// Differential coverage for std/crypto's HOTP/TOTP (RFC 4226 / RFC 6238,
// SHA-256 mode) across backends, against the RFC 6238 Appendix B
// vectors (key = 32 ASCII bytes, 8 digits, 30s step). Also checks the
// i64 time-step counter (T = 2e10 exercises the wide counter) and the
// digit truncation. Returns 42 iff every vector matches. Each leg skips
// itself when its toolchain is absent.
const totpProg = `
import "std/crypto" as crypto;
import "std/string";
function main(): i32 {
    var key: u8[] = "12345678901234567890123456789012".bytes();
    if (crypto.totp_sha256(key, 59, 30, 8) != 46119246) { return 1; }
    if (crypto.totp_sha256(key, 1111111109, 30, 8) != 68084774) { return 2; }
    if (crypto.totp_sha256(key, 1111111111, 30, 8) != 67062674) { return 3; }
    if (crypto.totp_sha256(key, 1234567890, 30, 8) != 91819424) { return 4; }
    if (crypto.totp_sha256(key, 2000000000, 30, 8) != 90698825) { return 5; }
    if (crypto.totp_sha256(key, 20000000000, 30, 8) != 77737706) { return 6; }
    if (crypto.hotp_sha256(key, 1, 8) != 46119246) { return 7; }
    if (crypto.hotp_sha256(key, 1, 6) != 46119246 % 1000000) { return 8; }
    return 42;
}
`

func TestTotpInterp(t *testing.T) {
	if got := runInterpExit(t, totpProg); got != 42 {
		t.Fatalf("interp got %d, want 42", got)
	}
}

func TestTotpX86_64(t *testing.T) {
	if _, got := compileAndRunX86_64(t, totpProg); got != 42 {
		t.Fatalf("x86-64 got %d, want 42", got)
	}
}

func TestTotpWasm(t *testing.T) {
	if got := compileAndRunWasmbinMain(t, totpProg); got != 42 {
		t.Fatalf("wasm got %d, want 42", got)
	}
}

func TestTotpArm64(t *testing.T) {
	if _, got := compileAndRunArm64(t, totpProg); got != 42 {
		t.Fatalf("arm64 got %d, want 42", got)
	}
}
