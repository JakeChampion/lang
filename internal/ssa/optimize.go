package ssa

// Optimize runs the Phase 2 peephole passes — Fold, Simplify,
// FoldBranches, TrivialPhis, CSE, DCE — in that order,
// iterating until the function's printed form stops changing.
// Returns the iteration count (useful in tests and for the
// upcoming `lang dump-ssa -opt-iters` flag).
//
// The order matters:
//  1. Fold collapses constant arithmetic + comparisons.
//  2. Simplify rewrites operand positions through algebraic
//     identities (`x + 0` → `x`).
//  3. FoldBranches collapses BrIf-on-const into Br, dropping
//     phi args for un-taken edges.
//  4. TrivialPhis aliases now-redundant phis (single arg or
//     all args identical) to their surviving Value.
//  5. CSE dedups pure expressions left over after Simplify +
//     TrivialPhis unified equivalent arg lists.
//  6. DCE reclaims everything orphaned by the five previous
//     passes.
//
// On a real program a single iteration usually does the job;
// the loop guards against the rare cascade where pass N+1
// unlocks fresh opportunities for pass N (e.g. FoldBranches
// drops a phi arg, TrivialPhis collapses the now-trivial phi,
// CSE merges the equivalent expressions that the phi was
// blocking).
//
// Caps iterations at maxOptimizeIters to bound worst-case
// runtime; in practice the loop converges in ≤ 3 passes for
// every test case we have so far.
const maxOptimizeIters = 16

func Optimize(f *Func) int {
	if f == nil {
		return 0
	}
	prev := f.String()
	for i := 1; i <= maxOptimizeIters; i++ {
		Fold(f)
		Simplify(f)
		FoldBranches(f)
		TrivialPhis(f)
		CSE(f)
		DCE(f)
		cur := f.String()
		if cur == prev {
			return i
		}
		prev = cur
	}
	return maxOptimizeIters
}
