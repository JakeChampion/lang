package parser

import (
	"testing"

	"github.com/jakechampion/lang/internal/syntax/ast"
	"github.com/jakechampion/lang/internal/syntax/printer"
)

func TestParseFullRangeSlice(t *testing.T) {
	prog, err := Parse(`function main(): i32 { let a: i32[] = [1, 2]; let s: [i32] = a[:]; return s.len(); }`)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	vd, ok := prog.Funcs[0].Body.Stmts[1].(*ast.Var)
	if !ok {
		t.Fatalf("second statement is %T, want *ast.Var", prog.Funcs[0].Body.Stmts[1])
	}
	sl, ok := vd.Init.(*ast.SliceExpr)
	if !ok {
		t.Fatalf("initialiser is %T, want *ast.SliceExpr", vd.Init)
	}
	if sl.Low != nil {
		t.Errorf("Low = %T, want nil (implicit 0)", sl.Low)
	}
	if sl.High != nil {
		t.Errorf("High = %T, want nil (implicit len)", sl.High)
	}
	if id, ok := sl.Source.(*ast.Ident); !ok || id.Name != "a" {
		t.Errorf("Source = %#v, want ident a", sl.Source)
	}
}

func TestFullRangeSliceRoundTrips(t *testing.T) {
	src := "function main(): i32 {\n  let a: i32[] = [1, 2];\n  let s: [i32] = a[:];\n  return s.len();\n}\n"
	prog, err := Parse(src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	got := printer.Format(prog)
	if got != src {
		t.Errorf("format round-trip:\n got %q\nwant %q", got, src)
	}
}
