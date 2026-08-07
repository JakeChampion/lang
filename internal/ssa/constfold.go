package ssa

import "math"

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
// Covers the integer + boolean cases `internal/ir/constprop.go`
// handles. String concat and bool short-circuit folds are not
// folded here.
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
		rewriteInt(op, negAtWidth(op.Width == 64, v))
		return
	case OpTrunc:
		if len(op.Args) != 1 {
			return
		}
		v, ok := constInt(op.Args[0], defs)
		if !ok {
			return
		}
		rewriteInt(op, int64(int32(v)))
		return
	case OpExtendS:
		if len(op.Args) != 1 {
			return
		}
		v, ok := constInt(op.Args[0], defs)
		if !ok {
			return
		}
		rewriteInt(op, int64(int32(v)))
		return
	case OpExtendU:
		if len(op.Args) != 1 {
			return
		}
		v, ok := constInt(op.Args[0], defs)
		if !ok {
			return
		}
		rewriteInt(op, int64(uint32(v)))
		return
	case OpExtend8S:
		if len(op.Args) != 1 {
			return
		}
		v, ok := constInt(op.Args[0], defs)
		if !ok {
			return
		}
		rewriteInt(op, int64(int8(v)))
		return
	case OpExtend16S:
		if len(op.Args) != 1 {
			return
		}
		v, ok := constInt(op.Args[0], defs)
		if !ok {
			return
		}
		rewriteInt(op, int64(int16(v)))
		return
	case OpFPromote:
		if len(op.Args) != 1 {
			return
		}
		v, ok := constFloat(op.Args[0], defs)
		if !ok {
			return
		}
		rewriteFloat(op, v) // lossless
		return
	case OpFDemote:
		if len(op.Args) != 1 {
			return
		}
		v, ok := constFloat(op.Args[0], defs)
		if !ok {
			return
		}
		rewriteFloat(op, float64(float32(v))) // lossy: round to f32 precision
		return
	case OpIToFS:
		if len(op.Args) != 1 {
			return
		}
		v, ok := constInt(op.Args[0], defs)
		if !ok {
			return
		}
		rewriteFloat(op, float64(v))
		return
	case OpIToFU:
		if len(op.Args) != 1 {
			return
		}
		v, ok := constInt(op.Args[0], defs)
		if !ok {
			return
		}
		rewriteFloat(op, float64(uint64(v)))
		return
	case OpFToIS:
		if len(op.Args) != 1 {
			return
		}
		v, ok := constFloat(op.Args[0], defs)
		if !ok {
			return
		}
		rewriteInt(op, satFToIS(v, op.Width))
		return
	case OpFToIU:
		if len(op.Args) != 1 {
			return
		}
		v, ok := constFloat(op.Args[0], defs)
		if !ok {
			return
		}
		rewriteInt(op, satFToIU(v, op.Width))
		return
	case OpReinterpretF32ToI32:
		if len(op.Args) != 1 {
			return
		}
		v, ok := constFloat(op.Args[0], defs)
		if !ok {
			return
		}
		rewriteInt(op, int64(int32(math.Float32bits(float32(v)))))
		return
	case OpReinterpretI32ToF32:
		if len(op.Args) != 1 {
			return
		}
		v, ok := constInt(op.Args[0], defs)
		if !ok {
			return
		}
		rewriteFloat(op, float64(math.Float32frombits(uint32(v))))
		return
	case OpReinterpretF64ToI64:
		if len(op.Args) != 1 {
			return
		}
		v, ok := constFloat(op.Args[0], defs)
		if !ok {
			return
		}
		rewriteInt(op, int64(math.Float64bits(v)))
		return
	case OpReinterpretI64ToF64:
		if len(op.Args) != 1 {
			return
		}
		v, ok := constInt(op.Args[0], defs)
		if !ok {
			return
		}
		rewriteFloat(op, math.Float64frombits(uint64(v)))
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

	// Fold at the op's integer width (i32 unless Width==64) so the
	// constant matches what the backend would compute at runtime —
	// wraparound, masked shift counts, and u32 unsigned compares.
	res, isBool, boolRes, ok := foldIntBinaryAtWidth(op.Kind, op.Width == 64, lhs, rhs)
	if !ok {
		return
	}
	if isBool {
		rewriteBool(op, boolRes)
	} else {
		rewriteInt(op, res)
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
//
// An f32 op (Width 32) rounds its result back to f32 through
// `roundW`. Folding at f64 precision and keeping the extra bits
// does NOT match what the same expression computes at runtime,
// where every f32 op rounds — the backends emit an fcvt round
// trip for exactly this reason. Skipping it made constant
// folding observable: `((a - b) * c) * (d * (e * (g - h)))` over
// f32 literals folded to -360517687, a value f32 cannot even
// represent (the ulp at that magnitude is 32), where the
// interpreter and both native backends produce -360517664.
func tryFoldFloat(op *Op, defs map[int32]*Op) {
	if len(op.Args) != 2 {
		return
	}
	lhs, lok := constFloat(op.Args[0], defs)
	rhs, rok := constFloat(op.Args[1], defs)
	if !lok || !rok {
		return
	}
	roundW := func(v float64) float64 {
		if op.Width == 32 {
			return float64(float32(v))
		}
		return v
	}
	switch op.Kind {
	case OpFAdd:
		rewriteFloat(op, roundW(lhs+rhs))
	case OpFSub:
		rewriteFloat(op, roundW(lhs-rhs))
	case OpFMul:
		rewriteFloat(op, roundW(lhs*rhs))
	case OpFDiv:
		rewriteFloat(op, roundW(lhs/rhs))
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
