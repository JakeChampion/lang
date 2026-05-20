package ssa

// FoldBranches rewrites BrIf terminators whose condition is a
// compile-time constant into unconditional branches to the
// taken target. The un-taken successor loses its inbound edge
// — its Preds entry for this block is removed, and every
// phi-node arg slot in that successor that lined up with the
// dropped pred is removed too (Preds + phi-Args parallelism).
//
// Also handles the redundant-BrIf case: if `True == False`
// the branch can become unconditional regardless of cond.
// (Common after `if (x) { /* same */ } else { /* same */ }`
// gets dead-code-eliminated to identical blocks, or when a
// front-end emits redundant branches.)
//
// `brif (not v), T, F` is rewritten to `brif v, F, T`. CmpFlip
// already pulls `not(cmp)` into its inverted comparison op for
// the value-position case; this handles the residual where the
// not's argument isn't a comparison (e.g. a bool param threaded
// through a not) and the only consumer is the brif. Saves a not
// op when DCE reclaims it.
//
// Composes with Fold: a chain like
//
//	v1 = const_int 1
//	v2 = const_int 1
//	v3 = eq v1, v2       // ⇒ const_bool 1
//	brif v3, T, F        // ⇒ br T
//
// folds end-to-end with `Fold` then `FoldBranches`. After
// elimination the Op chain producing the const may go dead;
// pair with DCE to reclaim it.
//
// Doesn't recompute reachability — blocks that lose every
// inbound edge stay in `f.Blocks` (Verify already tolerates
// unreachable blocks via the dom-tree builder skipping them).
// A separate reachability pass can prune them later.
func FoldBranches(f *Func) {
	if f == nil {
		return
	}
	defs := map[int32]*Op{}
	for _, b := range f.Blocks {
		for _, op := range b.Ops {
			if op.Result.IsValid() {
				defs[op.Result.ID] = op
			}
		}
	}

	for _, b := range f.Blocks {
		if b.Term.Kind != TermBrIf {
			continue
		}
		tBlock := b.Term.True
		fBlock := b.Term.False
		if tBlock == nil || fBlock == nil {
			continue
		}

		// True == False: collapse without consulting the cond.
		if tBlock == fBlock {
			b.Term = Terminator{Kind: TermBr, Target: tBlock}
			// Preds for tBlock already includes b once (appendUnique).
			continue
		}

		cond := b.Term.Cond
		if !cond.IsValid() {
			continue
		}
		def, ok := defs[cond.ID]
		if !ok {
			continue
		}

		// brif (not v), T, F  →  brif v, F, T. Saves the OpNot.
		// Loop in case `v` is itself wrapped — chained nots
		// collapse all the way through. `seen` guards against a
		// malformed self-referencing OpNot.
		seen := map[int32]bool{}
		for def.Kind == OpNot && len(def.Args) == 1 && def.Args[0].IsValid() {
			if seen[cond.ID] {
				break
			}
			seen[cond.ID] = true
			b.Term.Cond = def.Args[0]
			b.Term.True, b.Term.False = b.Term.False, b.Term.True
			tBlock, fBlock = b.Term.True, b.Term.False
			cond = b.Term.Cond
			def, ok = defs[cond.ID]
			if !ok {
				break
			}
		}

		if !ok || def.Kind != OpConstBool {
			continue
		}

		var taken, untaken *Block
		if def.Imm != 0 {
			taken, untaken = tBlock, fBlock
		} else {
			taken, untaken = fBlock, tBlock
		}

		b.Term = Terminator{Kind: TermBr, Target: taken}
		removePred(untaken, b)
	}
}

// removePred drops `pred` from `succ.Preds` and removes the
// corresponding parallel slot from every phi in succ.Ops.
// No-op if pred isn't actually in the Preds list — defensive
// against double-removes.
func removePred(succ, pred *Block) {
	idx := -1
	for i, p := range succ.Preds {
		if p == pred {
			idx = i
			break
		}
	}
	if idx == -1 {
		return
	}
	succ.Preds = append(succ.Preds[:idx], succ.Preds[idx+1:]...)
	for _, op := range succ.Ops {
		if op.Kind != OpPhi {
			continue
		}
		if idx < len(op.Args) {
			op.Args = append(op.Args[:idx], op.Args[idx+1:]...)
		}
	}
}
