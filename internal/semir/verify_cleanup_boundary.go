package semir

import (
	"fmt"

	"github.com/jakechampion/lang/internal/ssa"
)

type cleanupBoundaryFlow struct {
	known  map[*cleanupBoundary]bool
	points map[*ssa.Block]*cleanupFlowPoint
}

type cleanupFlowPoint struct {
	entry                   *cleanupBoundary
	ends                    []*cleanupBoundary
	start, finish, endOwner *cleanupExit
	registers               []*cleanupRegion
	replay                  *cleanupRegion
}

func verifyCleanupBoundaries(f *Func, dom *ssa.DomTree) (*cleanupBoundaryFlow, error) {
	fail := func(message string) (*cleanupBoundaryFlow, error) {
		return nil, fmt.Errorf("semir %s cleanup: %s", f.graph.Name, message)
	}
	flow := &cleanupBoundaryFlow{known: make(map[*cleanupBoundary]bool), points: make(map[*ssa.Block]*cleanupFlowPoint, len(f.graph.Blocks))}
	// One arena and one block index share event storage across structural and
	// dataflow verification, instead of allocating an index for every event kind.
	points := make([]cleanupFlowPoint, len(f.graph.Blocks))
	for i, block := range f.graph.Blocks {
		flow.points[block] = &points[i]
	}
	if len(f.boundaries) == 0 {
		return fail("missing function boundary")
	}
	for i, scope := range f.boundaries {
		if scope == nil || flow.known[scope] || scope.owner != f || flow.points[scope.entry] == nil || flow.points[scope.entry].entry != nil {
			return fail("invalid boundary identity, owner or entry")
		}
		if i == 0 {
			if scope.parent != nil || scope.entry != f.graph.Entry || scope.header != nil || scope.exit != nil {
				return fail("invalid function boundary")
			}
		} else {
			if !flow.known[scope.parent] || flow.points[scope.header] == nil || scope.header == f.graph.Entry ||
				(scope.exit != nil && flow.points[scope.exit] == nil) || !dom.Dominates(scope.header, scope.entry) {
				return fail("invalid iteration boundary or parent")
			}
		}
		flow.known[scope] = true
		flow.points[scope.entry].entry = scope
	}
	for _, e := range f.cleanupExits {
		if e == nil || !flow.known[e.from] || !flow.known[e.through] || flow.points[e.start] == nil || flow.points[e.finish] == nil || flow.points[e.start].start != nil || flow.points[e.finish].finish != nil {
			return fail("invalid cleanup exit identity")
		}
		if e.kind == cleanupReturn {
			if e.through.parent != nil || e.target != nil || e.finish.Term.Kind != ssa.TermRet {
				return fail("function cleanup must end at a return")
			}
		} else {
			if e.through.parent == nil || flow.points[e.target] == nil || e.finish.Term.Kind != ssa.TermBr || e.finish.Term.Target != e.target {
				return fail("iteration cleanup must end at its declared branch")
			}
			switch e.kind {
			case cleanupTail:
				if e.from != e.through || e.target != e.through.header {
					return fail("invalid iteration tail")
				}
			case cleanupContinue:
				if e.target != e.through.header {
					return fail("continue targets a different iteration")
				}
			case cleanupBreak:
				if e.target != e.through.exit {
					return fail("break targets a different iteration")
				}
			default:
				return fail("invalid cleanup exit kind")
			}
		}
		scope, previous := e.from, e.start
		for i, end := range e.ends {
			point := flow.points[end.block]
			if scope == nil || end.boundary != scope || point == nil || !dom.Dominates(previous, end.block) || !dom.Dominates(end.block, e.finish) ||
				(point.endOwner != nil && point.endOwner != e) {
				return fail("invalid crossed boundary sequence or cleanup end")
			}
			if scope == e.through && i != len(e.ends)-1 {
				return fail("cleanup crosses beyond its declared boundary")
			}
			point.endOwner = e
			point.ends = append(point.ends, scope)
			previous, scope = end.block, scope.parent
		}
		if len(e.ends) == 0 || e.ends[len(e.ends)-1].boundary != e.through || previous != e.finish {
			return fail("cleanup omits a crossed boundary")
		}
		flow.points[e.start].start, flow.points[e.finish].finish = e, e
	}
	for _, block := range f.graph.Blocks {
		if block.Term.Kind == ssa.TermRet && flow.points[block].finish == nil {
			return fail("return lacks a function cleanup exit")
		}
	}
	return flow, nil
}
