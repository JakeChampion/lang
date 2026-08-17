package parser

import (
	"testing"

	"github.com/jakechampion/lang/internal/ast"
	"github.com/jakechampion/lang/internal/printer"
)

func varInit(t *testing.T, src string) ast.Expr {
	t.Helper()
	prog, err := Parse(src)
	if err != nil {
		t.Fatalf("parse %q: %v", src, err)
	}
	vd, ok := prog.Funcs[0].Body.Stmts[0].(*ast.Var)
	if !ok {
		t.Fatalf("first statement is %T, want *ast.Var", prog.Funcs[0].Body.Stmts[0])
	}
	return vd.Init
}

// `'x'` and `b'x'` parse to one node distinguished by IsByte, carrying
// the decoded value and the spelling the author used.
func TestParseCharAndByteLiterals(t *testing.T) {
	cases := []struct {
		expr   string
		value  int64
		isByte bool
	}{
		{`'x'`, 'x', false},
		{`'\n'`, '\n', false},
		{`'\u{1F600}'`, 0x1F600, false},
		{`b'['`, '[', true},
		{`b'\xFF'`, 0xFF, true},
	}
	for _, c := range cases {
		init := varInit(t, `function main(): i32 { var v = `+c.expr+`; return 0; }`)
		lit, ok := init.(*ast.CharLit)
		if !ok {
			t.Errorf("%s parsed to %T, want *ast.CharLit", c.expr, init)
			continue
		}
		if lit.Value != c.value {
			t.Errorf("%s: Value = %d, want %d", c.expr, lit.Value, c.value)
		}
		if lit.IsByte != c.isByte {
			t.Errorf("%s: IsByte = %v, want %v", c.expr, lit.IsByte, c.isByte)
		}
		if lit.Raw != c.expr {
			t.Errorf("%s: Raw = %q, want the source spelling", c.expr, lit.Raw)
		}
	}
}

// A literal pattern in match-arm position accepts both forms, so byte
// dispatch can be written `match (s[i]) { b'[' => …` rather than on
// decimal codes.
func TestParseCharLiteralMatchArm(t *testing.T) {
	prog, err := Parse(`function f(b: u8): i32 {
	    match (b) {
	        b'[' => { return 1; },
	        b'\n' => { return 2; },
	        _ => { return 0; }
	    }
	    return 0;
	}
	function main(): i32 { return 0; }`)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	m, ok := prog.Funcs[0].Body.Stmts[0].(*ast.Match)
	if !ok {
		t.Fatalf("first statement is %T, want *ast.Match", prog.Funcs[0].Body.Stmts[0])
	}
	for i, want := range []int64{'[', '\n'} {
		lit, ok := m.Arms[i].Literal.(*ast.CharLit)
		if !ok {
			t.Fatalf("arm %d literal is %T, want *ast.CharLit", i, m.Arms[i].Literal)
		}
		if lit.Value != want || !lit.IsByte {
			t.Errorf("arm %d = %d (isByte %v), want %d (isByte true)", i, lit.Value, lit.IsByte, want)
		}
	}
}

// The formatter re-emits the spelling rather than normalising it: an
// escape the author chose is part of what they wrote.
func TestFormatCharLiteralRoundTrip(t *testing.T) {
	src := "function main(): i32 {\n" +
		"  var a: char = 'x';\n" +
		"  var b: char = '\\u{1F600}';\n" +
		"  var c: u8 = b'\\x1B';\n" +
		"  var d: u8 = b'[';\n" +
		"  return 0;\n" +
		"}\n"
	prog, err := Parse(src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	got := printer.Format(prog)
	if got != src {
		t.Errorf("format round-trip:\n got %q\nwant %q", got, src)
	}
}
