package ssa

// FuseLinearBlocks merges a block A's ops into its sole
// successor B (and inherits B's terminator) when:
//
//   - A's terminator is `br B`, AND
//   - B is not the entry block (would re-target Entry), AND
//   - B's only predecessor is A, AND
//   - B has no phi ops (phi with one pred is degenerate; pair
//     with TrivialPhis to fold those first).
//
// Effect: A becomes the union of A's ops + B's ops, ending
// with B's old terminator. B becomes unreachable. PruneUnreachable
// reclaims it on the next Optimize iteration.
//
// Each successor S of B has B replaced with A in S.Preds, so
// phi nodes at S continue to reference the right Pred-slot
// Value (the Value flowing in is unchanged).
//
// Stronger than MergeTrivialBlocks (which requires B to be
// empty); this version fuses linear chains regardless of B's
// op count.
//
// Wired into the Optimize pipeline between MergeTrivialBlocks
// and TrivialPhis.
func FuseLinearBlocks(f *Func) {
	if f == nil {
		return
	}
	for _, a := range f.Blocks {
		if a.Term.Kind != TermBr || a.Term.Target == nil {
			continue
		}
		b := a.Term.Target
		if b == f.Entry {
			continue
		}
		if len(b.Preds) != 1 || b.Preds[0] != a {
			continue
		}
		// Skip if B has phi ops — fusing into A would change the
		// pred-identity for those phis; TrivialPhis handles the
		// single-pred case first.
		hasPhi := false
		for _, op := range b.Ops {
			if op.Kind == OpPhi {
				hasPhi = true
				break
			}
		}
		if hasPhi {
			continue
		}
		// Splice: append B's ops to A's, take over B's terminator.
		a.Ops = append(a.Ops, b.Ops...)
		oldTerm := b.Term
		a.Term = oldTerm
		// Update successors' Preds: B → A.
		for _, s := range b.Succs() {
			for j, p := range s.Preds {
				if p == b {
					s.Preds[j] = a
				}
			}
		}
		// B is now orphan.
		b.Ops = nil
		b.Preds = nil
		b.Term = Terminator{}
	}
}
