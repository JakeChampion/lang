package ssa

// MergeTrivialBlocks splices out blocks whose only role is to
// forward control to another block via an unconditional br
// with no ops. Specifically: B is eligible iff
//
//   - B is not the function's entry block, AND
//   - B has zero Ops, AND
//   - B's terminator is `br X`, AND
//   - B has exactly one predecessor A, AND
//   - A is not already a predecessor of X (no duplicate-Preds
//     pitfall that would break phi parallelism).
//
// When all of those hold, we rewrite A's terminator to point
// at X instead of B and rewrite X.Preds to replace B with A.
// B becomes unreachable; PruneUnreachable picks it up later.
//
// Phi nodes at X don't need rewiring: phi.Args follows Preds
// order, and we're swapping one Pred-identity for another at
// the same slot — the Value flowing in is unchanged (it's
// whatever A produced before its terminator, which is what
// B produced before, since B was just forwarding).
//
// Wired into the Optimize pipeline between PruneUnreachable
// and TrivialPhis — single-pass; the second Optimize iteration
// runs PruneUnreachable again to clean up the orphaned B's.
func MergeTrivialBlocks(f *Func) {
	if f == nil {
		return
	}
	for _, b := range f.Blocks {
		if b == f.Entry {
			continue
		}
		if len(b.Ops) != 0 {
			continue
		}
		if b.Term.Kind != TermBr || b.Term.Target == nil {
			continue
		}
		if len(b.Preds) != 1 {
			continue
		}
		a := b.Preds[0]
		x := b.Term.Target
		if predContains(x, a) {
			// A is already a pred of X; merging would duplicate.
			continue
		}
		// Rewrite A's terminator: any reference to B → X.
		switch a.Term.Kind {
		case TermBr:
			if a.Term.Target == b {
				a.Term.Target = x
			}
		case TermBrIf:
			if a.Term.True == b {
				a.Term.True = x
			}
			if a.Term.False == b {
				a.Term.False = x
			}
		default:
			continue
		}
		// Replace B with A in X.Preds.
		for j, p := range x.Preds {
			if p == b {
				x.Preds[j] = a
				break
			}
		}
		// B's outgoing edge to X drops on the floor — B becomes
		// unreachable. Clear B's terminator so it can't be picked
		// up by future passes mid-flight.
		b.Term = Terminator{Kind: TermBr, Target: x}
		b.Preds = nil
	}
}

func predContains(b *Block, p *Block) bool {
	for _, x := range b.Preds {
		if x == p {
			return true
		}
	}
	return false
}
