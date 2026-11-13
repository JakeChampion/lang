package parser

import (
	"testing"

	"github.com/jakechampion/lang/internal/syntax/ast"
)

// An unbraced statement arm that starts with `if` or `match` is that
// statement, the same as the braced arm, and not an expression statement.
func TestUnbracedArmIfOrMatchIsAStatement(t *testing.T) {
	src := `function f(n: i32, raw: boolean): void {
  match (n) {
    1 => if (raw) { print(1); } else { print(2); },
    2 => match (raw) { true => print(3), false => print(4) },
    _ => print(5),
  }
}
`
	prog, err := Parse(src)
	if err != nil {
		t.Fatal(err)
	}
	m := prog.Funcs[0].Body.Stmts[0].(*ast.Match)
	if _, ok := m.Arms[0].Body.Stmts[0].(*ast.If); !ok {
		t.Errorf("arm 1: %T, want *ast.If", m.Arms[0].Body.Stmts[0])
	}
	if _, ok := m.Arms[1].Body.Stmts[0].(*ast.Match); !ok {
		t.Errorf("arm 2: %T, want *ast.Match", m.Arms[1].Body.Stmts[0])
	}
	if _, ok := m.Arms[2].Body.Stmts[0].(*ast.ExprStmt); !ok {
		t.Errorf("arm 3: %T, want *ast.ExprStmt", m.Arms[2].Body.Stmts[0])
	}
}
