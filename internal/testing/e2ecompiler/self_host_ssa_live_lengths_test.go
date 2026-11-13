package e2ecompiler

import (
	"os/exec"
	"testing"
)

// TestSelfHostSSALiveLengths runs ssa_live_lengths_run.fern: the live length
// of each value, and the spill choice it gives to a value that is dead in a
// loop its interval covers.
func TestSelfHostSSALiveLengths(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	const driver = "drivers/ssa_live_lengths_run.fern"
	copySelfHostDriver(t, dir, driver)
	bin := buildSelfHostBin(t, gcc, dir, driver, "ssa_live_lengths")
	cmd := exec.Command(bin)
	if len(runner) != 0 {
		cmd = exec.Command(runner[0], append(runner[1:], bin)...)
	}
	out, err := cmd.CombinedOutput()
	if err != nil || string(out) != "failures=0\n" {
		t.Fatalf("SSA live lengths: %v\n%s", err, out)
	}
}
