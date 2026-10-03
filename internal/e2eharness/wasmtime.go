package e2eharness

import (
	"os"
	"os/exec"
	"testing"
)

// Wasmtime returns wasmtime's path, skipping the test when it is not on PATH.
// FERN_REQUIRE_WASMTIME=1 turns the skip into a failure, for a lane that
// installs wasmtime — the mirror of FERN_REQUIRE_X86_64_TOOLING.
func Wasmtime(t testing.TB) string {
	t.Helper()
	p, err := exec.LookPath("wasmtime")
	if err == nil {
		return p
	}
	if os.Getenv("FERN_REQUIRE_WASMTIME") == "1" {
		t.Fatal("wasmtime not on PATH and FERN_REQUIRE_WASMTIME=1: this lane installs it, so a skip here covers nothing")
	}
	t.Skip("wasmtime not on PATH; skipping wasm e2e")
	return ""
}
