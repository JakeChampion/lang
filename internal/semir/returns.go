package semir

import (
	"fmt"

	"github.com/jakechampion/lang/internal/ast"
	"github.com/jakechampion/lang/internal/ssa"
)

type sourceKind uint8

const (
	sourceParameter sourceKind = iota
	sourceGenerated            // a semantic producer, NOT a physical freshness proof
	sourceImmortal
	sourceScalar
)

// A path is interned within one analysis. Field -1 selects the summary of an
// array's elements; nonnegative fields select exact tuple fields. Parameters
// supply the finite path vocabulary from their resolved types. Substitution
// selects existing argument facts, never grows paths through a recursive call.
type sourcePath struct {
	parent *sourcePath
	field  int
}

type source struct {
	kind  sourceKind
	param int
	path  *sourcePath
}

// valueFlow is a may-provenance lattice for one complete semantic type. Root
// identity and contained values are separate: an array's roots do not identify
// its elements. Arrays use one element summary; tuples keep individual fields.
// Empty roots mean bottom (no returned value discovered), NOT an owned value,
// a borrow proof, or proof that execution terminates.
type valueFlow struct {
	roots    map[source]bool
	children []*valueFlow
}

func emptyFlow(typ ast.Type) *valueFlow {
	f := &valueFlow{roots: make(map[source]bool)}
	switch t := typ.(type) {
	case ast.ArrayType:
		f.children = []*valueFlow{emptyFlow(t.Elem)}
	case ast.TupleType:
		for _, elem := range t.Elems {
			f.children = append(f.children, emptyFlow(elem))
		}
	}
	return f
}

func addSource(dst *valueFlow, s source) bool {
	if dst.roots[s] {
		return false
	}
	dst.roots[s] = true
	return true
}

func mergeRoots(dst, src *valueFlow) bool {
	changed := false
	for s := range src.roots {
		changed = addSource(dst, s) || changed
	}
	return changed
}

func mergeFlow(dst, src *valueFlow) bool {
	changed := mergeRoots(dst, src)
	for i := range dst.children {
		changed = mergeFlow(dst.children[i], src.children[i]) || changed
	}
	return changed
}

type functionFlow struct {
	values  map[int32]*valueFlow
	result  *valueFlow
	uses    *ssa.Uses
	reach   map[*ssa.Block]bool
	returns []ssa.Value
}

type returnFlow struct {
	funcs map[*Func]*functionFlow
}

// solveReturnFlow computes one closed-module fixed point using SSA def-use
// edges and callee-to-caller dependencies. Facts only grow in a finite lattice:
// each function has its parameter/type paths plus three nonparameter atoms.
// Thus recursion needs neither a depth limit nor a prematurely latched verdict.
//
// This is provenance, not RC certification. It does not prove that an incoming
// counted unit remains available, that a returned projection owns a unit, or
// that a generated result is unique. Those require the subsequent unit/lifetime
// verifier. No AST names or emitted runtime helpers participate in this pass.
func solveReturnFlow(p *Program) (*returnFlow, error) {
	if err := VerifyProgram(p); err != nil {
		return nil, err
	}
	out := &returnFlow{funcs: make(map[*Func]*functionFlow)}
	paths := make(map[sourcePath]*sourcePath)
	var seedParameter func(*valueFlow, ast.Type, int, *sourcePath)
	seedParameter = func(flow *valueFlow, typ ast.Type, param int, path *sourcePath) {
		if referenceBearing(typ) {
			addSource(flow, source{kind: sourceParameter, param: param, path: path})
		} else {
			addSource(flow, source{kind: sourceScalar})
		}
		child := func(field int) *sourcePath {
			key := sourcePath{parent: path, field: field}
			if paths[key] == nil {
				copy := key
				paths[key] = &copy
			}
			return paths[key]
		}
		switch t := typ.(type) {
		case ast.ArrayType:
			seedParameter(flow.children[0], t.Elem, param, child(-1))
		case ast.TupleType:
			for i, elem := range t.Elems {
				seedParameter(flow.children[i], elem, param, child(i))
			}
		}
	}
	for _, f := range p.funcs {
		state := &functionFlow{
			values: make(map[int32]*valueFlow), result: emptyFlow(f.result),
			uses: ssa.BuildUses(f.graph), reach: ssa.Reachable(f.graph),
		}
		for _, param := range f.graph.Params {
			state.values[param.ID] = emptyFlow(f.values[param.ID].typ.source)
		}
		for i, param := range f.graph.Params {
			seedParameter(state.values[param.ID], f.values[param.ID].typ.source, i, nil)
		}
		for _, block := range f.graph.RPO() {
			for _, op := range block.Ops {
				if op.Result.IsValid() {
					state.values[op.Result.ID] = emptyFlow(f.values[op.Result.ID].typ.source)
				}
			}
			if block.Term.Kind == ssa.TermRet && block.Term.Value.IsValid() {
				state.returns = append(state.returns, block.Term.Value)
			}
		}
		out.funcs[f] = state
	}

	type equation struct {
		fn    *Func
		block *ssa.Block
		op    *ssa.Op // nil updates the function's return summary
	}
	var equations []equation
	opTask := make(map[*ssa.Op]int)
	returnTask := make(map[*Func]int)
	callers := make(map[*Func][]int)
	for _, f := range p.funcs {
		for _, block := range f.graph.RPO() {
			for _, op := range block.Ops {
				// A void call still has checked argument effects and a
				// verified callee, but no value-provenance equation.
				if !op.Result.IsValid() {
					continue
				}
				id := len(equations)
				opTask[op] = id
				equations = append(equations, equation{fn: f, block: block, op: op})
				if op.Kind == ssa.OpSemanticCall {
					callee, err := f.callee(op)
					if err != nil {
						return nil, err
					}
					callers[callee] = append(callers[callee], id)
				}
			}
		}
		returnTask[f] = len(equations)
		equations = append(equations, equation{fn: f})
	}
	var queue []int
	queued := make([]bool, len(equations))
	enqueue := func(id int) {
		if !queued[id] {
			queue = append(queue, id)
			queued[id] = true
		}
	}
	for id := range equations {
		enqueue(id)
	}
	for len(queue) != 0 {
		id := queue[0]
		queue = queue[1:]
		queued[id] = false
		eq := equations[id]
		state := out.funcs[eq.fn]
		if eq.op == nil {
			changed := false
			for _, value := range state.returns {
				changed = mergeFlow(state.result, state.values[value.ID]) || changed
			}
			if changed {
				for _, caller := range callers[eq.fn] {
					enqueue(caller)
				}
			}
			continue
		}
		op := eq.op
		dst := state.values[op.Result.ID]
		arg := func(i int) *valueFlow { return state.values[op.Args[i].ID] }
		changed := false
		switch op.Kind {
		case ssa.OpConstInt, ssa.OpConstBool, ssa.OpNot, ssa.OpNeg, ssa.OpAdd, ssa.OpSub, ssa.OpMul,
			ssa.OpEq, ssa.OpNe, ssa.OpLt, ssa.OpLe, ssa.OpGt, ssa.OpGe,
			ssa.OpStateAbsent, ssa.OpStatePresent, ssa.OpStateHas, ssa.OpStateGet:
			changed = addSource(dst, source{kind: sourceScalar})
		case ssa.OpConstString:
			changed = addSource(dst, source{kind: sourceImmortal})
		case ssa.OpArrayMake:
			changed = addSource(dst, source{kind: sourceGenerated})
			for i := range op.Args {
				changed = mergeFlow(dst.children[0], arg(i)) || changed
			}
		case ssa.OpTupleMake:
			changed = addSource(dst, source{kind: sourceGenerated})
			for i := range op.Args {
				changed = mergeFlow(dst.children[i], arg(i)) || changed
			}
		case ssa.OpArrayAppend:
			changed = addSource(dst, source{kind: sourceGenerated})
			// A future reuse choice may retain the input buffer's identity.
			// Neither that possibility nor the generated alternative is a
			// guarantee of physical freshness or a borrow-return contract.
			changed = mergeRoots(dst, arg(0)) || changed
			changed = mergeFlow(dst.children[0], arg(0).children[0]) || changed
			changed = mergeFlow(dst.children[0], arg(1)) || changed
		case ssa.OpArrayGet:
			changed = mergeFlow(dst, arg(0).children[0])
		case ssa.OpTupleGet:
			changed = mergeFlow(dst, arg(0).children[op.Imm])
		case ssa.OpPhi:
			for i := range op.Args {
				if state.reach[eq.block.Preds[i]] {
					changed = mergeFlow(dst, arg(i)) || changed
				}
			}
		case ssa.OpSemanticCall:
			callee, err := eq.fn.callee(op)
			if err != nil {
				return nil, err
			}
			changed = substituteFlow(dst, out.funcs[callee].result, op.Args, state.values)
		default:
			return nil, fmt.Errorf("semir: missing return-flow contract for %s", op.Kind)
		}
		if changed {
			for _, use := range state.uses.Of(op.Result) {
				if use.Op != nil {
					if task, exists := opTask[use.Op]; exists {
						enqueue(task)
					}
				} else if state.reach[use.Block] && use.Block.Term.Kind == ssa.TermRet {
					enqueue(returnTask[eq.fn])
				}
			}
		}
	}
	return out, nil
}

func selectPath(flow *valueFlow, path *sourcePath) *valueFlow {
	if path == nil {
		return flow
	}
	parent := selectPath(flow, path.parent)
	if path.field == -1 {
		return parent.children[0]
	}
	return parent.children[path.field]
}

func substituteFlow(dst, summary *valueFlow, args []ssa.Value, values map[int32]*valueFlow) bool {
	changed := false
	for atom := range summary.roots {
		if atom.kind == sourceParameter {
			actual := selectPath(values[args[atom.param].ID], atom.path)
			changed = mergeRoots(dst, actual) || changed
		} else {
			changed = addSource(dst, atom) || changed
		}
	}
	for i := range dst.children {
		changed = substituteFlow(dst.children[i], summary.children[i], args, values) || changed
	}
	return changed
}
