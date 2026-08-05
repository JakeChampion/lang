package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/checker"
	"github.com/jakechampion/lang/internal/codegen/x86_64"
	"github.com/jakechampion/lang/internal/constfold"
	"github.com/jakechampion/lang/internal/modload"
)

// Sibling to TestSelfHostAsmRunX86_64 — exercises the
// asm_arm64.fern ARM64 codegen layer. The driver
// (asm_ir_run.fern (-target arm64)) is compiled on the host (x86_64),
// reads lang source from stdin, and prints aarch64 assembly
// to stdout. The Go test pipes each source in, gcc-assembles
// the output with aarch64-linux-gnu-gcc, then runs the
// resulting binary under qemu-aarch64 (or natively on arm64
// hosts) and asserts the exit code matches.
//
// Scope mirrors asm_arm64.fern's: i32 literals + arithmetic
// (+ - * / %) + unary `-` + `return` only. Locals / control
// flow / functions land in follow-up PRs.

func TestSelfHostAsmArm64Bootstrap(t *testing.T) {
	gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	// Build the driver as an x86_64 binary — the driver itself
	// runs on the test host, only its OUTPUT is arm64 asm.
	prog, _, err := modload.Load(filepath.Join(dir, "asm_ir_run.fern"))
	if err != nil {
		t.Fatalf("modload: %v", err)
	}
	if err := constfold.Fold(prog, nil); err != nil {
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
		{"and-both-true", "if (true && true) { return 1; } return 0;", 1, ""},
		{"and-left-false", "if (false && true) { return 1; } return 0;", 0, ""},
		{"and-right-false", "if (true && false) { return 1; } return 0;", 0, ""},
		{"and-short-circuits-rhs", "function side(): boolean { write(\"R\"); return true; } function main(): i32 { write(\"A\"); if (false && side()) { return 1; } write(\"B\"); return 0; }", 0, "AB"},
		{"or-both-false", "if (false || false) { return 1; } return 0;", 0, ""},
		{"or-left-true", "if (true || false) { return 1; } return 0;", 1, ""},
		{"or-right-true", "if (false || true) { return 1; } return 0;", 1, ""},
		{"or-short-circuits-rhs", "function side(): boolean { write(\"R\"); return false; } function main(): i32 { write(\"A\"); if (true || side()) { write(\"B\"); } return 0; }", 0, "AB"},
		{"and-or-mixed", "var a = true; var b = false; var c = true; if ((a && b) || c) { return 1; } return 0;", 1, ""},
		{"and-with-comparison", "var x = 5; if (x > 0 && x < 10) { return 1; } return 0;", 1, ""},
		{"not-true", "if (!true) { return 1; } return 0;", 0, ""},
		{"not-false", "if (!false) { return 1; } return 0;", 1, ""},
		{"not-comparison", "var x = 5; if (!(x < 0)) { return 1; } return 0;", 1, ""},
		{"not-double", "var b = true; if (!!b) { return 1; } return 0;", 1, ""},
		{"not-and", "var a = true; var b = false; if (!a && !b) { return 1; } return 2;", 2, ""},
		{"not-or-truthy", "var a = false; var b = true; if (!a || !b) { return 1; } return 2;", 1, ""},
		{"locals-single", "var x = 5; return x;", 5, ""},
		{"locals-three", "var a = 10; var b = 20; var c = 30; return a + b + c;", 60, ""},
		{"reassign", "var x = 5; x = x + 3; return x;", 8, ""},
		// `arena` is no longer a reserved word (the arena block + builtins
		// were removed) — it must parse as an ordinary identifier.
		{"arena-as-identifier", "var arena = 3; arena = arena + 4; return arena;", 7, ""},
		{"compound-assign", "var x = 1; x *= 6; x += 1; return x;", 7, ""},
		{"while-sum-counter", "var i = 1; var s = 0; while (i <= 5) { s += i; i += 1; } return s;", 15, ""},
		{"while-early-return", "var i = 0; while (i < 100) { if (i == 7) { return i; } i += 1; } return 0 - 1;", 7, ""},
		{"func-decl-call", "function add(x: i32, y: i32): i32 { return x + y; } function main(): i32 { return add(2, 3); }", 5, ""},
		{"func-three-args", "function sum3(a: i32, b: i32, c: i32): i32 { return a + b + c; } function main(): i32 { return sum3(10, 20, 30); }", 60, ""},
		{"recursive-factorial", "function fact(n: i32): i32 { if (n <= 1) { return 1; } return n * fact(n - 1); } function main(): i32 { return fact(5); }", 120, ""},
		{"recursive-fib", "function fib(n: i32): i32 { if (n < 2) { return n; } return fib(n - 1) + fib(n - 2); } function main(): i32 { return fib(8); }", 21, ""},
		{"mutual-recursion", "function is_even(n: i32): i32 { if (n == 0) { return 1; } return is_odd(n - 1); } function is_odd(n: i32): i32 { if (n == 0) { return 0; } return is_even(n - 1); } function main(): i32 { return is_even(6); }", 1, ""},
		{"func-with-local-vars", "function compute(a: i32): i32 { var b = a * 2; var c = b + 1; return c; } function main(): i32 { return compute(5); }", 11, ""},
		// Pipe operator |> — desugars `x |> f(args)` to `f(x, args)`.
		{"pipe-call", "function inc(n: i32): i32 { return n + 1; } function main(): i32 { return 5 |> inc(); }", 6, ""},
		{"pipe-bare-callee", "function inc(n: i32): i32 { return n + 1; } function main(): i32 { return 5 |> inc; }", 6, ""},
		{"pipe-extra-args", "function add(a: i32, b: i32): i32 { return a + b; } function main(): i32 { return 5 |> add(10); }", 15, ""},
		{"pipe-chained", "function inc(n: i32): i32 { return n + 1; } function dbl(n: i32): i32 { return n * 2; } function main(): i32 { return 5 |> inc() |> dbl(); }", 12, ""},
		{"pipe-binary-lhs", "function inc(n: i32): i32 { return n + 1; } function main(): i32 { return 2 + 3 |> inc(); }", 6, ""},
		// if-expressions — desugar to an immediately-invoked closure.
		{"if-expr-true", "function main(): i32 { var x: i32 = if (true) { 3 } else { 4 }; return x; }", 3, ""},
		{"if-expr-false", "function main(): i32 { var x: i32 = if (false) { 3 } else { 4 }; return x; }", 4, ""},
		{"if-expr-capture", "function main(): i32 { var n: i32 = 10; var x: i32 = if (n > 5) { n + 1 } else { 0 }; return x; }", 11, ""},
		{"if-expr-return", "function pick(c: i32): i32 { return if (c == 1) { 7 } else { 9 }; } function main(): i32 { return pick(0); }", 9, ""},
		{"if-expr-else-if", "function main(): i32 { var n: i32 = 2; var x: i32 = if (n == 1) { 10 } else if (n == 2) { 20 } else { 30 }; return x; }", 20, ""},
		{"if-expr-as-arg", "function id(n: i32): i32 { return n; } function main(): i32 { return id(if (true) { 5 } else { 6 }); }", 5, ""},
		{"direct-iife", "function main(): i32 { return (function(): i32 { return 3; })(); }", 3, ""},
		{"iife-with-args", "function main(): i32 { return (function(a: i32, b: i32): i32 { return a + b; })(4, 5); }", 9, ""},
		// Local (nested) functions — desugar to a closure-valued local.
		{"local-fn-basic", "function main(): i32 { function helper(): i32 { return 5; } return helper(); }", 5, ""},
		{"local-fn-args", "function main(): i32 { function add(a: i32, b: i32): i32 { return a + b; } return add(4, 5); }", 9, ""},
		{"local-fn-capture", "function main(): i32 { var n: i32 = 10; function bump(): i32 { return n + 1; } return bump(); }", 11, ""},
		{"local-fn-two", "function main(): i32 { function f(): i32 { return 2; } function g(): i32 { return 3; } return f() * g(); }", 6, ""},
		// defer — runs the action at function exit (LIFO, conditional via a
		// per-defer flag, return value captured before cleanup). Observed
		// via an array a caller mutates and reads back.
		{"defer-fires", "function inc(a: i32[]): i32 { defer a[0] = 9; return 1; } function main(): i32 { var arr = [0]; inc(arr); return arr[0]; }", 9, ""},
		{"defer-retval-before-cleanup", "function f(): i32 { var x = 5; defer x = 99; return x; } function main(): i32 { return f(); }", 5, ""},
		{"defer-lifo", "function f(a: i32[]): i32 { defer a[0] = 1; defer a[0] = 2; return 0; } function main(): i32 { var arr = [0]; f(arr); return arr[0]; }", 1, ""},
		{"defer-conditional-off", "function f(a: i32[], c: i32): i32 { if (c == 1) { defer a[0] = 7; } return 0; } function main(): i32 { var arr = [0]; f(arr, 0); return arr[0]; }", 0, ""},
		{"defer-conditional-on", "function f(a: i32[], c: i32): i32 { if (c == 1) { defer a[0] = 7; } return 0; } function main(): i32 { var arr = [0]; f(arr, 1); return arr[0]; }", 7, ""},
		{"defer-early-return", "function f(a: i32[], c: i32): i32 { defer a[0] = 5; if (c == 1) { return 0; } a[0] = 99; return 0; } function main(): i32 { var arr = [0]; f(arr, 1); return arr[0]; }", 5, ""},
		{"defer-loop-survives", "function f(a: i32[]): i32 { defer a[0] = a[0] + 50; var i = 0; while (i < 3) { a[0] = a[0] + 1; i = i + 1; } return 0; } function main(): i32 { var arr = [0]; f(arr); return arr[0]; }", 53, ""},
		{"hello-arm64", "print(\"Hello, ARM64!\"); return 0;", 0, "Hello, ARM64!\n"},
		{"print-twice", "print(\"line A\"); print(\"line B\"); return 0;", 0, "line A\nline B\n"},
		{"print-then-return", "print(\"out\"); return 7;", 7, "out\n"},
		{"print-int-literal", "function main(): i32 { print_int(42); write(\"\\n\"); return 0; }", 0, "42\n"},
		{"print-int-zero", "function main(): i32 { print_int(0); write(\"\\n\"); return 0; }", 0, "0\n"},
		{"print-int-negative", "function main(): i32 { print_int(0 - 7); write(\"\\n\"); return 0; }", 0, "-7\n"},
		{"print-int-fact", "function fact(n: i32): i32 { if (n <= 1) { return 1; } return n * fact(n - 1); } function main(): i32 { print_int(fact(8)); write(\"\\n\"); return 0; }", 0, "40320\n"},
		{
			"fizzbuzz-canonical-1-to-15",
			"function main(): i32 { " +
				"var i = 1; " +
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
				"function main(): i32 { var i = 0; while (i < 10) { print_int(fib(i)); write(\" \"); i = i + 1; } write(\"\\n\"); return 0; }",
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
			"function is_prime(n: i32): i32 { if (n < 2) { return 0; } var i = 2; while (i * i <= n) { if (n % i == 0) { return 0; } i = i + 1; } return 1; } " +
				"function main(): i32 { var i = 2; while (i <= 30) { if (is_prime(i) == 1) { print_int(i); write(\" \"); } i = i + 1; } write(\"\\n\"); return 0; }",
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
			// Struct element of a destructured tuple keeps its type →
			// receiver method dispatches via shape pointer (Box.bump),
			// not __fn_i32__bump. See the x86 mirror.
			"tuple-destructure-struct-method",
			"struct Box { n: i32 } " +
				"pub function (b: Box) bump(): i32 { return b.n + 1; } " +
				"function pair(): (i32, Box) { return (5, Box { n: 10 }); } " +
				"function main(): i32 { var (x, b) = pair(); return b.bump(); }",
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
				"function main(): i32 { var f: Foo = Foo { n: 0 }; " +
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
				"var w: Wrap = Wrap { n: 42 }; " +
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
			"function main(): i32 { " +
				"var m: Map[string, i32] = map_new(8); " +
				"m.insert(\"a\", 10); " +
				"var a: i32 = m.get_or(\"a\", 0); " +
				"var b: i32 = m.get_or(\"missing\", 99); " +
				"return a + b; }",
			109,
			"",
		},
		{
			// `u32` / `u64` / `i64` route through the i32 codegen path
			// so `(n as u32).to_string()` doesn't fall to struct
			// shape-dispatch. See x86 mirror.
			"wider-int-as-cast-to-string",
			"function main(): i32 { var a: u32 = 99 as u32; var b: u64 = 7 as u64; " +
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
			"function main(): i32 { " +
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
				"var nan: f64 = 0.0 / 0.0; var one: f64 = 1.0; " +
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
				"var a: f64 = 3.5; " +
				"if (f64_from_bits(f64_bits(a)) != a) { return 1; } " +
				"if (f32_from_bits(f32_bits(a)) != a) { return 2; } " +
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
				"var a: i32 = 0; var b: i32 = 0; " +
				"match (ok_path()) { Ok(v) => { a = v; }, Err(_) => { return 1; } } " +
				"match (err_path()) { Ok(_) => { return 2; }, Err(_) => { b = 2; } } " +
				"return a + b; }",
			42,
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
			"function main(): i32 { var a = [10, 20, 30]; return a.len(); }",
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
			"function main(): i32 { var a = [1, 2, 3, 4, 5]; var i = 0; var s = 0; while (i < a.len()) { s = s + a[i]; i = i + 1; } return s; }",
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
			// Capture is by REFERENCE — one shared cell — so the outer n = 99
			// is visible inside the closure, matching the interpreter, which
			// defines the semantics (closureconv.BoxMutatedCaptures, #2896 /
			// #5301). This expected 5 back when the legacy AST arm64 backend
			// (this table runs the driver without -ir) still snapshotted at
			// make time; that divergence has since closed, so the legacy path
			// now agrees with the oracle and the IR path (#5479).
			"closure-capture-by-reference",
			"function main(): i32 { var n = 5; var f = function (): i32 { return n; }; n = 99; return f(); }",
			99,
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
			"plain-string-fn-result-print",
			"function get(): string { return \"hi\"; } function main(): i32 { write(get()); return 0; }",
			0,
			"hi",
		},
		{
			"closure-string-capture",
			"function main(): i32 { var s = \"hi\"; var f = function (): string { return s; }; write(f()); return 0; }",
			0,
			"hi",
		},
		{
			"closure-string-concat-capture",
			"function main(): i32 { var prefix = \"hello-\"; var f = function (suf: string): string { return prefix + suf; }; write(f(\"world\")); return 0; }",
			0,
			"hello-world",
		},
		{
			"closure-bool-returning",
			"function main(): i32 { var x = 5; var is_big = function (): boolean { return x > 3; }; if (is_big()) { return 1; } return 0; }",
			1,
			"",
		},
		{
			"closure-string-multi-capture",
			"function main(): i32 { var a = \"foo\"; var b = \"bar\"; var f = function (): string { return a + b; }; write(f()); return 0; }",
			0,
			"foobar",
		},
		{
			"closure-i32-returning-string-capture",
			"function main(): i32 { var s = \"hello\"; var f = function (): i32 { return s.len(); }; return f(); }",
			5,
			"",
		},
		{
			"arr-i32-push-len",
			"function main(): i32 { var xs: i32[] = [1, 2, 3]; xs = xs.append(4); return xs.len(); }",
			4,
			"",
		},
		{
			"arr-i32-push-last",
			"function main(): i32 { var xs: i32[] = [10, 20, 30]; xs = xs.append(99); return xs[3]; }",
			99,
			"",
		},
		{
			"arr-i32-push-preserves-existing",
			"function main(): i32 { var xs: i32[] = [1, 2, 3]; xs = xs.append(4); return xs[0] + xs[1] + xs[2] + xs[3]; }",
			10,
			"",
		},
		{
			"arr-i32-push-empty",
			"function main(): i32 { var xs: i32[] = []; xs = xs.append(42); return xs[0]; }",
			42,
			"",
		},
		{
			"arr-string-push",
			"function main(): i32 { var xs: string[] = [\"a\", \"b\"]; xs = xs.append(\"c\"); for s in xs { write(s); } return xs.len(); }",
			3,
			"abc",
		},
		{
			"arr-i32-push-chain",
			"function main(): i32 { var xs: i32[] = []; xs = xs.append(1); xs = xs.append(2); xs = xs.append(3); return xs[0] + xs[1] + xs[2]; }",
			6,
			"",
		},
		{
			"str-method-split-len",
			"function main(): i32 { var s = \"a,b,c\"; var parts = s.split(\",\"); return parts.len(); }",
			3,
			"",
		},
		{
			"str-method-split-content",
			"function main(): i32 { var s = \"x|y|z\"; var parts = s.split(\"|\"); for t in parts { write(t); } return 0; }",
			0,
			"xyz",
		},
		{
			"str-method-contains-true",
			"function main(): i32 { var s = \"hello world\"; if (s.contains(\"world\")) { return 1; } return 0; }",
			1,
			"",
		},
		{
			"str-method-contains-false",
			"function main(): i32 { var s = \"hello world\"; if (s.contains(\"xyz\")) { return 1; } return 0; }",
			0,
			"",
		},
		{
			"str-method-starts-with",
			"function main(): i32 { var s = \"hello\"; if (s.starts_with(\"he\")) { return 1; } return 0; }",
			1,
			"",
		},
		{
			"str-method-ends-with",
			"function main(): i32 { var s = \"hello\"; if (s.ends_with(\"lo\")) { return 1; } return 0; }",
			1,
			"",
		},
		{
			"str-method-index-of",
			"function main(): i32 { var s = \"hello\"; return s.index_of(\"ll\"); }",
			2,
			"",
		},
		{
			"str-method-trim",
			"function main(): i32 { var s = \"  hi  \"; var t = s.trim(); write(t); return t.len(); }",
			2,
			"hi",
		},
		{
			"str-method-to-upper",
			"function main(): i32 { var s = \"hello\"; write(s.to_ascii_upper()); return 0; }",
			0,
			"HELLO",
		},
		{
			"str-method-to-lower",
			"function main(): i32 { var s = \"HELLO\"; write(s.to_ascii_lower()); return 0; }",
			0,
			"hello",
		},
		{
			"str-method-repeat",
			"function main(): i32 { var s = \"ab\"; write(s.repeat(3)); return 0; }",
			0,
			"ababab",
		},
		{
			"str-method-replace",
			"function main(): i32 { var s = \"hello\"; write(s.replace(\"l\", \"L\")); return 0; }",
			0,
			"heLLo",
		},
		{
			"str-method-chain",
			"function main(): i32 { var s = \"  HELLO WORLD  \"; write(s.trim().to_ascii_lower()); return 0; }",
			0,
			"hello world",
		},
		{
			"str-literal-method-direct",
			"function main(): i32 { write(\"hello\".to_ascii_upper()); return 0; }",
			0,
			"HELLO",
		},
		{
			"str-literal-method-chain",
			"function main(): i32 { write(\"  HI  \".trim().repeat(2)); return 0; }",
			0,
			"HIHI",
		},
		{
			"arr-i32-slice-len",
			"function main(): i32 { var xs: i32[] = [10, 20, 30, 40, 50]; var ys = xs[1:4]; return ys.len(); }",
			3,
			"",
		},
		{
			"arr-i32-slice-content",
			"function main(): i32 { var xs: i32[] = [10, 20, 30, 40, 50]; var ys = xs[1:4]; return ys[0] + ys[1] + ys[2]; }",
			90,
			"",
		},
		{
			"arr-i32-slice-empty",
			"function main(): i32 { var xs: i32[] = [1, 2, 3]; var ys = xs[1:1]; return ys.len(); }",
			0,
			"",
		},
		{
			"arr-i32-slice-full",
			"function main(): i32 { var xs: i32[] = [7, 8, 9]; var ys = xs[0:3]; return ys[0] + ys[1] + ys[2]; }",
			24,
			"",
		},
		{
			"arr-string-slice",
			"function main(): i32 { var xs: string[] = [\"a\", \"b\", \"c\", \"d\"]; var ys = xs[1:3]; for s in ys { write(s); } return ys.len(); }",
			2,
			"bc",
		},
		// 64-bit-element arrays (i64[] / f64[]): the native arm64 backend
		// already uses 8-byte element slots, so values above 2^31 round-trip.
		// Previously untested; these lock it in (mirror the wasm i64arr-* cases).
		{
			"arr-i64-literal-index-large",
			"function main(): i32 { var xs: i64[] = [5000000000, 42]; if (xs[0] == 5000000000) { return xs[1] as i32; } return 0; }",
			42,
			"",
		},
		{
			"arr-i64-for-sum",
			"function main(): i32 { var xs: i64[] = [3, 5, 90]; var s: i64 = 0; for v in xs { s = s + v; } return s as i32; }",
			98,
			"",
		},
		{
			"arr-i64-set-index-large",
			"function main(): i32 { var xs: i64[] = [1, 2, 3]; xs[1] = 5000000000; if (xs[1] == 5000000000) { return 7; } return 0; }",
			7,
			"",
		},
		{
			"arr-i64-push-grow",
			"function main(): i32 { var xs: i64[] = [10]; xs = xs.append(20); xs = xs.append(5000000000); if (xs[2] == 5000000000) { return (xs[0] + xs[1]) as i32; } return 0; }",
			30,
			"",
		},
		{
			"arr-i64-slice",
			"function main(): i32 { var xs: i64[] = [10, 20, 30, 40]; var ys = xs[1:3]; return (ys[0] + ys[1]) as i32; }",
			50,
			"",
		},
		{
			"arr-f64-for-sum",
			"function main(): i32 { var xs: f64[] = [1.5, 2.5, 3.0]; var s: f64 = 0.0; for v in xs { s = s + v; } return s as i32; }",
			7,
			"",
		},
		{
			"i32-abs-positive",
			"function main(): i32 { var n: i32 = 7; return n.abs(); }",
			7,
			"",
		},
		{
			"i32-abs-negative",
			"function main(): i32 { var n: i32 = 0 - 42; return n.abs(); }",
			42,
			"",
		},
		{
			"i32-abs-zero",
			"function main(): i32 { var n: i32 = 0; return n.abs(); }",
			0,
			"",
		},
		{
			"i32-is-zero-true",
			"function main(): i32 { var n: i32 = 0; if (n.is_zero()) { return 1; } return 0; }",
			1,
			"",
		},
		{
			"i32-is-zero-false",
			"function main(): i32 { var n: i32 = 5; if (n.is_zero()) { return 1; } return 0; }",
			0,
			"",
		},
		{
			"i32-is-positive-true",
			"function main(): i32 { var n: i32 = 5; if (n.is_positive()) { return 1; } return 0; }",
			1,
			"",
		},
		{
			"i32-is-positive-false-zero",
			"function main(): i32 { var n: i32 = 0; if (n.is_positive()) { return 1; } return 0; }",
			0,
			"",
		},
		{
			"i32-is-positive-false-negative",
			"function main(): i32 { var n: i32 = 0 - 5; if (n.is_positive()) { return 1; } return 0; }",
			0,
			"",
		},
		{
			"i32-is-negative-true",
			"function main(): i32 { var n: i32 = 0 - 5; if (n.is_negative()) { return 1; } return 0; }",
			1,
			"",
		},
		{
			"i32-is-negative-false-zero",
			"function main(): i32 { var n: i32 = 0; if (n.is_negative()) { return 1; } return 0; }",
			0,
			"",
		},
		{
			"i32-is-even-true",
			"function main(): i32 { var n: i32 = 4; if (n.is_even()) { return 1; } return 0; }",
			1,
			"",
		},
		{
			"i32-is-even-false",
			"function main(): i32 { var n: i32 = 7; if (n.is_even()) { return 1; } return 0; }",
			0,
			"",
		},
		{
			"i32-is-odd-true",
			"function main(): i32 { var n: i32 = 9; if (n.is_odd()) { return 1; } return 0; }",
			1,
			"",
		},
		{
			"i32-is-odd-false",
			"function main(): i32 { var n: i32 = 8; if (n.is_odd()) { return 1; } return 0; }",
			0,
			"",
		},
		{
			"i32-is-even-zero",
			"function main(): i32 { var n: i32 = 0; if (n.is_even()) { return 1; } return 0; }",
			1,
			"",
		},
		{
			"i32-sign-positive",
			"function main(): i32 { var n: i32 = 42; return n.sign(); }",
			1,
			"",
		},
		{
			"i32-sign-zero",
			"function main(): i32 { var n: i32 = 0; return n.sign(); }",
			0,
			"",
		},
		{
			"i32-sign-negative",
			"function main(): i32 { var n: i32 = 0 - 7; print_int(n.sign()); return 0; }",
			0,
			"-1",
		},
		{
			"i32-pow-2-10",
			"function main(): i32 { var n: i32 = 2; print_int(n.pow(10)); return 0; }",
			0,
			"1024",
		},
		{
			"i32-pow-exp-zero",
			"function main(): i32 { var n: i32 = 7; return n.pow(0); }",
			1,
			"",
		},
		{
			"i32-pow-base-zero",
			"function main(): i32 { var n: i32 = 0; return n.pow(5); }",
			0,
			"",
		},
		{
			"i32-pow-3-5",
			"function main(): i32 { var n: i32 = 3; print_int(n.pow(5)); return 0; }",
			0,
			"243",
		},
		{
			"i32-clamp-in-range",
			"function main(): i32 { var n: i32 = 5; return n.clamp(0, 10); }",
			5,
			"",
		},
		{
			"i32-clamp-below",
			"function main(): i32 { var n: i32 = 0 - 3; return n.clamp(0, 10); }",
			0,
			"",
		},
		{
			"i32-clamp-above",
			"function main(): i32 { var n: i32 = 99; return n.clamp(0, 10); }",
			10,
			"",
		},
		{
			"i32-clamp-equals-low",
			"function main(): i32 { var n: i32 = 0; return n.clamp(0, 10); }",
			0,
			"",
		},
		{
			"i32-clamp-equals-high",
			"function main(): i32 { var n: i32 = 10; return n.clamp(0, 10); }",
			10,
			"",
		},
		{
			"i32-min-pick-first",
			"function main(): i32 { var a: i32 = 3; return a.min(7); }",
			3,
			"",
		},
		{
			"i32-min-pick-second",
			"function main(): i32 { var a: i32 = 9; return a.min(4); }",
			4,
			"",
		},
		{
			"i32-max-pick-first",
			"function main(): i32 { var a: i32 = 8; return a.max(3); }",
			8,
			"",
		},
		{
			"i32-max-pick-second",
			"function main(): i32 { var a: i32 = 2; return a.max(11); }",
			11,
			"",
		},
		{
			"i32-min-equal",
			"function main(): i32 { var a: i32 = 5; return a.min(5); }",
			5,
			"",
		},
		{
			"arr-i32-index-of-found-first",
			"function main(): i32 { var xs: i32[] = [10, 20, 30, 40]; return xs.index_of(10); }",
			0,
			"",
		},
		{
			"arr-i32-index-of-found-middle",
			"function main(): i32 { var xs: i32[] = [10, 20, 30, 40]; return xs.index_of(30); }",
			2,
			"",
		},
		{
			"arr-i32-index-of-not-found",
			"function main(): i32 { var xs: i32[] = [10, 20, 30]; var r = xs.index_of(99); if (r < 0) { return 1; } return 0; }",
			1,
			"",
		},
		{
			"arr-i32-contains-true",
			"function main(): i32 { var xs: i32[] = [10, 20, 30]; if (xs.contains(20)) { return 1; } return 0; }",
			1,
			"",
		},
		{
			"arr-i32-contains-false",
			"function main(): i32 { var xs: i32[] = [10, 20, 30]; if (xs.contains(99)) { return 1; } return 0; }",
			0,
			"",
		},
		{
			"arr-i32-contains-empty",
			"function main(): i32 { var xs: i32[] = []; if (xs.contains(42)) { return 1; } return 0; }",
			0,
			"",
		},
		{
			"arr-i32-reverse",
			"function main(): i32 { var xs: i32[] = [1, 2, 3, 4]; var ys = xs.reverse(); return ys[0] - ys[3]; }",
			3,
			"",
		},
		{
			"arr-i32-reverse-preserves-source",
			"function main(): i32 { var xs: i32[] = [1, 2, 3]; var ys = xs.reverse(); return xs[0] + xs[1] + xs[2] + ys[0] + ys[1] + ys[2]; }",
			12,
			"",
		},
		{
			"arr-i32-reverse-empty",
			"function main(): i32 { var xs: i32[] = []; var ys = xs.reverse(); return ys.len(); }",
			0,
			"",
		},
		{
			"arr-i32-reverse-single",
			"function main(): i32 { var xs: i32[] = [42]; var ys = xs.reverse(); return ys[0]; }",
			42,
			"",
		},
		{
			"arr-string-reverse",
			"function main(): i32 { var xs: string[] = [\"a\", \"b\", \"c\"]; var ys = xs.reverse(); for s in ys { write(s); } return 0; }",
			0,
			"cba",
		},
		{
			"arr-i32-concat-len",
			"function main(): i32 { var a: i32[] = [1, 2]; var b: i32[] = [3, 4, 5]; var c = a.concat(b); return c.len(); }",
			5,
			"",
		},
		{
			"arr-i32-concat-content",
			"function main(): i32 { var a: i32[] = [1, 2]; var b: i32[] = [10, 20]; var c = a.concat(b); return c[0] + c[1] + c[2] + c[3]; }",
			33,
			"",
		},
		{
			"arr-i32-concat-empty-lhs",
			"function main(): i32 { var a: i32[] = []; var b: i32[] = [1, 2, 3]; var c = a.concat(b); return c[0] + c[1] + c[2]; }",
			6,
			"",
		},
		{
			"arr-i32-concat-empty-rhs",
			"function main(): i32 { var a: i32[] = [4, 5]; var b: i32[] = []; var c = a.concat(b); return c[0] + c[1]; }",
			9,
			"",
		},
		{
			"arr-string-concat",
			"function main(): i32 { var a: string[] = [\"x\", \"y\"]; var b: string[] = [\"z\"]; var c = a.concat(b); for s in c { write(s); } return c.len(); }",
			3,
			"xyz",
		},
		{
			"arr-i32-first",
			"function main(): i32 { var xs: i32[] = [10, 20, 30]; return xs.first(); }",
			10,
			"",
		},
		{
			"arr-i32-last",
			"function main(): i32 { var xs: i32[] = [10, 20, 30]; return xs.last(); }",
			30,
			"",
		},
		{
			"arr-i32-first-single",
			"function main(): i32 { var xs: i32[] = [99]; return xs.first(); }",
			99,
			"",
		},
		{
			"arr-i32-last-single",
			"function main(): i32 { var xs: i32[] = [99]; return xs.last(); }",
			99,
			"",
		},
		{
			"arr-string-first",
			"function main(): i32 { var xs: string[] = [\"hello\", \"world\"]; write(xs.first()); return 0; }",
			0,
			"hello",
		},
		{
			"arr-string-last",
			"function main(): i32 { var xs: string[] = [\"hello\", \"world\"]; write(xs.last()); return 0; }",
			0,
			"world",
		},
		{
			"arr-string-index-of-found",
			"function main(): i32 { var xs: string[] = [\"a\", \"b\", \"c\"]; return xs.index_of(\"b\"); }",
			1,
			"",
		},
		{
			"arr-string-index-of-not-found",
			"function main(): i32 { var xs: string[] = [\"a\", \"b\", \"c\"]; var r = xs.index_of(\"z\"); if (r < 0) { return 1; } return 0; }",
			1,
			"",
		},
		{
			"arr-string-contains-true",
			"function main(): i32 { var xs: string[] = [\"foo\", \"bar\"]; if (xs.contains(\"bar\")) { return 1; } return 0; }",
			1,
			"",
		},
		{
			"arr-string-contains-false",
			"function main(): i32 { var xs: string[] = [\"foo\", \"bar\"]; if (xs.contains(\"baz\")) { return 1; } return 0; }",
			0,
			"",
		},
		{
			"arr-string-contains-content-equality",
			"function main(): i32 { var x = \"hello\"; var y = \"hel\" + \"lo\"; var xs: string[] = [x]; if (xs.contains(y)) { return 1; } return 0; }",
			1,
			"",
		},
		{
			"arr-string-join-basic",
			"function main(): i32 { var xs: string[] = [\"a\", \"b\", \"c\"]; write(xs.join(\",\")); return 0; }",
			0,
			"a,b,c",
		},
		{
			"arr-string-join-empty",
			"function main(): i32 { var xs: string[] = []; var r = xs.join(\",\"); write(\"[\"); write(r); write(\"]\"); return r.len(); }",
			0,
			"[]",
		},
		{
			"arr-string-join-single",
			"function main(): i32 { var xs: string[] = [\"solo\"]; write(xs.join(\",\")); return 0; }",
			0,
			"solo",
		},
		{
			"arr-string-join-empty-sep",
			"function main(): i32 { var xs: string[] = [\"a\", \"b\", \"c\"]; write(xs.join(\"\")); return 0; }",
			0,
			"abc",
		},
		{
			"arr-string-join-multi-char-sep",
			"function main(): i32 { var xs: string[] = [\"x\", \"y\", \"z\"]; write(xs.join(\" - \")); return 0; }",
			0,
			"x - y - z",
		},
		{
			"str-method-len",
			"function main(): i32 { var s = \"hello\"; return s.len(); }",
			5,
			"",
		},
		{
			"str-method-is-empty-false",
			"function main(): i32 { var s = \"hi\"; if (s.is_empty()) { return 1; } return 0; }",
			0,
			"",
		},
		{
			"str-method-is-empty-true",
			"function main(): i32 { var s = \"\"; if (s.is_empty()) { return 1; } return 0; }",
			1,
			"",
		},
		{
			"arr-method-len",
			"function main(): i32 { var xs: i32[] = [10, 20, 30, 40]; return xs.len(); }",
			4,
			"",
		},
		{
			"arr-method-is-empty-false",
			"function main(): i32 { var xs: i32[] = [1]; if (xs.is_empty()) { return 1; } return 0; }",
			0,
			"",
		},
		{
			"arr-method-is-empty-true",
			"function main(): i32 { var xs: i32[] = []; if (xs.is_empty()) { return 1; } return 0; }",
			1,
			"",
		},
		{
			"arr-string-method-len",
			"function main(): i32 { var xs: string[] = [\"a\", \"b\"]; return xs.len(); }",
			2,
			"",
		},
		{
			"arr-i32-sum",
			"function main(): i32 { var xs: i32[] = [1, 2, 3, 4, 5]; return xs.sum(); }",
			15,
			"",
		},
		{
			"arr-i32-sum-empty",
			"function main(): i32 { var xs: i32[] = []; return xs.sum(); }",
			0,
			"",
		},
		{
			"arr-i32-sum-single",
			"function main(): i32 { var xs: i32[] = [42]; return xs.sum(); }",
			42,
			"",
		},
		{
			"arr-i32-sum-negatives",
			"function main(): i32 { var xs: i32[] = [10, 0 - 3, 0 - 2, 5]; return xs.sum(); }",
			10,
			"",
		},
		{
			"str-first-byte",
			"function main(): i32 { var s = \"abc\"; return s.first_byte(); }",
			97,
			"",
		},
		{
			"str-last-byte",
			"function main(): i32 { var s = \"abc\"; return s.last_byte(); }",
			99,
			"",
		},
		{
			"str-first-byte-uppercase",
			"function main(): i32 { var s = \"Hello\"; return s.first_byte(); }",
			72,
			"",
		},
		{
			"str-last-byte-symbol",
			"function main(): i32 { var s = \"hi!\"; return s.last_byte(); }",
			33,
			"",
		},
		{
			"str-reverse-basic",
			"function main(): i32 { var s = \"hello\"; write(s.reverse()); return 0; }",
			0,
			"olleh",
		},
		{
			"str-reverse-empty",
			"function main(): i32 { var s = \"\"; var r = s.reverse(); return r.len(); }",
			0,
			"",
		},
		{
			"str-reverse-single",
			"function main(): i32 { var s = \"x\"; write(s.reverse()); return 0; }",
			0,
			"x",
		},
		{
			"str-reverse-palindrome",
			"function main(): i32 { var s = \"madam\"; if (s == s.reverse()) { return 1; } return 0; }",
			1,
			"",
		},
		{
			"str-bytes-len",
			"function main(): i32 { var s = \"abc\"; var bs = s.bytes(); return bs.len(); }",
			3,
			"",
		},
		{
			"str-bytes-value",
			"function main(): i32 { var s = \"A\"; var bs = s.bytes(); return bs[0]; }",
			65,
			"",
		},
		{
			"str-bytes-multi",
			"function main(): i32 { var s = \"abc\"; var bs = s.bytes(); print_int(bs[0] + bs[1] + bs[2]); return 0; }",
			0,
			"294",
		},
		{
			"str-bytes-sum",
			"function main(): i32 { var s = \"abc\"; print_int(s.bytes().sum()); return 0; }",
			0,
			"294",
		},
		{
			"str-bytes-empty",
			"function main(): i32 { var s = \"\"; var bs = s.bytes(); return bs.len(); }",
			0,
			"",
		},
		{
			"str-lines-count",
			"function main(): i32 { var s = \"line1\\nline2\\nline3\"; var ls = s.lines(); return ls.len(); }",
			3,
			"",
		},
		{
			"str-lines-content",
			"function main(): i32 { var s = \"foo\\nbar\"; var ls = s.lines(); for l in ls { write(l); write(\"|\"); } return 0; }",
			0,
			"foo|bar|",
		},
		{
			"str-lines-single",
			"function main(): i32 { var s = \"alone\"; var ls = s.lines(); return ls.len(); }",
			1,
			"",
		},
		{
			"str-lines-trailing-newline",
			"function main(): i32 { var s = \"x\\n\"; var ls = s.lines(); return ls.len(); }",
			1,
			"",
		},
		{
			"str-chars-len",
			"function main(): i32 { var s = \"hello\"; var cs = s.chars(); return cs.len(); }",
			5,
			"",
		},
		{
			"str-chars-content",
			"function main(): i32 { var s = \"abc\"; var cs = s.chars(); for c in cs { write(c); write(\"|\"); } return 0; }",
			0,
			"a|b|c|",
		},
		{
			"str-chars-empty",
			"function main(): i32 { var s = \"\"; var cs = s.chars(); return cs.len(); }",
			0,
			"",
		},
		{
			"str-chars-index",
			"function main(): i32 { var s = \"xyz\"; var cs = s.chars(); write(cs[1]); return 0; }",
			0,
			"y",
		},
		{
			"arr-i32-product",
			"function main(): i32 { var xs: i32[] = [2, 3, 5]; return xs.product(); }",
			30,
			"",
		},
		{
			"arr-i32-product-empty",
			"function main(): i32 { var xs: i32[] = []; return xs.product(); }",
			1,
			"",
		},
		{
			"arr-i32-product-with-zero",
			"function main(): i32 { var xs: i32[] = [4, 0, 7]; return xs.product(); }",
			0,
			"",
		},
		{
			"arr-i32-min",
			"function main(): i32 { var xs: i32[] = [5, 2, 8, 1, 7]; match (xs.min()) { Some(v) => { return v; }, None => { return 0 - 1; } } return 0 - 2; }",
			1,
			"",
		},
		{
			"arr-i32-max",
			"function main(): i32 { var xs: i32[] = [5, 2, 8, 1, 7]; match (xs.max()) { Some(v) => { return v; }, None => { return 0 - 1; } } return 0 - 2; }",
			8,
			"",
		},
		{
			"arr-i32-min-single",
			"function main(): i32 { var xs: i32[] = [42]; match (xs.min()) { Some(v) => { return v; }, None => { return 0 - 1; } } return 0 - 2; }",
			42,
			"",
		},
		{
			"arr-i32-max-single",
			"function main(): i32 { var xs: i32[] = [42]; match (xs.max()) { Some(v) => { return v; }, None => { return 0 - 1; } } return 0 - 2; }",
			42,
			"",
		},
		{
			"arr-i32-min-negatives",
			"function main(): i32 { var xs: i32[] = [3, 0 - 5, 1]; match (xs.min()) { Some(v) => { print_int(v); }, None => { } } return 0; }",
			0,
			"-5",
		},
		{
			"arr-i32-max-negatives",
			"function main(): i32 { var xs: i32[] = [0 - 9, 0 - 2, 0 - 5]; match (xs.max()) { Some(v) => { print_int(v); }, None => { } } return 0; }",
			0,
			"-2",
		},
		{
			"arr-i32-max-empty",
			"function main(): i32 { var xs: i32[] = []; match (xs.max()) { Some(v) => { return v; }, None => { return 99; } } return 0 - 2; }",
			99,
			"",
		},
		{
			"closure-uses-multiple-string-methods",
			"function (s: string) bang(): string { return s + \"!\"; } function main(): i32 { var msg = \"hi\"; var f = function (): string { return msg.bang() + \"?\"; }; write(f()); return 0; }",
			0,
			"hi!?",
		},
		{
			"closure-captures-closure-i32",
			"function main(): i32 { var inner = function (): i32 { return 42; }; var outer = function (): i32 { return inner(); }; return outer(); }",
			42,
			"",
		},
		{
			"closure-captures-string-closure",
			"function main(): i32 { var hello = function (): string { return \"hi\"; }; var wrap = function (): string { return hello() + \"!\"; }; write(wrap()); return 0; }",
			0,
			"hi!",
		},
		{
			"closure-nested-three-deep",
			"function main(): i32 { var k = 100; var a = function (): i32 { var b = function (): i32 { var c = function (): i32 { return k; }; return c(); }; return b(); }; return a(); }",
			100,
			"",
		},
		{
			"string-len-literal",
			"function main(): i32 { var s = \"hello\"; return s.len(); }",
			5,
			"",
		},
		{
			"string-print-ident",
			"function main(): i32 { var s = \"world\\n\"; write(s); return 0; }",
			0,
			"world\n",
		},
		{
			"string-byte-indexing",
			"function main(): i32 { var s = \"abc\"; return s[1] as i32; }",
			98,
			"",
		},
		{
			"string-concat",
			"function main(): i32 { var a = \"hi \"; var b = \"there\"; var c = a + b; write(c); return c.len(); }",
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
			"function main(): i32 { var s = \"hello world\"; var sub = s[6:11]; write(sub); return sub.len(); }",
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
			"function shout(s: string): string { return s + \"!\"; } function main(): i32 { var out = shout(\"hi\"); write(out); return out.len(); }",
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
			"function main(): i32 { var arr = [\"hi\", \"bye\"]; write(arr[0]); write(\"\\n\"); write(arr[1]); write(\"\\n\"); return 0; }",
			0,
			"hi\nbye\n",
		},
		{
			"string-array-len-and-index",
			"function main(): i32 { var arr = [\"a\", \"bb\", \"ccc\"]; return arr[1].len() + arr.len() * 10; }",
			32,
			"",
		},
		{
			"string-array-for-in",
			"function main(): i32 { var arr = [\"one\", \"two\", \"three\"]; for s in arr { write(s); write(\"\\n\"); } return 0; }",
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
			"function (s: string) shout(): string { return s + \"!\"; } function main(): i32 { var msg = \"hi\"; var out = msg.shout(); write(out); return out.len(); }",
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
			"function (s: string) repeat3(): string { return s + s + s; } function main(): i32 { var m = \"ab\"; var out = m.repeat3(); write(out); return out.len(); }",
			6,
			"ababab",
		},
		{
			"method-on-string-with-args",
			"function (s: string) join_with(sep: string, other: string): string { return s + sep + other; } function main(): i32 { var a = \"foo\"; var b = \"bar\"; var out = a.join_with(\"-\", b); write(out); return 0; }",
			0,
			"foo-bar",
		},
		{
			"i32-to-string-zero",
			"function main(): i32 { var s = i32_to_string(0); write(s); return s.len(); }",
			1,
			"0",
		},
		{
			"i32-dot-to-string",
			"function main(): i32 { var n: i32 = 42; var s = n.to_string(); write(s); return s.len(); }",
			2,
			"42",
		},
		{
			"i32-dot-to-string-in-closure",
			"function main(): i32 { var n = 5; var f = function (): string { return n.to_string(); }; write(f()); return 0; }",
			0,
			"5",
		},
		{
			"i32-dot-to-string-concat",
			"function main(): i32 { var n: i32 = 99; var msg: string = \"value=\" + n.to_string(); write(msg); return 0; }",
			0,
			"value=99",
		},
		{
			"i32-dot-to-string-zero",
			"function main(): i32 { var n: i32 = 0; var s = n.to_string(); write(s); return s.len(); }",
			1,
			"0",
		},
		{
			"i32-dot-to-string-negative",
			"function main(): i32 { var n: i32 = 0 - 7; var s = n.to_string(); write(s); return s.len(); }",
			2,
			"-7",
		},
		{
			"string-dot-to-string-identity",
			"function main(): i32 { var s = \"hi\"; var t = s.to_string(); write(t); return t.len(); }",
			2,
			"hi",
		},
		{
			"i32-to-string-positive",
			"function main(): i32 { var s = i32_to_string(12345); write(s); return s.len(); }",
			5,
			"12345",
		},
		{
			"i32-to-string-negative",
			"function main(): i32 { var s = i32_to_string(0 - 42); write(s); return s.len(); }",
			3,
			"-42",
		},
		{
			"i32-to-string-concat",
			"function main(): i32 { var n = 7; var msg = \"answer: \" + i32_to_string(n); write(msg); return 0; }",
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
			"function main(): i32 { print(\"out\"); eprint(\"err\\n\"); return 0; }",
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
		{
			"str-trim-spaces",
			"function main(): i32 { var t = str_trim(\"   hello   \"); write(t); return t.len(); }",
			5,
			"hello",
		},
		{
			"str-trim-tabs-newlines",
			"function main(): i32 { var t = str_trim(\"\\t\\n hi \\r\\n\"); write(t); return t.len(); }",
			2,
			"hi",
		},
		{
			"str-trim-no-whitespace",
			"function main(): i32 { var t = str_trim(\"abc\"); write(t); return t.len(); }",
			3,
			"abc",
		},
		{
			"str-trim-empty",
			"function main(): i32 { var t = str_trim(\"\"); return t.len(); }",
			0,
			"",
		},
		{
			"str-trim-all-whitespace",
			"function main(): i32 { var t = str_trim(\"   \\n\\t \"); return t.len(); }",
			0,
			"",
		},
		{
			"str-to-upper-basic",
			"function main(): i32 { var u = str_to_upper(\"hello\"); write(u); return u.len(); }",
			5,
			"HELLO",
		},
		{
			"str-to-upper-mixed",
			"function main(): i32 { var u = str_to_upper(\"Hi 123 World!\"); write(u); return 0; }",
			0,
			"HI 123 WORLD!",
		},
		{
			"str-to-lower-basic",
			"function main(): i32 { var l = str_to_lower(\"HELLO\"); write(l); return l.len(); }",
			5,
			"hello",
		},
		{
			"str-to-lower-mixed",
			"function main(): i32 { var l = str_to_lower(\"AbCdE 99\"); write(l); return 0; }",
			0,
			"abcde 99",
		},
		{
			"str-to-upper-empty",
			"function main(): i32 { return (str_to_upper(\"\")).len(); }",
			0,
			"",
		},
		{
			"str-case-round-trip",
			"function main(): i32 { var s = str_to_lower(str_to_upper(\"AbCd\")); write(s); return 0; }",
			0,
			"abcd",
		},
		{
			"str-repeat-basic",
			"function main(): i32 { var r = str_repeat(\"ab\", 3); write(r); return r.len(); }",
			6,
			"ababab",
		},
		{
			"str-repeat-once",
			"function main(): i32 { var r = str_repeat(\"hi\", 1); write(r); return r.len(); }",
			2,
			"hi",
		},
		{
			"str-repeat-zero",
			"function main(): i32 { var r = str_repeat(\"foo\", 0); return r.len(); }",
			0,
			"",
		},
		{
			"str-repeat-negative",
			"function main(): i32 { var r = str_repeat(\"foo\", 0 - 3); return r.len(); }",
			0,
			"",
		},
		{
			"str-repeat-empty-source",
			"function main(): i32 { var r = str_repeat(\"\", 5); return r.len(); }",
			0,
			"",
		},
		{
			"str-repeat-many",
			"function main(): i32 { var r = str_repeat(\"-=\", 4); write(r); return r.len(); }",
			8,
			"-=-=-=-=",
		},
		{
			"str-replace-basic",
			"function main(): i32 { var r = str_replace(\"hello world\", \"world\", \"there\"); write(r); return r.len(); }",
			11,
			"hello there",
		},
		{
			"str-replace-shorter",
			"function main(): i32 { var r = str_replace(\"abcabc\", \"abc\", \"x\"); write(r); return r.len(); }",
			2,
			"xx",
		},
		{
			"str-replace-longer",
			"function main(): i32 { var r = str_replace(\"a-b\", \"-\", \"---\"); write(r); return r.len(); }",
			5,
			"a---b",
		},
		{
			"str-replace-none",
			"function main(): i32 { var r = str_replace(\"hello\", \"xyz\", \"---\"); write(r); return r.len(); }",
			5,
			"hello",
		},
		{
			"str-replace-empty-old",
			"function main(): i32 { var r = str_replace(\"abc\", \"\", \"xyz\"); write(r); return r.len(); }",
			3,
			"abc",
		},
		{
			"str-replace-all-occurrences",
			"function main(): i32 { var r = str_replace(\"banana\", \"a\", \"!\"); write(r); return r.len(); }",
			6,
			"b!n!n!",
		},
		{
			"str-replace-empty-new",
			"function main(): i32 { var r = str_replace(\"banana\", \"a\", \"\"); write(r); return r.len(); }",
			3,
			"bnn",
		},
		{
			"chr-uppercase-a",
			"function main(): i32 { var c = chr(65); write(c); return c.len(); }",
			1,
			"A",
		},
		{
			"chr-newline",
			"function main(): i32 { var c = chr(10); write(\"before\"); write(c); write(\"after\"); return 0; }",
			0,
			"before\nafter",
		},
		{
			"chr-zero",
			"function main(): i32 { var c = chr(0); return c.len(); }",
			1,
			"",
		},
		{
			"chr-concat-build-string",
			"function main(): i32 { var msg = chr(72) + chr(105) + chr(33); write(msg); return msg.len(); }",
			3,
			"Hi!",
		},
		{
			"chr-index-of-result",
			"function main(): i32 { var c = chr(98); if (c == \"b\") { return 1; } return 0; }",
			1,
			"",
		},
		{
			"eprint-int-zero-stdout-clean",
			"function main(): i32 { eprint_int(0); return 0; }",
			0,
			"",
		},
		{
			"eprint-int-positive",
			"function main(): i32 { eprint_int(42); return 0; }",
			0,
			"",
		},
		{
			"eprint-int-and-print",
			"function main(): i32 { write(\"out=\"); print_int(1); write(\"\\n\"); eprint(\"err=\"); eprint_int(2); return 0; }",
			0,
			"out=1\n",
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
		{
			"args-count-at-least-one",
			"function main(): i32 { if (args_count() >= 1) { return 1; } return 0; }",
			1,
			"",
		},
		{
			"args-at-zero-non-empty",
			"function main(): i32 { var p = arg_at(0); if (p.len() > 0) { return 1; } return 0; }",
			1,
			"",
		},
		{
			"str-split-basic",
			"function main(): i32 { var a = str_split(\"a,b,c\", \",\"); return a.len(); }",
			3,
			"",
		},
		{
			"str-split-content",
			"function main(): i32 { var a = str_split(\"a,bb,ccc\", \",\"); for s in a { write(s); write(\"|\"); } return 0; }",
			0,
			"a|bb|ccc|",
		},
		{
			"str-split-no-sep",
			"function main(): i32 { var a = str_split(\"abc\", \",\"); return a.len(); }",
			1,
			"",
		},
		{
			// An empty separator CHAR-SPLITS, matching std/string.split and the
			// interp. This used to expect 1 (the whole string in one piece);
			// see the sibling case in self_host_asm_run_test.go for why that
			// divergence existed and why it no longer does.
			"str-split-empty-sep",
			"function main(): i32 { var a = str_split(\"hello\", \"\"); for s in a { write(s); write(\"|\"); } return a.len(); }",
			5,
			"h|e|l|l|o|",
		},
		{
			"str-split-leading-sep",
			"function main(): i32 { var a = str_split(\",a,b\", \",\"); for s in a { write(s); write(\"|\"); } return a.len(); }",
			3,
			"|a|b|",
		},
		{
			"str-split-trailing-sep",
			"function main(): i32 { var a = str_split(\"a,b,\", \",\"); for s in a { write(s); write(\"|\"); } return a.len(); }",
			3,
			"a|b||",
		},
		{
			"str-split-multi-char-sep",
			"function main(): i32 { var a = str_split(\"foo--bar--baz\", \"--\"); for s in a { write(s); write(\"|\"); } return a.len(); }",
			3,
			"foo|bar|baz|",
		},
		{
			"str-split-consecutive",
			"function main(): i32 { var a = str_split(\"a,,b\", \",\"); for s in a { write(s); write(\"|\"); } return a.len(); }",
			3,
			"a||b|",
		},
		// Match-arm guards (`Pat when <expr> =>`): true guard runs the arm; a
		// false guard falls through to the next arm (the guard reads the binding).
		{"match-guard-pass", "enum O { Has(i32), Nil } function main(): i32 { var o: O = Has(8); match (o) { Has(n) when n > 5 => { return 1; }, _ => { return 2; } } return 0 - 1; }", 1, ""},
		{"match-guard-fallthrough", "enum O { Has(i32), Nil } function main(): i32 { var o: O = Has(3); match (o) { Has(n) when n > 5 => { return 1; }, _ => { return 2; } } return 0 - 1; }", 2, ""},
		// Match expressions (value position): IIFE + statement-match desugar.
		{"match-expr", "enum O { Has(i32), Nil } function main(): i32 { var o: O = Has(5); var x: i32 = match (o) { Has(n) => n, Nil => 0 }; return x; }", 5, ""},
		{"match-expr-other-arm", "enum O { Has(i32), Nil } function main(): i32 { var o: O = Nil; return match (o) { Has(n) => n, Nil => 42 }; }", 42, ""},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Run the driver (x86_64 binary) with -target arm64 to get the arm64 asm.
			var cmd *exec.Cmd
			if len(x86runner) == 0 {
				cmd = exec.Command(driverBin, "-target", "arm64")
			} else {
				cmd = exec.Command(x86runner[0], append(append([]string{}, x86runner[1:]...), driverBin, "-target", "arm64")...)
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

	// Negative: the self-host type-check pass must REJECT a program that
	// uses an Option[i32] (`.max()`) where an i32 is declared, rather than
	// silently emitting a box pointer. The driver should exit non-zero and
	// print an E002 diagnostic; no asm is produced.
	t.Run("rejects-option-as-i32", func(t *testing.T) {
		var cmd *exec.Cmd
		if len(x86runner) == 0 {
			cmd = exec.Command(driverBin, "-target", "arm64")
		} else {
			cmd = exec.Command(x86runner[0], append(append([]string{}, x86runner[1:]...), driverBin, "-target", "arm64")...)
		}
		bad := "function main(): i32 { var xs: i32[] = [1, 2, 3]; return xs.max(); }"
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
}
