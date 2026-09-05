package e2e

import (
	"testing"

	"github.com/jakechampion/lang/internal/e2eharness"
)

// The string builder past 64 MiB on the natives (#8212): the whole compiler's
// arm64 asm is 63.3 MB after the peephole and larger before it. These drive a
// build across that boundary and read bytes on both sides of it.

func TestArm64StrbufGrowsPastFixedCeiling(t *testing.T) {
	_, exit := compileAndRunArm64(t, e2eharness.StrbufCeilingProbe)
	if exit != 0 {
		t.Errorf("exit %d, want 0 (%s)", exit, e2eharness.StrbufCeilingProbeCodes)
	}
}

func TestX86_64StrbufGrowsPastFixedCeiling(t *testing.T) {
	_, exit := compileAndRunX86Native(t, e2eharness.StrbufCeilingProbe)
	if exit != 0 {
		t.Errorf("exit %d, want 0 (%s)", exit, e2eharness.StrbufCeilingProbeCodes)
	}
}
