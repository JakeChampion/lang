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
// The tree is an immutable analysis snapshot. Rebuild after changing CFG edges;
// callers must not mutate Idom or RPO. Concurrent read-only queries are safe.
type DomTree struct {
	// Idom maps each reachable block to its immediate dominator.
	Idom map[*Block]*Block
	// rpo is reverse-postorder of reachable blocks, with the
	// entry first. Cached so callers running tree-shaped
	// analyses don't recompute it.
	rpo []*Block
	// Reuse construction's block index for dense dominator-tree DFS intervals.
	// Block.ID is not an identity: other functions can reuse the same numbers.
	index map[*Block]int
	spans []domSpan
}

type domSpan struct{ start, end int }

// RPO returns the cached reverse-postorder walk of reachable
// blocks (entry first). Callers must not mutate the slice.
func (d *DomTree) RPO() []*Block { return d.rpo }

// Dominates reports whether `a` dominates `b` (a appears on
// every entry-to-b path). It is O(1), using subtree interval containment.
// Preserve reflexivity for every non-nil block, including unreachable blocks;
// distinct unreachable or foreign blocks do not dominate one another.
func (d *DomTree) Dominates(a, b *Block) bool {
	if a == nil || b == nil {
		return false
	}
	if a == b {
		return true
	}
	ai, aok := d.index[a]
	bi, bok := d.index[b]
	if !aok || !bok {
		return false
	}
	as, bs := d.spans[ai], d.spans[bi]
	return as.start <= bs.start && bs.start < as.end
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
	d.Idom[f.Entry] = f.Entry
	// A singleton tree has no non-reflexive dominance relations to index.
	if len(d.rpo) == 1 {
		return d
	}
	rpoIndex := map[*Block]int{}
	for i, b := range d.rpo {
		rpoIndex[b] = i
	}
	d.index = rpoIndex

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

	d.indexSubtrees()
	return d
}

// Index the completed tree, not the CFG's traversal order: a dominator subtree
// need not be contiguous in CFG RPO. Child/sibling links share one scratch arena.
// Iterative traversal avoids a second depth-proportional call stack. Every edge
// is followed a constant number of times, so this adds O(N) construction work.
func (d *DomTree) indexSubtrees() {
	n := len(d.rpo)
	d.spans = make([]domSpan, n)
	links := make([]int, 2*n)
	children, siblings := links[:n], links[n:]
	for i, block := range d.rpo[1:] {
		child := i + 1
		parent := d.index[d.Idom[block]]
		siblings[child] = children[parent]
		children[parent] = child // Zero is the root, never a child.
	}
	node, clock := 0, 0
	for {
		d.spans[node].start = clock
		clock++
		if child := children[node]; child != 0 {
			node = child
			continue
		}
		for {
			d.spans[node].end = clock
			if sibling := siblings[node]; sibling != 0 {
				node = sibling
				break
			}
			if node == 0 {
				return
			}
			node = d.index[d.Idom[d.rpo[node]]]
		}
	}
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
