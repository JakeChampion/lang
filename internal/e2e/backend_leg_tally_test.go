package e2e

import (
	"sort"
	"strings"
	"sync/atomic"
	"testing"
)

// Counting what a multi-leg oracle's backend legs actually EXECUTED.
//
// Every such oracle here runs one sub-test per backend so a missing toolchain
// skips only its own leg — docs/TEST-GATES.md rule 9. That buys leg
// independence at the cost of making the parent's PASS meaningless when every
// leg skips, which is #7310 (the diff oracle: 256 seeds green, 768 legs
// skipped, 0 executed, exit 0) and #7400 (the same hole in the two oracles
// behind runBackendsAgainst).
//
// The instrument is shared because the failure is: an availability check sees
// only the toolchain, and a leg can be installed and still stop running
// per-seed. Only a tally of executions sees the second case.
//
// The FLOOR is per-oracle rather than a constant, because what a leg may
// legitimately skip differs: the diff oracle's wasmbin leg parks a seed on an
// emit gap, while runBackendsAgainst's legs skip only for a listed
// known-divergence row. Each caller states its own measured number.

// backendLeg is one leg of an oracle and how to tell whether this host can
// run it.
type backendLeg struct {
	name      string
	available func() bool
}

// legTally counts per-leg executions against the seeds that reached the legs
// at all — seeds lost on the interpreter side are not a leg's fault and are
// excluded rather than held against it.
type legTally struct {
	expected []string
	minRatio float64
	compared int64
	ran      map[string]*int64
}

func newLegTally(expected []string, minRatio float64) *legTally {
	ran := make(map[string]*int64, len(expected))
	for _, name := range expected {
		ran[name] = new(int64)
	}
	return &legTally{expected: expected, minRatio: minRatio, ran: ran}
}

func (d *legTally) seedCompared() { atomic.AddInt64(&d.compared, 1) }

func (d *legTally) legRan(name string) {
	if c, ok := d.ran[name]; ok {
		atomic.AddInt64(c, 1)
	}
}

// check reports a leg that was available but did not run, which is the
// failure an up-front toolchain check cannot see.
func (d *legTally) check(t *testing.T) {
	t.Helper()
	compared := atomic.LoadInt64(&d.compared)
	if compared == 0 {
		// Every seed skipped on the interpreter side. Not a leg problem,
		// but still not a run anyone should read as coverage.
		t.Errorf("no seed reached a backend — all sampled seeds skipped on the " +
			"interpreter side, so the oracle compared nothing")
		return
	}
	for _, name := range d.expected {
		got := atomic.LoadInt64(d.ran[name])
		ratio := float64(got) / float64(compared)
		// Logged whether or not it passes: rule 11 in docs/TEST-GATES.md —
		// most lanes cannot tell you whether a given test ran, and a gate
		// whose whole point is establishing that should say so out loud.
		t.Logf("leg %q ran %d of %d compared seeds (%.3f)", name, got, compared, ratio)
		if ratio < d.minRatio {
			t.Errorf("leg %q has its toolchain but ran only %d of %d compared seeds (%.3f), "+
				"below the %.2f floor — a leg that is installed and not running is the same "+
				"hollowed-out lane as a missing one",
				name, got, compared, ratio, d.minRatio)
		}
	}
}

// availableLegs splits legs into the ones this host can run and the ones it
// cannot, so a caller can require, report, or tally them.
func availableLegs(legs []backendLeg) (have, missing []string) {
	for _, leg := range legs {
		if leg.available() {
			have = append(have, leg.name)
		} else {
			missing = append(missing, leg.name)
		}
	}
	return have, missing
}

func describeLegs(names []string) string {
	if len(names) == 0 {
		return "none"
	}
	sorted := append([]string(nil), names...)
	sort.Strings(sorted)
	return strings.Join(sorted, ", ")
}
