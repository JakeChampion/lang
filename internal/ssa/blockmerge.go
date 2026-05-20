package ssa

// MergeTrivialBlocks splices out blocks whose only role is to
// forward control to another block via an unconditional br
// with no ops. B is eligible iff:
//
//   - B is not the function's entry block, AND
//   - B has zero Ops, AND
//   - B's terminator is `br X`, AND
//   - none of B's predecessors P are already preds of X (no
//     duplicate-Preds pitfall), AND
//   - if X has phi ops, the number of preds B will inject into
//     X.Preds is finite (always true; mentioned for clarity).
//
// Each pred P of B is rewritten to point at X directly; the
// `B` entry in X.Preds is replaced with the first P, and the
// remaining P's are appended. Phi nodes at X get their B-
// indexed Arg replicated for each new pred-slot (the Value
// flowing through B is unchanged — B is empty — so all of P1,
// P2, …, Pn see the same incoming Value).
//
// Single-pred B is a special case of this (single-slot
// replacement, no growth).
//
// Wired into the Optimize pipeline between PruneUnreachable
// and FuseLinearBlocks. Pair with PruneUnreachable on the
// next Optimize iteration to drop the orphan B.
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
		if len(b.Preds) == 0 {
			continue
		}
		x := b.Term.Target
		if x == b {
			// Self-loop on an empty block — degenerate, skip.
			continue
		}
		// Bail if any pred of B is already a pred of X — merging
		// would create duplicate Preds at X.
		conflict := false
		for _, p := range b.Preds {
			if predContains(x, p) {
				conflict = true
				break
			}
		}
		if conflict {
			continue
		}
		// Find B's slot in X.Preds — its phi-arg index.
		bSlot := -1
		for j, p := range x.Preds {
			if p == b {
				bSlot = j
				break
			}
		}
		if bSlot < 0 {
			continue
		}
		// Rewrite each pred's terminator: B → X.
		for _, p := range b.Preds {
			switch p.Term.Kind {
			case TermBr:
				if p.Term.Target == b {
					p.Term.Target = x
				}
			case TermBrIf:
				if p.Term.True == b {
					p.Term.True = x
				}
				if p.Term.False == b {
					p.Term.False = x
				}
			}
		}
		// Splice the B-slot in X.Preds: replace with B.Preds[0]
		// and append B.Preds[1..n] to the end. Each phi at X
		// must follow: replicate its B-slot Arg for the new
		// trailing preds.
		newPreds := make([]*Block, 0, len(x.Preds)+len(b.Preds)-1)
		newPreds = append(newPreds, x.Preds[:bSlot]...)
		newPreds = append(newPreds, b.Preds...)
		newPreds = append(newPreds, x.Preds[bSlot+1:]...)
		x.Preds = newPreds
		// Walk phis at X — only ops that are OpPhi can be in
		// header position. Replicate the B-slot arg for the
		// (len(b.Preds) - 1) trailing slots we're inserting.
		extra := len(b.Preds) - 1
		if extra > 0 {
			for _, op := range x.Ops {
				if op.Kind != OpPhi {
					continue
				}
				if bSlot >= len(op.Args) {
					continue
				}
				bArg := op.Args[bSlot]
				newArgs := make([]Value, 0, len(op.Args)+extra)
				newArgs = append(newArgs, op.Args[:bSlot]...)
				for i := 0; i < len(b.Preds); i++ {
					newArgs = append(newArgs, bArg)
				}
				newArgs = append(newArgs, op.Args[bSlot+1:]...)
				op.Args = newArgs
			}
		}
		// B becomes orphan.
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
