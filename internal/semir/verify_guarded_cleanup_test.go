package semir

import (
	"slices"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/ast"
	"github.com/jakechampion/lang/internal/ssa"
)

func expandedGuardedFixture(t *testing.T, promoted bool) *Func {
	t.Helper()
	f := pendingCleanupSource(t, `function pilot(flag: boolean): void {
  if (flag) { var items: i32[] = [1]; defer items = [2]; }
}`)
	if err := expandCleanups(f); err != nil {
		t.Fatal(err)
	}
	if promoted {
		if err := promoteBindings(f); err != nil {
			t.Fatal(err)
		}
		if err := finishFlow(f); err != nil {
			t.Fatal(err)
		}
	}
	if err := Verify(f); err != nil {
		t.Fatal(err)
	}
	return f
}

func requireGuardedCleanupRejection(t *testing.T, f *Func, want string) {
	t.Helper()
	if err := ssa.Verify(f.graph); err != nil {
		t.Fatalf("mutation invalidated ordinary SSA: %v", err)
	}
	if err := Verify(f); err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("got %v, want %q", err, want)
	}
}

func TestGuardedCleanupRejectsMalformedDispatch(t *testing.T) {
	for _, promoted := range []bool{false, true} {
		for _, tc := range []struct {
			name, want string
			edit       func(*Func, *cleanupRegion)
		}{
			{"missing-record", "guarded expansion phase", func(f *Func, r *cleanupRegion) { r.guarded = nil }},
			{"wrong-mode", "guarded expansion phase", func(f *Func, r *cleanupRegion) { f.guardedCleanups = false }},
			{"invalid-activation", "invalid activation", func(f *Func, r *cleanupRegion) { r.guarded.activation = 0 }},
			{"capture-as-activation", "invalid activation", func(f *Func, r *cleanupRegion) { r.guarded.activation = r.captures[0] }},
			{"wrong-registration", "unique registration", func(f *Func, r *cleanupRegion) { r.register = f.graph.Entry }},
			{"missing-dispatch", "every dispatch", func(f *Func, r *cleanupRegion) { r.guarded.dispatches = nil }},
			{"skipped-activation-guard", "execute region", func(f *Func, r *cleanupRegion) { r.guarded.dispatches[0].guards = r.guarded.dispatches[0].guards[1:] }},
			{"missing-capture-guard", "execute region", func(f *Func, r *cleanupRegion) { r.guarded.dispatches[0].guards = r.guarded.dispatches[0].guards[:1] }},
			{"wrong-guard-binding", "captured identity", func(f *Func, r *cleanupRegion) { r.guarded.dispatches[0].guards[1].id = r.guarded.activation }},
			{"wrong-guard-state", "exact snapshot", func(f *Func, r *cleanupRegion) {
				r.guarded.dispatches[0].guards[0].state = r.guarded.dispatches[0].guards[1].state
			}},
			{"wrong-true-edge", "guard chain", func(f *Func, r *cleanupRegion) {
				r.guarded.dispatches[0].guards[0].next = r.guarded.dispatches[0].continuation
			}},
			{"wrong-body-entry", "execute region", func(f *Func, r *cleanupRegion) { r.guarded.dispatches[0].entry = r.guarded.dispatches[0].site }},
			{"guard-side-effect", "before its guards", func(f *Func, r *cleanupRegion) {
				f.addOp(r.guarded.dispatches[0].site, ssa.OpArrayMake, ast.ArrayType{Elem: ast.NumberType{}}, r.pos)
			}},
		} {
			name := tc.name + map[bool]string{false: "/places", true: "/promoted"}[promoted]
			t.Run(name, func(t *testing.T) {
				f := expandedGuardedFixture(t, promoted)
				tc.edit(f, f.cleanups[0])
				requireGuardedCleanupRejection(t, f, tc.want)
			})
		}
	}
}

func TestGuardedCleanupRequiresInitializationHistory(t *testing.T) {
	for _, promoted := range []bool{false, true} {
		for _, activation := range []bool{false, true} {
			f := expandedGuardedFixture(t, promoted)
			r := f.cleanups[0]
			id, want := r.captures[0], "absent on a registered replay path"
			if activation {
				id, want = r.guarded.activation, "no activation initializer"
			}
			if promoted {
				f.bindingStates.writes = slices.DeleteFunc(f.bindingStates.writes, func(w bindingStateWrite) bool {
					return w.id == id && w.initializer
				})
			} else {
				for _, block := range f.graph.Blocks {
					block.Ops = slices.DeleteFunc(block.Ops, func(op *ssa.Op) bool {
						if op.Kind == ssa.OpBindingInit && BindingID(op.Imm) == id {
							delete(f.effectPositions, op)
							return true
						}
						return false
					})
				}
			}
			requireGuardedCleanupRejection(t, f, want)
		}
	}
}

func TestGuardedCleanupRejectsPromotedHistoryCorruption(t *testing.T) {
	for _, tc := range []struct {
		name, want string
		edit       func(*Func, *cleanupRegion)
	}{
		{"activation-replacement", "unique registration", func(f *Func, r *cleanupRegion) {
			for i := range f.bindingStates.writes {
				if f.bindingStates.writes[i].id == r.guarded.activation {
					f.bindingStates.writes[i].initializer = false
				}
			}
		}},
		{"duplicate-activation", "unique registration", func(f *Func, r *cleanupRegion) {
			for _, write := range f.bindingStates.writes {
				if write.id == r.guarded.activation {
					f.bindingStates.writes = append(f.bindingStates.writes, write)
					break
				}
			}
		}},
		{"capture-replacement-is-not-initialization", "absent on a registered", func(f *Func, r *cleanupRegion) {
			for i := range f.bindingStates.writes {
				if f.bindingStates.writes[i].id == r.captures[0] {
					f.bindingStates.writes[i].initializer = false
				}
			}
		}},
		{"missing-observation", "matching promoted observation", func(f *Func, r *cleanupRegion) {
			f.bindingStates.observations = nil
		}},
		{"wrong-observation-block", "matching promoted observation", func(f *Func, r *cleanupRegion) {
			f.bindingStates.observations[0].block = r.register
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := expandedGuardedFixture(t, true)
			tc.edit(f, f.cleanups[0])
			requireGuardedCleanupRejection(t, f, tc.want)
		})
	}
}

func TestGuardedCleanupRejectsReversedActivationBranch(t *testing.T) {
	for _, promoted := range []bool{false, true} {
		f := pendingCleanupSource(t, `function pilot(flag: boolean): void { if (flag) { defer {} } }`)
		if err := expandCleanups(f); err != nil {
			t.Fatal(err)
		}
		if promoted {
			if err := promoteBindings(f); err != nil {
				t.Fatal(err)
			}
		}
		term := &f.cleanups[0].replays[0].Term
		term.True, term.False = term.False, term.True
		// With no captured StateGet, ordinary SSA/presence verification cannot
		// detect an action that runs exactly when it was not registered.
		requireGuardedCleanupRejection(t, f, "exact snapshot presence")
	}
}

func TestGuardedCleanupRejectsEarlyCaptureSnapshot(t *testing.T) {
	f := expandedGuardedFixture(t, false)
	guard := f.cleanups[0].guarded.dispatches[0].guards[1]
	for i, op := range guard.block.Ops {
		if op.Result == guard.state {
			guard.block.Ops = slices.Delete(guard.block.Ops, i, i+1)
			f.graph.Entry.Ops = append(f.graph.Entry.Ops, op)
			break
		}
	}
	// The old snapshot still dominates its uses and has a genuine Has guard,
	// but it observes absence before registration instead of the late capture.
	requireGuardedCleanupRejection(t, f, "matching direct binding snapshot")
}

func TestGuardedCleanupRejectsWrongPromotedInitializerScope(t *testing.T) {
	f := pendingCleanupSource(t, `function pilot(flag: boolean): void {
  loop { if (flag) { var items: i32[] = [1]; defer items = [2]; } break; }
}`)
	for _, phase := range []func(*Func) error{expandCleanups, promoteBindings, finishFlow} {
		if err := phase(f); err != nil {
			t.Fatal(err)
		}
	}
	id := f.cleanups[0].captures[0]
	for i := range f.bindingStates.writes {
		if f.bindingStates.writes[i].id == id && f.bindingStates.writes[i].initializer {
			f.bindingStates.writes[i].block = f.graph.Entry
		}
	}
	requireGuardedCleanupRejection(t, f, "initializer belongs to a different active boundary")
}

func TestGuardedCleanupCannotSilentlySkipRegisteredAction(t *testing.T) {
	f := expandedGuardedFixture(t, true)
	changed := false
	for _, block := range f.graph.Blocks {
		for _, op := range block.Ops {
			if op.Kind == ssa.OpStatePresent && ast.Equal(f.values[op.Result.ID].typ.source, ast.BoolType{}) {
				op.Kind, op.Args = ssa.OpStateAbsent, nil
				changed = true
			}
		}
	}
	if !changed {
		t.Fatal("fixture did not materialize activation")
	}
	// All guard identities and registration events remain unchanged. Only the
	// independent post-promotion equation proof exposes this silent skip.
	requireGuardedCleanupRejection(t, f, "promoted snapshot")
}
