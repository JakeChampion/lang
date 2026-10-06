package e2ecompiler

import (
	"strings"
	"testing"
)

// TestSelfHostAsmArm64Bootstrap compiles each case through the self-hosted
// CLI (`fern.fern`, built for the x86-64 host) with `-target arm64-linux
// -emit asm`, assembles the output with aarch64-linux-gnu-gcc and runs it
// under qemu-aarch64 (or natively on an arm64 host), checking the exit code
// and, where a case names one, stdout. A declaration the typed lowering
// refuses fails the compile. A case is either a whole program or bare
// statements, which the CLI wraps into `main`.
func TestSelfHostAsmArm64Bootstrap(t *testing.T) {
	gcc, qemu := arm64Tooling(t)
	cli := newStrictCLI(t)

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
		{"cmp-lt-true", `if (5 < 10) { return 1; } return 0;`, 1, ""},
		{"cmp-lt-false", `if (10 < 5) { return 1; } return 0;`, 0, ""},
		{"cmp-le-true", `if (5 <= 5) { return 1; } return 0;`, 1, ""},
		{"cmp-gt-true", `if (7 > 3) { return 1; } return 0;`, 1, ""},
		{"cmp-ge-true", `if (7 >= 7) { return 1; } return 0;`, 1, ""},
		{"cmp-eq-true", `if (4 == 4) { return 1; } return 0;`, 1, ""},
		{"cmp-eq-false", `if (4 == 5) { return 1; } return 0;`, 0, ""},
		{"cmp-ne-true", `if (4 != 5) { return 1; } return 0;`, 1, ""},
		{"bool-true", `if (true) { return 1; } return 0;`, 1, ""},
		{"bool-false", `if (false) { return 1; } return 0;`, 0, ""},
		{"if-then-taken", "if (true) { return 9; } else { return 0; }", 9, ""},
		{"if-else-taken", "if (false) { return 9; } else { return 7; }", 7, ""},
		{"if-no-else-fall", "if (false) { return 9; } return 5;", 5, ""},
		{"if-cond-via-cmp", "if (5 < 10) { return 1; } else { return 2; }", 1, ""},
		{"and-both-true", "if (true && true) { return 1; } return 0;", 1, ""},
		{"and-left-false", "if (false && true) { return 1; } return 0;", 0, ""},
		{"and-right-false", "if (true && false) { return 1; } return 0;", 0, ""},
		{"and-short-circuits-rhs", "function side(): boolean { write(\"R\"); return true; } function main(): i32 { write(\"A\"); if (false && side()) { return 1; } write(\"B\"); return 0; }", 0, "AB"},
		{"or-both-false", "if (false || false) { return 1; } return 0;", 0, ""},
		{"or-left-true", "if (true || false) { return 1; } return 0;", 1, ""},
		{"or-right-true", "if (false || true) { return 1; } return 0;", 1, ""},
		{"or-short-circuits-rhs", "function side(): boolean { write(\"R\"); return false; } function main(): i32 { write(\"A\"); if (true || side()) { write(\"B\"); } return 0; }", 0, "AB"},
		{"and-or-mixed", "let a = true; let b = false; let c = true; if ((a && b) || c) { return 1; } return 0;", 1, ""},
		{"and-with-comparison", "let x = 5; if (x > 0 && x < 10) { return 1; } return 0;", 1, ""},
		{"not-true", "if (!true) { return 1; } return 0;", 0, ""},
		{"not-false", "if (!false) { return 1; } return 0;", 1, ""},
		{"not-comparison", "let x = 5; if (!(x < 0)) { return 1; } return 0;", 1, ""},
		{"not-double", "let b = true; if (!!b) { return 1; } return 0;", 1, ""},
		{"not-and", "let a = true; let b = false; if (!a && !b) { return 1; } return 2;", 2, ""},
		{"not-or-truthy", "let a = false; let b = true; if (!a || !b) { return 1; } return 2;", 1, ""},
		{"locals-single", "let x = 5; return x;", 5, ""},
		{"locals-three", "let a = 10; let b = 20; let c = 30; return a + b + c;", 60, ""},
		{"reassign", "let x = 5; x = x + 3; return x;", 8, ""},
		// `arena` is no longer a reserved word (the arena block + builtins
		// were removed) — it must parse as an ordinary identifier.
		{"arena-as-identifier", "let arena = 3; arena = arena + 4; return arena;", 7, ""},
		{"compound-assign", "let x = 1; x *= 6; x += 1; return x;", 7, ""},
		{"while-sum-counter", "let i = 1; let s = 0; while (i <= 5) { s += i; i += 1; } return s;", 15, ""},
		{"while-early-return", "let i = 0; while (i < 100) { if (i == 7) { return i; } i += 1; } return 0 - 1;", 7, ""},
		{"func-decl-call", "function add(x: i32, y: i32): i32 { return x + y; } function main(): i32 { return add(2, 3); }", 5, ""},
		{"func-three-args", "function sum3(a: i32, b: i32, c: i32): i32 { return a + b + c; } function main(): i32 { return sum3(10, 20, 30); }", 60, ""},
		{"recursive-factorial", "function fact(n: i32): i32 { if (n <= 1) { return 1; } return n * fact(n - 1); } function main(): i32 { return fact(5); }", 120, ""},
		{"recursive-fib", "function fib(n: i32): i32 { if (n < 2) { return n; } return fib(n - 1) + fib(n - 2); } function main(): i32 { return fib(8); }", 21, ""},
		{"mutual-recursion", "function is_even(n: i32): i32 { if (n == 0) { return 1; } return is_odd(n - 1); } function is_odd(n: i32): i32 { if (n == 0) { return 0; } return is_even(n - 1); } function main(): i32 { return is_even(6); }", 1, ""},
		{"func-with-local-vars", "function compute(a: i32): i32 { let b = a * 2; let c = b + 1; return c; } function main(): i32 { return compute(5); }", 11, ""},
		// Pipe operator |> — desugars `x |> f(args)` to `f(x, args)`.
		{"pipe-call", "function inc(n: i32): i32 { return n + 1; } function main(): i32 { return 5 |> inc(); }", 6, ""},
		{"pipe-bare-callee", "function inc(n: i32): i32 { return n + 1; } function main(): i32 { return 5 |> inc; }", 6, ""},
		{"pipe-extra-args", "function add(a: i32, b: i32): i32 { return a + b; } function main(): i32 { return 5 |> add(10); }", 15, ""},
		{"pipe-chained", "function inc(n: i32): i32 { return n + 1; } function dbl(n: i32): i32 { return n * 2; } function main(): i32 { return 5 |> inc() |> dbl(); }", 12, ""},
		{"pipe-binary-lhs", "function inc(n: i32): i32 { return n + 1; } function main(): i32 { return 2 + 3 |> inc(); }", 6, ""},
		// if-expressions — desugar to an immediately-invoked closure.
		{"if-expr-true", "function main(): i32 { let x: i32 = if (true) { 3 } else { 4 }; return x; }", 3, ""},
		{"if-expr-false", "function main(): i32 { let x: i32 = if (false) { 3 } else { 4 }; return x; }", 4, ""},
		{"if-expr-capture", "function main(): i32 { let n: i32 = 10; let x: i32 = if (n > 5) { n + 1 } else { 0 }; return x; }", 11, ""},
		{"if-expr-return", "function pick(c: i32): i32 { return if (c == 1) { 7 } else { 9 }; } function main(): i32 { return pick(0); }", 9, ""},
		{"if-expr-else-if", "function main(): i32 { let n: i32 = 2; let x: i32 = if (n == 1) { 10 } else if (n == 2) { 20 } else { 30 }; return x; }", 20, ""},
		{"if-expr-as-arg", "function id(n: i32): i32 { return n; } function main(): i32 { return id(if (true) { 5 } else { 6 }); }", 5, ""},
		{"direct-iife", "function main(): i32 { return ((): i32 => { return 3; })(); }", 3, ""},
		{"iife-with-args", "function main(): i32 { return ((a: i32, b: i32): i32 => { return a + b; })(4, 5); }", 9, ""},
		// Local (nested) functions — desugar to a closure-valued local.
		{"local-fn-basic", "function main(): i32 { function helper(): i32 { return 5; } return helper(); }", 5, ""},
		{"local-fn-args", "function main(): i32 { function add(a: i32, b: i32): i32 { return a + b; } return add(4, 5); }", 9, ""},
		{"local-fn-capture", "function main(): i32 { let n: i32 = 10; function bump(): i32 { return n + 1; } return bump(); }", 11, ""},
		{"local-fn-two", "function main(): i32 { function f(): i32 { return 2; } function g(): i32 { return 3; } return f() * g(); }", 6, ""},
		// defer — runs the action at function exit (LIFO, conditional via a
		// per-defer flag, return value captured before cleanup). Observed
		// via an array a caller mutates and reads back.
		{"defer-fires", `function inc(c: Cell[i32]): i32 { defer c.set(9); return 1; } function main(): i32 { let c = cell_new(0); inc(c); return c.get(); }`, 9, ""},
		{"defer-retval-before-cleanup", "function f(): i32 { let x = 5; defer x = 99; return x; } function main(): i32 { return f(); }", 5, ""},
		{"defer-lifo", `function f(c: Cell[i32]): i32 { defer c.set(1); defer c.set(2); return 0; } function main(): i32 { let c = cell_new(0); f(c); return c.get(); }`, 1, ""},
		{"defer-conditional-off", `function f(c: Cell[i32], k: i32): i32 { if (k == 1) { defer c.set(7); } return 0; } function main(): i32 { let c = cell_new(0); f(c, 0); return c.get(); }`, 0, ""},
		{"defer-conditional-on", `function f(c: Cell[i32], k: i32): i32 { if (k == 1) { defer c.set(7); } return 0; } function main(): i32 { let c = cell_new(0); f(c, 1); return c.get(); }`, 7, ""},
		{"defer-early-return", `function f(c: Cell[i32], k: i32): i32 { defer c.set(5); if (k == 1) { return 0; } c.set(99); return 0; } function main(): i32 { let c = cell_new(0); f(c, 1); return c.get(); }`, 5, ""},
		{"defer-loop-survives", `function f(c: Cell[i32]): i32 { defer c.set(c.get() + 50); let i = 0; while (i < 3) { c.set(c.get() + 1); i = i + 1; } return 0; } function main(): i32 { let c = cell_new(0); f(c); return c.get(); }`, 53, ""},
		{"hello-arm64", "print(\"Hello, ARM64!\"); return 0;", 0, "Hello, ARM64!\n"},
		{"print-twice", "print(\"line A\"); print(\"line B\"); return 0;", 0, "line A\nline B\n"},
		{"print-then-return", "print(\"out\"); return 7;", 7, "out\n"},
		{"print-int-literal", `function main(): i32 { print_int(42); write("\n"); return 0; }`, 0, "42\n"},
		{"print-int-zero", `function main(): i32 { print_int(0); write("\n"); return 0; }`, 0, "0\n"},
		{"print-int-negative", `function main(): i32 { print_int(0 - 7); write("\n"); return 0; }`, 0, "-7\n"},
		{"print-int-fact", `function fact(n: i32): i32 { if (n <= 1) { return 1; } return n * fact(n - 1); } function main(): i32 { print_int(fact(8)); write("\n"); return 0; }`, 0, "40320\n"},
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
		},
		{
			"fibonacci-series-first-10",
			"function fib(n: i32): i32 { if (n < 2) { return n; } return fib(n - 1) + fib(n - 2); } " +
				"function main(): i32 { let i = 0; while (i < 10) { print_int(fib(i)); write(\" \"); i = i + 1; } write(\"\\n\"); return 0; }",
			0,
			"0 1 1 2 3 5 8 13 21 34 \n",
		},
		{
			"sum-via-recursion-and-print",
			"function sum(n: i32): i32 { if (n == 0) { return 0; } return n + sum(n - 1); } " +
				"function main(): i32 { write(\"sum(1..10) = \"); print_int(sum(10)); write(\"\\n\"); return 0; }",
			0,
			"sum(1..10) = 55\n",
		},
		{
			"primes-up-to-30",
			"function is_prime(n: i32): i32 { if (n < 2) { return 0; } let i = 2; while (i * i <= n) { if (n % i == 0) { return 0; } i = i + 1; } return 1; } " +
				"function main(): i32 { let i = 2; while (i <= 30) { if (is_prime(i) == 1) { print_int(i); write(\" \"); } i = i + 1; } write(\"\\n\"); return 0; }",
			0,
			"2 3 5 7 11 13 17 19 23 29 \n",
		},
		{
			"tuple-literal-access-zero",
			"function main(): i32 { let t = (7, 11, 13); return t.0; }",
			7,
			"",
		},
		{
			// Struct element of a destructured tuple keeps its type →
			// receiver method dispatches via shape pointer (Box.bump),
			// not __fn_i32__bump. See the x86 mirror.
			"tuple-destructure-struct-method",
			"struct Box { n: i32 } " +
				"pub function (b: Box) bump(): i32 { return b.n + 1; } " +
				"function pair(): (i32, Box) { return (5, Box { n: 10 }); } " +
				"function main(): i32 { let (x, b) = pair(); return b.bump(); }",
			11,
			"",
		},
		{
			// Method call with a `fn`-typed parameter — see x86 mirror.
			"method-fn-arg-boxed-not-called",
			"struct Foo { n: i32 } " +
				"pub function (f: Foo) call_one(fn: () => void): Foo { " +
				"fn(); return Foo { n: f.n + 99 }; } " +
				"function noop(): void { } " +
				"function main(): i32 { let f: Foo = Foo { n: 0 }; " +
				"f = f.call_one(noop); return f.n; }",
			99,
			"",
		},
		{
			// Receiver-method calls returning Option[T] — see x86 mirror.
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
		},
		{
			// `m.get_or(k, default)` — see x86 mirror.
			"map-get-or-string",
			"import \"core/map\";\nfunction main(): i32 { " +
				"let m: Map[string, i32] = map_new(8); " +
				"m = m.insert(\"a\", 10); " +
				"let a: i32 = m.get_or(\"a\", 0); " +
				"let b: i32 = m.get_or(\"missing\", 99); " +
				"return a + b; }",
			109,
			"",
		},
		{
			// `u32` / `u64` / `i64` route through the i32 codegen path
			// so `(n as u32).to_string()` doesn't fall to struct
			// shape-dispatch. See x86 mirror.
			"wider-int-as-cast-to-string",
			"import \"std/u32\";\nimport \"std/u64\";\nfunction main(): i32 { let a: u32 = 99 as u32; let b: u64 = 7 as u64; " +
				"if (a.to_string() != \"99\") { return 1; } " +
				"if (b.to_string() != \"7\") { return 2; } " +
				"return 42; }",
			42,
			"",
		},
		{
			// u32.to_string() with BIT 31 SET must format UNSIGNED via the
			// __fern_u32_to_string runtime helper, not the signed i32 one.
			// arm64 mirror of the x86 regression guard for #2649.
			"u32-high-bit-to-string",
			"import \"std/u32\";\nfunction main(): i32 { " +
				"if ((4294967295 as u32).to_string() != \"4294967295\") { return 1; } " +
				"if (((1 as u32) << (31 as u32)).to_string() != \"2147483648\") { return 2; } " +
				"if (((0 as u32) - (1 as u32)).to_string() != \"4294967295\") { return 3; } " +
				"return 42; }",
			42,
			"",
		},
		{
			// IEEE NaN semantics — every relation with NaN is false
			// except `!=`. arm64's fcmp + cset family already handles
			// this (Z=1 only on ordered equal, mi/ls/gt/ge all require
			// ordered flags), so this case is a parity assertion vs
			// the x86 NaN-mask fix.
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
		},
		{
			// Small builtins: f64<->i64 / f32<->i32 bit reinterprets,
			// sleep_ms(0), remove_file on a missing path. See x86 mirror.
			"small-builtins-roundtrip",
			"function main(): i32 { " +
				"let a: f64 = 3.5; " +
				"if (f64_from_bits(f64_bits(a)) != a) { return 1; } " +
				"let h: f32 = 3.5 as f32; " +
				"if (f32_from_bits(f32_bits(h)) != h) { return 2; } " +
				"sleep_ms(0); " +
				"match (remove_file(\"/tmp/lang-no-such-file-zzz\")) { Err(_) => {}, Ok(_) => { return 3; } } " +
				"return 42; }",
			42,
			"",
		},
		{
			// Ok(x) / Err(x) lower as Result heap boxes (tag @0,
			// payload @8), not as calls to __fn_Ok / __fn_Err.
			// See the x86 mirror.
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
		},
		{
			"tuple-literal-access-middle",
			"function main(): i32 { let t = (7, 11, 13); return t.1; }",
			11,
			"",
		},
		{
			"tuple-literal-access-last",
			"function main(): i32 { let t = (7, 11, 13); return t.2; }",
			13,
			"",
		},
		{
			"tuple-sum-fields",
			"function main(): i32 { let t = (10, 20, 30); return t.0 + t.1 + t.2; }",
			60,
			"",
		},
		{
			"tuple-of-expressions",
			"function main(): i32 { let x = 5; let t = (x * 2, x + 1, x - 1); return t.0 + t.1 + t.2; }",
			20,
			"",
		},
		{
			"array-literal-len",
			"function main(): i32 { let a = [10, 20, 30]; return a.len(); }",
			3,
			"",
		},
		{
			"array-index-first",
			"function main(): i32 { let a = [42, 99, 7]; return a[0]; }",
			42,
			"",
		},
		{
			"array-index-middle",
			"function main(): i32 { let a = [42, 99, 7]; return a[1]; }",
			99,
			"",
		},
		{
			"array-index-last",
			"function main(): i32 { let a = [42, 99, 7]; return a[2]; }",
			7,
			"",
		},
		{
			"array-index-via-var",
			"function main(): i32 { let a = [10, 20, 30, 40]; let i = 2; return a[i]; }",
			30,
			"",
		},
		{
			"array-sum-via-while",
			"function main(): i32 { let a = [1, 2, 3, 4, 5]; let i = 0; let s = 0; while (i < a.len()) { s = s + a[i]; i = i + 1; } return s; }",
			15,
			"",
		},
		{
			"array-of-expressions",
			"function main(): i32 { let x = 4; let a = [x, x + 1, x * 2]; return a[0] + a[1] + a[2]; }",
			17,
			"",
		},
		{
			"match-single-variant-binding",
			`struct Circle { r: i32 } function main(): i32 { let c = Circle { r: 5 }; match (c) { Circle { r } => { return r; } } return 0 - 2; }`,
			5,
			"",
		},
		{
			"match-no-binding",
			`struct Empty { } function main(): i32 { let e = Empty { }; match (e) { Empty { } => { return 11; } } return 0 - 2; }`,
			11,
			"",
		},
		{
			"match-write-to-outer-var",
			`struct Circle { r: i32 } function main(): i32 { let c = Circle { r: 8 }; let n: i32 = 0; match (c) { Circle { r } => { n = r; } } return n; }`,
			8,
			"",
		},
		{
			"for-sum-array",
			"function main(): i32 { let a = [10, 20, 30]; let s = 0; for x in a { s = s + x; } return s; }",
			60,
			"",
		},
		{
			"for-count-iterations",
			"function main(): i32 { let a = [1, 2, 3, 4, 5]; let n = 0; for x in a { n = n + 1; } return n; }",
			5,
			"",
		},
		{
			"for-empty-array",
			"function main(): i32 { let a = [42]; let s = 0; for x in a { s = s + 1; } return s; }",
			1,
			"",
		},
		{
			"for-element-squares",
			"function main(): i32 { let a = [2, 3, 4]; let s = 0; for x in a { s = s + x * x; } return s; }",
			29,
			"",
		},
		{
			"break-in-while",
			"function main(): i32 { let i = 0; let s = 0; while (i < 100) { if (i == 5) { break; } s = s + i; i = i + 1; } return s; }",
			10,
			"",
		},
		{
			"continue-in-while",
			"function main(): i32 { let i = 0; let s = 0; while (i < 10) { i = i + 1; if (i == 5) { continue; } s = s + i; } return s; }",
			50,
			"",
		},
		{
			"break-in-for",
			"function main(): i32 { let a = [10, 20, 30, 40]; let s = 0; for x in a { if (x == 30) { break; } s = s + x; } return s; }",
			30,
			"",
		},
		{
			"continue-in-for",
			"function main(): i32 { let a = [1, 2, 3, 4, 5]; let s = 0; for x in a { if (x == 3) { continue; } s = s + x; } return s; }",
			12,
			"",
		},
		{
			"method-area-no-args",
			"struct Circle { r: i32 } function (c: Circle) area(): i32 { return c.r * c.r; } function main(): i32 { let k = Circle { r: 5 }; return k.area(); }",
			25,
			"",
		},
		{
			"method-with-args",
			"struct Box { v: i32 } function (b: Box) scale(n: i32): i32 { return b.v * n; } function main(): i32 { let x = Box { v: 4 }; return x.scale(3); }",
			12,
			"",
		},
		{
			"method-mixed-with-plain-func",
			"struct Circle { r: i32 } function (c: Circle) area(): i32 { return c.r * c.r; } function area(): i32 { return 100; } function main(): i32 { let k = Circle { r: 5 }; let m: i32 = area(); let n: i32 = k.area(); return m + n; }",
			125,
			"",
		},
		{
			"method-multi-struct-dispatch",
			"struct Circle { r: i32 } struct Square { s: i32 } function (c: Circle) area(): i32 { return c.r * c.r; } function (q: Square) area(): i32 { return q.s * q.s; } function main(): i32 { let k = Square { s: 6 }; return k.area(); }",
			36,
			"",
		},
		{
			"method-three-args",
			"struct P { x: i32 } function (p: P) f(a: i32, b: i32, c: i32): i32 { return p.x + a + b + c; } function main(): i32 { let p = P { x: 10 }; return p.f(1, 2, 3); }",
			16,
			"",
		},
		{
			"lambda-no-capture",
			"function main(): i32 { let f = (x: i32): i32 => { return x + 1; }; return f(41); }",
			42,
			"",
		},
		{
			"lambda-no-args",
			"function main(): i32 { let f = (): i32 => { return 7; }; return f(); }",
			7,
			"",
		},
		{
			"lambda-multi-args",
			"function main(): i32 { let add = (a: i32, b: i32): i32 => { return a + b; }; return add(20, 22); }",
			42,
			"",
		},
		{
			"lambda-with-locals",
			"function main(): i32 { let f = (n: i32): i32 => { let sq = n * n; let dbl = n + n; return sq + dbl; }; return f(5); }",
			35,
			"",
		},
		{
			"closure-single-capture",
			"function main(): i32 { let n = 5; let f = (x: i32): i32 => { return x + n; }; return f(7); }",
			12,
			"",
		},
		{
			"closure-multi-capture",
			"function main(): i32 { let a = 10; let b = 20; let f = (): i32 => { return a + b; }; return f(); }",
			30,
			"",
		},
		{
			// Capture is by REFERENCE — one shared cell — so the outer n = 99
			// is visible inside the closure, matching the interpreter, which
			// defines the semantics (closureconv.BoxMutatedCaptures, #2896 /
			// #5301). This expected 5 back when the legacy AST arm64 backend
			// still snapshotted at
			// make time; that divergence has since closed, so the legacy path
			// now agrees with the oracle and the IR path (#5479).
			"closure-capture-by-reference",
			"function main(): i32 { let n = 5; let f = (): i32 => { return n; }; n = 99; return f(); }",
			99,
			"",
		},
		{
			"closure-capture-and-arg",
			"function main(): i32 { let k = 100; let g = (x: i32, y: i32): i32 => { return x + y + k; }; return g(2, 3); }",
			105,
			"",
		},
		{
			"closure-nested-recapture",
			"function main(): i32 { let n = 7; let outer = (): i32 => { let inner = (): i32 => { return n; }; return inner(); }; return outer(); }",
			7,
			"",
		},
		{
			"plain-string-fn-result-print",
			"function get(): string { return \"hi\"; } function main(): i32 { write(get()); return 0; }",
			0,
			"hi",
		},
		{
			"closure-string-capture",
			"function main(): i32 { let s = \"hi\"; let f = (): string => { return s; }; write(f()); return 0; }",
			0,
			"hi",
		},
		{
			"closure-string-concat-capture",
			"function main(): i32 { let prefix = \"hello-\"; let f = (suf: string): string => { return prefix + suf; }; write(f(\"world\")); return 0; }",
			0,
			"hello-world",
		},
		{
			"closure-bool-returning",
			"function main(): i32 { let x = 5; let is_big = (): boolean => { return x > 3; }; if (is_big()) { return 1; } return 0; }",
			1,
			"",
		},
		{
			"closure-string-multi-capture",
			"function main(): i32 { let a = \"foo\"; let b = \"bar\"; let f = (): string => { return a + b; }; write(f()); return 0; }",
			0,
			"foobar",
		},
		{
			"closure-i32-returning-string-capture",
			"function main(): i32 { let s = \"hello\"; let f = (): i32 => { return s.len(); }; return f(); }",
			5,
			"",
		},
		{
			"arr-i32-push-len",
			"function main(): i32 { let xs: i32[] = [1, 2, 3]; xs = xs.append(4); return xs.len(); }",
			4,
			"",
		},
		{
			"arr-i32-push-last",
			"function main(): i32 { let xs: i32[] = [10, 20, 30]; xs = xs.append(99); return xs[3]; }",
			99,
			"",
		},
		{
			"arr-i32-push-preserves-existing",
			"function main(): i32 { let xs: i32[] = [1, 2, 3]; xs = xs.append(4); return xs[0] + xs[1] + xs[2] + xs[3]; }",
			10,
			"",
		},
		{
			"arr-i32-push-empty",
			"function main(): i32 { let xs: i32[] = []; xs = xs.append(42); return xs[0]; }",
			42,
			"",
		},
		{
			"arr-string-push",
			"function main(): i32 { let xs: string[] = [\"a\", \"b\"]; xs = xs.append(\"c\"); for s in xs { write(s); } return xs.len(); }",
			3,
			"abc",
		},
		{
			"arr-i32-push-chain",
			"function main(): i32 { let xs: i32[] = []; xs = xs.append(1); xs = xs.append(2); xs = xs.append(3); return xs[0] + xs[1] + xs[2]; }",
			6,
			"",
		},
		{
			"str-method-split-len",
			`import "std/string";
function main(): i32 { let s = "a,b,c"; let parts = s.split(","); return parts.len(); }`,
			3,
			"",
		},
		{
			"str-method-split-content",
			`import "std/string";
function main(): i32 { let s = "x|y|z"; let parts = s.split("|"); for t in parts { write(t); } return 0; }`,
			0,
			"xyz",
		},
		{
			"str-method-trim",
			`import "std/string";
function main(): i32 { let s = "  hi  "; let t = s.trim(); write(t); return t.len(); }`,
			2,
			"hi",
		},
		{
			"str-method-to-upper",
			`import "std/string";
function main(): i32 { let s = "hello"; write(s.to_ascii_upper()); return 0; }`,
			0,
			"HELLO",
		},
		{
			"str-method-to-lower",
			`import "std/string";
function main(): i32 { let s = "HELLO"; write(s.to_ascii_lower()); return 0; }`,
			0,
			"hello",
		},
		{
			"str-method-repeat",
			`import "std/string";
function main(): i32 { let s = "ab"; write(s.repeat(3)); return 0; }`,
			0,
			"ababab",
		},
		{
			"str-method-replace",
			`import "std/string";
function main(): i32 { let s = "hello"; write(s.replace("l", "L")); return 0; }`,
			0,
			"heLLo",
		},
		{
			"str-method-chain",
			`import "std/string";
function main(): i32 { let s = "  HELLO WORLD  "; write(s.trim().to_ascii_lower()); return 0; }`,
			0,
			"hello world",
		},
		{
			"str-literal-method-direct",
			`import "std/string";
function main(): i32 { write("hello".to_ascii_upper()); return 0; }`,
			0,
			"HELLO",
		},
		{
			"str-literal-method-chain",
			`import "std/string";
function main(): i32 { write("  HI  ".trim().repeat(2)); return 0; }`,
			0,
			"HIHI",
		},
		{
			"arr-i32-slice-len",
			"function main(): i32 { let xs: i32[] = [10, 20, 30, 40, 50]; let ys = xs[1:4]; return ys.len(); }",
			3,
			"",
		},
		{
			"arr-i32-slice-content",
			"function main(): i32 { let xs: i32[] = [10, 20, 30, 40, 50]; let ys = xs[1:4]; return ys[0] + ys[1] + ys[2]; }",
			90,
			"",
		},
		{
			"arr-i32-slice-empty",
			"function main(): i32 { let xs: i32[] = [1, 2, 3]; let ys = xs[1:1]; return ys.len(); }",
			0,
			"",
		},
		{
			"arr-i32-slice-full",
			"function main(): i32 { let xs: i32[] = [7, 8, 9]; let ys = xs[0:3]; return ys[0] + ys[1] + ys[2]; }",
			24,
			"",
		},
		{
			"arr-string-slice",
			"function main(): i32 { let xs: string[] = [\"a\", \"b\", \"c\", \"d\"]; let ys = xs[1:3]; for s in ys { write(s); } return ys.len(); }",
			2,
			"bc",
		},
		// 64-bit-element arrays (i64[] / f64[]): the native arm64 backend
		// already uses 8-byte element slots, so values above 2^31 round-trip.
		// Mirrors the wasm i64arr-* cases.
		{
			"arr-i64-literal-index-large",
			"function main(): i32 { let xs: i64[] = [5000000000, 42]; if (xs[0] == 5000000000) { return xs[1] as i32; } return 0; }",
			42,
			"",
		},
		{
			"arr-i64-for-sum",
			"function main(): i32 { let xs: i64[] = [3, 5, 90]; let s: i64 = 0; for v in xs { s = s + v; } return s as i32; }",
			98,
			"",
		},
		{
			"arr-i64-set-index-large",
			`function main(): i32 { let xs: i64[] = [1, 2, 3]; xs = xs.with(1, 5000000000); if (xs[1] == 5000000000) { return 7; } return 0; }`,
			7,
			"",
		},
		{
			"arr-i64-push-grow",
			"function main(): i32 { let xs: i64[] = [10]; xs = xs.append(20); xs = xs.append(5000000000); if (xs[2] == 5000000000) { return (xs[0] + xs[1]) as i32; } return 0; }",
			30,
			"",
		},
		{
			"arr-i64-slice",
			"function main(): i32 { let xs: i64[] = [10, 20, 30, 40]; let ys = xs[1:3]; return (ys[0] + ys[1]) as i32; }",
			50,
			"",
		},
		{
			"arr-f64-for-sum",
			"function main(): i32 { let xs: f64[] = [1.5, 2.5, 3.0]; let s: f64 = 0.0; for v in xs { s = s + v; } return s as i32; }",
			7,
			"",
		},
		{
			"i32-abs-positive",
			`import "std/i32";
function main(): i32 { let n: i32 = 7; return n.abs(); }`,
			7,
			"",
		},
		{
			"i32-abs-negative",
			`import "std/i32";
function main(): i32 { let n: i32 = 0 - 42; return n.abs(); }`,
			42,
			"",
		},
		{
			"i32-abs-zero",
			`import "std/i32";
function main(): i32 { let n: i32 = 0; return n.abs(); }`,
			0,
			"",
		},
		{
			"i32-is-zero-true",
			`import "std/i32";
function main(): i32 { let n: i32 = 0; if (n.is_zero()) { return 1; } return 0; }`,
			1,
			"",
		},
		{
			"i32-is-zero-false",
			`import "std/i32";
function main(): i32 { let n: i32 = 5; if (n.is_zero()) { return 1; } return 0; }`,
			0,
			"",
		},
		{
			"i32-is-positive-true",
			`import "std/i32";
function main(): i32 { let n: i32 = 5; if (n.is_positive()) { return 1; } return 0; }`,
			1,
			"",
		},
		{
			"i32-is-positive-false-zero",
			`import "std/i32";
function main(): i32 { let n: i32 = 0; if (n.is_positive()) { return 1; } return 0; }`,
			0,
			"",
		},
		{
			"i32-is-positive-false-negative",
			`import "std/i32";
function main(): i32 { let n: i32 = 0 - 5; if (n.is_positive()) { return 1; } return 0; }`,
			0,
			"",
		},
		{
			"i32-is-negative-true",
			`import "std/i32";
function main(): i32 { let n: i32 = 0 - 5; if (n.is_negative()) { return 1; } return 0; }`,
			1,
			"",
		},
		{
			"i32-is-negative-false-zero",
			`import "std/i32";
function main(): i32 { let n: i32 = 0; if (n.is_negative()) { return 1; } return 0; }`,
			0,
			"",
		},
		{
			"i32-is-even-true",
			`import "std/i32";
function main(): i32 { let n: i32 = 4; if (n.is_even()) { return 1; } return 0; }`,
			1,
			"",
		},
		{
			"i32-is-even-false",
			`import "std/i32";
function main(): i32 { let n: i32 = 7; if (n.is_even()) { return 1; } return 0; }`,
			0,
			"",
		},
		{
			"i32-is-odd-true",
			`import "std/i32";
function main(): i32 { let n: i32 = 9; if (n.is_odd()) { return 1; } return 0; }`,
			1,
			"",
		},
		{
			"i32-is-odd-false",
			`import "std/i32";
function main(): i32 { let n: i32 = 8; if (n.is_odd()) { return 1; } return 0; }`,
			0,
			"",
		},
		{
			"i32-is-even-zero",
			`import "std/i32";
function main(): i32 { let n: i32 = 0; if (n.is_even()) { return 1; } return 0; }`,
			1,
			"",
		},
		{
			"i32-sign-positive",
			`import "std/i32";
function main(): i32 { let n: i32 = 42; return n.signum(); }`,
			1,
			"",
		},
		{
			"i32-sign-zero",
			`import "std/i32";
function main(): i32 { let n: i32 = 0; return n.signum(); }`,
			0,
			"",
		},
		{
			"i32-sign-negative",
			`import "std/i32";
function main(): i32 { let n: i32 = 0 - 7; print_int(n.signum()); return 0; }`,
			0,
			"-1",
		},
		{
			"i32-clamp-in-range",
			`import "std/i32";
function main(): i32 { let n: i32 = 5; return n.clamp(0, 10); }`,
			5,
			"",
		},
		{
			"i32-clamp-below",
			`import "std/i32";
function main(): i32 { let n: i32 = 0 - 3; return n.clamp(0, 10); }`,
			0,
			"",
		},
		{
			"i32-clamp-above",
			`import "std/i32";
function main(): i32 { let n: i32 = 99; return n.clamp(0, 10); }`,
			10,
			"",
		},
		{
			"i32-clamp-equals-low",
			`import "std/i32";
function main(): i32 { let n: i32 = 0; return n.clamp(0, 10); }`,
			0,
			"",
		},
		{
			"i32-clamp-equals-high",
			`import "std/i32";
function main(): i32 { let n: i32 = 10; return n.clamp(0, 10); }`,
			10,
			"",
		},
		{
			"i32-min-pick-first",
			`import "std/i32";
function main(): i32 { let a: i32 = 3; return a.min(7); }`,
			3,
			"",
		},
		{
			"i32-min-pick-second",
			`import "std/i32";
function main(): i32 { let a: i32 = 9; return a.min(4); }`,
			4,
			"",
		},
		{
			"i32-max-pick-first",
			`import "std/i32";
function main(): i32 { let a: i32 = 8; return a.max(3); }`,
			8,
			"",
		},
		{
			"i32-max-pick-second",
			`import "std/i32";
function main(): i32 { let a: i32 = 2; return a.max(11); }`,
			11,
			"",
		},
		{
			"i32-min-equal",
			`import "std/i32";
function main(): i32 { let a: i32 = 5; return a.min(5); }`,
			5,
			"",
		},
		{
			"arr-i32-first",
			`import "std/array";
function main(): i32 { let xs: i32[] = [10, 20, 30]; match (xs.first()) { Some(v) => { return v; }, None => { return 0 - 1; } } }`,
			10,
			"",
		},
		{
			"arr-i32-last",
			`import "std/array";
function main(): i32 { let xs: i32[] = [10, 20, 30]; match (xs.last()) { Some(v) => { return v; }, None => { return 0 - 1; } } }`,
			30,
			"",
		},
		{
			"arr-i32-first-single",
			`import "std/array";
function main(): i32 { let xs: i32[] = [99]; match (xs.first()) { Some(v) => { return v; }, None => { return 0 - 1; } } }`,
			99,
			"",
		},
		{
			"arr-i32-last-single",
			`import "std/array";
function main(): i32 { let xs: i32[] = [99]; match (xs.last()) { Some(v) => { return v; }, None => { return 0 - 1; } } }`,
			99,
			"",
		},
		{
			"arr-string-first",
			`import "std/array";
function main(): i32 { let xs: string[] = ["hello", "world"]; match (xs.first()) { Some(s) => { write(s); }, None => {} } return 0; }`,
			0,
			"hello",
		},
		{
			"arr-string-last",
			`import "std/array";
function main(): i32 { let xs: string[] = ["hello", "world"]; match (xs.last()) { Some(s) => { write(s); }, None => {} } return 0; }`,
			0,
			"world",
		},
		{
			"arr-string-join-basic",
			`import "std/array";
function main(): i32 { let xs: string[] = ["a", "b", "c"]; write(xs.join(",")); return 0; }`,
			0,
			"a,b,c",
		},
		{
			"arr-string-join-empty",
			`import "std/array";
function main(): i32 { let xs: string[] = []; let r = xs.join(","); write("["); write(r); write("]"); return r.len(); }`,
			0,
			"[]",
		},
		{
			"arr-string-join-single",
			`import "std/array";
function main(): i32 { let xs: string[] = ["solo"]; write(xs.join(",")); return 0; }`,
			0,
			"solo",
		},
		{
			"arr-string-join-empty-sep",
			`import "std/array";
function main(): i32 { let xs: string[] = ["a", "b", "c"]; write(xs.join("")); return 0; }`,
			0,
			"abc",
		},
		{
			"arr-string-join-multi-char-sep",
			`import "std/array";
function main(): i32 { let xs: string[] = ["x", "y", "z"]; write(xs.join(" - ")); return 0; }`,
			0,
			"x - y - z",
		},
		{
			"str-method-len",
			"function main(): i32 { let s = \"hello\"; return s.len(); }",
			5,
			"",
		},
		{
			"str-method-is-empty-false",
			`import "std/string";
function main(): i32 { let s = "hi"; if (s.is_empty()) { return 1; } return 0; }`,
			0,
			"",
		},
		{
			"str-method-is-empty-true",
			`import "std/string";
function main(): i32 { let s = ""; if (s.is_empty()) { return 1; } return 0; }`,
			1,
			"",
		},
		{
			"arr-method-len",
			"function main(): i32 { let xs: i32[] = [10, 20, 30, 40]; return xs.len(); }",
			4,
			"",
		},
		{
			"arr-method-is-empty-false",
			`import "std/array";
function main(): i32 { let xs: i32[] = [1]; if (xs.is_empty()) { return 1; } return 0; }`,
			0,
			"",
		},
		{
			"arr-method-is-empty-true",
			`import "std/array";
function main(): i32 { let xs: i32[] = []; if (xs.is_empty()) { return 1; } return 0; }`,
			1,
			"",
		},
		{
			"arr-string-method-len",
			"function main(): i32 { let xs: string[] = [\"a\", \"b\"]; return xs.len(); }",
			2,
			"",
		},
		{
			"str-first-byte",
			`function main(): i32 { let s = "abc"; return s[0] as i32; }`,
			97,
			"",
		},
		{
			"str-last-byte",
			`function main(): i32 { let s = "abc"; return s[s.len() - 1] as i32; }`,
			99,
			"",
		},
		{
			"str-first-byte-uppercase",
			`function main(): i32 { let s = "Hello"; return s[0] as i32; }`,
			72,
			"",
		},
		{
			"str-last-byte-symbol",
			`function main(): i32 { let s = "hi!"; return s[s.len() - 1] as i32; }`,
			33,
			"",
		},
		{
			"str-bytes-len",
			`import "std/string";
function main(): i32 { let s = "abc"; let bs = s.bytes(); return bs.len(); }`,
			3,
			"",
		},
		{
			"str-bytes-value",
			`import "std/string";
function main(): i32 { let s = "A"; let bs = s.bytes(); return bs[0] as i32; }`,
			65,
			"",
		},
		{
			"str-bytes-multi",
			`import "std/string";
function main(): i32 { let s = "abc"; let bs = s.bytes(); print_int((bs[0] as i32) + (bs[1] as i32) + (bs[2] as i32)); return 0; }`,
			0,
			"294",
		},
		{
			"str-bytes-empty",
			`import "std/string";
function main(): i32 { let s = ""; let bs = s.bytes(); return bs.len(); }`,
			0,
			"",
		},
		{
			"str-lines-count",
			`import "std/string";
function main(): i32 { let s = "line1\nline2\nline3"; let ls = s.lines(); return ls.len(); }`,
			3,
			"",
		},
		{
			"str-lines-content",
			`import "std/string";
function main(): i32 { let s = "foo\nbar"; let ls = s.lines(); for l in ls { write(l); write("|"); } return 0; }`,
			0,
			"foo|bar|",
		},
		{
			"str-lines-single",
			`import "std/string";
function main(): i32 { let s = "alone"; let ls = s.lines(); return ls.len(); }`,
			1,
			"",
		},
		{
			"str-lines-trailing-newline",
			`import "std/string";
function main(): i32 { let s = "x\n"; let ls = s.lines(); return ls.len(); }`,
			1,
			"",
		},
		{
			"closure-uses-multiple-string-methods",
			"function (s: string) bang(): string { return s + \"!\"; } function main(): i32 { let msg = \"hi\"; let f = (): string => { return msg.bang() + \"?\"; }; write(f()); return 0; }",
			0,
			"hi!?",
		},
		{
			"closure-captures-closure-i32",
			"function main(): i32 { let inner = (): i32 => { return 42; }; let outer = (): i32 => { return inner(); }; return outer(); }",
			42,
			"",
		},
		{
			"closure-captures-string-closure",
			"function main(): i32 { let hello = (): string => { return \"hi\"; }; let wrap = (): string => { return hello() + \"!\"; }; write(wrap()); return 0; }",
			0,
			"hi!",
		},
		{
			"closure-nested-three-deep",
			"function main(): i32 { let k = 100; let a = (): i32 => { let b = (): i32 => { let c = (): i32 => { return k; }; return c(); }; return b(); }; return a(); }",
			100,
			"",
		},
		{
			"string-len-literal",
			"function main(): i32 { let s = \"hello\"; return s.len(); }",
			5,
			"",
		},
		{
			"string-print-ident",
			"function main(): i32 { let s = \"world\\n\"; write(s); return 0; }",
			0,
			"world\n",
		},
		{
			"string-byte-indexing",
			"function main(): i32 { let s = \"abc\"; return s[1] as i32; }",
			98,
			"",
		},
		{
			"string-concat",
			"function main(): i32 { let a = \"hi \"; let b = \"there\"; let c = a + b; write(c); return c.len(); }",
			8,
			"hi there",
		},
		{
			"string-eq-true",
			"function main(): i32 { let a = \"foo\"; let b = \"foo\"; if (a == b) { return 1; } return 0; }",
			1,
			"",
		},
		{
			"string-eq-false",
			"function main(): i32 { let a = \"foo\"; let b = \"bar\"; if (a == b) { return 1; } return 0; }",
			0,
			"",
		},
		{
			"string-slice",
			"function main(): i32 { let s = \"hello world\"; let sub = slice_unchecked(s, 6, 11); write(sub); return sub.len(); }",
			5,
			"world",
		},
		{
			"string-param-print",
			"function greet(s: string): i32 { write(s); return 0; } function main(): i32 { greet(\"hi!\\n\"); return 0; }",
			0,
			"hi!\n",
		},
		{
			"string-param-len",
			"function strlen(s: string): i32 { return s.len(); } function main(): i32 { return strlen(\"abcdef\"); }",
			6,
			"",
		},
		{
			"string-param-concat",
			"function shout(s: string): string { return s + \"!\"; } function main(): i32 { let out = shout(\"hi\"); write(out); return out.len(); }",
			3,
			"hi!",
		},
		{
			"string-lt-true",
			"function main(): i32 { let a = \"apple\"; let b = \"banana\"; if (a < b) { return 1; } return 0; }",
			1,
			"",
		},
		{
			"string-lt-false",
			"function main(): i32 { let a = \"banana\"; let b = \"apple\"; if (a < b) { return 1; } return 0; }",
			0,
			"",
		},
		{
			"string-lt-prefix",
			"function main(): i32 { let a = \"app\"; let b = \"apple\"; if (a < b) { return 1; } return 0; }",
			1,
			"",
		},
		{
			"string-le-equal",
			"function main(): i32 { let a = \"abc\"; let b = \"abc\"; if (a <= b) { return 1; } return 0; }",
			1,
			"",
		},
		{
			"string-gt-true",
			"function main(): i32 { let a = \"zebra\"; let b = \"apple\"; if (a > b) { return 1; } return 0; }",
			1,
			"",
		},
		{
			"string-ge-equal",
			"function main(): i32 { let a = \"xy\"; let b = \"xy\"; if (a >= b) { return 1; } return 0; }",
			1,
			"",
		},
		{
			"string-array-literal-print",
			"function main(): i32 { let arr = [\"hi\", \"bye\"]; write(arr[0]); write(\"\\n\"); write(arr[1]); write(\"\\n\"); return 0; }",
			0,
			"hi\nbye\n",
		},
		{
			"string-array-len-and-index",
			"function main(): i32 { let arr = [\"a\", \"bb\", \"ccc\"]; return arr[1].len() + arr.len() * 10; }",
			32,
			"",
		},
		{
			"string-array-for-in",
			"function main(): i32 { let arr = [\"one\", \"two\", \"three\"]; for s in arr { write(s); write(\"\\n\"); } return 0; }",
			0,
			"one\ntwo\nthree\n",
		},
		{
			"string-array-eq",
			"function main(): i32 { let arr = [\"x\", \"y\", \"z\"]; if (arr[1] == \"y\") { return 1; } return 0; }",
			1,
			"",
		},
		{
			"method-on-string-receiver",
			"function (s: string) shout(): string { return s + \"!\"; } function main(): i32 { let msg = \"hi\"; let out = msg.shout(); write(out); return out.len(); }",
			3,
			"hi!",
		},
		{
			"method-on-i32-receiver",
			"function (n: i32) twice(): i32 { return n * 2; } function main(): i32 { let x = 21; return x.twice(); }",
			42,
			"",
		},
		{
			"method-on-string-arg",
			"function (s: string) repeat3(): string { return s + s + s; } function main(): i32 { let m = \"ab\"; let out = m.repeat3(); write(out); return out.len(); }",
			6,
			"ababab",
		},
		{
			"method-on-string-with-args",
			"function (s: string) join_with(sep: string, other: string): string { return s + sep + other; } function main(): i32 { let a = \"foo\"; let b = \"bar\"; let out = a.join_with(\"-\", b); write(out); return 0; }",
			0,
			"foo-bar",
		},
		{
			"i32-dot-to-string",
			`import "std/i32";
function main(): i32 { let n: i32 = 42; let s = n.to_string(); write(s); return s.len(); }`,
			2,
			"42",
		},
		{
			"i32-dot-to-string-in-closure",
			`import "std/i32";
function main(): i32 { let n = 5; let f = (): string => { return n.to_string(); }; write(f()); return 0; }`,
			0,
			"5",
		},
		{
			"i32-dot-to-string-concat",
			`import "std/i32";
function main(): i32 { let n: i32 = 99; let msg: string = "value=" + n.to_string(); write(msg); return 0; }`,
			0,
			"value=99",
		},
		{
			"i32-dot-to-string-zero",
			`import "std/i32";
function main(): i32 { let n: i32 = 0; let s = n.to_string(); write(s); return s.len(); }`,
			1,
			"0",
		},
		{
			"i32-dot-to-string-negative",
			`import "std/i32";
function main(): i32 { let n: i32 = 0 - 7; let s = n.to_string(); write(s); return s.len(); }`,
			2,
			"-7",
		},
		{
			"string-dot-to-string-identity",
			`import "std/string";
function main(): i32 { let s = "hi"; let t = s.to_string(); write(t); return t.len(); }`,
			2,
			"hi",
		},
		{
			"eprint-literal-exits-clean",
			"function main(): i32 { eprint(\"error msg\\n\"); return 7; }",
			7,
			"",
		},
		{
			"eprint-ident-string",
			"function main(): i32 { let msg = \"oops\\n\"; eprint(msg); return 42; }",
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
			"function main(): i32 { print(\"out\"); eprint(\"err\\n\"); return 0; }",
			0,
			"out\n",
		},
		{
			"chr-uppercase-a",
			"function main(): i32 { let c = chr(65); write(c); return c.len(); }",
			1,
			"A",
		},
		{
			"chr-newline",
			"function main(): i32 { let c = chr(10); write(\"before\"); write(c); write(\"after\"); return 0; }",
			0,
			"before\nafter",
		},
		{
			"chr-zero",
			"function main(): i32 { let c = chr(0); return c.len(); }",
			1,
			"",
		},
		{
			"chr-concat-build-string",
			"function main(): i32 { let msg = chr(72) + chr(105) + chr(33); write(msg); return msg.len(); }",
			3,
			"Hi!",
		},
		{
			"chr-index-of-result",
			"function main(): i32 { let c = chr(98); if (c == \"b\") { return 1; } return 0; }",
			1,
			"",
		},
		{
			"exit-from-helper",
			"function check(): i32 { exit(7); return 0; } function main(): i32 { check(); return 99; }",
			7,
			"",
		},
		{
			"exit-before-print",
			"function main(): i32 { write(\"a\"); exit(3); print(\"b\"); return 0; }",
			3,
			"a",
		},
		{
			"exit-zero",
			"function main(): i32 { exit(0); return 5; }",
			0,
			"",
		},
		// Match-arm guards (`Pat when <expr> =>`): true guard runs the arm; a
		// false guard falls through to the next arm (the guard reads the binding).
		{"match-guard-pass", "enum O { Has(i32), Nil } function main(): i32 { let o: O = Has(8); match (o) { Has(n) when n > 5 => { return 1; }, _ => { return 2; } } return 0 - 1; }", 1, ""},
		{"match-guard-fallthrough", "enum O { Has(i32), Nil } function main(): i32 { let o: O = Has(3); match (o) { Has(n) when n > 5 => { return 1; }, _ => { return 2; } } return 0 - 1; }", 2, ""},
		// Match expressions (value position): IIFE + statement-match desugar.
		{"match-expr", "enum O { Has(i32), Nil } function main(): i32 { let o: O = Has(5); let x: i32 = match (o) { Has(n) => n, Nil => 0 }; return x; }", 5, ""},
		{"match-expr-other-arm", "enum O { Has(i32), Nil } function main(): i32 { let o: O = Nil; return match (o) { Has(n) => n, Nil => 42 }; }", 42, ""},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			code, stdout := runArm64(t, gcc, qemu, cli.emit(t, "arm64-linux", withPrintInt(tc.source)))
			if code != tc.expected {
				t.Errorf("exit code = %d, want %d\n--- source ---\n%s", code, tc.expected, tc.source)
			}
			if tc.stdout != "" && stdout != tc.stdout {
				t.Errorf("stdout = %q, want %q\n--- source ---\n%s", stdout, tc.stdout, tc.source)
			}
		})
	}

	// Negative: the checker must REJECT a program that uses an Option[i32]
	// (`.max()`) where an i32 is declared, rather than emitting a box pointer.
	t.Run("rejects-option-as-i32", func(t *testing.T) {
		bad := "import \"std/array\";\nfunction main(): i32 { let xs: i32[] = [1, 2, 3]; return xs.max(); }"
		out, stderr, err := cli.tryEmit(t, "arm64-linux", bad)
		if err == nil {
			t.Fatalf("expected the CLI to reject Option-as-i32, but it compiled\n--- asm ---\n%s", out)
		}
		if !strings.Contains(stderr, "E002") || !strings.Contains(stderr, "Option[i32]") {
			t.Errorf("expected E002 / Option[i32] diagnostic, got stderr:\n%s", stderr)
		}
	})
}
