package ssa

import (
	"math/bits"
	"sort"
)

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

// bitset is a set of Value IDs, one bit per ID, so the dataflow fixpoint
// below unions and subtracts whole words rather than walking map entries.
type bitset []uint64

func (s bitset) set(id int32)      { s[id>>6] |= 1 << (uint(id) & 63) }
func (s bitset) has(id int32) bool { return s[id>>6]&(1<<(uint(id)&63)) != 0 }

// toMap lists the set's members as the map shape the callers read.
func (s bitset) toMap() map[int32]bool {
	m := map[int32]bool{}
	for w, word := range s {
		for word != 0 {
			m[int32(w*64+bits.TrailingZeros64(word))] = true
			word &= word - 1
		}
	}
	return m
}

// blockLocal holds the per-block sets that don't change across the dataflow
// fixpoint: the upward-exposed uses, the defs, and the phi bookkeeping.
type blockLocal struct {
	uses    bitset // upward-exposed: used before any def in this block
	defs    bitset // every value defined in this block (incl. phi results)
	phiDefs bitset // just the phi results of this block
	// phiUse[predIndex] is the set of values this block's phis pull from the
	// predecessor at that index (i.e. live-out contributions on that edge).
	phiUse []bitset
}

// ComputeLiveness runs SSA-aware backward dataflow to a fixpoint and returns
// the per-block live sets. Deterministic: iteration order is the function's
// block slice order, and the result depends only on the CFG + def/use graph.
func ComputeLiveness(f *Func) *Liveness {
	return ComputeLivenessWithDependencies(f, nil)
}

// ComputeLivenessWithDependencies adds lifetime dependencies to every use of a
// value, including terminator uses and phi uses on predecessor edges. The
// original use is always preserved. Each supplied slice must already include
// transitive dependencies, and its values must be available wherever the key
// value is used. The caller owns verification of these semantic obligations.
// This does not mutate the graph or by itself certify ownership or uniqueness.
func ComputeLivenessWithDependencies(f *Func, dependencies map[int32][]Value) *Liveness {
	words := int(maxValueID(f, dependencies))/64 + 1
	index := make(map[*Block]int, len(f.Blocks))
	local := make([]*blockLocal, len(f.Blocks))
	for i, b := range f.Blocks {
		index[b] = i
		local[i] = computeBlockLocalWithDependencies(b, dependencies, words)
	}

	liveIn := make([]bitset, len(f.Blocks))
	liveOut := make([]bitset, len(f.Blocks))
	for i := range f.Blocks {
		liveIn[i] = make(bitset, words)
		liveOut[i] = make(bitset, words)
	}

	// Backward dataflow. Iterate over all blocks until no set changes.
	// Processing in reverse block order converges in few passes for the
	// reducible CFGs the lifter produces, but correctness does not depend on
	// the order — only the fixpoint does.
	for changed := true; changed; {
		changed = false
		for i := len(f.Blocks) - 1; i >= 0; i-- {
			b := f.Blocks[i]
			lb := local[i]

			// liveOut(B) = ∪ over successors S of the values live across the
			// edge B→S: (liveIn(S) minus S's phi results) plus the phi args
			// that S pulls from B.
			out := liveOut[i]
			for _, s := range b.Succs() {
				si := index[s]
				ls := local[si]
				in := liveIn[si]
				var edge bitset
				if pi := predIndex(s, b); pi >= 0 && pi < len(ls.phiUse) {
					edge = ls.phiUse[pi]
				}
				for w := range out {
					next := out[w] | (in[w] &^ ls.phiDefs[w])
					if edge != nil {
						next |= edge[w]
					}
					if next != out[w] {
						out[w] = next
						changed = true
					}
				}
			}

			// liveIn(B) = uses(B) ∪ (liveOut(B) \ defs(B)).
			in := liveIn[i]
			for w := range in {
				next := in[w] | lb.uses[w] | (out[w] &^ lb.defs[w])
				if next != in[w] {
					in[w] = next
					changed = true
				}
			}
		}
	}

	l := &Liveness{
		f:       f,
		LiveIn:  make(map[*Block]map[int32]bool, len(f.Blocks)),
		LiveOut: make(map[*Block]map[int32]bool, len(f.Blocks)),
	}
	for i, b := range f.Blocks {
		l.LiveIn[b] = liveIn[i].toMap()
		l.LiveOut[b] = liveOut[i].toMap()
	}
	return l
}

// maxValueID returns the largest Value ID the dataflow can meet in f: the
// values its ops define and use, and the dependencies attached to them.
func maxValueID(f *Func, dependencies map[int32][]Value) int32 {
	max := f.nextValueID
	note := func(v Value) {
		if v.ID > max {
			max = v.ID
		}
	}
	for _, b := range f.Blocks {
		for _, op := range b.Ops {
			note(op.Result)
			note(op.Result2)
			for _, a := range op.Args {
				note(a)
			}
		}
		for _, v := range termUses(b.Term) {
			note(v)
		}
	}
	for _, ds := range dependencies {
		for _, d := range ds {
			note(d)
		}
	}
	return max
}

// computeBlockLocalWithDependencies builds the fixpoint-invariant sets for one block.
func computeBlockLocalWithDependencies(b *Block, dependencies map[int32][]Value, words int) *blockLocal {
	lb := &blockLocal{
		uses:    make(bitset, words),
		defs:    make(bitset, words),
		phiDefs: make(bitset, words),
		phiUse:  make([]bitset, len(b.Preds)),
	}
	for i := range lb.phiUse {
		lb.phiUse[i] = make(bitset, words)
	}
	use := func(set bitset, v Value, local bool) {
		if v.IsValid() && (!local || !lb.defs.has(v.ID)) {
			set.set(v.ID)
		}
		for _, d := range dependencies[v.ID] {
			if d.IsValid() && (!local || !lb.defs.has(d.ID)) {
				set.set(d.ID)
			}
		}
	}

	for _, op := range b.Ops {
		if op.Kind == OpPhi {
			// Phi result is a def of this block; phi args are edge-uses of the
			// matching predecessor, never local uses of this block.
			for pi, a := range op.Args {
				if a.IsValid() && pi < len(lb.phiUse) {
					use(lb.phiUse[pi], a, false)
				}
			}
			if op.Result.IsValid() {
				lb.defs.set(op.Result.ID)
				lb.phiDefs.set(op.Result.ID)
			}
			continue
		}
		// Non-phi op: an arg used before it has been defined locally is
		// upward-exposed.
		for _, a := range op.Args {
			use(lb.uses, a, true)
		}
		if op.Result.IsValid() {
			lb.defs.set(op.Result.ID)
		}
		if op.Result2.IsValid() {
			lb.defs.set(op.Result2.ID)
		}
	}

	// Terminator operands are used at the end of the block.
	for _, v := range termUses(b.Term) {
		use(lb.uses, v, true)
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
