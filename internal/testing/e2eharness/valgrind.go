package e2eharness

import (
	"os"
	"os/exec"
	"testing"
)

// Valgrind returns valgrind's path, skipping the test when it is not on PATH.
// FERN_REQUIRE_VALGRIND=1 turns the skip into a failure, for a lane that
// installs valgrind — the mirror of FERN_REQUIRE_WASMTIME.
func Valgrind(t testing.TB) string {
	t.Helper()
	p, err := exec.LookPath("valgrind")
	if err == nil {
		return p
	}
	if os.Getenv("FERN_REQUIRE_VALGRIND") == "1" {
		t.Fatal("valgrind not on PATH and FERN_REQUIRE_VALGRIND=1: this lane installs it, so a skip here covers nothing")
	}
	t.Skip("valgrind not on PATH; skipping the memcheck gate")
	return ""
}
