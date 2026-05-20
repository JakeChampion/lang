package ssa

// Optimize runs the Phase 2 peephole passes — Fold, Simplify,
// FoldBranches, PruneUnreachable, TrivialPhis, CSE, DCE — in
// that order, iterating until the function's printed form
// stops changing. Returns the iteration count.
//
// The order matters:
//  1. Fold collapses constant arithmetic + comparisons.
//  2. Simplify rewrites operand positions through algebraic
//     identities (`x + 0` → `x`).
//  3. FoldBranches collapses BrIf-on-const into Br, dropping
//     phi args for un-taken edges.
//  4. PruneUnreachable drops blocks that became unreachable
//     after FoldBranches severed their inbound edges, and
//     cleans up their dangling pred entries in live
//     successors.
//  5. TrivialPhis aliases now-redundant phis (single arg or
//     all args identical) to their surviving Value.
//  6. CSE dedups pure expressions left over after Simplify +
//     TrivialPhis unified equivalent arg lists.
//  7. DCE reclaims everything orphaned by the previous passes.
//
// On a real program a single iteration usually does the job;
// the loop guards against the rare cascade where pass N+1
// unlocks fresh opportunities for pass N.
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
		PruneUnreachable(f)
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
