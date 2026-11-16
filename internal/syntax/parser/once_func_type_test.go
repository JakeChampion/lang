package parser

import (
	"testing"

	"github.com/jakechampion/lang/internal/syntax/ast"
)

func TestOnceFuncTypeParses(t *testing.T) {
	ty := parseTypeOfParam(t, `function f(own g: once (own i32[]) => own string): void {}`)
	ft, ok := ty.(*ast.FuncType)
	if !ok {
		t.Fatalf("expected a function type, got %T", ty)
	}
	if !ft.Once || !ft.OwnAt(0) || !ft.ResultOwn {
		t.Fatalf("once %v, own %v, result own %v: want all true", ft.Once, ft.ParamOwn, ft.ResultOwn)
	}
	if got, want := ft.String(), "once (own i32[]) => own string"; got != want {
		t.Errorf("String() = %q, want %q", got, want)
	}
}

func TestOnceFuncTypeInsideOtherTypes(t *testing.T) {
	ty := parseTypeOfParam(t, `function f(g: Option[(once (i32) => i32)[]]): void {}`)
	if got, want := ty.String(), "Option[(once (i32) => i32)[]]"; got != want {
		t.Errorf("String() = %q, want %q", got, want)
	}
	plain := parseTypeOfParam(t, `function f(g: (i32) => i32): void {}`)
	if ast.Equal(plain, parseTypeOfParam(t, `function f(g: once (i32) => i32): void {}`)) {
		t.Error("a once function type equals the plain one")
	}
}

func TestOnceMarksOnlyAFunctionType(t *testing.T) {
	if _, err := Parse(`function f(g: once (i32)): void {}`); err == nil {
		t.Fatal("`once (i32)` parsed; want an error that names a function type")
	}
}

func TestOnceLambdaAndOnceName(t *testing.T) {
	prog, err := Parse(`function main(): i32 {
  let once = 1;
  let k = once (i: i32): i32 => i + once;
  return once;
}`)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	body := prog.Funcs[0].Body.Stmts
	first, ok := body[0].(*ast.Var)
	if !ok || first.Name != "once" {
		t.Fatalf("first statement = %#v, want the local named once", body[0])
	}
	second, ok := body[1].(*ast.Var)
	if !ok {
		t.Fatalf("second statement = %T, want a let", body[1])
	}
	lam, ok := second.Init.(*ast.Lambda)
	if !ok || !lam.Once {
		t.Fatalf("init = %#v, want a once lambda", second.Init)
	}
}
