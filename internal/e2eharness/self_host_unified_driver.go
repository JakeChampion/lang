// Package e2eharness holds the shared e2e test harness — driver builds,
// tooling discovery, caches — used by both internal/e2e and
// internal/e2eselfhost (#4398 part 3). Extracted verbatim from
// internal/e2e/self_host_unified_driver_test.go.
package e2eharness

import (
	"fmt"
	"strings"
	"testing"
)

// RunDriverStdinExits runs a self-host driver binary with `src` on stdin and
// returns an error only if the process failed to exit normally (a crash, as
// opposed to choosing any exit status). Used by the cache warmers as a smoke
// check that a freshly-compiled driver actually runs.
func RunDriverStdinExits(runner []string, bin, src string) error {
	cmd := RunX86_64Bin(runner, bin)
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
	cmd := RunX86_64Bin(runner, bin)
	cmd.Stdin = strings.NewReader(src)
	_ = cmd.Run()
	if cmd.ProcessState == nil || !cmd.ProcessState.Exited() {
		t.Fatalf("elig driver did not exit normally")
	}
	return cmd.ProcessState.ExitCode()
}

// EligBits builds the lean IR-eligibility probe driver
// (examples/self_host/asm_ir_elig_run.fern — front-end + asm_ir only, so its
// binary is small and link-caches cheaply), runs it over each program, and sums
// weight[i] for every program asm_ir.all_eligible accepts (exit 1). It is the
// shared core of the six IR-eligibility probe tests, which previously each
// compiled a bespoke self-host probe binary. The compile + link are content-
// addressed (cachedSelfHostAsm / CachedLink), so the driver builds at most once
// per shard and is served from the warmed disk cache when present. Building via
// CachedLink (not BuildSelfHostBin) keeps the binary out of the shared source
// tree.
func EligBits(t *testing.T, progs []string, weights []int) int {
	t.Helper()
	gcc, runner := X86_64Tooling(t)
	bin := CachedDriverBin(t, gcc, "../../examples/self_host", "asm_ir_elig_run.fern")
	got := 0
	for i, p := range progs {
		if runStdinExit(t, runner, bin, p) == 1 {
			got += weights[i]
		}
	}
	return got
}
