// Strength reduction: rewrite expensive operations into cheaper
// equivalents when the operands satisfy a known shape. The rewrites
// trigger purely on adjacent IR ops — no dataflow needed — and stay
// safe under signed-integer semantics:
//
//   - <expr> ; const 2^k ; mul   → <expr> ; const k ; shl
//     Multiplication by a power of two is a left-shift. Both wasm
//     and arm64 issue a single shift op instead of a multiply.
//   - <expr> ; const 1 ; mul     → <expr>
//   - <expr> ; const 0 ; mul     → <expr> ; drop ; const 0
//     The drop preserves any side effect <expr> carried out (a call
//     in the operand position). Without it the operand stack would
//     imbalance.
//   - <expr> ; const 0 ; add     → <expr>
//   - <expr> ; const 0 ; sub     → <expr>
//   - <expr> ; const 0 ; or      → <expr>
//   - <expr> ; const -1 ; and    → <expr>   (-1 is all-bits-set)
//   - <expr> ; const 0 ; and     → <expr> ; drop ; const 0
//
// Deliberate skips:
//
//   - Division and remainder by 2^k. Signed `i32.div_s` rounds
//     toward zero (`-1 / 2 == 0`); `i32.shr_s` rounds toward
//     negative infinity (`-1 >> 1 == -1`). Replacing one with the
//     other would silently change behaviour for negative dividends.
//     Same story for `% 2^k` vs `& (2^k - 1)`.
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
func ReduceStrength(prog *Program) {
	for _, fn := range prog.Funcs {
		fn.Ops = reduceStrengthOps(fn.Ops)
	}
}

func reduceStrengthOps(ops []Op) []Op {
	out := make([]Op, 0, len(ops))
	for i := 0; i < len(ops); i++ {
		// Pattern: <prev op leaves a value> ; const K ; <binop>.
		if i+1 < len(ops) && ops[i].Kind == OpConstI32 {
			k := ops[i].I32
			switch ops[i+1].Kind {
			case OpMul:
				if k == 1 {
					i++ // drop const+mul; <expr>'s value stays
					continue
				}
				if k == 0 {
					out = append(out, Op{Kind: OpDrop, Pos: ops[i].Pos})
					out = append(out, Op{Kind: OpConstI32, I32: 0, Pos: ops[i+1].Pos})
					i++
					continue
				}
				if shift, ok := log2I32(k); ok {
					out = append(out, Op{Kind: OpConstI32, I32: shift, Pos: ops[i].Pos})
					out = append(out, Op{Kind: OpShl, Pos: ops[i+1].Pos})
					i++
					continue
				}
			case OpAdd, OpSub, OpOr, OpXor, OpShl, OpShrS:
				if k == 0 {
					i++
					continue
				}
			case OpAnd:
				if k == 0 {
					out = append(out, Op{Kind: OpDrop, Pos: ops[i].Pos})
					out = append(out, Op{Kind: OpConstI32, I32: 0, Pos: ops[i+1].Pos})
					i++
					continue
				}
				if k == -1 {
					i++ // x & -1 = x; -1 is all-bits-set
					continue
				}
			}
		}
		out = append(out, ops[i])
	}
	return out
}

// log2I32 returns the base-2 log of n when n is a positive power of
// two. The bool is false otherwise (zero, negative, or non-power-
// of-two values).
func log2I32(n int32) (int32, bool) {
	if n <= 0 || n&(n-1) != 0 {
		return 0, false
	}
	var r int32
	for n > 1 {
		r++
		n >>= 1
	}
	return r, true
}
