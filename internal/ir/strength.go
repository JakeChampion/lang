// Strength reduction: rewrite expensive operations into cheaper
// equivalents when the operands satisfy a known shape. The rewrites
// trigger purely on adjacent IR ops — no dataflow needed — and stay
// safe under the integer semantics of the surrounding op:
//
//   - <expr> ; const 2^k ; mul   → <expr> ; const k ; shl
//     Multiplication by a power of two is a left-shift. Both wasm
//     and arm64 issue a single shift op instead of a multiply.
//   - <expr> ; const 1 ; mul     → <expr>
//   - <expr> ; const 0 ; mul     → <expr> ; drop ; const 0
//     The drop preserves any side effect <expr> carried out (a call
//     in the operand position). Without it the operand stack would
//     imbalance.
//   - <expr> ; const 0 ; add / sub / or / xor / shl / shr → <expr>
//   - <expr> ; const -1 ; and    → <expr>   (-1 is all-bits-set)
//   - <expr> ; const 0 ; and     → <expr> ; drop ; const 0
//   - <expr> ; const 1 ; div     → <expr>            (signed + unsigned)
//   - <expr> ; const 1 ; rem     → <expr> ; drop ; const 0
//   - <expr> ; const 2^k ; div_u → <expr> ; const k ; shr_u
//   - <expr> ; const 2^k ; rem_u → <expr> ; const 2^k-1 ; and
//     Unsigned division / remainder by a power of two is exact — the
//     logical shift and the low-bit mask reproduce it bit-for-bit.
//
// Every pattern applies at both i32 and i64 width: the trigger is the
// constant's own kind (OpConstI32 / OpConstI64), and the replacement
// mirrors that width (an i64 shift count is emitted as OpConstI64 so
// the wasm validator sees matching operand types).
//
// Deliberate skips:
//
//   - SIGNED division and remainder by 2^k. Signed `div_s` rounds
//     toward zero (`-1 / 2 == 0`); `shr_s` rounds toward negative
//     infinity (`-1 >> 1 == -1`). Replacing one with the other would
//     silently change behaviour for negative dividends. Same story
//     for signed `% 2^k` vs `& (2^k - 1)`. Only the divisor-1 case
//     (always exact) and the unsigned variants are rewritten.
//   - Floats. They're handled by Fold when both operands are
//     literals; pattern rewrites would have to think about NaN and
//     signed-zero rules and aren't worth the complexity yet.
//
// Pattern matches only the `<expr> ; const K ; <op>` shape: the
// constant sits immediately after some other op, and the binop
// follows the constant. Mirror cases (`const K ; <expr> ; op` for
// commutative ops) would need a stack-effect walk to find <expr>'s
// boundary; deferred.

package ir

// ReduceStrength rewrites every recognised arithmetic / bitwise
// shape in prog into its cheaper equivalent. Programs without an
// eligible site are unchanged.
// Returns whether any function's op list changed (see Fold — #4377 slice 1b).
func ReduceStrength(prog *Program) bool {
	changed := false
	for _, fn := range prog.Funcs {
		next := reduceStrengthOps(fn.Ops)
		if !opsEqual(next, fn.Ops) {
			fn.Ops = next
			changed = true
		}
	}
	return changed
}

func reduceStrengthOps(ops []Op) []Op {
	out := make([]Op, 0, len(ops))
	for i := 0; i < len(ops); i++ {
		// Pattern: <prev op leaves a value> ; const K ; <binop>.
		if i+1 < len(ops) {
			if repl, ok := strengthRewrite(ops[i], ops[i+1]); ok {
				out = append(out, repl...)
				i++ // consumed the const + the binop
				continue
			}
		}
		out = append(out, ops[i])
	}
	return out
}

// strengthRewrite returns the ops that replace `c ; bop` (leaving the
// preceding <expr>'s value in place), and whether a rewrite applied.
// A nil slice with ok==true means both ops vanish (an identity).
func strengthRewrite(c, bop Op) ([]Op, bool) {
	var k int64
	var w64 bool
	switch c.Kind {
	case OpConstI32:
		k = int64(c.I32)
	case OpConstI64:
		k, w64 = c.I64, true
	default:
		return nil, false
	}
	// The binop must operate at the constant's width, else the rewrite
	// would splice a mismatched-width op (a checker-guaranteed non-case,
	// guarded here anyway).
	if binWidth64(bop) != w64 {
		return nil, false
	}
	binW := 0
	if w64 {
		binW = 64
	}
	constK := func(v int64) Op {
		if w64 {
			return Op{Kind: OpConstI64, I64: v, Pos: c.Pos}
		}
		return Op{Kind: OpConstI32, I32: int32(v), Pos: c.Pos}
	}
	// dropConst emits `<expr> ; drop ; const v` — keeps <expr>'s side
	// effect while replacing its value with a constant.
	dropConst := func(v int64) []Op {
		return []Op{{Kind: OpDrop, Pos: c.Pos}, constK(v)}
	}

	switch bop.Kind {
	case OpMul:
		switch {
		case k == 1:
			return nil, true // x * 1 = x
		case k == 0:
			return dropConst(0), true // x * 0 = 0 (keep side effect)
		default:
			if sh, ok := log2I64(k); ok {
				return []Op{constK(sh), {Kind: OpShl, Width: binW, Pos: bop.Pos}}, true
			}
		}
	case OpAdd, OpSub, OpOr, OpXor, OpShl, OpShrS:
		if k == 0 {
			return nil, true // x +/-/|/^/<</>> 0 = x
		}
	case OpAnd:
		switch {
		case k == 0:
			return dropConst(0), true // x & 0 = 0 (keep side effect)
		case k == -1:
			return nil, true // x & -1 = x (all bits set)
		}
	case OpDivS:
		// Signed div by 2^k rounds differently than a shift — skip.
		// Div by 1 is exact for either sign; unsigned div by 2^k is a
		// logical shift.
		switch {
		case k == 1:
			return nil, true // x / 1 = x
		case bop.Unsigned:
			if sh, ok := log2I64(k); ok {
				return []Op{constK(sh), {Kind: OpShrS, Width: binW, Unsigned: true, Pos: bop.Pos}}, true
			}
		}
	case OpRemS:
		switch {
		case k == 1:
			return dropConst(0), true // x % 1 = 0 (keep side effect)
		case bop.Unsigned:
			if _, ok := log2I64(k); ok {
				return []Op{constK(k - 1), {Kind: OpAnd, Width: binW, Pos: bop.Pos}}, true // x %u 2^k = x & (2^k-1)
			}
		}
	}
	return nil, false
}

// binWidth64 reports whether op runs at 64-bit width (Width 0 defaults
// to 32; see Op.Width).
func binWidth64(op Op) bool { return op.Width == 64 }

// log2I64 returns the base-2 log of n when n is a positive power of
// two. The bool is false otherwise (zero, negative, or non-power-
// of-two values).
func log2I64(n int64) (int64, bool) {
	if n <= 0 || n&(n-1) != 0 {
		return 0, false
	}
	var r int64
	for n > 1 {
		r++
		n >>= 1
	}
	return r, true
}
