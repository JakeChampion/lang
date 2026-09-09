package semir

import (
	"fmt"

	"github.com/jakechampion/lang/internal/ast"
	"github.com/jakechampion/lang/internal/ssa"
)

type cleanupObservationKey struct {
	block *ssa.Block
	id    BindingID
	state ssa.Value
}

// Bind actual dispatch branches to registration and snapshot semantics. The
// finite history/capture proof and the snapshot equation verifier are both
// required in addition to this structural contract.
func verifyGuardedCleanups(f *Func, flow *cleanupBoundaryFlow, dom *ssa.DomTree) error {
	fail := func(r *cleanupRegion, message string) error {
		return fmt.Errorf("semir %s cleanup at %d:%d: guarded replay %s", f.graph.Name, r.pos.Line, r.pos.Col, message)
	}
	type definition struct {
		op    *ssa.Op
		block *ssa.Block
	}
	defs := make(map[int32]definition, len(f.values))
	for _, block := range f.graph.Blocks {
		for _, op := range block.Ops {
			if op.Result.IsValid() {
				defs[op.Result.ID] = definition{op, block}
			}
		}
	}
	observations := make(map[cleanupObservationKey]bool)
	if f.bindingStates != nil {
		for _, o := range f.bindingStates.observations {
			observations[cleanupObservationKey{o.block, o.id, o.state}] = true
		}
	}
	activations := make(map[BindingID]*cleanupRegion)
	for _, r := range f.cleanups {
		if r.guarded == nil {
			continue
		}
		id := r.guarded.activation
		if f.unexpandedCleanups || id == 0 || int(id) > len(f.bindings) || activations[id] != nil ||
			!ast.Equal(f.bindings[id-1].typ, ast.BoolType{}) || f.bindings[id-1].boundary != r.boundary {
			return fail(r, "has an invalid activation binding or phase")
		}
		activations[id] = r
		if len(r.guarded.dispatches) != len(r.replays) {
			return fail(r, "does not describe every dispatch")
		}
		for i, d := range r.guarded.dispatches {
			if d.site != r.replays[i] || flow.points[d.entry] == nil || flow.points[d.exit] == nil || flow.points[d.continuation] == nil ||
				d.site == d.entry || d.site == d.continuation || d.entry == d.continuation ||
				len(d.guards) != len(r.captures)+1 || !dom.Dominates(d.entry, d.exit) ||
				d.exit.Term.Kind != ssa.TermBr || d.exit.Term.Target != d.continuation {
				return fail(r, "has an invalid execute region or continuation")
			}
			current := d.site
			for j, guard := range d.guards {
				wantID := id
				if j != 0 {
					wantID = r.captures[j-1]
				}
				if guard.id != wantID || guard.block != current || flow.points[guard.next] == nil ||
					len(guard.next.Preds) != 1 || guard.next.Preds[0] != current {
					return fail(r, "has an invalid guard chain or captured identity")
				}
				term := current.Term
				test := defs[term.Cond.ID].op
				if term.Kind != ssa.TermBrIf || term.True != guard.next || term.False != d.continuation ||
					test == nil || test.Kind != ssa.OpStateHas || len(test.Args) != 1 || test.Args[0] != guard.state {
					return fail(r, "does not branch on its exact snapshot presence")
				}
				if f.unpromotedBindings {
					snapshot := defs[guard.state.ID]
					if snapshot.op == nil || snapshot.op.Kind != ssa.OpBindingSnapshot || BindingID(snapshot.op.Imm) != wantID || snapshot.block != current {
						return fail(r, "has no matching direct binding snapshot")
					}
				} else if !observations[cleanupObservationKey{current, wantID, guard.state}] {
					return fail(r, "has no matching promoted observation")
				}
				for _, op := range current.Ops {
					switch op.Kind {
					case ssa.OpBindingSnapshot, ssa.OpStateHas, ssa.OpStateGet, ssa.OpPhi, ssa.OpStateAbsent, ssa.OpStatePresent:
					default:
						return fail(r, "executes work before its guards succeed")
					}
				}
				current = guard.next
			}
			if current != d.entry {
				return fail(r, "guard chain bypasses its execute entry")
			}
		}
	}
	initialized := make(map[BindingID]bool, len(activations))
	if err := walkBindingWrites(f, func(write bindingStateWrite) error {
		r := activations[write.id]
		if r == nil {
			return nil
		}
		if !write.initializer || write.block != r.register || initialized[write.id] {
			return fail(r, "activation differs from its unique registration")
		}
		initialized[write.id] = true
		return nil
	}); err != nil {
		return err
	}
	for id, r := range activations {
		if !initialized[id] {
			return fail(r, "has no activation initializer")
		}
	}
	return nil
}

func walkBindingWrites(f *Func, visit func(bindingStateWrite) error) error {
	if !f.unpromotedBindings {
		if f.bindingStates != nil {
			for _, write := range f.bindingStates.writes {
				if err := visit(write); err != nil {
					return err
				}
			}
		}
		return nil
	}
	for _, block := range f.graph.Blocks {
		for _, op := range block.Ops {
			switch op.Kind {
			case ssa.OpBindingInit, ssa.OpBindingReplace, ssa.OpBindingReplaceGuarded:
				if err := visit(bindingStateWrite{id: BindingID(op.Imm), initializer: op.Kind == ssa.OpBindingInit,
					block: block, value: bindingWriteValue(op), pos: f.effectPositions[op]}); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func cleanupInitializerEvents(f *Func) (map[*ssa.Block][]bindingStateWrite, error) {
	events := make(map[*ssa.Block][]bindingStateWrite)
	seen := make(map[BindingID]bool)
	err := walkBindingWrites(f, func(write bindingStateWrite) error {
		if !write.initializer {
			return nil
		}
		if write.id == 0 || int(write.id) > len(f.bindings) || seen[write.id] {
			return fmt.Errorf("semir %s: invalid or duplicate retained initializer identity", f.graph.Name)
		}
		seen[write.id] = true
		events[write.block] = append(events[write.block], write)
		return nil
	})
	return events, err
}
