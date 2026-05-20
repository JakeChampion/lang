package ssa

// StrengthReduce rewrites pure ops whose result is an absorbing
// constant directly into a const_int. Unlike Simplify (which
// only bypasses identity ops by aliasing the result to the
// untouched operand), this pass synthesises a new constant
// when the algebraic answer doesn't equal either operand.
//
// Identities handled (Phase 1):
//
//	x - x   ⇒ const_int 0
//	x ^ x   ⇒ const_int 0
//	x * 0   ⇒ const_int 0
//	0 * x   ⇒ const_int 0
//	x & 0   ⇒ const_int 0
//	0 & x   ⇒ const_int 0
//	x | -1  ⇒ const_int -1
//	-1 | x  ⇒ const_int -1
//	x % 1   ⇒ const_int 0        (signed remainder by 1 is always 0)
//	x %u 1  ⇒ const_int 0        (unsigned remainder by 1 is always 0)
//
// Integer self-comparison identities (Phase 2):
//
//	x == x  ⇒ const_bool true
//	x != x  ⇒ const_bool false
//	x <  x  ⇒ const_bool false   (signed + unsigned)
//	x <= x  ⇒ const_bool true    (signed + unsigned)
//	x >  x  ⇒ const_bool false   (signed + unsigned)
//	x >= x  ⇒ const_bool true    (signed + unsigned)
//
// Float self-comparison is intentionally NOT folded: IEEE-754
// NaN compares unequal to itself, so `x == x` may be false and
// `x != x` may be true for x = NaN. Integer values can't be NaN
// so the integer forms are safe.
//
// Not yet handled (would need a "definitely non-zero" check
// on x that we don't track yet):
//
//	x / x   ⇒ const_int 1   (only safe if x ≠ 0)
//	x % x   ⇒ const_int 0   (same)
//
// In-place rewrite of the Op: Kind becomes OpConstInt, Imm
// gets the synthesised value, Args clears. The Op's Result
// Value stays the same so existing uses keep working —
// downstream passes see them as const_int 0.
//
// Pair with DCE after to reclaim ops whose operands are now
// only referenced via the (since-rewritten) Op.
func StrengthReduce(f *Func) {
	if f == nil {
		return
	}
	defs := map[int32]*Op{}
	for _, b := range f.Blocks {
		for _, op := range b.Ops {
			if op.Result.IsValid() {
				defs[op.Result.ID] = op
			}
		}
	}

	for _, b := range f.Blocks {
		for _, op := range b.Ops {
			switch op.Kind {
			case OpSub, OpXor:
				if len(op.Args) == 2 && op.Args[0].IsValid() && op.Args[0] == op.Args[1] {
					rewriteInt(op, 0)
				}
			case OpMul, OpAnd:
				if len(op.Args) != 2 {
					continue
				}
				if lImm, lOK := constInt(op.Args[0], defs); lOK && lImm == 0 {
					rewriteInt(op, 0)
					continue
				}
				if rImm, rOK := constInt(op.Args[1], defs); rOK && rImm == 0 {
					rewriteInt(op, 0)
				}
			case OpOr:
				// x | -1 → -1 (all bits set absorbs).
				if len(op.Args) != 2 {
					continue
				}
				if lImm, lOK := constInt(op.Args[0], defs); lOK && lImm == -1 {
					rewriteInt(op, -1)
					continue
				}
				if rImm, rOK := constInt(op.Args[1], defs); rOK && rImm == -1 {
					rewriteInt(op, -1)
				}
			case OpRem, OpRemU:
				// x % 1 → 0. RHS-only (LHS-1 isn't an identity:
				// 1 % x depends on x). Same for unsigned.
				if len(op.Args) != 2 {
					continue
				}
				if rImm, rOK := constInt(op.Args[1], defs); rOK && rImm == 1 {
					rewriteInt(op, 0)
				}
			case OpEq, OpLe, OpLeU, OpGe, OpGeU:
				// x == x, x <= x, x >= x ⇒ true.
				if len(op.Args) == 2 && op.Args[0].IsValid() && op.Args[0] == op.Args[1] {
					rewriteBool(op, true)
				}
			case OpNe, OpLt, OpLtU, OpGt, OpGtU:
				// x != x, x < x, x > x ⇒ false.
				if len(op.Args) == 2 && op.Args[0].IsValid() && op.Args[0] == op.Args[1] {
					rewriteBool(op, false)
				}
			}
		}
	}
}
