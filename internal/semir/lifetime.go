package semir

import (
	"maps"
	"slices"

	"github.com/jakechampion/lang/internal/ssa"
)

// lifetimeFlow assumes reference phis receive an independent unit on each
// incoming edge. The unit planner must discharge that obligation. Projection
// borrows instead extend their actual container's lifetime through every use.
// This is not the machine register liveness of the unlowered graph.
type lifetimeFlow struct {
	dependencies map[int32][]ssa.Value
	live         *ssa.Liveness
	reachable    map[*ssa.Block]bool
	owned        map[int32]bool
	immortal     map[int32]bool
	// last includes units whose last lifetime use is this operation, plus
	// unused newly produced units. It avoids a live-set copy per instruction.
	last      map[*ssa.Op]map[int32]bool
	entryDead map[*ssa.Block][]ssa.Value
}

func analyzeLifetimes(f *Func, effects *functionEffects) *lifetimeFlow {
	l := &lifetimeFlow{
		dependencies: make(map[int32][]ssa.Value),
		reachable:    ssa.Reachable(f.graph),
		owned:        make(map[int32]bool),
		immortal:     make(map[int32]bool),
		last:         make(map[*ssa.Op]map[int32]bool),
		entryDead:    make(map[*ssa.Block][]ssa.Value),
	}
	parents := make(map[int32]ssa.Value)
	for i, v := range f.graph.Params {
		l.owned[v.ID] = f.modes[i] == ParamCounted
	}
	for op, effect := range effects.ops {
		id := op.Result.ID
		if !referenceBearing(f.values[id].typ) {
			continue
		}
		switch effect.result {
		case resultCounted, resultCall, resultJoin:
			// Calls and joins are obligations of the closed-module unit ABI,
			// not ownership conclusions drawn from semantic provenance.
			l.owned[id] = true
		case resultImmortal:
			l.immortal[id] = true
		case resultProjection:
			parents[id] = effect.parent
		}
	}
	// A verified projection's parent dominates it, so these chains cannot
	// cycle. Iteratively memoize chains without a recursion/depth limit.
	for id := range parents {
		var pending []int32
		at := id
		for parents[at].IsValid() && l.dependencies[at] == nil {
			pending = append(pending, at)
			at = parents[at].ID
		}
		for i := len(pending) - 1; i >= 0; i-- {
			child := pending[i]
			parent := parents[child]
			l.dependencies[child] = append([]ssa.Value{parent}, l.dependencies[parent.ID]...)
		}
	}
	l.live = ssa.ComputeLivenessWithDependencies(f.graph, l.dependencies)
	for _, b := range f.graph.Blocks {
		if !l.reachable[b] {
			continue
		}
		l.entryDead[b] = nil
		live := maps.Clone(l.live.LiveOut[b])
		l.addUse(live, b.Term.Value)
		l.addUse(live, b.Term.Cond)
		for i := len(b.Ops) - 1; i >= 0; i-- {
			op := b.Ops[i]
			if op.Kind == ssa.OpPhi {
				continue
			}
			last := make(map[int32]bool)
			if l.owned[op.Result.ID] && !live[op.Result.ID] {
				last[op.Result.ID] = true
			}
			delete(live, op.Result.ID)
			for _, arg := range op.Args {
				l.addLastUse(live, last, arg)
				for _, parent := range l.dependencies[arg.ID] {
					l.addLastUse(live, last, parent)
				}
			}
			l.last[op] = last
		}
		initial := make(map[int32]bool)
		if b == f.graph.Entry {
			for _, v := range f.graph.Params {
				initial[v.ID] = l.owned[v.ID]
			}
		}
		for _, op := range b.Ops {
			if op.Kind == ssa.OpPhi {
				initial[op.Result.ID] = l.owned[op.Result.ID]
			}
		}
		for id, owns := range initial {
			if owns && !live[id] {
				l.entryDead[b] = append(l.entryDead[b], ssa.Value{ID: id, Func: f.graph})
			}
		}
		sortValues(l.entryDead[b])
	}
	return l
}

func (l *lifetimeFlow) addLastUse(live, last map[int32]bool, v ssa.Value) {
	if l.owned[v.ID] && !live[v.ID] {
		last[v.ID] = true
	}
	live[v.ID] = true
}

func (l *lifetimeFlow) addUse(set map[int32]bool, v ssa.Value) {
	if !v.IsValid() {
		return
	}
	set[v.ID] = true
	for _, parent := range l.dependencies[v.ID] {
		set[parent.ID] = true
	}
}

func sortValues(values []ssa.Value) {
	slices.SortFunc(values, func(a, b ssa.Value) int {
		if a.ID < b.ID {
			return -1
		}
		if a.ID > b.ID {
			return 1
		}
		return 0
	})
}
