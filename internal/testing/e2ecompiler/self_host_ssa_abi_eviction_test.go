package e2ecompiler

import (
	"os/exec"
	"testing"
)

func TestSelfHostSSAABIEviction(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	const driver = "drivers/ssa_abi_eviction_run.fern"
	copySelfHostDriver(t, dir, driver)
	bin := buildSelfHostBin(t, gcc, dir, driver, "ssa_abi_eviction")
	cmd := exec.Command(bin)
	if len(runner) != 0 {
		cmd = exec.Command(runner[0], append(runner[1:], bin)...)
	}
	out, err := cmd.CombinedOutput()
	if err != nil || string(out) != "failures=0\n" {
		t.Fatalf("SSA ABI eviction: %v\n%s", err, out)
	}
}
