package semir

import (
	"fmt"

	"github.com/jakechampion/lang/internal/ssa"
)

// Conditional admission projects traces onto one action, two actions, or an
// action and one captured place. Each projection has a fixed finite alphabet
// and state space. A bad trace has a witness in one of these projections:
// repeated/missing replay, an outstanding newer action, or an absent capture.
// Keeping reachable product states (not independent may facts) preserves these
// correlations through joins and arbitrarily many loop iterations.
func verifyConditionalCleanupFlow(f *Func, flow *cleanupBoundaryFlow) error {
	events, err := cleanupInitializerEvents(f)
	if err != nil {
		return err
	}
	if err := verifyConditionalCleanupScopes(f, flow, events); err != nil {
		return err
	}
	return verifyCleanupProjectionsWithEvents(f, flow, events)
}

func verifyCleanupProjections(f *Func, flow *cleanupBoundaryFlow) error {
	events, err := cleanupInitializerEvents(f)
	if err != nil {
		return err
	}
	return verifyCleanupProjectionsWithEvents(f, flow, events)
}

func verifyCleanupProjectionsWithEvents(f *Func, flow *cleanupBoundaryFlow, events map[*ssa.Block][]bindingStateWrite) error {
	walk := newCleanupProjection(f, flow)
	walk.initializers = events
	for i, action := range f.cleanups {
		partners := f.cleanups[i+1:]
		if len(f.cleanups) == 1 {
			partners = []*cleanupRegion{nil}
		}
		// A pair checks both individual histories too. Do not repeat separate
		// single-action walks when every action already participates in a pair.
		for _, partner := range partners {
			if err := walk.actions(action, partner); err != nil {
				return err
			}
		}
		for _, id := range action.captures {
			if err := walk.capture(action, id); err != nil {
				return err
			}
		}
	}
	return nil
}

// Scope identity is not conditional: every CFG join must have the same active
// boundary. Pending actions differ by path, but may reset only at verified ends.
func verifyConditionalCleanupScopes(f *Func, flow *cleanupBoundaryFlow, events map[*ssa.Block][]bindingStateWrite) error {
	fail := func(message string) error { return fmt.Errorf("semir %s cleanup: %s", f.graph.Name, message) }
	states := map[*ssa.Block]*cleanupBoundary{f.graph.Entry: nil}
	queue := []*ssa.Block{f.graph.Entry}
	for next := 0; next < len(queue); next++ {
		block := queue[next]
		scope := states[block]
		point := flow.points[block]
		if entry := point.entry; entry != nil {
			if scope != entry.parent {
				return fail("boundary entry has the wrong active parent")
			}
			scope = entry
		}
		for _, write := range events[block] {
			if f.bindings[write.id-1].boundary != scope {
				return fmt.Errorf("semir %s binding %d at %d:%d: initializer belongs to a different active boundary", f.graph.Name, write.id, write.pos.Line, write.pos.Col)
			}
		}
		if len(point.registers) != 0 && point.replay != nil {
			return fail("registration and replay require distinct program points")
		}
		for _, action := range point.registers {
			if action.boundary != scope {
				return fail("registration belongs to a different active boundary")
			}
		}
		if point.replay != nil && point.replay.boundary != scope {
			return fail("replay belongs to a different active boundary")
		}
		if point.start != nil && point.start.from != scope {
			return fail("cleanup exit starts in a different active boundary")
		}
		for _, end := range point.ends {
			if end != scope {
				return fail("boundary closes in the wrong order")
			}
			scope = scope.parent
		}
		if point.finish != nil && scope != point.finish.through.parent {
			return fail("cleanup exit finishes in a different parent boundary")
		}
		if block.Term.Kind == ssa.TermRet && scope != nil {
			return fail("return leaves an active boundary")
		}
		for _, succ := range block.Succs() {
			if old, ok := states[succ]; ok {
				if old != scope {
					return fail("active boundary disagrees across a join or cycle")
				}
			} else {
				states[succ] = scope
				queue = append(queue, succ)
			}
		}
	}
	for block, point := range flow.points {
		if _, ok := states[block]; !ok && (point.entry != nil || point.start != nil || point.replay != nil || len(point.registers) != 0) {
			return fail("unreachable cleanup event")
		}
	}
	return nil
}

type cleanupProjection struct {
	initializers map[*ssa.Block][]bindingStateWrite
	f            *Func
	points       []*cleanupFlowPoint
	succs        [][]int
	entry        int
	seen         []uint32
	queue        []int
}

func newCleanupProjection(f *Func, flow *cleanupBoundaryFlow) *cleanupProjection {
	w := &cleanupProjection{f: f, points: make([]*cleanupFlowPoint, len(f.graph.Blocks)), succs: make([][]int, len(f.graph.Blocks)), seen: make([]uint32, len(f.graph.Blocks))}
	indices := make(map[*ssa.Block]int, len(f.graph.Blocks))
	edgeCount := 0
	for i, block := range f.graph.Blocks {
		indices[block] = i
		w.points[i] = flow.points[block]
		edgeCount += len(block.Preds)
	}
	w.entry = indices[f.graph.Entry]
	edges := make([]int, 0, edgeCount)
	for i, block := range f.graph.Blocks {
		start := len(edges)
		for _, succ := range block.Succs() {
			edges = append(edges, indices[succ])
		}
		w.succs[i] = edges[start:len(edges)]
	}
	return w
}

// At most 18 states per block for a pair, six for a capture. Queue/storage are
// reused across projections. No whole action subsets or path-length cap exist.
func (w *cleanupProjection) run(step func(int, uint8) (uint8, error)) error {
	clear(w.seen)
	w.seen[w.entry] = 1
	w.queue = append(w.queue[:0], w.entry*32)
	for next := 0; next < len(w.queue); next++ {
		item := w.queue[next]
		block, state := item/32, uint8(item%32)
		out, err := step(block, state)
		if err != nil {
			return err
		}
		mask := uint32(1) << out
		for _, succ := range w.succs[block] {
			if w.seen[succ]&mask == 0 {
				w.seen[succ] |= mask
				w.queue = append(w.queue, succ*32+int(out))
			}
		}
	}
	return nil
}

const (
	cleanupNever uint8 = iota
	cleanupPending
	cleanupReplayed
)

func (w *cleanupProjection) fail(action *cleanupRegion, message string) error {
	return fmt.Errorf("semir %s cleanup at %d:%d: %s", w.f.graph.Name, action.pos.Line, action.pos.Col, message)
}

func (w *cleanupProjection) actions(a, b *cleanupRegion) error {
	return w.run(func(block int, state uint8) (uint8, error) {
		status := [2]uint8{state % 3, state / 3 % 3}
		latest := state / 9
		point := w.points[block]
		for _, registered := range point.registers {
			for i, action := range [2]*cleanupRegion{a, b} {
				if action == nil || registered != action {
					continue
				}
				if status[i] != cleanupNever {
					return 0, w.fail(action, "action registers again before its lifetime ends")
				}
				status[i], latest = cleanupPending, uint8(i)
			}
		}
		for i, action := range [2]*cleanupRegion{a, b} {
			if action == nil {
				continue
			}
			if point.replay == action {
				if status[i] == cleanupReplayed {
					return 0, w.fail(action, "action replays more than once in its lifetime")
				}
				if status[i] == cleanupPending {
					if status[1-i] == cleanupPending && latest != uint8(i) {
						return 0, w.fail(action, "replay does not consume the most recent pending registration")
					}
					status[i] = cleanupReplayed
				}
			}
			for _, end := range point.ends {
				if end == action.boundary {
					if status[i] == cleanupPending {
						return 0, w.fail(action, "boundary closes with a pending action")
					}
					status[i] = cleanupNever
				}
			}
			if w.f.graph.Blocks[block].Term.Kind == ssa.TermRet && status[i] != cleanupNever {
				return 0, w.fail(action, "return leaves action history outside its lifetime")
			}
		}
		if status[0] != cleanupPending || status[1] != cleanupPending {
			latest = 0
		}
		return status[0] + 3*status[1] + 9*latest, nil
	})
}

func (w *cleanupProjection) capture(action *cleanupRegion, id BindingID) error {
	return w.run(func(block int, state uint8) (uint8, error) {
		status, initialized := state%3, state/3 != 0
		point := w.points[block]
		for _, write := range w.initializers[w.f.graph.Blocks[block]] {
			if write.id == id {
				initialized = true
			}
		}
		for _, registered := range point.registers {
			if registered == action {
				status = cleanupPending // Single-action projection proved no repeats.
			}
		}
		if point.replay == action && status == cleanupPending {
			if !initialized {
				return 0, w.fail(action, fmt.Sprintf("captured binding %d is absent on a registered replay path", id))
			}
			status = cleanupReplayed
		}
		for _, end := range point.ends {
			if end == action.boundary {
				status = cleanupNever
			}
			if end == w.f.bindings[id-1].boundary {
				initialized = false
			}
		}
		if initialized {
			status += 3
		}
		return status, nil
	})
}
