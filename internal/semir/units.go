package semir

import "github.com/jakechampion/lang/internal/ssa"

type unitMode uint8

const (
	unitInvalid unitMode = iota
	unitRetain
	unitMove
	unitImmortal
)

// unitSupply satisfies one occurrence, not all aliases of a value. Retains
// must execute before moves, and all supplies before drops. The slot identifies
// a store/call argument, or a phi result by its position in the successor.
type unitSupply struct {
	value ssa.Value
	slot  int
	mode  unitMode
}

type unitStep struct {
	supplies []unitSupply
	// copyElements acquires child units, not a unit for the array buffer.
	// It is a separate bulk operation in the copy-form append contract.
	copyElements []ssa.Value
	drops        []ssa.Value
}

type flowEdge struct{ from, to *ssa.Block }

// functionUnits is a proposed plan, not a certificate. Calls assume the new
// closed-module counted-result ABI. No existing backend may consume a plan
// until independent unit verification and explicit RC lowering are connected.
type functionUnits struct {
	function *Func
	lifetime *lifetimeFlow
	entry    map[*ssa.Block][]ssa.Value
	ops      map[*ssa.Op]unitStep
	edges    map[flowEdge]unitStep
	returns  map[*ssa.Block]unitStep
}

func planFunctionUnits(f *Func) (*functionUnits, error) {
	effects, err := ownershipEffects(f)
	if err != nil {
		return nil, err
	}
	l := analyzeLifetimes(f, effects)
	p := &functionUnits{
		function: f, lifetime: l, entry: l.entryDead,
		ops: make(map[*ssa.Op]unitStep), edges: make(map[flowEdge]unitStep),
		returns: make(map[*ssa.Block]unitStep),
	}
	for _, b := range f.graph.Blocks {
		if !l.reachable[b] {
			continue
		}
		for _, op := range b.Ops {
			if op.Kind == ssa.OpPhi {
				continue
			}
			effect := effects.ops[op]
			step := unitStep{}
			// A consuming callee may release its own argument before it
			// finishes reading another borrowed argument. Keep any caller
			// unit anchoring that borrow alive across the entire call.
			hold := make(map[int32]bool)
			for i, input := range effect.inputs {
				if op.Kind == ssa.OpSemanticCall && !input.consume {
					l.addUse(hold, input.value)
				}
				if input.consume || (input.store == storeValue && input.counted) {
					step.supplies = append(step.supplies, unitSupply{value: input.value, slot: i})
				}
				if input.store == storeArrayElements && input.counted {
					step.copyElements = append(step.copyElements, input.value)
				}
			}
			p.satisfy(&step, l.last[op], hold)
			p.ops[op] = step
		}
		if b.Term.Kind == ssa.TermRet {
			step := unitStep{}
			value := b.Term.Value
			if referenceBearing(f.values[value.ID].typ) {
				step.supplies = []unitSupply{{value: value}}
			}
			last := make(map[int32]bool)
			l.addUse(last, value)
			p.satisfy(&step, last, nil)
			p.returns[b] = step
		}
		for _, succ := range b.Succs() {
			step := unitStep{}
			pi := -1
			for i, pred := range succ.Preds {
				if pred == b {
					pi = i
					break
				}
			}
			for i, op := range succ.Ops {
				if op.Kind == ssa.OpPhi && l.owned[op.Result.ID] {
					step.supplies = append(step.supplies, unitSupply{value: op.Args[pi], slot: i})
				}
			}
			dead := make(map[int32]bool)
			for id := range l.live.LiveOut[b] {
				if !l.live.LiveIn[succ][id] {
					dead[id] = true
				}
			}
			p.satisfy(&step, dead, nil)
			p.edges[flowEdge{b, succ}] = step
		}
	}
	return p, nil
}

func (p *functionUnits) satisfy(step *unitStep, dead, hold map[int32]bool) {
	moved := make(map[int32]bool)
	// Choose the last occurrence for a move, so duplicate stores first
	// acquire their independent units. Every existing unit can move once.
	for i := len(step.supplies) - 1; i >= 0; i-- {
		supply := &step.supplies[i]
		id := supply.value.ID
		switch {
		case p.lifetime.immortal[id]:
			supply.mode = unitImmortal
		case p.lifetime.owned[id] && dead[id] && !hold[id] && !moved[id]:
			supply.mode = unitMove
			moved[id] = true
		default:
			supply.mode = unitRetain
		}
	}
	for id := range dead {
		if p.lifetime.owned[id] && !moved[id] {
			step.drops = append(step.drops, ssa.Value{ID: id, Func: p.function.graph})
		}
	}
	sortValues(step.drops)
}
