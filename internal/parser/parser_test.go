package parser

import (
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/ast"
)

func TestEmptyFunction(t *testing.T) {
	prog, err := Parse("function f() {}")
	if err != nil {
		t.Fatal(err)
	}
	if len(prog.Funcs) != 1 || prog.Funcs[0].Name != "f" {
		t.Fatalf("unexpected program: %+v", prog)
	}
	if _, ok := prog.Funcs[0].ReturnType.(ast.VoidType); !ok {
		t.Errorf("default return type should be void, got %s", prog.Funcs[0].ReturnType)
	}
}

func TestPrecedence(t *testing.T) {
	// 1 + 2 * 3 should parse as 1 + (2 * 3)
	prog, err := Parse("function f(): number { return 1 + 2 * 3; }")
	if err != nil {
		t.Fatal(err)
	}
	ret := prog.Funcs[0].Body.Stmts[0].(*ast.Return)
	bin := ret.Value.(*ast.Binary)
	if bin.Op != "+" {
		t.Fatalf("outer op = %q, want +", bin.Op)
	}
	rhs, ok := bin.Right.(*ast.Binary)
	if !ok || rhs.Op != "*" {
		t.Fatalf("rhs = %v, want * binary", bin.Right)
	}
}

func TestArrayLitAndIndex(t *testing.T) {
	prog, err := Parse("function f(): number { var a: number[] = [1,2,3]; return a[1]; }")
	if err != nil {
		t.Fatal(err)
	}
	v := prog.Funcs[0].Body.Stmts[0].(*ast.Var)
	if _, ok := v.Init.(*ast.ArrayLit); !ok {
		t.Errorf("expected ArrayLit init, got %T", v.Init)
	}
	r := prog.Funcs[0].Body.Stmts[1].(*ast.Return)
	if _, ok := r.Value.(*ast.Index); !ok {
		t.Errorf("expected Index return, got %T", r.Value)
	}
}

func TestAssignToCallIsError(t *testing.T) {
	_, err := Parse("function f() { f() = 1; }")
	if err == nil {
		t.Fatal("expected parse error for assign-to-call")
	}
}

func TestIfElse(t *testing.T) {
	prog, err := Parse("function f(): number { if (true) { return 1; } else { return 2; } }")
	if err != nil {
		t.Fatal(err)
	}
	ifs := prog.Funcs[0].Body.Stmts[0].(*ast.If)
	if ifs.Else == nil {
		t.Fatal("expected else branch")
	}
}

// `for` produces a real For node (so `continue` can target the step).
func TestForProducesForNode(t *testing.T) {
	prog, err := Parse(`function f(): number {
		for (var i = 0; i < 3; i = i + 1) { i; }
		return 0;
	}`)
	if err != nil {
		t.Fatal(err)
	}
	loop, ok := prog.Funcs[0].Body.Stmts[0].(*ast.For)
	if !ok {
		t.Fatalf("expected For, got %T", prog.Funcs[0].Body.Stmts[0])
	}
	if _, ok := loop.Init.(*ast.Var); !ok {
		t.Errorf("Init should be Var, got %T", loop.Init)
	}
	if loop.Step == nil {
		t.Errorf("Step should be set")
	}
}

func TestForWithExprInit(t *testing.T) {
	prog, err := Parse(`function f(): number {
		var i = 0;
		for (i = 0; i < 3; i = i + 1) {}
		return 0;
	}`)
	if err != nil {
		t.Fatal(err)
	}
	_ = prog
}

func TestForEmptyInitAndStep(t *testing.T) {
	// `for (; cond; ) body` — no init, no step.
	prog, err := Parse(`function f(): number {
		var i = 0;
		for (; i < 3 ;) { i = i + 1; }
		return i;
	}`)
	if err != nil {
		t.Fatal(err)
	}
	_ = prog
}

// The parser should keep going after a per-statement error so a single
// run reports every problem, not just the first.
func TestRecoversAndReportsMultiplePerStatement(t *testing.T) {
	src := `function f(): number {
		var x = ;
		var y = 1 +;
		return 0;
	}`
	prog, err := Parse(src)
	if err == nil {
		t.Fatal("expected errors")
	}
	if strings.Count(err.Error(), "parse error") < 2 {
		t.Errorf("expected at least 2 parse errors, got:\n%s", err.Error())
	}
	if prog == nil || len(prog.Funcs) != 1 {
		t.Errorf("expected 1 partial function, got %v", prog)
	}
}

// A junk top-level declaration shouldn't stop later, valid functions
// from being parsed.
func TestRecoversAtTopLevel(t *testing.T) {
	src := `garbage tokens here;
		function good(): number { return 42; }`
	prog, err := Parse(src)
	if err == nil {
		t.Fatal("expected errors")
	}
	if prog == nil || len(prog.Funcs) != 1 || prog.Funcs[0].Name != "good" {
		t.Errorf("expected `good` to still be parsed, got %v", prog)
	}
}
