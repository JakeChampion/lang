package e2ecompiler

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A `?` inside a `defer` action is refused by the checker as E079, so no
// CHECKED pipeline ever hands one to the lowering. asm_modload_run is not a
// checked pipeline — it parses, bundles and lowers — and the conformance sweep
// (TestSelfHostIRVerifyProvidedCorpusClean) feeds it every fixture in the
// corpus, diag_e079 among them.
//
// Lowering that shape used to recurse without end and take the driver down
// with a SIGSEGV, which is how #9470 presented before E079 was written: the `?`
// failure edge replays the deferred actions, and the action being replayed
// carries the `?` that replays them again. A segfault also loses the sweep's
// verdict entirely — the driver never reaches its tally — so the fixture read
// as a harness fault rather than as the one shape the language refuses.
//
// The lowering bails instead. That is what keeps the pass total on input the
// checker did not get to see, and the refusal names the construct, so a reader
// gets E079's answer rather than a stack trace.
func TestSelfHostDeferTryOpBailsRatherThanRecursing(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	shDir := writeSelfHostModloadProject(t)
	driverBin := buildSelfHostBin(t, gcc, shDir, "drivers/asm_modload_run.fern", "defer_try_bail_driver")

	src := `function g(v: i32): Option[i32] {
    if (v < 100) { return Some(v + 1); }
    return None;
}
function build(): Option[i32] {
    let n: i32 = 0;
    defer n = g(n)?;
    return Some(n);
}
function main(): i32 { build(); return 0; }
`
	entry := filepath.Join(t.TempDir(), "defer_try.fern")
	if err := os.WriteFile(entry, []byte(src), 0o644); err != nil {
		t.Fatalf("write entry: %v", err)
	}

	// The lowering replays the deferred actions on a `?` failure edge, and
	// refuses the shape by name, on both the per-module and the merged route.
	for _, args := range [][]string{{"-per-module-emit", "0"}, nil} {
		_, stderr, code := runDriver(t, runner, driverBin, nil, true, append([]string{entry}, args...)...)
		if code != 3 {
			t.Fatalf("%v: exited %d, want 3 (a refusal) — a signal here is the recursion back\n%s", args, code, stderr)
		}
		if !strings.Contains(stderr, "build: `?` inside a defer action (E079)") {
			t.Errorf("%v: refusal does not name build and the reason\n%s", args, stderr)
		}
	}
}
