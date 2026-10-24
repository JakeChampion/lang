package e2e

import (
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

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
