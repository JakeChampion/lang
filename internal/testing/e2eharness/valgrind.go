package e2eharness

import (
	"os/exec"
	"testing"

	"github.com/jakechampion/lang/internal/envflag"
)

func Valgrind(t testing.TB) string {
	t.Helper()
	p, err := exec.LookPath("valgrind")
	if err == nil {
		return p
	}
	if envflag.On("FERN_REQUIRE_VALGRIND") {
		t.Fatal("valgrind not on PATH and FERN_REQUIRE_VALGRIND=1: this lane installs it, so a skip here covers nothing")
	}
	t.Skip("valgrind not on PATH; skipping the memcheck gate")
	return ""
}
