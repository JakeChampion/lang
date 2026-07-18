package constfold

import (
	"testing"

	"github.com/jakechampion/lang/internal/ast"
	"github.com/jakechampion/lang/internal/parser"
)

// countIfs walks every statement list reachable from the program's
// function bodies and returns (assert-desugared ifs, other ifs).
func countIfs(prog *ast.Program) (asserts, plain int) {
	var walkStmt func(st ast.Stmt)
	walkBlock := func(b *ast.Block) {
		if b == nil {
			return
		}
		for _, st := range b.Stmts {
			walkStmt(st)
		}
	}
	walkStmt = func(st ast.Stmt) {
		switch x := st.(type) {
		case *ast.Block:
			walkBlock(x)
		case *ast.If:
			if x.IsAssert {
				asserts++
			} else {
				plain++
			}
			walkStmt(x.Then)
			if x.Else != nil {
				walkStmt(x.Else)
			}
		case *ast.While:
			walkStmt(x.Body)
		case *ast.Loop:
			walkStmt(x.Body)
		case *ast.For:
			walkStmt(x.Body)
		case *ast.ForEach:
			walkStmt(x.Body)
		case *ast.Match:
			for _, arm := range x.Arms {
				walkBlock(arm.Body)
			}
		case *ast.Var:
			if lam, ok := x.Init.(*ast.Lambda); ok {
				walkBlock(lam.Body)
			}
		}
	}
	for _, fn := range prog.Funcs {
		walkBlock(fn.Body)
	}
	return asserts, plain
}

// ElideAsserts drops every assert-desugared If — top-level and nested
// in control flow — while leaving ordinary ifs
// (including ones spelling a similar shape by hand) untouched.
func TestElideAsserts(t *testing.T) {
	prog, err := parser.Parse(`
function helper(n: i32): i32 {
    assert(n > 0);
    if (n > 10) {
        assert(n < 100, "range");
        return 10;
    }
    return n;
}
function main(): i32 {
    assert(true);
    while (helper(3) > 0) {
        assert(false);
        return 0;
    }
    if (!false) { return 1; }
    return 2;
}
`)
	if err != nil {
		t.Fatal(err)
	}
	asserts, plainBefore := countIfs(prog)
	if asserts != 4 {
		t.Fatalf("parsed program should carry 4 assert desugars, found %d", asserts)
	}
	ElideAsserts(prog)
	asserts, plainAfter := countIfs(prog)
	if asserts != 0 {
		t.Fatalf("ElideAsserts left %d assert desugars behind", asserts)
	}
	if plainAfter != plainBefore {
		t.Fatalf("elision changed non-assert ifs: %d before, %d after", plainBefore, plainAfter)
	}
	if plainAfter == 0 {
		t.Fatal("expected the hand-written ifs to survive")
	}
}
