package semir

import (
	"fmt"

	"github.com/jakechampion/lang/internal/ssa"
)

// Reconstruct observation equations from typed writes, current predecessor slots
// and lifetime ends. This verifier neither calls the promoter nor consumes its
// reaching-definition cache. Cycles close only repeated value/place/entry proof
// obligations; all incoming initializer/absence paths must still discharge.
func verifyBindingStates(f *Func, dom *ssa.DomTree) error {
	c := f.bindingStates
	type point struct {
		block *ssa.Block
		id    BindingID
	}
	type definition struct {
		block *ssa.Block
		op    *ssa.Op
	}
	type obligation struct {
		point
		value ssa.Value
	}
	blocks := make(map[*ssa.Block]bool, len(f.graph.Blocks))
	defs := make(map[int32]definition, len(f.values))
	for _, block := range f.graph.Blocks {
		blocks[block] = true
		for _, op := range block.Ops {
			if op.Result.IsValid() {
				defs[op.Result.ID] = definition{block, op}
			}
		}
	}
	last := make(map[point]int)
	for i, write := range c.writes {
		if !blocks[write.block] || write.id == 0 || int(write.id) > len(f.bindings) {
			return fmt.Errorf("semir %s: invalid promoted binding write identity", f.graph.Name)
		}
		if write.value.ID <= 0 || write.value.Func != f.graph {
			return fmt.Errorf("semir %s binding %d at %d:%d: invalid promoted write payload identity", f.graph.Name, write.id, write.pos.Line, write.pos.Col)
		}
		// DCE may discard an unobserved pure payload. Keep its historical
		// identity without retaining a runtime use; queried writes below must
		// still have an actual Present of that payload in the verified graph.
		if info, live := f.values[write.value.ID]; live && !sameValueType(info.typ, sourceValueType(f.bindings[write.id-1].typ)) {
			return fmt.Errorf("semir %s binding %d at %d:%d: invalid promoted write payload type", f.graph.Name, write.id, write.pos.Line, write.pos.Col)
		}
		last[point{write.block, write.id}] = i
	}
	ends := bindingLifetimeEnds(f)
	seen := make(map[obligation]bool)
	var queue []obligation
	check := func(observation bindingObservation) error {
		fail := func(message string) error {
			return fmt.Errorf("semir %s binding %d at %d:%d: promoted snapshot %s", f.graph.Name, observation.id, observation.pos.Line, observation.pos.Col, message)
		}
		if !blocks[observation.block] || observation.id == 0 || int(observation.id) > len(f.bindings) ||
			observation.state.Func != f.graph || !sameValueType(f.values[observation.state.ID].typ, valueType{source: f.bindings[observation.id-1].typ, form: availabilityForm}) {
			return fail("has an invalid observation identity or type")
		}
		if def := defs[observation.state.ID]; def.op == nil || !dom.Dominates(def.block, observation.block) {
			return fail("is unavailable at its observation block")
		}
		present := func(value ssa.Value, write bindingStateWrite) bool {
			op := defs[value.ID].op
			return value.Func == f.graph && op != nil && op.Kind == ssa.OpStatePresent && op.Args[0] == write.value
		}
		if observation.local >= 0 {
			if observation.local >= len(c.writes) {
				return fail("has an invalid local write")
			}
			write := c.writes[observation.local]
			if write.block != observation.block || write.id != observation.id || !present(observation.state, write) {
				return fail("does not preserve its local write payload")
			}
			return nil
		}
		if observation.local != -1 {
			return fail("has an invalid incoming-state position")
		}
		queue = append(queue[:0], obligation{point{observation.block, observation.id}, observation.state})
		for next := 0; next < len(queue); next++ {
			at := queue[next]
			if seen[at] {
				continue
			}
			seen[at] = true
			def := defs[at.value.ID]
			if def.op == nil || at.value.Func != f.graph {
				return fail("has a missing incoming state")
			}
			if len(at.block.Preds) == 0 {
				if def.op.Kind != ssa.OpStateAbsent {
					return fail("invents an entry payload")
				}
				continue
			}
			for i, pred := range at.block.Preds {
				value := at.value
				if def.block == at.block && def.op.Kind == ssa.OpPhi {
					value = def.op.Args[i]
				}
				if mask := ends[pred]; mask != nil && mask[(at.id-1)/64]&(uint64(1)<<((at.id-1)%64)) != 0 {
					if end := defs[value.ID].op; end == nil || end.Kind != ssa.OpStateAbsent {
						return fail("retains a payload after its lifetime end")
					}
				} else if index, ok := last[point{pred, at.id}]; ok {
					if !present(value, c.writes[index]) {
						return fail("changes a reaching write payload or predecessor slot")
					}
				} else {
					queue = append(queue, obligation{point{pred, at.id}, value})
				}
			}
		}
		return nil
	}
	if len(c.observations) == 1 {
		return check(c.observations[0])
	}
	// Share obligations across observations of one binding, but release that
	// scratch state before the next binding. A global map unnecessarily retains
	// the Cartesian product of all places and their traversed value/CFG states.
	// Linked indices preserve first-occurrence order without copying records or
	// requiring the semantic event stream itself to be sorted.
	first := make(map[BindingID]int)
	next := make([]int, len(c.observations))
	for i := len(c.observations) - 1; i >= 0; i-- {
		id := c.observations[i].id
		next[i], first[id] = first[id], i+1
	}
	for i, observation := range c.observations {
		if first[observation.id] != i+1 {
			continue
		}
		clear(seen)
		for at := i + 1; at != 0; at = next[at-1] {
			if err := check(c.observations[at-1]); err != nil {
				return err
			}
		}
	}
	return nil
}
