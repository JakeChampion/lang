// Copy propagation through locals.
//
// FuseTee collapses adjacent `store X; load X` to a single
// OpTeeLocal X, which keeps the value on the operand stack while
// also writing it to the slot. When that slot is never read
// elsewhere in the function the write is dead — the tee's effect
// reduces to "pop the value, push it back", which is identity. This
// pass spots those tees and drops them outright. The motivating
// shape comes from the inliner: a single-use callee param round-
// trips through a fresh slot only because the inliner had to bind
// the arg somewhere; once the bind round-trip fuses to a tee and
// the body's only use of the param has been folded into the tee
// itself, no later op references the slot at all.
//
// Concrete payoff: `dbl(7)` (where `dbl(x) { return x * 2; }`)
// pipes through the optimisations as
//
//   inline:    const 7 ; store ; load ; const 2 ; mul ; return
//   fuse-tee:  const 7 ; tee   ;        const 2 ; mul ; return
//   copy-prop: const 7 ;                const 2 ; mul ; return
//   fold:      const 14 ;                              ; return
//
// where without copy-prop the trailing const-then-tee leaves Fold
// nothing to do. The slot stays declared in the function's
// ScratchTypes — the WAT/asm emitter's `(local …)` declaration is
// harmless dead weight, and tightening that up belongs to a future
// "shrink ScratchTypes when slots become unused" sweep.
//
// The pass also drops dead OpStoreLocal sites (no loads or tees of
// the slot exist anywhere) by rewriting them to OpDrop, since the
// store still has to consume its operand. Without that the operand
// stack would imbalance.

package ir

// PropagateCopies eliminates OpTeeLocal / OpStoreLocal sites whose
// slot is unused elsewhere in the function. Functions without an
// eligible site are unchanged.
// Returns whether any function's op list changed (see Fold — #4377 slice 1b).
func PropagateCopies(prog *Program) bool {
	ptrW := prog.PtrW
	if ptrW == 0 {
		ptrW = 4
	}
	changed := false
	for _, fn := range prog.Funcs {
		next := propagateCopiesOps(fn, fn.Ops, ptrW)
		if !opsEqual(next, fn.Ops) {
			fn.Ops = next
			changed = true
		}
	}
	return changed
}

// slotIsTwoWord reports whether slot `idx` in `fn` materialises as two
// operand-stack values. Used by the dead-store rewrite below: a dead
// OpStoreLocal that targets a two-word slot has to pop both halves, not
// one, so the replacement drop must carry `Width: WidthString`.
func slotIsTwoWord(fn *Func, idx int32, ptrW int) bool {
	return TypeIsTwoWord(slotTypeAt(fn, idx), ptrW)
}

func propagateCopiesOps(fn *Func, ops []Op, ptrW int) []Op {
	// Pre-walk: count how many times each slot is read (OpLoadLocal),
	// stored to (OpStoreLocal), or tee'd (OpTeeLocal). The
	// per-kind split lets us tell apart "slot only ever gets a tee"
	// (drop the tee) from "slot has an explicit OpLoadLocal that
	// would observe the value" (keep the tee).
	reads := map[int32]int{}
	storeOnly := map[int32]int{}
	teeOnly := map[int32]int{}
	for _, op := range ops {
		switch op.Kind {
		case OpLoadLocal:
			reads[op.I32]++
		case OpStoreLocal:
			storeOnly[op.I32]++
		case OpTeeLocal:
			teeOnly[op.I32]++
		}
	}
	out := make([]Op, 0, len(ops))
	for _, op := range ops {
		switch op.Kind {
		case OpTeeLocal:
			// Drop the tee when the slot has no other touches.
			// The tee's pop / store / push reduces to "pop, push"
			// — the operand stack carries the value into the next
			// op untouched.
			if reads[op.I32] == 0 && storeOnly[op.I32] == 0 && teeOnly[op.I32] == 1 {
				continue
			}
		case OpStoreLocal:
			// A store to a slot that nothing reads is dead. Replace
			// with OpDrop so the popped operand still leaves the
			// stack — without that the stack would imbalance after
			// the next op tries to consume the now-missing input.
			// Two-word slots inherit `Width: WidthString` so the
			// backend drops both halves — two `drop`s on wasm, a
			// two-slot `sp` bump on arm64.
			if reads[op.I32] == 0 && storeOnly[op.I32] == 1 && teeOnly[op.I32] == 0 {
				w := 0
				if slotIsTwoWord(fn, op.I32, ptrW) {
					w = WidthString
				}
				out = append(out, Op{Kind: OpDrop, Width: w, Pos: op.Pos})
				continue
			}
		}
		out = append(out, op)
	}
	return out
}
