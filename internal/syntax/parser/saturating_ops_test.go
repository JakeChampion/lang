package parser

import (
	"testing"

	"github.com/jakechampion/lang/internal/syntax/ast"
)

func exprOfReturn(t *testing.T, src string) ast.Expr {
	t.Helper()
	prog, err := Parse("function main(): i32 { return " + src + "; }")
	if err != nil {
		t.Fatalf("parse %q: %v", src, err)
	}
	stmts := prog.Funcs[0].Body.Stmts
	ret, ok := stmts[len(stmts)-1].(*ast.Return)
	if !ok {
		t.Fatalf("parse %q: last statement is %T, want *ast.Return", src, stmts[len(stmts)-1])
	}
	return ret.Value
}

func shape(e ast.Expr) string {
	switch x := e.(type) {
	case *ast.Binary:
		return "(" + x.Op + " " + shape(x.Left) + " " + shape(x.Right) + ")"
	case *ast.Ident:
		return x.Name
	case *ast.NumberLit:
		if x.Value == 0 {
			return "0"
		}
		s := ""
		for v := x.Value; v > 0; v /= 10 {
			s = string(rune('0'+v%10)) + s
		}
		return s
	}
	return "?"
}

func TestSaturatingOperatorPrecedence(t *testing.T) {
	for _, tc := range []struct{ src, want string }{
		{"a +| b", "(+| a b)"},
		{"a -| b", "(-| a b)"},
		{"a *| b", "(*| a b)"},
		{"a +| b * c", "(+| a (* b c))"},
		{"a + b *| c", "(+ a (*| b c))"},
		{"a -| b -| c", "(-| (-| a b) c)"},
		{"a + b -| c", "(-| (+ a b) c)"},
		{"a *| b / c", "(/ (*| a b) c)"},
		{"a << b +| c", "(<< a (+| b c))"},
		{"a +| b | c", "(| (+| a b) c)"},
		{"a <<| b", "(<<| a b)"},
		{"a <<| b + c", "(<<| a (+ b c))"},
		{"a <<| b <<| c", "(<<| (<<| a b) c)"},
		{"a << b <<| c", "(<<| (<< a b) c)"},
		{"a <<| b & c", "(& (<<| a b) c)"},
		{"a +| b <<| c", "(<<| (+| a b) c)"},
	} {
		if got := shape(exprOfReturn(t, tc.src)); got != tc.want {
			t.Errorf("parse %q = %s, want %s", tc.src, got, tc.want)
		}
	}
}

func TestCompoundAssignStillLexesBeforeSaturating(t *testing.T) {
	if _, err := Parse(`function main(): i32 { let a: i32 = 1; a += 2; return a; }`); err != nil {
		t.Errorf("compound assign after adding +|: %v", err)
	}
	if _, err := Parse(`function main(): i32 { let a: i32 = 1; let b: i32 = 2; return a | b; }`); err != nil {
		t.Errorf("bitwise or after adding +|: %v", err)
	}
	if _, err := Parse(`function main(): i32 { let a: i32 = 1; a <<= 2; return a; }`); err != nil {
		t.Errorf("shift-assign after adding <<|: %v", err)
	}
	if got := shape(exprOfReturn(t, "a << b")); got != "(<< a b)" {
		t.Errorf("wrapping shift after adding <<| = %s, want (<< a b)", got)
	}
}
