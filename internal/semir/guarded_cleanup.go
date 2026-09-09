package semir

import (
	"fmt"

	"github.com/jakechampion/lang/internal/ast"
	"github.com/jakechampion/lang/internal/ssa"
)

// These are executable semantic identities, not a pre-expansion certificate.
// The verifier relates actual branches and snapshots to registration history.
type guardedCleanup struct {
	activation BindingID
	dispatches []cleanupDispatch
}

type cleanupDispatch struct {
	site, entry, exit, continuation *ssa.Block
	guards                          []cleanupGuard
}

type cleanupGuard struct {
	id          BindingID
	block, next *ssa.Block
	state       ssa.Value
}

func prepareCleanupActivation(f *Func) {
	f.guardedCleanups = true
	for i, r := range f.cleanups {
		id := f.addBinding(fmt.Sprintf("cleanup.activation.%d", i), ast.BoolType{}, r.pos)
		f.bindings[id-1].boundary = r.boundary
		flag := f.addOp(r.register, ssa.OpConstBool, ast.BoolType{}, r.pos)
		r.register.Ops[len(r.register.Ops)-1].Imm = 1
		f.writeBinding(r.register, ssa.OpBindingInit, id, flag, r.pos)
		r.guarded = &guardedCleanup{activation: id, dispatches: make([]cleanupDispatch, 0, len(r.replays))}
	}
}

func expandGuardedCleanupSite(f *Func, r *cleanupRegion, site *ssa.Block) *ssa.Block {
	term, successors := site.Term, site.Succs()
	continuation := f.graph.NewBlock()
	continuation.Term = term
	relocateCleanupContinuation(site, continuation, successors)
	d := cleanupDispatch{site: site, continuation: continuation, guards: make([]cleanupGuard, 0, len(r.captures)+1)}
	current := site
	guard := func(id BindingID) ssa.Value {
		state := f.snapshotBinding(current, id, r.pos)
		has := f.addOp(current, ssa.OpStateHas, ast.BoolType{}, r.pos, state)
		next := f.graph.NewBlock()
		f.graph.SetBrIf(current, has, next, continuation)
		d.guards = append(d.guards, cleanupGuard{id, current, next, state})
		current = next
		return state
	}
	guard(r.guarded.activation)
	values := make(map[int32]ssa.Value, len(r.body.values))
	for i, id := range r.captures {
		state := guard(id)
		values[r.body.graph.Params[i].ID] = f.addOp(current, ssa.OpStateGet, f.bindings[id-1].typ, r.pos, state)
	}
	d.entry = current
	d.exit = cloneCleanupBody(f, r, current, values)
	for i, id := range r.captures {
		f.replaceBindingGuarded(d.exit, id, d.guards[i+1].state, values[r.yield.Args[i].ID], r.pos)
	}
	f.graph.SetBr(d.exit, continuation)
	r.guarded.dispatches = append(r.guarded.dispatches, d)
	return continuation
}

func (f *Func) rewriteBindingAliases(aliases ssa.ValueAliases) {
	f.bindingStates.rewrite(aliases)
	if len(aliases) == 0 || !f.guardedCleanups {
		return
	}
	for _, r := range f.cleanups {
		if r.guarded == nil {
			continue
		}
		for i := range r.guarded.dispatches {
			for j := range r.guarded.dispatches[i].guards {
				guard := &r.guarded.dispatches[i].guards[j]
				guard.state = aliases.Resolve(guard.state)
			}
		}
	}
}
