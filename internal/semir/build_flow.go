package semir

import (
	"fmt"

	"github.com/jakechampion/lang/internal/ast"
	"github.com/jakechampion/lang/internal/ssa"
)

// A block is sealed once all its incoming edges are known. Reads in an open
// loop header create typed incomplete phis, filled when its back edges exist.
// Binding identity is lexical; each block maps it to its own SSA value.
type bindingFlow struct {
	sealed  bool
	values  map[BindingID]ssa.Value
	pending []bindingPhi
}

type bindingPhi struct {
	id BindingID
	op *ssa.Op
}

type sourceLoop struct {
	label        string
	header, exit *ssa.Block
}

func (b *builder) state(block *ssa.Block) *bindingFlow {
	if b.flow[block] == nil {
		b.flow[block] = &bindingFlow{values: make(map[BindingID]ssa.Value)}
	}
	return b.flow[block]
}

func (b *builder) readBinding(block *ssa.Block, id BindingID) (ssa.Value, error) {
	state := b.state(block)
	if value, ok := state.values[id]; ok {
		return value, nil
	}
	if state.sealed && len(block.Preds) == 1 {
		value, err := b.readBinding(block.Preds[0], id)
		if err == nil {
			state.values[id] = value
		}
		return value, err
	}
	if state.sealed && len(block.Preds) == 0 {
		return ssa.Value{}, fmt.Errorf("semir: binding %d has no definition on entry to block %d", id, block.ID)
	}
	decl := b.fn.bindings[id-1]
	value := b.fn.addPhi(block, decl.typ, decl.pos)
	var op *ssa.Op
	for _, candidate := range block.Ops {
		if candidate.Result == value {
			op = candidate
			break
		}
	}
	state.values[id] = value // Publish before recursively reading back edges.
	phi := bindingPhi{id, op}
	if !state.sealed {
		state.pending = append(state.pending, phi)
		return value, nil
	}
	return value, b.fillPhi(block, phi)
}

func (b *builder) fillPhi(block *ssa.Block, phi bindingPhi) error {
	for _, pred := range block.Preds {
		value, err := b.readBinding(pred, phi.id)
		if err != nil {
			return err
		}
		phi.op.Args = append(phi.op.Args, value)
	}
	return nil
}

func (b *builder) seal(block *ssa.Block) error {
	state := b.state(block)
	state.sealed = true
	for _, phi := range state.pending {
		if err := b.fillPhi(block, phi); err != nil {
			return err
		}
	}
	state.pending = nil
	return nil
}

func (b *builder) sourceLoop(cond ast.Expr, body ast.Stmt, label string) error {
	header, exit := b.fn.graph.NewBlock(), b.fn.graph.NewBlock()
	b.fn.graph.SetBr(b.current, header)
	b.current = header
	b.loops = append(b.loops, sourceLoop{label, header, exit})
	defer func() { b.loops = b.loops[:len(b.loops)-1] }()
	if cond != nil {
		value, err := b.expr(cond)
		if err != nil {
			return err
		}
		work := b.fn.graph.NewBlock()
		b.fn.graph.SetBrIf(b.current, value, work, exit)
		b.current = work
		if err := b.seal(work); err != nil {
			return err
		}
	}
	b.pushScope()
	err := b.stmt(body)
	b.popScope()
	if err != nil {
		return err
	}
	if b.current != nil {
		b.fn.graph.SetBr(b.current, header)
	}
	if err := b.seal(header); err != nil {
		return err
	}
	b.current = exit
	if len(exit.Preds) == 0 {
		// An unconditional loop without a break has no continuation. Do not
		// fabricate an undefined return or incoming value in a detached block.
		for i, block := range b.fn.graph.Blocks {
			if block == exit {
				b.fn.graph.Blocks = append(b.fn.graph.Blocks[:i], b.fn.graph.Blocks[i+1:]...)
				break
			}
		}
		b.current = nil
		return nil
	}
	return b.seal(exit)
}

func (b *builder) loopBranch(label string, continuing bool, pos ast.Position) error {
	for i := len(b.loops) - 1; i >= 0; i-- {
		loop := b.loops[i]
		if label != "" && label != loop.label {
			continue
		}
		target := loop.exit
		if continuing {
			target = loop.header
		}
		b.fn.graph.SetBr(b.current, target)
		b.current = nil
		return nil
	}
	return b.errorAt(pos, "unresolved loop label: "+label)
}

func finishFlow(f *Func) error {
	if err := Verify(f); err != nil {
		return err
	}
	// These two existing passes operate only on explicit value identities and
	// pure unused results. Semantic allocations/projections/calls are impure,
	// so they cannot be erased. TrivialPhis preserves result IDs when it
	// materializes constants; retain their semantic types and source spans.
	// No width-dependent folding or low-level ownership analysis runs here.
	for {
		before := len(f.values)
		ssa.TrivialPhis(f.graph)
		ssa.DCE(f.graph)
		live := make(map[int32]valueInfo)
		for _, param := range f.graph.Params {
			live[param.ID] = f.values[param.ID]
		}
		for _, block := range f.graph.Blocks {
			for _, op := range block.Ops {
				live[op.Result.ID] = f.values[op.Result.ID]
			}
		}
		f.values = live
		if before == len(live) {
			return Verify(f)
		}
	}
}
