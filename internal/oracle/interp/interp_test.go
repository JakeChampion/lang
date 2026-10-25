package interp

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/jakechampion/lang/internal/check/checker"
	"github.com/jakechampion/lang/internal/syntax/parser"
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
			let sum: i32 = 0;
			for (let i: i32 = 0; i < 10; i = i + 1) {
				if (i == 3) { continue; }
				if (i == 7) { break; }
				sum = sum + i;
			}
			return sum;
		}`)
	if n, ok := v.(Number); !ok || n != 18 {
		t.Errorf("got %v, want 18", v)
	}
}

func TestArrayIndexAndAssign(t *testing.T) {
	v, _ := evalProgram(t, `
		function main(): i32 {
			let a: i32[] = [10, 20, 30];
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

func TestPutcharWritesOneByte(t *testing.T) {
	for _, tc := range []struct {
		name string
		arg  string
		want string
	}{
		{"ascii-agrees-either-way", "65", "A"},
		{"high-byte-is-not-utf8-encoded", "233", "\xe9"},
		{"top-of-range", "255", "\xff"},
		{"zero-is-a-nul-byte", "0", "\x00"},
		{"above-a-byte-wraps", "321", "A"},
		{"negative-takes-its-low-byte", "0 - 1", "\xff"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			prog, err := parser.Parse(`function main(): void { putchar(` + tc.arg + `); }`)
			if err != nil {
				t.Fatal(err)
			}
			i := New()
			var buf bytes.Buffer
			i.Stdout = &buf
			for _, fn := range prog.Funcs {
				i.Register(fn)
			}
			if _, err := i.CallByName("main", nil); err != nil {
				t.Fatal(err)
			}
			if got := buf.String(); got != tc.want {
				t.Errorf("putchar(%s) wrote % x, want % x", tc.arg, got, tc.want)
			}
		})
	}
}

func TestIndirectCallViaLocal(t *testing.T) {
	v, _ := evalProgram(t, `
		function dbl(x: i32): i32 { return x * 2; }
		function main(): i32 {
			let f = dbl;
			return f(7);
		}`)
	if n, ok := v.(Number); !ok || n != 14 {
		t.Errorf("got %v, want 14", v)
	}
}

func TestInterpLenOfString(t *testing.T) {
	if got := evalProgramValue(t, `function main(): i32 { return ("hello").len(); }`); got != Number(5) {
		t.Errorf("got %v, want 5", got)
	}
}

func TestInterpLenOfArray(t *testing.T) {
	if got := evalProgramValue(t, `function main(): i32 {
		let a: i32[] = [1, 2, 3, 4];
		return a.len();
	}`); got != Number(4) {
		t.Errorf("got %v, want 4", got)
	}
}

func TestInterpStringIndex(t *testing.T) {
	if got := evalProgramValue(t, `function main(): i32 {
		let s: string = "ABC";
		return s[1] as i32;
	}`); got != Number(int64('B')) {
		t.Errorf("got %v, want %d", got, 'B')
	}
}

func TestInterpStringEquality(t *testing.T) {
	src := `function main(): boolean {
		let a: string = "hello";
		let b: string = "hello";
		return a == b;
	}`
	if got := evalProgramValue(t, src); got != Bool(true) {
		t.Errorf("got %v, want true", got)
	}
}

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

func TestInterpClosure(t *testing.T) {
	src := `function main(): i32 {
		let n: i32 = 100;
		function bump(x: i32): i32 { return x + n; }
		return bump(5);
	}`
	got, _ := evalProgram(t, src)
	if n, ok := got.(Number); !ok || int64(n) != 105 {
		t.Errorf("got %v, want 105", got)
	}
}

func TestInterpLambda(t *testing.T) {
	src := `function main(): i32 {
		let k: i32 = 7;
		let mul: (i32) => i32 = (x: i32): i32 => { return x * k; };
		return mul(6);
	}`
	got, _ := evalProgram(t, src)
	if n, ok := got.(Number); !ok || int64(n) != 42 {
		t.Errorf("got %v, want 42", got)
	}
}

func TestInterpMapBasic(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want int64
	}{
		{"len of empty", `function main(): i32 { let m: Map[i32, i32] = map_new(4); return m.len(); }`, 0},
		{"len after set", `function main(): i32 {
			let m: Map[i32, i32] = map_new(4);
			m = m.insert(1, 10);
			m = m.insert(2, 20);
			m = m.insert(3, 30);
			return m.len();
		}`, 3},
		{"set then get", `function main(): i32 {
			let m: Map[i32, i32] = map_new(4);
			m = m.insert(7, 42);
			match (m.get(7)) {
				Some(v) => { return v; },
				None => { return -1; }
			}
			return 999;
		}`, 42},
		{"get missing key", `function main(): i32 {
			let m: Map[i32, i32] = map_new(4);
			match (m.get(7)) {
				Some(v) => { return v; },
				None => { return -1; }
			}
			return 999;
		}`, -1},
		{"has and delete", `function main(): i32 {
			let m: Map[i32, i32] = map_new(4);
			m = m.insert(1, 100);
			if (m.contains(1) && !m.contains(2)) {
				let had: boolean = m.contains(1);
				let rest = m.remove(1);
				if (had && !rest.contains(1) && m.contains(1)) { return 0; }
				return -1;
			}
			return -2;
		}`, 0},
		{"get_or", `function main(): i32 {
			let m: Map[i32, i32] = map_new(4);
			m = m.insert(7, 70);
			return m.get_or(7, 1) + m.get_or(99, 2);
		}`, 72},
		{"literal", `function main(): i32 {
			let m: Map[i32, i32] = Map { 1: 10, 2: 20, 3: 30 };
			return m.len();
		}`, 3},
		{"for-each iter", `function main(): i32 {
			let m: Map[i32, i32] = Map { 1: 10, 2: 20, 3: 30 };
			let sum: i32 = 0;
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
	src := `struct Point { x: i32, y: i32 }
		function main(): i32 {
			let p: Point = Point { x: 3, y: 4 };
			p = Point { ...p, x: p.x + 1 };
			return p.x + p.y;
		}`
	v, _ := evalProgram(t, src)
	if n, ok := v.(Number); !ok || n != 8 {
		t.Errorf("got %v, want 8 (4+4)", v)
	}
}

func TestInterpStructUpdate(t *testing.T) {
	src := `struct Point { x: i32, y: i32 }
		function main(): i32 {
			let a: Point = Point { x: 3, y: 4 };
			let b: Point = Point { ...a, y: 10 };
			return b.x + b.y;
		}`
	v, _ := evalProgram(t, src)
	if n, ok := v.(Number); !ok || n != 13 {
		t.Errorf("got %v, want 13 (copied x=3 + override y=10)", v)
	}
}

func TestInterpStructUpdateIsFunctional(t *testing.T) {
	src := `struct Point { x: i32, y: i32 }
		function main(): i32 {
			let a: Point = Point { x: 3, y: 4 };
			let b: Point = Point { ...a, x: 100 };
			return a.x + a.y;
		}`
	v, _ := evalProgram(t, src)
	if n, ok := v.(Number); !ok || n != 7 {
		t.Errorf("got %v, want 7 (base a unchanged: 3+4)", v)
	}
}

func TestInterpEnumMatchPayload(t *testing.T) {
	src := `enum Pair { Two(i32, i32) }
		function main(): i32 {
			let p: Pair = Two(7, 5);
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

func TestInterpGenericOption(t *testing.T) {
	src := `function main(): i32 {
		let o: Option[i32] = Some(42);
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

func TestInterpQualifiedVariants(t *testing.T) {
	v, _ := evalProgram(t, `enum Color { Red, Green, Blue }
		enum Status { Red, Yellow }
		function main(): i32 {
			let c: Color = Color.Red;
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
	v, _ = evalProgram(t, `enum A { Foo(i32), Bar }
		enum B { Bar, Foo(i32) }
		function main(): i32 {
			let a: A = A.Foo(42);
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

func TestInterpTypeAscription(t *testing.T) {
	v, _ := evalProgram(t, `function main(): i32 {
		let x: Option[i32] = None as Option[i32];
		match (x) {
			Some(n) => { return n; },
			None => { return 42; }
		}
		return 0;
	}`)
	if n, ok := v.(Number); !ok || n != 42 {
		t.Errorf("None as Option[i32]: got %v, want 42", v)
	}
	v, _ = evalProgram(t, `function main(): i32 {
		let r: Result[i32, string] = Ok(7) as Result[i32, string];
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

func TestInterpMatchWildcard(t *testing.T) {
	src := `enum Light { Red, Green, Yellow }
		function main(): i32 {
			let l: Light = Yellow;
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

func TestInterpMatchLiteralNonEnum(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want Number
	}{
		{"i32-stmt-first", `function main(): i32 {
			let n: i32 = 1;
			match (n) { 1 => { return 10; }, 2 => { return 20; }, _ => { return 0; } }
			return 0;
		}`, 10},
		{"i32-stmt-default", `function main(): i32 {
			let n: i32 = 9;
			match (n) { 1 => { return 10; }, 2 => { return 20; }, _ => { return 7; } }
			return 0;
		}`, 7},
		{"string-stmt", `function main(): i32 {
			let s: string = "b";
			match (s) { "a" => { return 1; }, "b" => { return 7; }, _ => { return 0; } }
			return 0;
		}`, 7},
		{"bool-stmt", `function main(): i32 {
			let b: boolean = true;
			match (b) { true => { return 42; }, false => { return 1; }, _ => { return 0; } }
			return 0;
		}`, 42},
		{"expr-form", `function main(): i32 {
			let n: i32 = 3;
			let r: i32 = match (n) { 1 => 10, 3 => 30, _ => 0 };
			return r;
		}`, 30},
		{"guard", `function main(): i32 {
			let n: i32 = 5;
			let r: i32 = match (n) { 1 => 1, 5 when n > 3 => 99, _ => 0 };
			return r;
		}`, 99},
		{"guard-falls-through", `function main(): i32 {
			let n: i32 = 5;
			let r: i32 = match (n) { 1 => 1, 5 when n > 100 => 99, _ => 8 };
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

func TestInterpTcpSocketEcho(t *testing.T) {
	src := `function main(): i32 {
		let fd: i32 = tcp_listen(0);
		if (fd < 0) { return 1; }
		let c: i32 = tcp_close(fd);
		if (c != 0) { return 2; }
		return 0;
	}`
	v, _ := evalProgram(t, src)
	if n, ok := v.(Number); !ok || n != 0 {
		t.Errorf("tcp_listen + tcp_close: got %v, want 0", v)
	}
}

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

func TestInterpParamDestructure(t *testing.T) {
	src := `function add((a, b): (i32, i32)): i32 {
		return a + b;
	}
	function scale(k: i32, (lo, hi): (i32, i32)): i32 {
		return k * (hi - lo);
	}
	function main(): i32 {
		let f = ((x, y): (i32, i32)): i32 => { return x * y; };
		let g = ((lo, hi): (i32, i32)) => hi - lo;
		return add((30, 5)) + scale(2, (3, 5)) + f((1, 2)) + g((5, 6));
	}`
	v, _ := evalProgram(t, src)
	if n, ok := v.(Number); !ok || n != 42 {
		t.Errorf("param destructure: got %v, want 42", v)
	}
}

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
		let t = classify((0, 0)) + classify((0, 7)) + classify((3, 0)) + classify((9, 4)) + classify((2, 5));
		let s = match ((1, 2)) { (1, b) => b * 3, (a, _) => a };
		// t=50, s=6, tag(("a",2))=2, tag(("z",1))=100 → 50+6+2+100 = 158
		return t + s + tag(("a", 2)) + tag(("z", 1));
	}`
	v, _ := evalProgram(t, src)
	if n, ok := v.(Number); !ok || n != 158 {
		t.Errorf("tuple match: got %v, want 158", v)
	}
}

func TestBuiltinReadFileInvalidUtf8(t *testing.T) {
	dir := t.TempDir()
	bad := filepath.Join(dir, "bad.bin")
	if err := os.WriteFile(bad, []byte{0xff, 0xfe}, 0o644); err != nil {
		t.Fatal(err)
	}
	v, err := builtinReadFile(nil, []Value{String(bad)})
	if err != nil {
		t.Fatalf("builtinReadFile: %v", err)
	}
	res, ok := v.(*Enum)
	if !ok || res.VariantName != "Err" {
		t.Fatalf("got %#v, want Err", v)
	}
	ioe, ok := res.Payloads[0].(*Enum)
	if !ok || ioe.VariantName != "InvalidUtf8" || ioe.Index != 3 {
		t.Fatalf("got %#v, want InvalidUtf8 (tag 3)", res.Payloads[0])
	}
	if p, ok := ioe.Payloads[0].(String); !ok || string(p) != bad {
		t.Fatalf("payload = %#v, want the path", ioe.Payloads[0])
	}
	v, err = builtinReadFileBytes(nil, []Value{String(bad)})
	if err != nil {
		t.Fatalf("builtinReadFileBytes: %v", err)
	}
	res, ok = v.(*Enum)
	if !ok || res.VariantName != "Ok" {
		t.Fatalf("bytes: got %#v, want Ok", v)
	}
	if arr, ok := res.Payloads[0].(Array); !ok || len(arr.Elems()) != 2 {
		t.Fatalf("bytes payload = %#v, want 2-byte array", res.Payloads[0])
	}
}

func evalSliceOption(t *testing.T, decl, expr string) *Enum {
	t.Helper()
	_, in := evalProgram(t, "function f(): Option[str] { "+decl+" return "+expr+"; }\n"+
		"function main(): i32 { return 0; }")
	v, err := in.CallByName("f", nil)
	if err != nil {
		t.Fatalf("%s: call f: %v", expr, err)
	}
	e, ok := v.(*Enum)
	if !ok {
		t.Fatalf("%s: got %#v, want an Option enum", expr, v)
	}
	if e.EnumName != "Option" {
		t.Fatalf("%s: enum is %q, want Option", expr, e.EnumName)
	}
	return e
}

func TestStringSliceYieldsSome(t *testing.T) {
	for _, tc := range []struct{ expr, want string }{
		{`s[2:5]`, "cde"},
		{`s[:3]`, "abc"},
		{`s[7:]`, "hij"},
		{`s[0:0]`, ""},
		{`s[10:10]`, ""},
		{`s[0:10]`, "abcdefghij"},
	} {
		e := evalSliceOption(t, `let s: string = "abcdefghij";`, tc.expr)
		if e.VariantName != "Some" || e.Index != 0 {
			t.Fatalf("%s: got %s, want Some", tc.expr, e.VariantName)
		}
		got, ok := e.Payloads[0].(String)
		if !ok {
			t.Fatalf("%s: payload %#v is not a String", tc.expr, e.Payloads[0])
		}
		if string(got) != tc.want {
			t.Errorf("%s = %q, want %q", tc.expr, got, tc.want)
		}
	}
}

func TestStringSliceSplitCodePointYieldsNone(t *testing.T) {
	for _, expr := range []string{`m[1:2]`, `m[2:4]`, `m[2:2]`, `m[0:2]`} {
		e := evalSliceOption(t, `let m: string = "héllo";`, expr)
		if e.VariantName != "None" || e.Index != 1 {
			t.Errorf("%s: got %s(%v), want None", expr, e.VariantName, e.Payloads)
		}
	}
	e := evalSliceOption(t, `let m: string = "héllo";`, `m[1:3]`)
	if e.VariantName != "Some" {
		t.Fatalf(`m[1:3]: got %s, want Some`, e.VariantName)
	}
	if got := e.Payloads[0].(String); string(got) != "é" {
		t.Errorf(`m[1:3] = %q, want "é"`, got)
	}
}

func TestStringSliceOutOfRangeYieldsNone(t *testing.T) {
	for _, expr := range []string{
		`s[1:99]`,
		`s[0 - 1:3]`,
		`s[4:2]`,
		`s[11:11]`,
	} {
		e := evalSliceOption(t, `let s: string = "abcdefghij";`, expr)
		if e.VariantName != "None" || e.Index != 1 {
			t.Errorf("%s: got %s(%v), want None", expr, e.VariantName, e.Payloads)
		}
	}
}

func TestArraySliceOutOfRangeStillErrors(t *testing.T) {
	src := `function main(): i32 { let xs: i32[] = [1, 2, 3]; let s: [i32] = xs[1:9]; return s.len(); }`
	prog, err := parser.Parse(src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if _, err := checker.Check(prog); err != nil {
		t.Fatalf("check: %v", err)
	}
	i := New()
	for _, fn := range prog.Funcs {
		i.Register(fn)
	}
	_, err = i.CallByName("main", nil)
	var abort *Abort
	if !errors.As(err, &abort) || abort.Cause != CauseSliceRange {
		t.Errorf("got %v, want the slice-range abort", err)
	}
}

func TestSliceUncheckedStillTraps(t *testing.T) {
	if _, err := builtinSliceUnchecked(nil, []Value{String("hi"), Number(1), Number(9)}); err == nil {
		t.Fatal("slice_unchecked out of range must be an error")
	}
	v, err := builtinSliceUnchecked(nil, []Value{String("héllo"), Number(1), Number(2)})
	if err != nil {
		t.Fatalf("slice_unchecked: %v", err)
	}
	if s, ok := v.(String); !ok || len(string(s)) != 1 {
		t.Errorf("got %#v, want the single byte", v)
	}
}

func TestInterpUsizeShiftsAtSixtyFourBits(t *testing.T) {
	v := evalProgramValue(t, `function main(): i32 {
    let one: usize = 1 as usize;
    let top: usize = one << 63;
    let a: usize = 70000000000 as usize;
    let got: i32 = 0;
    if (top > a && a < top) { got = got + 1; }
    if ((top >> 62) as i32 == 2) { got = got + 2; }
    if ((a >> 32) as i32 == 16) { got = got + 4; }
    return got;
}`)
	if n, ok := v.(Number); !ok || n != 7 {
		t.Errorf("got %v, want 7", v)
	}
}
