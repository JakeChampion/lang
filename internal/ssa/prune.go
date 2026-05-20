package ssa

// PruneUnreachable removes blocks that have no path from
// `f.Entry`. Returns the number of blocks removed. Each
// dropped block has its outgoing edges severed too — every
// surviving successor loses the dead block's entry in its
// `Preds` list, and any phi-arg slot in that successor that
// lined up with the dead pred is also removed (Preds + phi
// parallelism preserved).
//
// Typical use after FoldBranches: the un-taken branch loses
// its inbound edge from the brif block, but its OWN outgoing
// edges still exist — so the merge block downstream still
// has the dead block in its Preds (and a corresponding phi
// arg). PruneUnreachable walks from entry, finds the dead
// block, and cleans up.
//
// Cheap: O(blocks + edges) DFS plus the same again for the
// Preds cleanup. No dom-tree needed.
func PruneUnreachable(f *Func) int {
	if f == nil || f.Entry == nil {
		return 0
	}
	reachable := map[*Block]bool{}
	var visit func(*Block)
	visit = func(b *Block) {
		if b == nil || reachable[b] {
			return
		}
		reachable[b] = true
		for _, s := range b.Succs() {
			visit(s)
		}
	}
	visit(f.Entry)

	out := f.Blocks[:0]
	var removed int
	for _, b := range f.Blocks {
		if reachable[b] {
			out = append(out, b)
			continue
		}
		removed++
		// Sever dead-block → live-successor edges.
		for _, s := range b.Succs() {
			if reachable[s] {
				removePred(s, b)
			}
		}
	}
	for i := len(out); i < len(f.Blocks); i++ {
		f.Blocks[i] = nil
	}
	f.Blocks = out
	return removed
}
