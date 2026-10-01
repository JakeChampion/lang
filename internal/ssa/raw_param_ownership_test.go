package ssa

import (
	"reflect"
	"testing"

	"github.com/jakechampion/lang/internal/ast"
	"github.com/jakechampion/lang/internal/ir"
)

func TestLiftRawParameterProvenanceAcrossStringABIs(t *testing.T) {
	for _, tc := range []struct {
		name string
		ptrW int
		two  bool
		want []bool
	}{
		{"native single word", 8, false, []bool{false, true, false}},
		{"native two words", 8, true, []bool{false, false, true, false}},
		{"wasm two words", 4, true, []bool{false, false, true, false}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f, err := LiftFromIR(&ir.Func{
				Name: "f", PtrW: tc.ptrW, TwoWordStr: tc.two,
				Params: []ast.Param{
					{Name: "text", Type: ast.StringType{}},
					{Name: "word", Type: ast.NumberType{Width: ast.WidthPtr}},
					{Name: "count", Type: ast.NumberType{Width: 64}},
				},
				Ops: []ir.Op{{Kind: ir.OpReturnVoid}},
			})
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(f.ParamRawAddrs, tc.want) {
				t.Fatalf("raw parameter positions = %v, want %v", f.ParamRawAddrs, tc.want)
			}
			for i, raw := range f.ParamRawAddrs {
				if raw && !f.ParamAddrs[i] {
					t.Fatal("raw word lost its pointer-width register requirement")
				}
			}
		})
	}
}

// A metadata-dependent cleanup may receive an unboxed scalar or an owned
// pointer. Its possible release establishes consumption, but cannot prove
// a unit was present on the scalar branch. A managed reference with the
// same body really does leak on that branch.
func TestCertifySeparatesRawWordsFromManagedParameters(t *testing.T) {
	for _, tc := range []struct {
		name     string
		typ      ast.Type
		raw      bool
		unplaced int
		leaks    int
	}{
		{"usize", ast.NumberType{Width: ast.WidthPtr}, true, 1, 0},
		{"usize pointer type", &ast.NumberType{Width: ast.WidthPtr}, true, 1, 0},
		{"string", ast.StringType{}, false, 0, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f, err := LiftFromIR(&ir.Func{
				Name: "cleanup",
				Params: []ast.Param{
					{Name: "boxed", Type: ast.BoolType{}},
					{Name: "value", Type: tc.typ},
				},
				Ops: []ir.Op{
					{Kind: ir.OpLoadLocal, I32: 0},
					{Kind: ir.OpIf, I32: ir.BlockTypeVoid},
					{Kind: ir.OpLoadLocal, I32: 1},
					{Kind: ir.OpConstI32, I32: 8},
					{Kind: ir.OpCallDirect, Str: "__free", I32: 2},
					{Kind: ir.OpReturnVoid},
					{Kind: ir.OpEnd},
					{Kind: ir.OpReturnVoid},
				},
			})
			if err != nil {
				t.Fatal(err)
			}
			if err := Verify(f); err != nil {
				t.Fatal(err)
			}
			if !f.ParamAddrs[1] || f.ParamRawAddrs[1] != tc.raw {
				t.Fatalf("address facts = %v / raw %v", f.ParamAddrs, f.ParamRawAddrs)
			}
			sol := SolveOwnership(map[string]*Func{f.Name: f})
			if sol.Sigs[f.Name].Params[1] != Consumed {
				t.Fatal("cleanup must still consume its second argument")
			}
			rep := Certify(f, sol.Sigs)
			if !rep.Modelled || rep.Unplaced != tc.unplaced || len(rep.Leaks) != tc.leaks {
				t.Fatalf("report = %+v, want modelled, %d unplaced, %d leaks", rep, tc.unplaced, tc.leaks)
			}
		})
	}
}

// Unknown entry provenance must not hide a definite allocation in the
// same function. Nor may it turn an unclassified word into a scalar:
// consumption and register width still need their original facts.
func TestCertifyFindsFreshLeaksBesideRawParameters(t *testing.T) {
	f := NewFunc("f")
	f.AddParam()
	f.ParamAddrs = []bool{true}
	f.ParamRawAddrs = []bool{true}
	b := f.NewBlock()
	f.Entry = b
	v := f.AddOp(b, OpAlloc)
	b.Term = Terminator{Kind: TermRet}
	sigs := map[string]Signature{"f": {Params: []ParamOwnership{Consumed}, Pointer: []bool{true}}}
	rep := Certify(f, sigs)
	if !rep.Modelled || rep.Unplaced != 1 || len(rep.Leaks) != 1 || rep.Leaks[0].Value.ID != v.ID || rep.Leaks[0].Origin != UnitFresh {
		t.Fatalf("want the fresh leak and one unplaced raw parameter, got %+v", rep)
	}
}
