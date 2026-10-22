package monomorph_test

import (
	"testing"

	"github.com/jakechampion/lang/internal/check/checker"
	"github.com/jakechampion/lang/internal/oracle/monomorph"
	"github.com/jakechampion/lang/internal/syntax/ast"
	"github.com/jakechampion/lang/internal/syntax/parser"
)

// A float cast converts a generic call's result. Its arguments keep their types.
func TestFloatCastDoesNotRetargetGenericCall(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		width      int
	}{
		{"widen", "function f(x: f32): f64 { return id(x) as f64; }", 32},
		{"narrow", "function f(x: f64): f32 { return id(x) as f32; }", 64},
		{"suffixed-literal", "function f(): f64 { return id(0.1f32) as f64; }", 32},
		{"compound", "function f(x: f32): f64 { return (id(x) + x) as f64; }", 32},
		{"polymorphic-literal", "function f(): f32 { return id(0.1) as f32; }", 32},
	} {
		t.Run(tc.name, func(t *testing.T) {
			prog, err := parser.Parse("function id[T](x: T): T { return x; }\n" + tc.body)
			if err != nil {
				t.Fatal(err)
			}
			info, err := checker.Check(prog)
			if err != nil {
				t.Fatal(err)
			}
			if err := monomorph.Run(prog, info); err != nil {
				t.Fatal(err)
			}
			found := false
			ast.WalkProgram(prog, func(n ast.Node) bool {
				cast, ok := n.(*ast.CastExpr)
				if !ok {
					return true
				}
				found = true
				if ft, ok := cast.InnerType.(ast.FloatType); !ok || ft.NormalWidth() != tc.width {
					t.Errorf("cast operand type %v, want f%d", cast.InnerType, tc.width)
				}
				return true
			})
			if !found {
				t.Fatal("no cast in checked program")
			}
		})
	}
}
