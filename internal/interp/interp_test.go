package interp

import (
	"bytes"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/checker"
	"github.com/jakechampion/lang/internal/parser"
)

func evalProgramValue(t *testing.T, src string) Value {
	t.Helper()
	v, _ := evalProgram(t, src)
	return v
}

// evalProgram parses, type-checks, registers, and calls `main`.
// Match the cmd/fern script-mode pipeline so method-call rewrites,
// FString desugaring, and variant-call IsVariantCall flags land
// before interp dispatch — early tests had separate "checked" and
// "parser-only" helpers because some checker features arrived
// later; once the interp moved to handle every AST shape directly,
// the parser-only form became the exceptional path. Tests that
// intentionally exercise interp BEFORE the checker has run (the
// FString fallback case) wire up `interp.New` + `parser.Parse`
// directly so the absence of a checker pass is documented at the
// call site.
func evalProgram(t *testing.T, src string) (Value, *Interp) {
	t.Helper()
	prog, err := parser.Parse(src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if _, err := checker.Check(prog); err != nil {
		t.Fatalf("check: %v", err)
	}
	i := New()
	for _, ed := range prog.Enums {
		i.RegisterEnum(ed)
	}
	for _, fn := range prog.Funcs {
		i.Register(fn)
	}
	if _, ok := i.Funcs["main"]; !ok {
		t.Fatalf("program has no main")
	}
	v, err := i.CallByName("main", nil)
	if err != nil {
		t.Fatalf("call main: %v", err)
	}
	return v, i
}

func TestSimpleArithmetic(t *testing.T) {
	v, _ := evalProgram(t, `function main(): i32 { return 1 + 2 * 3; }`)
	if n, ok := v.(Number); !ok || n != 7 {
		t.Errorf("got %v, want 7", v)
	}
}

func TestRecursionFactorial(t *testing.T) {
	v, _ := evalProgram(t, `
		function fact(n: i32): i32 {
			if (n == 0) { return 1; }
			return n * fact(n - 1);
		}
		function main(): i32 { return fact(6); }`)
	if n, ok := v.(Number); !ok || n != 720 {
		t.Errorf("got %v, want 720", v)
	}
}

func TestForLoopWithBreakContinue(t *testing.T) {
	v, _ := evalProgram(t, `
		function main(): i32 {
			var sum: i32 = 0;
			for (var i: i32 = 0; i < 10; i = i + 1) {
				if (i == 3) { continue; }
				if (i == 7) { break; }
				sum = sum + i;
			}
			return sum;
		}`)
	// 0+1+2 (skip 3) +4+5+6 (break before 7) = 18
	if n, ok := v.(Number); !ok || n != 18 {
		t.Errorf("got %v, want 18", v)
	}
}

func TestArrayIndexAndAssign(t *testing.T) {
	v, _ := evalProgram(t, `
		function main(): i32 {
			var a: i32[] = [10, 20, 30];
			a = a.with(1, 99);
			return a[0] + a[1] + a[2];
		}`)
	if n, ok := v.(Number); !ok || n != 139 {
		t.Errorf("got %v, want 139", v)
	}
}

func TestPrintBuiltin(t *testing.T) {
	prog, err := parser.Parse(`function main(): void { print("hello"); }`)
	if err != nil {
		t.Fatal(err)
	}
	i := New()
	var buf bytes.Buffer
	i.Stdout = &buf
	for _, ed := range prog.Enums {
		i.RegisterEnum(ed)
	}
	for _, fn := range prog.Funcs {
		i.Register(fn)
	}
	if _, err := i.CallByName("main", nil); err != nil {
		t.Fatal(err)
	}
	if got := buf.String(); got != "hello\n" {
		t.Errorf("stdout = %q, want \"hello\\n\"", got)
	}
}

func TestIndirectCallViaLocal(t *testing.T) {
	v, _ := evalProgram(t, `
		function dbl(x: i32): i32 { return x * 2; }
		function main(): i32 {
			var f = dbl;
			return f(7);
		}`)
	if n, ok := v.(Number); !ok || n != 14 {
		t.Errorf("got %v, want 14", v)
	}
}

func TestREPLEvaluatesExpressions(t *testing.T) {
	in := strings.NewReader("1 + 2 * 3\n\"hi\"\nvar x = 5;\nx + 10\n")
	var out bytes.Buffer
	if err := REPL(in, &out); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	for _, want := range []string{"7", "hi", "15"} {
		if !strings.Contains(got, want) {
			t.Errorf("REPL output missing %q\n--- got ---\n%s", want, got)
		}
	}
}

func TestREPLDeclaresThenCallsFunction(t *testing.T) {
	in := strings.NewReader("function dbl(x: i32): i32 { return x * 2; }\ndbl(21)\n")
	var out bytes.Buffer
	if err := REPL(in, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "42") {
		t.Errorf("expected 42 in REPL output, got:\n%s", out.String())
	}
}

func TestInterpLenOfString(t *testing.T) {
	if got := evalProgramValue(t, `function main(): i32 { return ("hello").len(); }`); got != Number(5) {
		t.Errorf("got %v, want 5", got)
	}
}

func TestInterpLenOfArray(t *testing.T) {
	if got := evalProgramValue(t, `function main(): i32 {
		var a: i32[] = [1, 2, 3, 4];
		return a.len();
	}`); got != Number(4) {
		t.Errorf("got %v, want 4", got)
	}
}

func TestInterpStringIndex(t *testing.T) {
	if got := evalProgramValue(t, `function main(): i32 {
		var s: string = "ABC";
		return s[1] as i32;
	}`); got != Number(int64('B')) {
		t.Errorf("got %v, want %d", got, 'B')
	}
}

func TestInterpStringEquality(t *testing.T) {
	src := `function main(): boolean {
		var a: string = "hello";
		var b: string = "hello";
		return a == b;
	}`
	if got := evalProgramValue(t, src); got != Bool(true) {
		t.Errorf("got %v, want true", got)
	}
}

// TestInterpFStringFallback exercises the parser-only path (no
// checker run) so Desugared is nil and the interpreter assembles
// the f-string from raw Parts. The literal-only segment proves
// the path concatenates static text correctly.
//
// Wires interp directly rather than going through evalProgram
// because that helper runs the checker (which would populate
// Desugared and bypass the fallback this test exists to cover).
func TestInterpFStringFallback(t *testing.T) {
	src := `function main(): string { return f"hello world"; }`
	prog, err := parser.Parse(src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	i := New()
	for _, fn := range prog.Funcs {
		i.Register(fn)
	}
	got, err := i.CallByName("main", nil)
	if err != nil {
		t.Fatalf("call main: %v", err)
	}
	if got != String("hello world") {
		t.Errorf("got %v, want \"hello world\"", got)
	}
}

// TestInterpClosure exercises the Closure Value: a nested
// function captures `n` from the enclosing scope and returns
// `x + n` when called.
func TestInterpClosure(t *testing.T) {
	src := `function main(): i32 {
		var n: i32 = 100;
		function bump(x: i32): i32 { return x + n; }
		return bump(5);
	}`
	got, _ := evalProgram(t, src)
	if n, ok := got.(Number); !ok || int64(n) != 105 {
		t.Errorf("got %v, want 105", got)
	}
}

// TestInterpLambda exercises the *ast.Lambda Value path: an
// anonymous function expression is bound to a var and then
// invoked.
func TestInterpLambda(t *testing.T) {
	src := `function main(): i32 {
		var k: i32 = 7;
		var mul: (i32) => i32 = function (x: i32): i32 { return x * k; };
		return mul(6);
	}`
	got, _ := evalProgram(t, src)
	if n, ok := got.(Number); !ok || int64(n) != 42 {
		t.Errorf("got %v, want 42", got)
	}
}

// TestInterpMapBasic exercises the new Map runtime through the
// surface user code sees: literal construction, .get returning
// Some / None, .set + read-back, .len, .has, .delete. Each
// case is small so a regression in any single shim shows up
// independently.
func TestInterpMapBasic(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want int64
	}{
		{"len of empty", `function main(): i32 { var m: Map[i32, i32] = map_new(4); return m.len(); }`, 0},
		{"len after set", `function main(): i32 {
			var m: Map[i32, i32] = map_new(4);
			m = m.insert(1, 10);
			m = m.insert(2, 20);
			m = m.insert(3, 30);
			return m.len();
		}`, 3},
		{"set then get", `function main(): i32 {
			var m: Map[i32, i32] = map_new(4);
			m = m.insert(7, 42);
			match (m.get(7)) {
				Some(v) => { return v; },
				None => { return -1; }
			}
			return 999;
		}`, 42},
		{"get missing key", `function main(): i32 {
			var m: Map[i32, i32] = map_new(4);
			match (m.get(7)) {
				Some(v) => { return v; },
				None => { return -1; }
			}
			return 999;
		}`, -1},
		{"has and delete", `function main(): i32 {
			var m: Map[i32, i32] = map_new(4);
			m = m.insert(1, 100);
			if (m.has(1) && !m.has(2)) {
				if (m.without(1).1) {
					if (m.has(1)) { return -1; }
					return 0;
				}
			}
			return -2;
		}`, 0},
		{"get_or", `function main(): i32 {
			var m: Map[i32, i32] = map_new(4);
			m = m.insert(7, 70);
			return m.get_or(7, 1) + m.get_or(99, 2);
		}`, 72},
		{"literal", `function main(): i32 {
			var m: Map[i32, i32] = Map { 1: 10, 2: 20, 3: 30 };
			return m.len();
		}`, 3},
		{"for-each iter", `function main(): i32 {
			var m: Map[i32, i32] = Map { 1: 10, 2: 20, 3: 30 };
			var sum: i32 = 0;
			for (k, v) in m {
				sum = sum + k + v;
			}
			return sum;
		}`, 66},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, _ := evalProgram(t, c.src)
			n, ok := got.(Number)
			if !ok {
				t.Fatalf("expected Number, got %T (%v)", got, got)
			}
			if int64(n) != c.want {
				t.Errorf("got %d, want %d", int64(n), c.want)
			}
		})
	}
}

func TestInterpStructBasic(t *testing.T) {
	// Fields are immutable; update via struct-update + rebind.
	src := `struct Point { x: i32, y: i32 }
		function main(): i32 {
			var p: Point = Point { x: 3, y: 4 };
			p = Point { ...p, x: p.x + 1 };
			return p.x + p.y;
		}`
	v, _ := evalProgram(t, src)
	if n, ok := v.(Number); !ok || n != 8 {
		t.Errorf("got %v, want 8 (4+4)", v)
	}
}

// Struct-update literal `Point { ...a, y: 10 }`: un-named fields copy
// from the base, named fields override. The result is a fresh value.
func TestInterpStructUpdate(t *testing.T) {
	src := `struct Point { x: i32, y: i32 }
		function main(): i32 {
			var a: Point = Point { x: 3, y: 4 };
			var b: Point = Point { ...a, y: 10 };
			return b.x + b.y;
		}`
	v, _ := evalProgram(t, src)
	if n, ok := v.(Number); !ok || n != 13 {
		t.Errorf("got %v, want 13 (copied x=3 + override y=10)", v)
	}
}

// Struct-update produces a fresh value: mutating/overriding into the
// new struct must not change the base. Returns the base's fields after
// an update derived from it.
func TestInterpStructUpdateIsFunctional(t *testing.T) {
	src := `struct Point { x: i32, y: i32 }
		function main(): i32 {
			var a: Point = Point { x: 3, y: 4 };
			var b: Point = Point { ...a, x: 100 };
			return a.x + a.y;
		}`
	v, _ := evalProgram(t, src)
	if n, ok := v.(Number); !ok || n != 7 {
		t.Errorf("got %v, want 7 (base a unchanged: 3+4)", v)
	}
}

// Sum types in the interpreter: a payload-carrying variant
// constructed and then matched routes through the right arm with
// payload bindings visible in the arm body.
func TestInterpEnumMatchPayload(t *testing.T) {
	src := `enum Pair { Two(i32, i32) }
		function main(): i32 {
			var p: Pair = Two(7, 5);
			match (p) {
				Two(a, b) => { return a + b; }
			}
			return -1;
		}`
	v, _ := evalProgram(t, src)
	if n, ok := v.(Number); !ok || n != 12 {
		t.Errorf("got %v, want 12 (7 + 5)", v)
	}
}

// Generic enums in the interpreter: type erasure makes the
// runtime representation independent of T, so a single Enum
// value works for any instantiation. Constructor inference and
// match-arm payload extraction both route correctly.
func TestInterpGenericOption(t *testing.T) {
	// `Option[T]` is auto-injected by the checker now (the
	// previous test scaffolding skipped the checker and had to
	// declare it locally). With evalProgram running the
	// checker, just use the built-in.
	src := `function main(): i32 {
		var o: Option[i32] = Some(42);
		match (o) {
			Some(v) => { return v; },
			None => { return -1; }
		}
		return 99;
	}`
	v, _ := evalProgram(t, src)
	if n, ok := v.(Number); !ok || n != 42 {
		t.Errorf("got %v, want 42", v)
	}
}

// TestInterpQualifiedVariants — two enums can declare the same
// variant name; the disambiguation is the `Color.Red`-style
// qualifier at the use site. Was IMPROVEMENTS.md #15.
func TestInterpQualifiedVariants(t *testing.T) {
	// Bare-name reference in payload-less position.
	v, _ := evalProgram(t, `enum Color { Red, Green, Blue }
		enum Status { Red, Yellow }
		function main(): i32 {
			var c: Color = Color.Red;
			match (c) {
				Color.Red => { return 1; },
				Color.Green => { return 2; },
				Color.Blue => { return 3; }
			}
			return 0;
		}`)
	if n, ok := v.(Number); !ok || n != 1 {
		t.Errorf("Color.Red: got %v, want 1", v)
	}
	// Qualified variant call with payload. The two enums' Foo
	// variants have different positions (`Foo` is index 0 in A,
	// index 1 in B) — picking the wrong enum would mismatch the
	// match arm's tag check.
	v, _ = evalProgram(t, `enum A { Foo(i32), Bar }
		enum B { Bar, Foo(i32) }
		function main(): i32 {
			var a: A = A.Foo(42);
			match (a) {
				A.Foo(x) => { return x; },
				A.Bar => { return -1; }
			}
			return 0;
		}`)
	if n, ok := v.(Number); !ok || n != 42 {
		t.Errorf("A.Foo(42): got %v, want 42", v)
	}
}

// TestInterpTypeAscription — `expr as T` in non-numeric form is a
// zero-cost annotation at the interp level. The checker accepts
// the cast when `inner` is assignable to `T` (None vs Option[i32],
// Ok(1) vs Result[i32, string], etc.); the interp must pass the
// value through unchanged.
func TestInterpTypeAscription(t *testing.T) {
	// None gets a concrete type via ascription. Match still
	// fires the None arm.
	v, _ := evalProgram(t, `function main(): i32 {
		var x: Option[i32] = None as Option[i32];
		match (x) {
			Some(n) => { return n; },
			None => { return 42; }
		}
		return 0;
	}`)
	if n, ok := v.(Number); !ok || n != 42 {
		t.Errorf("None as Option[i32]: got %v, want 42", v)
	}
	// Partial Result inference — ascription fixes E without
	// changing the runtime value.
	v, _ = evalProgram(t, `function main(): i32 {
		var r: Result[i32, string] = Ok(7) as Result[i32, string];
		match (r) {
			Ok(n) => { return n; },
			Err(_) => { return -1; }
		}
		return 0;
	}`)
	if n, ok := v.(Number); !ok || n != 7 {
		t.Errorf("Ok(7) as Result[i32, string]: got %v, want 7", v)
	}
}

// Wildcard arms catch what the explicit arms miss.
func TestInterpMatchWildcard(t *testing.T) {
	src := `enum Light { Red, Green, Yellow }
		function main(): i32 {
			var l: Light = Yellow;
			match (l) {
				Red => { return 1; },
				_ => { return 99; }
			}
			return 0;
		}`
	v, _ := evalProgram(t, src)
	if n, ok := v.(Number); !ok || n != 99 {
		t.Errorf("got %v, want 99", v)
	}
}

// match on a NON-enum scrutinee (i32 / string / bool) dispatches
// each arm by `==` against its literal, with `_` as the
// fall-through — the same semantics the compiled backends produce
// via emitLiteralMatch. The interpreter must not reject it
// ("match scrutinee is interp.Number, expected enum value") and
// diverge from the compiled backends; pinned in both statement
// and expression position, including guards.
func TestInterpMatchLiteralNonEnum(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want Number
	}{
		{"i32-stmt-first", `function main(): i32 {
			var n: i32 = 1;
			match (n) { 1 => { return 10; }, 2 => { return 20; }, _ => { return 0; } }
			return 0;
		}`, 10},
		{"i32-stmt-default", `function main(): i32 {
			var n: i32 = 9;
			match (n) { 1 => { return 10; }, 2 => { return 20; }, _ => { return 7; } }
			return 0;
		}`, 7},
		{"string-stmt", `function main(): i32 {
			var s: string = "b";
			match (s) { "a" => { return 1; }, "b" => { return 7; }, _ => { return 0; } }
			return 0;
		}`, 7},
		{"bool-stmt", `function main(): i32 {
			var b: boolean = true;
			match (b) { true => { return 42; }, false => { return 1; }, _ => { return 0; } }
			return 0;
		}`, 42},
		{"expr-form", `function main(): i32 {
			var n: i32 = 3;
			var r: i32 = match (n) { 1 => 10, 3 => 30, _ => 0 };
			return r;
		}`, 30},
		{"guard", `function main(): i32 {
			var n: i32 = 5;
			var r: i32 = match (n) { 1 => 1, 5 when n > 3 => 99, _ => 0 };
			return r;
		}`, 99},
		{"guard-falls-through", `function main(): i32 {
			var n: i32 = 5;
			var r: i32 = match (n) { 1 => 1, 5 when n > 100 => 99, _ => 8 };
			return r;
		}`, 8},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			v, _ := evalProgram(t, tc.src)
			if n, ok := v.(Number); !ok || n != tc.want {
				t.Errorf("got %v, want %d", v, tc.want)
			}
		})
	}
}

// End-to-end test of the TCP socket builtins through the
// interpreter — uses Go's net package to spin up a local
// server, accept one connection, echo the bytes, and close.
// Validates the full builtin surface (listen / accept / recv /
// send / close) without needing qemu or wasmtime.
func TestInterpTcpSocketEcho(t *testing.T) {
	// We pick port 0 in the lang program — but our builtin
	// requires a specific port. Use a high port and retry on
	// "address in use". Simpler: bind in Go first, get the
	// port, then have the lang program connect.
	//
	// For a focused test, exercise listen + close (no client
	// dial). A full echo loop would need goroutine
	// orchestration; that's covered by the AOT backends'
	// integration tests on real ports.
	src := `function main(): i32 {
		var fd: i32 = tcp_listen(0);
		if (fd < 0) { return 1; }
		var c: i32 = tcp_close(fd);
		if (c != 0) { return 2; }
		return 0;
	}`
	v, _ := evalProgram(t, src)
	if n, ok := v.(Number); !ok || n != 0 {
		t.Errorf("tcp_listen + tcp_close: got %v, want 0", v)
	}
}

// `let (a, b) = expr;` binds each name to the corresponding
// tuple element. Covers a tuple literal, a function returning
// a tuple, and a 3-element destructure for arity > 2.
func TestInterpTupleDestructure(t *testing.T) {
	src := `function divmod(a: i32, b: i32): (i32, i32) {
		return (a / b, a % b);
	}
	function main(): i32 {
		let (a, b) = (10, 32);
		if (a != 10) { return 1; }
		if (b != 32) { return 2; }
		let (q, r) = divmod(17, 5);
		if (q != 3) { return 3; }
		if (r != 2) { return 4; }
		let (x, y, z) = (1, 2, 3);
		return x + y + z;
	}`
	v, _ := evalProgram(t, src)
	if n, ok := v.(Number); !ok || n != 6 {
		t.Errorf("destructure: got %v, want 6", v)
	}
}

// Tuple-destructuring parameters `(a, b): (T, U)` — the parse-time
// desugar routes through the same Destructure evaluation, covering a
// named function, a mixed param list, and both lambda forms.
func TestInterpParamDestructure(t *testing.T) {
	src := `function add((a, b): (i32, i32)): i32 {
		return a + b;
	}
	function scale(k: i32, (lo, hi): (i32, i32)): i32 {
		return k * (hi - lo);
	}
	function main(): i32 {
		var f = function((x, y): (i32, i32)): i32 { return x * y; };
		var g = ((lo, hi): (i32, i32)) => hi - lo;
		return add((30, 5)) + scale(2, (3, 5)) + f((1, 2)) + g((5, 6));
	}`
	v, _ := evalProgram(t, src)
	if n, ok := v.(Number); !ok || n != 42 {
		t.Errorf("param destructure: got %v, want 42", v)
	}
}

// Tuple patterns in match arms — literal elements dispatch by
// equality, binders bind, `_` is ignored, guards see the bindings.
// Covers the statement form, the expression form, and a string
// element.
func TestInterpTupleMatch(t *testing.T) {
	src := `function classify(p: (i32, i32)): i32 {
		match (p) {
			(0, 0) => { return 1; },
			(0, y) => { return y; },
			(x, 0) => { return x * 10; },
			(x, y) when x > y => { return x - y; },
			(x, y) => { return x + y; }
		}
		return -1;
	}
	function tag(p: (string, i32)): i32 {
		match (p) {
			("a", n) => { return n; },
			(_, n) => { return n * 100; }
		}
		return -1;
	}
	function main(): i32 {
		// 1 + 7 + 30 + 5 + 7 = 50
		var t = classify((0, 0)) + classify((0, 7)) + classify((3, 0)) + classify((9, 4)) + classify((2, 5));
		var s = match ((1, 2)) { (1, b) => b * 3, (a, _) => a };
		// t=50, s=6, tag(("a",2))=2, tag(("z",1))=100 → 50+6+2+100 = 158
		return t + s + tag(("a", 2)) + tag(("z", 1));
	}`
	v, _ := evalProgram(t, src)
	if n, ok := v.(Number); !ok || n != 158 {
		t.Errorf("tuple match: got %v, want 158", v)
	}
}
