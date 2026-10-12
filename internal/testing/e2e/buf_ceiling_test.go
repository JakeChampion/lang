package e2e

import (
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

// A builder past 64 MiB on the natives (#8212): the whole compiler's arm64 asm
// is 63.3 MB after the peephole and larger before it, and the emitter
// accumulates it in one. These drive a build across that boundary and read
// bytes on both sides of it.

func TestArm64BufGrowsPastFixedCeiling(t *testing.T) {
	_, exit := compileAndRunArm64(t, e2eharness.BufCeilingProbe)
	if exit != 0 {
		t.Errorf("exit %d, want 0 (%s)", exit, e2eharness.BufCeilingProbeCodes)
	}
}

func TestX86_64BufGrowsPastFixedCeiling(t *testing.T) {
	_, exit := compileAndRunX86_64(t, e2eharness.BufCeilingProbe)
	if exit != 0 {
		t.Errorf("exit %d, want 0 (%s)", exit, e2eharness.BufCeilingProbeCodes)
	}
}
