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
			}
		}
	}
}
