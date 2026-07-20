package e2e

import (
	"os"
	"path/filepath"
	"testing"
)

// TestExampleIterPipeline runs the committed examples/iter_pipeline.fern — a
// tour of the core/iter + core/cmp stdlib (iter.of / filter / map / sum +
// cmp.sort) — on the native interp / x86-64 / wasm backends and oracle-
// checks the exit code. End-to-end integration coverage that the shipped
// iterator/sort surface composes (each piece is unit-tested elsewhere; this
// pins them working together through a realistic pipeline). The example is also
// compiled for arm64 by the `examples-*` CI jobs.
func TestExampleIterPipeline(t *testing.T) {
	p, err := filepath.Abs("../../examples/iter_pipeline.fern")
	if err != nil {
		t.Fatalf("abs: %v", err)
	}
	if _, code := runFixtureInterp(t, p, ""); code != 148 {
		t.Errorf("iter_pipeline interp = %d, want 148", code)
	}
	if _, code := runFixtureX86_64(t, p, ""); code != 148 {
		t.Errorf("iter_pipeline x86-64 = %d, want 148", code)
	}
	srcBytes, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("read example: %v", err)
	}
	if code := runWasm(t, string(srcBytes)); code != 148 {
		t.Errorf("iter_pipeline wasm = %d, want 148", code)
	}
}

// TestExampleIterPipelineArm64 is the arm64 leg (CI-gated; qemu).
func TestExampleIterPipelineArm64(t *testing.T) {
	p, err := filepath.Abs("../../examples/iter_pipeline.fern")
	if err != nil {
		t.Fatalf("abs: %v", err)
	}
	if _, code := runFixtureArm64(t, p, ""); code != 148 {
		t.Errorf("iter_pipeline arm64 = %d, want 148", code)
	}
}
