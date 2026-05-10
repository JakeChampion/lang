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
	roundTrip(t, `function f(): i32 { return 42; }`)
}

func TestRoundtripArithmetic(t *testing.T) {
	roundTrip(t, `function f(): i32 { return 1 + 2 * 3 - 4 / 5; }`)
}

func TestRoundtripBooleans(t *testing.T) {
	roundTrip(t, `function f(): boolean { return true && (false || true); }`)
}

func TestRoundtripIfElse(t *testing.T) {
	roundTrip(t, `function f(n: i32): i32 {
		if (n == 0) { return 1; } else { return 2; }
	}`)
}

func TestRoundtripWhileAndAssign(t *testing.T) {
	roundTrip(t, `function f(): i32 {
		var i: i32 = 0;
		var sum: i32 = 0;
		while (i < 10) { sum = sum + i; i = i + 1; }
		return sum;
	}`)
}

func TestRoundtripArraysAndIndexing(t *testing.T) {
	roundTrip(t, `function f(): i32 {
		var a: i32[] = [1, 2, 3];
		a[0] = 99;
		return a[0] + a[1];
	}`)
}

func TestRoundtripRecursiveFactorial(t *testing.T) {
	roundTrip(t, `function fact(n: i32): i32 {
		if (n == 0) { return 1; }
		return n * fact(n - 1);
	}
	function main(): i32 { return fact(5); }`)
}

func TestRoundtripUnary(t *testing.T) {
	roundTrip(t, `function f(): i32 { return -1 + -(2 + 3); }`)
}

func TestRoundtripStrings(t *testing.T) {
	roundTrip(t, `function f(): void {
		var s: string = "hello, world";
		print(s);
	}`)
}

func TestRoundtripForBreakContinue(t *testing.T) {
	roundTrip(t, `function f(): i32 {
		var sum: i32 = 0;
		for (var i: i32 = 0; i < 10; i = i + 1) {
			if (i == 3) { continue; }
			if (i == 7) { break; }
			sum = sum + i;
		}
		return sum;
	}`)
}

func TestRoundtripFunctionType(t *testing.T) {
	roundTrip(t, `function apply(f: (i32, i32) => i32, a: i32, b: i32): i32 {
		return f(a, b);
	}
	function add(x: i32, y: i32): i32 { return x + y; }
	function main(): i32 { return apply(add, 1, 2); }`)
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
	for _, sd := range prog.Structs {
		sd.P = ast.Position{}
	}
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
	case *ast.FuncDecl:
		x.P = ast.Position{}
		zeroBlock(x.Body)
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
	case *ast.IfExpr:
		x.P = ast.Position{}
		zeroExpr(x.Cond)
		zeroExpr(x.Then)
		zeroExpr(x.Else)
	case *ast.TryOp:
		x.P = ast.Position{}
		zeroExpr(x.Inner)
	case *ast.StructLit:
		x.P = ast.Position{}
		for _, f := range x.Fields {
			zeroExpr(f.Value)
		}
	case *ast.FieldAccess:
		x.P = ast.Position{}
		zeroExpr(x.Target)
	}
}

func TestRoundtripSwitch(t *testing.T) {
	roundTrip(t, `function f(n: i32): i32 {
		switch (n) {
			case 1, 2: return 10;
			case 3: return 30;
			default: return 0;
		}
		return -1;
	}`)
}

func TestRoundtripIfExpr(t *testing.T) {
	roundTrip(t, `function f(b: boolean): i32 { return if (b) { 1 } else { 2 }; }`)
}

func TestRoundtripCompoundAssign(t *testing.T) {
	// The printer always emits the desugared `x = x + 1` form, which
	// re-parses to the same AST.
	roundTrip(t, `function f(): i32 { var x: i32 = 0; x += 1; return x; }`)
}

func TestRoundtripStruct(t *testing.T) {
	roundTrip(t, `struct Point { x: i32, y: i32 }
		function main(): i32 {
			var p: Point = Point { x: 1, y: 2 };
			p.x = 10;
			return p.x + p.y;
		}`)
}

func TestRoundtripNestedFunction(t *testing.T) {
	roundTrip(t, `function makeAdder(n: i32): (i32) => i32 {
		function add(x: i32): i32 { return x + n; }
		return add;
	}`)
}

func TestRoundtripMethod(t *testing.T) {
	roundTrip(t, `struct Point { x: i32, y: i32 }
		function (p: Point) sum(): i32 { return p.x + p.y; }`)
}
