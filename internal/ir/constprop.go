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
//   - Constants flowing INTO control-flow scopes:
//       const 0 ; store 0 ; if cond ; load 0 ; …  // load 0 → const 0
//     A scope-stack tracks per-scope entry snapshots and merges at
//     the matching OpEnd: an OpIf with an OpElse intersects the
//     then-end and else-end states; a then-only OpIf intersects
//     then-end with the entry snapshot (the "branch not taken"
//     path). An OpBlock merges the linear OpEnd state with the
//     state at every OpBr / OpBrIf that targeted it. OpLoop is
//     conservative — the entry state clears so the loop body can't
//     assume any prior binding, and at OpEnd any slot written
//     inside the loop is invalidated. The result: an inlined-
//     parameter constant survives across the wrapper OpBlock and
//     the function-body if-chain, exposing `if (keyKind == 0)`
//     style guards to Fold's pruneConstIf.
//
// Tracking remains conservative outside of the merges described
// above: an OpBr / OpBrIf that targets an OpLoop scope doesn't
// contribute to the post-loop state (the back-edge restarts the
// loop body, not the continuation), and after a straight OpBr the
// linearly-next slot table is cleared because reachable execution
// only resumes at the merge point. Calls do NOT invalidate slots
// — slots are private to their function in this IR (no aliasing
// through pointers), so even an OpCallDirect leaves callers' slot
// bindings intact.

package ir

// ConstPropagate replaces every OpLoadLocal of a slot known to hold
// a constant with a fresh copy of that constant op. Programs whose
// slots never see a constant write are unchanged.
func ConstPropagate(prog *Program) {
	for _, fn := range prog.Funcs {
		fn.Ops = constPropOps(fn.Ops)
	}
}

// scopeFrame tracks the analyzer's knowledge inside a structured
// control-flow scope. `pre` is a snapshot of the slot-table at
// scope entry; `thenEnd` is captured at OpElse so the analyzer can
// merge then-arm and else-arm endings at OpEnd; `brStates` records
// the slot-table at every OpBr / OpBrIf that targets this scope so
// the post-end merge can intersect them in. `written` is the union
// of slots assigned anywhere inside the scope — used by OpLoop's
// conservative end-of-scope cleanup.
type scopeFrame struct {
	kind     OpKind
	pre      map[int32]Op
	thenEnd  map[int32]Op
	sawElse  bool
	brStates []map[int32]Op
	written  map[int32]bool
}

func constPropOps(ops []Op) []Op {
	out := make([]Op, 0, len(ops))
	consts := map[int32]Op{}
	var stack []*scopeFrame

	for _, op := range ops {
		switch op.Kind {
		case OpBlock, OpLoop, OpIf:
			sc := &scopeFrame{
				kind:    op.Kind,
				pre:     cloneConsts(consts),
				written: map[int32]bool{},
			}
			stack = append(stack, sc)
			if op.Kind == OpLoop {
				// Loop body may re-execute with values produced
				// inside the body; entering with stale outside
				// bindings would be unsound.
				consts = map[int32]Op{}
			}
			out = append(out, op)

		case OpElse:
			if n := len(stack); n > 0 && stack[n-1].kind == OpIf {
				sc := stack[n-1]
				sc.thenEnd = cloneConsts(consts)
				sc.sawElse = true
				consts = cloneConsts(sc.pre)
			}
			out = append(out, op)

		case OpEnd:
			if n := len(stack); n > 0 {
				sc := stack[n-1]
				stack = stack[:n-1]
				consts = endScope(sc, consts)
			}
			out = append(out, op)

		case OpBr:
			if depth := int(op.I32); depth < len(stack) {
				if target := stack[len(stack)-1-depth]; target.kind != OpLoop {
					target.brStates = append(target.brStates, cloneConsts(consts))
				}
			}
			// Linearly-next code is unreachable from this OpBr;
			// the merge point is the target scope's OpEnd, which
			// already factored in this state via brStates.
			consts = map[int32]Op{}
			out = append(out, op)

		case OpBrIf:
			if depth := int(op.I32); depth < len(stack) {
				if target := stack[len(stack)-1-depth]; target.kind != OpLoop {
					target.brStates = append(target.brStates, cloneConsts(consts))
				}
			}
			// Branch-not-taken keeps execution on the linear
			// path, so the slot table is preserved as-is.
			out = append(out, op)

		case OpStoreLocal, OpTeeLocal:
			if len(out) > 0 && isConstOp(out[len(out)-1].Kind) {
				consts[op.I32] = out[len(out)-1]
			} else {
				delete(consts, op.I32)
			}
			for _, sc := range stack {
				sc.written[op.I32] = true
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

// endScope computes the slot-table that survives past sc's OpEnd
// given the current straight-line state. The exact recipe depends
// on the scope kind:
//
//   - OpIf with OpElse: intersect the then-end and else-end states
//     (slot binding survives only when both branches agree on the
//     value).
//   - OpIf without OpElse: intersect the then-end state with the
//     scope's entry snapshot (the "branch not taken" path).
//   - OpBlock: take the straight-line state — no implicit alternate
//     path through the block.
//   - OpLoop: take the entry snapshot minus any slot written inside
//     the loop body (the body may have executed zero or more
//     times; a written slot is no longer trustably the entry
//     constant).
//
// Every kind also folds in brStates: each OpBr / OpBrIf that
// targeted this scope contributes its own state to the merge.
func endScope(sc *scopeFrame, current map[int32]Op) map[int32]Op {
	var merged map[int32]Op
	switch sc.kind {
	case OpIf:
		if sc.sawElse {
			merged = intersectConsts(sc.thenEnd, current)
		} else {
			merged = intersectConsts(current, sc.pre)
		}
	case OpBlock:
		merged = current
	case OpLoop:
		merged = cloneConsts(sc.pre)
		for slot := range sc.written {
			delete(merged, slot)
		}
	default:
		merged = current
	}
	for _, brSt := range sc.brStates {
		merged = intersectConsts(merged, brSt)
	}
	return merged
}

// intersectConsts keeps every slot whose binding is identical in
// both `a` and `b`. A slot present in one but absent from the
// other drops out — its post-merge value is uncertain.
func intersectConsts(a, b map[int32]Op) map[int32]Op {
	if len(a) > len(b) {
		a, b = b, a
	}
	r := make(map[int32]Op, len(a))
	for k, v := range a {
		if w, ok := b[k]; ok && constOpEqual(v, w) {
			r[k] = v
		}
	}
	return r
}

// cloneConsts returns a shallow copy of m. Op values are small
// structs with no pointer-shared sub-fields used by the const
// propagator, so a value-copy is enough to isolate later writes.
func cloneConsts(m map[int32]Op) map[int32]Op {
	c := make(map[int32]Op, len(m))
	for k, v := range m {
		c[k] = v
	}
	return c
}

// constOpEqual compares two constant ops by kind and payload — the
// fields that decide whether the value is "the same constant". Pos
// and other metadata are ignored.
func constOpEqual(a, b Op) bool {
	if a.Kind != b.Kind {
		return false
	}
	switch a.Kind {
	case OpConstI32:
		return a.I32 == b.I32
	case OpConstI64:
		return a.I64 == b.I64
	case OpConstF32:
		return a.F32 == b.F32
	case OpConstF64:
		return a.F64 == b.F64
	case OpConstStr, OpConstFunc:
		return a.Str == b.Str
	}
	return false
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
