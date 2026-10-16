package e2ecompiler

import (
	"os/exec"
	"testing"
)

// A frequently read long interval should survive competition from a short
// value, while a less frequently read one should still be evicted.
func TestSelfHostSSASpillCost(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	const driver = "drivers/ssa_spill_cost_run.fern"
	copySelfHostDriver(t, dir, driver)
	bin := buildSelfHostBin(t, gcc, dir, driver, "ssa_spill_cost")
	cmd := exec.Command(bin)
	if len(runner) != 0 {
		cmd = exec.Command(runner[0], append(runner[1:], bin)...)
	}
	out, err := cmd.CombinedOutput()
	if err != nil || string(out) != "failures=0\n" {
		t.Fatalf("SSA spill cost: %v\n%s", err, out)
	}
}
