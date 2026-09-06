package parser

import (
	"testing"

	"github.com/jakechampion/lang/internal/ast"
)

// An arrow lambda's declared return type may be a TUPLE (#8706).
//
// After the parameter list, `: (i32, i32) => …` is ambiguous to a type parser
// that treats `( … ) =>` as a function type: in return-type position an `=>`
// ALWAYS follows, so a parenthesised type could never be read as a tuple, and
// `(i32, i32) => {` consumed the lambda's own arrow and then wanted a type
// where the body started. The `function` spelling was the only way to write a
// tuple-returning lambda, and #2673 retires it.
//
// The rule: in that position the top-level function-type arrow is off, so a
// parenthesised type is a tuple (or a grouping). A function-type return is
// still writable — with grouping parens, whose inner `=>` is consumed inside
// them.
func TestArrowLambdaReturnTypeParens(t *testing.T) {
	for _, tc := range []struct {
		name string
		src  string
		want func(*testing.T, ast.Type)
	}{
		{
			"tuple return", `function main(): i32 { var f = (p: (i32, i32)): (i32, i32) => { return p; }; return 0; }`,
			func(t *testing.T, ty ast.Type) {
				tt, ok := ty.(ast.TupleType)
				if !ok {
					t.Fatalf("want a TupleType return, got %T", ty)
				}
				if len(tt.Elems) != 2 {
					t.Errorf("want 2 tuple elements, got %d", len(tt.Elems))
				}
			},
		},
		{
			"tuple return, no params", `function main(): i32 { var f = (): (string, i32) => { return ("x", 1); }; return 0; }`,
			func(t *testing.T, ty ast.Type) {
				if _, ok := ty.(ast.TupleType); !ok {
					t.Fatalf("want a TupleType return, got %T", ty)
				}
			},
		},
		{
			// The grouping parens are what make this writable at all.
			"function-type return", `function main(): i32 { var f = (p: i32): ((i32) => i32) => (q: i32) => p + q; return 0; }`,
			func(t *testing.T, ty ast.Type) {
				if _, ok := ty.(*ast.FuncType); !ok {
					t.Fatalf("want a FuncType return, got %T", ty)
				}
			},
		},
		{
			// A `=>` INSIDE the parens still spells a function type, so the
			// suppression must not reach nested types.
			"tuple of function types", `function main(): i32 { var f = (): ((i32) => i32, i32) => { return ((x: i32) => x, 1); }; return 0; }`,
			func(t *testing.T, ty ast.Type) {
				tt, ok := ty.(ast.TupleType)
				if !ok {
					t.Fatalf("want a TupleType return, got %T", ty)
				}
				if _, ok := tt.Elems[0].(*ast.FuncType); !ok {
					t.Errorf("want the first element to stay a FuncType, got %T", tt.Elems[0])
				}
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			prog, err := Parse(tc.src)
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			var lam *ast.Lambda
			ast.WalkProgram(prog, func(n ast.Node) bool {
				if l, ok := n.(*ast.Lambda); ok && lam == nil {
					lam = l
				}
				return true
			})
			if lam == nil {
				t.Fatal("no lambda in the parsed program")
			}
			tc.want(t, lam.ReturnType)
		})
	}
}

// A NAMED function's return type is unaffected: `{` follows it, so `(T,…) => R`
// there is unambiguously a function type and must keep parsing as one.
func TestNamedFunctionFunctionTypeReturnUnaffected(t *testing.T) {
	prog, err := Parse(`function mk(): (i32, i32) => i32 { return (a: i32, b: i32) => a + b; }
function main(): i32 { return mk()(40, 2); }`)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	var fn *ast.FuncDecl
	ast.WalkProgram(prog, func(n ast.Node) bool {
		if f, ok := n.(*ast.FuncDecl); ok && f.Name == "mk" {
			fn = f
		}
		return true
	})
	if fn == nil {
		t.Fatal("no `mk` in the parsed program")
	}
	if _, ok := fn.ReturnType.(*ast.FuncType); !ok {
		t.Errorf("a named function's `(i32, i32) => i32` return must stay a FuncType, got %T", fn.ReturnType)
	}
}
