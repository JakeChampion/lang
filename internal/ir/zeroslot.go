// Zero-slot uniqueness guards.
//
// Lowering emits an `is_unique`-gated drop for every droppable local at
// a scope exit, whether or not the slot was assigned on the path that
// reaches it. On the paths where it was not, the slot still holds the
// zero its prologue wrote, `is_unique(null)` is 0 by every backend
// helper's null guard, and the whole drop body the guard gates — the
// tag switch, the per-variant payload drops, the box free — is
// unreachable.
//
// ConstPropagate already delivers that zero for the straight-line and
// simply-merged cases, and Fold's `OpConstI32 0 ; OpRcIsUnique` rule
// plus pruneConstIf then delete the block. What it cannot deliver is
// the branchy case: its slot table clears at a loop entry and after a
// straight OpBr, so a fact that survives an order-based argument is
// lost by an incremental one. Over the self-host compiler that gap is
// 2,680 of the 12,109 guards a backend still sees after the full pass
// battery.
//
// This pass closes it with the argument ConstPropagate cannot make. The
// IR is structured, so outside a loop every branch is forward: a write
// later in op order cannot execute before an earlier read. A slot whose
// only earlier writes are zero-init therefore holds 0 at the guard,
// however the control flow between them is shaped.

package ir

// PruneZeroSlotGuards replaces `OpLoadLocal N ; OpRcIsUnique` with the
// `OpConstI32 0` it evaluates to, wherever slot N provably still holds
// its zero-init. It removes nothing by itself — Fold's pruneConstIf
// deletes the block the constant now conditions.
//
// The pair is replaced rather than just the load: the load pushes a
// pointer and the guard an i32, so rewriting the load alone would leave
// a pointer where an i32 is expected.
//
// Returns whether any function changed, for OptimizeCleanup's fixpoint.
func PruneZeroSlotGuards(prog *Program) bool {
	changed := false
	for _, fn := range prog.Funcs {
		if next, ok := pruneZeroSlotGuardsIn(fn); ok {
			fn.Ops = next
			changed = true
		}
	}
	return changed
}

func pruneZeroSlotGuardsIn(fn *Func) ([]Op, bool) {
	// A slot is provably zero at op i when its first non-zero write is
	// after i and it has a zero-init write before i. Both are answered
	// from one sweep so the pass stays linear in the op count.
	const never = int(^uint(0) >> 1)
	firstReal := map[int32]int{}
	firstZero := map[int32]int{}
	inLoop := make([]bool, len(fn.Ops))
	var stack []bool
	loops := 0
	for i, o := range fn.Ops {
		switch o.Kind {
		case OpBlock, OpIf:
			stack = append(stack, false)
		case OpLoop:
			stack = append(stack, true)
			loops++
		case OpEnd:
			if n := len(stack); n > 0 {
				if stack[n-1] {
					loops--
				}
				stack = stack[:n-1]
			}
		case OpStoreLocal, OpTeeLocal:
			// The prologue's zero-init is a store fed by `const.i32 0`.
			if i > 0 && fn.Ops[i-1].Kind == OpConstI32 && fn.Ops[i-1].I32 == 0 {
				if _, seen := firstZero[o.I32]; !seen {
					firstZero[o.I32] = i
				}
			} else if _, seen := firstReal[o.I32]; !seen {
				firstReal[o.I32] = i
			}
		}
		inLoop[i] = loops > 0
	}

	out := make([]Op, 0, len(fn.Ops))
	hit := false
	for i := 0; i < len(fn.Ops); i++ {
		o := fn.Ops[i]
		if o.Kind == OpLoadLocal && i+1 < len(fn.Ops) && fn.Ops[i+1].Kind == OpRcIsUnique &&
			int(o.I32) >= len(fn.Params) && !inLoop[i+1] {
			real, hasReal := firstReal[o.I32]
			if !hasReal {
				real = never
			}
			zero, hasZero := firstZero[o.I32]
			if hasZero && zero < i && real > i {
				out = append(out, Op{Kind: OpConstI32, I32: 0, Pos: fn.Ops[i+1].Pos})
				i++ // consume the guard
				hit = true
				continue
			}
		}
		out = append(out, o)
	}
	return out, hit
}
