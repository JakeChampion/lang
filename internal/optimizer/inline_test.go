package optimizer

import (
	"testing"

	"github.com/jakechampion/lang/internal/ast"
	"github.com/jakechampion/lang/internal/parser"
)

func TestInlineSingleReturnFunction(t *testing.T) {
	prog, err := parser.Parse(`
		function dbl(x: number): number { return x * 2; }
		function main(): number { return dbl(7); }`)
	if err != nil {
		t.Fatal(err)
	}
	Optimize(prog)
	main := prog.Funcs[1]
	ret := main.Body.Stmts[0].(*ast.Return)
	// Inlining substitutes the body, leaving `7 * 2`. The IR fold
	// pass collapses it to 14 later — at the AST level we only see
	// the substituted Binary.
	bin, ok := ret.Value.(*ast.Binary)
	if !ok || bin.Op != "*" {
		t.Fatalf("expected `7 * 2` Binary, got %T %v", ret.Value, ret.Value)
	}
	left, ok := bin.Left.(*ast.NumberLit)
	if !ok || left.Value != 7 {
		t.Errorf("Left should be NumberLit 7, got %#v", bin.Left)
	}
	right, ok := bin.Right.(*ast.NumberLit)
	if !ok || right.Value != 2 {
		t.Errorf("Right should be NumberLit 2, got %#v", bin.Right)
	}
}

func TestInlineDoesNotTouchRecursiveFunction(t *testing.T) {
	prog, err := parser.Parse(`function fact(n: number): number {
		if (n == 0) { return 1; }
		return n * fact(n - 1);
	}`)
	if err != nil {
		t.Fatal(err)
	}
	Optimize(prog)
	// fact has more than one statement (the if + the recursive return),
	// so it isn't eligible. Body must still hold both statements.
	if len(prog.Funcs[0].Body.Stmts) != 2 {
		t.Errorf("recursive function body should still have 2 stmts, got %d",
			len(prog.Funcs[0].Body.Stmts))
	}
}

func TestInlineSubstitutesIdentArgs(t *testing.T) {
	prog, err := parser.Parse(`
		function add(a: number, b: number): number { return a + b; }
		function main(x: number, y: number): number { return add(x, y); }`)
	if err != nil {
		t.Fatal(err)
	}
	Optimize(prog)
	ret := prog.Funcs[1].Body.Stmts[0].(*ast.Return)
	bin, ok := ret.Value.(*ast.Binary)
	if !ok || bin.Op != "+" {
		t.Fatalf("expected `x + y` Binary, got %T %v", ret.Value, ret.Value)
	}
	li, ok := bin.Left.(*ast.Ident)
	if !ok || li.Name != "x" {
		t.Errorf("Left should be Ident x, got %#v", bin.Left)
	}
	ri, ok := bin.Right.(*ast.Ident)
	if !ok || ri.Name != "y" {
		t.Errorf("Right should be Ident y, got %#v", bin.Right)
	}
}

func TestInlineSkipsCallArgs(t *testing.T) {
	// `add(branchy(3), 2)` — branchy has more than one statement so it
	// isn't itself inlineable; that leaves a Call as one of add's
	// arguments, which the inliner refuses to substitute (it might
	// duplicate side effects).
	prog, err := parser.Parse(`
		function branchy(x: number): number {
			if (x == 0) { return 1; }
			return x * 2;
		}
		function add(a: number, b: number): number { return a + b; }
		function main(): number { return add(branchy(3), 2); }`)
	if err != nil {
		t.Fatal(err)
	}
	Optimize(prog)
	ret := prog.Funcs[2].Body.Stmts[0].(*ast.Return)
	if _, ok := ret.Value.(*ast.Call); !ok {
		t.Errorf("expected outer Call (add not inlined), got %T", ret.Value)
	}
}

func TestInlinePreservesMultipleCallSites(t *testing.T) {
	// Two call sites must each get their own substituted body. Without
	// the cloning in substitute, both sites would share the same
	// Binary node and a later transform mutating one would corrupt
	// the other. The expected post-inline shape is
	// `(3 + 3) + (4 + 4)` — the IR fold collapses to 14 later.
	prog, err := parser.Parse(`
		function dbl(x: number): number { return x + x; }
		function main(): number { return dbl(3) + dbl(4); }`)
	if err != nil {
		t.Fatal(err)
	}
	Optimize(prog)
	ret := prog.Funcs[1].Body.Stmts[0].(*ast.Return)
	outer, ok := ret.Value.(*ast.Binary)
	if !ok || outer.Op != "+" {
		t.Fatalf("expected outer `+` Binary, got %T %v", ret.Value, ret.Value)
	}
	left, ok := outer.Left.(*ast.Binary)
	if !ok || left.Op != "+" {
		t.Errorf("Left arm should be `3 + 3` Binary, got %#v", outer.Left)
	} else {
		// Confirm cloning: the two call sites must yield distinct
		// Binary nodes (otherwise mutating one mutates the other).
		right, _ := outer.Right.(*ast.Binary)
		if right == left {
			t.Errorf("call sites must clone, got shared Binary at both arms")
		}
	}
}
