package semir

import (
	"fmt"

	"github.com/jakechampion/lang/internal/ast"
	"github.com/jakechampion/lang/internal/ssa"
)

// A cleanup action is built once as typed SSA, independently of registration
// values. Parameters and yield fields address the same enclosing BindingIDs.
// The tuple yield is a region interface, removed during expansion before RC;
// it never allocates an environment or return tuple in the enclosing function.
type cleanupRegion struct {
	owner    *Func
	body     *Func
	pos      ast.Position
	register *ssa.Block
	captures []BindingID
	boundary *cleanupBoundary
	exit     *ssa.Block
	yield    *ssa.Op
	replays  []*ssa.Block
}

type cleanupBuilder struct {
	parent *builder
	region *cleanupRegion
	locals []BindingID
}

func (b *builder) captureBinding(name string) (BindingID, bool) {
	a := b.action
	parentID, ok := a.parent.lookup(name)
	if !ok {
		return 0, false
	}
	decl := a.parent.fn.bindings[parentID-1]
	mode := ParamValue
	if referenceBearing(decl.typ) {
		mode = ParamBorrow
	}
	param := b.fn.addParam(decl.typ, mode, decl.pos)
	id := b.fn.addBinding(name, decl.typ, decl.pos)
	// Captures live in the outermost action scope. An action-local declaration
	// can shadow one without changing the captured enclosing binding's identity.
	b.scopes[0][name] = id
	// Captures are discovered on demand, including from nested blocks. Their
	// parameter initialization belongs at region entry before any region read.
	op := b.fn.writeBinding(b.fn.graph.Entry, ssa.OpBindingInit, id, param, decl.pos)
	entry := b.fn.graph.Entry
	entry.Ops = append([]*ssa.Op{op}, entry.Ops[:len(entry.Ops)-1]...)
	a.region.captures = append(a.region.captures, parentID)
	a.locals = append(a.locals, id)
	return id, true
}

func (b *builder) registerCleanup(n *ast.Defer) error {
	if b.action != nil {
		return b.errorAt(n.P, "nested cleanup registration is not implemented in the typed pilot")
	}
	if n.OnError {
		return b.errorAt(n.P, "error-only cleanup is not implemented in the typed pilot")
	}
	if b.cleanupConditionDepth != 0 {
		return b.errorAt(n.P, "loop-condition cleanup registration is not implemented in the typed pilot")
	}
	r := &cleanupRegion{owner: b.fn, pos: n.P, register: b.current, boundary: b.cleanupScope}
	r.body = newFunc(fmt.Sprintf("%s.cleanup%d", b.fn.graph.Name, len(b.fn.cleanups)), ast.VoidType{})
	r.body.program = b.fn.program
	r.body.unpromotedBindings = true
	a := builder{fn: r.body, info: b.info, current: r.body.graph.NewBlock()}
	a.action = &cleanupBuilder{parent: b, region: r}
	a.pushScope()
	if err := a.effectExpr(n.Expr); err != nil {
		return err
	}
	if a.current == nil {
		return b.errorAt(n.P, "non-completing cleanup action is not implemented in the typed pilot")
	}
	var types []ast.Type
	var values []ssa.Value
	for i, id := range a.action.locals {
		value := a.fn.readBinding(a.current, id, n.P)
		values = append(values, value)
		types = append(types, b.fn.bindings[r.captures[i]-1].typ)
	}
	r.body.result = ast.TupleType{Elems: types}
	result := r.body.addOp(a.current, ssa.OpTupleMake, r.body.result, n.P, values...)
	r.yield = a.current.Ops[len(a.current.Ops)-1]
	r.exit = a.current
	r.body.graph.SetRet(a.current, result)
	if err := promoteBindings(r.body); err != nil {
		return err
	}
	b.fn.cleanups = append(b.fn.cleanups, r)
	b.cleanupScope.actions = append(b.cleanupScope.actions, r)
	return nil
}

func (b *builder) emitCleanups() error {
	return b.emitCleanupExit(b.fn.boundaries[0], cleanupReturn, nil)
}

func (b *builder) expandCleanup(r *cleanupRegion) error {
	values := make(map[int32]ssa.Value, len(r.body.values))
	for i, id := range r.captures {
		value := b.fn.readBinding(b.current, id, r.pos)
		values[r.body.graph.Params[i].ID] = value
	}
	blocks := make(map[*ssa.Block]*ssa.Block, len(r.body.graph.Blocks))
	for _, old := range r.body.graph.Blocks {
		blocks[old] = b.fn.graph.NewBlock()
		for _, op := range old.Ops {
			if op == r.yield || !op.Result.IsValid() {
				continue
			}
			v := b.fn.graph.NewValue()
			values[op.Result.ID] = v
			b.fn.values[v.ID] = r.body.values[op.Result.ID]
		}
	}
	for _, old := range r.body.graph.Blocks {
		block := blocks[old]
		// Preserve predecessor order exactly: phi operands use that order,
		// which need not match source block allocation or traversal order.
		for _, pred := range old.Preds {
			block.Preds = append(block.Preds, blocks[pred])
		}
		for _, op := range old.Ops {
			if op == r.yield {
				continue
			}
			copyOp := *op
			copyOp.Result = values[op.Result.ID]
			copyOp.Args = make([]ssa.Value, len(op.Args))
			for i, arg := range op.Args {
				copyOp.Args[i] = values[arg.ID]
			}
			block.Ops = append(block.Ops, &copyOp)
			if !op.Result.IsValid() {
				if b.fn.effectPositions == nil {
					b.fn.effectPositions = make(map[*ssa.Op]ast.Position)
				}
				b.fn.effectPositions[&copyOp] = r.body.effectPositions[op]
			}
		}
		term := old.Term
		term.Cond = values[term.Cond.ID]
		term.Target, term.True, term.False = blocks[term.Target], blocks[term.True], blocks[term.False]
		term.Value = ssa.Value{}
		block.Term = term
	}
	entry := blocks[r.body.graph.Entry]
	b.fn.graph.SetBr(b.current, entry)
	r.replays = append(r.replays, entry)
	b.current = blocks[r.exit]
	// The protocol return is now an ordinary continuation. All capture outputs
	// are read before publishing any updates, so simultaneous replacements do
	// not accidentally read another output's newly installed binding value.
	b.current.Term = ssa.Terminator{}
	for i, id := range r.captures {
		b.fn.writeBinding(b.current, ssa.OpBindingReplace, id, values[r.yield.Args[i].ID], r.pos)
	}
	return nil
}
