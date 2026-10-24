package e2e

import (
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

func TestSimReactorInterp(t *testing.T) {
	if out, got := runInterpExitCode(t, e2eharness.SimReactorProbe()); got != 42 {
		t.Fatalf("interp got %d, want 42; first failing check: %s", got, out)
	}
}

func TestSimReactorX86_64(t *testing.T) {
	if out, got := compileAndRunX86_64(t, e2eharness.SimReactorProbe()); got != 42 {
		t.Fatalf("x86-64 got %d, want 42; first failing check: %s", got, out)
	}
}
