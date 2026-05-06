package printer

import (
	"reflect"
	"testing"

	"github.com/jakechampion/lang/internal/ast"
	"github.com/jakechampion/lang/internal/parser"
)

// roundTrip parses src, pretty-prints the result, and parses that
// output again. The two ASTs should match modulo source positions.
func roundTrip(t *testing.T, src string) {
	t.Helper()
	first, err := parser.Parse(src)
	if err != nil {
		t.Fatalf("first parse of %q: %v", src, err)
	}
	printed := Print(first)
	second, err := parser.Parse(printed)
	if err != nil {
		t.Fatalf("second parse of printed output\nprinted: %s\nerr: %v", printed, err)
	}
	zeroPositions(first)
	zeroPositions(second)
	if !reflect.DeepEqual(first, second) {
		t.Errorf("round-trip differs:\noriginal source:\n%s\nprinted:\n%s\nfirst AST:  %#v\nsecond AST: %#v",
			src, printed, first, second)
	}
}

func TestRoundtripNumbers(t *testing.T) {
	roundTrip(t, `function f(): number { return 42; }`)
}

func TestRoundtripArithmetic(t *testing.T) {
	roundTrip(t, `function f(): number { return 1 + 2 * 3 - 4 / 5; }`)
}

func TestRoundtripBooleans(t *testing.T) {
	roundTrip(t, `function f(): boolean { return true && (false || true); }`)
}

func TestRoundtripIfElse(t *testing.T) {
	roundTrip(t, `function f(n: number): number {
		if (n == 0) { return 1; } else { return 2; }
	}`)
}

func TestRoundtripWhileAndAssign(t *testing.T) {
	roundTrip(t, `function f(): number {
		var i: number = 0;
		var sum: number = 0;
		while (i < 10) { sum = sum + i; i = i + 1; }
		return sum;
	}`)
}

func TestRoundtripArraysAndIndexing(t *testing.T) {
	roundTrip(t, `function f(): number {
		var a: number[] = [1, 2, 3];
		a[0] = 99;
		return a[0] + a[1];
	}`)
}

func TestRoundtripRecursiveFactorial(t *testing.T) {
	roundTrip(t, `function fact(n: number): number {
		if (n == 0) { return 1; }
		return n * fact(n - 1);
	}
	function main(): number { return fact(5); }`)
}

func TestRoundtripUnary(t *testing.T) {
	roundTrip(t, `function f(): number { return -1 + -(2 + 3); }`)
}

func TestRoundtripStrings(t *testing.T) {
	roundTrip(t, `function f(): void {
		var s: string = "hello, world";
		print(s);
	}`)
}

func TestRoundtripForBreakContinue(t *testing.T) {
	roundTrip(t, `function f(): number {
		var sum: number = 0;
		for (var i: number = 0; i < 10; i = i + 1) {
			if (i == 3) { continue; }
			if (i == 7) { break; }
			sum = sum + i;
		}
		return sum;
	}`)
}

func TestRoundtripFunctionType(t *testing.T) {
	roundTrip(t, `function apply(f: (number, number) => number, a: number, b: number): number {
		return f(a, b);
	}
	function add(x: number, y: number): number { return x + y; }
	function main(): number { return apply(add, 1, 2); }`)
}

func TestRoundtripStringEscapes(t *testing.T) {
	roundTrip(t, `function f(): void {
		print("tab:\tnewline:\nquote:\"backslash:\\");
	}`)
}

// ---------- helpers ----------

// zeroPositions walks every AST node and zeroes its source position so
// two ASTs compare equal regardless of whitespace differences in the
// pretty-printed output.
func zeroPositions(prog *ast.Program) {
	for _, fn := range prog.Funcs {
		fn.P = ast.Position{}
		zeroBlock(fn.Body)
	}
}

func zeroBlock(b *ast.Block) {
	if b == nil {
		return
	}
	b.P = ast.Position{}
	for _, s := range b.Stmts {
		zeroStmt(s)
	}
}

func zeroStmt(s ast.Stmt) {
	switch x := s.(type) {
	case *ast.Block:
		zeroBlock(x)
	case *ast.If:
		x.P = ast.Position{}
		zeroExpr(x.Cond)
		zeroStmt(x.Then)
		if x.Else != nil {
			zeroStmt(x.Else)
		}
	case *ast.While:
		x.P = ast.Position{}
		zeroExpr(x.Cond)
		zeroStmt(x.Body)
	case *ast.For:
		x.P = ast.Position{}
		if x.Init != nil {
			zeroStmt(x.Init)
		}
		zeroExpr(x.Cond)
		if x.Step != nil {
			zeroStmt(x.Step)
		}
		zeroStmt(x.Body)
	case *ast.Break:
		x.P = ast.Position{}
	case *ast.Continue:
		x.P = ast.Position{}
	case *ast.Return:
		x.P = ast.Position{}
		if x.Value != nil {
			zeroExpr(x.Value)
		}
	case *ast.Var:
		x.P = ast.Position{}
		zeroExpr(x.Init)
	case *ast.ExprStmt:
		x.P = ast.Position{}
		zeroExpr(x.Expr)
	case *ast.Switch:
		x.P = ast.Position{}
		zeroExpr(x.Tag)
		for _, k := range x.Cases {
			k.P = ast.Position{}
			for _, v := range k.Values {
				zeroExpr(v)
			}
			zeroBlock(k.Body)
		}
		if x.Default != nil {
			zeroBlock(x.Default)
		}
	}
}

func zeroExpr(e ast.Expr) {
	switch x := e.(type) {
	case *ast.NumberLit:
		x.P = ast.Position{}
	case *ast.BoolLit:
		x.P = ast.Position{}
	case *ast.StringLit:
		x.P = ast.Position{}
	case *ast.FloatLit:
		x.P = ast.Position{}
	case *ast.Ident:
		x.P = ast.Position{}
	case *ast.ArrayLit:
		x.P = ast.Position{}
		for _, el := range x.Elems {
			zeroExpr(el)
		}
	case *ast.Index:
		x.P = ast.Position{}
		zeroExpr(x.Array)
		zeroExpr(x.Idx)
	case *ast.Call:
		x.P = ast.Position{}
		zeroExpr(x.Callee)
		for _, a := range x.Args {
			zeroExpr(a)
		}
	case *ast.Binary:
		x.P = ast.Position{}
		zeroExpr(x.Left)
		zeroExpr(x.Right)
	case *ast.Unary:
		x.P = ast.Position{}
		zeroExpr(x.Operand)
	case *ast.Assign:
		x.P = ast.Position{}
		zeroExpr(x.Target)
		zeroExpr(x.Value)
	case *ast.Ternary:
		x.P = ast.Position{}
		zeroExpr(x.Cond)
		zeroExpr(x.Then)
		zeroExpr(x.Else)
	}
}

func TestRoundtripSwitch(t *testing.T) {
	roundTrip(t, `function f(n: number): number {
		switch (n) {
			case 1, 2: return 10;
			case 3: return 30;
			default: return 0;
		}
		return -1;
	}`)
}

func TestRoundtripTernary(t *testing.T) {
	roundTrip(t, `function f(b: boolean): number { return b ? 1 : 2; }`)
}

func TestRoundtripCompoundAssign(t *testing.T) {
	// The printer always emits the desugared `x = x + 1` form, which
	// re-parses to the same AST.
	roundTrip(t, `function f(): number { var x: number = 0; x += 1; return x; }`)
}
