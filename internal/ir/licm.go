package ir

import "github.com/jakechampion/lang/internal/ast"

// HoistLoopInvariants lifts a loop-invariant `s.len()` out of a loop's
// HEADER: the run of ops between `loop` and the first op that can branch or
// open a scope. A `while` lowers its condition there, so `while (i < s.len())`
// re-reads the length on every iteration, and reading it is not free — a
// string may be an inline (small-string) value or a heap pointer, so OpStrLen
// expands to a tag test and two arms in every backend.
//
// The header is the only region this pass will hoist FROM, and that is what
// makes the transform safe rather than speculative. `loop` falls through into
// its header, so those ops run whenever the loop is reached, whether or not
// the body ever executes. Moving one out therefore cannot introduce a read
// that the original program did not perform — the trap behaviour is identical.
// Hoisting from deeper in the body would not have that property: a loop whose
// body never runs would gain a read it never made.
//
// The hoisted ops are inserted immediately before the `loop`, so they are
// reached under exactly the same condition it is. A `br` back to the loop
// re-enters at the header and skips them, which is correct: invariance is
// checked over the whole body, so nothing in the loop can have stored to the
// operand.
func HoistLoopInvariants(prog *Program) {
	for _, fn := range prog.Funcs {
		hoistLoopInvariantsIn(fn)
	}
}

func hoistLoopInvariantsIn(fn *Func) {
	// Later loops first: rewriting one shifts every index after it, and
	// walking backwards keeps the indices of the loops still to visit valid.
	for i := len(fn.Ops) - 1; i >= 0; i-- {
		if fn.Ops[i].Kind != OpLoop {
			continue
		}
		if next, ok := hoistFromLoop(fn, i); ok {
			fn.Ops = next
		}
	}
}

// hoistFromLoop rewrites the one loop opening at loopIdx.
func hoistFromLoop(fn *Func, loopIdx int) ([]Op, bool) {
	end := matchingScopeEnd(fn.Ops, loopIdx)
	if end < 0 {
		return nil, false
	}
	hdrEnd := loopHeaderEnd(fn.Ops, loopIdx)
	stored := storedLocals(fn.Ops[loopIdx+1 : end])

	// Which locals does the header take a length of, without the body ever
	// storing to them? Insertion order, so the emitted prologue is stable.
	var order []int32
	slotFor := map[int32]int32{}
	for j := loopIdx + 1; j+1 < hdrEnd; j++ {
		if fn.Ops[j].Kind != OpLoadLocal || fn.Ops[j+1].Kind != OpStrLen {
			continue
		}
		src := fn.Ops[j].I32
		if stored[src] {
			continue
		}
		if _, seen := slotFor[src]; !seen {
			slotFor[src] = 0 // reserved below, once the count is known
			order = append(order, src)
		}
	}
	if len(order) == 0 {
		return nil, false
	}

	base := int32(len(fn.Params)) + int32(len(fn.Locals)) + int32(len(fn.ScratchTypes))
	pre := make([]Op, 0, len(order)*3)
	for n, src := range order {
		slot := base + int32(n)
		slotFor[src] = slot
		fn.ScratchTypes = append(fn.ScratchTypes, ast.NumberType{})
		// The load and the OpStrLen are moved verbatim rather than rebuilt,
		// so whatever width or flags they carry travel with them.
		pre = append(pre, Op{Kind: OpLoadLocal, I32: src}, Op{Kind: OpStrLen}, Op{Kind: OpStoreLocal, I32: slot})
	}

	out := make([]Op, 0, len(fn.Ops)+len(pre))
	out = append(out, fn.Ops[:loopIdx]...)
	out = append(out, pre...)
	out = append(out, fn.Ops[loopIdx])
	for j := loopIdx + 1; j < len(fn.Ops); j++ {
		if j+1 < hdrEnd && fn.Ops[j].Kind == OpLoadLocal && fn.Ops[j+1].Kind == OpStrLen {
			if slot, ok := slotFor[fn.Ops[j].I32]; ok && !stored[fn.Ops[j].I32] {
				out = append(out, Op{Kind: OpLoadLocal, I32: slot})
				j++ // the OpStrLen is consumed with it
				continue
			}
		}
		out = append(out, fn.Ops[j])
	}
	return out, true
}

// loopHeaderEnd is one past the last op that runs unconditionally on reaching
// the loop: the run after `loop` up to the first branch or nested scope.
func loopHeaderEnd(ops []Op, loopIdx int) int {
	j := loopIdx + 1
	for j < len(ops) && !endsLoopHeader(ops[j].Kind) {
		j++
	}
	return j
}

func endsLoopHeader(k OpKind) bool {
	switch k {
	case OpBr, OpBrIf, OpIf, OpElse, OpBlock, OpLoop, OpEnd, OpReturn, OpReturnVoid:
		return true
	}
	return false
}

// matchingScopeEnd is the index of the OpEnd closing the scope opened at open.
func matchingScopeEnd(ops []Op, open int) int {
	depth := 0
	for j := open; j < len(ops); j++ {
		switch ops[j].Kind {
		case OpBlock, OpLoop, OpIf:
			depth++
		case OpEnd:
			depth--
			if depth == 0 {
				return j
			}
		}
	}
	return -1
}

// storedLocals is every slot the ops write. A slot written anywhere in the
// loop is not invariant, wherever in the body the write sits.
func storedLocals(ops []Op) map[int32]bool {
	stored := map[int32]bool{}
	for _, o := range ops {
		switch o.Kind {
		case OpStoreLocal, OpTeeLocal:
			stored[o.I32] = true
		}
	}
	return stored
}
