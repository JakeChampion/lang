package ssa

// DominanceFrontier returns the dominance frontier for every
// reachable block. The frontier of block `b` is the set of
// blocks `c` such that:
//
//   - b dominates a predecessor of c, AND
//   - b does NOT strictly dominate c.
//
// In plain language: b's frontier marks the "earliest" blocks
// at which b's influence ends — the join points just past the
// region b dominates. If a value is defined in b, the phi
// nodes that select between b's def and the def from a
// sibling control-flow path live in b's frontier (Cytron et
// al., "Efficiently Computing Static Single Assignment Form",
// TOPLAS 1991).
//
// Algorithm: Cooper/Harvey/Kennedy ("A Simple, Fast Dominance
// Algorithm", TR-06-33870, 2006) — same paper as the dom-tree
// builder, §3.2. For every block with ≥2 predecessors (a join
// point), walk each predecessor up the idom chain until we
// hit the block's immediate dominator; every block visited
// along the way has this join point in its frontier.
//
// O(N · avg-tree-depth) — cheap in practice.
//
// Unreachable blocks are absent from the returned map.
func DominanceFrontier(f *Func) map[*Block][]*Block {
	d := BuildDomTree(f)
	return DominanceFrontierFrom(f, d)
}

// DominanceFrontierFrom is the same as DominanceFrontier but
// reuses a pre-built DomTree. Use this when a caller already
// has the dom tree handy (e.g. an analysis pipeline that
// builds it once and feeds multiple frontier-derived
// transformations).
func DominanceFrontierFrom(f *Func, d *DomTree) map[*Block][]*Block {
	df := map[*Block][]*Block{}
	if f == nil || d == nil {
		return df
	}
	for _, b := range f.Blocks {
		if _, ok := d.Idom[b]; !ok {
			continue // unreachable
		}
		if len(b.Preds) < 2 {
			continue // join points only — single-pred blocks add nothing
		}
		bIdom := d.Idom[b]
		for _, p := range b.Preds {
			if _, ok := d.Idom[p]; !ok {
				continue
			}
			runner := p
			for runner != nil && runner != bIdom {
				df[runner] = appendUniqueBlock(df[runner], b)
				next := d.Idom[runner]
				if next == runner {
					break // entry self-loop — stop climbing
				}
				runner = next
			}
		}
	}
	return df
}

// appendUniqueBlock appends `x` if `s` doesn't already contain
// it. The frontier lists are sets-as-slices; this keeps them
// duplicate-free without paying for a map per block.
func appendUniqueBlock(s []*Block, x *Block) []*Block {
	for _, b := range s {
		if b == x {
			return s
		}
	}
	return append(s, x)
}
