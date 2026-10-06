package e2e

import (
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/e2eharness"
)

// SimNet (docs/DST-PLATFORM-BRIEF.md slice 2) is std/sim's scripted
// request/response layer — the sim sibling of fetch.fetch_future:
// endpoints registered on a sim.Net with per-endpoint body, first-byte
// latency, and a chunking schedule, fetched through futures that honour
// the real fetch contract (body on success, "" for a dead upstream,
// one re-suspension per chunk). These gates pin that handler fan-out
// logic is testable against scripted upstreams with EXACT virtual-time
// assertions, on every backend including the interpreter.

// `tests/stdlib/sim_net_test.fern` is the TAP suite: input-order
// gather over three latencies, race picking the fast endpoint,
// with_deadline dropping only the slow one, chunk accumulation +
// per-chunk re-suspension, the dead-upstream "" contract, and the
// per-endpoint hits counter / wildcard path. Passing → exit 0.
func TestRunnerSimNetExamplePasses(t *testing.T) {
	bin := buildLangBinForInterp(t)
	src := langSrcAbs(t, "tests/stdlib/sim_net_test.fern")
	code, out, errOut := runLangInterp(t, bin, src)
	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	for _, w := range []string{"# Suite: std/sim net", "# pass 8", "# fail 0", "1..8"} {
		if !strings.Contains(out, w) {
			t.Errorf("stdout missing %q\nfull output:\n%s", w, out)
		}
	}
}

func TestWASMSimNet(t *testing.T) {
	if code := runWasmResult(t, e2eharness.SimNetProgram); code != 42 {
		t.Errorf("wasm SimNet exit = %d, want 42 (failing check index)", code)
	}
}
