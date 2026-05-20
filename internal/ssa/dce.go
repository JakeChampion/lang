package ssa

// DCE removes Ops whose Result Value has no consumers anywhere
// in the function. Side-effect-only Ops (Store + Call — calls
// can be impure even when their result is unused) are kept
// regardless; only pure Ops with no remaining uses get
// dropped.
//
// "Consumer" means: appears in another Op's Args, or in a
// terminator's Cond/Value. Block predecessor lists don't count
// — Block-level def-use isn't a Value-level use.
//
// Iterates to a fixed point because removing one Op can drop
// the last use of another (e.g. constfold rewrote `x + 1`
// to a const but x's original definition now has no uses).
// On real-world functions DCE typically converges in 1–2
// passes; the iteration loop is cheap-but-correct.
//
// Pairs naturally with Fold: run Fold then DCE to collapse
// constant chains. Phase 2 will add a per-Func driver that
// runs both to fixed point along with copyprop once that
// lands.
func DCE(f *Func) {
	if f == nil {
		return
	}
	for {
		uses := collectUses(f)
		changed := false
		for _, b := range f.Blocks {
			out := b.Ops[:0]
			for _, op := range b.Ops {
				if isDeadOp(op, uses) {
					changed = true
					continue
				}
				out = append(out, op)
			}
			// Zero the dropped slots so the GC can reclaim Op
			// values; defensive against accidental re-reads.
			for i := len(out); i < len(b.Ops); i++ {
				b.Ops[i] = nil
			}
			b.Ops = out
		}
		if !changed {
			return
		}
	}
}

func isDeadOp(op *Op, uses map[int32]int) bool {
	if op == nil {
		return false
	}
	if !op.Result.IsValid() {
		// Side-effect-only Op (Store etc.). Keep.
		return false
	}
	if hasSideEffect(op.Kind) {
		return false
	}
	return uses[op.Result.ID] == 0
}

// hasSideEffect identifies Op kinds whose execution can affect
// state outside their Result Value, so DCE must keep them even
// when nobody reads their result.
//
// Phase 1: Call + Load + Store. Call is conservatively impure
// (we don't have a purity analysis); Load reads memory that
// another instruction might re-read; Store writes memory by
// definition. Pure arithmetic + comparison + const Ops are
// safe to drop.
func hasSideEffect(k OpKind) bool {
	switch k {
	case OpCall, OpCallIndirect, OpLoad, OpStore,
		OpMakeClosure, OpMakeEnv:
		return true
	default:
		return false
	}
}

// collectUses tallies how many times each Value is referenced
// across the whole function (in Op.Args + terminator
// Cond/Value). A Value with count == 0 has no consumers and
// — if its def is pure — is safe to delete.
func collectUses(f *Func) map[int32]int {
	uses := map[int32]int{}
	bump := func(v Value) {
		if !v.IsValid() {
			return
		}
		uses[v.ID]++
	}
	for _, b := range f.Blocks {
		for _, op := range b.Ops {
			for _, a := range op.Args {
				bump(a)
			}
		}
		switch b.Term.Kind {
		case TermBrIf:
			bump(b.Term.Cond)
		case TermRet:
			bump(b.Term.Value)
		}
	}
	return uses
}
