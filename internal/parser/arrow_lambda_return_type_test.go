package parser

import (
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/ast"
)

// lambdaReturnType parses a program whose first statement binds an arrow
// lambda and returns that lambda's written return type.
func lambdaReturnType(t *testing.T, src string) ast.Type {
	t.Helper()
	prog, err := Parse(src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	v, ok := prog.Funcs[0].Body.Stmts[0].(*ast.Var)
	if !ok {
		t.Fatalf("first statement is %T, want *ast.Var", prog.Funcs[0].Body.Stmts[0])
	}
	lam, ok := v.Init.(*ast.Lambda)
	if !ok {
		t.Fatalf("initialiser is %T, want *ast.Lambda", v.Init)
	}
	if lam.ReturnUnannotated {
		t.Fatalf("lambda reports no written return type")
	}
	return lam.ReturnType
}

// In an arrow lambda's return-type position the `=>` after a parenthesised
// type is the lambda's own, so `(A, B)` there is a tuple and `(A)` a
// grouping (#8706, #8717, #8726). The type parser used to read `(i32, i32) =>`
// as a function type and then want that type's result where the body
// starts, which left a tuple-returning lambda unable to annotate its return.
func TestArrowLambdaTupleReturnType(t *testing.T) {
	for _, tc := range []struct{ name, src string }{
		{"braced_body", `function main(): i32 { var id = (p: (i32, i32)): (i32, i32) => { return p; }; return 0; }`},
		{"expression_body", `function main(): i32 { var id = (p: (i32, i32)): (i32, i32) => p; return 0; }`},
		{"string_tuple_expression_body", `function main(): i32 { var g = (): (string, i32) => ("abcd", 7); return 0; }`},
		{"nullary_braced_body", `function main(): i32 { var g = (): (string, i32) => { return ("abcd", 7); }; return 0; }`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rt, ok := lambdaReturnType(t, tc.src).(ast.TupleType)
			if !ok || len(rt.Elems) != 2 {
				t.Fatalf("return type = %#v, want a 2-element ast.TupleType", rt)
			}
		})
	}
}

// A single parenthesised element there is a grouping of the inner type, not
// a one-tuple — the same reading `(T)` has everywhere else.
func TestArrowLambdaGroupedReturnType(t *testing.T) {
	rt := lambdaReturnType(t, `function main(): i32 { var g = (): (i32) => { return 7; }; return 0; }`)
	if _, ok := rt.(ast.NumberType); !ok {
		t.Errorf("return type = %#v, want ast.NumberType (the grouped i32)", rt)
	}
}

// A function-type return is written grouped, `((A) => B)`: its arrow is
// consumed inside the parens, so the `=>` that follows is the lambda's.
func TestArrowLambdaGroupedFunctionReturnType(t *testing.T) {
	for _, tc := range []struct{ name, src string }{
		{"expression_body", `function main(): i32 { var mk = (p: i32): ((i32) => i32) => (q: i32) => p + q; return 0; }`},
		{"braced_body", `function main(): i32 { var mk = (p: i32): ((i32) => i32) => { return (q: i32): i32 => p + q; }; return 0; }`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rt, ok := lambdaReturnType(t, tc.src).(*ast.FuncType)
			if !ok || len(rt.Params) != 1 {
				t.Fatalf("return type = %#v, want a 1-param *ast.FuncType", rt)
			}
			if _, ok := rt.Result.(ast.NumberType); !ok {
				t.Errorf("function-type result = %#v, want ast.NumberType", rt.Result)
			}
		})
	}
}

// The flag covers exactly one type: an `=>` NESTED inside the parens still
// spells a function type, so `((i32) => i32, i32)` is a tuple whose first
// element is a function.
func TestArrowLambdaTupleReturnTypeWithFunctionElement(t *testing.T) {
	rt, ok := lambdaReturnType(t, `function main(): i32 { var pair = (): ((i32) => i32, i32) => ((n: i32) => n + 1, 5); return 0; }`).(ast.TupleType)
	if !ok || len(rt.Elems) != 2 {
		t.Fatalf("return type = %#v, want a 2-element ast.TupleType", rt)
	}
	if _, ok := rt.Elems[0].(*ast.FuncType); !ok {
		t.Errorf("first element = %#v, want *ast.FuncType", rt.Elems[0])
	}
}

// The suffix loop still runs on the tuple: `(i32, i32)[]` is an array of
// tuples in return position too.
func TestArrowLambdaTupleArrayReturnType(t *testing.T) {
	rt, ok := lambdaReturnType(t, `function main(): i32 { var g = (): (i32, i32)[] => [(1, 2)]; return 0; }`).(ast.ArrayType)
	if !ok {
		t.Fatalf("return type = %#v, want ast.ArrayType", rt)
	}
	if _, ok := rt.Elem.(ast.TupleType); !ok {
		t.Errorf("element = %#v, want ast.TupleType", rt.Elem)
	}
}

// Outside an arrow lambda's return position nothing changes: a NAMED
// function's return type is followed by `{`, so `(i32, i32) => i32` there is
// still the function type over a tuple of parameters, and a variable's
// annotation still takes the bare function type.
func TestFunctionTypeReturnOutsideArrowLambdaUnchanged(t *testing.T) {
	prog, err := Parse(`function mk(): (i32, i32) => i32 { return (a: i32, b: i32): i32 => a + b; }
function main(): i32 { var f: () => (string, i32) = () => ("abcd", 7); return 0; }`)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	rt, ok := prog.Funcs[0].ReturnType.(*ast.FuncType)
	if !ok || len(rt.Params) != 2 {
		t.Fatalf("mk's return type = %#v, want a 2-param *ast.FuncType", prog.Funcs[0].ReturnType)
	}
	v := prog.Funcs[1].Body.Stmts[0].(*ast.Var)
	ft, ok := v.Type.(*ast.FuncType)
	if !ok {
		t.Fatalf("f's annotation = %#v, want *ast.FuncType", v.Type)
	}
	if _, ok := ft.Result.(ast.TupleType); !ok {
		t.Errorf("f's result = %#v, want ast.TupleType", ft.Result)
	}
}

// The bare `(A) => B` spelling in return position is gone with the rule:
// `(i32)` is the grouped return type and the lambda's body then starts at
// `i32`, which no expression opens with.
func TestArrowLambdaBareFunctionReturnTypeRejected(t *testing.T) {
	_, err := Parse(`function main(): i32 { var g = (): (i32) => i32 => { return (n: i32) => n; }; return 0; }`)
	if err == nil {
		t.Fatal("bare function-type return on an arrow lambda parsed; it must be written grouped, ((i32) => i32)")
	}
	if !strings.Contains(err.Error(), `unexpected token "i32"`) {
		t.Errorf("error = %v, want it to name the i32 where the body starts", err)
	}
}
