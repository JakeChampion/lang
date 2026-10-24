package e2ecompiler

import (
	"os/exec"
	"testing"
)

// The cold-call classification and the allocation it allows: a call that only
// some loop iterations make, a push, an inline rc primitive, the interior
// fixed-register table, and a hot phi that crosses only cold calls keeping a
// caller register (docs/SELFHOST-SSA-BACKEND.md, "Cold calls").
func TestSelfHostSSAColdCalls(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	const driver = "drivers/ssa_cold_calls_run.fern"
	copySelfHostDriver(t, dir, driver)
	bin := buildSelfHostBin(t, gcc, dir, driver, "ssa_cold_calls")
	cmd := exec.Command(bin)
	if len(runner) != 0 {
		cmd = exec.Command(runner[0], append(runner[1:], bin)...)
	}
	out, err := cmd.CombinedOutput()
	if err != nil || string(out) != "failures=0\n" {
		t.Fatalf("SSA cold calls: %v\n%s", err, out)
	}
}
