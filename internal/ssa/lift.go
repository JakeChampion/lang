package ssa

import (
	"fmt"

	"github.com/jakechampion/lang/internal/ir"
)

// LiftFromIR converts a legacy ir.Func into SSA form. Phase 1
// coverage is intentionally narrow — straight-line constant
// arithmetic — so we can ship the entry point + tests now
// and grow the supported subset incrementally.
//
// Supported in Phase 1:
//   - OpConstI32 / OpConstI64 → OpConstInt
//   - OpAdd / OpSub / OpMul → matching SSA op
//   - OpReturn → ret <value>
//   - OpReturnVoid → ret
//
// Anything else returns an `unsupported op` error. Locals
// (OpLoadLocal / OpStoreLocal), branches (OpBlock / OpBr /
// OpBrIf / OpLoop / OpIf), calls, and the full integer / float
// op surface land in follow-up PRs; each follows the same
// "stack machine → SSA Value" shape established here.
//
// The legacy IR is a stack-machine encoding: every Op consumes
// its operand-stack inputs and pushes its result. The lift
// maintains a runtime stack of SSA Values mirroring that
// shape — pop N for an N-arg op, push the new Result.
func LiftFromIR(in *ir.Func) (*Func, error) {
	if in == nil {
		return nil, fmt.Errorf("ssa.LiftFromIR: nil func")
	}
	if len(in.Params) > 0 {
		return nil, fmt.Errorf("ssa.LiftFromIR: params not yet supported (have %d)", len(in.Params))
	}
	if len(in.Locals) > 0 {
		return nil, fmt.Errorf("ssa.LiftFromIR: locals not yet supported (have %d)", len(in.Locals))
	}

	out := NewFunc(in.Name)
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
		case ir.OpAdd, ir.OpSub, ir.OpMul:
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
	}
	return OpInvalid
}
