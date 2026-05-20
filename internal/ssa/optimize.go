package ssa

// Optimize runs the Phase 2 peephole passes in order,
// iterating until the function's printed form stops changing.
// Returns the iteration count.
//
// The order matters:
//  1. SCCP propagates constants + CFG reachability together,
//     folding ops to const_<kind> and collapsing brif-on-const
//     terminators. Strictly more powerful than Fold +
//     FoldBranches running separately because SCCP can prove
//     that a phi merging values from multiple paths is constant
//     when only ONE of those paths is reachable.
//  2. Fold collapses any remaining constant arithmetic +
//     comparisons SCCP didn't reach (the standalone fold pass
//     covers Op shapes the SCCP rewriter doesn't yet, e.g.
//     OpTrunc / OpExtend* folds).
//  3. Simplify rewrites operand positions through algebraic
//     identities (`x + 0` → `x`).
//  4. CmpFlip / StrengthReduce / Canonicalize run their
//     specialised rewrites.
//  5. FoldBranches collapses BrIf-on-const that SCCP missed
//     (e.g. introduced post-SCCP by Fold/Simplify).
//  6. PruneUnreachable drops blocks that became unreachable
//     after FoldBranches severed their inbound edges.
//  7. MergeTrivialBlocks / FuseLinearBlocks compact the CFG.
//  8. TrivialPhis aliases now-redundant phis (single arg or
//     all args identical) to their surviving Value.
//  9. CSE dedups pure expressions left over after Simplify,
//     Canonicalize, and TrivialPhis unified equivalent args.
//
// 10. DCE reclaims everything orphaned by the previous passes.
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
		SCCP(f)
		Fold(f)
		Simplify(f)
		CmpFlip(f)
		StrengthReduce(f)
		Canonicalize(f)
		FoldBranches(f)
		PruneUnreachable(f)
		MergeTrivialBlocks(f)
		FuseLinearBlocks(f)
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
