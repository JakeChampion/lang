package parser

import (
	"testing"

	"github.com/jakechampion/lang/internal/ast"
)

// exprOfReturn parses `function main(): i32 { return <src>; }` and hands back
// the returned expression.
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

// shape renders a binary expression tree as `(op left right)` so precedence
// and associativity are asserted structurally rather than by evaluation.
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

// The saturating operators (#5542) sit in the existing arithmetic tiers:
// `+|` / `-|` with `+` / `-`, `*|` with `*` / `/` / `%`. They lex as single
// two-character punctuators, so `a +| b` is one operator rather than `a + (|b)`
// — and `+=` still wins over a `+`-prefixed match.
func TestSaturatingOperatorPrecedence(t *testing.T) {
	for _, tc := range []struct{ src, want string }{
		{"a +| b", "(+| a b)"},
		{"a -| b", "(-| a b)"},
		{"a *| b", "(*| a b)"},
		// `*` binds tighter than `+|`.
		{"a +| b * c", "(+| a (* b c))"},
		// `*|` binds tighter than `+`.
		{"a + b *| c", "(+ a (*| b c))"},
		// Same tier, left-associative.
		{"a -| b -| c", "(-| (-| a b) c)"},
		{"a + b -| c", "(-| (+ a b) c)"},
		{"a *| b / c", "(/ (*| a b) c)"},
		// Shifts are looser than the additive tier.
		{"a << b +| c", "(<< a (+| b c))"},
		// Bitwise-or is looser still, so `a +| b | c` groups as `(a +| b) | c`.
		{"a +| b | c", "(| (+| a b) c)"},
	} {
		if got := shape(exprOfReturn(t, tc.src)); got != tc.want {
			t.Errorf("parse %q = %s, want %s", tc.src, got, tc.want)
		}
	}
}

// TestCompoundAssignStillLexesBeforeSaturating pins that adding `+|` to the
// multi-character punctuator table did not shadow `+=`.
func TestCompoundAssignStillLexesBeforeSaturating(t *testing.T) {
	if _, err := Parse(`function main(): i32 { var a: i32 = 1; a += 2; return a; }`); err != nil {
		t.Errorf("compound assign after adding +|: %v", err)
	}
	if _, err := Parse(`function main(): i32 { var a: i32 = 1; var b: i32 = 2; return a | b; }`); err != nil {
		t.Errorf("bitwise or after adding +|: %v", err)
	}
}
