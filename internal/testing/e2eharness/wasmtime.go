package e2eharness

import (
	"os/exec"
	"testing"

	"github.com/jakechampion/lang/internal/envflag"
)

func Wasmtime(t testing.TB) string {
	t.Helper()
	p, err := exec.LookPath("wasmtime")
	if err == nil {
		return p
	}
	if envflag.On("FERN_REQUIRE_WASMTIME") {
		t.Fatal("wasmtime not on PATH and FERN_REQUIRE_WASMTIME=1: this lane installs it, so a skip here covers nothing")
	}
	t.Skip("wasmtime not on PATH; skipping wasm e2e")
	return ""
}
