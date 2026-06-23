package e2e

import (
	"fmt"
	"os/exec"
	"strings"
	"testing"
)

// runDriverStdinExits runs a self-host driver binary with `src` on stdin and
// returns an error only if the process failed to exit normally (a crash, as
// opposed to choosing any exit status). Used by the cache warmers as a smoke
// check that a freshly-compiled driver actually runs.
func runDriverStdinExits(runner []string, bin, src string) error {
	var cmd *exec.Cmd
	if len(runner) == 0 {
		cmd = exec.Command(bin)
	} else {
		cmd = exec.Command(runner[0], append(append([]string{}, runner[1:]...), bin)...)
	}
	cmd.Stdin = strings.NewReader(src)
	_ = cmd.Run()
	if cmd.ProcessState == nil || !cmd.ProcessState.Exited() {
		return fmt.Errorf("driver did not exit normally")
	}
	return nil
}

// runStdinExit runs a self-host driver with `src` on stdin and returns its exit
// code, failing the test if it did not exit normally.
func runStdinExit(t *testing.T, runner []string, bin, src string) int {
	t.Helper()
	var cmd *exec.Cmd
	if len(runner) == 0 {
		cmd = exec.Command(bin)
	} else {
		cmd = exec.Command(runner[0], append(append([]string{}, runner[1:]...), bin)...)
	}
	cmd.Stdin = strings.NewReader(src)
	_ = cmd.Run()
	if cmd.ProcessState == nil || !cmd.ProcessState.Exited() {
		t.Fatalf("elig driver did not exit normally")
	}
	return cmd.ProcessState.ExitCode()
}

// eligBits builds the lean IR-eligibility probe driver
// (examples/self_host/asm_ir_elig_run.fern — front-end + asm_ir only, so its
// binary is small and link-caches cheaply), runs it over each program, and sums
// weight[i] for every program asm_ir.all_eligible accepts (exit 1). It is the
// shared core of the six IR-eligibility probe tests, which previously each
// compiled a bespoke self-host probe binary. The compile + link are content-
// addressed (cachedSelfHostAsm / cachedLink), so the driver builds at most once
// per shard and is served from the warmed disk cache when present. Building via
// cachedLink (not buildSelfHostBin) keeps the binary out of the shared source
// tree.
func eligBits(t *testing.T, progs []string, weights []int) int {
	t.Helper()
	gcc, runner := x86_64Tooling(t)
	bin := cachedDriverBin(t, gcc, "../../examples/self_host", "asm_ir_elig_run.fern")
	got := 0
	for i, p := range progs {
		if runStdinExit(t, runner, bin, p) == 1 {
			got += weights[i]
		}
	}
	return got
}
