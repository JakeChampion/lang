package e2e

import "testing"

// std/crypto (#2681) — SHA-256 + HMAC-SHA256 known-answer vectors compiled
// through the native AOT backends (interp has its own test,
// TestInterpScriptCrypto). All four backends pass: the multi-vector program
// makes repeated allocation-heavy calls, which earlier mis-hashed on arm64
// until std/crypto stopped assuming __alloc_u8 returns zeroed memory and began
// zeroing the SHA padding buffers explicitly.

func TestX86_64Crypto(t *testing.T) {
	if _, code := compileAndRunX86_64(t, cryptoVectorsProgram); code != 0 {
		t.Errorf("x86-64 crypto: exit = %d, want 0 (failed vector)", code)
	}
}

func TestArm64Crypto(t *testing.T) {
	if _, code := compileAndRunArm64(t, cryptoVectorsProgram); code != 0 {
		t.Errorf("arm64 crypto: exit = %d, want 0 (failed vector)", code)
	}
}

func TestWASMCrypto(t *testing.T) {
	if code := runWasm(t, cryptoVectorsProgram); code != 0 {
		t.Errorf("wasm crypto: exit = %d, want 0 (failed vector)", code)
	}
}
