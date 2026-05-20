package ssa

// Fold rewrites Ops whose operands all resolve to compile-time
// constants into the resulting constant directly. Walks blocks
// + ops in linear order, threading a def-site map as it goes,
// so a chain like
//
//	v1 = const_int 1
//	v2 = const_int 2
//	v3 = add v1, v2     // → const_int 3
//	v4 = mul v3, v3     // → const_int 9
//
// folds cleanly in one pass. The Op's Result Value stays put
// (callers that pre-cached uses don't get pulled out from under
// them); only Kind, Imm, and Args change.
//
// Division + remainder skip folding when the RHS is zero — the
// runtime owns that trap; constfold's job is to preserve
// observable behaviour, not paper over it.
//
// Comparisons fold to OpConstBool (Imm 0 / 1). The package
// doesn't track types per Value yet, so downstream passes that
// care about the const flavour should switch on Kind.
//
// Phase 1 covers the integer + boolean cases the existing
// `internal/ir/constprop.go` peephole pass handles; Phase 2
// adds string concat + bool short-circuit folds when those
// land on the SSA side.
func Fold(f *Func) {
	if f == nil {
		return
	}
	defs := map[int32]*Op{}
	for _, b := range f.Blocks {
		for _, op := range b.Ops {
			tryFold(op, defs)
			if op.Result.IsValid() {
				defs[op.Result.ID] = op
			}
		}
	}
}

func tryFold(op *Op, defs map[int32]*Op) {
	switch op.Kind {
	case OpAdd, OpSub, OpMul, OpDiv, OpRem,
		OpAnd, OpOr, OpXor,
		OpShl, OpShr,
		OpEq, OpNe, OpLt, OpLe, OpGt, OpGe:
	case OpNeg:
		if len(op.Args) != 1 {
			return
		}
		v, ok := constInt(op.Args[0], defs)
		if !ok {
			return
		}
		rewriteInt(op, -v)
		return
	default:
		return
	}
	if len(op.Args) != 2 {
		return
	}
	lhs, lok := constInt(op.Args[0], defs)
	rhs, rok := constInt(op.Args[1], defs)
	if !lok || !rok {
		return
	}

	switch op.Kind {
	case OpAdd:
		rewriteInt(op, lhs+rhs)
	case OpSub:
		rewriteInt(op, lhs-rhs)
	case OpMul:
		rewriteInt(op, lhs*rhs)
	case OpDiv:
		if rhs == 0 {
			return
		}
		rewriteInt(op, lhs/rhs)
	case OpRem:
		if rhs == 0 {
			return
		}
		rewriteInt(op, lhs%rhs)
	case OpAnd:
		rewriteInt(op, lhs&rhs)
	case OpOr:
		rewriteInt(op, lhs|rhs)
	case OpXor:
		rewriteInt(op, lhs^rhs)
	case OpShl:
		if rhs < 0 || rhs >= 64 {
			return
		}
		rewriteInt(op, lhs<<uint(rhs))
	case OpShr:
		if rhs < 0 || rhs >= 64 {
			return
		}
		rewriteInt(op, lhs>>uint(rhs))
	case OpEq:
		rewriteBool(op, lhs == rhs)
	case OpNe:
		rewriteBool(op, lhs != rhs)
	case OpLt:
		rewriteBool(op, lhs < rhs)
	case OpLe:
		rewriteBool(op, lhs <= rhs)
	case OpGt:
		rewriteBool(op, lhs > rhs)
	case OpGe:
		rewriteBool(op, lhs >= rhs)
	}
}

func constInt(v Value, defs map[int32]*Op) (int64, bool) {
	if !v.IsValid() {
		return 0, false
	}
	def, ok := defs[v.ID]
	if !ok {
		return 0, false
	}
	if def.Kind != OpConstInt {
		return 0, false
	}
	return def.Imm, true
}

func rewriteInt(op *Op, v int64) {
	op.Kind = OpConstInt
	op.Imm = v
	op.Args = nil
	op.Str = ""
}

func rewriteBool(op *Op, v bool) {
	op.Kind = OpConstBool
	if v {
		op.Imm = 1
	} else {
		op.Imm = 0
	}
	op.Args = nil
	op.Str = ""
}
