package semir

import (
	"github.com/jakechampion/lang/internal/ast"
	"github.com/jakechampion/lang/internal/ssa"
)

const (
	statePresenceLane uint8 = 1 << iota
	statePayloadLane
)

// A state component only needs a machine definition if a semantic use observes
// it. Propagate lane demand through phis, including cycles, once per lane/edge.
// Verified conditional RC adds physical uses; this is not an ownership proof.
func availabilityLaneDemand(f *Func, plan *functionUnits) map[int32]uint8 {
	needed := make(map[int32]uint8)
	phis := make(map[int32]*ssa.Op)
	type demand struct {
		id   int32
		lane uint8
	}
	var queue []demand
	mark := func(id int32, lane uint8) {
		if needed[id]&lane == 0 {
			needed[id] |= lane
			queue = append(queue, demand{id, lane})
		}
	}
	for _, block := range f.graph.Blocks {
		for _, op := range block.Ops {
			switch op.Kind {
			case ssa.OpStateHas:
				mark(op.Args[0].ID, statePresenceLane)
			case ssa.OpStateGet:
				mark(op.Args[0].ID, statePayloadLane)
			case ssa.OpPhi:
				if f.values[op.Result.ID].typ.form == availabilityForm {
					phis[op.Result.ID] = op
				}
			}
		}
	}
	if plan != nil {
		drops := func(values []ssa.Value) {
			for _, value := range values {
				if f.values[value.ID].typ.conditionalUnit() {
					mark(value.ID, statePresenceLane)
					mark(value.ID, statePayloadLane)
				}
			}
		}
		step := func(step unitStep) {
			for _, supply := range step.supplies {
				if supply.mode == unitRetain && supply.conditional {
					mark(supply.value.ID, statePresenceLane)
					mark(supply.value.ID, statePayloadLane)
				}
			}
			drops(step.drops)
		}
		for _, values := range plan.entry {
			drops(values)
		}
		for _, s := range plan.ops {
			step(s)
		}
		for _, s := range plan.edges {
			step(s)
		}
		for _, s := range plan.returns {
			step(s)
		}
	}
	for next := 0; next < len(queue); next++ {
		at := queue[next]
		if phi := phis[at.id]; phi != nil {
			for _, arg := range phi.Args {
				mark(arg.ID, at.lane)
			}
		}
	}
	return needed
}

// Guard payload operations by the semantic discriminant, never by pointer
// bits. The payload lane has no meaning on the absent path.
func (b *armBuilder) whenPresent(flag ssa.Value, action func()) {
	yes, next := b.f.NewBlock(), b.f.NewBlock()
	b.f.SetBrIf(b.b, flag, yes, next)
	b.b = yes
	action()
	b.f.SetBr(b.b, next)
	b.b = next
}

func (b *armBuilder) dropSemantic(src *Func, value ssa.Value, values, payloads map[int32]ssa.Value) {
	typ := src.values[value.ID].typ
	if typ.conditionalUnit() {
		b.whenPresent(values[value.ID], func() { b.drop(payloads[value.ID], typ.source) })
	} else {
		b.drop(values[value.ID], typ.source)
	}
}

func (b *armBuilder) phi(typ ast.Type) ssa.Value {
	v := b.f.AddPhi(b.b)
	for _, phi := range b.b.Ops {
		if phi.Result == v {
			phi.Width, phi.Addr = armValueShape(typ)
			b.l.out.Positions[phi] = b.pos
		}
	}
	return v
}
