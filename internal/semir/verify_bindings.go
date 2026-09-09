package semir

import (
	"fmt"
	"slices"

	"github.com/jakechampion/lang/internal/ssa"
)

// Initialization is a must fact: intersect incoming states, never union them.
// The finite descending bitset lattice includes backedges without enumerating
// paths or assuming a bounded number of loop iterations. Absence is not a value.
func verifyBindingInitialization(f *Func, deadBlocks *map[*ssa.Block]bool) error {
	fail := func(op *ssa.Op, message string) error {
		pos := f.effectPositions[op]
		if op.Result.IsValid() {
			pos = f.values[op.Result.ID].pos
		}
		return fmt.Errorf("semir %s binding %d at %d:%d: %s", f.graph.Name, op.Imm, pos.Line, pos.Col, message)
	}
	if len(f.bindings) == 0 {
		return nil
	}
	words := (len(f.bindings) + 63) / 64
	ends := bindingLifetimeEnds(f)
	// Before expansion the site itself reads every capture and publishes its
	// replacement only after the action. Check availability without fabricating
	// an SSA payload or relying on the later expansion to discover an invalid read.
	var replays map[*ssa.Block]*cleanupRegion
	if f.unexpandedCleanups {
		replays = make(map[*ssa.Block]*cleanupRegion)
		for _, r := range f.cleanups {
			for _, site := range r.replays {
				replays[site] = r
			}
		}
	}
	initialized := func(state []uint64, id int64) bool { return state[(id-1)/64]&(uint64(1)<<((id-1)%64)) != 0 }
	set := func(state []uint64, id int64) { state[(id-1)/64] |= uint64(1) << ((id - 1) % 64) }
	order := f.graph.RPO()
	definitions := make([]bool, len(f.bindings))
	storage := make([]uint64, words*len(order))
	outputs := make(map[*ssa.Block][]uint64, len(order))
	for i, block := range order {
		outputs[block] = storage[i*words : (i+1)*words]
		for j := range outputs[block] {
			outputs[block][j] = ^uint64(0)
		}
	}
	for _, block := range f.graph.Blocks {
		if outputs[block] == nil && deadBlocks != nil {
			if *deadBlocks == nil {
				*deadBlocks = make(map[*ssa.Block]bool)
			}
			(*deadBlocks)[block] = true
		}
		for _, op := range block.Ops {
			if bindingOp(op.Kind) && outputs[block] == nil {
				return fail(op, "unreachable binding operation")
			}
			if op.Kind == ssa.OpBindingInit {
				if definitions[op.Imm-1] {
					return fail(op, "multiple initializer identities")
				}
				definitions[op.Imm-1] = true
			}
		}
	}
	state := make([]uint64, words)
	entryState := func(block *ssa.Block) {
		clear(state)
		if block == f.graph.Entry {
			return
		}
		first := true
		for _, pred := range block.Preds {
			if outputs[pred] == nil {
				continue
			}
			if first {
				copy(state, outputs[pred])
				first = false
			} else {
				for i, bits := range outputs[pred] {
					state[i] &= bits
				}
			}
		}
	}
	for changed := true; changed; {
		changed = false
		for _, block := range order {
			entryState(block)
			for _, op := range block.Ops {
				if op.Kind == ssa.OpBindingInit {
					set(state, op.Imm)
				}
			}
			for i, bits := range ends[block] {
				state[i] &^= bits
			}
			if !slices.Equal(state, outputs[block]) {
				copy(outputs[block], state)
				changed = true
			}
		}
	}
	for _, block := range order {
		entryState(block)
		if r := replays[block]; r != nil {
			for _, id := range r.captures {
				if !initialized(state, int64(id)) {
					return fmt.Errorf("semir %s cleanup at %d:%d: captured binding %d requires initialization on every incoming path", f.graph.Name, r.pos.Line, r.pos.Col, id)
				}
			}
		}
		for _, op := range block.Ops {
			switch op.Kind {
			case ssa.OpBindingInit:
				set(state, op.Imm)
			case ssa.OpBindingRead, ssa.OpBindingReplace:
				if !initialized(state, op.Imm) {
					return fail(op, "read or replacement requires initialization on every incoming path")
				}
			}
		}
	}
	return nil
}
