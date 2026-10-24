package e2ecompiler

import (
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

func TestSelfHostFetchAccumulatorStaysLinearX86_64(t *testing.T) {
	gcc, runner, driverBin := buildModloadDriverX86(t)
	const bodyLen = 1 << 20
	port := e2eharness.StartBodyUpstream(t, e2eharness.AlphabetBody(bodyLen))
	asm, progDir := compileSourceModload(t, runner, driverBin, e2eharness.FetchAccumulatorSource(port, bodyLen))
	cmd := binCmd(runner, buildBin(t, gcc, progDir, "fetchlinear", asm))
	_ = cmd.Run()
	if code := cmd.ProcessState.ExitCode(); code != 42 {
		t.Fatalf("accumulator shape exit = %d, want 42", code)
	}
}
