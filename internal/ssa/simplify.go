package ssa

// Simplify rewrites operand positions throughout the function
// to bypass Ops that compute an algebraic identity. After this
// pass the identity Op itself is unused — pair with DCE to
// reclaim the slot.
//
// Identities handled (Phase 1):
//
//	x + 0  ⇒ x
//	0 + x  ⇒ x
//	x - 0  ⇒ x
//	x * 1  ⇒ x
//	1 * x  ⇒ x
//	x / 1  ⇒ x
//
// "Synthesising" identities (x - x ⇒ 0, x * 0 ⇒ 0, x / x ⇒ 1)
// need a fresh OpConstInt op to materialise the answer; that
// shape lands in a future pass alongside the IR-→SSA lift,
// which already knows how to mint helper Ops inline.
//
// The pass walks the function twice:
//  1. Identify each Op whose Result can be aliased to one of
//     its operands. Build a substitution map keyed by Value
//     ID.
//  2. Walk every Op's Args + the terminator operands and
//     resolve each Value through the chain (transitively, so
//     `a → b → c` collapses to `c` in one rewrite).
//
// Cycle guard: the substitution map is finite + monotonic
// (each new entry maps to a value that itself maps to nothing
// new at insertion time), but a defensive `seen` set in the
// resolver keeps a malformed graph from spinning forever.
//
// Composes with Fold + DCE:
//
//	Fold(f)      // const_int 0 + const_int x → const_int x
//	Simplify(f)  // OpAdd v, (const 0) → v
//	DCE(f)       // drop the now-orphan identity ops
func Simplify(f *Func) {
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

	sub := map[int32]Value{}
	for _, b := range f.Blocks {
		for _, op := range b.Ops {
			if !op.Result.IsValid() {
				continue
			}
			if repl, ok := identityReplacement(op, defs); ok {
				sub[op.Result.ID] = repl
			}
		}
	}

	if len(sub) == 0 {
		return
	}

	resolve := func(v Value) Value {
		seen := map[int32]bool{}
		for v.IsValid() {
			if seen[v.ID] {
				break
			}
			seen[v.ID] = true
			next, ok := sub[v.ID]
			if !ok {
				return v
			}
			v = next
		}
		return v
	}

	for _, b := range f.Blocks {
		for _, op := range b.Ops {
			for i := range op.Args {
				op.Args[i] = resolve(op.Args[i])
			}
		}
		b.Term.Cond = resolve(b.Term.Cond)
		b.Term.Value = resolve(b.Term.Value)
		b.Term.Value2 = resolve(b.Term.Value2)
	}
}

func identityReplacement(op *Op, defs map[int32]*Op) (Value, bool) {
	// Unary cases first.
	if op.Kind == OpNot {
		if len(op.Args) != 1 {
			return Value{}, false
		}
		def, ok := defs[op.Args[0].ID]
		if !ok || def.Kind != OpNot || len(def.Args) != 1 {
			return Value{}, false
		}
		return def.Args[0], true
	}
	// Ternary OpSelect.
	if op.Kind == OpSelect {
		if len(op.Args) != 3 {
			return Value{}, false
		}
		if op.Args[1] == op.Args[2] {
			// both branches identical → either one.
			return op.Args[1], true
		}
		if v, ok := constBool(op.Args[0], defs); ok {
			if v {
				return op.Args[1], true
			}
			return op.Args[2], true
		}
		return Value{}, false
	}
	if len(op.Args) != 2 {
		return Value{}, false
	}
	lhs, rhs := op.Args[0], op.Args[1]
	lhsImm, lhsConst := constInt(lhs, defs)
	rhsImm, rhsConst := constInt(rhs, defs)

	switch op.Kind {
	case OpAdd:
		if rhsConst && rhsImm == 0 {
			return lhs, true
		}
		if lhsConst && lhsImm == 0 {
			return rhs, true
		}
	case OpSub:
		if rhsConst && rhsImm == 0 {
			return lhs, true
		}
	case OpMul:
		if rhsConst && rhsImm == 1 {
			return lhs, true
		}
		if lhsConst && lhsImm == 1 {
			return rhs, true
		}
	case OpDiv:
		if rhsConst && rhsImm == 1 {
			return lhs, true
		}
	case OpAnd:
		// x & x → x; x & -1 → x (-1 is all-bits set); 0 & x not
		// here (synthesised by StrengthReduce).
		if lhs == rhs {
			return lhs, true
		}
		if rhsConst && rhsImm == -1 {
			return lhs, true
		}
		if lhsConst && lhsImm == -1 {
			return rhs, true
		}
	case OpOr:
		// x | 0 → x; x | x → x.
		if lhs == rhs {
			return lhs, true
		}
		if rhsConst && rhsImm == 0 {
			return lhs, true
		}
		if lhsConst && lhsImm == 0 {
			return rhs, true
		}
	case OpXor:
		// x ^ 0 → x (x ^ x ⇒ 0 belongs in StrengthReduce).
		if rhsConst && rhsImm == 0 {
			return lhs, true
		}
		if lhsConst && lhsImm == 0 {
			return rhs, true
		}
	case OpShl, OpShr:
		// x << 0 → x ; x >> 0 → x. Shift count on the right
		// only; no commutative form.
		if rhsConst && rhsImm == 0 {
			return lhs, true
		}
	}
	return Value{}, false
}
