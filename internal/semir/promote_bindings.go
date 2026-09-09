package semir

import (
	"fmt"

	"github.com/jakechampion/lang/internal/ssa"
)

// promoteBindings consumes verified typed places, not syntax or source names.
// It resolves each read at its instruction position, using complete CFG edges
// for pruned, demand-driven SSA joins. No place or fake absent payload reaches
// ownership planning, and replacement lifetimes become ordinary SSA liveness.
// The enclosing VerifyProgram checks the promoted result before any ownership
// consumer; it also checks promoted private action regions through their owner.
func promoteBindings(f *Func) error {
	if f.unexpandedCleanups {
		return fmt.Errorf("semir %s: binding promotion requires expanded cleanup actions", f.graph.Name)
	}
	if !f.unpromotedBindings {
		return fmt.Errorf("semir %s: binding promotion outside construction phase", f.graph.Name)
	}
	var facts verificationFacts
	if err := verifyWithFacts(f, &facts); err != nil {
		return err
	}
	pruneDeadBindingPredecessors(f, facts.deadBlocks)
	ends := bindingLifetimeEnds(f)
	type key struct {
		block *ssa.Block
		id    BindingID
	}
	last := make(map[key]ssa.Value)
	entry := make(map[key]ssa.Value)
	type bindingRead struct {
		op    *ssa.Op
		block *ssa.Block
		local ssa.Value
	}
	var reads []bindingRead
	var snapshots map[BindingID]bool
	for _, block := range f.graph.Blocks {
		for _, op := range block.Ops {
			if op.Kind == ssa.OpBindingInit || op.Kind == ssa.OpBindingReplace || op.Kind == ssa.OpBindingReplaceGuarded {
				last[key{block, BindingID(op.Imm)}] = bindingWriteValue(op)
			} else if op.Kind == ssa.OpBindingRead {
				reads = append(reads, bindingRead{op, block, last[key{block, BindingID(op.Imm)}]})
			} else if op.Kind == ssa.OpBindingSnapshot {
				if snapshots == nil {
					snapshots = make(map[BindingID]bool)
				}
				snapshots[BindingID(op.Imm)] = true
			}
		}
	}
	if len(snapshots) != 0 {
		f.bindingStates = recordBindingStates(f, snapshots)
		aliases := promoteBindingSnapshots(f, snapshots, ends)
		f.rewriteBindingAliases(aliases)
	}
	var readEntry func(*ssa.Block, BindingID) (ssa.Value, error)
	readEnd := func(block *ssa.Block, id BindingID) (ssa.Value, error) {
		if mask := ends[block]; mask != nil && mask[(id-1)/64]&(uint64(1)<<((id-1)%64)) != 0 {
			return ssa.Value{}, fmt.Errorf("semir %s: binding %d is absent after its lifetime end", f.graph.Name, id)
		}
		if value, ok := last[key{block, id}]; ok {
			return value, nil
		}
		return readEntry(block, id)
	}
	readEntry = func(block *ssa.Block, id BindingID) (ssa.Value, error) {
		k := key{block, id}
		if value, ok := entry[k]; ok {
			return value, nil
		}
		if len(block.Preds) == 0 {
			return ssa.Value{}, fmt.Errorf("semir %s: initialized binding %d lacks a reaching definition", f.graph.Name, id)
		}
		if len(block.Preds) == 1 {
			value, err := readEnd(block.Preds[0], id)
			if err == nil {
				entry[k] = value
			}
			return value, err
		}
		decl := f.bindings[id-1]
		value := f.addPhi(block, decl.typ, decl.pos)
		entry[k] = value // Break loop recursion before asking predecessor edges.
		var phi *ssa.Op
		for _, op := range block.Ops {
			if op.Result == value {
				phi = op
				break
			}
		}
		for _, pred := range block.Preds {
			incoming, err := readEnd(pred, id)
			if err != nil {
				return ssa.Value{}, err
			}
			phi.Args = append(phi.Args, incoming)
		}
		return value, nil
	}
	replacements := make(map[int32]ssa.Value)
	// Read positions were recorded before phi insertion, which can mutate a
	// block's operation slice in place. Never traverse that slice while adding phis.
	for _, read := range reads {
		value := read.local
		if !value.IsValid() {
			var err error
			value, err = readEntry(read.block, BindingID(read.op.Imm))
			if err != nil {
				return err
			}
		}
		replacements[read.op.Result.ID] = value
	}
	// Canonicalize read aliases once. Repeated assignments such as x = x must
	// not turn every later operand rewrite into a walk of the full read chain.
	for id := range replacements {
		value := replacements[id]
		steps := 0
		for {
			next, ok := replacements[value.ID]
			if !ok {
				break
			}
			steps++
			if steps > len(replacements) {
				return fmt.Errorf("semir %s: cyclic binding read aliases", f.graph.Name)
			}
			value = next
		}
		for at := id; at != value.ID; {
			next := replacements[at]
			replacements[at] = value
			at = next.ID
		}
	}
	rewrite := func(value ssa.Value) ssa.Value {
		if replacement, ok := replacements[value.ID]; ok {
			return replacement
		}
		return value
	}
	for _, block := range f.graph.Blocks {
		ops := block.Ops[:0]
		for _, op := range block.Ops {
			if bindingOp(op.Kind) {
				delete(f.values, op.Result.ID)
				delete(f.effectPositions, op)
				continue
			}
			for i, arg := range op.Args {
				op.Args[i] = rewrite(arg)
			}
			ops = append(ops, op)
		}
		block.Ops = ops
		block.Term.Value = rewrite(block.Term.Value)
		block.Term.Cond = rewrite(block.Term.Cond)
	}
	f.rewriteBindingAliases(replacements)
	f.unpromotedBindings = false
	return nil
}

// A live read can have a dead cyclic predecessor even when the read itself is
// reachable. Normalize those edges before either reaching-definition walk.
// Reuse SSA's pruning to preserve phi/predecessor order and remove semantic
// metadata for exactly the discarded operations. Verified cleanup events cannot
// be dead; an unused optional iteration exit may be, and ceases to name a block.
func pruneDeadBindingPredecessors(f *Func, dead map[*ssa.Block]bool) {
	if len(dead) == 0 {
		return
	}
	for block := range dead {
		for _, op := range block.Ops {
			if op.Result.IsValid() {
				delete(f.values, op.Result.ID)
			} else {
				delete(f.effectPositions, op)
			}
		}
	}
	for _, boundary := range f.boundaries {
		if dead[boundary.exit] {
			boundary.exit = nil
		}
	}
	ssa.PruneUnreachable(f.graph)
}
