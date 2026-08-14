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

func TestRoundtripLoop(t *testing.T) {
	roundTrip(t, `function f(): i32 {
		var i: i32 = 0;
		loop { i = i + 1; if (i >= 3) { break; } }
		return i;
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
		for i := range sd.Fields {
			sd.Fields[i].NamePos = ast.Position{}
		}
	}
	for _, fn := range prog.Funcs {
		fn.P = ast.Position{}
		fn.NamePos = ast.Position{}
		for i := range fn.Params {
			fn.Params[i].NamePos = ast.Position{}
		}
		if fn.Receiver != nil {
			fn.Receiver.NamePos = ast.Position{}
		}
		zeroBlock(fn.Body)
	}
	// TypeRefs is a parser-recorded side table whose source-position
	// content varies with whitespace, so positions diverge between
	// the source and printer output even when the underlying AST
	// matches. Drop it for the comparison. Comments / BlankLines are
	// likewise whitespace-keyed side tables (their line numbers shift
	// as formatting re-flows the source), not part of the AST shape.
	prog.TypeRefs = nil
	prog.Comments = nil
	prog.BlankLines = nil
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
	case *ast.Loop:
		x.P = ast.Position{}
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
	case *ast.FuncDecl:
		x.P = ast.Position{}
		x.NamePos = ast.Position{}
		for i := range x.Params {
			x.Params[i].NamePos = ast.Position{}
		}
		if x.Receiver != nil {
			x.Receiver.NamePos = ast.Position{}
		}
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
		if x.Base != nil {
			zeroExpr(x.Base)
		}
		for i := range x.Fields {
			x.Fields[i].NamePos = ast.Position{}
			zeroExpr(x.Fields[i].Value)
		}
	case *ast.FieldAccess:
		x.P = ast.Position{}
		x.FieldPos = ast.Position{}
		zeroExpr(x.Target)
	}
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

// Construction-site type arguments on a struct literal survive the
// round-trip, and their ABSENCE survives too — printing them onto a
// literal that never wrote them changes the program just as much as
// dropping them from one that did. Both directions matter because the
// dropped form re-infers: `Box[i64] { val: 42 }` printed as
// `Box { val: 42 }` silently becomes `Box[i32]`.
func TestRoundtripStructLitTypeArgs(t *testing.T) {
	const decls = "struct Box[T] { val: T }\nstruct Stack[T] { items: T[] }\nstruct Pair[A, B] { a: A, b: B }\n"
	for _, body := range []string{
		// Written — must survive.
		`function main(): i32 { var b = Box[i32] { val: 1 }; return b.val; }`,
		`function main(): i32 { var s = Stack[i32] { items: [] }; return s.items.len(); }`,
		`function main(): i32 { var p = Pair[i32, string] { a: 1, b: "x" }; return p.a; }`,
		`function main(): i32 { var b = Box[i32[]] { val: [1] }; return b.val.len(); }`,
		// Written, on a struct-update literal.
		`function main(): i32 { var a = Box[i32] { val: 1 }; var b = Box[i32] { ...a, val: 2 }; return b.val; }`,
		// Not written — must stay not written.
		`function main(): i32 { var b = Box { val: 1 }; return b.val; }`,
		`function main(): i32 { var a = Box { val: 1 }; var b = Box { ...a, val: 2 }; return b.val; }`,
	} {
		roundTrip(t, decls+body)
	}
}

// Struct-update literals (`Foo { ...base, field: v }` and the pure
// copy `Foo { ...base }`) round-trip through parse → print → parse
// with the spread base preserved.
func TestRoundtripStructUpdate(t *testing.T) {
	roundTrip(t, `struct Point { x: i32, y: i32 }
		function main(): i32 {
			var a: Point = Point { x: 1, y: 2 };
			var b: Point = Point { ...a, y: 9 };
			var c: Point = Point { ...a };
			return b.y + c.x;
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

// An `@import` extern (bring-your-own WIT, P4) must survive Print → re-parse
// with its binding intact and a nil body — and must not panic on the nil body.
func TestRoundtripExternImport(t *testing.T) {
	roundTrip(t, `@import("wasi:random/random@0.2.0", "get-random-bytes")
function rand_bytes(n: u64): u8[];
function main(): i32 { return 0; }`)
}

// `@derive(...)` on a struct must survive Print → re-parse with the trait
// list intact (Print formerly dropped it, losing the derives).
func TestRoundtripDeriveAttr(t *testing.T) {
	roundTrip(t, `@derive(Eq, Display)
struct P { x: i32, y: i32 }
function main(): i32 { return 0; }`)
}
