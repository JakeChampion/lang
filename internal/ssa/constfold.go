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
	case OpAdd, OpSub, OpMul, OpDiv, OpDivU, OpRem, OpRemU,
		OpAnd, OpOr, OpXor,
		OpShl, OpShr, OpShrU,
		OpEq, OpNe,
		OpLt, OpLtU, OpLe, OpLeU,
		OpGt, OpGtU, OpGe, OpGeU:
	case OpFAdd, OpFSub, OpFMul, OpFDiv,
		OpFEq, OpFNe, OpFLt, OpFLe, OpFGt, OpFGe:
		tryFoldFloat(op, defs)
		return
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
	case OpFNeg:
		if len(op.Args) != 1 {
			return
		}
		v, ok := constFloat(op.Args[0], defs)
		if !ok {
			return
		}
		rewriteFloat(op, -v)
		return
	case OpNot:
		if len(op.Args) != 1 {
			return
		}
		v, ok := constBool(op.Args[0], defs)
		if !ok {
			return
		}
		rewriteBool(op, !v)
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
	case OpDivU:
		if rhs == 0 {
			return
		}
		rewriteInt(op, int64(uint64(lhs)/uint64(rhs)))
	case OpRem:
		if rhs == 0 {
			return
		}
		rewriteInt(op, lhs%rhs)
	case OpRemU:
		if rhs == 0 {
			return
		}
		rewriteInt(op, int64(uint64(lhs)%uint64(rhs)))
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
	case OpShrU:
		if rhs < 0 || rhs >= 64 {
			return
		}
		rewriteInt(op, int64(uint64(lhs)>>uint(rhs)))
	case OpEq:
		rewriteBool(op, lhs == rhs)
	case OpNe:
		rewriteBool(op, lhs != rhs)
	case OpLt:
		rewriteBool(op, lhs < rhs)
	case OpLtU:
		rewriteBool(op, uint64(lhs) < uint64(rhs))
	case OpLe:
		rewriteBool(op, lhs <= rhs)
	case OpLeU:
		rewriteBool(op, uint64(lhs) <= uint64(rhs))
	case OpGt:
		rewriteBool(op, lhs > rhs)
	case OpGtU:
		rewriteBool(op, uint64(lhs) > uint64(rhs))
	case OpGe:
		rewriteBool(op, lhs >= rhs)
	case OpGeU:
		rewriteBool(op, uint64(lhs) >= uint64(rhs))
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

// constBool is the boolean analogue of constInt — returns the
// def's Imm (interpreted as 0/1) if `v` is defined by an
// OpConstBool, else false.
func constBool(v Value, defs map[int32]*Op) (bool, bool) {
	if !v.IsValid() {
		return false, false
	}
	def, ok := defs[v.ID]
	if !ok {
		return false, false
	}
	if def.Kind != OpConstBool {
		return false, false
	}
	return def.Imm != 0, true
}

// constFloat is the float analogue of constInt — returns the
// def's F64 if `v` is defined by an OpConstFloat, else false.
func constFloat(v Value, defs map[int32]*Op) (float64, bool) {
	if !v.IsValid() {
		return 0, false
	}
	def, ok := defs[v.ID]
	if !ok {
		return 0, false
	}
	if def.Kind != OpConstFloat {
		return 0, false
	}
	return def.F64, true
}

// tryFoldFloat handles the binary float ops. Unlike integer
// fold, no need to gate on rhs == 0 for FDiv — IEEE-754
// division by zero is well-defined (produces ±Inf or NaN)
// so the runtime trap question doesn't apply.
func tryFoldFloat(op *Op, defs map[int32]*Op) {
	if len(op.Args) != 2 {
		return
	}
	lhs, lok := constFloat(op.Args[0], defs)
	rhs, rok := constFloat(op.Args[1], defs)
	if !lok || !rok {
		return
	}
	switch op.Kind {
	case OpFAdd:
		rewriteFloat(op, lhs+rhs)
	case OpFSub:
		rewriteFloat(op, lhs-rhs)
	case OpFMul:
		rewriteFloat(op, lhs*rhs)
	case OpFDiv:
		rewriteFloat(op, lhs/rhs)
	case OpFEq:
		rewriteBool(op, lhs == rhs)
	case OpFNe:
		rewriteBool(op, lhs != rhs)
	case OpFLt:
		rewriteBool(op, lhs < rhs)
	case OpFLe:
		rewriteBool(op, lhs <= rhs)
	case OpFGt:
		rewriteBool(op, lhs > rhs)
	case OpFGe:
		rewriteBool(op, lhs >= rhs)
	}
}

func rewriteFloat(op *Op, v float64) {
	op.Kind = OpConstFloat
	op.F64 = v
	op.Args = nil
	op.Imm = 0
	op.Str = ""
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
