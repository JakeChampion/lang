package e2e

import (
	"testing"

	"github.com/jakechampion/lang/internal/e2eharness"
)

// The string builder past 64 MiB on the natives. The buffer was a fixed
// 64 MiB .bss reservation with no bounds check, so a build that crossed it
// wrote over the .bss words after it and the program died later with no
// diagnostic (#8212: the whole compiler's arm64 asm, emitted by the self-host
// compiler, is 63.3 MB after the peephole and larger before it). It is a heap
// block that doubles now; these drive a build past the old ceiling and read
// bytes on both sides of it.

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
