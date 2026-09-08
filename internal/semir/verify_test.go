package semir

import (
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/ast"
	"github.com/jakechampion/lang/internal/ssa"
)

func arrayRead() (*Func, *ssa.Block, ssa.Value, ssa.Value, ssa.Value) {
	f := newFunc("read", ast.StringType{})
	array := f.addParam(ast.ArrayType{Elem: ast.StringType{}}, ParamBorrow, ast.Position{})
	index := f.addParam(ast.NumberType{}, ParamValue, ast.Position{})
	b := f.graph.NewBlock()
	v := f.addOp(b, ssa.OpArrayGet, ast.StringType{}, ast.Position{}, array, index)
	f.graph.SetRet(b, v)
	return f, b, array, index, v
}

func TestProjectionDependencies(t *testing.T) {
	f, b, array, index, value := arrayRead()
	if err := Verify(f); err != nil {
		t.Fatal(err)
	}
	p, ok := projection(b.Ops[0])
	if !ok || p.Container != array || p.Index != index || p.Container == value {
		t.Fatalf("lost containment identity: %+v", p)
	}
	uses := ssa.BuildUses(f.graph)
	for _, v := range []ssa.Value{array, index} {
		if uses.Count(v) != 1 || uses.Of(v)[0].Op != b.Ops[0] {
			t.Fatalf("projection dependency %v hidden from def-use", v)
		}
	}
	// An own parameter supplies a counted unit, not a claim about aliases.
	f.modes[0] = ParamCounted
	if err := Verify(f); err != nil {
		t.Fatal(err)
	}
}

func TestRejectInvalidSemanticGraphs(t *testing.T) {
	for _, tc := range []struct {
		name string
		edit func(*Func, *ssa.Block, ssa.Value, ssa.Value, ssa.Value)
		want string
	}{
		{"missing-type", func(f *Func, _ *ssa.Block, _, _, v ssa.Value) { delete(f.values, v.ID) }, "no semantic type"},
		{"nil-type", func(f *Func, _ *ssa.Block, _, _, v ssa.Value) { f.values[v.ID] = valueInfo{} }, "unresolved or unsupported"},
		{"unknown-element", func(f *Func, _ *ssa.Block, a, _, _ ssa.Value) {
			f.values[a.ID] = valueInfo{typ: ast.ArrayType{Elem: ast.ParamType{Name: "T"}}}
		}, "unresolved or unsupported"},
		{"unsettled-integer", func(f *Func, _ *ssa.Block, _, i, _ ssa.Value) {
			f.values[i.ID] = valueInfo{typ: ast.NumberType{Polymorphic: true}}
		}, "unresolved or unsupported"},
		{"invalid-width", func(f *Func, _ *ssa.Block, _, i, _ ssa.Value) {
			f.values[i.ID] = valueInfo{typ: ast.NumberType{Width: 17}}
		}, "unresolved or unsupported"},
		{"hidden-container", func(_ *Func, b *ssa.Block, _, i, _ ssa.Value) { b.Ops[0].Args = []ssa.Value{i} }, "arity"},
		{"zero-container", func(_ *Func, b *ssa.Block, _, _, _ ssa.Value) { b.Ops[0].Args[0] = ssa.Value{} }, "invalid or foreign"},
		{"foreign-container-same-id", func(_ *Func, b *ssa.Block, _, _, _ ssa.Value) {
			b.Ops[0].Args[0] = ssa.NewFunc("other").AddParam()
		}, "invalid or foreign"},
		{"untyped-load", func(_ *Func, b *ssa.Block, _, _, _ ssa.Value) { b.Ops[0].Kind = ssa.OpLoad }, "not supported"},
		{"wrong-element", func(f *Func, _ *ssa.Block, _, _, v ssa.Value) {
			f.values[v.ID] = valueInfo{typ: ast.NumberType{}}
		}, "types or arity"},
		{"non-integer-index", func(f *Func, _ *ssa.Block, _, i, _ ssa.Value) {
			f.values[i.ID] = valueInfo{typ: ast.BoolType{}}
		}, "types or arity"},
		{"wrong-return", func(f *Func, _ *ssa.Block, _, _, _ ssa.Value) { f.result = ast.NumberType{} }, "return type"},
		{"missing-return", func(_ *Func, b *ssa.Block, _, _, _ ssa.Value) { b.Term.Value = ssa.Value{} }, "return type"},
		{"value-mode-on-reference", func(f *Func, _ *ssa.Block, _, _, _ ssa.Value) { f.modes[0] = ParamValue }, "ownership mode"},
		{"counted-mode-on-number", func(f *Func, _ *ssa.Block, _, _, _ ssa.Value) { f.modes[1] = ParamCounted }, "ownership mode"},
		{"missing-mode", func(f *Func, _ *ssa.Block, _, _, _ ssa.Value) { f.modes = nil }, "modes"},
		{"stale-metadata", func(f *Func, _ *ssa.Block, _, _, _ ssa.Value) { f.values[99] = valueInfo{typ: ast.StringType{}} }, "stale"},
		{"split-result", func(_ *Func, b *ssa.Block, _, i, _ ssa.Value) { b.Ops[0].Result2 = i }, "ABI-split"},
		{"duplicate-definition", func(_ *Func, b *ssa.Block, a, _, _ ssa.Value) { b.Ops[0].Result = a }, "defined twice"},
		{"nil-block", func(f *Func, _ *ssa.Block, _, _, _ ssa.Value) { f.graph.Blocks = append(f.graph.Blocks, nil) }, "block identity"},
		{"duplicate-block", func(f *Func, b *ssa.Block, _, _, _ ssa.Value) { f.graph.Blocks = append(f.graph.Blocks, b) }, "block identity"},
		{"foreign-successor", func(f *Func, b *ssa.Block, _, _, _ ssa.Value) { f.graph.SetBr(b, ssa.NewFunc("other").NewBlock()) }, "foreign successor"},
		{"stale-predecessor", func(f *Func, b *ssa.Block, _, _, _ ssa.Value) {
			extra := f.graph.NewBlock()
			f.graph.SetRet(extra, b.Term.Value)
			extra.Preds = []*ssa.Block{b}
		}, "stale predecessor"},
		{"nil-op", func(_ *Func, b *ssa.Block, _, _, _ ssa.Value) { b.Ops[0] = nil }, "nil operation"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f, b, a, i, v := arrayRead()
			tc.edit(f, b, a, i, v)
			if err := Verify(f); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Verify = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestTypedBranchJoin(t *testing.T) {
	f := newFunc("choose", ast.ArrayType{Elem: ast.StringType{}})
	left := f.addParam(f.result, ParamBorrow, ast.Position{})
	right := f.addParam(f.result, ParamBorrow, ast.Position{})
	cond := f.addParam(ast.BoolType{}, ParamValue, ast.Position{})
	entry, yes, no, join := f.graph.NewBlock(), f.graph.NewBlock(), f.graph.NewBlock(), f.graph.NewBlock()
	f.graph.SetBrIf(entry, cond, yes, no)
	f.graph.SetBr(yes, join)
	f.graph.SetBr(no, join)
	v := f.addPhi(join, f.result, ast.Position{}, left, right)
	f.graph.SetRet(join, v)
	if err := Verify(f); err != nil {
		t.Fatal(err)
	}
	f.values[right.ID] = valueInfo{typ: ast.ArrayType{Elem: ast.NumberType{}}}
	if err := Verify(f); err == nil {
		t.Fatal("phi admitted incompatible semantic types with the same machine representation")
	}
}

func TestProjectionDominance(t *testing.T) {
	f := newFunc("bad_branch", ast.StringType{})
	cond := f.addParam(ast.BoolType{}, ParamValue, ast.Position{})
	index := f.addParam(ast.NumberType{}, ParamValue, ast.Position{})
	entry, yes, no := f.graph.NewBlock(), f.graph.NewBlock(), f.graph.NewBlock()
	f.graph.SetBrIf(entry, cond, yes, no)
	item := f.addOp(yes, ssa.OpConstString, ast.StringType{}, ast.Position{})
	array := f.addOp(yes, ssa.OpArrayMake, ast.ArrayType{Elem: ast.StringType{}}, ast.Position{}, item)
	f.graph.SetRet(yes, item)
	v := f.addOp(no, ssa.OpArrayGet, ast.StringType{}, ast.Position{}, array, index)
	f.graph.SetRet(no, v)
	if err := Verify(f); err == nil || !strings.Contains(err.Error(), "dominates") {
		t.Fatalf("projection from another branch accepted: %v", err)
	}
}

func TestTupleProjectionAndImmutableAppend(t *testing.T) {
	arrayType := ast.ArrayType{Elem: ast.StringType{}}
	tupleType := ast.TupleType{Elems: []ast.Type{ast.NumberType{Width: 64, Signed: true}, arrayType}}
	f := newFunc("append_field", arrayType)
	tuple := f.addParam(tupleType, ParamBorrow, ast.Position{})
	item := f.addParam(ast.StringType{}, ParamBorrow, ast.Position{})
	b := f.graph.NewBlock()
	array := f.addOp(b, ssa.OpTupleGet, arrayType, ast.Position{}, tuple)
	b.Ops[0].Imm = 1
	updated := f.addOp(b, ssa.OpArrayAppend, arrayType, ast.Position{}, array, item)
	f.graph.SetRet(b, updated)
	if err := Verify(f); err != nil {
		t.Fatal(err)
	}
	if array == updated || ssa.BuildUses(f.graph).Count(tuple) != 1 {
		t.Fatal("lost replacement identity or tuple dependency")
	}
	for _, field := range []int64{-1, 0, 2} {
		b.Ops[0].Imm = field
		if err := Verify(f); err == nil {
			t.Fatalf("invalid tuple projection %d accepted", field)
		}
	}
}

func TestBindingIdentityIsNotSpellingOrValue(t *testing.T) {
	f := newFunc("scope", ast.VoidType{})
	a := f.addBinding("items", ast.ArrayType{Elem: ast.StringType{}}, ast.Position{Line: 1})
	b := f.addBinding("items", ast.ArrayType{Elem: ast.StringType{}}, ast.Position{Line: 2})
	if a == 0 || b == 0 || a == b {
		t.Fatal("shadowed declarations need distinct identities")
	}
}

func TestSemanticOperationsNotLowLevelPure(t *testing.T) {
	for _, kind := range []ssa.OpKind{ssa.OpArrayMake, ssa.OpArrayGet, ssa.OpArrayAppend, ssa.OpTupleMake, ssa.OpTupleGet, ssa.OpSemanticCall} {
		if ssa.IsPure(kind) {
			t.Errorf("%s admitted to low-level purity rules before ownership lowering", kind)
		}
	}
}
