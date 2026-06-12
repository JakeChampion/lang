package e2e

import "testing"

// std/crypto (#2681) — SHA-256 + HMAC-SHA256 known-answer vectors compiled
// through the native AOT backends. interp has its own test
// (TestInterpScriptCrypto); arm64 is intentionally omitted pending the #2768
// freelist-corruption bug (single calls are correct, but the multi-vector
// program trips the 2nd-allocation-heavy-call corruption).

func TestX86_64Crypto(t *testing.T) {
	if _, code := compileAndRunX86_64(t, cryptoVectorsProgram); code != 0 {
		t.Errorf("x86-64 crypto: exit = %d, want 0 (failed vector)", code)
	}
}

func TestWASMCrypto(t *testing.T) {
	if code := runWasm(t, cryptoVectorsProgram); code != 0 {
		t.Errorf("wasm crypto: exit = %d, want 0 (failed vector)", code)
	}
}
