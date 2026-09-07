// Rotate fusion: the shift/shift/or spelling of a rotate becomes OpRotr.
//
// A rotate has no surface syntax, so every Fern program that wants one
// writes it as `(x >> n) | (x << (W-n))` — the digest kernels in
// `std/crypto` do it 384 times per BLAKE2b compression. Lowered
// literally that is three instructions plus a second evaluation of `x`,
// where every target has a single-instruction rotate.
//
// The pattern this recognises, over the flat op stream:
//
//	<X> ; const n ; shr_u ; <X> ; const W-n ; shl ; or   → <X> ; const n ; rotr
//	<X> ; const n ; shl ; <X> ; const W-n ; shr_u ; or   → <X> ; const W-n ; rotr
//
// The second is a LEFT rotate by n, which is a right rotate by W-n; the
// count the rewrite keeps is always the logical-right shift's own, so
// one IR op covers both directions.
//
// <X> is any repeated PURE operand expression, not just a local read.
// The generator writes the operand out on both sides of the `|`, so
// requiring a bound temporary would mean the fusion fired nowhere in the
// stdlib as it stands — and binding one is a pessimisation on a stack
// machine, where naming a value is what puts it in memory. Matching the
// repeated form fuses the two shifts AND drops the second evaluation.
//
// Excluded on purpose:
//
//   - A SIGNED right shift. `>>` on a signed type shifts the sign bit in,
//     so `(x >> n) | (x << (W-n))` is not a rotate for negative x. Only
//     an op carrying Unsigned qualifies.
//   - n == 0 and n == W. Neither is the rotate this pattern spells: the
//     partner shift's count would be W or 0, which the targets mask back
//     to 0, and the shape only happens to agree by accident. Both are
//     rejected rather than relied on.
//   - Mismatched widths between the two shifts, and any count pair that
//     does not sum to exactly the shift width.
//   - Anything impure in <X> — a call, a store, a memory read. Fusing
//     deletes the second evaluation, which is only sound when evaluating
//     it twice and once are the same thing.

package ir

import "github.com/jakechampion/lang/internal/ast"

// FuseRotates rewrites every recognised shift/shift/or rotate in prog
// into OpRotr. Reports whether any function changed.
func FuseRotates(prog *Program) bool {
	changed := false
	for _, fn := range prog.Funcs {
		if next := fuseRotatesIn(fn); !opsEqual(next, fn.Ops) {
			fn.Ops = next
			changed = true
		}
	}
	return changed
}

// fuseRotatesIn returns fn's op list with the rotates fused. The scan
// runs left to right and restarts matching after each rewrite, so a
// rotate whose operand is itself a rotate fuses on a later round of the
// cleanup fixpoint.
func fuseRotatesIn(fn *Func) []Op {
	ops := fn.Ops
	out := make([]Op, 0, len(ops))
	for i := 0; i < len(ops); i++ {
		if ops[i].Kind != OpOr {
			out = append(out, ops[i])
			continue
		}
		// The `or`'s operands end at i-1; matching walks backwards
		// over what has already been copied to `out`, because an
		// earlier rewrite may have changed it.
		start, repl, ok := matchRotate(fn, out, ops[i])
		if !ok {
			out = append(out, ops[i])
			continue
		}
		out = append(out[:start], repl...)
	}
	return out
}

// matchRotate tests whether the ops copied so far end in the two shifted
// halves of a rotate. It returns the index in `done` where the pattern
// starts and the ops that replace everything from there on.
func matchRotate(fn *Func, done []Op, or Op) (int, []Op, bool) {
	// Trailing half: <X> ; const m ; shiftB.
	hiStart, hiCount, hiShift, ok := matchShiftedOperand(fn, done, len(done))
	if !ok {
		return 0, nil, false
	}
	// Leading half: <X> ; const n ; shiftA, ending where the trailing
	// half begins.
	loStart, loCount, loShift, ok := matchShiftedOperand(fn, done, hiStart)
	if !ok {
		return 0, nil, false
	}
	// One half left, one half logical-right — in either order.
	var shrCount Op
	switch {
	case loShift.Kind == OpShrS && loShift.Unsigned && hiShift.Kind == OpShl:
		shrCount = loCount
	case loShift.Kind == OpShl && hiShift.Kind == OpShrS && hiShift.Unsigned:
		shrCount = hiCount
	default:
		return 0, nil, false
	}
	w, ok := shiftWidth(loShift)
	if !ok {
		return 0, nil, false
	}
	if hw, hok := shiftWidth(hiShift); !hok || hw != w {
		return 0, nil, false
	}
	if ow, ook := shiftWidth(or); !ook || ow != w {
		return 0, nil, false
	}
	n, ok := constValue(loCount)
	if !ok {
		return 0, nil, false
	}
	m, ok := constValue(hiCount)
	if !ok {
		return 0, nil, false
	}
	// n == 0 / n == W are not this rotate: reject rather than lean on
	// the targets masking the partner count back into range.
	if n <= 0 || m <= 0 || n >= int64(w) || m >= int64(w) || n+m != int64(w) {
		return 0, nil, false
	}
	// Both halves must shift the SAME value.
	lo := done[loStart : hiStart-2]
	hi := done[hiStart : len(done)-2]
	if !pureOperandsEqual(lo, hi) {
		return 0, nil, false
	}
	repl := make([]Op, 0, len(lo)+2)
	repl = append(repl, lo...)
	repl = append(repl, shrCount)
	repl = append(repl, Op{Kind: OpRotr, Width: loShift.Width, Pos: loShift.Pos})
	return loStart, repl, true
}

// matchShiftedOperand recognises `<pure X> ; const k ; shift` ending
// immediately before `end`, returning where X starts plus the const and
// shift ops.
func matchShiftedOperand(fn *Func, done []Op, end int) (int, Op, Op, bool) {
	if end < 3 {
		return 0, Op{}, Op{}, false
	}
	shift := done[end-1]
	if shift.Kind != OpShl && shift.Kind != OpShrS {
		return 0, Op{}, Op{}, false
	}
	count := done[end-2]
	if _, ok := constValue(count); !ok {
		return 0, Op{}, Op{}, false
	}
	start, ok := pureOperandStart(fn, done, end-2)
	if !ok {
		return 0, Op{}, Op{}, false
	}
	return start, count, shift, true
}

// pureOperandStart walks back from `end` (exclusive) over pure ops until
// exactly one value is left on the operand stack, returning the index
// the expression starts at. It bails on anything not on the pure list —
// which includes every control-flow op, so the span can never straddle a
// block boundary.
func pureOperandStart(fn *Func, ops []Op, end int) (int, bool) {
	need := 1
	for k := end - 1; k >= 0; k-- {
		pops, pushes, ok := pureStackEffect(fn, ops[k])
		if !ok {
			return 0, false
		}
		need = need - pushes + pops
		if need < 0 {
			return 0, false
		}
		if need == 0 {
			return k, true
		}
	}
	return 0, false
}

// pureStackEffect is the operand-stack effect of an op the fusion is
// willing to delete a second evaluation of: no side effect, no trap, and
// the same answer however many times it runs.
//
// Memory reads and division are deliberately absent. A read is only as
// pure as the store that has not happened yet, and div/rem trap on a
// zero divisor — neither is needed by any rotate the stdlib writes, and
// admitting them would put the burden of proof on this list.
func pureStackEffect(fn *Func, op Op) (pops, pushes int, ok bool) {
	switch op.Kind {
	case OpConstI32, OpConstI64:
		return 0, 1, true
	case OpLoadLocal:
		if !singleWordIntLocal(fn, op) {
			return 0, 0, false
		}
		return 0, 1, true
	case OpAdd, OpSub, OpMul,
		OpAnd, OpOr, OpXor,
		OpShl, OpShrS, OpRotr,
		OpEq, OpNe, OpLtS, OpLeS, OpGtS, OpGeS:
		return 2, 1, true
	case OpNot, OpClz, OpCtz, OpPopcount,
		OpExtendI32S, OpExtendI32U, OpWrapI64:
		return 1, 1, true
	}
	return 0, 0, false
}

// singleWordIntLocal reports whether a local read pushes exactly one
// integer stack slot. A string under the two-word ABI pushes two and a
// float pushes a float slot; both would desynchronise the backward walk
// or compare unlike values, so neither is admitted.
func singleWordIntLocal(fn *Func, op Op) bool {
	if op.Width == WidthString {
		return false
	}
	t, ok := localTypeOf(fn, op.I32)
	if !ok {
		return false
	}
	if _, isFloat := t.(ast.FloatType); isFloat {
		return false
	}
	return !TypeIsTwoWordABI(t, fn.PtrW, fn.TwoWordStr)
}

// localTypeOf is the declared type of fn's local slot idx, over the one
// flat index space of params, then declared locals, then scratch slots.
func localTypeOf(fn *Func, idx int32) (ast.Type, bool) {
	i := int(idx)
	if i < 0 {
		return nil, false
	}
	if i < len(fn.Params) {
		return fn.Params[i].Type, true
	}
	i -= len(fn.Params)
	if i < len(fn.Locals) {
		return fn.Locals[i].Type, true
	}
	i -= len(fn.Locals)
	if i < len(fn.ScratchTypes) {
		return fn.ScratchTypes[i], true
	}
	return nil, false
}

// pureOperandsEqual compares two operand spans for equality, ignoring
// source position: the two halves of a rotate are two textual
// occurrences of one expression, so their positions differ by
// construction.
func pureOperandsEqual(a, b []Op) bool {
	if len(a) == 0 || len(a) != len(b) {
		return false
	}
	for i := range a {
		x, y := a[i], b[i]
		if x.Kind != y.Kind || x.I32 != y.I32 || x.I64 != y.I64 ||
			x.Width != y.Width || x.Unsigned != y.Unsigned {
			return false
		}
	}
	return true
}

// constValue reads an integer constant op's value.
func constValue(op Op) (int64, bool) {
	switch op.Kind {
	case OpConstI32:
		return int64(op.I32), true
	case OpConstI64:
		return op.I64, true
	}
	return 0, false
}

// shiftWidth is the operand width an integer op works in. Zero means 32,
// matching Op.Width's default across the IR; anything else — the
// backend-resolved WidthPtr sentinel, a reserved sub-i32 width — is not
// a width this rewrite reasons about.
func shiftWidth(op Op) (int, bool) {
	switch op.Width {
	case 0, 32:
		return 32, true
	case 64:
		return 64, true
	}
	return 0, false
}
