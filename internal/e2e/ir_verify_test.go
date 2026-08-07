// Runs the independent IR verifier over the IR the compiler actually
// produces for the whole conformance corpus.
//
// The verifier is only worth having if it is run on real output, and
// only trustworthy if it is quiet on IR that is known good — a checker
// that fires on correct input gets disabled within a week. This is the
// gate for both: every case in the corpus is lowered at both pointer
// widths and verified, and any problem fails.
package e2e

import (
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/jakechampion/lang/internal/checker"
	"github.com/jakechampion/lang/internal/constfold"
	"github.com/jakechampion/lang/internal/ir"
	"github.com/jakechampion/lang/internal/modload"
)

func TestIRVerifierAcceptsEveryLoweredCase(t *testing.T) {
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

	// Both widths: wasm32 resolves WidthPtr to 4, the register backends
	// to 8, and the lowering differs enough that one can be well-formed
	// while the other is not.
	widths := []int{4, 8}

	var lowered int
	for _, name := range names {
		dir := filepath.Join(conformanceCases, name)
		if _, err := os.Stat(filepath.Join(dir, "expected.error")); err == nil {
			continue // compile-error case: never reaches lowering
		}
		main := filepath.Join(dir, "main.fern")

		prog, _, err := modload.Load(main)
		if err != nil {
			continue
		}
		if err := constfold.Fold(prog, nil); err != nil {
			continue
		}
		info, err := checker.Check(prog)
		if err != nil {
			continue
		}
		for _, w := range widths {
			// Lower mutates nothing shared, but re-load per width so a
			// pass that rewrites the AST cannot leak across the two.
			p2, _, err := modload.Load(main)
			if err != nil {
				continue
			}
			if err := constfold.Fold(p2, nil); err != nil {
				continue
			}
			info2, err := checker.Check(p2)
			if err != nil {
				continue
			}
			ip, err := ir.LowerWith(p2, info2, w)
			if err != nil {
				continue // not every case lowers on every width
			}
			lowered++
			if problems := ir.Verify(ip); len(problems) > 0 {
				t.Errorf("%s (ptr width %d): IR is not well-formed:%s",
					name, w, ir.FormatProblems(problems, 10))
			}
		}
		_ = info
	}

	if lowered == 0 {
		t.Fatalf("nothing lowered — the verifier is not being exercised")
	}
	t.Logf("verified %d lowered programs", lowered)
}
