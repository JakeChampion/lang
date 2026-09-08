package semir

import (
	"github.com/jakechampion/lang/internal/ast"
	"github.com/jakechampion/lang/internal/ssa"
)

// Boundaries are semantic scopes, not lexical name scopes: a value block or
// match arm does not start a new cleanup lifetime. Header/exit identify an
// iteration's real CFG targets; the root has neither and exits by return only.
type cleanupBoundary struct {
	owner               *Func
	parent              *cleanupBoundary
	entry, header, exit *ssa.Block
	pos                 ast.Position
	actions             []*cleanupRegion
}

type cleanupExitKind uint8

const (
	cleanupReturn cleanupExitKind = iota
	cleanupTail
	cleanupBreak
	cleanupContinue
)

type cleanupEnd struct {
	boundary *cleanupBoundary
	block    *ssa.Block
}

// An exit declares which lexical boundary chain is crossed, independently of
// the expanded action CFG. Ends occur after each boundary's actions finish.
type cleanupExit struct {
	from, through         *cleanupBoundary
	start, finish, target *ssa.Block
	kind                  cleanupExitKind
	ends                  []cleanupEnd
}

func (b *builder) newCleanupBoundary(parent *cleanupBoundary, entry, header, exit *ssa.Block, pos ast.Position) *cleanupBoundary {
	scope := &cleanupBoundary{owner: b.fn, parent: parent, entry: entry, header: header, exit: exit, pos: pos}
	b.fn.boundaries = append(b.fn.boundaries, scope)
	return scope
}

func (b *builder) emitCleanupExit(through *cleanupBoundary, kind cleanupExitKind, target *ssa.Block) error {
	e := &cleanupExit{from: b.cleanupScope, through: through, start: b.current, target: target, kind: kind}
	for scope := b.cleanupScope; ; scope = scope.parent {
		for i := len(scope.actions) - 1; i >= 0; i-- {
			r := scope.actions[i]
			// Record an invocation in the source CFG. The standalone typed pass
			// expands it only after all ordinary control flow is complete.
			site := b.fn.graph.NewBlock()
			b.fn.graph.SetBr(b.current, site)
			r.replays = append(r.replays, site)
			b.current = site
		}
		e.ends = append(e.ends, cleanupEnd{scope, b.current})
		if scope == through {
			break
		}
	}
	e.finish = b.current
	b.fn.cleanupExits = append(b.fn.cleanupExits, e)
	return nil
}
