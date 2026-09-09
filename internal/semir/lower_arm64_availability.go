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
// This is scalar-state lowering, not a reference lifetime or ownership proof.
func availabilityLaneDemand(f *Func) map[int32]uint8 {
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
