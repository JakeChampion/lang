package parser

import (
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
