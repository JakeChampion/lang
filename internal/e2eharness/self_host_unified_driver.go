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

// runStdinVerdict runs a self-host probe driver with `src` on stdin and returns
// its trimmed stdout, failing the test if it did not exit 0.
func runStdinVerdict(t *testing.T, runner []string, bin, src string) string {
	t.Helper()
	cmd := RunX86_64Bin(runner, bin)
	cmd.Stdin = strings.NewReader(src)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("path probe driver failed: %v", err)
	}
	return strings.TrimSpace(string(out))
}

// EligBits runs the path probe (compiler/drivers/asm_pathprobe_run.fern,
// semlower.verdict) over each program and sums weight[i] for every program the
// typed lowering produces whole ("ir"). It is the shared core of the six
// verdict-bitmask tests. The compile + link are content-addressed
// (cachedSelfHostAsm / CachedLink), so the driver builds at most once per shard
// and is served from the disk cache when present. Building via CachedLink (not
// BuildSelfHostBin) keeps the binary out of the shared source tree.
func EligBits(t *testing.T, progs []string, weights []int) int {
	t.Helper()
	gcc, runner := X86_64Tooling(t)
	bin := CachedDriverBin(t, gcc, "../../compiler", "drivers/asm_pathprobe_run.fern")
	got := 0
	for i, p := range progs {
		if runStdinVerdict(t, runner, bin, p) == "ir" {
			got += weights[i]
		}
	}
	return got
}
