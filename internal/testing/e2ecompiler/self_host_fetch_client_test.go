package e2ecompiler

import (
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

func TestSelfHostFetchClient(t *testing.T) {
	gcc, runner, driverBin := buildModloadDriverX86(t)
	up := e2eharness.StartFetchUpstream(t)
	e2eharness.SetFetchProxy(t, up)
	closed := e2eharness.ClosedLoopbackPort(t)
	asm, progDir := compileSourceModload(t, runner, driverBin, e2eharness.FetchClientSource(up.Port, closed))
	bin := buildBin(t, gcc, progDir, "fetchclient", asm)
	cmd := binCmd(runner, bin)
	out, _ := cmd.Output()
	e2eharness.CheckFetchClient(t, up, string(out), cmd.ProcessState.ExitCode())
}
