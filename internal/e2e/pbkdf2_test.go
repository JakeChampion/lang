package e2e

import "testing"

// Differential coverage for std/crypto's PBKDF2-HMAC-SHA256 against the
// standard known-answer vectors, including the c=4096 vectors and a
// 40-byte multi-block derivation that are too slow for the interpreter
// oracle (so they run only on the compiled backends here). Returns 42
// iff every vector matches. Each leg skips itself when its toolchain is
// absent.
const pbkdf2Prog = `
import "std/crypto" as crypto;
import "std/string";
function main(): i32 {
    if (crypto.pbkdf2_sha256_hex("password", "salt".bytes(), 1, 32) !=
        "120fb6cffcf8b32c43e7225256c4f837a86548c92ccc35480805987cb70be17b") { return 1; }
    if (crypto.pbkdf2_sha256_hex("password", "salt".bytes(), 2, 32) !=
        "ae4d0c95af6b46d32d0adff928f06dd02a303f8ef3c251dfd6e2d85a95474c43") { return 2; }
    if (crypto.pbkdf2_sha256_hex("password", "salt".bytes(), 4096, 32) !=
        "c5e478d59288c841aa530db6845c4c8d962893a001ce4e11a4963873aa98134a") { return 3; }
    if (crypto.pbkdf2_sha256_hex("passwordPASSWORDpassword",
        "saltSALTsaltSALTsaltSALTsaltSALTsalt".bytes(), 4096, 40) !=
        "348c89dbcbd32b2f32d814b8116e84cf2b17347ebc1800181c4e2a1fb8dd53e1c635518c7dac47e9") { return 4; }
    // constant-time verify against a stored key (accept + reject).
    if (!crypto.pbkdf2_verify_hex("password", "salt".bytes(), 4096,
        "c5e478d59288c841aa530db6845c4c8d962893a001ce4e11a4963873aa98134a")) { return 5; }
    if (crypto.pbkdf2_verify_hex("wrong", "salt".bytes(), 4096,
        "c5e478d59288c841aa530db6845c4c8d962893a001ce4e11a4963873aa98134a")) { return 6; }
    return 42;
}
`

func TestPbkdf2X86_64(t *testing.T) {
	if _, got := compileAndRunX86_64(t, pbkdf2Prog); got != 42 {
		t.Fatalf("x86-64 got %d, want 42", got)
	}
}

func TestPbkdf2Wasm(t *testing.T) {
	if got := compileAndRunWasmbinMain(t, pbkdf2Prog); got != 42 {
		t.Fatalf("wasm got %d, want 42", got)
	}
}

func TestPbkdf2Arm64(t *testing.T) {
	if _, got := compileAndRunArm64(t, pbkdf2Prog); got != 42 {
		t.Fatalf("arm64 got %d, want 42", got)
	}
}
