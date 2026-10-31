package parser

import (
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/syntax/ast"
)

func TestStatementBlockTailExpression(t *testing.T) {
	prog, err := Parse(`function main(): i32 {
  if (true) { print("a") } else { print("b") };
  while (false) { print("w") }
  return 0;
}`)
	if err != nil {
		t.Fatal(err)
	}
	body := prog.Funcs[0].Body.Stmts
	if len(body) != 3 {
		t.Fatalf("got %d statements, want 3 (the `;` after the if is not a statement)", len(body))
	}
	iff, ok := body[0].(*ast.If)
	if !ok {
		t.Fatalf("first statement is %T, want *ast.If", body[0])
	}
	then, ok := iff.Then.(*ast.Block)
	if !ok || len(then.Stmts) != 1 {
		t.Fatalf("then branch is %T, want a block of one statement", iff.Then)
	}
	if _, ok := then.Stmts[0].(*ast.ExprStmt); !ok {
		t.Errorf("then branch holds %T, want *ast.ExprStmt", then.Stmts[0])
	}

	for _, src := range []string{
		"function main(): i32 { let r: i32 = 0; { r = 2 } return r; }",
		"function main(): i32 { { let z: i32 = 1 } return 0; }",
		"function main(): i32 { print(\"a\") print(\"b\"); return 0; }",
	} {
		if _, err := Parse(src); err == nil || !strings.Contains(err.Error(), `expected ";"`) {
			t.Errorf("%s: err = %v, want a missing \";\"", src, err)
		}
	}
}
