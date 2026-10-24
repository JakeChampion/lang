package checker

import (
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/syntax/ast"
	"github.com/jakechampion/lang/internal/syntax/diag"
)

func TestMatchNumericJoinRetainsConcreteArms(t *testing.T) {
	shapes := []struct{ name, decl, param, arms string }{
		{"literal", "", "tag: i32", "0 => %s, 1 => %s, _ => %s"},
		{"tuple", "", "tag: (i32, i32)", "(0, _) => %s, (1, _) => %s, _ => %s"},
		{"struct", "struct Tag { n: i32 }", "tag: Tag", "Tag { n: 0 } => %s, Tag { n: 1 } => %s, _ => %s"},
		{"enum", "enum Tag { A, B, C }", "tag: Tag", "A => %s, B => %s, C => %s"},
	}
	numerics := []struct{ name, a, b, literal string }{
		{"float", "f32", "f64", "0.5"},
		{"integer", "i32", "i64", "0"},
		{"tuple-float", "(f32, i32)", "(f64, i32)", "(0.5, 0)"},
		{"array-integer", "i32[]", "i64[]", "[0]"},
	}
	orders := [][3]int{{0, 1, 2}, {0, 2, 1}, {1, 0, 2}, {1, 2, 0}, {2, 0, 1}, {2, 1, 0}}
	for _, shape := range shapes {
		for _, num := range numerics {
			for _, order := range orders {
				t.Run(fmt.Sprintf("%s/%s/%v", shape.name, num.name, order), func(t *testing.T) {
					values := []string{num.literal, "a", "b"}
					arms := fmt.Sprintf(shape.arms, values[order[0]], values[order[1]], values[order[2]])
					src := fmt.Sprintf("%s function f(%s, a: %s, b: %s): i32 { let result = match (tag) { %s }; return 0; }", shape.decl, shape.param, num.a, num.b, arms)
					err := checkSource(t, src)
					if err == nil || !strings.Contains(diag.Format("join.fern", src, err), "E031") {
						t.Fatalf("concrete arm conflict must report E031, got %v", err)
					}
				})
			}
		}
	}
}

func TestUnifyIfArmsCommitsLiteralContext(t *testing.T) {
	for _, pair := range [][2]ast.Type{
		{ast.FloatType{Polymorphic: true}, ast.FloatType{Width: 64}},
		{ast.NumberType{Polymorphic: true}, ast.NumberType{Width: 32, Signed: true}},
		{ast.TupleType{Elems: []ast.Type{ast.FloatType{Polymorphic: true}, ast.NumberType{Polymorphic: true}}}, ast.TupleType{Elems: []ast.Type{ast.FloatType{Width: 64}, ast.NumberType{Width: 32, Signed: true}}}},
		{ast.ArrayType{Elem: ast.NumberType{Polymorphic: true}}, ast.ArrayType{Elem: ast.NumberType{Width: 64}}},
		{ast.SliceType{Elem: ast.NumberType{Polymorphic: true}}, ast.SliceType{Elem: ast.FloatType{Width: 32}}},
		{ast.EnumType{Name: "Option", Args: []ast.Type{ast.NumberType{Polymorphic: true}}}, ast.EnumType{Name: "Option", Args: []ast.Type{ast.NumberType{Width: 32, Signed: true}}}},
	} {
		for _, order := range [][2]ast.Type{pair, {pair[1], pair[0]}} {
			if got := unifyIfArms(order[0], order[1]); !reflect.DeepEqual(got, pair[1]) {
				t.Errorf("join(%#v, %#v) = %#v, want %#v", order[0], order[1], got, pair[1])
			}
		}
	}
}
