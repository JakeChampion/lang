package e2e

import (
	"os"
	"sort"
	"testing"

	"github.com/jakechampion/lang/internal/ssa"
)

// The measurement #7787 has been missing: how many runtime
// `__fern_rc_is_unique` guards are PROVABLY going to answer 1, over the
// program the backend actually emits, with the denominators logged so
// the number stays comparable.
//
// The tree carries two incompatible prior figures for this population —
// "14313 guards, 0 conservatively elidable" and "7126 guards, headroom
// still 0" in docs/rc-log/, and an uncommitted "352 of 2041" on the
// tracker — and they disagree because none of them recorded what it was
// measured over. This gate is the reproducible derivation: post-battery
// lowering (rule 14 of docs/TEST-GATES.md — the battery deletes half
// the guard population, so a pre-battery count overstates shipped
// code), whole conformance corpus, per-site verdicts from
// ssa.SoleOwnedGuards with each refusal classed.
//
// It deliberately pins no exact count — those rot — but it does pin the
// properties a transform would rest on: every site mapped back to a
// source op, and the walk actually covering the program.
func TestX86_64SoleOwnedGuardCensus(t *testing.T) {
	if testing.Short() {
		t.Skip("lowers the whole conformance corpus; not a -short test")
	}
	entries, err := os.ReadDir(conformanceCases)
	if err != nil {
		t.Fatalf("read %s: %v", conformanceCases, err)
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)

	var fixtures, funcs, liftFailed int
	var total, proven, unmapped int
	byReason := map[string]int{}
	for _, name := range names {
		prog, ok := lowerFixtureForCertify(t, name)
		if !ok {
			continue
		}
		fixtures++
		lifted, failures := ssa.LiftProgram(prog)
		liftFailed += len(failures)
		sol := ssa.SolveOwnership(lifted)
		for _, f := range lifted {
			funcs++
			u := ssa.UnitsOf(f, sol.Sigs)
			for _, gs := range ssa.SoleOwnedGuards(f, u, sol.Sigs) {
				total++
				if !gs.Site.Mapped {
					unmapped++
				}
				if gs.Proven {
					proven++
				} else {
					byReason[gs.Reason]++
				}
			}
		}
	}

	t.Logf("denominator: %d fixtures lowered post-battery, %d functions, %d lift failures",
		fixtures, funcs, liftFailed)
	t.Logf("guards: %d total, %d proven sole-owned (%.2f%%), %d unmapped",
		total, proven, 100*float64(proven)/float64(max(total, 1)), unmapped)
	t.Logf("refusals: %s", topCounts(byReason, 10))

	// Floors, not counts. A corpus with no guards means the enumeration
	// broke, not that the guards went away; an unmapped site is an
	// answer with nowhere to apply it; and lift coverage below 99%
	// makes the census describe a minority of the program.
	if total < 500 {
		t.Errorf("only %d is_unique guards found across the corpus — the census is "+
			"no longer measuring the population", total)
	}
	if unmapped != 0 {
		t.Errorf("%d guard sites carry no source op — provenance is meant to be total", unmapped)
	}
	if cov := 100 * float64(funcs) / float64(max(funcs+liftFailed, 1)); cov < 99 {
		t.Errorf("only %.2f%% of functions lifted (%d of %d)", cov, funcs, funcs+liftFailed)
	}
}
