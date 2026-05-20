package ssa

// RPO returns the reachable blocks of `f` in reverse-postorder
// (entry first, then deeper blocks in DFS-finish-time order
// reversed). This is the natural traversal for forward dataflow
// analyses — every block is visited after at least one of its
// predecessors, modulo back-edges into loop headers.
//
// Unreachable blocks are NOT included in the result. Combine
// with `Reachable(f)` if you need to enumerate live blocks
// separately from their walk order.
//
// `(*DomTree).RPO()` returns the cached version once the dom
// tree is built; this is the standalone entry point for
// callers that don't need the rest of the dom-tree machinery.
//
// O(blocks + edges).
func (f *Func) RPO() []*Block {
	if f == nil {
		return nil
	}
	return reversePostorder(f.Entry)
}
