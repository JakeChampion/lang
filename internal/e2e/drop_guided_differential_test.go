package e2e

import (
	"fmt"
	"testing"

	"github.com/jakechampion/lang/internal/ast"
	"github.com/jakechampion/lang/internal/fernsmith"
)

// E3 drop-guided differential (the fuzz half of the comparison harness):
// fernsmith-generated programs are each run through the interpreter (the
// flag-independent source of truth) and compiled twice on x86-64 — flag
// OFF then flag ON — and all three results must agree pairwise. The flag
// selects reuse-pair PROPOSALS only; the shared is_unique guard +
// degrade-to-fresh-alloc lowering make every proposal behaviourally
// invisible, and this sweep pins that invariant across the generator's
// whole construct mix (structs, enums, tuples, matches, closures, loops).
//
// Seeds are DISJOINT from nothing — they deliberately reuse the same
// deterministic fernsmith.GenMain space the 2048-seed diff oracle covers,
// so any failure here reproduces under the existing tooling. Serial (no
// t.Parallel): the flag is a process-global toggled around each compile.
const dropGuidedDiffSeedCount = 224

func TestDropGuidedDifferential(t *testing.T) {
	seeds := uint64(dropGuidedDiffSeedCount)
	if testing.Short() {
		seeds = dropGuidedDiffSeedCount / 8
	}
	mismatches := 0
	for seed := uint64(0); seed < seeds; seed++ {
		seed := seed
		t.Run(fmt.Sprintf("seed=%d", seed), func(t *testing.T) {
			src := fernsmith.GenMain(seed)
			expected := runInterpByte(t, src) // skips on interp coverage gaps

			offOut, offCode := compileAndRunX86_64(t, src)
			var onOut string
			var onCode int
			prev := ast.RcReuseDropGuided
			ast.RcReuseDropGuided = true
			onOut, onCode = compileAndRunX86_64(t, src)
			ast.RcReuseDropGuided = prev

			if offCode != expected {
				mismatches++
				t.Errorf("flag OFF disagrees with interp: off=%d interp=%d\nsrc:\n%s", offCode, expected, src)
			}
			if onCode != offCode || onOut != offOut {
				mismatches++
				t.Errorf("drop-guided changed behaviour: off=%d/%q on=%d/%q\nsrc:\n%s",
					offCode, offOut, onCode, onOut, src)
			}
		})
	}
	if mismatches != 0 {
		t.Errorf("%d differential mismatches across %d seeds", mismatches, seeds)
	}
}
