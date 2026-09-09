package semir

import (
	"github.com/jakechampion/lang/internal/ast"
	"github.com/jakechampion/lang/internal/ssa"
)

type sourceLoop struct {
	label        string
	header, exit *ssa.Block
	boundary     *cleanupBoundary
}

func (b *builder) sourceLoop(cond ast.Expr, body ast.Stmt, label string, pos ast.Position) error {
	header, exit := b.fn.graph.NewBlock(), b.fn.graph.NewBlock()
	b.fn.graph.SetBr(b.current, header)
	b.current = header
	if cond != nil {
		b.cleanupConditionDepth++
		value, err := b.expr(cond)
		b.cleanupConditionDepth--
		if err != nil {
			return err
		}
		if value.ended {
			return b.closeLoop(header, exit, nil)
		}
		work := b.fn.graph.NewBlock()
		b.fn.graph.SetBrIf(b.current, value.value, work, exit)
		b.current = work
	}
	// The condition is evaluated in the enclosing loop scope, matching the
	// checker. Only the body introduces this loop's break/continue targets.
	parent := b.cleanupScope
	var boundary *cleanupBoundary
	if b.action == nil {
		boundary = b.newCleanupBoundary(parent, b.current, header, exit, pos)
		b.cleanupScope = boundary
	}
	defer func() { b.cleanupScope = parent }()
	b.loops = append(b.loops, sourceLoop{label, header, exit, boundary})
	defer func() { b.loops = b.loops[:len(b.loops)-1] }()
	b.pushScope()
	err := b.stmt(body)
	b.popScope()
	if err != nil {
		return err
	}
	if b.current != nil {
		if boundary != nil {
			if err := b.emitCleanupExit(boundary, cleanupTail, header); err != nil {
				return err
			}
		}
		b.fn.graph.SetBr(b.current, header)
	}
	return b.closeLoop(header, exit, boundary)
}

func (b *builder) closeLoop(header, exit *ssa.Block, boundary *cleanupBoundary) error {
	// A jump ended only the body path. A while's false condition can still
	// reach its exit, so subsequent source statements belong there. With no
	// incoming edge (an unconditional loop without a local break), discard
	// the exit below and preserve the lack of a continuation.
	b.current = exit
	if len(exit.Preds) == 0 {
		if boundary != nil {
			boundary.exit = nil
		}
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
	return nil
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
		if loop.boundary != nil {
			kind := cleanupBreak
			if continuing {
				kind = cleanupContinue
			}
			if err := b.emitCleanupExit(loop.boundary, kind, target); err != nil {
				return err
			}
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
		if f.bindingStates == nil {
			ssa.TrivialPhis(f.graph)
		} else {
			aliases := make(ssa.ValueAliases)
			ssa.TrivialPhisWithAliases(f.graph, aliases)
			f.bindingStates.rewrite(aliases)
		}
		ssa.DCE(f.graph)
		live := make(map[int32]valueInfo)
		for _, param := range f.graph.Params {
			live[param.ID] = f.values[param.ID]
		}
		for _, block := range f.graph.Blocks {
			for _, op := range block.Ops {
				if op.Result.IsValid() {
					live[op.Result.ID] = f.values[op.Result.ID]
				}
			}
		}
		f.values = live
		if f.bindingStates != nil {
			f.bindingStates.prune(live)
			if len(f.bindingStates.observations) == 0 {
				f.bindingStates = nil
			}
		}
		if before == len(live) {
			return Verify(f)
		}
	}
}
