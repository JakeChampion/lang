package semir

import (
	"slices"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/ast"
	"github.com/jakechampion/lang/internal/ssa"
)

func requireBindingStateRejection(t *testing.T, f *Func, want string) {
	t.Helper()
	if err := ssa.Verify(f.graph); err != nil {
		t.Fatalf("mutation broke structural SSA instead of the snapshot contract: %v", err)
	}
	contract := f.bindingStates
	f.bindingStates = nil
	legacy := Verify(f)
	f.bindingStates = contract
	if legacy != nil {
		t.Fatalf("mutation must isolate the post-promotion contract: %v", legacy)
	}
	if err := Verify(f); err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("got %v, want %q", err, want)
	}
}

func TestBindingStateContractRejectsWrongPayload(t *testing.T) {
	a := bindingSnapshotDiamond(ast.NumberType{}, ParamValue)
	other := a.f.addParam(ast.NumberType{}, ParamValue, ast.Position{})
	if err := promoteBindings(a.f); err != nil {
		t.Fatal(err)
	}
	for _, op := range a.yes.Ops {
		if op.Kind == ssa.OpStatePresent {
			op.Args[0] = other
		}
	}
	requireBindingStateRejection(t, a.f, "6:2: promoted snapshot changes a reaching write payload")
}

func TestBindingStateContractRejectsMalformedRecords(t *testing.T) {
	for _, tc := range []struct {
		name string
		edit func(*bindingSnapshotFixture)
		want string
	}{
		{"unknown-write-binding", func(a *bindingSnapshotFixture) { a.f.bindingStates.writes[0].id++ }, "write identity"},
		{"missing-write-block", func(a *bindingSnapshotFixture) { a.f.bindingStates.writes[0].block = nil }, "write identity"},
		{"foreign-write-value", func(a *bindingSnapshotFixture) { a.f.bindingStates.writes[0].value.Func = nil }, "3:4: invalid promoted write payload identity"},
		{"missing-write-value", func(a *bindingSnapshotFixture) { a.f.bindingStates.writes[0].value.ID = 0 }, "payload identity"},
		{"wrong-write-type", func(a *bindingSnapshotFixture) { a.f.bindingStates.writes[0].value = a.f.graph.Params[0] }, "payload type"},
		{"unknown-observation-binding", func(a *bindingSnapshotFixture) { a.f.bindingStates.observations[0].id++ }, "observation identity or type"},
		{"missing-observation-block", func(a *bindingSnapshotFixture) { a.f.bindingStates.observations[0].block = nil }, "observation identity or type"},
		{"foreign-observation-value", func(a *bindingSnapshotFixture) { a.f.bindingStates.observations[0].state.Func = nil }, "observation identity or type"},
		{"missing-observation-value", func(a *bindingSnapshotFixture) { a.f.bindingStates.observations[0].state.ID = 0 }, "observation identity or type"},
		{"source-observation-value", func(a *bindingSnapshotFixture) { a.f.bindingStates.observations[0].state = a.payload }, "observation identity or type"},
		{"missing-local-write", func(a *bindingSnapshotFixture) { a.f.bindingStates.observations[0].local = 1 }, "invalid local write"},
		{"write-in-another-block", func(a *bindingSnapshotFixture) { a.f.bindingStates.observations[0].local = 0 }, "local write payload"},
		{"invalid-incoming-position", func(a *bindingSnapshotFixture) { a.f.bindingStates.observations[0].local = -2 }, "incoming-state position"},
		{"observation-before-definition", func(a *bindingSnapshotFixture) { a.f.bindingStates.observations[0].block = a.entry }, "unavailable at its observation block"},
		{"other-same-type-binding", func(a *bindingSnapshotFixture) {
			a.f.bindingStates.observations[0].id = a.f.addBinding("other", ast.NumberType{}, ast.Position{})
		}, "invents an entry payload"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := bindingSnapshotDiamond(ast.NumberType{}, ParamValue)
			if err := promoteBindings(a.f); err != nil {
				t.Fatal(err)
			}
			tc.edit(a)
			requireBindingStateRejection(t, a.f, tc.want)
		})
	}
}

func TestBindingStateContractRewritesOrdinaryReadAliases(t *testing.T) {
	f, entry, firstID, payload := bindingTestFunc()
	f.writeBinding(entry, ssa.OpBindingInit, firstID, payload, ast.Position{})
	read := f.readBinding(entry, firstID, ast.Position{})
	secondID := f.addBinding("copy", ast.NumberType{}, ast.Position{})
	f.writeBinding(entry, ssa.OpBindingInit, secondID, read, ast.Position{})
	state := f.snapshotBinding(entry, secondID, ast.Position{})
	f.result = ast.BoolType{}
	f.graph.SetRet(entry, f.addOp(entry, ssa.OpStateHas, f.result, ast.Position{}, state))
	if err := promoteBindings(f); err != nil {
		t.Fatal(err)
	}
	if len(f.bindingStates.writes) != 1 || f.bindingStates.writes[0].value != payload {
		t.Fatal("snapshot history did not follow the ordinary binding-read alias")
	}
	if err := finishFlow(f); err != nil {
		t.Fatal(err)
	}
}

func TestBindingStateContractRejectsReversedPredecessors(t *testing.T) {
	a := bindingSnapshotDiamond(ast.NumberType{}, ParamValue)
	if err := promoteBindings(a.f); err != nil {
		t.Fatal(err)
	}
	// Hoisting Present(parameter) is structurally legal. This ensures reversing
	// the state phi slots is not rejected merely by ordinary SSA dominance.
	for i, op := range a.yes.Ops {
		if op.Kind == ssa.OpStatePresent {
			a.entry.Ops = append(a.entry.Ops, op)
			a.yes.Ops = slices.Delete(a.yes.Ops, i, i+1)
			break
		}
	}
	slices.Reverse(a.join.Ops[0].Args)
	requireBindingStateRejection(t, a.f, "promoted snapshot")
}

func TestBindingStateContractRejectsOldSnapshotReplacedByNewPayload(t *testing.T) {
	f, entry, id, old := bindingTestFunc()
	newValue := f.addParam(ast.NumberType{}, ParamValue, ast.Position{})
	f.writeBinding(entry, ssa.OpBindingInit, id, old, ast.Position{})
	snapshot := f.snapshotBinding(entry, id, ast.Position{Line: 8, Col: 2})
	f.writeBinding(entry, ssa.OpBindingReplace, id, newValue, ast.Position{})
	f.addOp(entry, ssa.OpStateHas, ast.BoolType{}, ast.Position{}, snapshot)
	f.graph.SetRet(entry, old)
	if err := promoteBindings(f); err != nil {
		t.Fatal(err)
	}
	for _, op := range entry.Ops {
		if op.Kind == ssa.OpStatePresent {
			op.Args[0] = newValue
		}
	}
	requireBindingStateRejection(t, f, "8:2: promoted snapshot does not preserve its local write payload")
}

func TestBindingStateContractRejectsStaleEpochAvailability(t *testing.T) {
	f := guardedBindingEpochLoop(true)
	if err := promoteBindings(f); err != nil {
		t.Fatal(err)
	}
	for _, op := range f.graph.Entry.Ops {
		if op.Kind == ssa.OpStateAbsent {
			op.Kind, op.Args = ssa.OpStatePresent, []ssa.Value{f.graph.Params[0]}
		}
	}
	requireBindingStateRejection(t, f, "promoted snapshot retains a payload after its lifetime end")
}

func TestBindingStateContractTracksTrivialPhiAliases(t *testing.T) {
	f, entry, firstID, payload := bindingTestFunc()
	flag := f.addParam(ast.BoolType{}, ParamValue, ast.Position{})
	f.writeBinding(entry, ssa.OpBindingInit, firstID, payload, ast.Position{})
	yes, no, join := f.graph.NewBlock(), f.graph.NewBlock(), f.graph.NewBlock()
	f.graph.SetBrIf(entry, flag, yes, no)
	f.graph.SetBr(yes, join)
	f.graph.SetBr(no, join)
	phi := f.addPhi(join, ast.NumberType{}, ast.Position{}, payload, payload)
	first := f.snapshotBinding(join, firstID, ast.Position{})
	secondID := f.addBinding("second", ast.NumberType{}, ast.Position{})
	f.writeBinding(join, ssa.OpBindingInit, secondID, phi, ast.Position{})
	second := f.snapshotBinding(join, secondID, ast.Position{})
	one := f.addOp(join, ssa.OpStateHas, ast.BoolType{}, ast.Position{}, first)
	two := f.addOp(join, ssa.OpStateHas, ast.BoolType{}, ast.Position{}, second)
	f.result = ast.BoolType{}
	f.graph.SetRet(join, f.addOp(join, ssa.OpEq, f.result, ast.Position{}, one, two))
	if err := promoteBindings(f); err != nil {
		t.Fatal(err)
	}
	oldState := f.bindingStates.observations[0].state
	if err := finishFlow(f); err != nil {
		t.Fatal(err)
	}
	if f.bindingStates == nil || len(f.bindingStates.observations) != 2 {
		t.Fatal("live observations disappeared during canonicalization")
	}
	if f.bindingStates.observations[0].state == oldState {
		t.Fatal("state phi alias was not reflected in its observation")
	}
	for _, write := range f.bindingStates.writes {
		if write.value != payload {
			t.Fatal("payload phi alias was not reflected in the typed write")
		}
	}
	if _, err := LowerARM64SSA(singleProgram(f)); err != nil {
		t.Fatal(err)
	}
}

func TestBindingStateContractDoesNotKeepDeadObservationsAlive(t *testing.T) {
	f, entry, id, result := bindingTestFunc()
	flag := f.addParam(ast.BoolType{}, ParamValue, ast.Position{})
	yes, no, join := f.graph.NewBlock(), f.graph.NewBlock(), f.graph.NewBlock()
	f.graph.SetBrIf(entry, flag, yes, no)
	f.writeBinding(yes, ssa.OpBindingInit, id, result, ast.Position{})
	f.graph.SetBr(yes, join)
	f.graph.SetBr(no, join)
	// The observation becomes an unused nontrivial state phi. Generic DCE
	// removes phis, but deliberately preserves the semantic Present/Absent
	// operations until ownership lowering. Do not widen their purity here.
	f.snapshotBinding(join, id, ast.Position{})
	f.graph.SetRet(join, result)
	if err := promoteBindings(f); err != nil {
		t.Fatal(err)
	}
	if err := finishFlow(f); err != nil {
		t.Fatal(err)
	}
	if f.bindingStates != nil {
		t.Fatal("dead observations kept their semantic contract alive")
	}
}

func TestBindingStateContractDoesNotKeepUnobservedPayloadAlive(t *testing.T) {
	f, entry, id, result := bindingTestFunc()
	f.writeBinding(entry, ssa.OpBindingInit, id, result, ast.Position{})
	state := f.snapshotBinding(entry, id, ast.Position{})
	// This later write is retained as history, but no observation queries it.
	unused := f.addOp(entry, ssa.OpConstInt, ast.NumberType{}, ast.Position{})
	f.writeBinding(entry, ssa.OpBindingReplace, id, unused, ast.Position{})
	f.result = ast.BoolType{}
	f.graph.SetRet(entry, f.addOp(entry, ssa.OpStateHas, f.result, ast.Position{}, state))
	if err := promoteBindings(f); err != nil {
		t.Fatal(err)
	}
	if err := finishFlow(f); err != nil {
		t.Fatal(err)
	}
	if f.bindingStates == nil || len(f.bindingStates.writes) != 2 {
		t.Fatal("expected a live observation and both original write events")
	}
	if _, retained := f.values[unused.ID]; retained {
		t.Fatal("metadata kept an unused write payload alive")
	}
}
