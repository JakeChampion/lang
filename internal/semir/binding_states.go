package semir

import (
	"github.com/jakechampion/lang/internal/ast"
	"github.com/jakechampion/lang/internal/ssa"
)

type bindingStateWrite struct {
	id          BindingID
	initializer bool
	block       *ssa.Block
	value       ssa.Value
	pos         ast.Position
}

type bindingObservation struct {
	id    BindingID
	block *ssa.Block
	state ssa.Value
	// Index of the last write before this observation within its block, or -1
	// for the incoming state. Later writes must not change an old observation.
	local int
	pos   ast.Position
}

type bindingStateContract struct {
	writes       []bindingStateWrite
	observations []bindingObservation
}

// Record typed program events before promotion changes any instruction. This
// preserves the input semantics, not the promoter's inferred reaching states.
func recordBindingStates(f *Func, needed map[BindingID]bool) *bindingStateContract {
	var writes, observations int
	for _, block := range f.graph.Blocks {
		for _, op := range block.Ops {
			if !needed[BindingID(op.Imm)] {
				continue
			}
			switch op.Kind {
			case ssa.OpBindingInit, ssa.OpBindingReplace, ssa.OpBindingReplaceGuarded:
				writes++
			case ssa.OpBindingSnapshot:
				observations++
			}
		}
	}
	c := &bindingStateContract{
		writes:       make([]bindingStateWrite, 0, writes),
		observations: make([]bindingObservation, 0, observations),
	}
	last := make(map[BindingID]int)
	for _, block := range f.graph.Blocks {
		clear(last)
		for _, op := range block.Ops {
			id := BindingID(op.Imm)
			if !needed[id] {
				continue
			}
			switch op.Kind {
			case ssa.OpBindingInit, ssa.OpBindingReplace, ssa.OpBindingReplaceGuarded:
				last[id] = len(c.writes)
				c.writes = append(c.writes, bindingStateWrite{
					id: id, initializer: op.Kind == ssa.OpBindingInit,
					block: block, value: bindingWriteValue(op), pos: f.effectPositions[op],
				})
			case ssa.OpBindingSnapshot:
				local, ok := last[id]
				if !ok {
					local = -1
				}
				c.observations = append(c.observations, bindingObservation{id, block, op.Result, local, f.values[op.Result.ID].pos})
			}
		}
	}
	return c
}

func (c *bindingStateContract) rewrite(aliases ssa.ValueAliases) {
	if c == nil || len(aliases) == 0 {
		return
	}
	for i := range c.writes {
		c.writes[i].value = aliases.Resolve(c.writes[i].value)
	}
	for i := range c.observations {
		c.observations[i].state = aliases.Resolve(c.observations[i].state)
	}
}

func (c *bindingStateContract) prune(live map[int32]valueInfo) {
	kept := c.observations[:0]
	for _, observation := range c.observations {
		if _, ok := live[observation.state.ID]; ok {
			kept = append(kept, observation)
		}
	}
	clear(c.observations[len(kept):])
	c.observations = kept
}
