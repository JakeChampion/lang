package e2ecompiler

import (
	"os/exec"
	"testing"
)

// ssa.frame_split on graphs made by hand: a guard, a loop behind a guard, a
// join, a jump that the emitter passes on and a rotated test.
func TestSelfHostSSAFrameSplit(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	const driver = "drivers/ssa_frame_split_run.fern"
	copySelfHostDriver(t, dir, driver)
	bin := buildSelfHostBin(t, gcc, dir, driver, "ssa_frame_split")
	cmd := exec.Command(bin)
	if len(runner) != 0 {
		cmd = exec.Command(runner[0], append(runner[1:], bin)...)
	}
	out, err := cmd.CombinedOutput()
	if err != nil || string(out) != "failures=0\n" {
		t.Fatalf("SSA frame split: %v\n%s", err, out)
	}
}
