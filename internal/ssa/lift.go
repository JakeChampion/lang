package ssa

import (
	"fmt"

	"github.com/jakechampion/lang/internal/ir"
)

// LiftFromIR converts a legacy ir.Func into SSA form. The
// supported subset grows incrementally; each follow-up PR
// extends the switch + adds tests for the newly handled ops.
//
// Supported (cumulative):
//
//   Phase 1:
//   - OpConstI32 / OpConstI64 → OpConstInt
//   - OpAdd / OpSub / OpMul → matching SSA op
//   - OpReturn → ret <value>
//   - OpReturnVoid → ret
//
//   Phase 2:
//   - Function params — minted via AddParam, addressed by
//     OpLoadLocal at slot indices [0, len(in.Params))
//   - OpLoadLocal for param slots only (non-param locals
//     still rejected — they need phi insertion)
//
//   Phase 3a:
//   - OpDivS / OpRemS → OpDiv / OpRem
//   - OpAnd / OpOr / OpXor → matching SSA op
//   - OpShl / OpShrS → OpShl / OpShr
//   - OpNot → OpNot
//   - OpEq / OpNe / OpLtS / OpLeS / OpGtS / OpGeS → matching SSA cmp
//
//   Phase 4:
//   - OpCallDirect → OpCall with Str = callee name, Args = the
//     popped arguments. Always pushes a single Result value;
//     void calls leak an unused Result onto the SSA stack —
//     harmless (DCE keeps Call ops anyway, side-effect-y) but
//     a future pass that consults callee signatures can prune
//     the dead Result if it ever becomes worthwhile.
//
//   Phase 5:
//   - OpConstStr → OpConstString with Str = string literal.
//
//   Phase 6:
//   - OpConstF32 / OpConstF64 → OpConstFloat (F64 carries the value)
//   - OpFAdd / OpFSub / OpFMul / OpFDiv → matching SSA float op
//   - OpFNeg → OpFNeg
//   - OpFEq / OpFNe / OpFLt / OpFLe / OpFGt / OpFGe → matching SSA fcmp
//
// Anything else returns an `unsupported op` error. Locals
// beyond the param prefix, OpStoreLocal, branches, indirect
// calls, and the conversion ops land in follow-up PRs.
//
// The legacy IR is a stack-machine encoding: every Op consumes
// its operand-stack inputs and pushes its result. The lift
// maintains a runtime stack of SSA Values mirroring that
// shape — pop N for an N-arg op, push the new Result.
func LiftFromIR(in *ir.Func) (*Func, error) {
	if in == nil {
		return nil, fmt.Errorf("ssa.LiftFromIR: nil func")
	}
	if len(in.Locals) > 0 {
		return nil, fmt.Errorf("ssa.LiftFromIR: locals beyond params not yet supported (have %d)", len(in.Locals))
	}

	out := NewFunc(in.Name)

	// Slots: [0, len(Params)) are parameters, minted up front.
	// (Phase 3 will extend with locals + phi insertion.)
	slots := make([]Value, len(in.Params))
	for i := range in.Params {
		slots[i] = out.AddParam()
	}

	entry := out.NewBlock()
	var stack []Value

	for i, op := range in.Ops {
		switch op.Kind {
		case ir.OpConstI32:
			v := out.AddOp(entry, OpConstInt)
			entry.Ops[len(entry.Ops)-1].Imm = int64(op.I32)
			stack = append(stack, v)
		case ir.OpConstI64:
			v := out.AddOp(entry, OpConstInt)
			entry.Ops[len(entry.Ops)-1].Imm = op.I64
			stack = append(stack, v)
		case ir.OpConstStr:
			v := out.AddOp(entry, OpConstString)
			entry.Ops[len(entry.Ops)-1].Str = op.Str
			stack = append(stack, v)
		case ir.OpConstF32:
			v := out.AddOp(entry, OpConstFloat)
			entry.Ops[len(entry.Ops)-1].F64 = float64(op.F32)
			stack = append(stack, v)
		case ir.OpConstF64:
			v := out.AddOp(entry, OpConstFloat)
			entry.Ops[len(entry.Ops)-1].F64 = op.F64
			stack = append(stack, v)
		case ir.OpLoadLocal:
			idx := int(op.I32)
			if idx < 0 || idx >= len(slots) {
				return nil, fmt.Errorf("ssa.LiftFromIR: OpLoadLocal at op[%d] slot %d out of range (have %d params)",
					i, idx, len(slots))
			}
			stack = append(stack, slots[idx])
		case ir.OpAdd, ir.OpSub, ir.OpMul,
			ir.OpDivS, ir.OpRemS,
			ir.OpAnd, ir.OpOr, ir.OpXor,
			ir.OpShl, ir.OpShrS,
			ir.OpEq, ir.OpNe,
			ir.OpLtS, ir.OpLeS, ir.OpGtS, ir.OpGeS,
			ir.OpFAdd, ir.OpFSub, ir.OpFMul, ir.OpFDiv,
			ir.OpFEq, ir.OpFNe, ir.OpFLt, ir.OpFLe, ir.OpFGt, ir.OpFGe:
			if len(stack) < 2 {
				return nil, fmt.Errorf("ssa.LiftFromIR: %v at op[%d] needs 2 operands, stack has %d",
					op.Kind, i, len(stack))
			}
			rhs := stack[len(stack)-1]
			lhs := stack[len(stack)-2]
			stack = stack[:len(stack)-2]
			kind := mapBinaryArith(op.Kind)
			v := out.AddOp(entry, kind, lhs, rhs)
			stack = append(stack, v)
		case ir.OpNot:
			if len(stack) < 1 {
				return nil, fmt.Errorf("ssa.LiftFromIR: OpNot at op[%d] needs 1 operand", i)
			}
			arg := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			v := out.AddOp(entry, OpNot, arg)
			stack = append(stack, v)
		case ir.OpFNeg:
			if len(stack) < 1 {
				return nil, fmt.Errorf("ssa.LiftFromIR: OpFNeg at op[%d] needs 1 operand", i)
			}
			arg := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			v := out.AddOp(entry, OpFNeg, arg)
			stack = append(stack, v)
		case ir.OpCallDirect:
			argc := int(op.I32)
			if len(stack) < argc {
				return nil, fmt.Errorf("ssa.LiftFromIR: OpCallDirect at op[%d] needs %d args, stack has %d",
					i, argc, len(stack))
			}
			args := append([]Value(nil), stack[len(stack)-argc:]...)
			stack = stack[:len(stack)-argc]
			result := out.AddOp(entry, OpCall, args...)
			// Set the callee name on the just-appended Op.
			entry.Ops[len(entry.Ops)-1].Str = op.Str
			stack = append(stack, result)
		case ir.OpReturn:
			if len(stack) < 1 {
				return nil, fmt.Errorf("ssa.LiftFromIR: OpReturn at op[%d] needs 1 operand", i)
			}
			out.SetRet(entry, stack[len(stack)-1])
			return out, nil
		case ir.OpReturnVoid:
			out.SetRet(entry, Value{})
			return out, nil
		default:
			return nil, fmt.Errorf("ssa.LiftFromIR: unsupported op %v at index %d", op.Kind, i)
		}
	}

	// Implicit void return — the legacy IR is happy to end without
	// an explicit OpReturn for void functions.
	out.SetRet(entry, Value{})
	return out, nil
}

func mapBinaryArith(k ir.OpKind) OpKind {
	switch k {
	case ir.OpAdd:
		return OpAdd
	case ir.OpSub:
		return OpSub
	case ir.OpMul:
		return OpMul
	case ir.OpDivS:
		return OpDiv
	case ir.OpRemS:
		return OpRem
	case ir.OpAnd:
		return OpAnd
	case ir.OpOr:
		return OpOr
	case ir.OpXor:
		return OpXor
	case ir.OpShl:
		return OpShl
	case ir.OpShrS:
		return OpShr
	case ir.OpEq:
		return OpEq
	case ir.OpNe:
		return OpNe
	case ir.OpLtS:
		return OpLt
	case ir.OpLeS:
		return OpLe
	case ir.OpGtS:
		return OpGt
	case ir.OpGeS:
		return OpGe
	case ir.OpFAdd:
		return OpFAdd
	case ir.OpFSub:
		return OpFSub
	case ir.OpFMul:
		return OpFMul
	case ir.OpFDiv:
		return OpFDiv
	case ir.OpFEq:
		return OpFEq
	case ir.OpFNe:
		return OpFNe
	case ir.OpFLt:
		return OpFLt
	case ir.OpFLe:
		return OpFLe
	case ir.OpFGt:
		return OpFGt
	case ir.OpFGe:
		return OpFGe
	}
	return OpInvalid
}
