package semir

import (
	"fmt"

	"github.com/jakechampion/lang/internal/ast"
	"github.com/jakechampion/lang/internal/ssa"
)

func stateOp(kind ssa.OpKind) bool {
	return kind == ssa.OpStateAbsent || kind == ssa.OpStatePresent || kind == ssa.OpStateHas || kind == ssa.OpStateGet
}

func (f *Func) addState(b *ssa.Block, kind ssa.OpKind, payload ast.Type, pos ast.Position, args ...ssa.Value) ssa.Value {
	v := f.addOp(b, kind, payload, pos, args...)
	info := f.values[v.ID]
	info.typ.form = availabilityForm
	f.values[v.ID] = info
	return v
}

func verifyStateOp(f *Func, op *ssa.Op) error {
	bad := func() error { return fmt.Errorf("invalid availability operand/result types or arity") }
	result := f.values[op.Result.ID].typ
	if !op.Result.IsValid() || op.Imm != 0 || op.Str != "" || op.F64 != 0 {
		return bad()
	}
	switch op.Kind {
	case ssa.OpStateAbsent:
		if result.form != availabilityForm || len(op.Args) != 0 {
			return bad()
		}
	case ssa.OpStatePresent:
		if result.form != availabilityForm || len(op.Args) != 1 {
			return bad()
		}
		arg := f.values[op.Args[0].ID].typ
		if arg.form != sourceForm || !ast.Equal(result.source, arg.source) {
			return bad()
		}
	case ssa.OpStateHas, ssa.OpStateGet:
		if result.form != sourceForm || len(op.Args) != 1 {
			return bad()
		}
		arg := f.values[op.Args[0].ID].typ
		if arg.form != availabilityForm {
			return bad()
		}
		want := arg.source
		if op.Kind == ssa.OpStateHas {
			want = ast.BoolType{}
		}
		if !ast.Equal(result.source, want) {
			return bad()
		}
	}
	return nil
}

// Presence proofs refer to an immutable state SSA identity, not merely a
// binding name or a boolean with the same spelling. Guard edges must dominate
// extraction: an alternate predecessor cannot enter the payload block.
func verifyStateGuards(f *Func) error {
	var gets []*ssa.Op
	var getBlocks []*ssa.Block
	for _, block := range f.graph.Blocks {
		for _, op := range block.Ops {
			if op.Kind == ssa.OpStateGet {
				gets = append(gets, op)
				getBlocks = append(getBlocks, block)
			}
		}
	}
	if len(gets) == 0 {
		return nil
	}
	defs := make(map[int32]*ssa.Op)
	for _, block := range f.graph.Blocks {
		for _, op := range block.Ops {
			if op.Result.IsValid() {
				defs[op.Result.ID] = op
			}
		}
	}
	guards := make(map[int32][]*ssa.Block)
	for _, block := range f.graph.Blocks {
		if block.Term.Kind != ssa.TermBrIf {
			continue
		}
		test := defs[block.Term.Cond.ID]
		yes := block.Term.True
		if test != nil && test.Kind == ssa.OpStateHas && len(yes.Preds) == 1 && yes.Preds[0] == block {
			guards[test.Args[0].ID] = append(guards[test.Args[0].ID], yes)
		}
	}
	dom := ssa.BuildDomTree(f.graph)
	for i, get := range gets {
		state := get.Args[0]
		if def := defs[state.ID]; def != nil && def.Kind == ssa.OpStatePresent {
			continue
		}
		proven := false
		for _, guard := range guards[state.ID] {
			proven = proven || dom.Dominates(guard, getBlocks[i])
		}
		if !proven {
			pos := f.values[get.Result.ID].pos
			return fmt.Errorf("semir %s at %d:%d: availability payload lacks a presence proof for %s", f.graph.Name, pos.Line, pos.Col, state)
		}
	}
	return nil
}
