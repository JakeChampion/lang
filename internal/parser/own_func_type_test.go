package parser

import (
	"testing"

	"github.com/jakechampion/lang/internal/ast"
)

func parseTypeOfParam(t *testing.T, src string) ast.Type {
	t.Helper()
	prog, err := Parse(src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(prog.Funcs) == 0 || len(prog.Funcs[0].Params) == 0 {
		t.Fatalf("no parameter to read")
	}
	return prog.Funcs[0].Params[0].Type
}

// `own` inside a function type marks a consuming slot, and it is part of the
// type's spelling — so a consuming function value cannot be handed to a
// lending position.
func TestFuncTypeParsesOwnParam(t *testing.T) {
	ty := parseTypeOfParam(t, `function f(g: (own i32[], string) => i32): void {}`)
	ft, ok := ty.(*ast.FuncType)
	if !ok {
		t.Fatalf("expected a function type, got %T", ty)
	}
	if !ft.OwnAt(0) || ft.OwnAt(1) {
		t.Fatalf("own flags = %v, want [true false]", ft.ParamOwn)
	}
	if got, want := ft.String(), "(own i32[], string) => i32"; got != want {
		t.Errorf("String() = %q, want %q", got, want)
	}
}

func TestFuncTypeWithoutOwnHasNoFlags(t *testing.T) {
	ty := parseTypeOfParam(t, `function f(g: (i32[]) => i32): void {}`)
	ft := ty.(*ast.FuncType)
	if ft.AnyOwn() || ft.ParamOwn != nil {
		t.Fatalf("own flags = %v, want none", ft.ParamOwn)
	}
}

// `own R` is also a resource-HANDLE type. The consuming marker is only read
// where a `=>` follows the matching `)`, so a grouped or tuple type keeps the
// handle meaning it had.
func TestOwnHandleTypeInParensUnchanged(t *testing.T) {
	ty := parseTypeOfParam(t, `resource Conn;
function f(h: (own Conn)): void {}`)
	if _, ok := ty.(ast.HandleType); !ok {
		t.Fatalf("expected a handle type, got %T (%v)", ty, ty)
	}
}

// A lambda spells a consuming parameter the way a declaration does.
func TestArrowLambdaParsesOwnParam(t *testing.T) {
	prog, err := Parse(`function f(): i32 {
    var g: (own i32[]) => i32 = (own xs: i32[]) => xs.len();
    return g([1]);
}`)
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
		t.Fatal("no lambda parsed")
	}
	if len(lam.Params) != 1 || !lam.Params[0].Own {
		t.Fatalf("lambda params = %+v, want one own param", lam.Params)
	}
}
