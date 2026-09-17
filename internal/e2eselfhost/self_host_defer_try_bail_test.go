package e2eselfhost

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
	driverBin := buildSelfHostBin(t, gcc, shDir, "asm_modload_run.fern", "defer_try_bail_driver")

	src := `function g(v: i32): Option[i32] {
    if (v < 100) { return Some(v + 1); }
    return None;
}
function build(): Option[i32] {
    var n: i32 = 0;
    defer n = g(n)?;
    return Some(n);
}
function main(): i32 { build(); return 0; }
`
	entry := filepath.Join(t.TempDir(), "defer_try.fern")
	if err := os.WriteFile(entry, []byte(src), 0o644); err != nil {
		t.Fatalf("write entry: %v", err)
	}

	// `-assume-eligible` is what makes the bail observable: it skips the
	// pre-check, so a function that does not lower reaches the emit and is
	// refused BY NAME with the reason the lowering recorded (#8590). Without
	// it the function is simply dropped and the driver says nothing about why.
	//
	// Both register targets run: the driver is one x86-64 binary and `-target`
	// only selects which emitter the refusal would otherwise have reached, so a
	// guard that fired on one and not the other would be a real divergence.
	for _, target := range []string{"x86-64-linux", "arm64-linux"} {
		out, stderr, code := runDriver(t, runner, driverBin, nil, true,
			entry, "-per-module-emit", "0", "-assume-eligible", "-target", target)
		if code != 3 {
			t.Fatalf("%s: exited %d, want 3 (a refusal) — a signal here is the recursion back\n%s", target, code, stderr)
		}
		if len(out) != 0 {
			t.Errorf("%s: emitted %d bytes for a function that did not lower", target, len(out))
		}
		if !strings.Contains(stderr, "build ") {
			t.Errorf("%s: refusal does not name the bailing function %q\n%s", target, "build", stderr)
		}
		if !strings.Contains(stderr, "`?` inside a defer action (E079)") {
			t.Errorf("%s: refusal does not carry the bail's reason\n%s", target, stderr)
		}
	}
}
