package e2e

import (
	"os"
	"path/filepath"
	"testing"
)

func TestExampleIterPipeline(t *testing.T) {
	p, err := filepath.Abs("../../../examples/iter_pipeline.fern")
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

func TestExampleIterPipelineArm64(t *testing.T) {
	p, err := filepath.Abs("../../../examples/iter_pipeline.fern")
	if err != nil {
		t.Fatalf("abs: %v", err)
	}
	if _, code := runFixtureArm64(t, p, ""); code != 148 {
		t.Errorf("iter_pipeline arm64 = %d, want 148", code)
	}
}
