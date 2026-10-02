package e2eselfhost

import (
	"testing"

	"github.com/jakechampion/lang/internal/e2eharness"
)

// The self-host twin of TestFetchClient: std/fetch's client against the
// scripted loopback origin, compiled by the self-host compiler.
func TestSelfHostFetchClient(t *testing.T) {
	gcc, runner, driverBin := buildModloadDriverX86(t)
	up := e2eharness.StartFetchUpstream(t)
	closed := e2eharness.ClosedLoopbackPort(t)
	asm, progDir := compileSourceModload(t, runner, driverBin, e2eharness.FetchClientSource(up.Port, closed))
	bin := buildBin(t, gcc, progDir, "fetchclient", asm)
	cmd := binCmd(runner, bin)
	out, _ := cmd.Output()
	e2eharness.CheckFetchClient(t, up, string(out), cmd.ProcessState.ExitCode())
}
