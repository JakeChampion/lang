package semir

import (
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/ast"
	"github.com/jakechampion/lang/internal/ssa"
)

func bindingLifetimeFunc() (*Func, *ssa.Block, *ssa.Block, BindingID, ssa.Value) {
	f, entry, id, value := bindingTestFunc()
	b := builder{fn: f}
	pos := ast.Position{Line: 1, Col: 1}
	root := b.newCleanupBoundary(nil, entry, nil, nil, pos)
	body, exit := f.graph.NewBlock(), f.graph.NewBlock()
	iteration := b.newCleanupBoundary(root, body, body, exit, pos)
	f.bindings[id-1].boundary = iteration
	f.graph.SetBr(entry, body)
	f.writeBinding(body, ssa.OpBindingInit, id, value, pos)
	f.graph.SetBr(body, exit)
	f.graph.SetRet(exit, value)
	f.cleanupExits = []*cleanupExit{
		{from: iteration, through: iteration, start: body, finish: body, target: exit, kind: cleanupBreak, ends: []cleanupEnd{{iteration, body}}},
		{from: root, through: root, start: exit, finish: exit, kind: cleanupReturn, ends: []cleanupEnd{{root, exit}}},
	}
	return f, body, exit, id, value
}

func TestBindingLifetimeRejectsStalePlaces(t *testing.T) {
	for _, tc := range []struct {
		name string
		edit func(*Func, *ssa.Block, *ssa.Block, BindingID, ssa.Value)
		want string
	}{
		{"read-after-break", func(f *Func, _, exit *ssa.Block, id BindingID, _ ssa.Value) {
			f.graph.SetRet(exit, f.readBinding(exit, id, ast.Position{}))
		}, "requires initialization"},
		{"replace-after-break", func(f *Func, _, exit *ssa.Block, id BindingID, value ssa.Value) {
			f.writeBinding(exit, ssa.OpBindingReplace, id, value, ast.Position{})
		}, "requires initialization"},
		{"foreign-boundary", func(f *Func, _, _ *ssa.Block, id BindingID, _ ssa.Value) {
			f.bindings[id-1].boundary = &cleanupBoundary{owner: f}
		}, "foreign lifetime boundary"},
		{"known-boundary-foreign-owner", func(f *Func, _, _ *ssa.Block, id BindingID, _ ssa.Value) {
			// Membership is not ownership. Keep the binding's known boundary,
			// but require the independent boundary verifier to reject its owner.
			f.bindings[id-1].boundary.owner = &Func{}
		}, "invalid boundary identity, owner or entry"},
		{"missing-boundary", func(f *Func, _, _ *ssa.Block, id BindingID, _ ssa.Value) {
			f.bindings[id-1].boundary = nil
		}, "missing or foreign lifetime boundary"},
		{"false-root-ownership", func(f *Func, _, _ *ssa.Block, id BindingID, _ ssa.Value) {
			f.bindings[id-1].boundary = f.boundaries[0]
		}, "initializer belongs to a different active boundary"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f, body, exit, id, value := bindingLifetimeFunc()
			tc.edit(f, body, exit, id, value)
			if tc.name == "read-after-break" || tc.name == "replace-after-break" {
				if bindingInitializationOracle(f) {
					t.Fatal("exact oracle missed lifetime end")
				}
				ends := f.cleanupExits
				f.cleanupExits = nil
				acceptedWithoutEnds := bindingInitializationOracle(f)
				f.cleanupExits = ends
				if !acceptedWithoutEnds {
					t.Fatal("fixture must isolate lifetime reset from ordinary initialization")
				}
			}
			if err := ssa.Verify(f.graph); err != nil {
				t.Fatal(err)
			}
			if err := Verify(f); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("want %q, got %v", tc.want, err)
			}
		})
	}
}

func TestBindingLifetimePreservesSavedSSAValue(t *testing.T) {
	f, body, exit, id, value := bindingLifetimeFunc()
	saved := f.readBinding(body, id, ast.Position{})
	f.graph.SetRet(exit, saved)
	if err := Verify(f); err != nil {
		t.Fatal(err)
	}
	if err := promoteBindings(f); err != nil {
		t.Fatal(err)
	}
	if err := Verify(f); err != nil {
		t.Fatal(err)
	}
	if exit.Term.Value != value {
		t.Fatal("ending the place invalidated its saved SSA value")
	}
}

func TestBindingLifetimePreservesOuterReplacement(t *testing.T) {
	f, body, exit, _, value := bindingLifetimeFunc()
	outer := f.addBinding("outer", ast.NumberType{}, ast.Position{})
	f.bindings[outer-1].boundary = f.boundaries[0]
	f.writeBinding(f.graph.Entry, ssa.OpBindingInit, outer, value, ast.Position{})
	replacement := f.addOp(body, ssa.OpConstInt, ast.NumberType{}, ast.Position{})
	f.writeBinding(body, ssa.OpBindingReplace, outer, replacement, ast.Position{})
	f.graph.SetRet(exit, f.readBinding(exit, outer, ast.Position{}))
	if err := promoteBindings(f); err != nil {
		t.Fatal(err)
	}
	if err := Verify(f); err != nil {
		t.Fatal(err)
	}
	if exit.Term.Value != replacement {
		t.Fatal("iteration end erased an outer binding update")
	}
}

func TestBindingLifetimeMasksKeepNestedAndRootExitsIndependent(t *testing.T) {
	f, body, exit, _, value := bindingLifetimeFunc()
	root, iteration := f.boundaries[0], f.boundaries[1]
	flag := f.addParam(ast.BoolType{}, ParamValue, ast.Position{})
	f.graph.SetBrIf(f.graph.Entry, flag, body, exit)
	f.graph.SetRet(body, value)
	// SetRet changes the terminator, not the old successor's predecessor list.
	exit.Preds = []*ssa.Block{f.graph.Entry}
	iteration.exit = nil
	f.cleanupExits[0] = &cleanupExit{from: iteration, through: root, start: body, finish: body, kind: cleanupReturn, ends: []cleanupEnd{{iteration, body}, {root, body}}}
	// Include absent places and more than one mask word. Ending these places
	// must not invent a payload or pollute another exit's shared scope mask.
	for i := 0; i < 70; i++ {
		id := f.addBinding("absent", ast.NumberType{}, ast.Position{})
		f.bindings[id-1].boundary = root
		if i%2 == 0 {
			f.bindings[id-1].boundary = iteration
		}
	}
	if err := Verify(f); err != nil {
		t.Fatal(err)
	}
	ends := bindingLifetimeEnds(f)
	for index, binding := range f.bindings {
		bit := uint64(1) << (index % 64)
		if ends[body][index/64]&bit == 0 {
			t.Fatal("nested return omitted a crossed boundary's place")
		}
		rootOnly := ends[exit][index/64]&bit != 0
		if rootOnly != (binding.boundary == root) {
			t.Fatal("nested and root-only exit masks alias")
		}
	}
}
