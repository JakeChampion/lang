package e2ecompiler

import (
	"os/exec"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

// The wasm leg of the chacha20poly1305 suite, which the stdtest differential
// runs on x86-64 and arm64: the component's TAP output must match the
// interpreter's byte for byte.
func TestSelfHostChaCha20Poly1305Wasm(t *testing.T) {
	wasmtime := e2eharness.Wasmtime(t)
	cli := buildSelfHostCLI(t)
	src := langSrcAbs(t, "tests/stdlib/chacha20poly1305_test.fern")
	want, err := exec.Command(buildLangBinForInterp(t), "-interp", src).Output()
	if err != nil {
		t.Fatalf("interpreter: %v\n%s", err, want)
	}
	got, err := exec.Command(wasmtime, "run", cli.wasmComponent(t, src, "FERN_STRICT_IR=1")).Output()
	if err != nil {
		t.Fatalf("wasmtime run: %v\n%s", err, got)
	}
	if string(got) != string(want) {
		t.Errorf("TAP output mismatch:\n--- wasm ---\n%s\n--- interp ---\n%s", got, want)
	}
}
