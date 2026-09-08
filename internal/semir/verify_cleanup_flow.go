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
	// registration needs explicit boundary/reset events before it is admitted.
	registered int
}

func verifyCleanupFlow(f *Func) error {
	fail := func(message string) error { return fmt.Errorf("semir %s cleanup: %s", f.graph.Name, message) }
	registers := make(map[*ssa.Block][]*cleanupRegion)
	replays := make(map[*ssa.Block]*cleanupRegion)
	for _, r := range f.cleanups {
		registers[r.register] = append(registers[r.register], r)
		for _, block := range r.replays {
			if replays[block] != nil {
				return fail("multiple actions share a replay entry")
			}
			replays[block] = r
		}
	}
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
	queue := []*ssa.Block{f.graph.Entry}
	for next := 0; next < len(queue); next++ {
		block := queue[next]
		state := states[block]
		if len(registers[block]) != 0 && replays[block] != nil {
			return fail("registration and replay require distinct program points")
		}
		for _, r := range registers[block] {
			state.pending = push(state.pending, r)
			state.registered = push(state.registered, r)
		}
		if r := replays[block]; r != nil {
			if state.pending == 0 || nodes[state.pending].action != r {
				return fail("replay does not consume the most recent pending registration")
			}
			state.pending = nodes[state.pending].previous
		}
		if block.Term.Kind == ssa.TermRet && state.pending != 0 {
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
	for block := range registers {
		if _, reachable := states[block]; !reachable {
			return fail("unreachable registration")
		}
	}
	for block := range replays {
		if _, reachable := states[block]; !reachable {
			return fail("unreachable replay")
		}
	}
	return nil
}
