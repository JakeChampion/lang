package semir

import (
	"slices"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/ast"
	"github.com/jakechampion/lang/internal/ssa"
)

func bindingTestFunc() (*Func, *ssa.Block, BindingID, ssa.Value) {
	f := newFunc("places", ast.NumberType{})
	f.unpromotedBindings = true
	entry := f.graph.NewBlock()
	pos := ast.Position{Line: 1, Col: 1}
	id := f.addBinding("item", ast.NumberType{}, pos)
	value := f.addOp(entry, ssa.OpConstInt, ast.NumberType{}, pos)
	entry.Ops[0].Imm = 7
	return f, entry, id, value
}

func TestVerifyBindingInitialization(t *testing.T) {
	for _, tc := range []struct {
		name string
		edit func(*Func, *ssa.Block, BindingID, ssa.Value)
		want string
	}{
		{"read-before-init", func(f *Func, entry *ssa.Block, id BindingID, value ssa.Value) {
			read := f.readBinding(entry, id, ast.Position{})
			f.writeBinding(entry, ssa.OpBindingInit, id, value, ast.Position{})
			f.graph.SetRet(entry, read)
		}, "requires initialization"},
		{"replace-before-init", func(f *Func, entry *ssa.Block, id BindingID, value ssa.Value) {
			f.writeBinding(entry, ssa.OpBindingReplace, id, value, ast.Position{})
			f.graph.SetRet(entry, value)
		}, "requires initialization"},
		{"duplicate-initializer", func(f *Func, entry *ssa.Block, id BindingID, value ssa.Value) {
			f.writeBinding(entry, ssa.OpBindingInit, id, value, ast.Position{})
			f.writeBinding(entry, ssa.OpBindingInit, id, value, ast.Position{})
			f.graph.SetRet(entry, value)
		}, "multiple initializer identities"},
		{"conditional-absence", func(f *Func, entry *ssa.Block, id BindingID, value ssa.Value) {
			flag := f.addParam(ast.BoolType{}, ParamValue, ast.Position{})
			yes, no, join := f.graph.NewBlock(), f.graph.NewBlock(), f.graph.NewBlock()
			f.graph.SetBrIf(entry, flag, yes, no)
			f.writeBinding(yes, ssa.OpBindingInit, id, value, ast.Position{})
			f.graph.SetBr(yes, join)
			f.graph.SetBr(no, join)
			f.graph.SetRet(join, f.readBinding(join, id, ast.Position{}))
		}, "requires initialization"},
		{"backedge-does-not-initialize-first-entry", func(f *Func, entry *ssa.Block, id BindingID, value ssa.Value) {
			flag := f.addParam(ast.BoolType{}, ParamValue, ast.Position{})
			header, body, exit := f.graph.NewBlock(), f.graph.NewBlock(), f.graph.NewBlock()
			f.graph.SetBr(entry, header)
			read := f.readBinding(header, id, ast.Position{})
			f.graph.SetBrIf(header, flag, body, exit)
			f.writeBinding(body, ssa.OpBindingInit, id, value, ast.Position{})
			f.graph.SetBr(body, header)
			f.graph.SetRet(exit, read)
		}, "requires initialization"},
		{"foreign-identity", func(f *Func, entry *ssa.Block, id BindingID, value ssa.Value) {
			op := f.writeBinding(entry, ssa.OpBindingInit, id, value, ast.Position{})
			op.Imm++
			f.graph.SetRet(entry, value)
		}, "invalid identity"},
		{"phase-escape", func(f *Func, entry *ssa.Block, id BindingID, value ssa.Value) {
			f.writeBinding(entry, ssa.OpBindingInit, id, value, ast.Position{})
			f.unpromotedBindings = false
			f.graph.SetRet(entry, value)
		}, "outside its phase"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f, entry, id, value := bindingTestFunc()
			tc.edit(f, entry, id, value)
			if err := ssa.Verify(f.graph); err != nil {
				t.Fatalf("fixture must retain valid SSA: %v", err)
			}
			if err := Verify(f); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("want %q, got %v", tc.want, err)
			}
		})
	}
}

func TestPromoteBindingsPreservesInstructionAndPredecessorOrder(t *testing.T) {
	for _, reverse := range []bool{false, true} {
		f, entry, id, value := bindingTestFunc()
		flag := f.addParam(ast.BoolType{}, ParamValue, ast.Position{})
		f.writeBinding(entry, ssa.OpBindingInit, id, value, ast.Position{})
		yes, no, join := f.graph.NewBlock(), f.graph.NewBlock(), f.graph.NewBlock()
		f.graph.SetBrIf(entry, flag, yes, no)
		before := f.readBinding(yes, id, ast.Position{})
		replacement := f.addOp(yes, ssa.OpConstInt, ast.NumberType{}, ast.Position{})
		f.writeBinding(yes, ssa.OpBindingReplace, id, replacement, ast.Position{})
		// The saved read still names 7, not the subsequent replacement.
		saved := f.addOp(yes, ssa.OpAdd, ast.NumberType{}, ast.Position{}, before, replacement)
		f.writeBinding(yes, ssa.OpBindingReplace, id, saved, ast.Position{})
		f.graph.SetBr(no, join) // Intentionally differs from block allocation order.
		f.graph.SetBr(yes, join)
		f.graph.SetRet(join, f.readBinding(join, id, ast.Position{}))
		if reverse {
			slices.Reverse(f.graph.Blocks)
		}
		if _, err := ownershipEffects(f); err == nil || !strings.Contains(err.Error(), "promotion must precede") {
			t.Fatalf("ownership accepted an unpromoted graph: %v", err)
		}
		if err := promoteBindings(f); err != nil {
			t.Fatal(err)
		}
		if err := Verify(f); err != nil {
			t.Fatal(err)
		}
		phi := join.Ops[0]
		if phi.Kind != ssa.OpPhi || !slices.Equal(phi.Args, []ssa.Value{value, saved}) {
			t.Fatalf("promotion lost predecessor order: %+v", phi)
		}
		for _, block := range f.graph.Blocks {
			for _, op := range block.Ops {
				if bindingOp(op.Kind) {
					t.Fatal("binding operation escaped promotion")
				}
				if op.Result == saved && op.Args[0] != value {
					t.Fatal("replacement changed the saved read")
				}
			}
		}
		if err := promoteBindings(f); err == nil {
			t.Fatal("promotion silently accepted a repeated phase transition")
		}
	}
}

func TestPromoteBindingsLongReadChain(t *testing.T) {
	f, entry, id, value := bindingTestFunc()
	f.writeBinding(entry, ssa.OpBindingInit, id, value, ast.Position{})
	for i := 0; i < 1024; i++ {
		read := f.readBinding(entry, id, ast.Position{})
		f.writeBinding(entry, ssa.OpBindingReplace, id, read, ast.Position{})
	}
	f.graph.SetRet(entry, f.readBinding(entry, id, ast.Position{}))
	if err := promoteBindings(f); err != nil {
		t.Fatal(err)
	}
	if err := Verify(f); err != nil {
		t.Fatal(err)
	}
	if entry.Term.Value != value || len(entry.Ops) != 1 {
		t.Fatal("read chain did not promote to the original value")
	}
}
