package ssa

import "sort"

// Liveness holds per-block live-in / live-out sets, keyed by Value ID. It is
// the foundation for register allocation (#4112): a register may be reused for
// two values only when their live ranges do not overlap, and live ranges are
// derived from these sets.
//
// SSA / phi semantics. A phi in block S with predecessors P[0..n-1] reads
// Args[i] *on the edge from P[i]*, not at the phi instruction itself. So a phi
// argument is live-out of the corresponding predecessor but is NOT, by virtue
// of the phi, live-in of S; and a phi's *result* is defined in S, so it is not
// live across any edge into S. Both rules are handled below — getting them
// wrong is the classic source of regalloc miscompiles, so they are explicit.
type Liveness struct {
	f *Func
	// LiveIn[B] / LiveOut[B] are the sets of Value IDs live on entry to /
	// exit from block B.
	LiveIn  map[*Block]map[int32]bool
	LiveOut map[*Block]map[int32]bool
}

// blockLocal holds the per-block sets that don't change across the dataflow
// fixpoint: the upward-exposed uses, the defs, and the phi bookkeeping.
type blockLocal struct {
	uses    map[int32]bool // upward-exposed: used before any def in this block
	defs    map[int32]bool // every value defined in this block (incl. phi results)
	phiDefs map[int32]bool // just the phi results of this block
	// phiUse[predIndex] is the set of values this block's phis pull from the
	// predecessor at that index (i.e. live-out contributions on that edge).
	phiUse []map[int32]bool
}

// ComputeLiveness runs SSA-aware backward dataflow to a fixpoint and returns
// the per-block live sets. Deterministic: iteration order is the function's
// block slice order, and the result depends only on the CFG + def/use graph.
func ComputeLiveness(f *Func) *Liveness {
	local := make(map[*Block]*blockLocal, len(f.Blocks))
	for _, b := range f.Blocks {
		local[b] = computeBlockLocal(b)
	}

	liveIn := make(map[*Block]map[int32]bool, len(f.Blocks))
	liveOut := make(map[*Block]map[int32]bool, len(f.Blocks))
	for _, b := range f.Blocks {
		liveIn[b] = map[int32]bool{}
		liveOut[b] = map[int32]bool{}
	}

	// Backward dataflow. Iterate over all blocks until no set changes.
	// Processing in reverse block order converges in few passes for the
	// reducible CFGs the lifter produces, but correctness does not depend on
	// the order — only the fixpoint does.
	for changed := true; changed; {
		changed = false
		for i := len(f.Blocks) - 1; i >= 0; i-- {
			b := f.Blocks[i]
			lb := local[b]

			// liveOut(B) = ∪ over successors S of the values live across the
			// edge B→S: (liveIn(S) minus S's phi results) plus the phi args
			// that S pulls from B.
			out := liveOut[b]
			for _, s := range b.Succs() {
				for id := range liveIn[s] {
					if !local[s].phiDefs[id] {
						if !out[id] {
							out[id] = true
							changed = true
						}
					}
				}
				if pi := predIndex(s, b); pi >= 0 && pi < len(local[s].phiUse) {
					for id := range local[s].phiUse[pi] {
						if !out[id] {
							out[id] = true
							changed = true
						}
					}
				}
			}

			// liveIn(B) = uses(B) ∪ (liveOut(B) \ defs(B)).
			in := liveIn[b]
			for id := range lb.uses {
				if !in[id] {
					in[id] = true
					changed = true
				}
			}
			for id := range out {
				if !lb.defs[id] && !in[id] {
					in[id] = true
					changed = true
				}
			}
		}
	}

	return &Liveness{f: f, LiveIn: liveIn, LiveOut: liveOut}
}

// computeBlockLocal builds the fixpoint-invariant sets for one block.
func computeBlockLocal(b *Block) *blockLocal {
	lb := &blockLocal{
		uses:    map[int32]bool{},
		defs:    map[int32]bool{},
		phiDefs: map[int32]bool{},
		phiUse:  make([]map[int32]bool, len(b.Preds)),
	}
	for i := range lb.phiUse {
		lb.phiUse[i] = map[int32]bool{}
	}

	for _, op := range b.Ops {
		if op.Kind == OpPhi {
			// Phi result is a def of this block; phi args are edge-uses of the
			// matching predecessor, never local uses of this block.
			for pi, a := range op.Args {
				if a.IsValid() && pi < len(lb.phiUse) {
					lb.phiUse[pi][a.ID] = true
				}
			}
			if op.Result.IsValid() {
				lb.defs[op.Result.ID] = true
				lb.phiDefs[op.Result.ID] = true
			}
			continue
		}
		// Non-phi op: an arg used before it has been defined locally is
		// upward-exposed.
		for _, a := range op.Args {
			if a.IsValid() && !lb.defs[a.ID] {
				lb.uses[a.ID] = true
			}
		}
		if op.Result.IsValid() {
			lb.defs[op.Result.ID] = true
		}
		if op.Result2.IsValid() {
			lb.defs[op.Result2.ID] = true
		}
	}

	// Terminator operands are used at the end of the block.
	for _, v := range termUses(b.Term) {
		if v.IsValid() && !lb.defs[v.ID] {
			lb.uses[v.ID] = true
		}
	}
	return lb
}

// termUses returns the Values read by a terminator.
func termUses(t Terminator) []Value {
	switch t.Kind {
	case TermBrIf:
		return []Value{t.Cond}
	case TermRet:
		return []Value{t.Value}
	case TermRetPair:
		return []Value{t.Value, t.Value2}
	default:
		return nil
	}
}

// predIndex returns the position of `pred` in `b.Preds`, or -1 if absent.
// Phi Args are parallel to Preds, so this maps an edge to its phi-arg slot.
func predIndex(b *Block, pred *Block) int {
	for i, p := range b.Preds {
		if p == pred {
			return i
		}
	}
	return -1
}

// LiveInSorted / LiveOutSorted return the live sets as ascending ID slices,
// for deterministic assertions and dumps.
func (l *Liveness) LiveInSorted(b *Block) []int32  { return sortedIDs(l.LiveIn[b]) }
func (l *Liveness) LiveOutSorted(b *Block) []int32 { return sortedIDs(l.LiveOut[b]) }

func sortedIDs(m map[int32]bool) []int32 {
	if len(m) == 0 {
		return nil
	}
	out := make([]int32, 0, len(m))
	for id := range m {
		out = append(out, id)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}
