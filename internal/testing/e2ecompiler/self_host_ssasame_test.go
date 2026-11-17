package e2ecompiler

import (
	"os/exec"
	"testing"
)

// ssasame.decided on graphs made by hand: a test below one side of the same
// test, with each pair of `eq` and `ne`, a test of another width, a reuse
// allocation on the zero side, and a join that only tests again.
func TestSelfHostSSASameTests(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	const driver = "drivers/ssasame_run.fern"
	copySelfHostDriver(t, dir, driver)
	bin := buildSelfHostBin(t, gcc, dir, driver, "ssasame")
	cmd := exec.Command(bin)
	if len(runner) != 0 {
		cmd = exec.Command(runner[0], append(runner[1:], bin)...)
	}
	out, err := cmd.CombinedOutput()
	if err != nil || string(out) != "failures=0\n" {
		t.Fatalf("SSA same tests: %v\n%s", err, out)
	}
}
