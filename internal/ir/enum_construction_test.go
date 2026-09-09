package ir

import (
	"testing"

	"github.com/jakechampion/lang/internal/ast"
	"github.com/jakechampion/lang/internal/checker"
)

func TestEnumConstructionContractErasure(t *testing.T) {
	for _, tc := range []struct {
		name  string
		typ   ast.Type
		want  ast.Type
		erase func(*ast.Program, *checker.Info)
	}{
		{"str", ast.StrType{}, ast.StringType{}, eraseSurfaceTypes},
		{"handle", ast.HandleType{}, ast.NumberType{}, eraseHandleTypes},
	} {
		t.Run(tc.name, func(t *testing.T) {
			expr := &ast.Call{}
			inner := ast.TupleType{Elems: []ast.Type{ast.ArrayType{Elem: tc.typ}}}
			info := &checker.Info{EnumConstructions: map[ast.Expr]checker.EnumConstruction{
				expr: {Type: ast.EnumType{Name: "Box", Args: []ast.Type{inner, tc.typ}}, VariantIndex: 2, Payloads: []ast.Type{inner}},
			}}
			for range 2 {
				tc.erase(&ast.Program{}, info)
				construction := info.EnumConstructions[expr]
				wantInner := ast.TupleType{Elems: []ast.Type{ast.ArrayType{Elem: tc.want}}}
				wantType := ast.EnumType{Name: "Box", Args: []ast.Type{wantInner, tc.want}}
				if !ast.Equal(construction.Type, wantType) || !ast.Equal(construction.Payloads[0], wantInner) || construction.VariantIndex != 2 {
					t.Fatalf("inconsistent erased contract: %+v", construction)
				}
			}
		})
	}
}
