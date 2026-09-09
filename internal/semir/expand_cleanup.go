package semir

import (
	"fmt"

	"github.com/jakechampion/lang/internal/ast"
	"github.com/jakechampion/lang/internal/ssa"
)

// expandCleanups consumes a complete, verified typed CFG. It needs neither
// source syntax nor checker state: captures, actions and continuations already
// have explicit identities. Binding promotion must follow this phase so late
// capture reads and action-to-action writes participate in ordinary SSA flow.
func expandCleanups(f *Func) error {
	if !f.unexpandedCleanups || !f.unpromotedBindings {
		return fmt.Errorf("semir %s: cleanup expansion outside construction phase", f.graph.Name)
	}
	if len(f.cleanups) != 0 {
		var facts verificationFacts
		if err := verifyWithFacts(f, &facts); err != nil {
			return err
		}
		if facts.conditional != nil {
			prepareCleanupActivation(f)
		}
		exits := make(map[*ssa.Block]*ssa.Block)
		for _, r := range f.cleanups {
			for _, site := range r.replays {
				end := site
				if r.guarded == nil {
					end = expandCleanupSite(f, r, site)
				} else {
					end = expandGuardedCleanupSite(f, r, site)
				}
				if end != site {
					exits[site] = end
				}
			}
		}
		// Relocate boundary endpoints once, not by scanning every exit for
		// every action. Expansion does not consult these records mid-pass.
		for _, exit := range f.cleanupExits {
			if end := exits[exit.finish]; end != nil {
				exit.finish = end
			}
			for i, old := range exit.ends {
				if end := exits[old.block]; end != nil {
					exit.ends[i].block = end
				}
			}
		}
	}
	f.unexpandedCleanups = false
	return nil
}

func expandCleanupSite(f *Func, r *cleanupRegion, site *ssa.Block) *ssa.Block {
	continuation := site.Term
	successors := site.Succs()
	values := make(map[int32]ssa.Value, len(r.body.values))
	for i, id := range r.captures {
		value := f.readBinding(site, id, r.pos)
		values[r.body.graph.Params[i].ID] = value
	}
	current := cloneCleanupBody(f, r, site, values)
	// All output values are computed before publishing any binding update.
	for i, id := range r.captures {
		f.writeBinding(current, ssa.OpBindingReplace, id, values[r.yield.Args[i].ID], r.pos)
	}
	relocateCleanupContinuation(site, current, successors)
	current.Term = continuation
	return current
}

// Both ordinary and guarded replay clone the same typed body. The caller maps
// private parameters to late capture values and publishes the yielded outputs.
func cloneCleanupBody(f *Func, r *cleanupRegion, site *ssa.Block, values map[int32]ssa.Value) *ssa.Block {
	blocks := make(map[*ssa.Block]*ssa.Block, len(r.body.graph.Blocks))
	for _, old := range r.body.graph.Blocks {
		if old == r.body.graph.Entry {
			blocks[old] = site
		} else {
			blocks[old] = f.graph.NewBlock()
		}
		for _, op := range old.Ops {
			if op == r.yield || !op.Result.IsValid() {
				continue
			}
			v := f.graph.NewValue()
			values[op.Result.ID] = v
			f.values[v.ID] = r.body.values[op.Result.ID]
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
				if f.effectPositions == nil {
					f.effectPositions = make(map[*ssa.Op]ast.Position)
				}
				f.effectPositions[&copyOp] = r.body.effectPositions[op]
			}
		}
		term := old.Term
		term.Cond = values[term.Cond.ID]
		term.Target, term.True, term.False = blocks[term.Target], blocks[term.True], blocks[term.False]
		term.Value = ssa.Value{}
		block.Term = term
	}
	current := blocks[r.exit]
	current.Term = ssa.Terminator{}
	return current
}

func relocateCleanupContinuation(site, current *ssa.Block, successors []*ssa.Block) {
	// Move the original continuation to the expanded action's exit. Replace
	// predecessor identities in place, preserving successor phi operand order.
	for _, successor := range successors {
		for i, pred := range successor.Preds {
			if pred == site {
				successor.Preds[i] = current
			}
		}
	}
}
