package parser

import (
	"testing"

	"github.com/jakechampion/lang/internal/syntax/ast"
	"github.com/jakechampion/lang/internal/syntax/lexer"
)

// A top-level const name in an arm is a literal pattern that reads the
// const. Another bare name binds, and a const name with a payload is a
// variant.
func TestConstNameInArmIsLiteral(t *testing.T) {
	src := `const LIMIT: i32 = 3;
function f(v: i32): i32 {
  match (v) {
    LIMIT => { return 1; },
    n => { return n; }
  }
}
function g(v: Option[i32]): i32 {
  return match (v) { Some(LIMIT) => 1, Some(LIMIT2) => LIMIT2, None => 0 };
}
`
	prog, err := Parse(src)
	if err != nil {
		t.Fatal(err)
	}
	m := prog.Funcs[0].Body.Stmts[0].(*ast.Match)
	if id, ok := m.Arms[0].Literal.(*ast.Ident); !ok || id.Name != "LIMIT" || m.Arms[0].VariantName != "" {
		t.Errorf("arm 0: literal %#v, variant %q; want a read of LIMIT", m.Arms[0].Literal, m.Arms[0].VariantName)
	}
	if m.Arms[1].Literal != nil || m.Arms[1].VariantName != "n" || !m.Arms[1].BareName {
		t.Errorf("arm 1: literal %#v, variant %q; want the binder n", m.Arms[1].Literal, m.Arms[1].VariantName)
	}
	ret := prog.Funcs[1].Body.Stmts[0].(*ast.Return)
	arms := ret.Value.(*ast.MatchExpr).Arms
	if lit, ok := arms[0].Payloads[0].Literal.(*ast.Ident); !ok || lit.Name != "LIMIT" {
		t.Errorf("Some(LIMIT): payload %#v; want a read of LIMIT", arms[0].Payloads[0])
	}
	if len(arms[1].Bindings) != 1 || arms[1].Bindings[0] != "LIMIT2" {
		t.Errorf("Some(LIMIT2): bindings %v; want the binder LIMIT2", arms[1].Bindings)
	}
}

// A const inside a block is not a top-level const, so its name binds.
func TestConstNameScanIsTopLevel(t *testing.T) {
	names := scanConstNames(mustTokens(t, "const A: i32 = 1;\nimpl X { const B: i32 = 2; }\npub const C = 3;\n"))
	if !names["A"] || names["B"] || !names["C"] {
		t.Errorf("got %v, want A and C only", names)
	}
}

func mustTokens(t *testing.T, src string) []lexer.Token {
	t.Helper()
	toks, _, err := lexer.Tokenize(src)
	if err != nil {
		t.Fatal(err)
	}
	return toks
}
