package parser

import (
	"testing"

	"github.com/jakechampion/lang/internal/syntax/ast"
)

func TestValuelessBranchTail(t *testing.T) {
	lastOf := func(t *testing.T, lambda string) ast.Stmt {
		t.Helper()
		prog, err := Parse(`function main(): i32 { let f = ` + lambda + `; return 0; }`)
		if err != nil {
			t.Fatalf("parse %s: %v", lambda, err)
		}
		var l *ast.Lambda
		ast.WalkProgram(prog, func(n ast.Node) bool {
			if x, ok := n.(*ast.Lambda); ok && l == nil {
				l = x
			}
			return true
		})
		if l == nil || len(l.Body.Stmts) == 0 {
			t.Fatalf("want a lambda with a body, got %#v", l)
		}
		return l.Body.Stmts[len(l.Body.Stmts)-1]
	}
	returned := func(t *testing.T, s ast.Stmt) ast.Expr {
		t.Helper()
		r, ok := s.(*ast.Return)
		if !ok {
			t.Fatalf("want the construct returned as the body's value, got %T", s)
		}
		return r.Value
	}

	t.Run("a match whose arms run statements", func(t *testing.T) {
		s := lastOf(t, `(n: i32): void => { match (n) { 0 => { print("a"); }, _ => { print("b"); } } }`)
		if _, ok := s.(*ast.Match); !ok {
			t.Errorf("want a match statement, got %T", s)
		}
	})

	t.Run("a match with a statement arm and a returning arm", func(t *testing.T) {
		s := lastOf(t, `(n: i32): void => { match (n) { 0 => { print("a"); }, _ => { return; } } }`)
		if _, ok := s.(*ast.Match); !ok {
			t.Errorf("want a match statement, got %T", s)
		}
	})

	t.Run("an if-else whose branches run statements", func(t *testing.T) {
		s := lastOf(t, `(n: i32): void => { if (n > 0) { print("a"); } else { print("b"); } }`)
		if _, ok := s.(*ast.If); !ok {
			t.Errorf("want an if statement, got %T", s)
		}
	})

	t.Run("a match whose arms give values stays the value", func(t *testing.T) {
		if _, ok := returned(t, lastOf(t, `(n: i32): i32 => { match (n) { 0 => { 1 }, _ => { 2 } } }`)).(*ast.MatchExpr); !ok {
			t.Error("want a returned match expression")
		}
	})

	t.Run("a match with bare arms stays the value", func(t *testing.T) {
		if _, ok := returned(t, lastOf(t, `(n: i32): i32 => { match (n) { 0 => 1, _ => { print("b"); } } }`)).(*ast.MatchExpr); !ok {
			t.Error("want a returned match expression")
		}
	})

	t.Run("an arm with a value and an arm without stays the value", func(t *testing.T) {
		if _, ok := returned(t, lastOf(t, `(n: i32): i32 => { match (n) { 0 => { 1 }, _ => { print("b"); } } }`)).(*ast.MatchExpr); !ok {
			t.Error("want a returned match expression, so the checker reports E061")
		}
	})

	t.Run("an if-else whose branches give values stays the value", func(t *testing.T) {
		if _, ok := returned(t, lastOf(t, `(n: i32): i32 => { if (n > 0) { 1 } else { 2 } }`)).(*ast.IfExpr); !ok {
			t.Error("want a returned if expression")
		}
	})

	t.Run("a lambda in a guard does not count as an arm", func(t *testing.T) {
		s := lastOf(t, `(n: i32): void => { match (n) { k when ((x: i32): boolean => x > 0)(k) => { print("a"); }, _ => { print("b"); } } }`)
		if _, ok := s.(*ast.Match); !ok {
			t.Errorf("want a match statement, got %T", s)
		}
	})
}
