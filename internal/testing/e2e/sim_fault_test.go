package e2e

import (
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

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
