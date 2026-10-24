package e2e

import "testing"

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
