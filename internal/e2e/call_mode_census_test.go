package e2e

import (
	"os"
	"sort"
	"testing"

	"github.com/jakechampion/lang/internal/ssa"
)

// The measurement #7792 asks for before anything is built: at how many
// call sites would an ownership-mode-specialised copy of the callee
// remove reference-count traffic, over the program the backend emits.
//
// The tracker carried three figures for this population — 662, 3,600
// and 3,599 — and none of them separated the sites where a variant
// deletes a retain/release PAIR from the sites where it only moves the
// caller's one release into the callee. `ssa.CallModeSites` makes that
// split, and this gate is its reproducible derivation: post-battery
// lowering (rule 14 of docs/TEST-GATES.md), whole conformance corpus,
// every site classed, with the denominators logged so the number stays
// comparable.
//
// It pins no count — those rot — but it does pin what a variant policy
// would rest on: every site mapped back to a source op, and the walk
// covering the program.
func TestX86_64CallModeCensus(t *testing.T) {
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

	var fixtures, funcs, liftFailed, total, carrying, unmapped int
	byClass := map[string]int{}
	pairCallees := map[string]int{}
	deferredCallees := map[string]int{}
	borrowedCallees := map[string]int{}
	for _, name := range names {
		prog, ok := lowerFixtureForCertify(t, name)
		if !ok {
			continue
		}
		fixtures++
		lifted, failures := ssa.LiftProgram(prog)
		liftFailed += len(failures)
		funcs += len(lifted)
		sol := ssa.SolveOwnership(lifted)
		for _, s := range ssa.CallModeSites(lifted, sol) {
			total++
			if s.Origin != ssa.UnitNone {
				carrying++
			}
			if !s.Mapped {
				unmapped++
			}
			class := s.Class()
			byClass[class]++
			switch class {
			case ssa.ClassOwnedVariantPair:
				pairCallees[s.Callee]++
			case ssa.ClassOwnedVariantDeferred:
				deferredCallees[s.Callee]++
			case ssa.ClassBorrowedVariantPair:
				borrowedCallees[s.Callee]++
			}
		}
	}

	t.Logf("denominator: %d fixtures lowered post-battery, %d functions, %d lift failures",
		fixtures, funcs, liftFailed)
	t.Logf("sites: %d pointer arguments at solved call sites, %d carrying a unit, %d unmapped",
		total, carrying, unmapped)
	t.Logf("by class: %s", topCounts(byClass, 4))
	t.Logf("owned variant, pair removable: %d callees, top: %s",
		len(pairCallees), topCounts(pairCallees, 8))
	t.Logf("owned variant, release only moves: %d callees, top: %s",
		len(deferredCallees), topCounts(deferredCallees, 5))
	t.Logf("borrowed variant, pair removable: %d callees, top: %s",
		len(borrowedCallees), topCounts(borrowedCallees, 5))

	// Floors, not counts. A corpus with no sites means the enumeration
	// broke; an unmapped site is an answer with nowhere to apply it;
	// and lift coverage below 99% makes the census describe a minority
	// of the program.
	if total < 2000 {
		t.Errorf("only %d call-site arguments found across the corpus — the census is "+
			"no longer measuring the population", total)
	}
	if unmapped != 0 {
		t.Errorf("%d sites carry no source op — provenance is meant to be total", unmapped)
	}
	if cov := 100 * float64(funcs) / float64(max(funcs+liftFailed, 1)); cov < 99 {
		t.Errorf("only %.2f%% of functions lifted (%d of %d)", cov, funcs, funcs+liftFailed)
	}
}
