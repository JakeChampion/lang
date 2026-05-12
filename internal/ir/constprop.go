// Constant propagation through locals.
//
// Tracks which slots are bound to a known constant (OpConstI32 /
// OpConstF32 / OpConstStr / OpConstFunc) and rewrites later
// OpLoadLocal sites of those slots to inline the constant. The
// shapes this catches that the existing FuseTee + PropagateCopies
// pair misses:
//
//   - Non-adjacent store / load:
//       const 7 ; store 0 ; <other ops> ; load 0 ; const 3 ; add
//     FuseTee can't fire (the store and load aren't neighbours), so
//     the load survives; ConstPropagate replaces it with `const 7`,
//     Fold then collapses `const 7 ; const 3 ; add` to const 10.
//   - Multi-load slots:
//       const 7 ; tee 0 ; <expr1 using load 0> ; <expr2 using load 0>
//     PropagateCopies keeps the tee (slot 0 has multiple reads), but
//     each load can be inlined as the constant — Fold then collapses
//     each enclosing arithmetic expression. A follow-up
//     PropagateCopies sweep notices the tee no longer has any reads
//     and drops it.
//
// The tracking is conservative: any control-flow op (block / loop /
// if / else / end / br / br_if) clears the entire constant table
// because the next reachable op might come from a different
// straight-line predecessor with a different value in the slot.
// More aggressive merge analysis would let constants flow across
// joined branches when both arms agree; deferred. Calls do NOT
// invalidate slots — slots are private to their function in this IR
// (no aliasing through pointers), so even an OpCallDirect leaves
// callers' slot bindings intact.

package ir

// ConstPropagate replaces every OpLoadLocal of a slot known to hold
// a constant with a fresh copy of that constant op. Programs whose
// slots never see a constant write are unchanged.
func ConstPropagate(prog *Program) {
	for _, fn := range prog.Funcs {
		fn.Ops = constPropOps(fn.Ops)
	}
}

func constPropOps(ops []Op) []Op {
	out := make([]Op, 0, len(ops))
	// consts[slot] holds the const op currently bound to that slot,
	// or is absent when the slot's value isn't statically known.
	consts := map[int32]Op{}

	clearAll := func() {
		// Reset on control-flow boundaries — entering or leaving a
		// scope, or branching, makes any prior straight-line
		// reasoning unsound.
		for k := range consts {
			delete(consts, k)
		}
	}

	for _, op := range ops {
		switch op.Kind {
		case OpBlock, OpLoop, OpIf, OpElse, OpEnd, OpBr, OpBrIf:
			clearAll()
			out = append(out, op)
		case OpStoreLocal, OpTeeLocal:
			// Look at the just-emitted previous op. If it's a
			// constant, the slot now holds that exact value.
			// Otherwise the binding becomes opaque.
			if len(out) > 0 && isConstOp(out[len(out)-1].Kind) {
				consts[op.I32] = out[len(out)-1]
			} else {
				delete(consts, op.I32)
			}
			out = append(out, op)
		case OpLoadLocal:
			if c, ok := consts[op.I32]; ok {
				replacement := c
				replacement.Pos = op.Pos
				out = append(out, replacement)
				continue
			}
			out = append(out, op)
		default:
			out = append(out, op)
		}
	}
	return out
}

// isConstOp reports whether op produces a single scalar / pointer
// value with no side effects — the values that are safe to copy
// across substitution sites.
func isConstOp(k OpKind) bool {
	switch k {
	case OpConstI32, OpConstI64, OpConstF32, OpConstF64, OpConstStr, OpConstFunc:
		return true
	}
	return false
}
