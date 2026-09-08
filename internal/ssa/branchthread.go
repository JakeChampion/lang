package ssa

// ThreadPhiBranches bypasses a boolean-only join from unconditional incoming
// edges. Instead of materialising a comparison, passing it through a phi and
// then branching, the predecessor branches directly on its incoming value.
// No operations are cloned or speculated: short-circuit evaluation stays on
// the original paths. A comparison now local to its branch can fuse in codegen.
//
// Deliberately limited to a single phi used only by the join's terminator.
// Other ops, escaping values, conditional predecessors and conflicting edges
// are left alone. Successor phi arguments are copied from the bypassed edge;
// since the join defines no escaping value, those definitions already dominate
// every incoming predecessor. PruneUnreachable removes fully orphaned joins.
func ThreadPhiBranches(f *Func) {
	if f == nil {
		return
	}
	uses := collectUses(f)
	booleans := make(map[int32]bool)
	for _, b := range f.Blocks {
		for _, op := range b.Ops {
			// Only bypass values known to be 0 or 1. This does not rely on
			// phi copies preserving arbitrary integer/address widths before
			// the backend's width-resolution pass has run.
			if op.Result.IsValid() && (IsComparison(op.Kind) ||
				(isIntConst(op.Kind) && (op.Imm == 0 || op.Imm == 1))) {
				booleans[op.Result.ID] = true
			}
		}
	}
	for _, b := range f.Blocks {
		if b == f.Entry || len(b.Preds) == 0 || len(b.Ops) != 1 || b.Term.Kind != TermBrIf {
			continue
		}
		phi := b.Ops[0]
		if phi.Kind != OpPhi || phi.Result != b.Term.Cond || uses[phi.Result.ID] != 1 || len(phi.Args) != len(b.Preds) || !threadPhiUniquePreds(b) {
			continue
		}
		yes, no := b.Term.True, b.Term.False
		if yes == nil || no == nil || yes == no || yes == b || no == b {
			continue
		}
		yesSlot, yesOK := threadPhiEdgeSlot(yes, b)
		noSlot, noOK := threadPhiEdgeSlot(no, b)
		if !yesOK || !noOK {
			continue
		}
		for i := len(b.Preds) - 1; i >= 0; i-- {
			p := b.Preds[i]
			if p == b || p == yes || p == no || p.Term.Kind != TermBr || p.Term.Target != b ||
				predContains(yes, p) || predContains(no, p) {
				continue
			}
			incoming := phi.Args[i]
			if !incoming.IsValid() || !booleans[incoming.ID] {
				continue
			}
			// The incoming value loses one phi use and gains one branch use,
			// leaving its count unchanged. Duplicated successor phi arguments
			// do add uses; account for them so later candidates stay conservative.
			for _, edge := range []struct {
				target *Block
				slot   int
			}{{yes, yesSlot}, {no, noSlot}} {
				for _, op := range edge.target.Ops {
					if op.Kind == OpPhi {
						arg := op.Args[edge.slot]
						op.Args = append(op.Args, arg)
						if arg.IsValid() {
							uses[arg.ID]++
						}
					}
				}
				edge.target.Preds = append(edge.target.Preds, p)
			}
			p.Term = Terminator{Kind: TermBrIf, Cond: incoming, True: yes, False: no}
			removePred(b, p)
		}
	}
}

func threadPhiUniquePreds(b *Block) bool {
	seen := make(map[*Block]bool, len(b.Preds))
	for _, p := range b.Preds {
		if p == nil || seen[p] {
			return false
		}
		seen[p] = true
	}
	return true
}

// threadPhiEdgeSlot validates predecessor/phi parallelism before mutation.
func threadPhiEdgeSlot(target, pred *Block) (int, bool) {
	slot := -1
	for i, p := range target.Preds {
		if p == pred {
			if slot != -1 {
				return 0, false
			}
			slot = i
		}
	}
	if slot == -1 {
		return 0, false
	}
	for _, op := range target.Ops {
		if op.Kind == OpPhi && len(op.Args) != len(target.Preds) {
			return 0, false
		}
	}
	return slot, true
}
