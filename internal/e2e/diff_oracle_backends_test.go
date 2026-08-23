package e2e

import (
	"os"
	"os/exec"
	"sort"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/jakechampion/lang/internal/e2eharness"
)

// Which oracle legs this host can actually run, decided once before any seed.
//
// Each leg skips itself when its toolchain is missing, and Go marks a parent
// PASS when every child SKIPs — so the oracle could compare a generated
// program against nothing at all and still exit 0. Measured on a stripped
// PATH before #7310 closed it: 256 seeds PASS, 768 backend legs skipped, 0
// legs executed, exit status 0 — indistinguishable in the log from a real
// run.
//
// Two rules close it. Neither can be satisfied by skipping.
//
//   - At least one leg must be runnable. An oracle with none compares the
//     interpreter against nothing; there is no host where that is a
//     legitimate result.
//   - FERN_REQUIRE_DIFF_BACKENDS names the legs a lane is meant to have, and
//     a missing one fails instead of skipping. Same shape as
//     FERN_REQUIRE_CROSS_BACKENDS and FERN_REQUIRE_ARM64_SSA_DIFF, which
//     exist for the same reason.
//
// The per-arch CI matrix is deliberate — the x86 runner exercises x86-64 and
// wasm while arm64 skips, the aarch64 runner the reverse — so "all three
// everywhere" is the wrong requirement and the env var names each lane's own
// expectation instead.
type diffOracleLeg struct {
	name      string
	available func() bool
}

var diffOracleLegs = []diffOracleLeg{
	{name: "arm64-linux", available: func() bool {
		_, _, ok := e2eharness.LookupArm64Tooling()
		return ok
	}},
	{name: "x86_64", available: func() bool {
		_, _, ok := e2eharness.LookupX86_64Tooling()
		return ok
	}},
	{name: "wasmbin", available: func() bool {
		_, err := exec.LookPath("wasmtime")
		return err == nil
	}},
}

// requireDiffOracleBackends fails the oracle before it runs a seed if the set
// of legs this host can exercise is not a set worth reporting a PASS for. It
// returns the available legs so the caller can name them in the log — a
// partial run should be visible as a statement, not inferred from a skip
// count nobody reads.
func requireDiffOracleBackends(t *testing.T) []string {
	t.Helper()

	var have, missing []string
	for _, leg := range diffOracleLegs {
		if leg.available() {
			have = append(have, leg.name)
		} else {
			missing = append(missing, leg.name)
		}
	}

	if want := os.Getenv("FERN_REQUIRE_DIFF_BACKENDS"); want != "" {
		var unmet []string
		for _, name := range strings.Split(want, ",") {
			name = strings.TrimSpace(name)
			if name == "" {
				continue
			}
			if !knownDiffOracleLeg(name) {
				t.Fatalf("FERN_REQUIRE_DIFF_BACKENDS names %q, which is not an oracle leg; known legs: %s",
					name, strings.Join(diffOracleLegNames(), ", "))
			}
			if !contains(have, name) {
				unmet = append(unmet, name)
			}
		}
		if len(unmet) > 0 {
			t.Fatalf("FERN_REQUIRE_DIFF_BACKENDS=%s, so this lane is meant to exercise those legs, "+
				"but their toolchains are missing: %s. Available here: %s",
				want, strings.Join(unmet, ", "), describeLegs(have))
		}
	}

	if len(have) == 0 {
		t.Fatalf("no oracle backend can run on this host (missing: %s) — every seed would compare "+
			"the interpreter against nothing and the run would still report PASS",
			strings.Join(missing, ", "))
	}
	return have
}

func knownDiffOracleLeg(name string) bool {
	for _, leg := range diffOracleLegs {
		if leg.name == name {
			return true
		}
	}
	return false
}

func diffOracleLegNames() []string {
	out := make([]string, 0, len(diffOracleLegs))
	for _, leg := range diffOracleLegs {
		out = append(out, leg.name)
	}
	return out
}

func describeLegs(names []string) string {
	if len(names) == 0 {
		return "none"
	}
	sorted := append([]string(nil), names...)
	sort.Strings(sorted)
	return strings.Join(sorted, ", ")
}

// TestDiffOracleHasABackend states the invariant on its own, so a host that
// cannot run the oracle says so in one line rather than as 1024 skipped
// subtests under a passing parent.
func TestDiffOracleHasABackend(t *testing.T) {
	have := requireDiffOracleBackends(t)
	t.Logf("differential oracle legs available here: %s", describeLegs(have))
}

// TestDiffOracleRequireEnvIsHonoured pins the env var's contract without
// depending on which toolchains this host happens to have: a leg name that
// does not exist is always unmet, so the failure path is reachable
// everywhere. Without this the strict mode would itself be a gate that only
// fires on a machine nobody tests on.
func TestDiffOracleRequireEnvIsHonoured(t *testing.T) {
	t.Setenv("FERN_REQUIRE_DIFF_BACKENDS", "arm64-linux")
	if !knownDiffOracleLeg("arm64-linux") {
		t.Fatal("arm64-linux is no longer an oracle leg; update this test")
	}
	// An unknown name must be rejected rather than silently satisfied --
	// a typo in a CI env var would otherwise disable the very check it
	// was added to enforce.
	if knownDiffOracleLeg("arm64") || knownDiffOracleLeg("") {
		t.Error("knownDiffOracleLeg accepts a name that is not a leg, so a typo in " +
			"FERN_REQUIRE_DIFF_BACKENDS would pass silently")
	}
	for _, name := range diffOracleLegNames() {
		if !knownDiffOracleLeg(name) {
			t.Errorf("leg %q does not recognise itself", name)
		}
	}
}

// diffOracleMinRunRatio is the floor on seeds a leg must actually execute,
// as a fraction of the seeds that got past the interpreter.
//
// The two register legs have no per-seed skip: a toolchain is present or it
// is not, and that is settled before the first seed, so anything under 1.00
// there is the leg hollowing out. wasmbin is the one that can legitimately
// sit below — `CompileAndRunWasmbinMain` skips a seed on a wasmbin coverage
// or emit gap, and a `knownDivergences` row parks one while its bug is open
// — so the floor is set for it and the register legs clear it trivially.
//
// Measured 2026-08-23 over the full 2048-seed corpus: arm64-linux and
// x86_64 both 2048/2048, wasmbin 2040/2048 (0.996 — eight emit gaps). The
// floor leaves wasmbin ~90 further gaps of headroom, so a generator change
// that reopens a few does not turn the lane red, while still sitting far
// above "the leg has hollowed out" — the same balance
// selfHostDiffMinRunRatio strikes for compile bails. Every leg logs its
// ratio on each run whether or not it passes, so drift toward the floor is
// readable before it fails.
//
// Same instrument and the same reasoning as selfHostDiffMinRunRatio, which
// exists because a widened compile-bail set would otherwise turn that lane
// green while testing almost nothing.
const diffOracleMinRunRatio = 0.95

// diffOracleTally counts what each leg executed, against the seeds that
// reached the legs at all.
type diffOracleTally struct {
	expected []string
	compared int64
	ran      map[string]*int64
}

func newDiffOracleTally(expected []string) *diffOracleTally {
	ran := make(map[string]*int64, len(expected))
	for _, name := range expected {
		ran[name] = new(int64)
	}
	return &diffOracleTally{expected: expected, ran: ran}
}

func (d *diffOracleTally) seedCompared() { atomic.AddInt64(&d.compared, 1) }

func (d *diffOracleTally) legRan(name string) {
	if c, ok := d.ran[name]; ok {
		atomic.AddInt64(c, 1)
	}
}

// check reports a leg that was available but did not run, which is the
// failure the up-front toolchain check cannot see.
func (d *diffOracleTally) check(t *testing.T) {
	t.Helper()
	compared := atomic.LoadInt64(&d.compared)
	if compared == 0 {
		// Every seed skipped on the interpreter side. Not a leg problem,
		// but still not a run anyone should read as coverage.
		t.Errorf("no seed reached a backend — all %d sampled seeds skipped on the "+
			"interpreter side, so the oracle compared nothing", compared)
		return
	}
	for _, name := range d.expected {
		got := atomic.LoadInt64(d.ran[name])
		ratio := float64(got) / float64(compared)
		// Logged whether or not it passes: rule 11 in docs/TEST-GATES.md —
		// most lanes cannot tell you whether a given test ran, and a gate
		// whose whole point is establishing that should say so out loud.
		t.Logf("leg %q ran %d of %d compared seeds (%.3f)", name, got, compared, ratio)
		if ratio < diffOracleMinRunRatio {
			t.Errorf("leg %q has its toolchain but ran only %d of %d compared seeds (%.3f), "+
				"below the %.2f floor — a leg that is installed and not running is the same "+
				"hollowed-out lane as a missing one",
				name, got, compared, ratio, diffOracleMinRunRatio)
		}
	}
}
