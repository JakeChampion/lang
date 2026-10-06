package e2e

import (
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

// Fault injection (docs/DST-PLATFORM-BRIEF.md slice 3) is std/sim's
// seed-driven fault layer on SimNet endpoints: fail (the real fetch's
// immediate-"" connect failure), stall (never resolves — exercises
// with_deadline drops), partial(k) (the first k chunks arrive on
// schedule, then silence — never resolves), and flaky(p) (each fetch
// draws once from the sim PRNG; the endpoint's fault fires when the
// draw lands below p). Every fault outcome is a pure function of the
// seed and the call order, and sim.sweep_seeds(n, prop) is the replay
// workflow in miniature: run a property over n seeds, report the first
// failing one.

// `tests/stdlib/sim_fault_test.fern` is the TAP suite: the four
// fault modes through gather / with_deadline / a hand drain, the
// seed-1 flaky(50) golden pattern, same-seed reproducibility +
// different-seed divergence, and sweep_seeds on both its all-green and
// first-failing-seed paths. Passing → exit 0.
func TestRunnerSimFaultExamplePasses(t *testing.T) {
	bin := buildLangBinForInterp(t)
	src := langSrcAbs(t, "tests/stdlib/sim_fault_test.fern")
	code, out, errOut := runLangInterp(t, bin, src)
	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	for _, w := range []string{"# Suite: std/sim faults", "# pass 8", "# fail 0", "1..8"} {
		if !strings.Contains(out, w) {
			t.Errorf("stdout missing %q\nfull output:\n%s", w, out)
		}
	}
}

func TestWASMSimFault(t *testing.T) {
	if code := runWasmResult(t, e2eharness.SimFaultProgram); code != 42 {
		t.Errorf("wasm sim-fault exit = %d, want 42 (failing check index)", code)
	}
}
