package semir

import (
	"fmt"

	"github.com/jakechampion/lang/internal/ssa"
)

// resultKind is a semantic ownership contract, not an allocation/uniqueness
// proof. A counted result may share both its buffer and its children. A join
// selects a predecessor's value; it does not create another counted unit.
type resultKind uint8

const (
	resultInvalid resultKind = iota
	resultValue
	resultImmortal
	resultCounted
	resultProjection
	resultJoin
	resultCall // return ownership awaits the callee's interprocedural summary
)

// storageKind specifies what becomes reachable through a constructed result.
// Installing a value and copying an array's element relationships are different
// effects: append does not store the original array itself as a child.
type storageKind uint8

const (
	storeNone storageKind = iota
	storeValue
	storeArrayElements
)

type operandEffect struct {
	value ssa.Value
	store storageKind
	// consume requires a counted unit at an own call argument. It does not
	// claim that the caller may move its existing unit: liveness and other
	// argument uses still have to prove that, or lowering must acquire one.
	consume bool
	// counted means a reference-bearing stored value (or each reference-bearing
	// element of a copied array) needs its own lifetime unit in the result.
	// The RC lowering pass may satisfy it by retain or a proven transfer; this
	// pre-RC contract neither picks a retain nor assumes a move is safe.
	counted bool
}

type opEffect struct {
	result resultKind
	callee *Func // explicit function identity for resultCall
	// Inputs are borrowed unless consume requires a unit at a call boundary.
	// This builder has not selected destructive reuse or
	// ownership transfers. Phi input uses occur on predecessor edges.
	inputs []operandEffect
	// parent is meaningful only for resultProjection. It is a true input,
	// never an identity alias or a newly acquired reference to the child.
	parent ssa.Value
}

type returnEffect struct {
	block *ssa.Block
	value ssa.Value
	// A reference-bearing value escaping the function needs a lifetime unit.
	// Immortal values need no physical retain, and an existing unit may be
	// transferred only after path-sensitive ownership analysis proves it.
	counted bool
}

type functionEffects struct {
	ops     map[*ssa.Op]opEffect
	returns []returnEffect
}

// ownershipEffects exposes the lifetime obligations of verified semantic
// operations. It does not solve liveness, place RC operations or grant reuse.
// Verification occurs once per function, not again for every operation.
func ownershipEffects(f *Func) (*functionEffects, error) {
	if err := Verify(f); err != nil {
		return nil, err
	}
	out := &functionEffects{ops: make(map[*ssa.Op]opEffect)}
	for _, block := range f.graph.Blocks {
		for _, op := range block.Ops {
			e := opEffect{inputs: make([]operandEffect, len(op.Args))}
			for i, arg := range op.Args {
				e.inputs[i].value = arg
			}
			ref := referenceBearing(f.values[op.Result.ID].typ)
			switch op.Kind {
			case ssa.OpConstInt, ssa.OpConstBool, ssa.OpAdd, ssa.OpSub, ssa.OpMul,
				ssa.OpEq, ssa.OpNe, ssa.OpLt, ssa.OpLe, ssa.OpGt, ssa.OpGe:
				e.result = resultValue
			case ssa.OpConstString:
				e.result = resultImmortal
			case ssa.OpArrayMake, ssa.OpTupleMake:
				e.result = resultCounted
				for i, arg := range op.Args {
					e.inputs[i].store = storeValue
					e.inputs[i].counted = referenceBearing(f.values[arg.ID].typ)
				}
			case ssa.OpArrayAppend:
				e.result = resultCounted
				e.inputs[0].store = storeArrayElements
				e.inputs[1].store = storeValue
				// The second operand has exactly the array's element type,
				// already established by semantic verification.
				e.inputs[0].counted = referenceBearing(f.values[op.Args[1].ID].typ)
				e.inputs[1].counted = e.inputs[0].counted
			case ssa.OpArrayGet, ssa.OpTupleGet:
				e.result = resultValue
				if ref {
					e.result = resultProjection
					e.parent = op.Args[0]
				}
			case ssa.OpPhi:
				e.result = resultJoin
			case ssa.OpSemanticCall:
				callee, err := f.callee(op)
				if err != nil {
					return nil, err
				}
				e.result, e.callee = resultCall, callee
				if !ref {
					e.result = resultValue
				}
				for i, mode := range callee.contract.modes {
					e.inputs[i].consume = mode == ParamCounted
				}
			default:
				return nil, fmt.Errorf("semir: missing ownership effect contract for %s", op.Kind)
			}
			out.ops[op] = e
		}
		if block.Term.Kind == ssa.TermRet && block.Term.Value.IsValid() {
			value := block.Term.Value
			out.returns = append(out.returns, returnEffect{
				block: block, value: value, counted: referenceBearing(f.values[value.ID].typ),
			})
		}
	}
	return out, nil
}
