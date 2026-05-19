package ssa

// DomTree is the immediate-dominator relationship over a Func's
// blocks. `Idom[b] == d` reads "d is the immediate dominator of
// b" — every path from the entry to b passes through d, and no
// other dominator of b sits between them. The entry block is
// its own idom by convention (matches Click et al.).
//
// Unreachable blocks (no path from entry) are absent from Idom.
// Callers that walk Func.Blocks should skip blocks for which
// `_, ok := Idom[b]` is false.
type DomTree struct {
	// Idom maps each reachable block to its immediate dominator.
	Idom map[*Block]*Block
	// rpo is reverse-postorder of reachable blocks, with the
	// entry first. Cached so callers running tree-shaped
	// analyses don't recompute it.
	rpo []*Block
}

// RPO returns the cached reverse-postorder walk of reachable
// blocks (entry first). Callers must not mutate the slice.
func (d *DomTree) RPO() []*Block { return d.rpo }

// Dominates reports whether `a` dominates `b` (a appears on
// every entry-to-b path). Reflexive: a dominates itself. Walks
// up the idom chain from b — O(depth) per call.
func (d *DomTree) Dominates(a, b *Block) bool {
	if a == nil || b == nil {
		return false
	}
	for cur := b; cur != nil; {
		if cur == a {
			return true
		}
		next, ok := d.Idom[cur]
		if !ok || next == cur {
			// hit the entry's self-loop or an unreachable node
			return cur == a
		}
		cur = next
	}
	return false
}

// BuildDomTree constructs the dominator tree for `f` using
// Cooper/Harvey/Kennedy's iterative algorithm ("A Simple, Fast
// Dominance Algorithm", TR-06-33870, 2006). The algorithm
// converges in O(N²) worst case but is near-linear on real
// CFGs; we don't bother with the Lengauer-Tarjan complexity
// reduction because compile-time SSA functions are small.
//
// Algorithm sketch:
//  1. Compute reverse-postorder (RPO) of reachable blocks.
//     Entry is at index 0.
//  2. Initialise idom[entry] = entry; idom[other] = nil.
//  3. Iterate in RPO order (skip entry): for each block b,
//     walk its Preds list, take the first one with a known
//     idom, and intersect it with every other already-known
//     predecessor idom. The intersection of two nodes a and b
//     in a dominator tree is their lowest common ancestor —
//     found by walking up each chain until they meet, using
//     RPO indices as a height proxy.
//  4. Repeat until no idom changes. On real-world CFGs this
//     converges in 1–3 passes.
func BuildDomTree(f *Func) *DomTree {
	d := &DomTree{Idom: map[*Block]*Block{}}
	if f == nil || f.Entry == nil {
		return d
	}

	d.rpo = reversePostorder(f.Entry)
	rpoIndex := map[*Block]int{}
	for i, b := range d.rpo {
		rpoIndex[b] = i
	}

	d.Idom[f.Entry] = f.Entry

	changed := true
	for changed {
		changed = false
		// Skip entry (index 0); it's its own idom.
		for _, b := range d.rpo[1:] {
			var newIdom *Block
			for _, p := range b.Preds {
				if _, ok := d.Idom[p]; !ok {
					continue // not yet processed
				}
				if newIdom == nil {
					newIdom = p
					continue
				}
				newIdom = intersect(p, newIdom, d.Idom, rpoIndex)
			}
			if newIdom == nil {
				continue
			}
			if cur, ok := d.Idom[b]; !ok || cur != newIdom {
				d.Idom[b] = newIdom
				changed = true
			}
		}
	}

	return d
}

// intersect finds the lowest common ancestor of b1 and b2 in
// the partially-built dominator tree, using RPO indices as a
// height proxy. Lower RPO index == higher in the tree; we walk
// the deeper finger up until both pointers meet.
func intersect(b1, b2 *Block, idom map[*Block]*Block, rpo map[*Block]int) *Block {
	for b1 != b2 {
		for rpo[b1] > rpo[b2] {
			next := idom[b1]
			if next == nil || next == b1 {
				break
			}
			b1 = next
		}
		for rpo[b2] > rpo[b1] {
			next := idom[b2]
			if next == nil || next == b2 {
				break
			}
			b2 = next
		}
		// Guard against the rare case where the two fingers
		// can't climb further (e.g. partial idom map mid-
		// iteration). Bail out so we don't infinite-loop.
		if rpo[b1] == rpo[b2] && b1 != b2 {
			return b1
		}
	}
	return b1
}

// reversePostorder returns the blocks reachable from `entry` in
// reverse-postorder (entry first, then deeper-into-the-graph
// blocks in the order DFS finished them, reversed). This is
// the natural traversal for dataflow analyses that propagate
// information forward — every block is visited after at least
// one of its preds (except for back-edges into loop headers).
func reversePostorder(entry *Block) []*Block {
	if entry == nil {
		return nil
	}
	visited := map[*Block]bool{}
	var post []*Block
	var visit func(*Block)
	visit = func(b *Block) {
		if b == nil || visited[b] {
			return
		}
		visited[b] = true
		for _, s := range b.Succs() {
			visit(s)
		}
		post = append(post, b)
	}
	visit(entry)
	// Reverse in place.
	for i, j := 0, len(post)-1; i < j; i, j = i+1, j-1 {
		post[i], post[j] = post[j], post[i]
	}
	return post
}
