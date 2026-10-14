package parser

import (
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/syntax/ast"
)

// A block in a value block's tail position is the enclosing block's value
// (#12388): `{ …; { 1 } }` yields the inner block, as the grammar's ValueBlock
// allows. A nested block that yields nothing, or that more statements follow,
// stays a statement block.
func TestNestedBlockTail(t *testing.T) {
	thenOf := func(t *testing.T, body string) ast.Expr {
		t.Helper()
		prog, err := Parse(`function f(b: boolean): i32 { return if (b) ` + body + ` else { 2 }; }`)
		if err != nil {
			t.Fatalf("parse %s: %v", body, err)
		}
		return prog.Funcs[0].Body.Stmts[0].(*ast.Return).Value.(*ast.IfExpr).Then
	}

	t.Run("a lone nested block is its tail", func(t *testing.T) {
		if _, ok := thenOf(t, `{ { 1 } }`).(*ast.NumberLit); !ok {
			t.Errorf("`{ { 1 } }` should pass through to the bare NumberLit")
		}
	})

	t.Run("statements then a nested block", func(t *testing.T) {
		be, ok := thenOf(t, `{ let k = 1; { let m = k; m + 1 } }`).(*ast.BlockExpr)
		if !ok {
			t.Fatal("the branch should be a BlockExpr")
		}
		if len(be.Stmts) != 1 {
			t.Fatalf("want the one leading let, got %d statements", len(be.Stmts))
		}
		inner, ok := be.Tail.(*ast.BlockExpr)
		if !ok || inner.Tail == nil {
			t.Fatalf("the tail should be the inner block with its own tail, got %T", be.Tail)
		}
	})

	t.Run("a nested block more statements follow stays a statement", func(t *testing.T) {
		be, ok := thenOf(t, `{ { let t = 1; } 5 }`).(*ast.BlockExpr)
		if !ok {
			t.Fatal("the branch should be a BlockExpr")
		}
		if _, ok := be.Stmts[0].(*ast.Block); !ok || len(be.Stmts) != 1 {
			t.Fatalf("want one statement Block, got %#v", be.Stmts)
		}
		if _, ok := be.Tail.(*ast.NumberLit); !ok {
			t.Errorf("the tail should be the 5, got %T", be.Tail)
		}
	})

	t.Run("a nested block with no value stays a statement", func(t *testing.T) {
		be, ok := thenOf(t, `{ { let t = 1; } }`).(*ast.BlockExpr)
		if !ok {
			t.Fatal("the branch should be a BlockExpr")
		}
		if be.Tail != nil {
			t.Errorf("a value-less inner block gives the branch no tail, got %T", be.Tail)
		}
		if _, ok := be.Stmts[0].(*ast.Block); !ok {
			t.Errorf("the inner block should stay a statement Block, got %T", be.Stmts[0])
		}
	})

	t.Run("a lambda body's nested block is its value", func(t *testing.T) {
		prog, err := Parse(`function main(): i32 { let f = (n: i32): i32 => { { n + 1 } }; return f(1); }`)
		if err != nil {
			t.Fatalf("parse: %v", err)
		}
		var l *ast.Lambda
		ast.WalkProgram(prog, func(n ast.Node) bool {
			if x, ok := n.(*ast.Lambda); ok && l == nil {
				l = x
			}
			return true
		})
		if l == nil || len(l.Body.Stmts) != 1 {
			t.Fatalf("want a lambda whose body is one return, got %#v", l)
		}
		ret, ok := l.Body.Stmts[0].(*ast.Return)
		if !ok {
			t.Fatalf("the body should return the nested block's value, got %T", l.Body.Stmts[0])
		}
		if _, ok := ret.Value.(*ast.Binary); !ok {
			t.Errorf("the returned value should be the bare `n + 1`, got %T", ret.Value)
		}
	})

	t.Run("a deep chain of nested blocks is read once per level", func(t *testing.T) {
		const n = 64
		v := thenOf(t, strings.Repeat("{ ", n)+"7"+strings.Repeat(" }", n))
		if _, ok := v.(*ast.NumberLit); !ok {
			t.Errorf("%d nested blocks around 7 should pass through to the NumberLit, got %T", n, v)
		}
	})
}
