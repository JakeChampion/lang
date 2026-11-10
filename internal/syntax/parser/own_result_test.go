package parser

import (
	"testing"

	"github.com/jakechampion/lang/internal/syntax/ast"
)

func TestParseOwnedResults(t *testing.T) {
	p, err := Parse(`function fold[T](own x: T, f: (own T) => own T): own T { return f(x); }
function main(): void { let f = (own x: string): own string => x; }`)
	if err != nil {
		t.Fatal(err)
	}
	f := p.Funcs[0]
	callback := f.Params[1].Type.(*ast.FuncType)
	if !f.ReturnOwn || !callback.ResultOwn || !callback.OwnAt(0) {
		t.Fatalf("lost owned result metadata: %#v, %#v", f, callback)
	}
	v := p.Funcs[1].Body.Stmts[0].(*ast.Var)
	if !v.Init.(*ast.Lambda).ReturnOwn {
		t.Fatal("lost lambda result mode")
	}
}
