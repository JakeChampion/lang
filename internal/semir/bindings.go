package semir

import (
	"github.com/jakechampion/lang/internal/ast"
	"github.com/jakechampion/lang/internal/ssa"
)

func bindingOp(kind ssa.OpKind) bool {
	return kind == ssa.OpBindingInit || kind == ssa.OpBindingRead || kind == ssa.OpBindingReplace
}

func (f *Func) readBinding(block *ssa.Block, id BindingID, pos ast.Position) ssa.Value {
	value := f.addOp(block, ssa.OpBindingRead, f.bindings[id-1].typ, pos)
	block.Ops[len(block.Ops)-1].Imm = int64(id)
	return value
}

func (f *Func) writeBinding(block *ssa.Block, kind ssa.OpKind, id BindingID, value ssa.Value, pos ast.Position) *ssa.Op {
	op := f.addEffect(block, kind, pos, value)
	op.Imm = int64(id)
	return op
}
