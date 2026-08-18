package parser

import (
	"testing"

	"github.com/jakechampion/lang/internal/ast"
	"github.com/jakechampion/lang/internal/printer"
)

// `xs[:]` is the full-range view: both bounds are optional, an absent low is
// 0 and an absent high is the source's length. It used to error out as a
// reserved form, which left the caller of a `[T]`-taking function spelling
// `xs[0:xs.len()]` (#6798).
func TestParseFullRangeSlice(t *testing.T) {
	prog, err := Parse(`function main(): i32 { var a: i32[] = [1, 2]; var s: [i32] = a[:]; return s.len(); }`)
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

// Both printers already emitted `[:]` for a both-bounds-absent SliceExpr, so
// accepting the form closes the round-trip the formatter could not complete.
func TestFullRangeSliceRoundTrips(t *testing.T) {
	src := "function main(): i32 {\n  var a: i32[] = [1, 2];\n  var s: [i32] = a[:];\n  return s.len();\n}\n"
	prog, err := Parse(src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	got := printer.Format(prog)
	if got != src {
		t.Errorf("format round-trip:\n got %q\nwant %q", got, src)
	}
}
