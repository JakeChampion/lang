package semir

import (
	"fmt"

	"github.com/jakechampion/lang/internal/ssa"
)

// Stack nodes are canonical: equal action sequences have the same identity.
// Transfer and join comparison do not copy or scan all pending registrations.
type cleanupStack struct {
	previous int
	action   *cleanupRegion
}

type cleanupFlowState struct {
	pending int
	// Function-scope registrations cannot reset after replay. Keeping their
	// history rejects a cycle that both registers and consumes an action: its
	// pending stack alone would misleadingly agree on the backedge. Iteration
	// history resets only when its verified iteration boundary closes.
	registered int
	scope      int
}

type cleanupScopeState struct {
	previous            int
	boundary            *cleanupBoundary
	pending, registered int
}

func indexCleanupActions(f *Func, boundaries *cleanupBoundaryFlow) error {
	fail := func(message string) error { return fmt.Errorf("semir %s cleanup: %s", f.graph.Name, message) }
	for _, r := range f.cleanups {
		point := boundaries.points[r.register]
		point.registers = append(point.registers, r)
		for _, block := range r.replays {
			if boundaries.points[block].replay != nil {
				return fail("multiple actions share a replay entry")
			}
			boundaries.points[block].replay = r
		}
	}
	return nil
}

func verifyCleanupFlow(f *Func, boundaries *cleanupBoundaryFlow) error {
	fail := func(message string) error { return fmt.Errorf("semir %s cleanup: %s", f.graph.Name, message) }
	// An indexed arena avoids a heap allocation for each persistent node. Zero
	// denotes the empty stack; only missing canonical nodes extend the arena.
	nodes := make([]cleanupStack, 1, 2*len(f.cleanups)+1)
	stacks := make(map[cleanupStack]int, len(f.cleanups))
	push := func(previous int, action *cleanupRegion) int {
		key := cleanupStack{previous: previous, action: action}
		if node := stacks[key]; node != 0 {
			return node
		}
		node := len(nodes)
		nodes = append(nodes, key)
		stacks[key] = node
		return node
	}
	states := map[*ssa.Block]cleanupFlowState{f.graph.Entry: {}}
	frames := make([]cleanupScopeState, 1, len(f.boundaries)+1)
	scopes := make(map[cleanupScopeState]int, len(f.boundaries))
	queue := []*ssa.Block{f.graph.Entry}
	for next := 0; next < len(queue); next++ {
		block := queue[next]
		state := states[block]
		point := boundaries.points[block]
		if scope := point.entry; scope != nil {
			if frames[state.scope].boundary != scope.parent {
				return fail("boundary entry has the wrong active parent")
			}
			key := cleanupScopeState{state.scope, scope, state.pending, state.registered}
			id := scopes[key]
			if id == 0 {
				id = len(frames)
				frames = append(frames, key)
				scopes[key] = id
			}
			state.scope = id
		}
		if f.unpromotedBindings {
			for _, op := range block.Ops {
				if op.Kind == ssa.OpBindingInit && f.bindings[op.Imm-1].boundary != frames[state.scope].boundary {
					pos := f.effectPositions[op]
					return fmt.Errorf("semir %s binding %d at %d:%d: initializer belongs to a different active boundary", f.graph.Name, op.Imm, pos.Line, pos.Col)
				}
			}
		}
		if len(point.registers) != 0 && point.replay != nil {
			return fail("registration and replay require distinct program points")
		}
		for _, r := range point.registers {
			if frames[state.scope].boundary != r.boundary {
				return fail("registration belongs to a different active boundary")
			}
			state.pending = push(state.pending, r)
			state.registered = push(state.registered, r)
		}
		if r := point.replay; r != nil {
			if state.pending == 0 || nodes[state.pending].action != r || frames[state.scope].boundary != r.boundary {
				return fail("replay does not consume the most recent pending registration")
			}
			state.pending = nodes[state.pending].previous
		}
		if e := point.start; e != nil && frames[state.scope].boundary != e.from {
			return fail("cleanup exit starts in a different active boundary")
		}
		for _, scope := range point.ends {
			frame := frames[state.scope]
			if frame.boundary != scope || state.pending != frame.pending {
				return fail("boundary closes with pending actions or in the wrong order")
			}
			state.registered, state.scope = frame.registered, frame.previous
		}
		if e := point.finish; e != nil && frames[state.scope].boundary != e.through.parent {
			return fail("cleanup exit finishes in a different parent boundary")
		}
		if block.Term.Kind == ssa.TermRet && (state.pending != 0 || state.scope != 0 || state.registered != 0) {
			return fail("return leaves a registered action pending")
		}
		for _, succ := range block.Succs() {
			if incoming, known := states[succ]; known {
				if incoming != state {
					return fail("registration state disagrees across a join or cycle")
				}
			} else {
				states[succ] = state
				queue = append(queue, succ)
			}
		}
	}
	for block, point := range boundaries.points {
		if _, reachable := states[block]; !reachable {
			switch {
			case len(point.registers) != 0:
				return fail("unreachable registration")
			case point.replay != nil:
				return fail("unreachable replay")
			case point.entry != nil:
				return fail("unreachable boundary entry")
			case point.start != nil:
				return fail("unreachable cleanup exit")
			}
		}
	}
	return nil
}
