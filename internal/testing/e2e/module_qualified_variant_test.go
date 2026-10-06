package e2e

import (
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

// `mod.Variant` constructs and matches an imported enum's variants
// (#10430) on every backend, including the two std/net variants that
// share their names with IoError's.
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
