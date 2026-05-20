package ssa

// Reachable returns the set of Blocks reachable from `f.Entry`
// via the CFG. Membership testing is via map lookup:
//
//	r := Reachable(f)
//	if r[b] { /* b is live */ }
//
// Iterative DFS, O(blocks + edges). The dom-tree and prune
// passes also need this; exposing it lets custom analyses
// share the result without re-walking.
//
// Returns an empty map (not nil) for a nil or entry-less
// Func so callers can range over the result safely.
func Reachable(f *Func) map[*Block]bool {
	out := map[*Block]bool{}
	if f == nil || f.Entry == nil {
		return out
	}
	stack := []*Block{f.Entry}
	for len(stack) > 0 {
		b := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if out[b] {
			continue
		}
		out[b] = true
		for _, s := range b.Succs() {
			if s != nil && !out[s] {
				stack = append(stack, s)
			}
		}
	}
	return out
}

// IsReachable reports whether `b` is reachable from `f.Entry`.
// Convenience wrapper over Reachable for callers that only
// need a yes/no for one block — uses BFS that bails early on
// the first hit rather than enumerating everything.
func IsReachable(f *Func, b *Block) bool {
	if f == nil || f.Entry == nil || b == nil {
		return false
	}
	if b == f.Entry {
		return true
	}
	seen := map[*Block]bool{f.Entry: true}
	stack := []*Block{f.Entry}
	for len(stack) > 0 {
		cur := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		for _, s := range cur.Succs() {
			if s == b {
				return true
			}
			if s != nil && !seen[s] {
				seen[s] = true
				stack = append(stack, s)
			}
		}
	}
	return false
}
