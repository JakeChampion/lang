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

func TestFunctionTypeAnnotation(t *testing.T) {
	prog, err := Parse(`function apply(f: (number, number) => number, a: number, b: number): number {
		return f(a, b);
	}`)
	if err != nil {
		t.Fatal(err)
	}
	param := prog.Funcs[0].Params[0]
	ft, ok := param.Type.(*ast.FuncType)
	if !ok {
		t.Fatalf("expected *FuncType, got %T", param.Type)
	}
	if len(ft.Params) != 2 || !ast.Equal(ft.Result, ast.NumberType{}) {
		t.Errorf("unexpected signature: %s", ft)
	}
}

func TestNullaryFunctionType(t *testing.T) {
	prog, err := Parse(`function call(f: () => number): number { return f(); }`)
	if err != nil {
		t.Fatal(err)
	}
	ft := prog.Funcs[0].Params[0].Type.(*ast.FuncType)
	if len(ft.Params) != 0 {
		t.Errorf("expected 0 params, got %d", len(ft.Params))
	}
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

func TestFloatLiteralAndType(t *testing.T) {
	prog, err := Parse(`function f(x: float): float { return x + 1.5; }`)
	if err != nil {
		t.Fatal(err)
	}
	fn := prog.Funcs[0]
	if _, ok := fn.ReturnType.(ast.FloatType); !ok {
		t.Errorf("return type = %T, want FloatType", fn.ReturnType)
	}
	if _, ok := fn.Params[0].Type.(ast.FloatType); !ok {
		t.Errorf("param type = %T, want FloatType", fn.Params[0].Type)
	}
	bin := fn.Body.Stmts[0].(*ast.Return).Value.(*ast.Binary)
	lit, ok := bin.Right.(*ast.FloatLit)
	if !ok {
		t.Fatalf("rhs = %T, want *FloatLit", bin.Right)
	}
	if lit.Value != 1.5 {
		t.Errorf("value = %v, want 1.5", lit.Value)
	}
}

func TestSwitchParsesBasic(t *testing.T) {
	prog, err := Parse(`function f(n: number): number {
		switch (n) {
			case 1, 2: return 10;
			case 3: return 30;
			default: return 0;
		}
	}`)
	if err != nil {
		t.Fatal(err)
	}
	sw := prog.Funcs[0].Body.Stmts[0].(*ast.Switch)
	if len(sw.Cases) != 2 {
		t.Fatalf("got %d cases, want 2", len(sw.Cases))
	}
	if len(sw.Cases[0].Values) != 2 {
		t.Errorf("first case has %d values, want 2", len(sw.Cases[0].Values))
	}
	if sw.Default == nil {
		t.Errorf("default block missing")
	}
}

func TestSwitchRejectsDuplicateDefault(t *testing.T) {
	_, err := Parse(`function f(n: number): number {
		switch (n) { default: return 0; default: return 1; }
	}`)
	if err == nil {
		t.Error("expected error on duplicate default")
	}
}

func TestCompoundAssignDesugars(t *testing.T) {
	prog, err := Parse(`function f(): number { var x: number = 1; x += 2; return x; }`)
	if err != nil {
		t.Fatal(err)
	}
	stmt := prog.Funcs[0].Body.Stmts[1].(*ast.ExprStmt)
	a, ok := stmt.Expr.(*ast.Assign)
	if !ok {
		t.Fatalf("compound `+=` should desugar to *Assign, got %T", stmt.Expr)
	}
	bin, ok := a.Value.(*ast.Binary)
	if !ok || bin.Op != "+" {
		t.Fatalf("RHS should be `+` Binary, got %v", a.Value)
	}
	if id, ok := bin.Left.(*ast.Ident); !ok || id.Name != "x" {
		t.Errorf("desugared LHS should reuse the target `x`, got %v", bin.Left)
	}
}

func TestTernary(t *testing.T) {
	prog, err := Parse(`function f(b: boolean): number { return b ? 1 : 2; }`)
	if err != nil {
		t.Fatal(err)
	}
	ret := prog.Funcs[0].Body.Stmts[0].(*ast.Return)
	tern, ok := ret.Value.(*ast.Ternary)
	if !ok {
		t.Fatalf("expected *Ternary, got %T", ret.Value)
	}
	if _, ok := tern.Cond.(*ast.Ident); !ok {
		t.Errorf("cond should be Ident, got %T", tern.Cond)
	}
}

func TestTernaryRightAssociative(t *testing.T) {
	// `a ? b : c ? d : e` parses as `a ? b : (c ? d : e)`.
	prog, err := Parse(`function f(a: boolean, c: boolean): number {
		return a ? 1 : c ? 2 : 3;
	}`)
	if err != nil {
		t.Fatal(err)
	}
	tern := prog.Funcs[0].Body.Stmts[0].(*ast.Return).Value.(*ast.Ternary)
	if _, ok := tern.Else.(*ast.Ternary); !ok {
		t.Fatalf("else should be a nested Ternary, got %T", tern.Else)
	}
}

func TestStructDecl(t *testing.T) {
	prog, err := Parse(`struct Point { x: number, y: number }`)
	if err != nil {
		t.Fatal(err)
	}
	if len(prog.Structs) != 1 {
		t.Fatalf("got %d structs, want 1", len(prog.Structs))
	}
	sd := prog.Structs[0]
	if sd.Name != "Point" || len(sd.Fields) != 2 {
		t.Errorf("unexpected struct: %+v", sd)
	}
}

func TestStructLitAndFieldAccess(t *testing.T) {
	prog, err := Parse(`struct P { x: number }
		function main(): number {
			var p: P = P { x: 5 };
			return p.x;
		}`)
	if err != nil {
		t.Fatal(err)
	}
	main := prog.Funcs[0]
	v := main.Body.Stmts[0].(*ast.Var)
	if _, ok := v.Init.(*ast.StructLit); !ok {
		t.Errorf("init should be StructLit, got %T", v.Init)
	}
	ret := main.Body.Stmts[1].(*ast.Return)
	if _, ok := ret.Value.(*ast.FieldAccess); !ok {
		t.Errorf("return should be FieldAccess, got %T", ret.Value)
	}
}
