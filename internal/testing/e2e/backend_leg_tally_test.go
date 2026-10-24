package e2e

import (
	"sort"
	"strings"
	"sync/atomic"
	"testing"
)

type backendLeg struct {
	name      string
	available func() bool
}

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

func (d *legTally) check(t *testing.T) {
	t.Helper()
	compared := atomic.LoadInt64(&d.compared)
	if compared == 0 {
		t.Errorf("no seed reached a backend — all sampled seeds skipped on the " +
			"interpreter side, so the oracle compared nothing")
		return
	}
	for _, name := range d.expected {
		got := atomic.LoadInt64(d.ran[name])
		ratio := float64(got) / float64(compared)
		t.Logf("leg %q ran %d of %d compared seeds (%.3f)", name, got, compared, ratio)
		if ratio < d.minRatio {
			t.Errorf("leg %q has its toolchain but ran only %d of %d compared seeds (%.3f), "+
				"below the %.2f floor — a leg that is installed and not running is the same "+
				"hollowed-out lane as a missing one",
				name, got, compared, ratio, d.minRatio)
		}
	}
}

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
