package parser

import (
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/syntax/ast"
)

func TestLetElseFullInitializer(t *testing.T) {
	for _, source := range []string{
		`function main(): i32 { let Some(n) = { Some(3) } else { return 1; }; return n; }`,
		`function main(): i32 { let 3 = { 3 } else { return 1; }; return 0; }`,
		`struct Box { n: i32 } function (b: Box) get(): Option[i32] { return Some(b.n); }
function main(): i32 { let Some(n) = Box { n: 3 }.get() else { return 1; }; return n; }`,
	} {
		if _, err := Parse(source); err != nil {
			t.Fatalf("initializer: %v\n%s", err, source)
		}
	}
}

func TestLetElseTupleOfPatterns(t *testing.T) {
	for _, source := range []string{
		`function f(x: Option[i32], y: Option[i32]): i32 { let (Some(a), Some(b)) = (x, y) else { return 0; }; return a + b; }`,
		`function f(t: (i32, i32)): i32 { let (1, b) = t else { return 0; }; return b; }`,
		`function f(x: Option[i32], y: Option[i32]): i32 { let w @ (Some(a), _) = (x, y) else { return 0; }; return a; }`,
		`function f(x: Option[i32], y: Option[i32]): i32 { let (Some(a), _) | (_, Some(a)) = (x, y) else { return 0; }; return a; }`,
	} {
		if _, err := Parse(source); err != nil {
			t.Fatalf("let-else tuple: %v\n%s", err, source)
		}
	}
	for source, kind := range map[string]string{
		`function f(): i32 { let (Some(a), b) = (Some(1), 2); return a + b; }`: "a variant tuple element can fail to match",
		`function f(): i32 { let (1, b) = (1, 2); return b; }`:                 "a literal tuple element can fail to match",
	} {
		_, err := Parse(source)
		if err == nil || !strings.Contains(err.Error(), kind) {
			t.Fatalf("no else: got %v, want %q\n%s", err, kind, source)
		}
	}
}

// The rest of a value branch after a let-else keeps its tail as the value: the
// branch ends in a value let-else match whose success arm holds the tail.
func TestLetElseValueBranchTail(t *testing.T) {
	prog, err := Parse(`function g(t: Option[i32]): i32 { let r = { let Some(s) = t else { return 0; }; s + 1 }; return r; }`)
	if err != nil {
		t.Fatal(err)
	}
	fn := prog.Funcs[0]
	init := fn.Body.Stmts[0].(*ast.Var).Init
	block, ok := init.(*ast.BlockExpr)
	if !ok || len(block.Stmts) != 0 {
		t.Fatalf("initializer: got %T, want a value block with no statements", init)
	}
	m, ok := block.Tail.(*ast.MatchExpr)
	if !ok || m.Origin != ast.OriginLetElse || len(m.Arms) != 2 {
		t.Fatalf("tail: got %T, want a value let-else match", block.Tail)
	}
	rest, ok := m.Arms[0].Body.(*ast.BlockExpr)
	if !ok || rest.Tail == nil {
		t.Fatalf("success arm: got %T, want a block that ends in the tail", m.Arms[0].Body)
	}
	if sum, ok := rest.Tail.(*ast.Binary); !ok || sum.Op != "+" {
		t.Fatalf("success arm tail: got %T, want s + 1", rest.Tail)
	}
	stmt, err := Parse(`function f(t: Option[i32]): void { let Some(s) = t else { return; }; print(s); }`)
	if err != nil {
		t.Fatal(err)
	}
	sm, ok := stmt.Funcs[0].Body.Stmts[0].(*ast.Match)
	if !ok || sm.Origin != ast.OriginLetElse || len(sm.Arms[0].Body.Stmts) != 1 {
		t.Fatalf("statement let-else: got %T, want a let-else match that keeps its rest", stmt.Funcs[0].Body.Stmts[0])
	}
}
