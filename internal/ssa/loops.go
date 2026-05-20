package ssa

// Loop describes a natural loop in a Func's CFG.
//
//   - Header: the block where every iteration begins. Always
//     dominates every other block in the loop.
//   - Body: every block in the loop, including the header
//     and the tails of all back-edges. Use Body[Header] for
//     a quick header check.
//   - BackEdges: the (tail, header) pairs that define this
//     loop. Most loops have exactly one back-edge; multiple
//     back-edges sharing a header (e.g. `continue` inside a
//     nested switch) get merged into one Loop.
//
// Loops are nested via dominance — if Loop A's header is
// dominated by Loop B's header, A is nested inside B. Callers
// that need nesting structure can sort Loops by dominator
// depth.
type Loop struct {
	Header    *Block
	Body      map[*Block]bool
	BackEdges []BackEdge
}

// BackEdge is a CFG edge whose target dominates its source.
// Back-edges define natural loops (Aho/Sethi/Ullman 9.6.6).
type BackEdge struct {
	Tail   *Block // source of the back-edge (within the loop body)
	Header *Block // target of the back-edge (the loop header)
}

// Loops returns every natural loop in `f`. Empty slice for a
// non-loopy function. Cost is O(blocks · avg-loop-size) — the
// natural-loop computation walks Preds backward from each
// back-edge tail until it hits the header.
//
// Algorithm (Aho/Sethi/Ullman §9.6.6):
//  1. Build the dom tree.
//  2. For each CFG edge (s → t), check if t dominates s.
//     If so, it's a back-edge and t is a loop header.
//  3. The natural loop with header t is the set of blocks
//     that can reach s without going through t (plus t
//     itself). Compute via reverse BFS on Preds, stopping
//     at t.
//  4. Merge back-edges that share a header into one Loop.
//
// Unreachable blocks are skipped — they can't contribute to
// any natural loop.
func Loops(f *Func) []*Loop {
	if f == nil || f.Entry == nil {
		return nil
	}
	dom := BuildDomTree(f)
	byHeader := map[*Block]*Loop{}

	for _, b := range f.Blocks {
		// Skip blocks the dom tree didn't reach.
		if _, ok := dom.Idom[b]; !ok && b != f.Entry {
			continue
		}
		for _, succ := range b.Succs() {
			if succ == nil {
				continue
			}
			// Self-loop: succ == b. Also a back-edge (b dominates itself).
			if succ == b || dom.Dominates(succ, b) {
				lp, ok := byHeader[succ]
				if !ok {
					lp = &Loop{Header: succ, Body: map[*Block]bool{succ: true}}
					byHeader[succ] = lp
				}
				lp.BackEdges = append(lp.BackEdges, BackEdge{Tail: b, Header: succ})
				addToLoopBody(lp, b)
			}
		}
	}

	if len(byHeader) == 0 {
		return nil
	}
	out := make([]*Loop, 0, len(byHeader))
	for _, lp := range byHeader {
		out = append(out, lp)
	}
	return out
}

// addToLoopBody walks Preds backwards from `tail`, stopping
// at the header, and adds every visited block to `lp.Body`.
// Idempotent — re-adding a block is a no-op.
func addToLoopBody(lp *Loop, tail *Block) {
	stack := []*Block{tail}
	for len(stack) > 0 {
		b := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if lp.Body[b] {
			continue
		}
		lp.Body[b] = true
		if b == lp.Header {
			continue
		}
		for _, p := range b.Preds {
			if p != nil && !lp.Body[p] {
				stack = append(stack, p)
			}
		}
	}
}

// IsLoopHeader reports whether `b` is the header of any loop
// in `f`. Convenience for callers that have a Block in hand
// and need a yes/no.
func IsLoopHeader(f *Func, b *Block) bool {
	for _, lp := range Loops(f) {
		if lp.Header == b {
			return true
		}
	}
	return false
}
