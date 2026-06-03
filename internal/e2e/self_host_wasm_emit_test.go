package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestSelfHostWasmRun exercises the self-hosted wasm emitter
// (examples/self_host/wasm.fern) end to end. wasm_run.fern reads Fern
// source from stdin, runs it through the self-host lexer + parser +
// wasm.emit_module, and prints a WASI core module in text format (WAT).
// For each case the test:
//
//  1. builds wasm_run.fern once with the Go x86-64 backend,
//  2. pipes the source in, capturing the emitted WAT,
//  3. runs it with `wasmtime run prog.wat`,
//  4. asserts the process exit code matches the program's result
//     (the emitter lowers `return <expr>;` to `proc_exit(<expr>)`).
//
// This is the wasm analogue of TestSelfHostAsmRunX86_64, and the first
// slice of the wasm backend on the path to retiring the Go wasm backend.
// Covered now: integer literals + unary `-` + binary `+ - * / %`.
func TestSelfHostWasmRun(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host wasm e2e")
	}
	gcc, runner := x86_64Tooling(t)

	dir := t.TempDir()
	for _, name := range []string{"lexer.fern", "parser.fern", "wasm.fern", "wasm_run.fern"} {
		src, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), src, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_run.fern", "wasm_run")

	cases := []struct {
		name   string
		source string
		exit   int
		stdout string // checked only when non-empty
	}{
		{"return-literal", "function main(): i32 { return 42; }", 42, ""},
		{"bare-return", "return 42;", 42, ""},
		{"arithmetic", "function main(): i32 { return 1 + 2 * 3; }", 7, ""},
		{"parens", "function main(): i32 { return (1 + 2) * 3; }", 9, ""},
		{"subtraction", "function main(): i32 { return 100 - 23; }", 77, ""},
		{"division", "function main(): i32 { return 84 / 2; }", 42, ""},
		{"modulo", "function main(): i32 { return 23 % 5; }", 3, ""},
		{"div-rem-combined", "function main(): i32 { return 84 / 2 + 23 % 5; }", 45, ""},
		// Non-trapping div/rem (would trap with naked i32.div_s/rem_s).
		{"div-by-zero", "function main(): i32 { return 5 / 0; }", 0, ""},
		{"mod-by-zero", "function main(): i32 { return 7 % 0; }", 7, ""},
		// INT_MIN / -1 == INT_MIN (no overflow trap); INT_MIN % -1 == 0.
		// INT_MIN is spelled `0 - 2147483647 - 1` to avoid a literal that
		// doesn't fit i32; the divisor `0 - 1` keeps it a runtime value.
		{"int-min-div-neg1", "function main(): i32 { var x = 0 - 2147483647 - 1; var d = 0 - 1; if (x / d == x) { return 42; } return 1; }", 42, ""},
		{"int-min-mod-neg1", "function main(): i32 { var x = 0 - 2147483647 - 1; var d = 0 - 1; if (x % d == 0) { return 42; } return 1; }", 42, ""},
		{"unary-neg", "function main(): i32 { return 0 - 5 + 10; }", 5, ""},
		{"nested", "function main(): i32 { return (2 + 3) * (4 + 4) - 1; }", 39, ""},
		{"no-return-exits-0", "function main(): i32 { }", 0, ""},
		// Locals + reassignment.
		{"locals", "function main(): i32 { var x = 5; var y = 10; return x + y; }", 15, ""},
		{"reassign", "function main(): i32 { var x = 5; x = x + 3; return x; }", 8, ""},
		{"compound-assign", "function main(): i32 { var x = 1; x *= 6; x += 1; return x; }", 7, ""},
		// Comparisons (0/1 results).
		{"comparison-true", "function main(): i32 { return 5 < 10; }", 1, ""},
		{"comparison-false", "function main(): i32 { return 10 < 5; }", 0, ""},
		{"equality-true", "function main(): i32 { return 7 == 7; }", 1, ""},
		// Logical + not.
		{"and", "function main(): i32 { if (true && true) { return 1; } return 0; }", 1, ""},
		{"or", "function main(): i32 { if (false || true) { return 1; } return 0; }", 1, ""},
		{"not", "function main(): i32 { if (!false) { return 1; } return 0; }", 1, ""},
		{"and-with-comparison", "function main(): i32 { var x = 5; if (x > 0 && x < 10) { return 1; } return 0; }", 1, ""},
		// if / else.
		{"if-then-branch", "function main(): i32 { var x = 5; if (x < 10) { return 1; } return 2; }", 1, ""},
		{"if-else-branch", "function main(): i32 { var x = 20; if (x < 10) { return 1; } return 2; }", 2, ""},
		{"if-else-explicit", "function main(): i32 { if (true) { return 9; } else { return 0; } }", 9, ""},
		// while.
		{"while-sum", "function main(): i32 { var i = 1; var s = 0; while (i <= 5) { s += i; i += 1; } return s; }", 15, ""},
		{"while-early-return", "function main(): i32 { var i = 0; while (i < 100) { if (i == 7) { return i; } i += 1; } return 99; }", 7, ""},
		// break / continue.
		{"break-in-while", "function main(): i32 { var i = 0; var s = 0; while (i < 100) { if (i == 5) { break; } s = s + i; i = i + 1; } return s; }", 10, ""},
		{"continue-in-while", "function main(): i32 { var i = 0; var s = 0; while (i < 10) { i = i + 1; if (i == 5) { continue; } s = s + i; } return s; }", 50, ""},
		// Mixed.
		{"mixed", "function main(): i32 { var a = 1 + 2; var b = 4 * 5; var c = a + b; if (c < 100) { return c; } return 0 - 1; }", 23, ""},
		// Function calls: free functions, args, locals, recursion,
		// mutual recursion.
		{"func-call", "function add(x: i32, y: i32): i32 { return x + y; } function main(): i32 { return add(2, 3); }", 5, ""},
		{"func-three-args", "function sum3(a: i32, b: i32, c: i32): i32 { return a + b + c; } function main(): i32 { return sum3(10, 20, 30); }", 60, ""},
		{"func-with-local-vars", "function compute(a: i32): i32 { var b = a * 2; var c = b + 1; return c; } function main(): i32 { return compute(5); }", 11, ""},
		{"recursive-factorial", "function fact(n: i32): i32 { if (n <= 1) { return 1; } return n * fact(n - 1); } function main(): i32 { return fact(5); }", 120, ""},
		{"recursive-fib", "function fib(n: i32): i32 { if (n < 2) { return n; } return fib(n - 1) + fib(n - 2); } function main(): i32 { return fib(8); }", 21, ""},
		{"mutual-recursion", "function is_even(n: i32): i32 { if (n == 0) { return 1; } return is_odd(n - 1); } function is_odd(n: i32): i32 { if (n == 0) { return 0; } return is_even(n - 1); } function main(): i32 { return is_even(6); }", 1, ""},
		{"call-in-condition", "function dbl(n: i32): i32 { return n * 2; } function main(): i32 { if (dbl(5) == 10) { return 42; } return 1; }", 42, ""},
		// Strings + linear memory: write (verbatim) and print (+\n) of
		// string literals, lowered to fd_write on the data section.
		{"write-verbatim", "function main(): i32 { write(\"hi\"); return 0; }", 0, "hi"},
		{"print-adds-newline", "function main(): i32 { print(\"hello\"); return 0; }", 0, "hello\n"},
		{"print-twice", "function main(): i32 { print(\"line 1\"); print(\"line 2\"); return 0; }", 0, "line 1\nline 2\n"},
		{"write-then-return", "function main(): i32 { write(\"out\"); return 42; }", 42, "out"},
		{"hello-world", "function main(): i32 { print(\"Hello, world!\"); return 0; }", 0, "Hello, world!\n"},
		{"write-with-embedded-newline", "function main(): i32 { write(\"a\\nb\"); return 0; }", 0, "a\nb"},
		{"print-in-function", "function greet(): i32 { print(\"hi from greet\"); return 7; } function main(): i32 { return greet(); }", 7, "hi from greet\n"},
		{"print-dedup-same-literal", "function main(): i32 { write(\"x\"); write(\"x\"); return 0; }", 0, "xx"},
		// print_int: integer → decimal formatted into memory.
		{"print-int-literal", "function main(): i32 { print_int(42); return 0; }", 0, "42"},
		{"print-int-zero", "function main(): i32 { print_int(0); return 0; }", 0, "0"},
		{"print-int-negative", "function main(): i32 { print_int(0 - 7); return 0; }", 0, "-7"},
		{"print-int-computed", "function main(): i32 { print_int(2 + 3 * 4); return 0; }", 0, "14"},
		{"print-int-multidigit", "function main(): i32 { print_int(1234567); return 0; }", 0, "1234567"},
		{"print-int-then-newline", "function main(): i32 { print_int(99); write(\"\\n\"); return 0; }", 0, "99\n"},
		{"print-int-int-min", "function main(): i32 { print_int(0 - 2147483647 - 1); return 0; }", 0, "-2147483648"},
		{"print-int-from-function", "function fact(n: i32): i32 { if (n <= 1) { return 1; } return n * fact(n - 1); } function main(): i32 { print_int(fact(8)); return 0; }", 0, "40320"},
		{"print-int-loop", "function main(): i32 { var i = 1; while (i <= 5) { print_int(i); write(\" \"); i = i + 1; } return 0; }", 0, "1 2 3 4 5 "},
		{"fizzbuzz-1-to-15", "function main(): i32 { var i = 1; while (i <= 15) { if (i % 15 == 0) { write(\"FizzBuzz\"); } else if (i % 3 == 0) { write(\"Fizz\"); } else if (i % 5 == 0) { write(\"Buzz\"); } else { print_int(i); } write(\"\\n\"); i = i + 1; } return 0; }", 0, "1\n2\nFizz\n4\nBuzz\nFizz\n7\n8\nFizz\nBuzz\n11\nFizz\n13\n14\nFizzBuzz\n"},
		// String values: literals flow through locals / params / returns
		// / reassignment as i32 pointers to [len][bytes] blocks.
		{"string-local", "function main(): i32 { var s = \"hello\"; write(s); return 0; }", 0, "hello"},
		{"string-local-print", "function main(): i32 { var s = \"hi\"; print(s); return 0; }", 0, "hi\n"},
		{"string-reassign", "function main(): i32 { var s = \"a\"; s = \"b\"; write(s); return 0; }", 0, "b"},
		{"string-two-locals", "function main(): i32 { var a = \"foo\"; var b = \"bar\"; write(a); write(b); return 0; }", 0, "foobar"},
		{"string-param", "function emit(s: string): i32 { write(s); return 0; } function main(): i32 { emit(\"param-str\"); return 0; }", 0, "param-str"},
		{"string-return", "function greet(): string { return \"howdy\"; } function main(): i32 { write(greet()); return 0; }", 0, "howdy"},
		{"string-return-local", "function pick(): string { var s = \"chosen\"; return s; } function main(): i32 { print(pick()); return 0; }", 0, "chosen\n"},
		{"string-through-call", "function id(s: string): string { return s; } function main(): i32 { write(id(\"echo\")); return 0; }", 0, "echo"},
		{"string-in-loop", "function main(): i32 { var s = \"x\"; var i = 0; while (i < 3) { write(s); i = i + 1; } return 0; }", 0, "xxx"},
		// String concatenation (+): bump-allocated [len][bytes] result.
		{"concat-literals", "function main(): i32 { write(\"foo\" + \"bar\"); return 0; }", 0, "foobar"},
		{"concat-var-literal", "function main(): i32 { var s = \"hello, \"; write(s + \"world\"); return 0; }", 0, "hello, world"},
		{"concat-two-vars", "function main(): i32 { var a = \"ab\"; var b = \"cd\"; write(a + b); return 0; }", 0, "abcd"},
		{"concat-chain", "function main(): i32 { write(\"a\" + \"b\" + \"c\" + \"d\"); return 0; }", 0, "abcd"},
		{"concat-to-local", "function main(): i32 { var s = \"x\" + \"y\"; write(s); return 0; }", 0, "xy"},
		{"concat-then-print", "function main(): i32 { var g = \"hi, \" + \"there\"; print(g); return 0; }", 0, "hi, there\n"},
		{"concat-param", "function greet(name: string): string { return \"hi \" + name; } function main(): i32 { write(greet(\"sam\")); return 0; }", 0, "hi sam"},
		{"concat-in-loop", "function main(): i32 { var s = \"\"; var i = 0; while (i < 3) { s = s + \"ab\"; i = i + 1; } write(s); return 0; }", 0, "ababab"},
		{"concat-empty", "function main(): i32 { var s = \"\"; write(s + \"end\"); return 0; }", 0, "end"},
		// int + still adds (not concat) — type-directed.
		{"int-plus-still-adds", "function main(): i32 { var a = 20; var b = 22; return a + b; }", 42, ""},
		// String equality / comparison + .len() (type-directed).
		{"str-eq-true", "function main(): i32 { var a = \"hi\"; if (a == \"hi\") { return 1; } return 0; }", 1, ""},
		{"str-eq-false", "function main(): i32 { var a = \"hi\"; if (a == \"ho\") { return 1; } return 0; }", 0, ""},
		{"str-eq-vars", "function main(): i32 { var a = \"foo\"; var b = \"f\" + \"oo\"; if (a == b) { return 42; } return 0; }", 42, ""},
		{"str-neq", "function main(): i32 { var a = \"x\"; if (a != \"y\") { return 1; } return 0; }", 1, ""},
		{"str-lt", "function main(): i32 { if (\"abc\" < \"abd\") { return 1; } return 0; }", 1, ""},
		{"str-lt-prefix", "function main(): i32 { if (\"ab\" < \"abc\") { return 1; } return 0; }", 1, ""},
		{"str-gt", "function main(): i32 { if (\"b\" > \"a\") { return 1; } return 0; }", 1, ""},
		{"str-ge-equal", "function main(): i32 { if (\"same\" >= \"same\") { return 1; } return 0; }", 1, ""},
		{"str-len", "function main(): i32 { var s = \"hello\"; return s.len(); }", 5, ""},
		{"str-len-empty", "function main(): i32 { var s = \"\"; return s.len(); }", 0, ""},
		{"str-len-literal", "function main(): i32 { return \"abcd\".len(); }", 4, ""},
		{"str-len-concat", "function main(): i32 { var s = \"ab\" + \"cde\"; return s.len(); }", 5, ""},
		{"str-len-param", "function l(s: string): i32 { return s.len(); } function main(): i32 { return l(\"seven!!\"); }", 7, ""},
		// String predicate methods (starts_with / ends_with / contains /
		// index_of) — read-only, return i32.
		{"str-starts-with-true", "function main(): i32 { var s = \"hello\"; if (s.starts_with(\"he\")) { return 1; } return 0; }", 1, ""},
		{"str-starts-with-false", "function main(): i32 { var s = \"hello\"; if (s.starts_with(\"lo\")) { return 1; } return 0; }", 0, ""},
		{"str-ends-with-true", "function main(): i32 { var s = \"hello\"; if (s.ends_with(\"lo\")) { return 1; } return 0; }", 1, ""},
		{"str-ends-with-false", "function main(): i32 { var s = \"hello\"; if (s.ends_with(\"he\")) { return 1; } return 0; }", 0, ""},
		{"str-contains-true", "function main(): i32 { var s = \"hello world\"; if (s.contains(\"o w\")) { return 1; } return 0; }", 1, ""},
		{"str-contains-false", "function main(): i32 { var s = \"hello\"; if (s.contains(\"xyz\")) { return 1; } return 0; }", 0, ""},
		{"str-index-of", "function main(): i32 { var s = \"hello\"; return s.index_of(\"ll\"); }", 2, ""},
		{"str-index-of-zero", "function main(): i32 { return \"abc\".index_of(\"a\"); }", 0, ""},
		{"str-index-of-missing", "function main(): i32 { var s = \"abc\"; var r = s.index_of(\"z\"); if (r < 0) { return 1; } return 0; }", 1, ""},
		{"str-contains-literal-recv", "function main(): i32 { if (\"foobar\".contains(\"oba\")) { return 42; } return 0; }", 42, ""},
		{"str-starts-with-after-concat", "function main(): i32 { var s = \"foo\" + \"bar\"; if (s.starts_with(\"foob\")) { return 1; } return 0; }", 1, ""},
		// Allocating string methods (return a fresh heap string).
		{"str-to-upper", "function main(): i32 { write(\"hello\".to_upper()); return 0; }", 0, "HELLO"},
		{"str-to-lower", "function main(): i32 { write(\"HeLLo\".to_lower()); return 0; }", 0, "hello"},
		{"str-to-upper-mixed", "function main(): i32 { var s = \"aB3z!\"; write(s.to_upper()); return 0; }", 0, "AB3Z!"},
		{"str-repeat", "function main(): i32 { write(\"ab\".repeat(3)); return 0; }", 0, "ababab"},
		{"str-repeat-zero", "function main(): i32 { var s = \"x\".repeat(0); return s.len(); }", 0, ""},
		{"str-repeat-var", "function main(): i32 { var s = \"-\"; write(s.repeat(5)); return 0; }", 0, "-----"},
		{"str-upper-concat", "function main(): i32 { write(\"hi \".to_upper() + \"there\"); return 0; }", 0, "HI there"},
		{"str-method-chain", "function main(): i32 { write(\"AbC\".to_lower().to_upper()); return 0; }", 0, "ABC"},
		{"str-upper-len", "function main(): i32 { return \"abc\".to_upper().len(); }", 3, ""},
		// i32 arrays (read side): literal, index, .len(), while-sum.
		{"arr-len", "function main(): i32 { var a = [10, 20, 30]; return a.len(); }", 3, ""},
		{"arr-index-first", "function main(): i32 { var a = [42, 99, 7]; return a[0]; }", 42, ""},
		{"arr-index-middle", "function main(): i32 { var a = [42, 99, 7]; return a[1]; }", 99, ""},
		{"arr-index-last", "function main(): i32 { var a = [42, 99, 7]; return a[2]; }", 7, ""},
		{"arr-index-via-var", "function main(): i32 { var a = [10, 20, 30, 40]; var i = 2; return a[i]; }", 30, ""},
		{"arr-sum-while", "function main(): i32 { var a = [1, 2, 3, 4, 5]; var i = 0; var s = 0; while (i < a.len()) { s = s + a[i]; i = i + 1; } return s; }", 15, ""},
		{"arr-of-exprs", "function main(): i32 { var x = 4; var a = [x, x + 1, x * 2]; return a[0] + a[1] + a[2]; }", 17, ""},
		{"arr-empty-len", "function main(): i32 { var a = [0]; var b = a; return b.len(); }", 1, ""},
		{"arr-index-computed", "function main(): i32 { var a = [5, 10, 15, 20]; return a[1 + 1]; }", 15, ""},
		{"arr-param", "function sum(a: i32[]): i32 { var i = 0; var s = 0; while (i < a.len()) { s = s + a[i]; i = i + 1; } return s; } function main(): i32 { return sum([10, 20, 12]); }", 42, ""},
		// Array mutation: for-in iteration, index-assignment, push.
		{"arr-for-sum", "function main(): i32 { var a = [10, 20, 30]; var s = 0; for x in a { s = s + x; } return s; }", 60, ""},
		{"arr-for-count", "function main(): i32 { var a = [1, 2, 3, 4, 5]; var n = 0; for x in a { n = n + 1; } return n; }", 5, ""},
		{"arr-for-print", "function main(): i32 { var a = [1, 2, 3]; for x in a { print_int(x); write(\" \"); } return 0; }", 0, "1 2 3 "},
		{"arr-for-break", "function main(): i32 { var a = [10, 20, 30, 40]; var s = 0; for x in a { if (x == 30) { break; } s = s + x; } return s; }", 30, ""},
		{"arr-for-continue", "function main(): i32 { var a = [1, 2, 3, 4, 5]; var s = 0; for x in a { if (x == 3) { continue; } s = s + x; } return s; }", 12, ""},
		{"arr-for-nested", "function main(): i32 { var a = [1, 2]; var b = [10, 20]; var s = 0; for x in a { for y in b { s = s + x * y; } } return s; }", 90, ""},
		{"arr-index-assign", "function main(): i32 { var a = [1, 2, 3]; a[1] = 99; return a[1]; }", 99, ""},
		{"arr-index-assign-sum", "function main(): i32 { var a = [0, 0, 0]; a[0] = 10; a[1] = 20; a[2] = 12; return a[0] + a[1] + a[2]; }", 42, ""},
		{"arr-push-len", "function main(): i32 { var a: i32[] = [1, 2, 3]; a = a.push(4); return a.len(); }", 4, ""},
		{"arr-push-last", "function main(): i32 { var a: i32[] = [10, 20]; a = a.push(99); return a[2]; }", 99, ""},
		{"arr-push-empty", "function main(): i32 { var a: i32[] = []; a = a.push(42); return a[0]; }", 42, ""},
		{"arr-push-chain", "function main(): i32 { var a: i32[] = []; a = a.push(1); a = a.push(2); a = a.push(3); return a[0] + a[1] + a[2]; }", 6, ""},
		{"arr-push-grow", "function main(): i32 { var a: i32[] = []; var i = 0; while (i < 10) { a = a.push(i); i = i + 1; } var s = 0; for x in a { s = s + x; } return s; }", 45, ""},
		// String arrays (string[]): literal, index, for-in, push, and
		// element used in string contexts (needs element typing).
		{"sarr-for-write", "function main(): i32 { var xs = [\"a\", \"b\", \"c\"]; for s in xs { write(s); } return 0; }", 0, "abc"},
		{"sarr-index-write", "function main(): i32 { var xs = [\"foo\", \"bar\"]; write(xs[1]); return 0; }", 0, "bar"},
		{"sarr-len", "function main(): i32 { var xs = [\"a\", \"b\", \"c\", \"d\"]; return xs.len(); }", 4, ""},
		{"sarr-elem-len", "function main(): i32 { var xs = [\"hello\", \"hi\"]; return xs[0].len(); }", 5, ""},
		{"sarr-elem-concat", "function main(): i32 { var xs = [\"foo\", \"bar\"]; write(xs[0] + xs[1]); return 0; }", 0, "foobar"},
		{"sarr-for-concat", "function main(): i32 { var xs = [\"a\", \"b\", \"c\"]; var acc = \"\"; for s in xs { acc = acc + s; } write(acc); return 0; }", 0, "abc"},
		{"sarr-for-eq", "function main(): i32 { var xs = [\"x\", \"y\", \"z\"]; var n = 0; for s in xs { if (s == \"y\") { n = n + 1; } } return n; }", 1, ""},
		{"sarr-push", "function main(): i32 { var xs: string[] = [\"a\"]; xs = xs.push(\"b\"); write(xs[1]); return xs.len(); }", 2, "b"},
		{"sarr-param", "function first(xs: string[]): string { return xs[0]; } function main(): i32 { write(first([\"hello\", \"world\"])); return 0; }", 0, "hello"},
		{"sarr-elem-method", "function main(): i32 { var xs = [\"abc\"]; write(xs[0].to_upper()); return 0; }", 0, "ABC"},
		{"sarr-for-var-method", "function main(): i32 { var xs = [\"ab\", \"cd\"]; for s in xs { write(s.to_upper()); } return 0; }", 0, "ABCD"},
		{"sarr-for-var-len", "function main(): i32 { var xs = [\"abc\", \"de\"]; var n = 0; for s in xs { n = n + s.len(); } return n; }", 5, ""},
		// join (string[] -> string) and split (string -> string[]).
		{"join-basic", "function main(): i32 { var xs = [\"a\", \"b\", \"c\"]; write(xs.join(\",\")); return 0; }", 0, "a,b,c"},
		{"join-empty-sep", "function main(): i32 { var xs = [\"a\", \"b\", \"c\"]; write(xs.join(\"\")); return 0; }", 0, "abc"},
		{"join-single", "function main(): i32 { var xs = [\"solo\"]; write(xs.join(\",\")); return 0; }", 0, "solo"},
		{"join-multichar-sep", "function main(): i32 { var xs = [\"x\", \"y\", \"z\"]; write(xs.join(\" - \")); return 0; }", 0, "x - y - z"},
		{"split-len", "function main(): i32 { var s = \"a,b,c\"; var parts = s.split(\",\"); return parts.len(); }", 3, ""},
		{"split-content", "function main(): i32 { var s = \"x|y|z\"; var parts = s.split(\"|\"); for p in parts { write(p); } return 0; }", 0, "xyz"},
		{"split-index", "function main(): i32 { var parts = \"foo.bar.baz\".split(\".\"); write(parts[1]); return 0; }", 0, "bar"},
		{"split-multichar", "function main(): i32 { var parts = \"aXXbXXc\".split(\"XX\"); return parts.len(); }", 3, ""},
		{"split-no-match", "function main(): i32 { var parts = \"abc\".split(\",\"); return parts.len(); }", 1, ""},
		{"split-then-join", "function main(): i32 { var parts = \"a,b,c\".split(\",\"); write(parts.join(\"-\")); return 0; }", 0, "a-b-c"},
		{"split-elem-method", "function main(): i32 { var parts = \"ab,cd\".split(\",\"); write(parts[0].to_upper()); return 0; }", 0, "AB"},
		// Structs: literal, field read, field assign, struct param/return.
		{"struct-field-read", "struct P { x: i32, y: i32 } function main(): i32 { var p = P { x: 40, y: 2 }; return p.x + p.y; }", 42, ""},
		{"struct-field-order", "struct P { x: i32, y: i32 } function main(): i32 { var p = P { y: 2, x: 40 }; return p.x; }", 40, ""},
		{"struct-field-assign", "struct P { x: i32, y: i32 } function main(): i32 { var p = P { x: 1, y: 2 }; p.x = 99; return p.x + p.y; }", 101, ""},
		{"struct-string-field", "struct Person { name: string, age: i32 } function main(): i32 { var p = Person { name: \"Sam\", age: 30 }; write(p.name); return p.age; }", 30, "Sam"},
		{"struct-string-field-concat", "struct Person { name: string, age: i32 } function main(): i32 { var p = Person { name: \"Sam\", age: 30 }; write(\"hi \" + p.name); return 0; }", 0, "hi Sam"},
		{"struct-string-field-method", "struct Box { s: string } function main(): i32 { var b = Box { s: \"abc\" }; write(b.s.to_upper()); return 0; }", 0, "ABC"},
		{"struct-param", "struct P { x: i32, y: i32 } function area(p: P): i32 { return p.x * p.y; } function main(): i32 { return area(P { x: 6, y: 7 }); }", 42, ""},
		{"struct-return", "struct P { x: i32, y: i32 } function mk(): P { return P { x: 20, y: 22 }; } function main(): i32 { var p = mk(); return p.x + p.y; }", 42, ""},
		{"struct-nested", "struct Inner { v: i32 } struct Outer { inner: Inner, k: i32 } function main(): i32 { var o = Outer { inner: Inner { v: 40 }, k: 2 }; return o.inner.v + o.k; }", 42, ""},
		{"struct-update", "struct P { x: i32, y: i32 } function main(): i32 { var p = P { x: 1, y: 2 }; var q = P { ...p, x: 40 }; return q.x + q.y; }", 42, ""},
		{"struct-field-in-loop", "struct Acc { total: i32 } function main(): i32 { var a = Acc { total: 0 }; var i = 1; while (i <= 5) { a.total = a.total + i; i = i + 1; } return a.total; }", 15, ""},
		// Option / Result via tag boxes + match.
		{"opt-some", "function find(): Option[i32] { return Some(42); } function main(): i32 { match (find()) { Some(v) => { return v; }, None => { return 0; } } return 1; }", 42, ""},
		{"opt-none", "function find(): Option[i32] { return None; } function main(): i32 { match (find()) { Some(v) => { return v; }, None => { return 7; } } return 1; }", 7, ""},
		{"opt-some-payload-add", "function mk(): Option[i32] { return Some(40); } function main(): i32 { match (mk()) { Some(v) => { return v + 2; }, None => { return 0; } } return 1; }", 42, ""},
		{"result-ok", "function run(): Result[i32, i32] { return Ok(40); } function main(): i32 { match (run()) { Ok(v) => { return v + 2; }, Err(e) => { return e; } } return 1; }", 42, ""},
		{"result-err", "function run(): Result[i32, i32] { return Err(13); } function main(): i32 { match (run()) { Ok(v) => { return v; }, Err(e) => { return e; } } return 1; }", 13, ""},
		{"opt-wildcard", "function mk(): Option[i32] { return None; } function main(): i32 { match (mk()) { Some(v) => { return v; }, _ => { return 99; } } return 1; }", 99, ""},
		{"opt-local", "function main(): i32 { var o: Option[i32] = Some(5); match (o) { Some(v) => { return v * 2; }, None => { return 0; } } return 1; }", 10, ""},
		{"opt-string-write", "function name(): Option[i32] { return Some(0); } function main(): i32 { match (name()) { Some(v) => { write(\"got\"); return 0; }, None => { write(\"none\"); return 0; } } return 1; }", 0, "got"},
		{"opt-in-if", "function lookup(k: i32): Option[i32] { if (k == 1) { return Some(100); } return None; } function main(): i32 { var sum = 0; match (lookup(1)) { Some(v) => { sum = sum + v; }, None => {} } match (lookup(2)) { Some(v) => { sum = sum + v; }, None => { sum = sum + 1; } } return sum; }", 101, ""},
		{"opt-nested-match", "function a(): Option[i32] { return Some(1); } function b(): Option[i32] { return Some(2); } function main(): i32 { match (a()) { Some(x) => { match (b()) { Some(y) => { return x + y + 39; }, None => { return 0; } } }, None => { return 0; } } return 1; }", 42, ""},
		// `?` try-operator: unwrap Some/Ok, else early-return None/Err.
		{"try-some", "function inner(): Option[i32] { return Some(41); } function f(): Option[i32] { var v = inner()?; return Some(v + 1); } function main(): i32 { match (f()) { Some(v) => { return v; }, None => { return 0; } } return 1; }", 42, ""},
		{"try-none-propagates", "function inner(): Option[i32] { return None; } function f(): Option[i32] { var v = inner()?; return Some(v + 100); } function main(): i32 { match (f()) { Some(v) => { return v; }, None => { return 7; } } return 1; }", 7, ""},
		{"try-ok", "function inner(): Result[i32, i32] { return Ok(40); } function f(): Result[i32, i32] { var v = inner()?; return Ok(v + 2); } function main(): i32 { match (f()) { Ok(v) => { return v; }, Err(e) => { return e; } } return 1; }", 42, ""},
		{"try-err-propagates", "function inner(): Result[i32, i32] { return Err(13); } function f(): Result[i32, i32] { var v = inner()?; return Ok(v + 1); } function main(): i32 { match (f()) { Ok(v) => { return v; }, Err(e) => { return e; } } return 1; }", 13, ""},
		{"try-chain", "function a(): Option[i32] { return Some(10); } function b(): Option[i32] { return Some(20); } function f(): Option[i32] { var x = a()?; var y = b()?; return Some(x + y + 12); } function main(): i32 { match (f()) { Some(v) => { return v; }, None => { return 0; } } return 1; }", 42, ""},
		{"try-inline", "function inner(): Option[i32] { return Some(20); } function f(): Option[i32] { return Some(inner()? + inner()? + 2); } function main(): i32 { match (f()) { Some(v) => { return v; }, None => { return 0; } } return 1; }", 42, ""},
		// PROBE: Option[string] payload operations.
		{"payload-len", "function f(): Option[string] { return Some(\"hello\"); } function main(): i32 { match (f()) { Some(s) => { return s.len(); }, None => { return 0; } } return 1; }", 5, ""},
		{"payload-write", "function f(): Option[string] { return Some(\"hi\"); } function main(): i32 { match (f()) { Some(s) => { write(s); return 0; }, None => { return 0; } } return 1; }", 0, "hi"},
		{"payload-method", "function f(): Option[string] { return Some(\"abc\"); } function main(): i32 { match (f()) { Some(s) => { write(s.to_upper()); return 0; }, None => { return 0; } } return 1; }", 0, "ABC"},
		{"payload-concat", "function f(): Option[string] { return Some(\"foo\"); } function g(): Option[string] { return Some(\"bar\"); } function main(): i32 { match (f()) { Some(a) => { match (g()) { Some(b) => { write(a + b); return 0; }, None => { return 0; } } }, None => { return 0; } } return 1; }", 0, "foobar"},
		{"payload-local-scrut", "function f(): Option[string] { return Some(\"abc\"); } function main(): i32 { var o: Option[string] = f(); match (o) { Some(s) => { write(s.to_upper()); return 0; }, None => { return 0; } } return 1; }", 0, "ABC"},
		{"payload-struct", "struct P { x: i32, y: i32 } function f(): Option[P] { return Some(P { x: 40, y: 2 }); } function main(): i32 { match (f()) { Some(p) => { return p.x + p.y; }, None => { return 0; } } return 1; }", 42, ""},
		{"payload-try-string", "function f(): Option[string] { return Some(\"hello\"); } function g(): Option[i32] { var s = f()?; return Some(s.len()); } function main(): i32 { match (g()) { Some(n) => { return n; }, None => { return 0; } } return 1; }", 5, ""},
		// Struct-union match (`type E = A | B`): dispatch on the struct's
		// type id @0; the variant binding is the value itself.
		{"union-first", "struct Circle { r: i32 } struct Square { s: i32 } type Shape = Circle | Square; function main(): i32 { var x: Shape = Circle { r: 5 }; match (x) { Circle(c) => { return c.r; }, Square(q) => { return q.s; } } return 0; }", 5, ""},
		{"union-second", "struct Circle { r: i32 } struct Square { s: i32 } type Shape = Circle | Square; function main(): i32 { var x: Shape = Square { s: 7 }; match (x) { Circle(c) => { return c.r; }, Square(q) => { return q.s; } } return 0; }", 7, ""},
		{"union-field-math", "struct Circle { r: i32 } struct Rect { w: i32, h: i32 } type Shape = Circle | Rect; function area(x: Shape): i32 { match (x) { Circle(c) => { return c.r * c.r; }, Rect(r) => { return r.w * r.h; } } return 0; } function main(): i32 { return area(Rect { w: 6, h: 7 }); }", 42, ""},
		{"union-string-field", "struct Named { name: string } struct Anon { id: i32 } type Entity = Named | Anon; function label(e: Entity): i32 { match (e) { Named(n) => { write(n.name); return 0; }, Anon(a) => { return a.id; } } return 0; } function main(): i32 { return label(Named { name: \"hi\" }); }", 0, "hi"},
		{"union-param-dispatch", "struct A { v: i32 } struct B { v: i32 } type AB = A | B; function pick(x: AB): i32 { match (x) { A(a) => { return a.v + 1; }, B(b) => { return b.v + 2; } } return 0; } function main(): i32 { return pick(A { v: 40 }) + pick(B { v: 0 }) - 1; }", 42, ""},
		// Receiver methods: static dispatch to $RecvType__name with the
		// receiver passed first.
		{"method-noarg", "struct Circle { r: i32 } function (c: Circle) area(): i32 { return c.r * c.r; } function main(): i32 { var k = Circle { r: 5 }; return k.area(); }", 25, ""},
		{"method-with-arg", "struct Box { v: i32 } function (b: Box) scale(n: i32): i32 { return b.v * n; } function main(): i32 { var x = Box { v: 4 }; return x.scale(3); }", 12, ""},
		{"method-two-structs", "struct Circle { r: i32 } struct Square { s: i32 } function (c: Circle) area(): i32 { return c.r * c.r; } function (q: Square) area(): i32 { return q.s * q.s; } function main(): i32 { var a = Circle { r: 3 }; var b = Square { s: 6 }; return a.area() + b.area(); }", 45, ""},
		{"method-string-return", "struct Person { name: string } function (p: Person) greeting(): string { return \"hi \" + p.name; } function main(): i32 { var p = Person { name: \"sam\" }; write(p.greeting()); return 0; }", 0, "hi sam"},
		{"method-string-result-method", "struct Box { s: string } function (b: Box) val(): string { return b.s; } function main(): i32 { var x = Box { s: \"abc\" }; write(x.val().to_upper()); return 0; }", 0, "ABC"},
		{"method-vs-free", "struct Circle { r: i32 } function (c: Circle) area(): i32 { return c.r * c.r; } function area(): i32 { return 100; } function main(): i32 { var k = Circle { r: 5 }; return area() + k.area(); }", 125, ""},
		{"method-chained-struct", "struct Counter { n: i32 } function (c: Counter) inc(): Counter { return Counter { n: c.n + 1 }; } function main(): i32 { var c = Counter { n: 40 }; return c.inc().inc().n; }", 42, ""},
		{"method-option-return", "struct Reg { v: i32 } function (r: Reg) get(): Option[i32] { if (r.v > 0) { return Some(r.v); } return None; } function main(): i32 { var r = Reg { v: 42 }; match (r.get()) { Some(v) => { return v; }, None => { return 0; } } return 1; }", 42, ""},
		// Generics by erasure: the parser discards the type-parameter
		// lists, so the backend compiles one body per generic decl.
		{"generic-id-i32", "function id[T](x: T): T { return x; } function main(): i32 { return id(42); }", 42, ""},
		{"generic-id-mixed", "function id[T](x: T): T { return x; } function main(): i32 { var n: i32 = id(40); var s: string = id(\"hi\"); return n + s.len(); }", 42, ""},
		{"generic-two-params", "function fst[A, B](a: A, b: B): A { return a; } function main(): i32 { return fst(42, 99); }", 42, ""},
		{"generic-struct", "struct Box[T] { val: T } function unbox[T](b: Box[T]): T { return b.val; } function main(): i32 { var b: Box[i32] = Box { val: 42 }; return unbox(b); }", 42, ""},
		{"generic-pair-struct", "struct Pair[A, B] { fst: A, snd: B } function main(): i32 { var p = Pair { fst: 40, snd: 2 }; return p.fst + p.snd; }", 42, ""},
		{"generic-fn-string", "function id[T](x: T): T { return x; } function main(): i32 { write(id(\"hello\")); return 0; }", 0, "hello"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			wat := runCapture(t, gcc, runner, driverBin, []byte(tc.source))
			if len(wat) == 0 {
				t.Fatal("wasm emitter produced 0 bytes")
			}
			watPath := filepath.Join(dir, tc.name+".wat")
			if err := os.WriteFile(watPath, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			cmd := exec.Command("wasmtime", "run", watPath)
			out, _ := cmd.Output() // captures the program's stdout
			if code := cmd.ProcessState.ExitCode(); code != tc.exit {
				t.Errorf("%s: wasm exited %d, want %d\n--- WAT ---\n%s", tc.name, code, tc.exit, wat)
			}
			if tc.stdout != "" && string(out) != tc.stdout {
				t.Errorf("%s: wasm stdout = %q, want %q\n--- WAT ---\n%s", tc.name, string(out), tc.stdout, wat)
			}
		})
	}
}
