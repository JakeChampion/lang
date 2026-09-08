package semir

import (
	"testing"

	"github.com/jakechampion/lang/internal/ast"
	"github.com/jakechampion/lang/internal/ssa"
)

func TestProjectionRequiresIndependentEscapeUnit(t *testing.T) {
	f, block, parent, _, value := arrayRead()
	effects, err := ownershipEffects(f)
	if err != nil {
		t.Fatal(err)
	}
	e := effects.ops[block.Ops[0]]
	if e.result != resultProjection || e.parent != parent || e.parent == value {
		t.Fatalf("projection granted identity or ownership: %+v", e)
	}
	for _, input := range e.inputs {
		if input.store != storeNone || input.counted {
			t.Fatal("reading an element is not a stored or acquired reference")
		}
	}
	if len(effects.returns) != 1 || !effects.returns[0].counted || effects.returns[0].value != value {
		t.Fatal("projected element must secure an independent lifetime when returned")
	}
}

func TestRepeatedStoredValuesRequireRepeatedUnits(t *testing.T) {
	f := newFunc("twice", ast.ArrayType{Elem: ast.StringType{}})
	item := f.addParam(ast.StringType{}, ParamCounted, ast.Position{})
	block := f.graph.NewBlock()
	array := f.addOp(block, ssa.OpArrayMake, f.result, ast.Position{}, item, item)
	f.graph.SetRet(block, array)
	effects, err := ownershipEffects(f)
	if err != nil {
		t.Fatal(err)
	}
	e := effects.ops[block.Ops[0]]
	if e.result != resultCounted || len(e.inputs) != 2 {
		t.Fatal("constructor lost its ownership obligations")
	}
	for _, input := range e.inputs {
		if input.value != item || input.store != storeValue || !input.counted {
			t.Fatal("each stored occurrence requires a separate lifetime unit")
		}
	}
	// The one counted parameter cannot be transferred twice. No part of this
	// contract describes either store as an already-proven destructive move.
}

func TestAppendSeparatesBufferAndElementOwnership(t *testing.T) {
	for _, tc := range []struct {
		name    string
		elem    ast.Type
		counted bool
	}{
		{"strings", ast.StringType{}, true},
		{"nested-arrays", ast.ArrayType{Elem: ast.StringType{}}, true},
		{"integers", ast.NumberType{}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFunc("append", ast.ArrayType{Elem: tc.elem})
			array := f.addParam(f.result, ParamBorrow, ast.Position{})
			mode := ParamValue
			if tc.counted {
				mode = ParamBorrow
			}
			item := f.addParam(tc.elem, mode, ast.Position{})
			block := f.graph.NewBlock()
			updated := f.addOp(block, ssa.OpArrayAppend, f.result, ast.Position{}, array, item)
			f.graph.SetRet(block, updated)
			effects, err := ownershipEffects(f)
			if err != nil {
				t.Fatal(err)
			}
			e := effects.ops[block.Ops[0]]
			if e.result != resultCounted || e.inputs[0].store != storeArrayElements || e.inputs[1].store != storeValue {
				t.Fatal("append conflates buffer identity with stored children")
			}
			for _, input := range e.inputs {
				if input.counted != tc.counted {
					t.Fatal("element lifetime contract does not follow its full semantic type")
				}
			}
		})
	}
}

func TestEverySupportedOperationHasAnEffectContract(t *testing.T) {
	decl, info := checkedFunc(t, `function pilot(items: string[], choose: boolean): string[] {
  var pair = (items, 7i64);
  let (alias, _) = pair;
  var item = alias[0];
  if (choose) { return [item, "literal"]; }
  return [];
}`)
	f, err := BuildFunc(decl, info)
	if err != nil {
		t.Fatal(err)
	}
	effects, err := ownershipEffects(f)
	if err != nil {
		t.Fatal(err)
	}
	for _, block := range f.graph.Blocks {
		for _, op := range block.Ops {
			if effects.ops[op].result == resultInvalid {
				t.Fatalf("%s lacks an ownership contract", op.Kind)
			}
		}
	}
}

func TestLoopPhiDoesNotCreateOwnershipUnits(t *testing.T) {
	f := newFunc("loop", ast.ArrayType{Elem: ast.StringType{}})
	array := f.addParam(f.result, ParamCounted, ast.Position{})
	condition := f.addParam(ast.BoolType{}, ParamValue, ast.Position{})
	entry, header, body, exit := f.graph.NewBlock(), f.graph.NewBlock(), f.graph.NewBlock(), f.graph.NewBlock()
	f.graph.SetBr(entry, header)
	f.graph.SetBrIf(header, condition, body, exit)
	f.graph.SetBr(body, header)
	value := f.addPhi(header, f.result, ast.Position{}, array, array)
	header.Ops[0].Args[1] = value
	f.graph.SetRet(exit, value)
	effects, err := ownershipEffects(f)
	if err != nil {
		t.Fatal(err)
	}
	e := effects.ops[header.Ops[0]]
	if e.result != resultJoin || len(e.inputs) != 2 {
		t.Fatal("loop phi is a selection, not a freshly owned value")
	}
	for _, input := range e.inputs {
		if input.counted || input.store != storeNone {
			t.Fatal("loop backedge invents an ownership unit")
		}
	}
}

func TestEffectsRejectUnverifiedGraph(t *testing.T) {
	f, block, _, _, _ := arrayRead()
	block.Ops[0].Args = nil
	if effects, err := ownershipEffects(f); effects != nil || err == nil {
		t.Fatal("malformed projection reached ownership analysis")
	}
}
