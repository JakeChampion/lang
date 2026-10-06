package e2ecompiler

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Bootstrap-style end-to-end demo. asm_run.fern is a driver
// that reads lang source from stdin, runs it through the
// self-host lexer + parser + asm emitter, and prints the
// resulting AT&T x86_64 assembly to stdout. This table-driven
// test runs every entry through that pipeline:
//
//   1. Build asm_run.fern once via the production langc.
//   2. For each test case: pipe its source to the driver,
//      capture stdout (= emitted asm), gcc-assemble the asm
//      into a standalone Linux ELF, run it, assert the inner
//      exit code matches the entry's expected value.
//
// End-to-end: lang source → fern-port asm emitter → real
// native binary → expected exit code. Proves the asm.fern
// lowering produces working executables across the full
// feature matrix it covers.

func TestSelfHostAsmRunX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_run.fern")
	// Build the driver once.
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_run.fern", "driver")

	// Each case: pipe source to driver, capture asm, assemble,
	// run, verify exit code.
	cases := []struct {
		name     string
		source   string
		expected int
		stdout   string // "" means don't check
		stdin    string // "" means no stdin
	}{
		{"return-literal", "return 42;", 42, "", ""},
		{"arithmetic", "return 1 + 2 * 3;", 7, "", ""},
		{"parens", "return (1 + 2) * 3;", 9, "", ""},
		{"subtraction", "return 100 - 23;", 77, "", ""},
		{"division", "return 84 / 2;", 42, "", ""},
		{"modulo", "return 23 % 5;", 3, "", ""},
		{"unary-neg", "return 0 - 5 + 10;", 5, "", ""},
		{"comparison-true", "if (5 < 10) { return 1; } return 0;", 1, "", ""},
		{"comparison-false", "if (10 < 5) { return 1; } return 0;", 0, "", ""},
		{"equality-true", "if (7 == 7) { return 1; } return 0;", 1, "", ""},
		{"equality-false", "if (7 == 8) { return 1; } return 0;", 0, "", ""},
		{"locals", "let x = 5; let y = 10; return x + y;", 15, "", ""},
		{"let-locals", "let x = 5; let y = 10; return x + y;", 15, "", ""},
		{"let-mixed-with-var", "let x = 40; let y = 2; return x + y;", 42, "", ""},
		{"reassign", "let x = 5; x = x + 3; return x;", 8, "", ""},
		{"compound-assign", "let x = 1; x *= 6; x += 1; return x;", 7, "", ""},
		{"if-then-branch", "let x = 5; if (x < 10) { return 1; } return 2;", 1, "", ""},
		{"if-else-branch", "let x = 20; if (x < 10) { return 1; } return 2;", 2, "", ""},
		{"if-else-explicit", "if (true) { return 9; } else { return 0; }", 9, "", ""},
		{"and-both-true", "if (true && true) { return 1; } return 0;", 1, "", ""},
		{"and-left-false", "if (false && true) { return 1; } return 0;", 0, "", ""},
		{"and-right-false", "if (true && false) { return 1; } return 0;", 0, "", ""},
		{"and-short-circuits-rhs", "function side(): boolean { write(\"R\"); return true; } function main(): i32 { write(\"A\"); if (false && side()) { return 1; } write(\"B\"); return 0; }", 0, "AB", ""},
		{"or-both-false", "if (false || false) { return 1; } return 0;", 0, "", ""},
		{"or-left-true", "if (true || false) { return 1; } return 0;", 1, "", ""},
		{"or-right-true", "if (false || true) { return 1; } return 0;", 1, "", ""},
		{"or-short-circuits-rhs", "function side(): boolean { write(\"R\"); return false; } function main(): i32 { write(\"A\"); if (true || side()) { write(\"B\"); } return 0; }", 0, "AB", ""},
		{"and-or-mixed", "let a = true; let b = false; let c = true; if ((a && b) || c) { return 1; } return 0;", 1, "", ""},
		{"and-with-comparison", "let x = 5; if (x > 0 && x < 10) { return 1; } return 0;", 1, "", ""},
		{"not-true", "if (!true) { return 1; } return 0;", 0, "", ""},
		{"not-false", "if (!false) { return 1; } return 0;", 1, "", ""},
		{"not-comparison", "let x = 5; if (!(x < 0)) { return 1; } return 0;", 1, "", ""},
		{"not-double", "let b = true; if (!!b) { return 1; } return 0;", 1, "", ""},
		{"not-and", "let a = true; let b = false; if (!a && !b) { return 1; } return 2;", 2, "", ""},
		{"not-or-truthy", "let a = false; let b = true; if (!a || !b) { return 1; } return 2;", 1, "", ""},
		{"while-sum", "let i = 1; let s = 0; while (i <= 5) { s += i; i += 1; } return s;", 15, "", ""},
		{"while-early-return", "let i = 0; while (i < 100) { if (i == 7) { return i; } i += 1; } return 0 - 1;", 7, "", ""},
		{"mixed", "let a = 1 + 2; let b = 4 * 5; let c = a + b; if (c < 100) { return c; } return 0 - 1;", 23, "", ""},
		{"func-decl-call", "function add(x: i32, y: i32): i32 { return x + y; } function main(): i32 { return add(2, 3); }", 5, "", ""},
		{"func-three-args", "function sum3(a: i32, b: i32, c: i32): i32 { return a + b + c; } function main(): i32 { return sum3(10, 20, 30); }", 60, "", ""},
		{"recursive-factorial", "function fact(n: i32): i32 { if (n <= 1) { return 1; } return n * fact(n - 1); } function main(): i32 { return fact(5); }", 120, "", ""},
		{"recursive-fib", "function fib(n: i32): i32 { if (n < 2) { return n; } return fib(n - 1) + fib(n - 2); } function main(): i32 { return fib(8); }", 21, "", ""},
		{"mutual-recursion", "function is_even(n: i32): i32 { if (n == 0) { return 1; } return is_odd(n - 1); } function is_odd(n: i32): i32 { if (n == 0) { return 0; } return is_even(n - 1); } function main(): i32 { return is_even(6); }", 1, "", ""},
		{"func-with-local-vars", "function compute(a: i32): i32 { let b = a * 2; let c = b + 1; return c; } function main(): i32 { return compute(5); }", 11, "", ""},
		// if-expressions — desugar to an immediately-invoked closure.
		{"if-expr-true", "function main(): i32 { let x: i32 = if (true) { 3 } else { 4 }; return x; }", 3, "", ""},
		{"if-expr-capture", "function main(): i32 { let n: i32 = 10; let x: i32 = if (n > 5) { n + 1 } else { 0 }; return x; }", 11, "", ""},
		{"if-expr-else-if", "function main(): i32 { let n: i32 = 2; let x: i32 = if (n == 1) { 10 } else if (n == 2) { 20 } else { 30 }; return x; }", 20, "", ""},
		{"direct-iife", "function main(): i32 { return ((): i32 => { return 3; })(); }", 3, "", ""},
		{"iife-with-args", "function main(): i32 { return ((a: i32, b: i32): i32 => { return a + b; })(4, 5); }", 9, "", ""},
		// Local (nested) functions — desugar to a closure-valued local.
		{"local-fn-basic", "function main(): i32 { function helper(): i32 { return 5; } return helper(); }", 5, "", ""},
		{"local-fn-capture", "function main(): i32 { let n: i32 = 10; function bump(): i32 { return n + 1; } return bump(); }", 11, "", ""},
		{"local-fn-two", "function main(): i32 { function f(): i32 { return 2; } function g(): i32 { return 3; } return f() * g(); }", 6, "", ""},
		// defer — action runs at function exit (LIFO, conditional, value
		// captured before cleanup).
		{"defer-fires", "function inc(): i32 { defer write(\"d\"); write(\"b\"); return 1; } function main(): i32 { return inc(); }", 1, "bd", ""},
		{"defer-retval-before-cleanup", "function f(): i32 { let x = 5; defer x = 99; return x; } function main(): i32 { return f(); }", 5, "", ""},
		{"defer-lifo", "function f(): i32 { defer write(\"1\"); defer write(\"2\"); return 0; } function main(): i32 { return f(); }", 0, "21", ""},
		{"defer-conditional-off", "function f(c: i32): i32 { if (c == 1) { defer write(\"d\"); } write(\"x\"); return 0; } function main(): i32 { return f(0); }", 0, "x", ""},
		{"defer-loop-survives", "function f(): i32 { defer write(\"d\"); let i = 0; while (i < 3) { write(\"l\"); i = i + 1; } return 0; } function main(): i32 { return f(); }", 0, "llld", ""},
		{"hello-world", "print(\"Hello, world!\"); return 0;", 0, "Hello, world!\n", ""},
		{"print-twice", "print(\"line 1\"); print(\"line 2\"); return 0;", 0, "line 1\nline 2\n", ""},
		{"print-then-return", "print(\"out\"); return 42;", 42, "out\n", ""},
		{
			"fizzbuzz-1-to-15",
			"function main(): i32 { " +
				"let i = 1; " +
				"while (i <= 15) { " +
				"if (i % 15 == 0) { write(\"FizzBuzz \"); } " +
				"else if (i % 3 == 0) { write(\"Fizz \"); } " +
				"else if (i % 5 == 0) { write(\"Buzz \"); } " +
				"else { write(\". \"); } " +
				"i = i + 1; " +
				"} " +
				"write(\"\\n\"); " +
				"return 0; }",
			0,
			". . Fizz . Buzz Fizz . . Fizz Buzz . Fizz . . FizzBuzz \n",
			"",
		},
		{
			"print-in-function",
			"function greet(): i32 { print(\"hi from greet\"); return 7; } " +
				"function main(): i32 { let r = greet(); return r; }",
			7,
			"hi from greet\n",
			"",
		},
		{
			"print-loop-fixed-count",
			"function main(): i32 { let i = 0; while (i < 4) { print(\"tick\"); i = i + 1; } return 0; }",
			0,
			"tick\ntick\ntick\ntick\n",
			"",
		},
		{
			"print-int-literal",
			"function main(): i32 { print_int(42); write(\"\\n\"); return 0; }",
			0,
			"42\n",
			"",
		},
		{
			"print-int-zero",
			"function main(): i32 { print_int(0); write(\"\\n\"); return 0; }",
			0,
			"0\n",
			"",
		},
		{
			"print-int-negative",
			"function main(): i32 { print_int(0 - 7); write(\"\\n\"); return 0; }",
			0,
			"-7\n",
			"",
		},
		{
			"print-int-computed",
			"function main(): i32 { print_int(2 + 3 * 4); write(\"\\n\"); return 0; }",
			0,
			"14\n",
			"",
		},
		{
			"print-int-from-function",
			"function fact(n: i32): i32 { if (n <= 1) { return 1; } return n * fact(n - 1); } " +
				"function main(): i32 { print_int(fact(8)); write(\"\\n\"); return 0; }",
			0,
			"40320\n",
			"",
		},
		{
			"print-int-counter-loop",
			"function main(): i32 { let i = 1; while (i <= 5) { print_int(i); write(\" \"); i = i + 1; } write(\"\\n\"); return 0; }",
			0,
			"1 2 3 4 5 \n",
			"",
		},
		{
			"fizzbuzz-canonical-1-to-15",
			"function main(): i32 { " +
				"let i = 1; " +
				"while (i <= 15) { " +
				"if (i % 15 == 0) { write(\"FizzBuzz\"); } " +
				"else if (i % 3 == 0) { write(\"Fizz\"); } " +
				"else if (i % 5 == 0) { write(\"Buzz\"); } " +
				"else { print_int(i); } " +
				"write(\"\\n\"); " +
				"i = i + 1; " +
				"} " +
				"return 0; }",
			0,
			"1\n2\nFizz\n4\nBuzz\nFizz\n7\n8\nFizz\nBuzz\n11\nFizz\n13\n14\nFizzBuzz\n",
			"",
		},
		{
			"fibonacci-series-first-10",
			"function fib(n: i32): i32 { if (n < 2) { return n; } return fib(n - 1) + fib(n - 2); } " +
				"function main(): i32 { let i = 0; while (i < 10) { print_int(fib(i)); write(\" \"); i = i + 1; } write(\"\\n\"); return 0; }",
			0,
			"0 1 1 2 3 5 8 13 21 34 \n",
			"",
		},
		{
			"sum-via-recursion-and-print",
			"function sum(n: i32): i32 { if (n == 0) { return 0; } return n + sum(n - 1); } " +
				"function main(): i32 { write(\"sum(1..10) = \"); print_int(sum(10)); write(\"\\n\"); return 0; }",
			0,
			"sum(1..10) = 55\n",
			"",
		},
		// read_int demos — pipe stdin via the cases struct's
		// stdin field. The inner binary reads, parses, computes,
		// and either prints the result or returns it via exit
		// code.
		{
			"read-int-double-via-exit",
			"function main(): i32 { return read_int() * 2; }",
			42,
			"",
			"21",
		},
		{
			"read-int-print-doubled",
			"function main(): i32 { let n = read_int(); print_int(n * 2); write(\"\\n\"); return 0; }",
			0,
			"50\n",
			"25",
		},
		{
			"read-int-square",
			"function main(): i32 { let n = read_int(); print_int(n * n); write(\"\\n\"); return 0; }",
			0,
			"49\n",
			"7",
		},
		{
			"read-int-negative",
			"function main(): i32 { let n = read_int(); if (n < 0) { print(\"neg\"); } else { print(\"pos\"); } return 0; }",
			0,
			"neg\n",
			"-42",
		},
		{
			"primes-up-to-30",
			"function is_prime(n: i32): i32 { if (n < 2) { return 0; } let i = 2; while (i * i <= n) { if (n % i == 0) { return 0; } i = i + 1; } return 1; } " +
				"function main(): i32 { let i = 2; while (i <= 30) { if (is_prime(i) == 1) { print_int(i); write(\" \"); } i = i + 1; } write(\"\\n\"); return 0; }",
			0,
			"2 3 5 7 11 13 17 19 23 29 \n",
			"",
		},
		{
			"squares-1-to-5",
			"function main(): i32 { let i = 1; while (i <= 5) { print_int(i * i); write(\" \"); i = i + 1; } write(\"\\n\"); return 0; }",
			0,
			"1 4 9 16 25 \n",
			"",
		},
		{
			"power-of-two",
			"function pow2(n: i32): i32 { if (n == 0) { return 1; } return 2 * pow2(n - 1); } " +
				"function main(): i32 { let i = 0; while (i <= 10) { print_int(pow2(i)); write(\" \"); i = i + 1; } write(\"\\n\"); return 0; }",
			0,
			"1 2 4 8 16 32 64 128 256 512 1024 \n",
			"",
		},
		{
			"tuple-literal-access-zero",
			"function main(): i32 { let t = (7, 11, 13); return t.0; }",
			7,
			"",
			"",
		},
		{
			// A struct element of a destructured tuple must keep its
			// type so its receiver method dispatches through the
			// shape-pointer path (__fn_Box__bump), not mis-mangle as
			// __fn_i32__bump. Regression for the tuple-destructure
			// element-typing fix.
			"tuple-destructure-struct-method",
			"struct Box { n: i32 } " +
				"pub function (b: Box) bump(): i32 { return b.n + 1; } " +
				"function pair(): (i32, Box) { return (5, Box { n: 10 }); } " +
				"function main(): i32 { let (x, b) = pair(); return b.bump(); }",
			11,
			"",
			"",
		},
		{
			// Method call with a `fn`-typed parameter: passing a bare
			// function ident must box it as a closure value, not call
			// it as a 0-arg function and pass the return value.
			// Without this `f.bench("…", n, my_workload)` silently
			// invoked my_workload zero times and the callee's `fn()`
			// crashed on the garbage closure box. Free-function calls
			// already had this; the method-call arg loop didn't.
			"method-fn-arg-boxed-not-called",
			"struct Foo { n: i32 } " +
				"pub function (f: Foo) call_one(fn: () => void): Foo { " +
				"fn(); return Foo { n: f.n + 99 }; } " +
				"function noop(): void { } " +
				"function main(): i32 { let f: Foo = Foo { n: 0 }; " +
				"f = f.call_one(noop); return f.n; }",
			99,
			"",
			"",
		},
		{
			// Receiver-method calls returning Option[T] / Result[T, E]
			// (e.g. `s.parse_int(): Option[i32]`) must surface their
			// payload type in match arms — otherwise `Some(got) =>`
			// binds got as "unknown" and got.to_string() falls to
			// struct shape-dispatch and segfaults. Regression for
			// the match_payload_type ExprFieldAccess lookup that now
			// walks s.funcs for receiver methods.
			"match-receiver-method-option-payload",
			"struct Wrap { n: i32 } " +
				"pub function (w: Wrap) try_get(): Option[i32] { " +
				"if (w.n == 0) { return None; } return Some(w.n); } " +
				"function main(): i32 { " +
				"let w: Wrap = Wrap { n: 42 }; " +
				"match (w.try_get()) { " +
				"Some(got) => { return got + 100; }, " +
				"None => { return 1; } } " +
				"return 99; }",
			142,
			"",
			"",
		},
		{
			// IEEE NaN semantics for f64 compares. Per IEEE, every
			// relation with NaN is false except `!=`. ucomisd sets
			// CF=ZF=PF=1 on unordered, so naked setb / setbe / sete
			// would mis-report `NaN < x` / `NaN <= x` / `NaN == NaN`
			// as true. `<` / `<=` / `==` now AND with `setnp`; `!=`
			// ORs with `setp` so it returns true on unordered.
			// Regression: is_nan (x != x) + strict-monotonic checks
			// (a < b with a NaN-tainted array) must agree with IEEE.
			"f64-nan-compares",
			"function main(): i32 { " +
				"let nan: f64 = 0.0 / 0.0; let one: f64 = 1.0; " +
				"if (!(nan != nan)) { return 1; } " +
				"if (nan == nan) { return 2; } " +
				"if (nan < one) { return 3; } " +
				"if (one < nan) { return 4; } " +
				"if (nan <= one) { return 5; } " +
				"if (nan > one) { return 6; } " +
				"if (nan >= one) { return 7; } " +
				"return 42; }",
			42,
			"",
			"",
		},
		{"small-builtins-roundtrip", "function main(): i32 { let a: f64 = 3.5; if (f64_from_bits(f64_bits(a)) != a) { return 1; } let b: f32 = 3.5; if (f32_from_bits(f32_bits(b)) != b) { return 2; } sleep_ms(0); match (remove_file(\"/tmp/lang-no-such-file-zzz\")) { Err(_) => {}, Ok(_) => { return 3; } } return 42; }", 42, "", ""},
		{
			// Ok(x) / Err(x) must lower as Result heap boxes (tag @0,
			// payload @8 — same shape as Some/None), not as calls to
			// __fn_Ok / __fn_Err. Without this every Fern body that
			// constructs a Result inside a function (std/fuzz, user
			// helpers, the obvious `return Ok(...)` pattern) link-errors.
			"result-ok-err-constructors",
			"function ok_path(): Result[i32, string] { return Ok(40); } " +
				"function err_path(): Result[i32, string] { return Err(\"nope\"); } " +
				"function main(): i32 { " +
				"let a: i32 = 0; let b: i32 = 0; " +
				"match (ok_path()) { Ok(v) => { a = v; }, Err(_) => { return 1; } } " +
				"match (err_path()) { Ok(_) => { return 2; }, Err(_) => { b = 2; } } " +
				"return a + b; }",
			42,
			"",
			"",
		},
		{
			"tuple-literal-access-middle",
			"function main(): i32 { let t = (7, 11, 13); return t.1; }",
			11,
			"",
			"",
		},
		{
			"tuple-literal-access-last",
			"function main(): i32 { let t = (7, 11, 13); return t.2; }",
			13,
			"",
			"",
		},
		{
			"tuple-sum-fields",
			"function main(): i32 { let t = (10, 20, 30); return t.0 + t.1 + t.2; }",
			60,
			"",
			"",
		},
		{
			"tuple-of-expressions",
			"function main(): i32 { let x = 5; let t = (x * 2, x + 1, x - 1); return t.0 + t.1 + t.2; }",
			20,
			"",
			"",
		},
		{
			"array-literal-len",
			"function main(): i32 { let a = [10, 20, 30]; return a.len(); }",
			3,
			"",
			"",
		},
		{
			"array-index-first",
			"function main(): i32 { let a = [42, 99, 7]; return a[0]; }",
			42,
			"",
			"",
		},
		{
			"array-index-middle",
			"function main(): i32 { let a = [42, 99, 7]; return a[1]; }",
			99,
			"",
			"",
		},
		{
			"array-index-last",
			"function main(): i32 { let a = [42, 99, 7]; return a[2]; }",
			7,
			"",
			"",
		},
		{
			"array-index-via-var",
			"function main(): i32 { let a = [10, 20, 30, 40]; let i = 2; return a[i]; }",
			30,
			"",
			"",
		},
		{
			"array-sum-via-while",
			"function main(): i32 { let a = [1, 2, 3, 4, 5]; let i = 0; let s = 0; while (i < a.len()) { s = s + a[i]; i = i + 1; } return s; }",
			15,
			"",
			"",
		},
		{
			"array-of-expressions",
			"function main(): i32 { let x = 4; let a = [x, x + 1, x * 2]; return a[0] + a[1] + a[2]; }",
			17,
			"",
			"",
		},
		{"match-single-variant-binding", "struct Circle { r: i32 } function main(): i32 { let c = Circle { r: 5 }; match (c) { Circle { r } => { return r; } } return 0 - 2; }", 5, "", ""},
		{
			// String-literal match arms (#4407): lower to an str_eq
			// if-else-if chain on the self-host path (build_literal_match).
			"match-string-literal-hit",
			"function main(): i32 { let s: string = \"no\"; match (s) { \"yes\" => { return 1; }, \"no\" => { return 2; }, _ => { return 9; } } return 0; }",
			2,
			"",
			"",
		},
		{
			// Exact match: \"nope\" is not \"no\", so it falls to `_`.
			"match-string-literal-wildcard",
			"function main(): i32 { let s: string = \"nope\"; match (s) { \"yes\" => { return 1; }, \"no\" => { return 2; }, _ => { return 9; } } return 0; }",
			9,
			"",
			"",
		},
		{"match-no-binding", "struct Empty { } function main(): i32 { let e = Empty { }; match (e) { Empty { } => { return 11; } } return 0 - 2; }", 11, "", ""},
		{"match-write-to-outer-var", "struct Circle { r: i32 } function main(): i32 { let c = Circle { r: 8 }; let n: i32 = 0; match (c) { Circle { r } => { n = r; } } return n; }", 8, "", ""},
		{
			"for-sum-array",
			"function main(): i32 { let a = [10, 20, 30]; let s = 0; for x in a { s = s + x; } return s; }",
			60,
			"",
			"",
		},
		{
			"for-count-iterations",
			"function main(): i32 { let a = [1, 2, 3, 4, 5]; let n = 0; for x in a { n = n + 1; } return n; }",
			5,
			"",
			"",
		},
		{
			"for-empty-array",
			"function main(): i32 { let a = [42]; let s = 0; for x in a { s = s + 1; } return s; }",
			1,
			"",
			"",
		},
		{
			"for-element-squares",
			"function main(): i32 { let a = [2, 3, 4]; let s = 0; for x in a { s = s + x * x; } return s; }",
			29,
			"",
			"",
		},
		{
			"break-in-while",
			"function main(): i32 { let i = 0; let s = 0; while (i < 100) { if (i == 5) { break; } s = s + i; i = i + 1; } return s; }",
			10,
			"",
			"",
		},
		{
			"continue-in-while",
			"function main(): i32 { let i = 0; let s = 0; while (i < 10) { i = i + 1; if (i == 5) { continue; } s = s + i; } return s; }",
			50,
			"",
			"",
		},
		{
			"break-in-for",
			"function main(): i32 { let a = [10, 20, 30, 40]; let s = 0; for x in a { if (x == 30) { break; } s = s + x; } return s; }",
			30,
			"",
			"",
		},
		{
			"continue-in-for",
			"function main(): i32 { let a = [1, 2, 3, 4, 5]; let s = 0; for x in a { if (x == 3) { continue; } s = s + x; } return s; }",
			12,
			"",
			"",
		},
		{
			"method-area-no-args",
			"struct Circle { r: i32 } function (c: Circle) area(): i32 { return c.r * c.r; } function main(): i32 { let k = Circle { r: 5 }; return k.area(); }",
			25,
			"",
			"",
		},
		{
			"method-with-args",
			"struct Box { v: i32 } function (b: Box) scale(n: i32): i32 { return b.v * n; } function main(): i32 { let x = Box { v: 4 }; return x.scale(3); }",
			12,
			"",
			"",
		},
		{
			"method-mixed-with-plain-func",
			"struct Circle { r: i32 } function (c: Circle) area(): i32 { return c.r * c.r; } function area(): i32 { return 100; } function main(): i32 { let k = Circle { r: 5 }; let m: i32 = area(); let n: i32 = k.area(); return m + n; }",
			125,
			"",
			"",
		},
		{
			"method-multi-struct-dispatch",
			"struct Circle { r: i32 } struct Square { s: i32 } function (c: Circle) area(): i32 { return c.r * c.r; } function (q: Square) area(): i32 { return q.s * q.s; } function main(): i32 { let k = Square { s: 6 }; return k.area(); }",
			36,
			"",
			"",
		},
		{
			"method-three-args",
			"struct P { x: i32 } function (p: P) f(a: i32, b: i32, c: i32): i32 { return p.x + a + b + c; } function main(): i32 { let p = P { x: 10 }; return p.f(1, 2, 3); }",
			16,
			"",
			"",
		},
		{
			"lambda-no-capture",
			"function main(): i32 { let f = (x: i32): i32 => { return x + 1; }; return f(41); }",
			42,
			"",
			"",
		},
		{
			"lambda-no-args",
			"function main(): i32 { let f = (): i32 => { return 7; }; return f(); }",
			7,
			"",
			"",
		},
		{
			"lambda-multi-args",
			"function main(): i32 { let add = (a: i32, b: i32): i32 => { return a + b; }; return add(20, 22); }",
			42,
			"",
			"",
		},
		{
			"lambda-with-locals",
			"function main(): i32 { let f = (n: i32): i32 => { let sq = n * n; let dbl = n + n; return sq + dbl; }; return f(5); }",
			35,
			"",
			"",
		},
		{
			"closure-single-capture",
			"function main(): i32 { let n = 5; let f = (x: i32): i32 => { return x + n; }; return f(7); }",
			12,
			"",
			"",
		},
		{
			"closure-multi-capture",
			"function main(): i32 { let a = 10; let b = 20; let f = (): i32 => { return a + b; }; return f(); }",
			30,
			"",
			"",
		},
		{
			// Outer reassignment after the closure is created is visible to the
			// closure — by-reference capture, matching the interpreter (#5301;
			// the pre-fix pin of 5 froze the by-value make-time snapshot).
			"closure-capture-by-reference",
			"function main(): i32 { let n = 5; let f = (): i32 => { return n; }; n = 99; return f(); }",
			99,
			"",
			"",
		},
		{
			"closure-capture-and-arg",
			"function main(): i32 { let k = 100; let g = (x: i32, y: i32): i32 => { return x + y + k; }; return g(2, 3); }",
			105,
			"",
			"",
		},
		{
			"closure-nested-recapture",
			"function main(): i32 { let n = 7; let outer = (): i32 => { let inner = (): i32 => { return n; }; return inner(); }; return outer(); }",
			7,
			"",
			"",
		},
		{
			"plain-string-fn-result-print",
			"function get(): string { return \"hi\"; } function main(): i32 { write(get()); return 0; }",
			0,
			"hi",
			"",
		},
		{
			"closure-string-capture",
			"function main(): i32 { let s = \"hi\"; let f = (): string => { return s; }; write(f()); return 0; }",
			0,
			"hi",
			"",
		},
		{
			"closure-string-concat-capture",
			"function main(): i32 { let prefix = \"hello-\"; let f = (suf: string): string => { return prefix + suf; }; write(f(\"world\")); return 0; }",
			0,
			"hello-world",
			"",
		},
		{
			"closure-bool-returning",
			"function main(): i32 { let x = 5; let is_big = (): boolean => { return x > 3; }; if (is_big()) { return 1; } return 0; }",
			1,
			"",
			"",
		},
		{
			"closure-string-multi-capture",
			"function main(): i32 { let a = \"foo\"; let b = \"bar\"; let f = (): string => { return a + b; }; write(f()); return 0; }",
			0,
			"foobar",
			"",
		},
		{
			"closure-i32-returning-string-capture",
			"function main(): i32 { let s = \"hello\"; let f = (): i32 => { return s.len(); }; return f(); }",
			5,
			"",
			"",
		},
		{
			"arr-i32-push-len",
			"function main(): i32 { let xs: i32[] = [1, 2, 3]; xs = xs.append(4); return xs.len(); }",
			4,
			"",
			"",
		},
		{
			"arr-i32-push-last",
			"function main(): i32 { let xs: i32[] = [10, 20, 30]; xs = xs.append(99); return xs[3]; }",
			99,
			"",
			"",
		},
		{
			"arr-i32-push-preserves-existing",
			"function main(): i32 { let xs: i32[] = [1, 2, 3]; xs = xs.append(4); return xs[0] + xs[1] + xs[2] + xs[3]; }",
			10,
			"",
			"",
		},
		{
			"arr-i32-push-empty",
			"function main(): i32 { let xs: i32[] = []; xs = xs.append(42); return xs[0]; }",
			42,
			"",
			"",
		},
		{
			"arr-string-push",
			"function main(): i32 { let xs: string[] = [\"a\", \"b\"]; xs = xs.append(\"c\"); for s in xs { write(s); } return xs.len(); }",
			3,
			"abc",
			"",
		},
		{
			"arr-i32-push-chain",
			"function main(): i32 { let xs: i32[] = []; xs = xs.append(1); xs = xs.append(2); xs = xs.append(3); return xs[0] + xs[1] + xs[2]; }",
			6,
			"",
			"",
		},
		{
			"str-method-split-len",
			"import \"std/string\"; function main(): i32 { let s = \"a,b,c\"; let parts = s.split(\",\"); return parts.len(); }",
			3,
			"",
			"",
		},
		{
			"str-method-split-content",
			"import \"std/string\"; function main(): i32 { let s = \"x|y|z\"; let parts = s.split(\"|\"); for t in parts { write(t); } return 0; }",
			0,
			"xyz",
			"",
		},
		{
			"str-method-trim",
			"import \"std/string\"; function main(): i32 { let s = \"  hi  \"; let t = s.trim(); write(t); return t.len(); }",
			2,
			"hi",
			"",
		},
		{
			"str-method-to-upper",
			"import \"std/string\"; function main(): i32 { let s = \"hello\"; write(s.to_ascii_upper()); return 0; }",
			0,
			"HELLO",
			"",
		},
		{
			"str-method-to-lower",
			"import \"std/string\"; function main(): i32 { let s = \"HELLO\"; write(s.to_ascii_lower()); return 0; }",
			0,
			"hello",
			"",
		},
		{
			"str-method-repeat",
			"import \"std/string\"; function main(): i32 { let s = \"ab\"; write(s.repeat(3)); return 0; }",
			0,
			"ababab",
			"",
		},
		{
			"str-method-replace",
			"import \"std/string\"; function main(): i32 { let s = \"hello\"; write(s.replace(\"l\", \"L\")); return 0; }",
			0,
			"heLLo",
			"",
		},
		{
			"str-method-chain",
			"import \"std/string\"; function main(): i32 { let s = \"  HELLO WORLD  \"; write(s.trim().to_ascii_lower()); return 0; }",
			0,
			"hello world",
			"",
		},
		{
			"str-literal-method-direct",
			"import \"std/string\"; function main(): i32 { write(\"hello\".to_ascii_upper()); return 0; }",
			0,
			"HELLO",
			"",
		},
		{
			"str-literal-method-chain",
			"import \"std/string\"; function main(): i32 { write(\"  HI  \".trim().repeat(2)); return 0; }",
			0,
			"HIHI",
			"",
		},
		{
			"arr-i32-slice-len",
			"function main(): i32 { let xs: i32[] = [10, 20, 30, 40, 50]; let ys = xs[1:4]; return ys.len(); }",
			3,
			"",
			"",
		},
		{
			"arr-i32-slice-content",
			"function main(): i32 { let xs: i32[] = [10, 20, 30, 40, 50]; let ys = xs[1:4]; return ys[0] + ys[1] + ys[2]; }",
			90,
			"",
			"",
		},
		{
			"arr-i32-slice-empty",
			"function main(): i32 { let xs: i32[] = [1, 2, 3]; let ys = xs[1:1]; return ys.len(); }",
			0,
			"",
			"",
		},
		{
			"arr-i32-slice-full",
			"function main(): i32 { let xs: i32[] = [7, 8, 9]; let ys = xs[0:3]; return ys[0] + ys[1] + ys[2]; }",
			24,
			"",
			"",
		},
		{
			"arr-string-slice",
			"function main(): i32 { let xs: string[] = [\"a\", \"b\", \"c\", \"d\"]; let ys = xs[1:3]; for s in ys { write(s); } return ys.len(); }",
			2,
			"bc",
			"",
		},
		// 64-bit-element arrays (i64[] / f64[]): the native backend already
		// uses 8-byte element slots, so values above 2^31 round-trip.
		// Mirrors the wasm i64arr-* cases.
		{
			"arr-i64-literal-index-large",
			"function main(): i32 { let xs: i64[] = [5000000000, 42]; if (xs[0] == 5000000000) { return xs[1] as i32; } return 0; }",
			42,
			"",
			"",
		},
		{
			"arr-i64-for-sum",
			"function main(): i32 { let xs: i64[] = [3, 5, 90]; let s: i64 = 0; for v in xs { s = s + v; } return s as i32; }",
			98,
			"",
			"",
		},
		{"arr-i64-set-index-large", "function main(): i32 { let xs: i64[] = [1, 2, 3]; xs = xs.with(1, 5000000000); if (xs[1] == 5000000000) { return 7; } return 0; }", 7, "", ""},
		{
			"arr-i64-push-grow",
			"function main(): i32 { let xs: i64[] = [10]; xs = xs.append(20); xs = xs.append(5000000000); if (xs[2] == 5000000000) { return (xs[0] + xs[1]) as i32; } return 0; }",
			30,
			"",
			"",
		},
		{
			"arr-i64-slice",
			"function main(): i32 { let xs: i64[] = [10, 20, 30, 40]; let ys = xs[1:3]; return (ys[0] + ys[1]) as i32; }",
			50,
			"",
			"",
		},
		{
			"arr-f64-for-sum",
			"function main(): i32 { let xs: f64[] = [1.5, 2.5, 3.0]; let s: f64 = 0.0; for v in xs { s = s + v; } return s as i32; }",
			7,
			"",
			"",
		},
		{
			"arr-string-join-basic",
			"import \"std/array\"; function main(): i32 { let xs: string[] = [\"a\", \"b\", \"c\"]; write(xs.join(\",\")); return 0; }",
			0,
			"a,b,c",
			"",
		},
		{
			"arr-string-join-empty",
			"import \"std/array\"; function main(): i32 { let xs: string[] = []; let r = xs.join(\",\"); write(\"[\"); write(r); write(\"]\"); return r.len(); }",
			0,
			"[]",
			"",
		},
		{
			"arr-string-join-single",
			"import \"std/array\"; function main(): i32 { let xs: string[] = [\"solo\"]; write(xs.join(\",\")); return 0; }",
			0,
			"solo",
			"",
		},
		{
			"arr-string-join-empty-sep",
			"import \"std/array\"; function main(): i32 { let xs: string[] = [\"a\", \"b\", \"c\"]; write(xs.join(\"\")); return 0; }",
			0,
			"abc",
			"",
		},
		{
			"arr-string-join-multi-char-sep",
			"import \"std/array\"; function main(): i32 { let xs: string[] = [\"x\", \"y\", \"z\"]; write(xs.join(\" - \")); return 0; }",
			0,
			"x - y - z",
			"",
		},
		{
			"str-method-len",
			"function main(): i32 { let s = \"hello\"; return s.len(); }",
			5,
			"",
			"",
		},
		{
			"arr-method-len",
			"function main(): i32 { let xs: i32[] = [10, 20, 30, 40]; return xs.len(); }",
			4,
			"",
			"",
		},
		{
			"arr-string-method-len",
			"function main(): i32 { let xs: string[] = [\"a\", \"b\"]; return xs.len(); }",
			2,
			"",
			"",
		},
		{"str-first-byte", "function main(): i32 { let s = \"abc\"; return s[0] as i32; }", 97, "", ""},
		{"str-last-byte", "function main(): i32 { let s = \"abc\"; return s[s.len() - 1] as i32; }", 99, "", ""},
		{"str-first-byte-uppercase", "function main(): i32 { let s = \"Hello\"; return s[0] as i32; }", 72, "", ""},
		{"str-last-byte-symbol", "function main(): i32 { let s = \"hi!\"; return s[s.len() - 1] as i32; }", 33, "", ""},
		{
			"str-bytes-len",
			"import \"std/string\"; function main(): i32 { let s = \"abc\"; let bs = s.bytes(); return bs.len(); }",
			3,
			"",
			"",
		},
		{
			"str-bytes-empty",
			"import \"std/string\"; function main(): i32 { let s = \"\"; let bs = s.bytes(); return bs.len(); }",
			0,
			"",
			"",
		},
		{
			"str-lines-count",
			"import \"std/string\"; function main(): i32 { let s = \"line1\\nline2\\nline3\"; let ls = s.lines(); return ls.len(); }",
			3,
			"",
			"",
		},
		{
			"str-lines-content",
			"import \"std/string\"; function main(): i32 { let s = \"foo\\nbar\"; let ls = s.lines(); for l in ls { write(l); write(\"|\"); } return 0; }",
			0,
			"foo|bar|",
			"",
		},
		{
			"str-lines-single",
			"import \"std/string\"; function main(): i32 { let s = \"alone\"; let ls = s.lines(); return ls.len(); }",
			1,
			"",
			"",
		},
		{
			"str-lines-trailing-newline",
			"import \"std/string\"; function main(): i32 { let s = \"x\\n\"; let ls = s.lines(); return ls.len(); }",
			1,
			"",
			"",
		},
		{
			"closure-captures-closure-i32",
			"function main(): i32 { let inner = (): i32 => { return 42; }; let outer = (): i32 => { return inner(); }; return outer(); }",
			42,
			"",
			"",
		},
		{
			"closure-captures-string-closure",
			"function main(): i32 { let hello = (): string => { return \"hi\"; }; let wrap = (): string => { return hello() + \"!\"; }; write(wrap()); return 0; }",
			0,
			"hi!",
			"",
		},
		{
			"closure-nested-three-deep",
			"function main(): i32 { let k = 100; let a = (): i32 => { let b = (): i32 => { let c = (): i32 => { return k; }; return c(); }; return b(); }; return a(); }",
			100,
			"",
			"",
		},
		{
			"string-len-literal",
			"function main(): i32 { let s = \"hello\"; return s.len(); }",
			5,
			"",
			"",
		},
		// String indexing `s[i]` -> the i32 byte value (str_index op).
		{"str-index-local", "function main(): i32 { let s = \"hello\"; return s[0] as i32; }", 104, "", ""},
		{"str-index-sum", "function main(): i32 { let s = \"hello\"; return (s[1] as i32) + (s[4] as i32); }", 212, "", ""},
		{"str-index-loop", "function main(): i32 { let s = \"abc\"; let sum = 0; let i = 0; while (i < 3) { sum = sum + (s[i] as i32); i = i + 1; } return sum % 200; }", 94, "", ""},
		{"str-index-param", "function first(s: string): i32 { return s[0] as i32; } function main(): i32 { return first(\"Z\"); }", 90, "", ""},
		{"str-index-literal", "function main(): i32 { return \"Q\"[0] as i32; }", 81, "", ""},
		{"str-slice-len", "function main(): i32 { let s = \"hello\"; let t = slice_unchecked(s, 1, 4); return t.len(); }", 3, "", ""},
		{"str-slice-idx0", "function main(): i32 { let s = \"hello\"; let t = slice_unchecked(s, 1, 4); return t[0] as i32; }", 101, "", ""},
		{"str-slice-chain", "function main(): i32 { return slice_unchecked(\"hello\", 1, 4)[2] as i32; }", 108, "", ""},
		{"str-slice-param", "function tok(s: string): i32 { return slice_unchecked(s, 0, 2).len(); } function main(): i32 { return tok(\"abcd\"); }", 2, "", ""},
		{
			"string-print-ident",
			"function main(): i32 { let s = \"world\\n\"; write(s); return 0; }",
			0,
			"world\n",
			"",
		},
		{
			"string-byte-indexing",
			"function main(): i32 { let s = \"abc\"; return s[1] as i32; }",
			98,
			"",
			"",
		},
		{
			"string-concat",
			"function main(): i32 { let a = \"hi \"; let b = \"there\"; let c = a + b; write(c); return c.len(); }",
			8,
			"hi there",
			"",
		},
		{
			"string-eq-true",
			"function main(): i32 { let a = \"foo\"; let b = \"foo\"; if (a == b) { return 1; } return 0; }",
			1,
			"",
			"",
		},
		{
			"string-eq-false",
			"function main(): i32 { let a = \"foo\"; let b = \"bar\"; if (a == b) { return 1; } return 0; }",
			0,
			"",
			"",
		},
		{
			"string-slice",
			"function main(): i32 { let s = \"hello world\"; let sub = slice_unchecked(s, 6, 11); write(sub); return sub.len(); }",
			5,
			"world",
			"",
		},
		{
			"string-param-print",
			"function greet(s: string): i32 { write(s); return 0; } function main(): i32 { greet(\"hi!\\n\"); return 0; }",
			0,
			"hi!\n",
			"",
		},
		{
			"string-param-len",
			"function strlen(s: string): i32 { return s.len(); } function main(): i32 { return strlen(\"abcdef\"); }",
			6,
			"",
			"",
		},
		{
			"string-param-concat",
			"function shout(s: string): string { return s + \"!\"; } function main(): i32 { let out = shout(\"hi\"); write(out); return out.len(); }",
			3,
			"hi!",
			"",
		},
		{
			"string-lt-true",
			"function main(): i32 { let a = \"apple\"; let b = \"banana\"; if (a < b) { return 1; } return 0; }",
			1,
			"",
			"",
		},
		{
			"string-lt-false",
			"function main(): i32 { let a = \"banana\"; let b = \"apple\"; if (a < b) { return 1; } return 0; }",
			0,
			"",
			"",
		},
		{
			"string-lt-prefix",
			"function main(): i32 { let a = \"app\"; let b = \"apple\"; if (a < b) { return 1; } return 0; }",
			1,
			"",
			"",
		},
		{
			"string-le-equal",
			"function main(): i32 { let a = \"abc\"; let b = \"abc\"; if (a <= b) { return 1; } return 0; }",
			1,
			"",
			"",
		},
		{
			"string-gt-true",
			"function main(): i32 { let a = \"zebra\"; let b = \"apple\"; if (a > b) { return 1; } return 0; }",
			1,
			"",
			"",
		},
		{
			"string-ge-equal",
			"function main(): i32 { let a = \"xy\"; let b = \"xy\"; if (a >= b) { return 1; } return 0; }",
			1,
			"",
			"",
		},
		// read_line() returns Option[string] (#4369): Some(line INCLUDING the
		// trailing '\n') / None at EOF. `s.len()` therefore counts the newline.
		{
			"read-line-len",
			"function main(): i32 { match (read_line()) { Some(s) => { return s.len(); }, None => { return 0; }, } }",
			6,
			"",
			"hello\n",
		},
		{
			"read-line-echo",
			"function main(): i32 { match (read_line()) { Some(s) => { write(s); return 0; }, None => { return 1; }, } }",
			0,
			"world\n",
			"world\n",
		},
		{
			"read-line-compare",
			"function main(): i32 { match (read_line()) { Some(s) => { if (s == \"yes\\n\") { return 1; } return 0; }, None => { return 0; }, } }",
			1,
			"",
			"yes\n",
		},
		{
			"string-array-literal-print",
			"function main(): i32 { let arr = [\"hi\", \"bye\"]; write(arr[0]); write(\"\\n\"); write(arr[1]); write(\"\\n\"); return 0; }",
			0,
			"hi\nbye\n",
			"",
		},
		{
			"string-array-len-and-index",
			"function main(): i32 { let arr = [\"a\", \"bb\", \"ccc\"]; return arr[1].len() + arr.len() * 10; }",
			32,
			"",
			"",
		},
		// Scalar-field structs via the IR path (struct_make / struct_get,
		// leak-only): literal construction + field read, field-order independence,
		// struct params, boolean fields. Exit codes must be exact.
		{"struct-lit-fields", "struct P { x: i32, y: i32 } function main(): i32 { let p = P { x: 3, y: 4 }; return p.x + p.y; }", 7, "", ""},
		{"struct-field-order", "struct P { x: i32, y: i32 } function main(): i32 { let p = P { y: 40, x: 2 }; return p.x + p.y; }", 42, "", ""},
		{"struct-one-field", "struct W { n: i32 } function main(): i32 { let w = W { n: 99 }; return w.n; }", 99, "", ""},
		{"struct-three-fields", "struct V { a: i32, b: i32, c: i32 } function main(): i32 { let v = V { a: 1, b: 2, c: 3 }; return v.a * 100 + v.b * 10 + v.c; }", 123, "", ""},
		{"struct-field-in-expr", "struct P { x: i32, y: i32 } function main(): i32 { let p = P { x: 5, y: 6 }; if (p.x < p.y) { return p.y - p.x; } return 0; }", 1, "", ""},
		{"struct-param", "struct P { x: i32, y: i32 } function sum(p: P): i32 { return p.x + p.y; } function main(): i32 { let p = P { x: 30, y: 12 }; return sum(p); }", 42, "", ""},
		{"struct-bool-field", "struct F { on: boolean, n: i32 } function main(): i32 { let f = F { on: true, n: 7 }; if (f.on) { return f.n; } return 0; }", 7, "", ""},
		{"struct-two-instances", "struct P { x: i32, y: i32 } function main(): i32 { let a = P { x: 1, y: 2 }; let b = P { x: 10, y: 20 }; return a.x + b.y; }", 21, "", ""},
		{"struct-in-loop", "struct P { x: i32, y: i32 } function main(): i32 { let s = 0; let i = 0; while (i < 4) { let p = P { x: i, y: i * 2 }; s = s + p.x + p.y; i = i + 1; } return s; }", 18, "", ""},
		{"struct-field-from-expr", "struct P { x: i32, y: i32 } function main(): i32 { let n = 5; let p = P { x: n * 2, y: n + 1 }; return p.x + p.y; }", 16, "", ""},
		// Functional struct update `P { ...base, f: v }` via the IR path
		// (desugars to struct_make with struct_get copying non-overridden fields
		// from the base). The base must be a simple ident; mutation still bails.
		{"struct-update-one", "struct P { x: i32, y: i32 } function main(): i32 { let p = P { x: 1, y: 2 }; let q = P { ...p, y: 9 }; return q.x + q.y; }", 10, "", ""},
		{"struct-update-first", "struct P { x: i32, y: i32 } function main(): i32 { let p = P { x: 1, y: 2 }; let q = P { ...p, x: 40 }; return q.x + q.y; }", 42, "", ""},
		// 3-field updates (exit codes are u8, so keep results < 256): the override
		// can be the first / middle / last field — non-overridden fields copy from
		// the base via struct_get (each lowered field pushes exactly one value).
		{"struct-update-mid", "struct V { a: i32, b: i32, c: i32 } function main(): i32 { let v = V { a: 1, b: 2, c: 3 }; let w = V { ...v, b: 20 }; return w.a + w.b + w.c; }", 24, "", ""},
		{"struct-update-none", "struct V { a: i32, b: i32, c: i32 } function main(): i32 { let v = V { a: 1, b: 2, c: 3 }; let w = V { ...v }; return w.a + w.b + w.c; }", 6, "", ""},
		{"struct-update-3a", "struct V { a: i32, b: i32, c: i32 } function main(): i32 { let v = V { a: 1, b: 2, c: 3 }; let w = V { ...v, a: 50 }; return w.a + w.b + w.c; }", 55, "", ""},
		{"struct-update-3c", "struct V { a: i32, b: i32, c: i32 } function main(): i32 { let v = V { a: 1, b: 2, c: 3 }; let w = V { ...v, c: 90 }; return w.a + w.b + w.c; }", 93, "", ""},
		{"struct-update-keeps-base", "struct P { x: i32, y: i32 } function main(): i32 { let p = P { x: 5, y: 6 }; let q = P { ...p, x: 50 }; return p.x + q.x; }", 55, "", ""},
		// A field rebound through a struct-update literal.
		{"field-mutate", "struct P { x: i32, y: i32 } function main(): i32 { let p = P { x: 1, y: 2 }; p = P { ...p, x: 40 }; return p.x + p.y; }", 42, "", ""},
		{"field-mutate-both", "struct P { x: i32, y: i32 } function main(): i32 { let p = P { x: 0, y: 0 }; p = P { ...p, x: 30 }; p = P { ...p, y: 12 }; return p.x + p.y; }", 42, "", ""},
		{"field-mutate-loop", "struct C { n: i32 } function main(): i32 { let c = C { n: 0 }; let i = 0; while (i < 5) { c = C { ...c, n: c.n + i }; i = i + 1; } return c.n; }", 10, "", ""},
		{"field-mutate-alias", "struct P { x: i32 } function main(): i32 { let p = P { x: 1 }; let q = p; q = P { ...q, x: 9 }; return p.x; }", 1, "", ""},
		// String-returning functions (str_ret_fns tracking; box leaks).
		{"str-return", "function greet(): string { return \"hi\"; } function main(): i32 { let s = greet(); return s.len(); }", 2, "", ""},
		{"str-return-concat", "function shout(s: string): string { return s + \"!\"; } function main(): i32 { let g = shout(\"hey\"); return g.len(); }", 4, "", ""},
		// String-typed struct/enum fields (leak-safe — strings never freed, no RC).
		{"struct-str-field", "struct Token { text: string, kind: i32 } function main(): i32 { let t = Token { text: \"hello\", kind: 7 }; return t.text.len() + t.kind; }", 12, "", ""},
		{"struct-str-method", "struct N { s: string } function (n: N) sz(): i32 { return n.s.len(); } function main(): i32 { let x = N { s: \"abcd\" }; return x.sz(); }", 4, "", ""},
		{"struct-str-mutate", "struct N { s: string } function main(): i32 { let n = N { s: \"a\" }; n = N { ...n, s: \"abcde\" }; return n.s.len(); }", 5, "", ""},
		{"enum-str-payload", "enum T { Word(string), Eof } function g(t: T): i32 { match (t) { Word(w) => { return w.len(); }, Eof => { return 3; } } return 0; } function main(): i32 { return g(Word(\"hello\")) + g(Eof); }", 8, "", ""},
		{"match-guard-fallthrough", "enum E { Pos(i32), Neg(i32), Zero } function f(e: E): i32 { match (e) { Pos(n) when n > 10 => { return 1; }, Pos(n) => { return 2; }, _ => { return 3; } } return 0; } function main(): i32 { return f(Pos(20)) * 100 + f(Pos(5)) * 10 + f(Zero); }", 123, "", ""},
		{"match-guard-mixed", "enum E { A(i32), B } function f(e: E): i32 { match (e) { A(n) when n > 3 => { return n * 2; }, A(n) => { return n; }, B => { return 99; } } return 0; } function main(): i32 { return f(A(5)) + f(A(1)) + f(B); }", 110, "", ""},
		{"opt-some-none", "function classify(n: i32): Option[i32] { if (n > 0) { return Some(n); } return None; } function f(n: i32): i32 { match (classify(n)) { Some(_) => { return 1; }, None => { return 0; } } return 9; } function main(): i32 { return f(5) * 10 + f(0); }", 10, "", ""},
		{"opt-ok-err", "function chk(n: i32): Result[i32, i32] { if (n > 0) { return Ok(n); } return Err(n); } function f(n: i32): i32 { match (chk(n)) { Ok(_) => { return 7; }, Err(_) => { return 3; } } return 9; } function main(): i32 { return f(2) * 10 + f(0); }", 73, "", ""},
		{"opt-none-first", "function g(n: i32): Option[i32] { if (n > 5) { return Some(n); } return None; } function f(n: i32): i32 { match (g(n)) { None => { return 4; }, Some(_) => { return 8; } } return 0; } function main(): i32 { return f(9) + f(1); }", 12, "", ""},
		{"opt-bind-some", "function g(n: i32): Option[i32] { if (n > 0) { return Some(n + 100); } return None; } function f(n: i32): i32 { match (g(n)) { Some(x) => { return x; }, None => { return 0; } } return 0; } function main(): i32 { return f(5); }", 105, "", ""},
		{"opt-bind-result", "function chk(n: i32): Result[i32, i32] { if (n > 0) { return Ok(n * 2); } return Err(n + 50); } function f(n: i32): i32 { match (chk(n)) { Ok(x) => { return x; }, Err(e) => { return e; } } return 0; } function main(): i32 { return f(3) + f(0); }", 56, "", ""},
		{"opt-bind-guard", "function g(n: i32): Option[i32] { if (n > 0) { return Some(n); } return None; } function f(n: i32): i32 { match (g(n)) { Some(x) when x > 10 => { return 1; }, Some(x) => { return x; }, None => { return 0; } } return 0; } function main(): i32 { return f(20) * 100 + f(5) * 10 + f(0); }", 150, "", ""},
		{"opt-bind-string", "function name(n: i32): Option[string] { if (n > 0) { return Some(\"hello\"); } return None; } function f(n: i32): i32 { match (name(n)) { Some(s) => { return s.len(); }, None => { return 0; } } return 0; } function main(): i32 { return f(1); }", 5, "", ""},
		{"opt-bind-result-strerr", "function chk(n: i32): Result[i32, string] { if (n > 0) { return Ok(n); } return Err(\"fail\"); } function f(n: i32): i32 { match (chk(n)) { Ok(x) => { return x; }, Err(e) => { return e.len(); } } return 0; } function main(): i32 { return f(7) * 10 + f(0); }", 74, "", ""},
		{"opt-bind-local", "function g(n: i32): Option[i32] { if (n > 0) { return Some(n + 100); } return None; } function f(n: i32): i32 { let r = g(n); match (r) { Some(x) => { return x; }, None => { return 0; } } return 0; } function main(): i32 { return f(5); }", 105, "", ""},
		{"opt-bind-local-strerr", "function chk(n: i32): Result[i32, string] { if (n > 0) { return Ok(n); } return Err(\"oops\"); } function f(n: i32): i32 { let r = chk(n); match (r) { Ok(x) => { return x; }, Err(e) => { return e.len(); } } return 0; } function main(): i32 { return f(7) * 10 + f(0); }", 74, "", ""},
		{"opt-bind-param", "function f(o: Option[i32]): i32 { match (o) { Some(x) => { return x * 2; }, None => { return 0; } } return 0; } function main(): i32 { return f(Some(21)) + f(None); }", 42, "", ""},
		{"struct-field-nested", "struct Point { x: i32, y: i32 } struct Box { p: Point } function bx(b: Box): i32 { return b.p.x + b.p.y; } function main(): i32 { let b = Box { p: Point { x: 30, y: 12 } }; return bx(b); }", 42, "", ""},
		{"struct-field-deep", "struct Inner { v: i32 } struct Mid { inner: Inner, n: i32 } struct Outer { mid: Mid } function f(o: Outer): i32 { return o.mid.inner.v + o.mid.n; } function main(): i32 { let o = Outer { mid: Mid { inner: Inner { v: 100 }, n: 5 } }; return f(o); }", 105, "", ""},
		{"struct-field-bind", "struct Point { x: i32, y: i32 } struct Box { p: Point, tag: i32 } function main(): i32 { let b = Box { p: Point { x: 7, y: 8 }, tag: 3 }; let pp = b.p; return pp.x * pp.y + b.tag; }", 59, "", ""},
		{"forin-i32", "function main(): i32 { let xs = [10, 20, 30, 40]; let sum = 0; for x in xs { sum = sum + x; } return sum; }", 100, "", ""},
		{"forin-i32-param", "function total(xs: i32[]): i32 { let s = 0; for v in xs { s = s + v; } return s; } function main(): i32 { let a = [1, 2, 3, 4, 5]; return total(a); }", 15, "", ""},
		{"forin-nested", "function main(): i32 { let xs = [1, 2, 3]; let t = 0; for a in xs { for b in xs { t = t + a * b; } } return t; }", 36, "", ""},
		{"forin-string", "function main(): i32 { let ss: string[] = [\"a\", \"bb\", \"ccc\", \"dddd\"]; let n = 0; for s in ss { n = n + s.len(); } return n; }", 10, "", ""},
		{"enum-struct-payload", "struct BinExpr { left: i32, right: i32 } enum Expr { Lit(i32), Binary(BinExpr) } function eval(e: Expr): i32 { match (e) { Lit(n) => { return n; }, Binary(b) => { return b.left + b.right; } } return 0; } function main(): i32 { return eval(Lit(7)) + eval(Binary(BinExpr { left: 3, right: 9 })); }", 19, "", ""},
		{"enum-struct-payload-guard", "struct P { x: i32, y: i32 } enum Shape { Rect(P), Dot } function area(s: Shape): i32 { match (s) { Rect(p) when p.x > 0 => { return p.x * p.y; }, _ => { return 0; } } return 0; } function main(): i32 { return area(Rect(P { x: 4, y: 5 })); }", 20, "", ""},
		{"enum-struct-payload-nested", "struct Inner { v: i32 } struct Mid { i: Inner } enum E { A(Mid), B } function f(e: E): i32 { match (e) { A(m) => { return m.i.v; }, B => { return 9; } } return 0; } function main(): i32 { return f(A(Mid { i: Inner { v: 42 } })) + f(B); }", 51, "", ""},
		{"enum-arr-payload-len", "enum E { Items(i32[]), Empty } function f(e: E): i32 { match (e) { Items(xs) => { return xs.len(); }, Empty => { return 0; } } return 0; } function main(): i32 { return f(Items([10, 20, 30])) * 10 + f(Empty); }", 30, "", ""},
		{"enum-arr-payload-forin", "enum E { Items(i32[]), Empty } function sum(e: E): i32 { match (e) { Items(xs) => { let t = 0; for x in xs { t = t + x; } return t; }, Empty => { return 0; } } return 0; } function main(): i32 { return sum(Items([5, 10, 15])); }", 30, "", ""},
		{"enum-arr-payload-alias", "enum E { Items(i32[]), Empty } function f(e: E): i32 { match (e) { Items(xs) => { return xs.len() + xs[0]; }, Empty => { return 0; } } return 0; } function main(): i32 { let a = [7, 8, 9]; return f(Items(a)); }", 10, "", ""},
		{"enum-strarr-payload-len", "enum E { Words(string[]), NoWords } function f(e: E): i32 { match (e) { Words(w) => { return w.len(); }, NoWords => { return 0; } } return 0; } function main(): i32 { return f(Words([\"a\", \"bb\", \"ccc\"])) * 10 + f(NoWords); }", 30, "", ""},
		{"enum-strarr-payload-forin", "enum E { Words(string[]), None } function f(e: E): i32 { match (e) { Words(w) => { let n = 0; for s in w { n = n + s.len(); } return n; }, None => { return 0; } } return 0; } function main(): i32 { return f(Words([\"a\", \"bb\", \"ccc\"])); }", 6, "", ""},
		{"struct-strarr-field-len", "struct Doc { lines: string[] } function nl(d: Doc): i32 { return d.lines.len(); } function main(): i32 { let d = Doc { lines: [\"x\", \"y\", \"z\"] }; return nl(d); }", 3, "", ""},
		{"struct-strarr-field-index", "struct Doc { lines: string[] } function f(d: Doc): i32 { return d.lines[1].len(); } function main(): i32 { let d = Doc { lines: [\"a\", \"bb\", \"ccc\"] }; return f(d); }", 2, "", ""},
		{"tuple-str-i32-dotn", "function main(): i32 { let t = (\"hello\", 7); return t.0.len() + t.1; }", 12, "", ""},
		{"tuple-str-i32-destructure", "function main(): i32 { let (a, b) = (\"world\", 3); return a.len() + b; }", 8, "", ""},
		{"tuple-struct-dotn", "struct P { x: i32, y: i32 } function main(): i32 { let t = (P { x: 4, y: 5 }, 2); return t.0.x * t.0.y + t.1; }", 22, "", ""},
		{"tuple-local-destructure", "function main(): i32 { let t = (\"ab\", 10); let (s, n) = t; return s.len() + n; }", 12, "", ""},
		{"tuple-3-destructure", "function main(): i32 { let (a, b, c) = (1, 2, 3); return a * 100 + b * 10 + c; }", 123, "", ""},
		{"tuple-4-destructure", "function main(): i32 { let (a, b, c, d) = (1, 2, 3, 4); return a + b + c + d; }", 10, "", ""},
		{"tuple-3-mixed-destructure", "function main(): i32 { let (s, n, m) = (\"hi\", 5, 10); return s.len() + n + m; }", 17, "", ""},
		{"tuple-3-local-destructure", "function main(): i32 { let t = (7, 8, 9); let (a, b, c) = t; return a + b * c; }", 79, "", ""},
		{"tuple-3-ret-destructure", "function three(): (i32, string, i32) { return (4, \"abc\", 6); } function main(): i32 { let (a, s, b) = three(); return a + s.len() + b; }", 13, "", ""},
		{"tuple-3-ret-dotn", "function three(): (i32, i32, i32) { return (4, 5, 6); } function main(): i32 { let t = three(); return t.0 * 100 + t.1 * 10 + t.2; }", 200, "", ""},
		{"tuple-3-ret-destructure-i32", "function three(): (i32, i32, i32) { return (4, 5, 6); } function main(): i32 { let (a, b, c) = three(); return a * 100 + b * 10 + c; }", 200, "", ""},
		{"i64-cmp", "function main(): i32 { let x: i64 = 5000000000; let y: i64 = 4000000000; if (x > y) { return 7; } return 0; }", 7, "", ""},
		{"i64-add", "function main(): i32 { let a: i64 = 3000000000; let b: i64 = 3000000000; let c: i64 = a + b; if (c > 5000000000) { return 11; } return 0; }", 11, "", ""},
		{"i64-mul", "function main(): i32 { let a: i64 = 100000; let b: i64 = 100000; let c: i64 = a * b; if (c > 4000000000) { return 5; } return 0; }", 5, "", ""},
		{"i64-sub-neg", "function main(): i32 { let a: i64 = 1000000000; let b: i64 = 2000000000; let c: i64 = a - b; if (c < 0) { return 9; } return 0; }", 9, "", ""},
		{"i64-loop", "function main(): i32 { let s: i64 = 0; let i: i32 = 0; while (i < 100000) { s = s + 100000; i = i + 1; } if (s > 4000000000) { return 13; } return 0; }", 13, "", ""},
		{"and-true", "function main(): i32 { let x = 5; if (x > 0 && x < 10) { return 7; } return 0; }", 7, "", ""},
		{"and-false", "function main(): i32 { let x = 15; if (x > 0 && x < 10) { return 7; } return 0; }", 0, "", ""},
		{"or-true", "function main(): i32 { let x = 15; if (x < 0 || x > 0) { return 3; } return 0; }", 3, "", ""},
		{"and-or-nest", "function main(): i32 { let a = 1; let b = 0; let c = 5; if (a > 0 && b > 0 || c > 0) { return 9; } return 0; }", 9, "", ""},
		{"and-not-operand", "function main(): i32 { let x = 5; if (!(x > 10) && x > 0) { return 4; } return 0; }", 4, "", ""},
		{"and-bool-vars", "function main(): i32 { let f = 5 > 3; let g = 2 > 8; if (f && !g) { return 6; } return 0; }", 6, "", ""},
		{"strcmp-lt", "function main(): i32 { let a = \"apple\"; let b = \"banana\"; if (a < b) { return 7; } return 0; }", 7, "", ""},
		{"strcmp-gt", "function main(): i32 { let a = \"banana\"; let b = \"apple\"; if (a > b) { return 3; } return 0; }", 3, "", ""},
		{"strcmp-le-eq", "function main(): i32 { let a = \"abc\"; let b = \"abc\"; if (a <= b) { return 5; } return 0; }", 5, "", ""},
		{"strcmp-prefix", "function main(): i32 { let a = \"ab\"; let b = \"abc\"; if (a < b) { return 9; } return 0; }", 9, "", ""},
		{"strcmp-ge-false", "function main(): i32 { let a = \"a\"; let b = \"b\"; if (a >= b) { return 11; } return 0; }", 0, "", ""},
		{"while-break", "function main(): i32 { let s = 0; let i = 0; while (i < 10) { if (i == 5) { break; } s = s + i; i = i + 1; } return s; }", 10, "", ""},
		{"while-continue", "function main(): i32 { let s = 0; let i = 0; while (i < 10) { i = i + 1; if (i % 2 == 1) { continue; } s = s + i; } return s; }", 30, "", ""},
		{"while-break-nested", "function main(): i32 { let t = 0; let i = 0; while (i < 3) { let j = 0; while (j < 5) { if (j == 2) { break; } t = t + j; j = j + 1; } i = i + 1; } return t; }", 3, "", ""},
		{"while-break-deep-if", "function main(): i32 { let s = 0; let i = 0; while (i < 10) { if (i > 3) { if (i == 4) { break; } } s = s + i; i = i + 1; } return s; }", 6, "", ""},
		{"cast-widen", "function main(): i32 { let n = 100000; let x: i64 = n as i64; let y: i64 = x * x; if (y > 4000000000) { return 5; } return 0; }", 5, "", ""},
		{"cast-narrow", "function main(): i32 { let big: i64 = 5000000007; let lo = (big as i32); return lo % 100; }", 11, "", ""},
		{"cast-mixed", "function main(): i32 { let base: i64 = 4000000000; let i = 5; let s: i64 = base + (i as i64); if (s > 4000000000) { return 7; } return 0; }", 7, "", ""},
		{"cast-roundtrip", "function main(): i32 { let n = 42; let x: i64 = n as i64; return (x as i32); }", 42, "", ""},
		{"call-8-args", "function add8(a: i32, b: i32, c: i32, d: i32, e: i32, f: i32, g: i32, h: i32): i32 { return a+b+c+d+e+f+g+h; } function main(): i32 { return add8(1,2,3,4,5,6,7,8); }", 36, "", ""},
		{"call-7-args-order", "function f(a:i32,b:i32,c:i32,d:i32,e:i32,g:i32,h:i32):i32 { return a - b - c - d - e - g - h; } function main(): i32 { return f(100,1,2,3,4,5,6); }", 79, "", ""},
		{"method-7-args", "struct P { base: i32 } function (p: P) sum7(a:i32,b:i32,c:i32,d:i32,e:i32,f:i32,g:i32): i32 { return p.base + a+b+c+d+e+f+g; } function main(): i32 { let p = P { base: 10 }; return p.sum7(1,2,3,4,5,6,7); }", 38, "", ""},
		{"i64-param", "function dbl(x: i64): i64 { return x * 2; } function main(): i32 { let r: i64 = dbl(3000000000); if (r > 5000000000) { return 7; } return 0; }", 7, "", ""},
		{"i64-return", "function big(): i64 { return 4000000000; } function main(): i32 { let x: i64 = big() + 1000000000; if (x > 4000000000) { return 5; } return 0; }", 5, "", ""},
		{"i64-param-mixed", "function f(a: i64, b: i32): i64 { return a + (b as i64); } function main(): i32 { let r: i64 = f(4000000000, 5); if (r > 4000000000) { return 9; } return 0; }", 9, "", ""},
		{"i64-return-recursion", "function pow2(n: i32): i64 { if (n <= 0) { return 1; } return pow2(n - 1) * 2; } function main(): i32 { if (pow2(33) > 4000000000) { return 13; } return 0; }", 13, "", ""},
		{"i64-div", "function main(): i32 { let a: i64 = 12000000000; let b: i64 = 4; let c: i64 = a / b; if (c > 2000000000) { return 7; } return 0; }", 7, "", ""},
		{"i64-rem", "function main(): i32 { let a: i64 = 12000000007; let r = (a % 10) as i32; return r; }", 7, "", ""},
		{"i64-div-trunc", "function main(): i32 { let a: i64 = 10000000000; let c: i64 = a / 3; if (c > 3000000000) { return 5; } return 0; }", 5, "", ""},
		{"i64-div-signed", "function main(): i32 { let a: i64 = 0 - 12000000000; let c: i64 = a / 4; if (c < 0) { return 9; } return 0; }", 9, "", ""},
		{"arr-slice", "function main(): i32 { let a = [10, 20, 30, 40, 50]; let b = a[1:4]; return b[0] + b[2]; }", 60, "", ""},
		{"arr-slice-len", "function main(): i32 { let a = [1, 2, 3, 4, 5]; let b = a[1:4]; return b.len(); }", 3, "", ""},
		{"arr-slice-strarr", "function main(): i32 { let a = [\"x\", \"yy\", \"zzz\", \"w\"]; let b = a[1:3]; return b[0].len() + b[1].len(); }", 5, "", ""},
		{"arr-slice-full", "function main(): i32 { let a = [5, 10, 15, 20]; let b = a[0:2]; return b[0] + b[1]; }", 15, "", ""},
		// Scalar-array struct fields (i32[]) — fresh-literal construction, leak-only
		// (the field array is owned by the struct + never swept, so no RC).
		{"struct-arr-field", "struct Buf { data: i32[], n: i32 } function main(): i32 { let b = Buf { data: [10, 20, 30], n: 3 }; let s = 0; let i = 0; while (i < b.n) { s = s + b.data[i]; i = i + 1; } return s; }", 60, "", ""},
		{"struct-arr-survives-alloc", "struct Buf { data: i32[] } function main(): i32 { let b = Buf { data: [10, 20, 30] }; let other = [99, 99, 99, 99, 99]; return b.data[0] + b.data[2]; }", 40, "", ""},
		{"struct-arr-param", "struct Buf { data: i32[], n: i32 } function sum(b: Buf): i32 { let s = 0; let i = 0; while (i < b.n) { s = s + b.data[i]; i = i + 1; } return s; } function main(): i32 { let b = Buf { data: [5, 10, 15], n: 3 }; return sum(b); }", 30, "", ""},
		{"struct-arr-extract", "struct Buf { data: i32[] } function main(): i32 { let b = Buf { data: [7, 8, 9] }; let a = b.data; return a[0] + a[2]; }", 16, "", ""},
		{"struct-arr-aliased-falls-back", "struct Buf { data: i32[] } function main(): i32 { let arr = [1, 2, 3]; let b = Buf { data: arr }; return b.data[1] + arr[0]; }", 3, "", ""},
		// Typed string[] arrays: literals, indexing (element is a string), params,
		// aliasing (array rc-tracked), string-op on elements. Elements leak.
		{"strarr-index", "function main(): i32 { let names = [\"foo\", \"bar\", \"hello\"]; return names[0].len() + names[2].len(); }", 8, "", ""},
		{"strarr-param", "function f(names: string[]): i32 { return names[0].len(); } function main(): i32 { return f([\"abcd\"]); }", 4, "", ""},
		{"strarr-loop", "function main(): i32 { let names = [\"a\", \"bb\", \"ccc\"]; let s = 0; let i = 0; while (i < 3) { s = s + names[i].len(); i = i + 1; } return s; }", 6, "", ""},
		{"strarr-eq-elem", "function main(): i32 { let names = [\"hi\", \"ho\"]; if (names[0] == \"hi\") { return 7; } return 0; }", 7, "", ""},
		// string[]-returning functions: the array is rc-tracked (move-on-return),
		// the call site tracks the result as string[] (element typing via
		// strarr_ret_fns) so `xs[i]` is a string. Elements leak.
		{"strarr-ret", "function names(): string[] { return [\"a\", \"bb\", \"ccc\"]; } function main(): i32 { let xs = names(); return xs[1].len(); }", 2, "", ""},
		{"strarr-ret-direct-index", "function names(): string[] { return [\"a\", \"bb\", \"ccc\"]; } function main(): i32 { return names()[2].len(); }", 3, "", ""},
		{"strarr-ret-len", "function names(): string[] { let a = [\"x\", \"yy\"]; return a; } function main(): i32 { let xs = names(); return xs.len() + xs[1].len(); }", 4, "", ""},
		{"strarr-ret-param", "function id(a: string[]): string[] { return a; } function main(): i32 { let xs = [\"q\", \"ww\", \"eee\"]; let ys = id(xs); return ys[1].len() + ys.len(); }", 5, "", ""},
		{"strarr-ret-loop", "function names(): string[] { return [\"a\", \"bb\", \"ccc\", \"dddd\"]; } function main(): i32 { let xs = names(); let i = 0; let s = 0; while (i < xs.len()) { s = s + xs[i].len(); i = i + 1; } return s; }", 10, "", ""},
		// Methods (receiver functions) via the IR path: receiver = arg 0, static
		// dispatch to __fn_<Type>.<name>. Field access on the receiver, args,
		// methods on params, and method-to-method (self) dispatch.
		{"ir-method-field", "struct P { x: i32 } function (p: P) get(): i32 { return p.x; } function main(): i32 { let p = P { x: 42 }; return p.get(); }", 42, "", ""},
		{"ir-method-two-fields", "struct P { x: i32, y: i32 } function (p: P) sum(): i32 { return p.x + p.y; } function main(): i32 { let p = P { x: 3, y: 4 }; return p.sum(); }", 7, "", ""},
		{"ir-method-with-arg", "struct B { v: i32 } function (b: B) scale(n: i32): i32 { return b.v * n; } function main(): i32 { let x = B { v: 4 }; return x.scale(3); }", 12, "", ""},
		{"ir-method-two-args", "struct P { x: i32 } function (p: P) comb(a: i32, b: i32): i32 { return p.x + a * 10 + b; } function main(): i32 { let p = P { x: 5 }; return p.comb(2, 3); }", 28, "", ""},
		{"ir-method-on-param", "struct P { x: i32, y: i32 } function (p: P) sum(): i32 { return p.x + p.y; } function runp(q: P): i32 { return q.sum(); } function main(): i32 { let p = P { x: 30, y: 12 }; return runp(p); }", 42, "", ""},
		{"ir-method-self-dispatch", "struct P { x: i32 } function (p: P) dbl(): i32 { return p.x * 2; } function (p: P) quad(): i32 { return p.dbl() * 2; } function main(): i32 { let p = P { x: 5 }; return p.quad(); }", 20, "", ""},
		{"ir-method-in-loop", "struct P { x: i32 } function (p: P) v(): i32 { return p.x; } function main(): i32 { let s = 0; let i = 0; while (i < 4) { let p = P { x: i * 3 }; s = s + p.v(); i = i + 1; } return s; }", 18, "", ""},
		{"ir-method-same-name-two-types", "struct A { n: i32 } struct B { n: i32 } function (a: A) get(): i32 { return a.n + 1; } function (b: B) get(): i32 { return b.n + 100; } function main(): i32 { let a = A { n: 5 }; let b = B { n: 5 }; return a.get() + b.get(); }", 111, "", ""},
		{
			"string-array-for-in",
			"function main(): i32 { let arr = [\"one\", \"two\", \"three\"]; for s in arr { write(s); write(\"\\n\"); } return 0; }",
			0,
			"one\ntwo\nthree\n",
			"",
		},
		{
			"string-array-eq",
			"function main(): i32 { let arr = [\"x\", \"y\", \"z\"]; if (arr[1] == \"y\") { return 1; } return 0; }",
			1,
			"",
			"",
		},
		{
			"method-on-string-receiver",
			"function (s: string) shout(): string { return s + \"!\"; } function main(): i32 { let msg = \"hi\"; let out = msg.shout(); write(out); return out.len(); }",
			3,
			"hi!",
			"",
		},
		{
			"method-on-i32-receiver",
			"function (n: i32) twice(): i32 { return n * 2; } function main(): i32 { let x = 21; return x.twice(); }",
			42,
			"",
			"",
		},
		{
			"method-on-string-arg",
			"function (s: string) repeat3(): string { return s + s + s; } function main(): i32 { let m = \"ab\"; let out = m.repeat3(); write(out); return out.len(); }",
			6,
			"ababab",
			"",
		},
		{
			"method-on-string-with-args",
			"function (s: string) join_with(sep: string, other: string): string { return s + sep + other; } function main(): i32 { let a = \"foo\"; let b = \"bar\"; let out = a.join_with(\"-\", b); write(out); return 0; }",
			0,
			"foo-bar",
			"",
		},
		{
			"eprint-literal-exits-clean",
			"function main(): i32 { eprint(\"error msg\\n\"); return 7; }",
			7,
			"",
			"",
		},
		{
			"eprint-ident-string",
			"function main(): i32 { let msg = \"oops\\n\"; eprint(msg); return 42; }",
			42,
			"",
			"",
		},
		{
			"eprint-no-stdout-emitted",
			"function main(): i32 { eprint(\"stderr only\"); return 0; }",
			0,
			"",
			"",
		},
		{
			"eprint-and-print-coexist",
			"function main(): i32 { print(\"out\"); eprint(\"err\\n\"); return 0; }",
			0,
			"out\n",
			"",
		},
		{
			"exit-from-helper",
			"function check(): i32 { exit(7); return 0; } function main(): i32 { check(); return 99; }",
			7,
			"",
			"",
		},
		{
			"exit-before-print",
			"function main(): i32 { write(\"a\"); exit(3); print(\"b\"); return 0; }",
			3,
			"a",
			"",
		},
		{
			"exit-zero",
			"function main(): i32 { exit(0); return 5; }",
			0,
			"",
			"",
		},
		// Match-arm guards (`Pat when <expr> =>`): a true guard runs the arm;
		// a false guard falls through to the next arm. The guard reads the
		// pattern binding.
		{"match-guard-pass", "enum O { Has(i32), Nil } function main(): i32 { let o: O = Has(8); match (o) { Has(n) when n > 5 => { return 1; }, _ => { return 2; } } return 0 - 1; }", 1, "", ""},
		{"match-guard-false-to-wildcard", "enum O { Has(i32), Nil } function main(): i32 { let o: O = Has(3); match (o) { Has(n) when n > 5 => { return 1; }, _ => { return 2; } } return 0 - 1; }", 2, "", ""},
		// Match expressions (`match (e) { Pat => E, … }` in value position):
		// desugar to an IIFE wrapping a statement-match with `return` arms.
		{"match-expr", "enum O { Has(i32), Nil } function main(): i32 { let o: O = Has(5); let x: i32 = match (o) { Has(n) => n, Nil => 0 }; return x; }", 5, "", ""},
		{"match-expr-other-arm", "enum O { Has(i32), Nil } function main(): i32 { let o: O = Nil; let x: i32 = match (o) { Has(n) => n, Nil => 42 }; return x; }", 42, "", ""},
		{"match-expr-arith", "enum O { Has(i32), Nil } function main(): i32 { let o: O = Has(20); return match (o) { Has(n) => n + 1, Nil => 0 } + 1; }", 22, "", ""},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Each case only pipes source through the already-built
			// driver, gcc-assembles, and runs — no recompile of the
			// 35k-line self-host driver — so the cases are independent
			// and cheap. Run them in parallel to spread the dozens of
			// gcc+exec cycles across the runner's cores (this test was
			// a ~50s CI long pole serially).
			t.Parallel()
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(driverBin)
			} else {
				cmd = exec.Command(runner[0], append(runner[1:], driverBin)...)
			}
			cmd.Stdin = bytes.NewReader([]byte(withPrintInt(tc.source)))
			emittedAsm, err := cmd.Output()
			if err != nil {
				t.Fatalf("driver run: %v\n--- source ---\n%s", err, tc.source)
			}
			if len(emittedAsm) == 0 {
				t.Fatalf("driver produced no asm output")
			}
			caseDir := t.TempDir()
			innerAsm := filepath.Join(caseDir, "inner.s")
			innerBin := filepath.Join(caseDir, "inner")
			if err := os.WriteFile(innerAsm, emittedAsm, 0o644); err != nil {
				t.Fatalf("write inner asm: %v", err)
			}
			if out, err := exec.Command(gcc, "-static", "-nostdlib", "-no-pie", innerAsm, "-o", innerBin).CombinedOutput(); err != nil {
				t.Fatalf("inner gcc: %v\n%s\n--- asm ---\n%s", err, out, emittedAsm)
			}
			var inner *exec.Cmd
			if len(runner) == 0 {
				inner = exec.Command(innerBin)
			} else {
				inner = exec.Command(runner[0], append(runner[1:], innerBin)...)
			}
			if tc.stdin != "" {
				inner.Stdin = bytes.NewReader([]byte(tc.stdin))
			}
			innerStdout, _ := inner.Output()
			if code := inner.ProcessState.ExitCode(); code != tc.expected {
				t.Errorf("inner exit code = %d, want %d\n--- source ---\n%s\n--- asm ---\n%s", code, tc.expected, tc.source, emittedAsm)
			}
			if tc.stdout != "" && string(innerStdout) != tc.stdout {
				t.Errorf("inner stdout = %q, want %q\n--- source ---\n%s\n--- asm ---\n%s", string(innerStdout), tc.stdout, tc.source, emittedAsm)
			}
		})
	}

	// Negative: the self-host type-check pass must REJECT a program that
	// uses an Option[i32] (`.max()`) where an i32 is declared, rather than
	// silently emitting a box pointer. The driver should exit non-zero and
	// print an E002 diagnostic; no asm is produced.
	t.Run("rejects-option-as-i32", func(t *testing.T) {
		t.Parallel()
		var cmd *exec.Cmd
		if len(runner) == 0 {
			cmd = exec.Command(driverBin)
		} else {
			cmd = exec.Command(runner[0], append(runner[1:], driverBin)...)
		}
		bad := "function main(): i32 { let xs: i32[] = [1, 2, 3]; return xs.max(); }"
		cmd.Stdin = bytes.NewReader([]byte(bad))
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		out, err := cmd.Output()
		if err == nil {
			t.Fatalf("expected driver to reject Option-as-i32, but it exited 0\n--- asm ---\n%s", out)
		}
		if !strings.Contains(stderr.String(), "E002") || !strings.Contains(stderr.String(), "Option[i32]") {
			t.Errorf("expected E002 / Option[i32] diagnostic, got stderr:\n%s", stderr.String())
		}
	})

	// Negative: parser failures must fail loudly. A reserved keyword used
	// as a function name (`use`) used to parse into an empty-name stub and
	// still emit asm (`__fn_:`), which could silently miscompile/hang.
	// The driver must now exit non-zero with a parse diagnostic.
	t.Run("rejects-parser-unknowns", func(t *testing.T) {
		t.Parallel()
		var cmd *exec.Cmd
		if len(runner) == 0 {
			cmd = exec.Command(driverBin)
		} else {
			cmd = exec.Command(runner[0], append(runner[1:], driverBin)...)
		}
		bad := "function use(src: i32[]): i32 { let i: i32 = 0; while (i < 3) { i = i + 1; } return i; } function main(): i32 { return 0; }"
		cmd.Stdin = bytes.NewReader([]byte(bad))
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		out, err := cmd.Output()
		if err == nil {
			t.Fatalf("expected driver to reject parser unknowns, but it exited 0\n--- asm ---\n%s", out)
		}
		if strings.Contains(string(out), "__fn_:") {
			t.Fatalf("expected no empty-name function emission, got asm:\n%s", out)
		}
		if !strings.Contains(stderr.String(), "P001") || !strings.Contains(stderr.String(), "malformed function declaration") {
			t.Errorf("expected P001 parse diagnostic, got stderr:\n%s", stderr.String())
		}
	})
}
