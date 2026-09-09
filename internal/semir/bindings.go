package semir

import (
	"github.com/jakechampion/lang/internal/ast"
	"github.com/jakechampion/lang/internal/ssa"
)

func bindingOp(kind ssa.OpKind) bool {
	return kind == ssa.OpBindingInit || kind == ssa.OpBindingRead || kind == ssa.OpBindingReplace || kind == ssa.OpBindingSnapshot || kind == ssa.OpBindingReplaceGuarded
}

// A guarded replacement still writes an ordinary value. Its separate immutable
// witness proves availability without becoming the payload reaching later reads.
func bindingWriteValue(op *ssa.Op) ssa.Value {
	if op.Kind == ssa.OpBindingReplaceGuarded {
		return op.Args[1]
	}
	return op.Args[0]
}

func (f *Func) replaceBindingGuarded(block *ssa.Block, id BindingID, witness, value ssa.Value, pos ast.Position) *ssa.Op {
	op := f.addEffect(block, ssa.OpBindingReplaceGuarded, pos, witness, value)
	op.Imm = int64(id)
	return op
}

// A snapshot observes availability, not an initialized payload. It remains an
// immutable SSA identity even when the place is replaced or its lifetime ends.
func (f *Func) snapshotBinding(block *ssa.Block, id BindingID, pos ast.Position) ssa.Value {
	value := f.addState(block, ssa.OpBindingSnapshot, f.bindings[id-1].typ, pos)
	block.Ops[len(block.Ops)-1].Imm = int64(id)
	return value
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
