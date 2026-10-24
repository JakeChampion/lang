package e2e

import (
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

func TestModuleQualifiedVariantInterp(t *testing.T) {
	if out, got := runInterpExitCode(t, e2eharness.ModuleQualifiedVariantProbe()); got != 42 {
		t.Fatalf("interp got %d, want 42; first failing check: %s", got, out)
	}
}

func TestModuleQualifiedVariantX86_64(t *testing.T) {
	if out, got := compileAndRunX86_64(t, e2eharness.ModuleQualifiedVariantProbe()); got != 42 {
		t.Fatalf("x86-64 got %d, want 42; first failing check: %s", got, out)
	}
}

func TestModuleQualifiedVariantWasm(t *testing.T) {
	if got := compileAndRunWasmbinMain(t, e2eharness.ModuleQualifiedVariantProbe()); got != 42 {
		t.Fatalf("wasm got %d, want 42", got)
	}
}

func TestModuleQualifiedVariantArm64(t *testing.T) {
	if out, got := compileAndRunArm64(t, e2eharness.ModuleQualifiedVariantProbe()); got != 42 {
		t.Fatalf("arm64 got %d, want 42; first failing check: %s", got, out)
	}
}
