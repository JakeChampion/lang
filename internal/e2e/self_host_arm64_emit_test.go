package e2e

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/jakechampion/lang/internal/checker"
	"github.com/jakechampion/lang/internal/codegen/x86_64"
	"github.com/jakechampion/lang/internal/constfold"
	"github.com/jakechampion/lang/internal/modload"
)

// Sibling to TestSelfHostAsmRunX86_64 — exercises the
// asm_arm64.lang ARM64 codegen layer. The driver
// (asm_arm64_run.lang) is compiled on the host (x86_64),
// reads lang source from stdin, and prints aarch64 assembly
// to stdout. The Go test pipes each source in, gcc-assembles
// the output with aarch64-linux-gnu-gcc, then runs the
// resulting binary under qemu-aarch64 (or natively on arm64
// hosts) and asserts the exit code matches.
//
// Scope mirrors asm_arm64.lang's: i32 literals + arithmetic
// (+ - * / %) + unary `-` + `return` only. Locals / control
// flow / functions land in follow-up PRs.

func TestSelfHostAsmArm64Bootstrap(t *testing.T) {
	gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	for _, name := range []string{"lexer.lang", "parser.lang", "asm_arm64.lang", "asm_arm64_run.lang"} {
		src, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), src, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	// Build the driver as an x86_64 binary — the driver itself
	// runs on the test host, only its OUTPUT is arm64 asm.
	prog, _, err := modload.Load(filepath.Join(dir, "asm_arm64_run.lang"))
	if err != nil {
		t.Fatalf("modload: %v", err)
	}
	if err := constfold.Fold(prog); err != nil {
		t.Fatalf("constfold: %v", err)
	}
	info, err := checker.Check(prog)
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	asm, err := x86_64.Emit(prog, info)
	if err != nil {
		t.Fatalf("emit: %v", err)
	}
	driverAsm := filepath.Join(dir, "driver.s")
	driverBin := filepath.Join(dir, "driver")
	if err := os.WriteFile(driverAsm, []byte(asm), 0o644); err != nil {
		t.Fatalf("write driver asm: %v", err)
	}
	if out, err := exec.Command(x86gcc, "-static", "-nostdlib", "-no-pie", driverAsm, "-o", driverBin).CombinedOutput(); err != nil {
		t.Fatalf("driver gcc: %v\n%s", err, out)
	}

	cases := []struct {
		name     string
		source   string
		expected int
		stdout   string // "" means don't check
	}{
		{"return-literal", "return 42;", 42, ""},
		{"arithmetic", "return 1 + 2 * 3;", 7, ""},
		{"parens", "return (1 + 2) * 3;", 9, ""},
		{"subtraction", "return 100 - 23;", 77, ""},
		{"division", "return 84 / 2;", 42, ""},
		{"modulo", "return 23 % 5;", 3, ""},
		{"unary-neg-via-zero-minus", "return 0 - 5 + 10;", 5, ""},
		{"nested-arith", "return (2 + 3) * 4;", 20, ""},
		{"cmp-lt-true", "return 5 < 10;", 1, ""},
		{"cmp-lt-false", "return 10 < 5;", 0, ""},
		{"cmp-le-true", "return 5 <= 5;", 1, ""},
		{"cmp-gt-true", "return 7 > 3;", 1, ""},
		{"cmp-ge-true", "return 7 >= 7;", 1, ""},
		{"cmp-eq-true", "return 4 == 4;", 1, ""},
		{"cmp-eq-false", "return 4 == 5;", 0, ""},
		{"cmp-ne-true", "return 4 != 5;", 1, ""},
		{"bool-true", "return true;", 1, ""},
		{"bool-false", "return false;", 0, ""},
		{"if-then-taken", "if (true) { return 9; } else { return 0; }", 9, ""},
		{"if-else-taken", "if (false) { return 9; } else { return 7; }", 7, ""},
		{"if-no-else-fall", "if (false) { return 9; } return 5;", 5, ""},
		{"if-cond-via-cmp", "if (5 < 10) { return 1; } else { return 2; }", 1, ""},
		{"locals-single", "var x = 5; return x;", 5, ""},
		{"locals-three", "var a = 10; var b = 20; var c = 30; return a + b + c;", 60, ""},
		{"reassign", "var x = 5; x = x + 3; return x;", 8, ""},
		{"compound-assign", "var x = 1; x *= 6; x += 1; return x;", 7, ""},
		{"while-sum-counter", "var i = 1; var s = 0; while (i <= 5) { s += i; i += 1; } return s;", 15, ""},
		{"while-early-return", "var i = 0; while (i < 100) { if (i == 7) { return i; } i += 1; } return 0 - 1;", 7, ""},
		{"func-decl-call", "function add(x: i32, y: i32): i32 { return x + y; } function main(): i32 { return add(2, 3); }", 5, ""},
		{"func-three-args", "function sum3(a: i32, b: i32, c: i32): i32 { return a + b + c; } function main(): i32 { return sum3(10, 20, 30); }", 60, ""},
		{"recursive-factorial", "function fact(n: i32): i32 { if (n <= 1) { return 1; } return n * fact(n - 1); } function main(): i32 { return fact(5); }", 120, ""},
		{"recursive-fib", "function fib(n: i32): i32 { if (n < 2) { return n; } return fib(n - 1) + fib(n - 2); } function main(): i32 { return fib(8); }", 21, ""},
		{"mutual-recursion", "function is_even(n: i32): i32 { if (n == 0) { return 1; } return is_odd(n - 1); } function is_odd(n: i32): i32 { if (n == 0) { return 0; } return is_even(n - 1); } function main(): i32 { return is_even(6); }", 1, ""},
		{"func-with-local-vars", "function compute(a: i32): i32 { var b = a * 2; var c = b + 1; return c; } function main(): i32 { return compute(5); }", 11, ""},
		{"hello-arm64", "print(\"Hello, ARM64!\\n\"); return 0;", 0, "Hello, ARM64!\n"},
		{"print-twice", "print(\"line A\\n\"); print(\"line B\\n\"); return 0;", 0, "line A\nline B\n"},
		{"print-then-return", "print(\"out\\n\"); return 7;", 7, "out\n"},
		{"print-int-literal", "function main(): i32 { print_int(42); print(\"\\n\"); return 0; }", 0, "42\n"},
		{"print-int-zero", "function main(): i32 { print_int(0); print(\"\\n\"); return 0; }", 0, "0\n"},
		{"print-int-negative", "function main(): i32 { print_int(0 - 7); print(\"\\n\"); return 0; }", 0, "-7\n"},
		{"print-int-fact", "function fact(n: i32): i32 { if (n <= 1) { return 1; } return n * fact(n - 1); } function main(): i32 { print_int(fact(8)); print(\"\\n\"); return 0; }", 0, "40320\n"},
		{
			"fizzbuzz-canonical-1-to-15",
			"function main(): i32 { " +
				"var i = 1; " +
				"while (i <= 15) { " +
				"if (i % 15 == 0) { print(\"FizzBuzz\"); } " +
				"else if (i % 3 == 0) { print(\"Fizz\"); } " +
				"else if (i % 5 == 0) { print(\"Buzz\"); } " +
				"else { print_int(i); } " +
				"print(\"\\n\"); " +
				"i = i + 1; " +
				"} " +
				"return 0; }",
			0,
			"1\n2\nFizz\n4\nBuzz\nFizz\n7\n8\nFizz\nBuzz\n11\nFizz\n13\n14\nFizzBuzz\n",
		},
		{
			"fibonacci-series-first-10",
			"function fib(n: i32): i32 { if (n < 2) { return n; } return fib(n - 1) + fib(n - 2); } " +
				"function main(): i32 { var i = 0; while (i < 10) { print_int(fib(i)); print(\" \"); i = i + 1; } print(\"\\n\"); return 0; }",
			0,
			"0 1 1 2 3 5 8 13 21 34 \n",
		},
		{
			"sum-via-recursion-and-print",
			"function sum(n: i32): i32 { if (n == 0) { return 0; } return n + sum(n - 1); } " +
				"function main(): i32 { print(\"sum(1..10) = \"); print_int(sum(10)); print(\"\\n\"); return 0; }",
			0,
			"sum(1..10) = 55\n",
		},
		{
			"primes-up-to-30",
			"function is_prime(n: i32): i32 { if (n < 2) { return 0; } var i = 2; while (i * i <= n) { if (n % i == 0) { return 0; } i = i + 1; } return 1; } " +
				"function main(): i32 { var i = 2; while (i <= 30) { if (is_prime(i) == 1) { print_int(i); print(\" \"); } i = i + 1; } print(\"\\n\"); return 0; }",
			0,
			"2 3 5 7 11 13 17 19 23 29 \n",
		},
		{
			"tuple-literal-access-zero",
			"function main(): i32 { var t = (7, 11, 13); return t.0; }",
			7,
			"",
		},
		{
			"tuple-literal-access-middle",
			"function main(): i32 { var t = (7, 11, 13); return t.1; }",
			11,
			"",
		},
		{
			"tuple-literal-access-last",
			"function main(): i32 { var t = (7, 11, 13); return t.2; }",
			13,
			"",
		},
		{
			"tuple-sum-fields",
			"function main(): i32 { var t = (10, 20, 30); return t.0 + t.1 + t.2; }",
			60,
			"",
		},
		{
			"tuple-of-expressions",
			"function main(): i32 { var x = 5; var t = (x * 2, x + 1, x - 1); return t.0 + t.1 + t.2; }",
			20,
			"",
		},
		{
			"array-literal-len",
			"function main(): i32 { var a = [10, 20, 30]; return len(a); }",
			3,
			"",
		},
		{
			"array-index-first",
			"function main(): i32 { var a = [42, 99, 7]; return a[0]; }",
			42,
			"",
		},
		{
			"array-index-middle",
			"function main(): i32 { var a = [42, 99, 7]; return a[1]; }",
			99,
			"",
		},
		{
			"array-index-last",
			"function main(): i32 { var a = [42, 99, 7]; return a[2]; }",
			7,
			"",
		},
		{
			"array-index-via-var",
			"function main(): i32 { var a = [10, 20, 30, 40]; var i = 2; return a[i]; }",
			30,
			"",
		},
		{
			"array-sum-via-while",
			"function main(): i32 { var a = [1, 2, 3, 4, 5]; var i = 0; var s = 0; while (i < len(a)) { s = s + a[i]; i = i + 1; } return s; }",
			15,
			"",
		},
		{
			"array-of-expressions",
			"function main(): i32 { var x = 4; var a = [x, x + 1, x * 2]; return a[0] + a[1] + a[2]; }",
			17,
			"",
		},
		{
			"match-single-variant-binding",
			"struct Circle { r: i32 } function main(): i32 { var c = Circle { r: 5 }; match (c) { Circle(x) => { return x.r; }, _ => { return 0 - 1; } } return 0 - 2; }",
			5,
			"",
		},
		{
			"match-two-variants-first-arm",
			"struct Circle { r: i32 } struct Square { s: i32 } function main(): i32 { var c = Circle { r: 3 }; match (c) { Circle(x) => { return x.r; }, Square(y) => { return y.s; } } return 0 - 1; }",
			3,
			"",
		},
		{
			"match-two-variants-second-arm",
			"struct Circle { r: i32 } struct Square { s: i32 } function main(): i32 { var q = Square { s: 7 }; match (q) { Circle(x) => { return x.r; }, Square(y) => { return y.s; } } return 0 - 1; }",
			7,
			"",
		},
		{
			"match-wildcard-fallback",
			"struct Circle { r: i32 } struct Triangle { b: i32 } function main(): i32 { var t = Triangle { b: 9 }; match (t) { Circle(c) => { return c.r; }, _ => { return 42; } } return 0 - 1; }",
			42,
			"",
		},
		{
			"match-no-binding",
			"struct Empty { } function main(): i32 { var e = Empty { }; match (e) { Empty => { return 11; }, _ => { return 0 - 1; } } return 0 - 2; }",
			11,
			"",
		},
		{
			"match-write-to-outer-var",
			"struct Circle { r: i32 } function main(): i32 { var c = Circle { r: 8 }; var n: i32 = 0; match (c) { Circle(x) => { n = x.r; }, _ => { n = 0 - 1; } } return n; }",
			8,
			"",
		},
		{
			"for-sum-array",
			"function main(): i32 { var a = [10, 20, 30]; var s = 0; for x in a { s = s + x; } return s; }",
			60,
			"",
		},
		{
			"for-count-iterations",
			"function main(): i32 { var a = [1, 2, 3, 4, 5]; var n = 0; for x in a { n = n + 1; } return n; }",
			5,
			"",
		},
		{
			"for-empty-array",
			"function main(): i32 { var a = [42]; var s = 0; for x in a { s = s + 1; } return s; }",
			1,
			"",
		},
		{
			"for-element-squares",
			"function main(): i32 { var a = [2, 3, 4]; var s = 0; for x in a { s = s + x * x; } return s; }",
			29,
			"",
		},
		{
			"break-in-while",
			"function main(): i32 { var i = 0; var s = 0; while (i < 100) { if (i == 5) { break; } s = s + i; i = i + 1; } return s; }",
			10,
			"",
		},
		{
			"continue-in-while",
			"function main(): i32 { var i = 0; var s = 0; while (i < 10) { i = i + 1; if (i == 5) { continue; } s = s + i; } return s; }",
			50,
			"",
		},
		{
			"break-in-for",
			"function main(): i32 { var a = [10, 20, 30, 40]; var s = 0; for x in a { if (x == 30) { break; } s = s + x; } return s; }",
			30,
			"",
		},
		{
			"continue-in-for",
			"function main(): i32 { var a = [1, 2, 3, 4, 5]; var s = 0; for x in a { if (x == 3) { continue; } s = s + x; } return s; }",
			12,
			"",
		},
		{
			"method-area-no-args",
			"struct Circle { r: i32 } function (c: Circle) area(): i32 { return c.r * c.r; } function main(): i32 { var k = Circle { r: 5 }; return k.area(); }",
			25,
			"",
		},
		{
			"method-with-args",
			"struct Box { v: i32 } function (b: Box) scale(n: i32): i32 { return b.v * n; } function main(): i32 { var x = Box { v: 4 }; return x.scale(3); }",
			12,
			"",
		},
		{
			"method-mixed-with-plain-func",
			"struct Circle { r: i32 } function (c: Circle) area(): i32 { return c.r * c.r; } function area(): i32 { return 100; } function main(): i32 { var k = Circle { r: 5 }; var m: i32 = area(); var n: i32 = k.area(); return m + n; }",
			125,
			"",
		},
		{
			"method-multi-struct-dispatch",
			"struct Circle { r: i32 } struct Square { s: i32 } function (c: Circle) area(): i32 { return c.r * c.r; } function (q: Square) area(): i32 { return q.s * q.s; } function main(): i32 { var k = Square { s: 6 }; return k.area(); }",
			36,
			"",
		},
		{
			"method-three-args",
			"struct P { x: i32 } function (p: P) f(a: i32, b: i32, c: i32): i32 { return p.x + a + b + c; } function main(): i32 { var p = P { x: 10 }; return p.f(1, 2, 3); }",
			16,
			"",
		},
		{
			"lambda-no-capture",
			"function main(): i32 { var f = function (x: i32): i32 { return x + 1; }; return f(41); }",
			42,
			"",
		},
		{
			"lambda-no-args",
			"function main(): i32 { var f = function (): i32 { return 7; }; return f(); }",
			7,
			"",
		},
		{
			"lambda-multi-args",
			"function main(): i32 { var add = function (a: i32, b: i32): i32 { return a + b; }; return add(20, 22); }",
			42,
			"",
		},
		{
			"lambda-with-locals",
			"function main(): i32 { var f = function (n: i32): i32 { var sq = n * n; var dbl = n + n; return sq + dbl; }; return f(5); }",
			35,
			"",
		},
		{
			"closure-single-capture",
			"function main(): i32 { var n = 5; var f = function (x: i32): i32 { return x + n; }; return f(7); }",
			12,
			"",
		},
		{
			"closure-multi-capture",
			"function main(): i32 { var a = 10; var b = 20; var f = function (): i32 { return a + b; }; return f(); }",
			30,
			"",
		},
		{
			"closure-capture-by-value",
			"function main(): i32 { var n = 5; var f = function (): i32 { return n; }; n = 99; return f(); }",
			5,
			"",
		},
		{
			"closure-capture-and-arg",
			"function main(): i32 { var k = 100; var g = function (x: i32, y: i32): i32 { return x + y + k; }; return g(2, 3); }",
			105,
			"",
		},
		{
			"closure-nested-recapture",
			"function main(): i32 { var n = 7; var outer = function (): i32 { var inner = function (): i32 { return n; }; return inner(); }; return outer(); }",
			7,
			"",
		},
		{
			"closure-nested-three-deep",
			"function main(): i32 { var k = 100; var a = function (): i32 { var b = function (): i32 { var c = function (): i32 { return k; }; return c(); }; return b(); }; return a(); }",
			100,
			"",
		},
		{
			"string-len-literal",
			"function main(): i32 { var s = \"hello\"; return len(s); }",
			5,
			"",
		},
		{
			"string-print-ident",
			"function main(): i32 { var s = \"world\\n\"; print(s); return 0; }",
			0,
			"world\n",
		},
		{
			"string-byte-indexing",
			"function main(): i32 { var s = \"abc\"; return s[1]; }",
			98,
			"",
		},
		{
			"string-concat",
			"function main(): i32 { var a = \"hi \"; var b = \"there\"; var c = a + b; print(c); return len(c); }",
			8,
			"hi there",
		},
		{
			"string-eq-true",
			"function main(): i32 { var a = \"foo\"; var b = \"foo\"; if (a == b) { return 1; } return 0; }",
			1,
			"",
		},
		{
			"string-eq-false",
			"function main(): i32 { var a = \"foo\"; var b = \"bar\"; if (a == b) { return 1; } return 0; }",
			0,
			"",
		},
		{
			"string-slice",
			"function main(): i32 { var s = \"hello world\"; var sub = s[6:11]; print(sub); return len(sub); }",
			5,
			"world",
		},
		{
			"string-param-print",
			"function greet(s: string): i32 { print(s); return 0; } function main(): i32 { greet(\"hi!\\n\"); return 0; }",
			0,
			"hi!\n",
		},
		{
			"string-param-len",
			"function strlen(s: string): i32 { return len(s); } function main(): i32 { return strlen(\"abcdef\"); }",
			6,
			"",
		},
		{
			"string-param-concat",
			"function shout(s: string): string { return s + \"!\"; } function main(): i32 { var out = shout(\"hi\"); print(out); return len(out); }",
			3,
			"hi!",
		},
		{
			"string-lt-true",
			"function main(): i32 { var a = \"apple\"; var b = \"banana\"; if (a < b) { return 1; } return 0; }",
			1,
			"",
		},
		{
			"string-lt-false",
			"function main(): i32 { var a = \"banana\"; var b = \"apple\"; if (a < b) { return 1; } return 0; }",
			0,
			"",
		},
		{
			"string-lt-prefix",
			"function main(): i32 { var a = \"app\"; var b = \"apple\"; if (a < b) { return 1; } return 0; }",
			1,
			"",
		},
		{
			"string-le-equal",
			"function main(): i32 { var a = \"abc\"; var b = \"abc\"; if (a <= b) { return 1; } return 0; }",
			1,
			"",
		},
		{
			"string-gt-true",
			"function main(): i32 { var a = \"zebra\"; var b = \"apple\"; if (a > b) { return 1; } return 0; }",
			1,
			"",
		},
		{
			"string-ge-equal",
			"function main(): i32 { var a = \"xy\"; var b = \"xy\"; if (a >= b) { return 1; } return 0; }",
			1,
			"",
		},
		{
			"string-array-literal-print",
			"function main(): i32 { var arr = [\"hi\", \"bye\"]; print(arr[0]); print(\"\\n\"); print(arr[1]); print(\"\\n\"); return 0; }",
			0,
			"hi\nbye\n",
		},
		{
			"string-array-len-and-index",
			"function main(): i32 { var arr = [\"a\", \"bb\", \"ccc\"]; return len(arr[1]) + len(arr) * 10; }",
			32,
			"",
		},
		{
			"string-array-for-in",
			"function main(): i32 { var arr = [\"one\", \"two\", \"three\"]; for s in arr { print(s); print(\"\\n\"); } return 0; }",
			0,
			"one\ntwo\nthree\n",
		},
		{
			"string-array-eq",
			"function main(): i32 { var arr = [\"x\", \"y\", \"z\"]; if (arr[1] == \"y\") { return 1; } return 0; }",
			1,
			"",
		},
		{
			"method-on-string-receiver",
			"function (s: string) shout(): string { return s + \"!\"; } function main(): i32 { var msg = \"hi\"; var out = msg.shout(); print(out); return len(out); }",
			3,
			"hi!",
		},
		{
			"method-on-i32-receiver",
			"function (n: i32) twice(): i32 { return n * 2; } function main(): i32 { var x = 21; return x.twice(); }",
			42,
			"",
		},
		{
			"method-on-string-arg",
			"function (s: string) repeat3(): string { return s + s + s; } function main(): i32 { var m = \"ab\"; var out = m.repeat3(); print(out); return len(out); }",
			6,
			"ababab",
		},
		{
			"method-on-string-with-args",
			"function (s: string) join_with(sep: string, other: string): string { return s + sep + other; } function main(): i32 { var a = \"foo\"; var b = \"bar\"; var out = a.join_with(\"-\", b); print(out); return 0; }",
			0,
			"foo-bar",
		},
		{
			"i32-to-string-zero",
			"function main(): i32 { var s = i32_to_string(0); print(s); return len(s); }",
			1,
			"0",
		},
		{
			"i32-to-string-positive",
			"function main(): i32 { var s = i32_to_string(12345); print(s); return len(s); }",
			5,
			"12345",
		},
		{
			"i32-to-string-negative",
			"function main(): i32 { var s = i32_to_string(0 - 42); print(s); return len(s); }",
			3,
			"-42",
		},
		{
			"i32-to-string-concat",
			"function main(): i32 { var n = 7; var msg = \"answer: \" + i32_to_string(n); print(msg); return 0; }",
			0,
			"answer: 7",
		},
		{
			"str-to-i32-zero",
			"function main(): i32 { return str_to_i32(\"0\"); }",
			0,
			"",
		},
		{
			"str-to-i32-positive",
			"function main(): i32 { return str_to_i32(\"42\"); }",
			42,
			"",
		},
		{
			"str-to-i32-multi-digit",
			"function main(): i32 { return str_to_i32(\"123\"); }",
			123,
			"",
		},
		{
			"str-to-i32-stops-at-non-digit",
			"function main(): i32 { return str_to_i32(\"99xyz\"); }",
			99,
			"",
		},
		{
			"str-to-i32-round-trip",
			"function main(): i32 { var n = 73; var s = i32_to_string(n); var back = str_to_i32(s); return back; }",
			73,
			"",
		},
		{
			"eprint-literal-exits-clean",
			"function main(): i32 { eprint(\"error msg\\n\"); return 7; }",
			7,
			"",
		},
		{
			"eprint-ident-string",
			"function main(): i32 { var msg = \"oops\\n\"; eprint(msg); return 42; }",
			42,
			"",
		},
		{
			"eprint-no-stdout-emitted",
			"function main(): i32 { eprint(\"stderr only\"); return 0; }",
			0,
			"",
		},
		{
			"eprint-and-print-coexist",
			"function main(): i32 { print(\"out\\n\"); eprint(\"err\\n\"); return 0; }",
			0,
			"out\n",
		},
		{
			"str-starts-with-true",
			"function main(): i32 { if (str_starts_with(\"hello world\", \"hello\")) { return 1; } return 0; }",
			1,
			"",
		},
		{
			"str-starts-with-false",
			"function main(): i32 { if (str_starts_with(\"hello world\", \"world\")) { return 1; } return 0; }",
			0,
			"",
		},
		{
			"str-starts-with-empty-prefix",
			"function main(): i32 { if (str_starts_with(\"abc\", \"\")) { return 1; } return 0; }",
			1,
			"",
		},
		{
			"str-starts-with-longer-prefix",
			"function main(): i32 { if (str_starts_with(\"hi\", \"hello\")) { return 1; } return 0; }",
			0,
			"",
		},
		{
			"str-contains-true",
			"function main(): i32 { if (str_contains(\"hello world\", \"o wo\")) { return 1; } return 0; }",
			1,
			"",
		},
		{
			"str-contains-false",
			"function main(): i32 { if (str_contains(\"hello\", \"xyz\")) { return 1; } return 0; }",
			0,
			"",
		},
		{
			"str-contains-empty-needle",
			"function main(): i32 { if (str_contains(\"hello\", \"\")) { return 1; } return 0; }",
			1,
			"",
		},
		{
			"str-index-of-found",
			"function main(): i32 { return str_index_of(\"hello world\", \"world\"); }",
			6,
			"",
		},
		{
			"str-index-of-not-found",
			"function main(): i32 { return str_index_of(\"hello\", \"xyz\") + 100; }",
			99,
			"",
		},
		{
			"str-index-of-prefix",
			"function main(): i32 { return str_index_of(\"hello\", \"hello\"); }",
			0,
			"",
		},
		{
			"str-ends-with-true",
			"function main(): i32 { if (str_ends_with(\"hello world\", \"world\")) { return 1; } return 0; }",
			1,
			"",
		},
		{
			"str-ends-with-false",
			"function main(): i32 { if (str_ends_with(\"hello world\", \"hello\")) { return 1; } return 0; }",
			0,
			"",
		},
		{
			"str-ends-with-empty-suffix",
			"function main(): i32 { if (str_ends_with(\"abc\", \"\")) { return 1; } return 0; }",
			1,
			"",
		},
		{
			"str-ends-with-longer-suffix",
			"function main(): i32 { if (str_ends_with(\"hi\", \"hello\")) { return 1; } return 0; }",
			0,
			"",
		},
		{
			"str-ends-with-whole",
			"function main(): i32 { if (str_ends_with(\"abc\", \"abc\")) { return 1; } return 0; }",
			1,
			"",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Run the driver (x86_64 binary) to get the arm64 asm.
			var cmd *exec.Cmd
			if len(x86runner) == 0 {
				cmd = exec.Command(driverBin)
			} else {
				cmd = exec.Command(x86runner[0], append(x86runner[1:], driverBin)...)
			}
			cmd.Stdin = bytes.NewReader([]byte(tc.source))
			emittedAsm, err := cmd.Output()
			if err != nil {
				t.Fatalf("driver run: %v\n--- source ---\n%s", err, tc.source)
			}
			caseDir := t.TempDir()
			innerAsm := filepath.Join(caseDir, "inner.s")
			innerBin := filepath.Join(caseDir, "inner")
			if err := os.WriteFile(innerAsm, emittedAsm, 0o644); err != nil {
				t.Fatalf("write inner asm: %v", err)
			}
			// Assemble + link as an arm64 binary.
			if out, err := exec.Command(gcc, "-static", "-nostdlib", innerAsm, "-o", innerBin).CombinedOutput(); err != nil {
				t.Fatalf("inner gcc: %v\n%s\n--- asm ---\n%s", err, out, emittedAsm)
			}
			inner := runArm64Bin(qemu, innerBin)
			innerStdout, _ := inner.Output()
			if code := inner.ProcessState.ExitCode(); code != tc.expected {
				t.Errorf("inner exit code = %d, want %d\n--- source ---\n%s\n--- asm ---\n%s", code, tc.expected, tc.source, emittedAsm)
			}
			if tc.stdout != "" && string(innerStdout) != tc.stdout {
				t.Errorf("inner stdout = %q, want %q\n--- source ---\n%s\n--- asm ---\n%s", string(innerStdout), tc.stdout, tc.source, emittedAsm)
			}
		})
	}
}
