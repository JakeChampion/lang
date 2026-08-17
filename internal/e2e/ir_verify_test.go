// Runs the independent IR verifier over the IR the compiler actually
// produces for the whole conformance corpus.
//
// The verifier is only worth having if it is run on real output, and
// only trustworthy if it is quiet on IR that is known good — a checker
// that fires on correct input gets disabled within a week. This is the
// gate for both: every case in the corpus is lowered at both pointer
// widths and verified, and any problem fails.
//
// Being quiet is not by itself evidence of checking anything, so two
// further gates sit alongside. The stack half reports how much of each
// program it could model, and that share is held above a floor here.
// And TestIRVerifierCatchesLoweringDamage breaks real lowered IR one op
// at a time and requires the damage to be reported.
package e2e

import (
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"testing"

	"github.com/jakechampion/lang/internal/checker"
	"github.com/jakechampion/lang/internal/constfold"
	"github.com/jakechampion/lang/internal/ir"
	"github.com/jakechampion/lang/internal/modload"
	"github.com/jakechampion/lang/internal/monomorph"
	"github.com/jakechampion/lang/internal/treeshake"
)

// Both widths: wasm32 resolves WidthPtr to 4, the register backends to
// 8, and the lowering differs enough that one can be well-formed while
// the other is not.
var verifyWidths = []int{4, 8}

// skippedLowering records the cases corpusPrograms could not lower, keyed
// "<case>@<ptrW>". A skip here removes a program from every gate built on
// the helper, so it is recorded rather than discarded — see
// TestCorpusLowersForVerification.
var skippedLowering = map[string]string{}

// corpusPrograms lowers every runnable case at both pointer widths and
// hands each resulting program to fn.
func corpusPrograms(t *testing.T, fn func(name string, ptrW int, ip *ir.Program)) {
	t.Helper()
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

	for _, name := range names {
		dir := filepath.Join(conformanceCases, name)
		if _, err := os.Stat(filepath.Join(dir, "expected.error")); err == nil {
			continue // compile-error case: never reaches lowering
		}
		if _, err := os.Stat(filepath.Join(dir, "expected.lowering-error")); err == nil {
			continue // the case IS a lowering rejection; failing to lower is the point
		}
		main := filepath.Join(dir, "main.fern")
		for _, w := range verifyWidths {
			// Re-load per width so a pass that rewrites the AST cannot
			// leak across the two.
			p, _, err := modload.Load(main)
			if err != nil {
				continue
			}
			if err := constfold.Fold(p, nil); err != nil {
				continue
			}
			info, err := checker.Check(p)
			if err != nil {
				continue
			}
			// Mirror what a backend does between checking and lowering.
			// Every omission here failed to LOWER rather than failing
			// loudly, so it was swallowed by the skip below and the case
			// went silently absent from every gate built on this helper:
			// without monomorphisation a generic instantiation stays
			// unspecialised, without the tree-shake a case importing
			// `std/float` dies on a function no program reaches, and
			// without DynSupported a `dyn Trait` case is refused outright
			// at ptrW 8.
			if err := monomorph.Run(p, info); err != nil {
				skippedLowering[name+"@"+strconv.Itoa(w)] = "monomorph: " + err.Error()
				continue
			}
			roots := append(treeshake.DynCoercionImplMethods(info),
				treeshake.DowncastImplMethods(p, info)...)
			treeshake.Run(p, info, roots...)
			var opts []ir.LowerOption
			if w == 8 {
				opts = append(opts, ir.DynSupported(), ir.DynRcSupported())
			}
			ip, err := ir.LowerWith(p, info, w, opts...)
			if err != nil {
				skippedLowering[name+"@"+strconv.Itoa(w)] = err.Error()
				continue // not every case lowers on every width
			}
			fn(name, w, ip)
		}
	}
}

// The share of functions the stack half must be able to model. It is a
// floor, not a target: a construct it cannot model is skipped rather
// than mis-reported, so the number falling is the signal that something
// new is going unchecked. The remainder today is generic code whose
// argument or result types are still type parameters at lowering time —
// an erased `T` may arrive as an integer, a float, or a two-word string,
// and nothing static says which.
const minStackCoverage = 97.0

func TestIRVerifierAcceptsEveryLoweredCase(t *testing.T) {
	var lowered, funcs, modelled int
	skipped := map[string]int{}

	corpusPrograms(t, func(name string, w int, ip *ir.Program) {
		lowered++
		problems, cov := ir.Verify(ip)
		funcs += cov.Funcs
		modelled += cov.Modelled
		for reason, n := range cov.Reasons() {
			skipped[reason] += n
		}
		if len(problems) > 0 {
			t.Errorf("%s (ptr width %d): IR is not well-formed:%s",
				name, w, ir.FormatProblems(problems, 10))
		}
	})

	if lowered == 0 {
		t.Fatalf("nothing lowered — the verifier is not being exercised")
	}
	got := 100 * float64(modelled) / float64(funcs)
	if got < minStackCoverage {
		var reasons []string
		for r := range skipped {
			reasons = append(reasons, r)
		}
		sort.Slice(reasons, func(a, b int) bool { return skipped[reasons[a]] > skipped[reasons[b]] })
		t.Errorf("the stack pass modelled %.1f%% of %d functions, below the %.1f%% floor — "+
			"something new is going unchecked. Most common reasons:%s",
			got, funcs, minStackCoverage, formatCounts(skipped, reasons, 5))
	}
	t.Logf("verified %d lowered programs, %d functions, %.1f%% stack-modelled", lowered, funcs, got)
}

func formatCounts(counts map[string]int, keys []string, max int) string {
	out := ""
	for i, k := range keys {
		if i == max {
			break
		}
		out += "\n    " + k + " ×" + strconv.Itoa(counts[k])
	}
	return out
}

// Deleting one stack-effecting op from a lowered function is exactly the
// shape a lowering bug takes: a value pushed and never consumed, or
// consumed and never pushed. Nearly all of it has to be reported, and
// this measures how much is.
//
// Not quite all, and the shortfall is wasm's rule rather than a gap. A
// `return` takes the function's results off the stack and marks the rest
// of the scope unreachable; operands stranded below it are discarded,
// exactly as wasm validation discards them. So an imbalance introduced
// before a `return` and never consumed is invisible — to this pass and
// to a wasm validator alike. That is the residual the floor below leaves
// room for. Deletions are restricted to the ops preceding the first
// branch or return so every mutation at least lands in code the pass
// treats as reachable.
const minMutationsCaught = 95.0

const maxMutationsPerProgram = 4

func TestIRVerifierCatchesLoweringDamage(t *testing.T) {
	var mutated, caught int
	missed := map[string]int{}

	corpusPrograms(t, func(name string, w int, ip *ir.Program) {
		_, base := ir.Verify(ip)
		perProgram := 0
		for fi, f := range ip.Funcs {
			// Each mutation re-verifies the whole program, so the work is
			// quadratic in a program's function count. Capping it keeps
			// the gate to a few seconds; the corpus is wide enough that
			// the sample still reaches thousands of distinct functions.
			if perProgram >= maxMutationsPerProgram {
				break
			}
			if _, wasSkipped := base.Skipped[f.Name]; wasSkipped {
				continue // an unmodelled function proves nothing either way
			}
			limit := reachableLimit(f.Ops)
			for oi := 0; oi < limit; oi++ {
				if !stackEffecting(f.Ops[oi].Kind) {
					continue
				}
				problems, cov := ir.Verify(withOpDeleted(ip, fi, oi))
				if _, nowSkipped := cov.Skipped[f.Name]; nowSkipped {
					break // the damage made the function unmodellable
				}
				mutated++
				perProgram++
				if len(problems) > 0 {
					caught++
				} else {
					missed[f.Ops[oi].Kind.String()]++
				}
				break // one mutation per function is enough
			}
		}
	})

	if mutated == 0 {
		t.Fatal("no mutation was applied — the gate is not exercising anything")
	}
	got := 100 * float64(caught) / float64(mutated)
	if got < minMutationsCaught {
		var kinds []string
		for k := range missed {
			kinds = append(kinds, k)
		}
		sort.Slice(kinds, func(a, b int) bool { return missed[kinds[a]] > missed[kinds[b]] })
		t.Errorf("deleted one stack-effecting op from %d functions and the verifier reported %.1f%% of them, "+
			"below the %.1f%% floor — a lowering bug of the same shape is going unreported:%s",
			mutated, got, minMutationsCaught, formatCounts(missed, kinds, 5))
	}
	t.Logf("caught %d of %d single-op deletions (%.1f%%)", caught, mutated, got)
}

// withOpDeleted returns a shallow copy of the program with one op
// removed from one function. Everything else is shared: only the
// mutated function is rebuilt.
func withOpDeleted(ip *ir.Program, fi, oi int) *ir.Program {
	f := ip.Funcs[fi]
	ops := make([]ir.Op, 0, len(f.Ops)-1)
	ops = append(ops, f.Ops[:oi]...)
	ops = append(ops, f.Ops[oi+1:]...)

	mutant := *f
	mutant.Ops = ops
	funcs := make([]*ir.Func, len(ip.Funcs))
	copy(funcs, ip.Funcs)
	funcs[fi] = &mutant

	out := *ip
	out.Funcs = funcs
	return &out
}

// stackEffecting reports whether deleting this op must change the
// operand stack. Ops that push and pop the same number of slots are
// invisible to a stack-discipline check by construction, and the control
// ops are the structural half's business.
func stackEffecting(k ir.OpKind) bool {
	switch k {
	case ir.OpConstI32, ir.OpConstI64, ir.OpConstF32, ir.OpConstF64,
		ir.OpConstStr, ir.OpConstFunc, ir.OpLoadLocal,
		ir.OpDrop, ir.OpStoreLocal:
		return true
	}
	return false
}

// reachableLimit is the number of leading ops the verifier treats as
// reachable: everything up to the first branch or return.
func reachableLimit(ops []ir.Op) int {
	for i, op := range ops {
		switch op.Kind {
		case ir.OpBr, ir.OpBrIf, ir.OpReturn, ir.OpReturnVoid, ir.OpReturnPair:
			return i
		}
	}
	return len(ops)
}

// A case corpusPrograms cannot lower is absent from every gate built on
// it, silently — which is how 280 of 758 case/width pairs went
// unverified. The helper ran `checker.Check` then `ir.LowerWith` with no
// options, where a backend first monomorphises, then tree-shakes, and at
// ptrW 8 opts into `dyn`. Every omission failed to LOWER rather than
// failing loudly, so each one subtracted programs from the gate instead
// of reporting anything.
//
// With the pipeline matched to a backend's, all 758 lower. This holds
// that, so the next omission is a failure rather than a quiet subtraction.
func TestCorpusLowersForVerification(t *testing.T) {
	var lowered int
	corpusPrograms(t, func(string, int, *ir.Program) { lowered++ })
	if lowered == 0 {
		t.Fatal("nothing lowered — the helper is not measuring anything")
	}
	if len(skippedLowering) > 0 {
		var keys []string
		for k := range skippedLowering {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			t.Errorf("%s did not lower, so it is in no IR gate: %s", k, skippedLowering[k])
		}
	}
	t.Logf("%d case/width pairs lowered, %d skipped", lowered, len(skippedLowering))
}
