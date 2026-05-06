package optimizer

import (
	"testing"

	"github.com/jakechampion/lang/internal/ast"
	"github.com/jakechampion/lang/internal/parser"
)

func parseAndFold(t *testing.T, src string) *ast.Program {
	t.Helper()
	prog, err := parser.Parse(src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	Optimize(prog)
	return prog
}

func TestFoldsAddition(t *testing.T) {
	prog := parseAndFold(t, `function f(): number { return 1 + 2; }`)
	ret := prog.Funcs[0].Body.Stmts[0].(*ast.Return)
	if n, ok := ret.Value.(*ast.NumberLit); !ok || n.Value != 3 {
		t.Errorf("expected NumberLit 3, got %T %v", ret.Value, ret.Value)
	}
}

func TestFoldsNestedArithmetic(t *testing.T) {
	prog := parseAndFold(t, `function f(): number { return 1 + 2 * 3 - 4 / 2; }`)
	ret := prog.Funcs[0].Body.Stmts[0].(*ast.Return)
	n, ok := ret.Value.(*ast.NumberLit)
	if !ok || n.Value != 5 {
		t.Errorf("expected 5, got %v", ret.Value)
	}
}

func TestFoldsBoolAnd(t *testing.T) {
	prog := parseAndFold(t, `function f(): boolean { return true && false; }`)
	ret := prog.Funcs[0].Body.Stmts[0].(*ast.Return)
	bl, ok := ret.Value.(*ast.BoolLit)
	if !ok || bl.Value {
		t.Errorf("expected false, got %v", ret.Value)
	}
}

func TestFoldsComparison(t *testing.T) {
	prog := parseAndFold(t, `function f(): boolean { return 5 < 10; }`)
	ret := prog.Funcs[0].Body.Stmts[0].(*ast.Return)
	bl, ok := ret.Value.(*ast.BoolLit)
	if !ok || !bl.Value {
		t.Errorf("expected true, got %v", ret.Value)
	}
}

func TestFoldsUnaryNegation(t *testing.T) {
	prog := parseAndFold(t, `function f(): number { return -7; }`)
	ret := prog.Funcs[0].Body.Stmts[0].(*ast.Return)
	if n, ok := ret.Value.(*ast.NumberLit); !ok || n.Value != -7 {
		t.Errorf("expected -7, got %v", ret.Value)
	}
}

func TestFoldsShortCircuitAndWithFalse(t *testing.T) {
	// `false && expensive()` collapses to `false` regardless of right.
	prog := parseAndFold(t, `function expensive(): boolean { return true; }
function f(): boolean { return false && expensive(); }`)
	ret := prog.Funcs[1].Body.Stmts[0].(*ast.Return)
	bl, ok := ret.Value.(*ast.BoolLit)
	if !ok || bl.Value {
		t.Errorf("expected false, got %v", ret.Value)
	}
}

func TestIfTrueCollapsesToThen(t *testing.T) {
	prog := parseAndFold(t, `function f(): number {
		if (true) { return 1; } else { return 2; }
	}`)
	// The whole if becomes the then-block (which itself becomes a Block).
	if _, ok := prog.Funcs[0].Body.Stmts[0].(*ast.Block); !ok {
		t.Errorf("expected Block, got %T", prog.Funcs[0].Body.Stmts[0])
	}
}

func TestUnreachableAfterReturnIsDropped(t *testing.T) {
	prog := parseAndFold(t, `function f(): number {
		return 1;
		var dead = 99;
		return 2;
	}`)
	stmts := prog.Funcs[0].Body.Stmts
	if len(stmts) != 1 {
		t.Errorf("expected 1 stmt after dead-code removal, got %d: %#v", len(stmts), stmts)
	}
}

func TestUnreachableAfterBreakIsDropped(t *testing.T) {
	prog := parseAndFold(t, `function f(): number {
		while (true) { break; var dead = 1; }
		return 0;
	}`)
	w := prog.Funcs[0].Body.Stmts[0].(*ast.While)
	body := w.Body.(*ast.Block)
	if len(body.Stmts) != 1 {
		t.Errorf("expected 1 stmt in loop body, got %d", len(body.Stmts))
	}
}

func TestFoldsStringConcat(t *testing.T) {
	prog := parseAndFold(t, `function f(): string { return "foo" + "bar"; }`)
	// Without checker, IsStringConcat is false — fold won't kick in.
	// Set it manually here to mimic the post-checker AST.
	ret := prog.Funcs[0].Body.Stmts[0].(*ast.Return)
	bin, ok := ret.Value.(*ast.Binary)
	if !ok {
		t.Fatalf("expected Binary, got %T", ret.Value)
	}
	bin.IsStringConcat = true
	Optimize(prog)
	ret = prog.Funcs[0].Body.Stmts[0].(*ast.Return)
	if s, ok := ret.Value.(*ast.StringLit); !ok || s.Value != "foobar" {
		t.Errorf(`expected StringLit "foobar", got %T %v`, ret.Value, ret.Value)
	}
}

func TestFoldsBitwiseAndShift(t *testing.T) {
	prog := parseAndFold(t, `function f(): number { return (1 << 4) | (12 & 10); }`)
	ret := prog.Funcs[0].Body.Stmts[0].(*ast.Return)
	if n, ok := ret.Value.(*ast.NumberLit); !ok || n.Value != 24 {
		t.Errorf("expected 24, got %v", ret.Value)
	}
}
