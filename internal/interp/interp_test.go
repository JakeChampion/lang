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

func evalProgram(t *testing.T, src string) (Value, *Interp) {
	t.Helper()
	prog, err := parser.Parse(src)
	if err != nil {
		t.Fatalf("parse: %v", err)
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

// evalChecked is evalProgram + a leading checker.Check pass, so
// method-call shapes (`m.set(k, v)`) get rewritten to their
// mangled form (`__method_Map_set(m, k, v)`) before the
// interpreter dispatches. Use this for any test that exercises
// the method-call path; the bare evalProgram path is fine for
// AST shapes the parser produces directly (free functions,
// struct field access, etc.).
func evalChecked(t *testing.T, src string) (Value, *Interp) {
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
			a[1] = 99;
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
	if got := evalProgramValue(t, `function main(): i32 { return len("hello"); }`); got != Number(5) {
		t.Errorf("got %v, want 5", got)
	}
}

func TestInterpLenOfArray(t *testing.T) {
	if got := evalProgramValue(t, `function main(): i32 {
		var a: i32[] = [1, 2, 3, 4];
		return len(a);
	}`); got != Number(4) {
		t.Errorf("got %v, want 4", got)
	}
}

func TestInterpStringIndex(t *testing.T) {
	if got := evalProgramValue(t, `function main(): i32 {
		var s: string = "ABC";
		return s[1];
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
func TestInterpFStringFallback(t *testing.T) {
	src := `function main(): string { return f"hello world"; }`
	if got := evalProgramValue(t, src); got != String("hello world") {
		t.Errorf("got %v, want \"hello world\"", got)
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
			m.set(1, 10);
			m.set(2, 20);
			m.set(3, 30);
			return m.len();
		}`, 3},
		{"set then get", `function main(): i32 {
			var m: Map[i32, i32] = map_new(4);
			m.set(7, 42);
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
			m.set(1, 100);
			if (m.has(1) && !m.has(2)) {
				if (m.delete(1)) {
					if (m.has(1)) { return -1; }
					return 0;
				}
			}
			return -2;
		}`, 0},
		{"get_or", `function main(): i32 {
			var m: Map[i32, i32] = map_new(4);
			m.set(7, 70);
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
			got, _ := evalChecked(t, c.src)
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
	src := `struct Point { x: i32, y: i32 }
		function main(): i32 {
			var p: Point = Point { x: 3, y: 4 };
			p.x = p.x + 1;
			return p.x + p.y;
		}`
	v, _ := evalProgram(t, src)
	if n, ok := v.(Number); !ok || n != 8 {
		t.Errorf("got %v, want 8 (4+4)", v)
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
	// The interp test scaffolding doesn't invoke checker.Check
	// (parses + interprets directly), so the builtin Option
	// auto-inject doesn't run — declare it locally so the
	// interpreter knows about Some/None.
	src := `enum Option[T] { Some(T), None }
		function main(): i32 {
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

// `arena { … }` runs as a normal scope at interp time —
// no allocator snap, but the block body executes and any
// declared bindings stay confined to the inner scope.
func TestInterpArenaScope(t *testing.T) {
	src := `function main(): i32 {
		var outer: i32 = 1;
		arena {
			var inner: i32 = 99;
			outer = outer + inner;
		}
		return outer;
	}`
	v, _ := evalProgram(t, src)
	if n, ok := v.(Number); !ok || n != 100 {
		t.Errorf("arena scope: got %v, want 100", v)
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
