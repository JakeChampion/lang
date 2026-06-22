package e2e

import (
	"os/exec"
	"strings"
	"testing"
)

// selfHostX86Driver builds the unified x86-family self-host driver
// (examples/self_host/selfhost_x86_run.fern) and returns the x86 runner plus
// the cached binary path. Every x86 self-host probe test that feeds a backend a
// program-under-test on stdin can share this ONE driver, so a CI shard pays the
// ~60s front-end compile a single time (the source→asm step is content-addressed
// and cached process-wide, then disk-cached when FERN_SELFHOST_BUILD_CACHE is
// set) instead of once per bespoke probe program. That per-test recompile of the
// whole ~35k-line front-end is what ran the x86 self-host shards long enough to
// be caught by hosted-runner preemption; collapsing the probes onto one driver
// drops a shard from minutes of redundant compiles to a single build.
func selfHostX86Driver(t *testing.T) (runner []string, bin string) {
	t.Helper()
	gcc, run := x86_64Tooling(t)
	asm := cachedSelfHostAsm(t, "../../examples/self_host", "selfhost_x86_run.fern")
	return run, cachedLink(t, gcc, asm)
}

// runUnifiedExit runs the unified driver in `mode`, feeding `src` on stdin, and
// returns the process exit code. Fails the test if the process did not exit
// normally (a crash, not a chosen exit status).
func runUnifiedExit(t *testing.T, runner []string, bin, mode, src string) int {
	t.Helper()
	var cmd *exec.Cmd
	if len(runner) == 0 {
		cmd = exec.Command(bin, mode)
	} else {
		cmd = exec.Command(runner[0], append(append([]string{}, runner[1:]...), bin, mode)...)
	}
	cmd.Stdin = strings.NewReader(src)
	_ = cmd.Run()
	if cmd.ProcessState == nil || !cmd.ProcessState.Exited() {
		t.Fatalf("unified driver (mode=%s) did not exit normally", mode)
	}
	return cmd.ProcessState.ExitCode()
}

// eligBits runs the unified driver's `elig` mode over each program and sums
// weight[i] for every program asm_ir.all_eligible accepts (exit 1). It is the
// shared core of the six IR-eligibility probe tests, which previously each
// compiled a bespoke self-host probe binary.
func eligBits(t *testing.T, progs []string, weights []int) int {
	t.Helper()
	runner, bin := selfHostX86Driver(t)
	got := 0
	for i, p := range progs {
		if runUnifiedExit(t, runner, bin, "elig", p) == 1 {
			got += weights[i]
		}
	}
	return got
}
