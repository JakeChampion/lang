package e2eselfhost

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
	for _, name := range []string{"lexer.fern", "parser.fern", "util.fern", "astwalk.fern", "asmcore.fern", "ir.fern", "irlower.fern", "asm_ir.fern", "wasm_ir.fern", "wasm.fern", "wasm_run.fern"} {
		src, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), src, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_run.fern", "wasm_run")

	// A fixed file in the preopened dir for read_file() cases.
	if err := os.WriteFile(filepath.Join(dir, "rf_test.txt"), []byte("file-contents-123"), 0o644); err != nil {
		t.Fatalf("write rf_test.txt: %v", err)
	}

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
		// `/` or `%` nested inside a compound literal (tuple / array /
		// struct-lit) or an index/slice position must still trigger
		// emission of the $__fern_idiv / $__fern_irem helpers — the
		// "uses divrem?" scan has to recurse into those nodes (regression:
		// harden11; it previously only looked through binary/unary/call).
		{"div-in-tuple", "function divmod(a: i32, b: i32): (i32, i32) { return (a / b, a % b); } function main(): i32 { var (q, r) = divmod(17, 5); return q + r; }", 5, ""},
		{"div-in-array", "function main(): i32 { var xs: i32[] = [100 / 4, 100 % 7]; return xs[0] + xs[1]; }", 27, ""},
		{"div-in-struct-lit", "struct R { v: i32 } function main(): i32 { var r = R { v: 84 / 2 }; return r.v; }", 42, ""},
		{"div-in-index", "function main(): i32 { var xs: i32[] = [5, 10, 15, 20]; return xs[6 / 2]; }", 20, ""},
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
		// if-expressions — desugar to an immediately-invoked closure
		// (call_indirect through the function table on wasm).
		{"if-expr-true", "function main(): i32 { var x: i32 = if (true) { 3 } else { 4 }; return x; }", 3, ""},
		{"if-expr-capture", "function main(): i32 { var n: i32 = 10; var x: i32 = if (n > 5) { n + 1 } else { 0 }; return x; }", 11, ""},
		{"if-expr-else-if", "function main(): i32 { var n: i32 = 2; var x: i32 = if (n == 1) { 10 } else if (n == 2) { 20 } else { 30 }; return x; }", 20, ""},
		// Two SEPARATE lambdas in one module — exercises the shared lam_ctr /
		// lamdefs Cells incrementing (0→1→2) and accumulating both bodies
		// (the array-as-cell idiom migrated to Cell). 1 + 4 = 5.
		// Array.build desugar through the self-host parser (parser.fern):
		// b.append in a loop builds [1,2,3,4]; sum 10.
		{"array-build", "function main(): i32 { var out: i32[] = Array.build(function(b: ArrayBuilder[i32]): void { var i = 0; while (i < 4) { b.append(i + 1); i = i + 1; } }); return out[0] + out[1] + out[2] + out[3]; }", 10, ""},
		{"two-lambdas", "function main(): i32 { var a: i32 = if (true) { 1 } else { 2 }; var b: i32 = if (false) { 3 } else { 4 }; return a + b; }", 5, ""},
		{"direct-iife", "function main(): i32 { return (function(): i32 { return 7; })(); }", 7, ""},
		// Local (nested) functions — desugar to a closure-valued local.
		{"local-fn-basic", "function main(): i32 { function helper(): i32 { return 5; } return helper(); }", 5, ""},
		{"local-fn-capture", "function main(): i32 { var n: i32 = 10; function bump(): i32 { return n + 1; } return bump(); }", 11, ""},
		// defer — action runs at function exit (LIFO, conditional, value
		// captured before cleanup).
		{"defer-fires", "function inc(a: i32[]): i32 { defer a[0] = 9; return 1; } function main(): i32 { var arr = [0]; inc(arr); return arr[0]; }", 9, ""},
		{"defer-lifo", "function f(a: i32[]): i32 { defer a[0] = 1; defer a[0] = 2; return 0; } function main(): i32 { var arr = [0]; f(arr); return arr[0]; }", 1, ""},
		{"defer-conditional-off", "function f(a: i32[], c: i32): i32 { if (c == 1) { defer a[0] = 7; } return 0; } function main(): i32 { var arr = [0]; f(arr, 0); return arr[0]; }", 0, ""},
		{"defer-loop-survives", "function f(a: i32[]): i32 { defer a[0] = a[0] + 50; var i = 0; while (i < 3) { a[0] = a[0] + 1; i = i + 1; } return 0; } function main(): i32 { var arr = [0]; f(arr); return arr[0]; }", 53, ""},
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
		// Scalar-field structs via the IR path (struct_make / struct_get, leak-
		// only): construction + field read, field-order independence, params,
		// boolean fields. wasm box is [type_id@0, f0@4, …] rc-headered; exit codes
		// must match. (Update / mutation fall back to AST.)
		{"ir-struct-lit-fields", "struct P { x: i32, y: i32 } function main(): i32 { var p = P { x: 3, y: 4 }; return p.x + p.y; }", 7, ""},
		{"ir-struct-field-order", "struct P { x: i32, y: i32 } function main(): i32 { var p = P { y: 40, x: 2 }; return p.x + p.y; }", 42, ""},
		{"ir-struct-three-fields", "struct V { a: i32, b: i32, c: i32 } function main(): i32 { var v = V { a: 1, b: 2, c: 3 }; return v.a * 100 + v.b * 10 + v.c; }", 123, ""},
		{"ir-struct-param", "struct P { x: i32, y: i32 } function sum(p: P): i32 { return p.x + p.y; } function main(): i32 { var p = P { x: 30, y: 12 }; return sum(p); }", 42, ""},
		{"ir-struct-bool-field", "struct F { on: boolean, n: i32 } function main(): i32 { var f = F { on: true, n: 7 }; if (f.on) { return f.n; } return 0; }", 7, ""},
		{"ir-struct-two-instances", "struct P { x: i32, y: i32 } function main(): i32 { var a = P { x: 1, y: 2 }; var b = P { x: 10, y: 20 }; return a.x + b.y; }", 21, ""},
		{"ir-struct-in-loop", "struct P { x: i32, y: i32 } function main(): i32 { var s = 0; var i = 0; while (i < 4) { var p = P { x: i, y: i * 2 }; s = s + p.x + p.y; i = i + 1; } return s; }", 18, ""},
		// Functional struct update `P { ...base, f: v }`.
		{"ir-struct-update-one", "struct P { x: i32, y: i32 } function main(): i32 { var p = P { x: 1, y: 2 }; var q = P { ...p, y: 9 }; return q.x + q.y; }", 10, ""},
		{"ir-struct-update-keeps-base", "struct P { x: i32, y: i32 } function main(): i32 { var p = P { x: 5, y: 6 }; var q = P { ...p, x: 50 }; return p.x + q.x; }", 55, ""},
		// Field mutation `p.x = v`.
		{"ir-field-mutate", "struct P { x: i32, y: i32 } function main(): i32 { var p = P { x: 1, y: 2 }; p.x = 40; return p.x + p.y; }", 42, ""},
		{"ir-field-mutate-loop", "struct C { n: i32 } function main(): i32 { var c = C { n: 0 }; var i = 0; while (i < 5) { c.n = c.n + i; i = i + 1; } return c.n; }", 10, ""},
		{"ir-field-mutate-alias", "struct P { x: i32 } function main(): i32 { var p = P { x: 1 }; var q = p; q.x = 9; return p.x; }", 9, ""},
		{"ir-str-return", "function greet(): string { return \"hi\"; } function main(): i32 { var s = greet(); return s.len(); }", 2, ""},
		{"ir-str-index-local", "function main(): i32 { var s = \"hello\"; return s[0] as i32; }", 104, ""},
		{"ir-str-index-loop", "function main(): i32 { var s = \"abc\"; var sum = 0; var i = 0; while (i < 3) { sum = sum + (s[i] as i32); i = i + 1; } return sum % 200; }", 94, ""},
		{"ir-str-index-param", "function first(s: string): i32 { return s[0] as i32; } function main(): i32 { return first(\"Z\"); }", 90, ""},
		{"ir-str-slice-len", "function main(): i32 { var s = \"hello\"; var t = s[1:4]; return t.len(); }", 3, ""},
		{"ir-str-slice-idx0", "function main(): i32 { var s = \"hello\"; var t = s[1:4]; return t[0] as i32; }", 101, ""},
		{"ir-str-slice-param", "function tok(s: string): i32 { return s[0:2].len(); } function main(): i32 { return tok(\"abcd\"); }", 2, ""},
		{"ir-str-return-concat", "function shout(s: string): string { return s + \"!\"; } function main(): i32 { var g = shout(\"hey\"); return g.len(); }", 4, ""},
		{"ir-struct-str-field", "struct Token { text: string, kind: i32 } function main(): i32 { var t = Token { text: \"hello\", kind: 7 }; return t.text.len() + t.kind; }", 12, ""},
		{"ir-enum-str-payload", "enum T { Word(string), Eof } function g(t: T): i32 { match (t) { Word(w) => { return w.len(); }, Eof => { return 3; } } return 0; } function main(): i32 { return g(Word(\"hello\")) + g(Eof); }", 8, ""},
		{"ir-match-guard-fallthrough", "enum E { Pos(i32), Neg(i32), Zero } function f(e: E): i32 { match (e) { Pos(n) when n > 10 => { return 1; }, Pos(n) => { return 2; }, _ => { return 3; } } return 0; } function main(): i32 { return f(Pos(20)) * 100 + f(Pos(5)) * 10 + f(Zero); }", 123, ""},
		{"ir-match-guard-wildcard", "enum E { V(i32) } function f(e: E): i32 { match (e) { _ when false => { return 5; }, V(n) => { return n; } } return 0; } function main(): i32 { return f(V(42)); }", 42, ""},
		{"ir-opt-some-none", "function classify(n: i32): Option[i32] { if (n > 0) { return Some(n); } return None; } function f(n: i32): i32 { match (classify(n)) { Some(_) => { return 1; }, None => { return 0; } } return 9; } function main(): i32 { return f(5) * 10 + f(0); }", 10, ""},
		{"ir-opt-ok-err", "function chk(n: i32): Result[i32, i32] { if (n > 0) { return Ok(n); } return Err(n); } function f(n: i32): i32 { match (chk(n)) { Ok(_) => { return 7; }, Err(_) => { return 3; } } return 9; } function main(): i32 { return f(2) * 10 + f(0); }", 73, ""},
		{"ir-opt-bind-some", "function g(n: i32): Option[i32] { if (n > 0) { return Some(n + 100); } return None; } function f(n: i32): i32 { match (g(n)) { Some(x) => { return x; }, None => { return 0; } } return 0; } function main(): i32 { return f(5); }", 105, ""},
		{"ir-opt-bind-string", "function name(n: i32): Option[string] { if (n > 0) { return Some(\"hello\"); } return None; } function f(n: i32): i32 { match (name(n)) { Some(s) => { return s.len(); }, None => { return 0; } } return 0; } function main(): i32 { return f(1); }", 5, ""},
		{"ir-opt-bind-result-strerr", "function chk(n: i32): Result[i32, string] { if (n > 0) { return Ok(n); } return Err(\"fail\"); } function f(n: i32): i32 { match (chk(n)) { Ok(x) => { return x; }, Err(e) => { return e.len(); } } return 0; } function main(): i32 { return f(7) * 10 + f(0); }", 74, ""},
		{"ir-opt-bind-local", "function g(n: i32): Option[i32] { if (n > 0) { return Some(n + 100); } return None; } function f(n: i32): i32 { var r = g(n); match (r) { Some(x) => { return x; }, None => { return 0; } } return 0; } function main(): i32 { return f(5); }", 105, ""},
		{"ir-opt-bind-param", "function f(o: Option[i32]): i32 { match (o) { Some(x) => { return x * 2; }, None => { return 0; } } return 0; } function main(): i32 { return f(Some(21)) + f(None); }", 42, ""},
		{"ir-struct-field-nested", "struct Point { x: i32, y: i32 } struct Box { p: Point } function bx(b: Box): i32 { return b.p.x + b.p.y; } function main(): i32 { var b = Box { p: Point { x: 30, y: 12 } }; return bx(b); }", 42, ""},
		{"ir-struct-field-deep", "struct Inner { v: i32 } struct Mid { inner: Inner, n: i32 } struct Outer { mid: Mid } function f(o: Outer): i32 { return o.mid.inner.v + o.mid.n; } function main(): i32 { var o = Outer { mid: Mid { inner: Inner { v: 100 }, n: 5 } }; return f(o); }", 105, ""},
		{"ir-forin-i32", "function main(): i32 { var xs = [10, 20, 30, 40]; var sum = 0; for x in xs { sum = sum + x; } return sum; }", 100, ""},
		{"ir-forin-string", "function main(): i32 { var ss: string[] = [\"a\", \"bb\", \"ccc\", \"dddd\"]; var n = 0; for s in ss { n = n + s.len(); } return n; }", 10, ""},
		{"ir-enum-struct-payload", "struct BinExpr { left: i32, right: i32 } enum Expr { Lit(i32), Binary(BinExpr) } function eval(e: Expr): i32 { match (e) { Lit(n) => { return n; }, Binary(b) => { return b.left + b.right; } } return 0; } function main(): i32 { return eval(Lit(7)) + eval(Binary(BinExpr { left: 3, right: 9 })); }", 19, ""},
		{"ir-enum-struct-payload-guard", "struct P { x: i32, y: i32 } enum Shape { Rect(P), Dot } function area(s: Shape): i32 { match (s) { Rect(p) when p.x > 0 => { return p.x * p.y; }, _ => { return 0; } } return 0; } function main(): i32 { return area(Rect(P { x: 4, y: 5 })); }", 20, ""},
		{"ir-enum-arr-payload-len", "enum E { Items(i32[]), Empty } function f(e: E): i32 { match (e) { Items(xs) => { return xs.len(); }, Empty => { return 0; } } return 0; } function main(): i32 { return f(Items([10, 20, 30])) * 10 + f(Empty); }", 30, ""},
		{"ir-enum-arr-payload-forin", "enum E { Items(i32[]), Empty } function sum(e: E): i32 { match (e) { Items(xs) => { var t = 0; for x in xs { t = t + x; } return t; }, Empty => { return 0; } } return 0; } function main(): i32 { return sum(Items([5, 10, 15])); }", 30, ""},
		{"ir-enum-strarr-payload-len", "enum E { Words(string[]), None } function f(e: E): i32 { match (e) { Words(w) => { return w.len(); }, None => { return 0; } } return 0; } function main(): i32 { return f(Words([\"a\", \"bb\", \"ccc\"])) * 10 + f(None); }", 30, ""},
		{"ir-struct-strarr-field-index", "struct Doc { lines: string[] } function f(d: Doc): i32 { return d.lines[1].len(); } function main(): i32 { var d = Doc { lines: [\"a\", \"bb\", \"ccc\"] }; return f(d); }", 2, ""},
		{"ir-tuple-str-i32-dotn", "function main(): i32 { var t = (\"hello\", 7); return t.0.len() + t.1; }", 12, ""},
		{"ir-tuple-struct-dotn", "struct P { x: i32, y: i32 } function main(): i32 { var t = (P { x: 4, y: 5 }, 2); return t.0.x * t.0.y + t.1; }", 22, ""},
		{"ir-map-i32-len3", "function main(): i32 { var m: Map[i32, i32] = map_new(4); m = m.insert(1, 100); m = m.insert(2, 200); m = m.insert(3, 300); return m.len(); }", 3, ""},
		{"ir-map-str-keys", "function main(): i32 { var m: Map[string, i32] = map_new(4); m = m.insert(\"a\", 1); m = m.insert(\"bb\", 2); m = m.insert(\"a\", 9); return m.len(); }", 2, ""},
		{"ir-map-get-hit", "function main(): i32 { var m: Map[i32, i32] = map_new(8); m = m.insert(7, 42); match (m.get(7)) { Some(v) => { return v; }, None => { return 0; } } return 9; }", 42, ""},
		{"ir-map-get-miss", "function main(): i32 { var m: Map[i32, i32] = map_new(8); m = m.insert(7, 42); match (m.get(999)) { Some(v) => { return v; }, None => { return 5; } } return 9; }", 5, ""},
		{"ir-map-has", "function main(): i32 { var m: Map[i32, i32] = map_new(4); m = m.insert(1, 1); var r = 0; if (m.has(1)) { r = r + 1; } if (m.has(2)) { r = r + 10; } return r; }", 1, ""},
		{"ir-map-get-or-hit", "function main(): i32 { var m: Map[i32, i32] = map_new(8); m = m.insert(7, 42); return m.get_or(7, 0); }", 42, ""},
		{"ir-map-get-or-miss", "function main(): i32 { var m: Map[i32, i32] = map_new(8); m = m.insert(7, 42); return m.get_or(999, 5); }", 5, ""},
		{"ir-map-get-or-strhit", "function main(): i32 { var m: Map[string, i32] = map_new(4); m = m.insert(\"hi\", 11); return m.get_or(\"hi\", 0); }", 11, ""},
		{"ir-map-get-or-strmiss", "function main(): i32 { var m: Map[string, i32] = map_new(4); m = m.insert(\"hi\", 11); return m.get_or(\"no\", 7); }", 7, ""},
		{"ir-map-keys-sum", "function main(): i32 { var m: Map[i32, i32] = map_new(8); m = m.insert(1, 10); m = m.insert(2, 20); m = m.insert(3, 30); var ks: i32[] = m.keys(); var s = 0; var i = 0; while (i < ks.len()) { s = s + ks[i]; i = i + 1; } return s; }", 6, ""},
		{"ir-map-values-sum", "function main(): i32 { var m: Map[i32, i32] = map_new(8); m = m.insert(1, 10); m = m.insert(2, 20); m = m.insert(3, 30); var vs: i32[] = m.values(); var s = 0; var i = 0; while (i < vs.len()) { s = s + vs[i]; i = i + 1; } return s; }", 60, ""},
		{"ir-map-forkv-values", "function main(): i32 { var m: Map[i32, i32] = map_new(8); m = m.insert(1, 10); m = m.insert(2, 20); m = m.insert(3, 30); var s = 0; for (k, v) in m { s = s + v; } return s; }", 60, ""},
		{"ir-map-forkv-keys", "function main(): i32 { var m: Map[i32, i32] = map_new(8); m = m.insert(1, 10); m = m.insert(2, 20); m = m.insert(3, 30); var s = 0; for (k, v) in m { s = s + k; } return s; }", 6, ""},
		{"ir-map-forkv-pair", "function main(): i32 { var m: Map[i32, i32] = map_new(8); m = m.insert(1, 2); m = m.insert(2, 3); m = m.insert(3, 4); var s = 0; for (k, v) in m { s = s + k * v; } return s; }", 20, ""},
		{"ir-map-forkv-strkey", "function main(): i32 { var m: Map[string, i32] = map_new(8); m = m.insert(\"ab\", 1); m = m.insert(\"cde\", 2); var s = 0; for (k, v) in m { s = s + k.len() + v; } return s; }", 8, ""},
		{"ir-i64-cmp", "function main(): i32 { var x: i64 = 5000000000; var y: i64 = 4000000000; if (x > y) { return 7; } return 0; }", 7, ""},
		{"ir-i64-add", "function main(): i32 { var a: i64 = 3000000000; var b: i64 = 3000000000; var c: i64 = a + b; if (c > 5000000000) { return 11; } return 0; }", 11, ""},
		{"ir-i64-mul", "function main(): i32 { var a: i64 = 100000; var b: i64 = 100000; var c: i64 = a * b; if (c > 4000000000) { return 5; } return 0; }", 5, ""},
		{"ir-i64-sub-neg", "function main(): i32 { var a: i64 = 1000000000; var b: i64 = 2000000000; var c: i64 = a - b; if (c < 0) { return 9; } return 0; }", 9, ""},
		{"ir-i64-loop", "function main(): i32 { var s: i64 = 0; var i: i32 = 0; while (i < 100000) { s = s + 100000; i = i + 1; } if (s > 4000000000) { return 13; } return 0; }", 13, ""},
		{"ir-and-true", "function main(): i32 { var x = 5; if (x > 0 && x < 10) { return 7; } return 0; }", 7, ""},
		{"ir-and-false", "function main(): i32 { var x = 15; if (x > 0 && x < 10) { return 7; } return 0; }", 0, ""},
		{"ir-or-true", "function main(): i32 { var x = 15; if (x < 0 || x > 0) { return 3; } return 0; }", 3, ""},
		{"ir-and-or-nest", "function main(): i32 { var a = 1; var b = 0; var c = 5; if (a > 0 && b > 0 || c > 0) { return 9; } return 0; }", 9, ""},
		{"ir-and-not-operand", "function main(): i32 { var x = 5; if (!(x > 10) && x > 0) { return 4; } return 0; }", 4, ""},
		{"ir-and-bool-vars", "function main(): i32 { var f = 5 > 3; var g = 2 > 8; if (f && !g) { return 6; } return 0; }", 6, ""},
		{"ir-strcmp-lt", "function main(): i32 { var a = \"apple\"; var b = \"banana\"; if (a < b) { return 7; } return 0; }", 7, ""},
		{"ir-strcmp-gt", "function main(): i32 { var a = \"banana\"; var b = \"apple\"; if (a > b) { return 3; } return 0; }", 3, ""},
		{"ir-strcmp-le-eq", "function main(): i32 { var a = \"abc\"; var b = \"abc\"; if (a <= b) { return 5; } return 0; }", 5, ""},
		{"ir-strcmp-prefix", "function main(): i32 { var a = \"ab\"; var b = \"abc\"; if (a < b) { return 9; } return 0; }", 9, ""},
		{"ir-strcmp-ge-false", "function main(): i32 { var a = \"a\"; var b = \"b\"; if (a >= b) { return 11; } return 0; }", 0, ""},
		{"ir-while-break", "function main(): i32 { var s = 0; var i = 0; while (i < 10) { if (i == 5) { break; } s = s + i; i = i + 1; } return s; }", 10, ""},
		{"ir-while-continue", "function main(): i32 { var s = 0; var i = 0; while (i < 10) { i = i + 1; if (i % 2 == 1) { continue; } s = s + i; } return s; }", 30, ""},
		{"ir-while-break-nested", "function main(): i32 { var t = 0; var i = 0; while (i < 3) { var j = 0; while (j < 5) { if (j == 2) { break; } t = t + j; j = j + 1; } i = i + 1; } return t; }", 3, ""},
		{"ir-while-break-deep-if", "function main(): i32 { var s = 0; var i = 0; while (i < 10) { if (i > 3) { if (i == 4) { break; } } s = s + i; i = i + 1; } return s; }", 6, ""},
		{"ir-cast-widen", "function main(): i32 { var n = 100000; var x: i64 = n as i64; var y: i64 = x * x; if (y > 4000000000) { return 5; } return 0; }", 5, ""},
		{"ir-cast-narrow", "function main(): i32 { var big: i64 = 5000000007; var lo = (big as i32); return lo % 100; }", 11, ""},
		{"ir-cast-mixed", "function main(): i32 { var base: i64 = 4000000000; var i = 5; var s: i64 = base + (i as i64); if (s > 4000000000) { return 7; } return 0; }", 7, ""},
		{"ir-cast-roundtrip", "function main(): i32 { var n = 42; var x: i64 = n as i64; return (x as i32); }", 42, ""},
		{"ir-call-8-args", "function add8(a: i32, b: i32, c: i32, d: i32, e: i32, f: i32, g: i32, h: i32): i32 { return a+b+c+d+e+f+g+h; } function main(): i32 { return add8(1,2,3,4,5,6,7,8); }", 36, ""},
		{"ir-method-7-args", "struct P { base: i32 } function (p: P) sum7(a:i32,b:i32,c:i32,d:i32,e:i32,f:i32,g:i32): i32 { return p.base + a+b+c+d+e+f+g; } function main(): i32 { var p = P { base: 10 }; return p.sum7(1,2,3,4,5,6,7); }", 38, ""},
		{"ir-i64-param", "function dbl(x: i64): i64 { return x * 2; } function main(): i32 { var r: i64 = dbl(3000000000); if (r > 5000000000) { return 7; } return 0; }", 7, ""},
		{"ir-i64-return", "function big(): i64 { return 4000000000; } function main(): i32 { var x: i64 = big() + 1000000000; if (x > 4000000000) { return 5; } return 0; }", 5, ""},
		{"ir-i64-param-mixed", "function f(a: i64, b: i32): i64 { return a + (b as i64); } function main(): i32 { var r: i64 = f(4000000000, 5); if (r > 4000000000) { return 9; } return 0; }", 9, ""},
		{"ir-i64-return-recursion", "function pow2(n: i32): i64 { if (n <= 0) { return 1; } return pow2(n - 1) * 2; } function main(): i32 { if (pow2(33) > 4000000000) { return 13; } return 0; }", 13, ""},
		{"ir-i64-div", "function main(): i32 { var a: i64 = 12000000000; var b: i64 = 4; var c: i64 = a / b; if (c > 2000000000) { return 7; } return 0; }", 7, ""},
		{"ir-i64-rem", "function main(): i32 { var a: i64 = 12000000007; var r = (a % 10) as i32; return r; }", 7, ""},
		{"ir-i64-div-signed", "function main(): i32 { var a: i64 = 0 - 12000000000; var c: i64 = a / 4; if (c < 0) { return 9; } return 0; }", 9, ""},
		{"ir-arr-slice", "function main(): i32 { var a = [10, 20, 30, 40, 50]; var b = a[1:4]; return b[0] + b[2]; }", 60, ""},
		{"ir-arr-slice-strarr", "function main(): i32 { var a = [\"x\", \"yy\", \"zzz\", \"w\"]; var b = a[1:3]; return b[0].len() + b[1].len(); }", 5, ""},
		{"ir-struct-arr-field", "struct Buf { data: i32[], n: i32 } function main(): i32 { var b = Buf { data: [10, 20, 30], n: 3 }; var s = 0; var i = 0; while (i < b.n) { s = s + b.data[i]; i = i + 1; } return s; }", 60, ""},
		{"ir-struct-arr-survives", "struct Buf { data: i32[] } function main(): i32 { var b = Buf { data: [10, 20, 30] }; var other = [99, 99, 99, 99, 99]; return b.data[0] + b.data[2]; }", 40, ""},
		{"ir-struct-arr-param", "struct Buf { data: i32[], n: i32 } function sum(b: Buf): i32 { var s = 0; var i = 0; while (i < b.n) { s = s + b.data[i]; i = i + 1; } return s; } function main(): i32 { var b = Buf { data: [5, 10, 15], n: 3 }; return sum(b); }", 30, ""},
		{"ir-strarr-index", "function main(): i32 { var names = [\"foo\", \"bar\", \"hello\"]; return names[0].len() + names[2].len(); }", 8, ""},
		{"ir-strarr-param", "function f(names: string[]): i32 { return names[0].len(); } function main(): i32 { return f([\"abcd\"]); }", 4, ""},
		{"ir-strarr-loop", "function main(): i32 { var names = [\"a\", \"bb\", \"ccc\"]; var s = 0; var i = 0; while (i < 3) { s = s + names[i].len(); i = i + 1; } return s; }", 6, ""},
		// string[]-returning functions (move-on-return; call-site element typing).
		{"ir-strarr-ret", "function names(): string[] { return [\"a\", \"bb\", \"ccc\"]; } function main(): i32 { var xs = names(); return xs[1].len(); }", 2, ""},
		{"ir-strarr-ret-direct-index", "function names(): string[] { return [\"a\", \"bb\", \"ccc\"]; } function main(): i32 { return names()[2].len(); }", 3, ""},
		{"ir-strarr-ret-loop", "function names(): string[] { return [\"a\", \"bb\", \"ccc\", \"dddd\"]; } function main(): i32 { var xs = names(); var i = 0; var s = 0; while (i < xs.len()) { s = s + xs[i].len(); i = i + 1; } return s; }", 10, ""},
		{"ir-strarr-ret-param", "function id(a: string[]): string[] { return a; } function main(): i32 { var xs = [\"q\", \"ww\", \"eee\"]; var ys = id(xs); return ys[1].len() + ys.len(); }", 5, ""},
		// Enums + match: scalar-payload + no-payload variant construction and
		// match dispatch (variant_is on the type-id @0) with payload binding +
		// wildcard. Non-scalar payloads (string) bail to AST.
		{"ir-enum-payload", "enum E { A(i32), B } function f(e: E): i32 { match (e) { A(n) => { return n * 2; }, B => { return 9; } } return 0; } function main(): i32 { return f(A(21)); }", 42, ""},
		{"ir-enum-unit", "enum E { A(i32), B } function f(e: E): i32 { match (e) { A(n) => { return n * 2; }, B => { return 9; } } return 0; } function main(): i32 { return f(B); }", 9, ""},
		{"ir-enum-three", "enum Shape { Circle(i32), Square(i32), Empty } function area(s: Shape): i32 { match (s) { Circle(r) => { return r + 1; }, Square(w) => { return w * 2; }, Empty => { return 7; } } return 99; } function main(): i32 { return area(Circle(4)) + area(Square(5)) + area(Empty); }", 22, ""},
		{"ir-enum-wildcard", "enum E { A(i32), B, C } function f(e: E): i32 { match (e) { A(n) => { return n; }, _ => { return 100; } } return 0; } function main(): i32 { return f(B); }", 100, ""},
		// Methods (receiver = arg 0, static dispatch to $<Type>.<name>).
		{"ir-method-field", "struct P { x: i32 } function (p: P) get(): i32 { return p.x; } function main(): i32 { var p = P { x: 42 }; return p.get(); }", 42, ""},
		{"ir-method-with-arg", "struct B { v: i32 } function (b: B) scale(n: i32): i32 { return b.v * n; } function main(): i32 { var x = B { v: 4 }; return x.scale(3); }", 12, ""},
		{"ir-method-self-dispatch", "struct P { x: i32 } function (p: P) dbl(): i32 { return p.x * 2; } function (p: P) quad(): i32 { return p.dbl() * 2; } function main(): i32 { var p = P { x: 5 }; return p.quad(); }", 20, ""},
		{"ir-method-same-name-two-types", "struct A { n: i32 } struct B { n: i32 } function (a: A) get(): i32 { return a.n + 1; } function (b: B) get(): i32 { return b.n + 100; } function main(): i32 { var a = A { n: 5 }; var b = B { n: 5 }; return a.get() + b.get(); }", 111, ""},
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
		{"str-to-upper", "function main(): i32 { write(\"hello\".to_ascii_upper()); return 0; }", 0, "HELLO"},
		{"str-to-lower", "function main(): i32 { write(\"HeLLo\".to_ascii_lower()); return 0; }", 0, "hello"},
		{"str-to-upper-mixed", "function main(): i32 { var s = \"aB3z!\"; write(s.to_ascii_upper()); return 0; }", 0, "AB3Z!"},
		{"str-repeat", "function main(): i32 { write(\"ab\".repeat(3)); return 0; }", 0, "ababab"},
		{"str-repeat-zero", "function main(): i32 { var s = \"x\".repeat(0); return s.len(); }", 0, ""},
		{"str-repeat-var", "function main(): i32 { var s = \"-\"; write(s.repeat(5)); return 0; }", 0, "-----"},
		{"str-upper-concat", "function main(): i32 { write(\"hi \".to_ascii_upper() + \"there\"); return 0; }", 0, "HI there"},
		{"str-method-chain", "function main(): i32 { write(\"AbC\".to_ascii_lower().to_ascii_upper()); return 0; }", 0, "ABC"},
		{"str-upper-len", "function main(): i32 { return \"abc\".to_ascii_upper().len(); }", 3, ""},
		// i32 arrays (read side): literal, index, .len(), while-sum.
		{"arr-len", "function main(): i32 { var a = [10, 20, 30]; return a.len(); }", 3, ""},
		{"cell-get-set", "function main(): i32 { var c: Cell[i32] = cell_new(0); c.set(c.get() + 5); c.set(c.get() * 2); return c.get(); }", 10, ""},
		{"cell-shared", "function bump(c: Cell[i32]): void { c.set(c.get() + 1); } function main(): i32 { var c: Cell[i32] = cell_new(10); bump(c); bump(c); bump(c); return c.get(); }", 13, ""},
		// Cell[string] — a string is a single pointer on the self-host wasm
		// backend, so the cell slot is one word (same as i32). Overwrite then
		// read back through get().
		{"cell-string", "function main(): i32 { var c: Cell[string] = cell_new(\"A\"); c.set(\"hi\"); write(c.get()); return 0; }", 0, "hi"},
		// Cell stored in a STRUCT FIELD: get/set on `b.c` (a field access).
		// is_cell_expr resolves the owner struct + the field's declared Cell
		// type so the wasm backend dispatches the cell load/store (it
		// previously only recognised Ident/cell_new receivers and miscompiled
		// `b.c.get()` to a bare i32.load). This is the lam_ctr/lamdefs shape.
		{"cell-i32-field", "struct Box { c: Cell[i32] } function main(): i32 { var b: Box = Box { c: cell_new(5) }; b.c.set(b.c.get() + 1); return b.c.get(); }", 6, ""},
		{"cell-str-field", "struct Box { c: Cell[string] } function main(): i32 { var b: Box = Box { c: cell_new(\"ab\") }; b.c.set(\"xyz\"); return b.c.get().len(); }", 3, ""},
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
		{"arr-push-len", "function main(): i32 { var a: i32[] = [1, 2, 3]; a = a.append(4); return a.len(); }", 4, ""},
		{"arr-push-last", "function main(): i32 { var a: i32[] = [10, 20]; a = a.append(99); return a[2]; }", 99, ""},
		{"arr-push-empty", "function main(): i32 { var a: i32[] = []; a = a.append(42); return a[0]; }", 42, ""},
		{"arr-push-chain", "function main(): i32 { var a: i32[] = []; a = a.append(1); a = a.append(2); a = a.append(3); return a[0] + a[1] + a[2]; }", 6, ""},
		{"arr-push-grow", "function main(): i32 { var a: i32[] = []; var i = 0; while (i < 10) { a = a.append(i); i = i + 1; } var s = 0; for x in a { s = s + x; } return s; }", 45, ""},
		// 64-bit-element arrays (i64[] / f64[]) use 8-byte element slots +
		// i64/f64 load/store, so values above 2^31 round-trip (the 4-byte
		// i32 slot would emit an out-of-range (i32.const …) and truncate).
		// Exercises every element path: literal, index, for, set, push,
		// slice. Regression: i64/f64 8-byte-slot series.
		{"i64arr-literal-index-large", "function main(): i32 { var xs: i64[] = [5000000000, 42]; if (xs[0] == 5000000000) { return xs[1] as i32; } return 0; }", 42, ""},
		{"i64arr-for-sum", "function main(): i32 { var xs: i64[] = [3, 5, 90]; var s: i64 = 0; for v in xs { s = s + v; } return s as i32; }", 98, ""},
		{"i64arr-set-index-large", "function main(): i32 { var xs: i64[] = [1, 2, 3]; xs[1] = 5000000000; if (xs[1] == 5000000000) { return 7; } return 0; }", 7, ""},
		{"i64arr-push-grow", "function main(): i32 { var xs: i64[] = [10]; xs = xs.append(20); xs = xs.append(30); var s: i64 = 0; for v in xs { s = s + v; } return s as i32; }", 60, ""},
		{"i64arr-slice", "function main(): i32 { var xs: i64[] = [10, 20, 30, 40]; var ys = xs[1:3]; return (ys[0] + ys[1]) as i32; }", 50, ""},
		{"i64arr-param", "function sum(xs: i64[]): i64 { var s: i64 = 0; for v in xs { s = s + v; } return s; } function main(): i32 { var xs: i64[] = [10, 20, 30]; return sum(xs) as i32; }", 60, ""},
		{"f64arr-for-sum", "function main(): i32 { var xs: f64[] = [1.5, 2.5, 3.0]; var s: f64 = 0.0; for v in xs { s = s + v; } return s as i32; }", 7, ""},
		// i32 arrays must be unaffected by the wide-slot machinery.
		{"i32arr-noregress-mix", "function main(): i32 { var xs: i32[] = [3, 5, 9]; xs = xs.append(11); var s = 0; for v in xs { s = s + v; } var ys = xs[1:3]; return s + ys[0]; }", 33, ""},
		// String arrays (string[]): literal, index, for-in, push, and
		// element used in string contexts (needs element typing).
		{"sarr-for-write", "function main(): i32 { var xs = [\"a\", \"b\", \"c\"]; for s in xs { write(s); } return 0; }", 0, "abc"},
		{"sarr-index-write", "function main(): i32 { var xs = [\"foo\", \"bar\"]; write(xs[1]); return 0; }", 0, "bar"},
		{"sarr-len", "function main(): i32 { var xs = [\"a\", \"b\", \"c\", \"d\"]; return xs.len(); }", 4, ""},
		{"sarr-elem-len", "function main(): i32 { var xs = [\"hello\", \"hi\"]; return xs[0].len(); }", 5, ""},
		{"sarr-elem-concat", "function main(): i32 { var xs = [\"foo\", \"bar\"]; write(xs[0] + xs[1]); return 0; }", 0, "foobar"},
		{"sarr-for-concat", "function main(): i32 { var xs = [\"a\", \"b\", \"c\"]; var acc = \"\"; for s in xs { acc = acc + s; } write(acc); return 0; }", 0, "abc"},
		{"sarr-for-eq", "function main(): i32 { var xs = [\"x\", \"y\", \"z\"]; var n = 0; for s in xs { if (s == \"y\") { n = n + 1; } } return n; }", 1, ""},
		{"sarr-push", "function main(): i32 { var xs: string[] = [\"a\"]; xs = xs.append(\"b\"); write(xs[1]); return xs.len(); }", 2, "b"},
		{"sarr-param", "function first(xs: string[]): string { return xs[0]; } function main(): i32 { write(first([\"hello\", \"world\"])); return 0; }", 0, "hello"},
		{"sarr-elem-method", "function main(): i32 { var xs = [\"abc\"]; write(xs[0].to_ascii_upper()); return 0; }", 0, "ABC"},
		{"sarr-for-var-method", "function main(): i32 { var xs = [\"ab\", \"cd\"]; for s in xs { write(s.to_ascii_upper()); } return 0; }", 0, "ABCD"},
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
		{"split-elem-method", "function main(): i32 { var parts = \"ab,cd\".split(\",\"); write(parts[0].to_ascii_upper()); return 0; }", 0, "AB"},
		// Structs: literal, field read, field assign, struct param/return.
		{"struct-field-read", "struct P { x: i32, y: i32 } function main(): i32 { var p = P { x: 40, y: 2 }; return p.x + p.y; }", 42, ""},
		{"struct-field-order", "struct P { x: i32, y: i32 } function main(): i32 { var p = P { y: 2, x: 40 }; return p.x; }", 40, ""},
		{"struct-field-assign", "struct P { x: i32, y: i32 } function main(): i32 { var p = P { x: 1, y: 2 }; p.x = 99; return p.x + p.y; }", 101, ""},
		{"struct-string-field", "struct Person { name: string, age: i32 } function main(): i32 { var p = Person { name: \"Sam\", age: 30 }; write(p.name); return p.age; }", 30, "Sam"},
		{"struct-string-field-concat", "struct Person { name: string, age: i32 } function main(): i32 { var p = Person { name: \"Sam\", age: 30 }; write(\"hi \" + p.name); return 0; }", 0, "hi Sam"},
		{"struct-string-field-method", "struct Box { s: string } function main(): i32 { var b = Box { s: \"abc\" }; write(b.s.to_ascii_upper()); return 0; }", 0, "ABC"},
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
		// errdefer (parse-time desugar, shared by every backend): cleanup runs
		// only on a `return None`/`Err`, observed via a 1-element i32[] cell.
		{"errdefer-err-fires", "function f(out: i32[], x: i32): Result[i32, i32] { errdefer out[0] = 9; if (x < 0) { return Err(1); } return Ok(x); } function main(): i32 { var a: i32[] = [0]; match (f(a, -1)) { Ok(v) => {}, Err(e) => {} } return a[0]; }", 9, ""},
		{"errdefer-ok-no-fire", "function f(out: i32[], x: i32): Result[i32, i32] { errdefer out[0] = 9; if (x < 0) { return Err(1); } return Ok(x); } function main(): i32 { var a: i32[] = [0]; match (f(a, 5)) { Ok(v) => {}, Err(e) => {} } return a[0]; }", 0, ""},
		{"opt-wildcard", "function mk(): Option[i32] { return None; } function main(): i32 { match (mk()) { Some(v) => { return v; }, _ => { return 99; } } return 1; }", 99, ""},
		// Match-arm guards (`Pat when <expr> =>`): a true guard runs the arm; a
		// false guard falls through to the next arm (the guard reads the binding).
		{"match-guard-pass", "function mk(): Option[i32] { return Some(8); } function main(): i32 { match (mk()) { Some(v) when v > 5 => { return 1; }, _ => { return 2; } } return 0; }", 1, ""},
		{"match-guard-fallthrough", "function mk(): Option[i32] { return Some(3); } function main(): i32 { match (mk()) { Some(v) when v > 5 => { return 1; }, _ => { return 2; } } return 0; }", 2, ""},
		// Match expressions (value position): desugar to an IIFE + statement-match.
		{"match-expr", "function mk(): Option[i32] { return Some(5); } function main(): i32 { var x: i32 = match (mk()) { Some(n) => n, None => 0 }; return x; }", 5, ""},
		{"match-expr-other-arm", "function mk(): Option[i32] { return None; } function main(): i32 { return match (mk()) { Some(n) => n, None => 42 }; }", 42, ""},
		// let-else — desugars the rest of the block into a match success
		// arm; the else block is the wildcard (diverging) arm.
		{"let-else-matched", "function mk(): Option[i32] { return Some(42); } function main(): i32 { let Some(v) = mk() else { return 1; } return v; }", 42, ""},
		{"let-else-diverge", "function mk(): Option[i32] { return None; } function main(): i32 { let Some(v) = mk() else { return 7; } return v; }", 7, ""},
		{"let-else-rest-multi", "function mk(): Option[i32] { return Some(40); } function main(): i32 { let Some(v) = mk() else { return 1; } var w: i32 = v + 2; return w; }", 42, ""},
		// recursive local functions — hoisted to top level when capture-free.
		{"rec-local-factorial", "function main(): i32 { function fact(n: i32): i32 { if (n <= 1) { return 1; } return n * fact(n - 1); } return fact(5); }", 120, ""},
		{"rec-local-fib", "function main(): i32 { function fib(n: i32): i32 { if (n < 2) { return n; } return fib(n - 1) + fib(n - 2); } return fib(10); }", 55, ""},
		{"opt-local", "function main(): i32 { var o: Option[i32] = Some(5); match (o) { Some(v) => { return v * 2; }, None => { return 0; } } return 1; }", 10, ""},
		{"opt-string-write", "function name(): Option[i32] { return Some(0); } function main(): i32 { match (name()) { Some(v) => { write(\"got\"); return 0; }, None => { write(\"none\"); return 0; } } return 1; }", 0, "got"},
		{"opt-in-if", "function lookup(k: i32): Option[i32] { if (k == 1) { return Some(100); } return None; } function main(): i32 { var sum = 0; match (lookup(1)) { Some(v) => { sum = sum + v; }, None => {} } match (lookup(2)) { Some(v) => { sum = sum + v; }, None => { sum = sum + 1; } } return sum; }", 101, ""},
		{"opt-nested-match", "function a(): Option[i32] { return Some(1); } function b(): Option[i32] { return Some(2); } function main(): i32 { match (a()) { Some(x) => { match (b()) { Some(y) => { return x + y + 39; }, None => { return 0; } } }, None => { return 0; } } return 1; }", 42, ""},
		// boolean `match`: `true`/`false` arms must compare the scrutinee,
		// not unconditionally take the first arm (regression: harden9).
		{"match-bool-true", "function main(): i32 { var b = true; match (b) { true => { return 8; }, false => { return 7; } } return 1; }", 8, ""},
		{"match-bool-false", "function main(): i32 { var b = false; match (b) { true => { return 7; }, false => { return 8; } } return 1; }", 8, ""},
		{"match-bool-cmp", "function main(): i32 { var x = 5; match (x > 3) { true => { return 42; }, false => { return 0; } } return 1; }", 42, ""},
		{"match-bool-cmp-false", "function main(): i32 { var x = 2; match (x > 3) { true => { return 0; }, false => { return 42; } } return 1; }", 42, ""},
		{"match-bool-nested", "function main(): i32 { var a = true; var b = false; match (a) { true => { match (b) { true => { return 1; }, false => { return 123; } } }, false => { return 0; } } return 1; }", 123, ""},
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
		{"payload-method", "function f(): Option[string] { return Some(\"abc\"); } function main(): i32 { match (f()) { Some(s) => { write(s.to_ascii_upper()); return 0; }, None => { return 0; } } return 1; }", 0, "ABC"},
		{"payload-concat", "function f(): Option[string] { return Some(\"foo\"); } function g(): Option[string] { return Some(\"bar\"); } function main(): i32 { match (f()) { Some(a) => { match (g()) { Some(b) => { write(a + b); return 0; }, None => { return 0; } } }, None => { return 0; } } return 1; }", 0, "foobar"},
		{"payload-local-scrut", "function f(): Option[string] { return Some(\"abc\"); } function main(): i32 { var o: Option[string] = f(); match (o) { Some(s) => { write(s.to_ascii_upper()); return 0; }, None => { return 0; } } return 1; }", 0, "ABC"},
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
		{"method-string-result-method", "struct Box { s: string } function (b: Box) val(): string { return b.s; } function main(): i32 { var x = Box { s: \"abc\" }; write(x.val().to_ascii_upper()); return 0; }", 0, "ABC"},
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
		// env(name): Option[string] via wasi environ (runner sets
		// FERNTEST=hello123 and EMPTYVAR=).
		{"env-set", "function main(): i32 { match (env(\"FERNTEST\")) { Some(v) => { write(v); return 0; }, None => { write(\"none\"); return 0; } } return 1; }", 0, "hello123"},
		{"env-missing", "function main(): i32 { match (env(\"NOPE_NOT_SET\")) { Some(v) => { write(v); return 0; }, None => { write(\"none\"); return 0; } } return 1; }", 0, "none"},
		{"env-empty", "function main(): i32 { match (env(\"EMPTYVAR\")) { Some(v) => { return v.len() + 7; }, None => { return 0; } } return 1; }", 7, ""},
		{"env-payload-method", "function main(): i32 { match (env(\"FERNTEST\")) { Some(v) => { write(v.to_ascii_upper()); return 0; }, None => { return 0; } } return 1; }", 0, "HELLO123"},
		{"env-len", "function main(): i32 { match (env(\"FERNTEST\")) { Some(v) => { return v.len(); }, None => { return 0; } } return 1; }", 8, ""},
		// random_bytes(n): u8[] via wasi random_get — non-deterministic
		// values, so assert length + byte range (0..255).
		{"random-len", "function main(): i32 { var b = random_bytes(8); return b.len(); }", 8, ""},
		{"random-zero", "function main(): i32 { var b = random_bytes(0); return b.len(); }", 0, ""},
		{"random-range", "function main(): i32 { var b = random_bytes(100); for x in b { if (x < 0) { return 1; } if (x > 255) { return 2; } } return 42; }", 42, ""},
		{"random-index", "function main(): i32 { var b = random_bytes(4); var x = b[0]; if (x >= 0 && x <= 255) { return 7; } return 1; }", 7, ""},
		// args(): string[] — the wasi argv (runner appends ALPHA BETA; argv[0]
		// is the module name).
		{"args-count", "function main(): i32 { return args().len(); }", 3, ""},
		{"args-index", "function main(): i32 { var a = args(); write(a[1]); write(a[2]); return 0; }", 0, "ALPHABETA"},
		{"args-for", "function main(): i32 { var a = args(); var n = 0; for s in a { n = n + s.len(); } if (n > 0) { write(a[1]); return 0; } return 1; }", 0, "ALPHA"},
		{"args-method", "function main(): i32 { var a = args(); write(a[1].to_ascii_lower()); return 0; }", 0, "alpha"},
		// read_file(path): Result[string, IoError] via wasi path_open/fd_read
		// (runner preopens the project dir, which contains rf_test.txt =
		// "file-contents-123").
		{"readfile-ok", "function main(): i32 { match (read_file(\"rf_test.txt\")) { Ok(s) => { write(s); return 0; }, Err(e) => { write(\"err\"); return 1; } } return 2; }", 0, "file-contents-123"},
		{"readfile-len", "function main(): i32 { match (read_file(\"rf_test.txt\")) { Ok(s) => { return s.len(); }, Err(e) => { return 0; } } return 1; }", 17, ""},
		{"readfile-method", "function main(): i32 { match (read_file(\"rf_test.txt\")) { Ok(s) => { if (s.starts_with(\"file-\")) { return 42; } return 1; }, Err(e) => { return 2; } } return 3; }", 42, ""},
		{"readfile-missing", "function main(): i32 { match (read_file(\"nope_missing.txt\")) { Ok(s) => { write(s); return 0; }, Err(e) => { write(\"err\"); return 0; } } return 2; }", 0, "err"},
		// write_file(path, content): Option[IoError] (None = ok). Tested by
		// a write→read round-trip in-program (preopened dir is writable).
		{"writefile-roundtrip", "function main(): i32 { match (write_file(\"wt.txt\", \"roundtrip!\")) { Some(e) => { return 1; }, None => {} } match (read_file(\"wt.txt\")) { Ok(s) => { write(s); return 0; }, Err(e) => { write(\"err\"); return 2; } } return 3; }", 0, "roundtrip!"},
		{"writefile-ok-none", "function main(): i32 { match (write_file(\"wt2.txt\", \"x\")) { Some(e) => { return 1; }, None => { return 0; } } return 2; }", 0, ""},
		{"writefile-built-content", "function main(): i32 { var c = \"a\" + \"b\" + \"c\"; var e = write_file(\"wt3.txt\", c); match (read_file(\"wt3.txt\")) { Ok(s) => { write(s); return 0; }, Err(x) => { return 1; } } return 2; }", 0, "abc"},

		// i64 value path. Literals / arithmetic that exceed the i32 range
		// must round-trip through 64-bit locals + the i64 formatter; bare
		// literals in i64 sinks (var/return/arg) coerce to i64.const.
		{"i64-literal-print", "function main(): i32 { var x: i64 = 5000000000; print_int(x); return 0; }", 0, "5000000000"},
		{"i64-add", "function main(): i32 { var a: i64 = 3000000000; var b: i64 = 2000000000; print_int(a + b); return 0; }", 0, "5000000000"},
		{"i64-sub", "function main(): i32 { var a: i64 = 5000000000; var b: i64 = 1000000000; print_int(a - b); return 0; }", 0, "4000000000"},
		{"i64-mul", "function main(): i32 { var a: i64 = 100000; var b: i64 = 100000; print_int(a * b); return 0; }", 0, "10000000000"},
		{"i64-div", "function main(): i32 { var a: i64 = 10000000000; print_int(a / 7); return 0; }", 0, "1428571428"},
		{"i64-rem", "function main(): i32 { var a: i64 = 10000000000; print_int(a % 7); return 0; }", 0, "4"},
		{"i64-negative", "function main(): i32 { var a: i64 = 0; var b: i64 = 5000000000; print_int(a - b); return 0; }", 0, "-5000000000"},
		{"i64-div-by-zero-guarded", "function main(): i32 { var a: i64 = 9000000000; var z: i64 = 0; print_int(a / z); return 0; }", 0, "0"},
		{"i64-rem-by-zero-guarded", "function main(): i32 { var a: i64 = 9000000000; var z: i64 = 0; print_int(a % z); return 0; }", 0, "9000000000"},
		{"i64-func-return", "function big(): i64 { return 9000000000; } function main(): i32 { print_int(big()); return 0; }", 0, "9000000000"},
		{"i64-param", "function dbl(x: i64): i64 { return x * 2; } function main(): i32 { print_int(dbl(3000000000)); return 0; }", 0, "6000000000"},
		{"i64-param-var", "function add1(x: i64): i64 { return x + 1; } function main(): i32 { var t: i64 = 9999999999; print_int(add1(t)); return 0; }", 0, "10000000000"},
		{"i64-reassign", "function main(): i32 { var a: i64 = 1000000000; a = a * 5; print_int(a); return 0; }", 0, "5000000000"},
		{"i64-compare-gt", "function main(): i32 { var a: i64 = 5000000000; if (a > 4000000000) { print_int(1); } else { print_int(0); } return 0; }", 0, "1"},
		{"i64-compare-eq", "function main(): i32 { var a: i64 = 5000000000; var b: i64 = 5000000000; if (a == b) { print_int(1); } else { print_int(0); } return 0; }", 0, "1"},
		{"i64-loop-accumulate", "function main(): i32 { var sum: i64 = 0; var i: i32 = 0; while (i < 5) { sum = sum + 1000000000; i = i + 1; } print_int(sum); return 0; }", 0, "5000000000"},

		// Clock builtins are non-deterministic; assert structural facts
		// only. monotonic_ns is non-decreasing (b - a >= 0); now_unix_ms is
		// well past the year-2001 epoch (> 1e12 ms).
		{"monotonic-non-decreasing", "function main(): i32 { var a: i64 = monotonic_ns(); var b: i64 = monotonic_ns(); if (b - a >= 0) { print_int(1); } else { print_int(0); } return 0; }", 0, "1"},
		{"now-unix-ms-recent", "function main(): i32 { var t: i64 = now_unix_ms(); if (t > 1000000000000) { print_int(1); } else { print_int(0); } return 0; }", 0, "1"},
		{"now-ns-positive", "function main(): i32 { var t: i64 = now_ns(); if (t > 0) { print_int(1); } else { print_int(0); } return 0; }", 0, "1"},

		// f64 value path. Floats lower to f64 wasm ops; printed by casting
		// the result to an integer (`x as i32` → i32.trunc_f64_s) since the
		// backend has no float formatter yet. Casts also exercise the
		// `as <ty>` desugar (unhandled on wasm before this slice).
		{"f64-literal-cast", "function main(): i32 { var x: f64 = 3.5; print_int(x as i32); return 0; }", 0, "3"},
		{"f64-add", "function main(): i32 { var a: f64 = 2.5; var b: f64 = 1.5; print_int((a + b) as i32); return 0; }", 0, "4"},
		{"f64-sub", "function main(): i32 { var a: f64 = 5.5; var b: f64 = 2.5; print_int((a - b) as i32); return 0; }", 0, "3"},
		{"f64-mul", "function main(): i32 { var a: f64 = 2.5; var b: f64 = 4.0; print_int((a * b) as i32); return 0; }", 0, "10"},
		{"f64-div", "function main(): i32 { var a: f64 = 9.0; var b: f64 = 2.0; print_int((a / b) as i32); return 0; }", 0, "4"},
		{"f64-neg", "function main(): i32 { var a: f64 = 4.0; print_int((-a) as i32); return 0; }", 0, "-4"},
		{"f64-compare-gt", "function main(): i32 { if (3.5 > 2.0) { print_int(1); } else { print_int(0); } return 0; }", 0, "1"},
		{"f64-compare-eq", "function main(): i32 { var a: f64 = 1.5; var b: f64 = 1.5; if (a == b) { print_int(1); } else { print_int(0); } return 0; }", 0, "1"},
		{"f64-compare-le", "function main(): i32 { var a: f64 = 2.0; if (a <= 2.0) { print_int(1); } else { print_int(0); } return 0; }", 0, "1"},
		{"f64-int-to-float", "function main(): i32 { var n: i32 = 7; var x: f64 = n as f64; print_int((x + 0.5) as i32); return 0; }", 0, "7"},
		{"f64-mixed-int-literal", "function main(): i32 { print_int((3.5 + 2) as i32); return 0; }", 0, "5"},
		{"f64-reassign", "function main(): i32 { var a: f64 = 1.0; a = a * 3.0; print_int(a as i32); return 0; }", 0, "3"},
		// f64_bits / f64_from_bits reinterpret an f64 to/from its IEEE-754
		// i64 bit pattern (i64.reinterpret_f64 / f64.reinterpret_i64) — the
		// wasm backend previously lacked these (native had them).
		{"f64-bits-hi", "function main(): i32 { return (f64_bits(3.5) >> 56) as i32; }", 64, ""},
		{"f64-bits-roundtrip", "function main(): i32 { var x: f64 = 3.5; if (f64_from_bits(f64_bits(x)) == x) { return 7; } return 0; }", 7, ""},
		{"f64-loop-accumulate", "function main(): i32 { var sum: f64 = 0.0; var i: i32 = 0; while (i < 4) { sum = sum + 1.5; i = i + 1; } print_int(sum as i32); return 0; }", 0, "6"},
		{"f64-func-return", "function half(x: f64): f64 { return x / 2.0; } function main(): i32 { print_int(half(9.0) as i32); return 0; }", 0, "4"},
		{"f64-param-int-arg", "function addhalf(x: f64): f64 { return x + 0.5; } function main(): i32 { print_int(addhalf(3) as i32); return 0; }", 0, "3"},
		{"f64-sqrt", "function main(): i32 { print_int(__sqrt_f64(16.0) as i32); return 0; }", 0, "4"},
		{"f64-floor", "function main(): i32 { print_int(__floor_f64(3.9) as i32); return 0; }", 0, "3"},
		{"f64-ceil", "function main(): i32 { print_int(__ceil_f64(3.1) as i32); return 0; }", 0, "4"},
		{"f64-trunc", "function main(): i32 { print_int(__trunc_f64(3.9) as i32); return 0; }", 0, "3"},
		{"f64-abs", "function main(): i32 { print_int(__abs_f64(-5.0) as i32); return 0; }", 0, "5"},
		{"f64-to-i64-cast", "function main(): i32 { var x: f64 = 5000000000.0; var r: i64 = x as i64; print_int(r); return 0; }", 0, "5000000000"},
		{"f64-to-i64-direct-print", "function main(): i32 { print_int(9000000000.0 as i64); return 0; }", 0, "9000000000"},
		// Un-annotated f64 locals: `var x = 1.5` (no `: f64`) must declare its
		// wasm local as f64 to match the f64 value stored — the type is inferred
		// from the initialiser (literal / float arith / negation / `as f64`).
		// Regression: collect_f64_var_names previously only honoured an explicit
		// annotation, so an inferred-f64 local was declared i32 and the module
		// failed to validate (i32 local <- f64.const).
		{"f64-inferred-unused", "function main(): i32 { var z = 1.5; return 5; }", 5, ""},
		{"f64-inferred-cast-print", "function main(): i32 { var x = 3.5; print_int(x as i32); return 0; }", 0, "3"},
		{"f64-inferred-arith", "function main(): i32 { var x = 2.5 + 1.5; print_int(x as i32); return 0; }", 0, "4"},
		{"f64-inferred-neg", "function main(): i32 { var a = 0.0 - 4.0; print_int(a as i32); return 0; }", 0, "-4"},
		{"f64-inferred-from-cast", "function main(): i32 { var x = 7 as f64; print_int((x + 0.5) as i32); return 0; }", 0, "7"},
		{"f64-inferred-reassign", "function main(): i32 { var a = 1.0; a = a * 3.0; print_int(a as i32); return 0; }", 0, "3"},
		{"f64-inferred-compare", "function main(): i32 { var z = 1.5; if (z > 1.0) { print_int(1); } else { print_int(0); } return 0; }", 0, "1"},

		// i32-keyed / i32-valued maps. `Map { k: v }` desugars to
		// map_new_i32(8).insert(...).insert(...); methods dispatch to the hash
		// runtime. `.len()` reuses the generic length read (count @ box+0).
		{"map-get-or", "function main(): i32 { var m = Map { 1: 10, 2: 20 }; print_int(m.get_or(1, 0)); return 0; }", 0, "10"},
		{"map-get-or-second", "function main(): i32 { var m = Map { 1: 10, 2: 20 }; print_int(m.get_or(2, 0)); return 0; }", 0, "20"},
		{"map-get-or-missing", "function main(): i32 { var m = Map { 1: 10 }; print_int(m.get_or(2, 99)); return 0; }", 0, "99"},
		{"map-has", "function main(): i32 { var m = Map { 5: 1 }; if (m.has(5)) { print_int(1); } else { print_int(0); } return 0; }", 0, "1"},
		{"map-has-missing", "function main(): i32 { var m = Map { 5: 1 }; if (m.has(6)) { print_int(1); } else { print_int(0); } return 0; }", 0, "0"},
		{"map-len", "function main(): i32 { var m = Map { 1: 1, 2: 2, 3: 3 }; print_int(m.len()); return 0; }", 0, "3"},
		{"map-update-value", "function main(): i32 { var m = Map { 1: 10 }; m = m.insert(1, 99); print_int(m.get_or(1, 0)); return 0; }", 0, "99"},
		{"map-update-keeps-len", "function main(): i32 { var m = Map { 1: 10 }; m = m.insert(1, 99); print_int(m.len()); return 0; }", 0, "1"},
		{"map-get-some", "function main(): i32 { var m = Map { 7: 42 }; match (m.get(7)) { Some(v) => { print_int(v); }, None => { print_int(0); } } return 0; }", 0, "42"},
		{"map-get-none", "function main(): i32 { var m = Map { 7: 42 }; match (m.get(8)) { Some(v) => { print_int(v); }, None => { print_int(0); print_int(1); } } return 0; }", 0, "01"},
		{"map-empty-then-set", "function main(): i32 { var m = map_new_i32(8); m = m.insert(3, 30); print_int(m.get_or(3, 0)); return 0; }", 0, "30"},
		{"map-zero-key", "function main(): i32 { var m = map_new_i32(8); m = m.insert(0, 123); print_int(m.get_or(0, -1)); return 0; }", 0, "123"},
		{"map-negative-key", "function main(): i32 { var m = Map { 1: 5 }; m = m.insert(-7, 88); print_int(m.get_or(-7, 0)); return 0; }", 0, "88"},
		{"map-grow-get", "function main(): i32 { var m = map_new_i32(8); var i: i32 = 0; while (i < 50) { m = m.insert(i, i * 2); i = i + 1; } print_int(m.get_or(37, -1)); return 0; }", 0, "74"},
		{"map-grow-len", "function main(): i32 { var m = map_new_i32(8); var i: i32 = 0; while (i < 50) { m = m.insert(i, i * 2); i = i + 1; } print_int(m.len()); return 0; }", 0, "50"},
		{"map-overwrite-loop", "function main(): i32 { var m = map_new_i32(8); var i: i32 = 0; while (i < 10) { m = m.insert(1, i); i = i + 1; } print_int(m.get_or(1, -1)); print_int(m.len()); return 0; }", 0, "91"},

		// String-keyed maps. A `Map { "k": v }` literal desugars to
		// map_new(8).insert(...); keys hash + compare by content (FNV-1a +
		// __fern_streq), so distinct pointers with equal bytes match.
		{"strmap-get-or", "function main(): i32 { var m = Map { \"a\": 1, \"b\": 2 }; print_int(m.get_or(\"a\", 0)); return 0; }", 0, "1"},
		{"strmap-get-or-second", "function main(): i32 { var m = Map { \"a\": 1, \"b\": 2 }; print_int(m.get_or(\"b\", 0)); return 0; }", 0, "2"},
		{"strmap-missing", "function main(): i32 { var m = Map { \"a\": 1 }; print_int(m.get_or(\"z\", 99)); return 0; }", 0, "99"},
		{"strmap-has", "function main(): i32 { var m = Map { \"hello\": 1 }; if (m.has(\"hello\")) { print_int(1); } else { print_int(0); } return 0; }", 0, "1"},
		{"strmap-has-missing", "function main(): i32 { var m = Map { \"hello\": 1 }; if (m.has(\"world\")) { print_int(1); } else { print_int(0); } return 0; }", 0, "0"},
		{"strmap-len", "function main(): i32 { var m = Map { \"a\": 1, \"b\": 2, \"c\": 3 }; print_int(m.len()); return 0; }", 0, "3"},
		{"strmap-update", "function main(): i32 { var m = Map { \"a\": 1 }; m = m.insert(\"a\", 50); print_int(m.get_or(\"a\", 0)); print_int(m.len()); return 0; }", 0, "501"},
		{"strmap-get-some", "function main(): i32 { var m = Map { \"k\": 42 }; match (m.get(\"k\")) { Some(v) => { print_int(v); }, None => { print_int(0); } } return 0; }", 0, "42"},
		{"strmap-get-none", "function main(): i32 { var m = Map { \"k\": 42 }; match (m.get(\"x\")) { Some(v) => { print_int(v); }, None => { print_int(7); } } return 0; }", 0, "7"},
		{"strmap-content-equality", "function main(): i32 { var k = \"h\" + \"i\"; var m = map_new(8); m = m.insert(k, 7); print_int(m.get_or(\"hi\", 0)); return 0; }", 0, "7"},
		{"strmap-prefix-distinct", "function main(): i32 { var m = Map { \"ab\": 1, \"abc\": 2 }; print_int(m.get_or(\"ab\", 0)); print_int(m.get_or(\"abc\", 0)); return 0; }", 0, "12"},
		{"strmap-grow", "function main(): i32 { var m = map_new(8); var i: i32 = 1; while (i <= 20) { m = m.insert(\"x\".repeat(i), i); i = i + 1; } print_int(m.get_or(\"x\".repeat(5), -1)); print_int(m.len()); return 0; }", 0, "520"},

		// String-valued maps. The runtime stores i32 slots (a string is a
		// pointer), so this is purely value-type tracking: `.get` / `.get_or`
		// results are typed as string so they print / concat correctly.
		{"strval-get-or", "function main(): i32 { var m = Map { 1: \"one\", 2: \"two\" }; write(m.get_or(1, \"?\")); return 0; }", 0, "one"},
		{"strval-get-or-missing", "function main(): i32 { var m = Map { 1: \"one\" }; write(m.get_or(3, \"none\")); return 0; }", 0, "none"},
		{"strval-string-key", "function main(): i32 { var m = Map { \"x\": \"hello\" }; write(m.get_or(\"x\", \"?\")); return 0; }", 0, "hello"},
		{"strval-get-some", "function main(): i32 { var m = Map { 1: \"one\", 2: \"two\" }; match (m.get(2)) { Some(v) => { write(v); }, None => { write(\"none\"); } } return 0; }", 0, "two"},
		{"strval-get-none", "function main(): i32 { var m = Map { 1: \"one\" }; match (m.get(9)) { Some(v) => { write(v); }, None => { write(\"none\"); } } return 0; }", 0, "none"},
		{"strval-concat", "function main(): i32 { var m = Map { 1: \"one\" }; write(m.get_or(1, \"?\") + \"!\"); return 0; }", 0, "one!"},
		{"strval-update", "function main(): i32 { var m = Map { 1: \"a\" }; m = m.insert(1, \"b\"); write(m.get_or(1, \"?\")); return 0; }", 0, "b"},
		{"strval-built-value", "function main(): i32 { var m = map_new_i32(8); m = m.insert(1, \"x\" + \"y\"); write(m.get_or(1, \"?\")); return 0; }", 0, "xy"},
		{"strval-len", "function main(): i32 { var m = Map { 1: \"a\", 2: \"b\" }; print_int(m.len()); return 0; }", 0, "2"},

		// Map .without — tombstone deletion (used slot → 2; the probe skips
		// past it, set reclaims it, grow drops it). `.without(k)` is typed
		// `(Map[K,V], boolean)` (the map + whether the key existed), so these
		// destructure `var (mw, we) = m.without(k)` and keep the map element —
		// the conforming form the native compiler and register backends require
		// (`m = m.without(k)` is an E003 type error: tuple ≠ map). #2933.
		{"map-delete-has", "function main(): i32 { var m = Map { 1: 10, 2: 20 }; var (mw, we) = m.without(1); m = mw; if (m.has(1)) { print_int(1); } else { print_int(0); } return 0; }", 0, "0"},
		{"map-delete-keeps-other", "function main(): i32 { var m = Map { 1: 10, 2: 20 }; var (mw, we) = m.without(1); m = mw; print_int(m.get_or(2, -1)); return 0; }", 0, "20"},
		{"map-delete-len", "function main(): i32 { var m = Map { 1: 1, 2: 2, 3: 3 }; var (mw, we) = m.without(2); m = mw; print_int(m.len()); return 0; }", 0, "2"},
		{"map-delete-missing-noop", "function main(): i32 { var m = Map { 1: 1 }; var (mw, we) = m.without(99); m = mw; print_int(m.len()); print_int(m.get_or(1, -1)); return 0; }", 0, "11"},
		{"map-delete-get-none", "function main(): i32 { var m = Map { 1: 10 }; var (mw, we) = m.without(1); m = mw; match (m.get(1)) { Some(v) => { print_int(v); }, None => { print_int(7); } } return 0; }", 0, "7"},
		{"map-delete-reinsert", "function main(): i32 { var m = Map { 1: 10 }; var (mw, we) = m.without(1); m = mw; m = m.insert(1, 99); print_int(m.get_or(1, -1)); print_int(m.len()); return 0; }", 0, "991"},
		{"map-delete-mid-chain", "function main(): i32 { var m = map_new_i32(8); var i: i32 = 0; while (i < 10) { m = m.insert(i, i); i = i + 1; } var (mw, we) = m.without(5); m = mw; print_int(m.get_or(4, -1)); print_int(m.get_or(6, -1)); print_int(m.has(5)); print_int(m.len()); return 0; }", 0, "4609"},
		{"map-delete-all-then-reuse", "function main(): i32 { var m = map_new_i32(8); var i: i32 = 0; while (i < 30) { m = m.insert(i, i); i = i + 1; } i = 0; while (i < 30) { var (mw, we) = m.without(i); m = mw; i = i + 1; } print_int(m.len()); m = m.insert(100, 7); print_int(m.get_or(100, -1)); return 0; }", 0, "07"},
		{"strmap-delete", "function main(): i32 { var m = Map { \"a\": 1, \"b\": 2 }; var (mw, we) = m.without(\"a\"); m = mw; print_int(m.has(\"a\")); print_int(m.get_or(\"b\", -1)); return 0; }", 0, "02"},

		// Map .keys() / .values() — snapshot arrays (probe order, so tests
		// assert order-independent facts: lengths and sums).
		{"map-keys-len", "function main(): i32 { var m = Map { 1: 10, 2: 20, 3: 30 }; print_int(m.keys().len()); return 0; }", 0, "3"},
		{"map-values-len", "function main(): i32 { var m = Map { 1: 10, 2: 20 }; print_int(m.values().len()); return 0; }", 0, "2"},
		{"map-values-sum", "function main(): i32 { var m = Map { 1: 10, 2: 20, 3: 30 }; var s: i32 = 0; for v in m.values() { s = s + v; } print_int(s); return 0; }", 0, "60"},
		{"map-keys-sum", "function main(): i32 { var m = Map { 4: 1, 5: 1, 6: 1 }; var s: i32 = 0; for k in m.keys() { s = s + k; } print_int(s); return 0; }", 0, "15"},
		{"map-values-sum-after-delete", "function main(): i32 { var m = Map { 1: 10, 2: 20, 3: 30 }; var (mw, we) = m.without(2); m = mw; var s: i32 = 0; for v in m.values() { s = s + v; } print_int(s); return 0; }", 0, "40"},
		{"map-empty-keys-len", "function main(): i32 { var m = map_new_i32(8); print_int(m.keys().len()); return 0; }", 0, "0"},
		{"map-keys-sum-grow", "function main(): i32 { var m = map_new_i32(8); var i: i32 = 1; while (i <= 20) { m = m.insert(i, i); i = i + 1; } var s: i32 = 0; for k in m.keys() { s = s + k; } print_int(s); return 0; }", 0, "210"},
		{"strmap-keys-charcount", "function main(): i32 { var m = Map { \"ab\": 1, \"cde\": 2 }; var n: i32 = 0; for k in m.keys() { n = n + k.len(); } print_int(n); return 0; }", 0, "5"},
		{"strval-values-charcount", "function main(): i32 { var m = Map { 1: \"ab\", 2: \"cde\" }; var n: i32 = 0; for v in m.values() { n = n + v.len(); } print_int(n); return 0; }", 0, "5"},

		// `for (k, v) in m` — direct pair iteration over live slots (probe
		// order, so tests assert order-independent sums / counts).
		{"map-forkv-sum-both", "function main(): i32 { var m = Map { 1: 10, 2: 20, 3: 30 }; var s: i32 = 0; for (k, v) in m { s = s + k + v; } print_int(s); return 0; }", 0, "66"},
		{"map-forkv-keys-only", "function main(): i32 { var m = Map { 4: 100, 5: 100, 6: 100 }; var s: i32 = 0; for (k, v) in m { s = s + k; } print_int(s); return 0; }", 0, "15"},
		{"map-forkv-count", "function main(): i32 { var m = Map { 1: 1, 2: 2, 3: 3, 4: 4 }; var n: i32 = 0; for (k, v) in m { n = n + 1; } print_int(n); return 0; }", 0, "4"},
		{"map-forkv-after-delete", "function main(): i32 { var m = Map { 1: 10, 2: 20, 3: 30 }; var (mw, we) = m.without(2); m = mw; var s: i32 = 0; for (k, v) in m { s = s + v; } print_int(s); return 0; }", 0, "40"},
		{"map-forkv-empty", "function main(): i32 { var m = map_new_i32(8); var n: i32 = 0; for (k, v) in m { n = n + 1; } print_int(n); return 0; }", 0, "0"},
		{"map-forkv-grow", "function main(): i32 { var m = map_new_i32(8); var i: i32 = 1; while (i <= 20) { m = m.insert(i, i * 2); i = i + 1; } var s: i32 = 0; for (k, v) in m { s = s + v; } print_int(s); return 0; }", 0, "420"},
		{"map-forkv-break", "function main(): i32 { var m = Map { 1: 1, 2: 2, 3: 3 }; var n: i32 = 0; for (k, v) in m { n = n + 1; if (n == 2) { break; } } print_int(n); return 0; }", 0, "2"},
		{"strmap-forkv-keylen", "function main(): i32 { var m = Map { \"ab\": 1, \"cde\": 2 }; var n: i32 = 0; for (k, v) in m { n = n + k.len() + v; } print_int(n); return 0; }", 0, "8"},
		{"strval-forkv-vallen", "function main(): i32 { var m = Map { 1: \"ab\", 2: \"cde\" }; var n: i32 = 0; for (k, v) in m { n = n + k + v.len(); } print_int(n); return 0; }", 0, "8"},

		// Slices `x[a:b]` (both bounds required by the parser). A string
		// slice reuses substr; an array slice copies the element range.
		{"str-slice-mid", "function main(): i32 { write(\"hello\"[1:4]); return 0; }", 0, "ell"},
		{"str-slice-full", "function main(): i32 { var s = \"world\"; write(s[0:5]); return 0; }", 0, "world"},
		{"str-slice-empty", "function main(): i32 { write(\"abc\"[1:1]); return 0; }", 0, ""},
		{"str-slice-len", "function main(): i32 { print_int(\"abcdef\"[2:5].len()); return 0; }", 0, "3"},
		{"str-slice-var-bounds", "function main(): i32 { var s = \"abcdef\"; var a: i32 = 1; var b: i32 = 4; write(s[a:b]); return 0; }", 0, "bcd"},
		{"str-slice-concat", "function main(): i32 { write(\"foo\"[0:2] + \"!\"); return 0; }", 0, "fo!"},
		{"str-slice-then-method", "function main(): i32 { write(\"HELLO\"[1:4].to_ascii_lower()); return 0; }", 0, "ell"},
		{"arr-slice-sum", "function main(): i32 { var xs = [10, 20, 30, 40, 50]; var sub = xs[1:4]; var s: i32 = 0; var i: i32 = 0; while (i < sub.len()) { s = s + sub[i]; i = i + 1; } print_int(s); return 0; }", 0, "90"},
		{"arr-slice-len", "function main(): i32 { var xs = [1, 2, 3, 4, 5]; print_int(xs[0:3].len()); return 0; }", 0, "3"},
		{"arr-slice-index", "function main(): i32 { var xs = [5, 6, 7, 8]; var sub = xs[2:4]; print_int(sub[0]); print_int(sub[1]); return 0; }", 0, "78"},
		{"arr-slice-string-elems", "function main(): i32 { var xs = [\"a\", \"b\", \"c\", \"d\"]; var sub = xs[1:3]; var i: i32 = 0; while (i < sub.len()) { write(sub[i]); i = i + 1; } return 0; }", 0, "bc"},
		{"arr-slice-for", "function main(): i32 { var xs = [1, 2, 3, 4, 5, 6]; var s: i32 = 0; for v in xs[2:5] { s = s + v; } print_int(s); return 0; }", 0, "12"},

		// Tuples `(a, b, …)` — N consecutive slots accessed by the numeric
		// `.N` field. Element types are tracked so a string element prints
		// / concats / supports methods.
		{"tuple-i32-fields", "function main(): i32 { var t = (10, 20); print_int(t.0); print_int(t.1); return 0; }", 0, "1020"},
		{"tuple-inline-access", "function main(): i32 { print_int((5, 7).1); return 0; }", 0, "7"},
		{"tuple-three", "function main(): i32 { var t = (1, 2, 3); print_int(t.0 + t.1 + t.2); return 0; }", 0, "6"},
		{"tuple-from-exprs", "function main(): i32 { var a: i32 = 3; var b: i32 = 4; var t = (a, b); print_int(t.0 * t.1); return 0; }", 0, "12"},
		{"tuple-string-elem", "function main(): i32 { var t = (1, \"hi\"); write(t.1); return 0; }", 0, "hi"},
		{"tuple-mixed-kinds", "function main(): i32 { var t = (\"a\", 42); write(t.0); print_int(t.1); return 0; }", 0, "a42"},
		{"tuple-string-concat", "function main(): i32 { var t = (1, \"x\"); write(t.1 + \"y\"); return 0; }", 0, "xy"},
		{"tuple-string-len", "function main(): i32 { var t = (0, \"hello\"); print_int(t.1.len()); return 0; }", 0, "5"},
		{"tuple-nested", "function main(): i32 { var t = (1, (2, 3)); var inner = t.1; print_int(inner.0 + inner.1); return 0; }", 0, "5"},
		// Destructuring a tuple whose element is a struct must keep the
		// element's struct type so `p.field` resolves (it previously read a
		// bogus `(i32.const 0)`). Covers an inline literal, an intermediate
		// tuple local, a tuple-returning free function and method, and a
		// pair of structs. Regression: tuple-destructure-struct pass.
		{"destructure-struct-inline", "struct P { x: i32 } function main(): i32 { var (p, n) = (P { x: 40 }, 2); return p.x + n; }", 42, ""},
		{"destructure-struct-local", "struct P { x: i32 } function main(): i32 { var t = (P { x: 42 }, 1); var (p, n) = t; return p.x + n; }", 43, ""},
		{"destructure-struct-funcret", "struct P { x: i32 } function mk(): (P, i32) { return (P { x: 40 }, 2); } function main(): i32 { var (p, n) = mk(); return p.x + n; }", 42, ""},
		{"destructure-struct-methodret", "struct P { x: i32 } struct Maker { } function (m: Maker) build(): (P, i32) { return (P { x: 100 }, 5); } function main(): i32 { var mk = Maker { }; var (p, n) = mk.build(); return p.x + n; }", 105, ""},
		{"destructure-struct-both", "struct P { x: i32 } struct Q { y: i32 } function main(): i32 { var (p, q) = (P { x: 30 }, Q { y: 12 }); return p.x + q.y; }", 42, ""},

		// Non-capturing lambdas — a lambda value is a [table_idx] closure
		// box; calls lower to call_indirect through the function table.
		{"lambda-call", "function main(): i32 { var f = function(x: i32): i32 { return x * 2; }; print_int(f(5)); return 0; }", 0, "10"},
		{"lambda-two-args", "function main(): i32 { var add = function(a: i32, b: i32): i32 { return a + b; }; print_int(add(3, 4)); return 0; }", 0, "7"},
		{"lambda-zero-args", "function main(): i32 { var k = function(): i32 { return 42; }; print_int(k()); return 0; }", 0, "42"},
		{"lambda-called-twice", "function main(): i32 { var sq = function(x: i32): i32 { return x * x; }; print_int(sq(3)); print_int(sq(4)); return 0; }", 0, "916"},
		{"lambda-two-distinct", "function main(): i32 { var inc = function(x: i32): i32 { return x + 1; }; var dec = function(x: i32): i32 { return x - 1; }; print_int(inc(10)); print_int(dec(10)); return 0; }", 0, "119"},
		{"lambda-interleaved", "function main(): i32 { var a = function(x: i32): i32 { return x + 100; }; var b = function(x: i32): i32 { return x + 200; }; var c = function(x: i32): i32 { return x + 300; }; print_int(b(1)); print_int(a(1)); print_int(c(1)); return 0; }", 0, "201101301"},
		{"lambda-string-return", "function main(): i32 { var g = function(): string { return \"hi\"; }; write(g()); return 0; }", 0, "hi"},
		{"lambda-calls-toplevel", "function dbl(n: i32): i32 { return n * 2; } function main(): i32 { var f = function(x: i32): i32 { return dbl(x) + 1; }; print_int(f(5)); return 0; }", 0, "11"},
		{"lambda-as-fn-param", "function apply(g: fn, n: i32): i32 { return g(n); } function main(): i32 { print_int(apply(function(x: i32): i32 { return x * 3; }, 6)); return 0; }", 0, "18"},
		{"lambda-fn-param-twice", "function twice(g: fn, n: i32): i32 { return g(g(n)); } function main(): i32 { print_int(twice(function(x: i32): i32 { return x + 5; }, 0)); return 0; }", 0, "10"},
		{"lambda-prints-inside", "function main(): i32 { var f = function(): i32 { print_int(7); return 0; }; return f(); }", 0, "7"},

		// Capturing closures — free locals of the enclosing function are
		// copied into the closure box [idx, cap0, …]; the body loads them
		// from $__env.
		{"closure-capture-one", "function main(): i32 { var n: i32 = 10; var add = function(x: i32): i32 { return x + n; }; print_int(add(5)); return 0; }", 0, "15"},
		{"closure-capture-two", "function main(): i32 { var a: i32 = 3; var b: i32 = 4; var f = function(x: i32): i32 { return x * a + b; }; print_int(f(2)); return 0; }", 0, "10"},
		{"closure-capture-only", "function main(): i32 { var base: i32 = 100; var get = function(): i32 { return base; }; print_int(get()); return 0; }", 0, "100"},
		{"closure-capture-string", "function main(): i32 { var name: string = \"world\"; var greet = function(): string { return \"hi \" + name; }; write(greet()); return 0; }", 0, "hi world"},
		{"closure-capture-string-method", "function main(): i32 { var s: string = \"hello\"; var f = function(): i32 { return s.len(); }; print_int(f()); return 0; }", 0, "5"},
		// Capture is by REFERENCE: one shared cell, so the outer `n = 99` is
		// visible inside the closure. That is the designed semantics, not an
		// accident — closureconv.BoxMutatedCaptures boxes a captured-and-
		// assigned local into a 1-element cell precisely to match the
		// interpreter, "which defines the semantics" (#2896 for scalars, #5301
		// for pointers). This expectation was "1" while these modules lowered
		// through the LEGACY AST wasm backend, which snapshotted at make time;
		// they reach the IR path now, so it agrees with the oracle (#5479).
		{"closure-snapshot-value", "function main(): i32 { var n: i32 = 1; var f = function(): i32 { return n; }; n = 99; print_int(f()); return 0; }", 0, "99"},
		{"closure-passed-capturing", "function apply(g: fn): i32 { return g(); } function main(): i32 { var k: i32 = 42; print_int(apply(function(): i32 { return k + 1; })); return 0; }", 0, "43"},
		{"closure-capture-param", "function make(p: i32): i32 { var f = function(x: i32): i32 { return x + p; }; return f(10); } function main(): i32 { print_int(make(5)); return 0; }", 0, "15"},
		// The by-reference counterpart in a loop: `add` reads the LIVE total
		// each call, so this runs 0+1=1, 1+2=3, 3+4=7 -> 11, matching the
		// oracle. It was "6" (add always seeing total == 0, summing 1+2+3)
		// while these modules lowered through the legacy AST wasm backend's
		// make-time snapshot; they reach the IR path now (#5479).
		{"closure-capture-in-loop", "function main(): i32 { var total: i32 = 0; var add = function(x: i32): i32 { return x + total; }; var i: i32 = 1; while (i <= 3) { total = total + add(i); i = i + 1; } print_int(total); return 0; }", 0, "11"},
		// Capturing a *struct* into a lambda must keep its struct type so a
		// `cap.field` read resolves the field offset (it previously emitted
		// a bogus `(i32.const 0)` because the capture wasn't sv-typed in the
		// lambda; the method-receiver sub-case also needs the receiver in
		// the capture set). Regression: struct-capture pass.
		{"closure-capture-struct-local", "struct Point { x: i32, y: i32 } function main(): i32 { var p = Point { x: 30, y: 12 }; var f = function(): i32 { return p.x + p.y; }; return f(); }", 42, ""},
		{"closure-capture-struct-two-fields", "struct Cfg { mult: i32, add: i32 } function main(): i32 { var c = Cfg { mult: 3, add: 1 }; var f = function(x: i32): i32 { return x * c.mult + c.add; }; return f(10); }", 31, ""},
		{"closure-capture-struct-returned", "struct Box { v: i32 } function wrap(b: Box): (i32) => i32 { return function(x: i32): i32 { return x + b.v; }; } function main(): i32 { var b = Box { v: 40 }; var f = wrap(b); return f(2); }", 42, ""},
		{"closure-capture-struct-receiver", "struct Adder { base: i32 } function (a: Adder) make(): (i32) => i32 { return function(x: i32): i32 { return x + a.base; }; } function main(): i32 { var a = Adder { base: 100 }; var f = a.make(); return f(5); }", 105, ""},

		// Returned closures — a `var f = g(...)` bound to a call of a
		// function whose return type is `fn` must go through call_indirect,
		// not a direct `(call $f …)` to a nonexistent function (regression:
		// harden10).
		{"return-closure-capture", "function make_adder(n: i32): fn { return function(x: i32): i32 { return x + n; }; } function main(): i32 { var add5 = make_adder(5); return add5(37); }", 42, ""},
		{"return-closure-noncap", "function get_const(): fn { return function(): i32 { return 99; }; } function main(): i32 { var f = get_const(); return f(); }", 99, ""},
		{"return-closure-twice", "function adder(n: i32): fn { return function(x: i32): i32 { return x + n; }; } function main(): i32 { var a = adder(10); var b = adder(20); return a(1) + b(2); }", 33, ""},
		{"return-closure-string", "function greeter(): fn { return function(): string { return \"hi\"; }; } function main(): i32 { var g = greeter(); write(g()); return 0; }", 0, "hi"},
		// A *method* returning a closure must also flow through
		// call_indirect: `var f = obj.m()` where m returns `fn` (regression:
		// harden12; the earlier fix only recognised free-function calls).
		// The precise `(i32) => i32` return spelling (which the Go compiler
		// requires and the self-host coarsens to `fn`) is used here.
		{"method-return-closure-noncap", "struct Maker { } function (m: Maker) make(): (i32) => i32 { return function(x: i32): i32 { return x * 2; }; } function main(): i32 { var m = Maker { }; var f = m.make(); return f(21); }", 42, ""},
		{"method-return-closure-capture-local", "struct Adder { base: i32 } function (a: Adder) make(): (i32) => i32 { var b = a.base; return function(x: i32): i32 { return x + b; }; } function main(): i32 { var a = Adder { base: 100 }; var f = a.make(); return f(5); }", 105, ""},
		{"method-return-closure-capture-param", "struct F { } function (f: F) mul(k: i32): (i32) => i32 { return function(x: i32): i32 { return x * k; }; } function main(): i32 { var f = F { }; var g = f.mul(7); return g(6); }", 42, ""},

		// `.to_string()` (integer→string runtime) + f-strings (which the
		// parser desugars to `"…" + (expr).to_string() + …`).
		{"tostring-i32", "function main(): i32 { var n: i32 = 42; write(n.to_string()); return 0; }", 0, "42"},
		{"tostring-zero", "function main(): i32 { write((0).to_string()); return 0; }", 0, "0"},
		{"tostring-negative", "function main(): i32 { var n: i32 = 0 - 17; write(n.to_string()); return 0; }", 0, "-17"},
		{"tostring-concat", "function main(): i32 { var n: i32 = 5; write(\"n=\" + n.to_string()); return 0; }", 0, "n=5"},
		{"tostring-string-identity", "function main(): i32 { var s: string = \"hi\"; write(s.to_string()); return 0; }", 0, "hi"},
		{"tostring-i64", "function main(): i32 { var b: i64 = 5000000000; write(b.to_string()); return 0; }", 0, "5000000000"},
		// A struct/enum receiver with its own `to_string` (hand-written or
		// `@derive(Display)`) dispatches to that method rather than the
		// integer formatter — the `to_string` intrinsic now defers to a
		// user method when one exists. See docs/TRAITS.md.
		{"tostring-struct-method", "struct P { x: i32 } function (p: P) to_string(): string { return \"box\"; } function main(): i32 { var p = P { x: 1 }; write(p.to_string()); return 0; }", 0, "box"},
		{"derive-display-struct", "@derive(Display) struct P { x: i32, y: i32 } function main(): i32 { var p: P = P { x: 3, y: 7 }; write(p.to_string()); return 0; }", 0, "P { x: 3, y: 7 }"},
		{"derive-display-nested", "@derive(Display) struct Inner { n: i32 } @derive(Display) struct Outer { a: Inner, tag: string } function main(): i32 { var p: Outer = Outer { a: Inner { n: 5 }, tag: \"hi\" }; write(p.to_string()); return 0; }", 0, "Outer { a: Inner { n: 5 }, tag: hi }"},
		// User-defined enums: positional variant construction (`Circle(3)`),
		// unit variants (`Nil` as a bare ident), and `match` binding the
		// payload — previously only Option/Result were special-cased, so a
		// `$Circle` call was undefined. See docs/TRAITS.md.
		{"enum-construct-match", "enum Shape { Circle(i32), Square(i32) } function main(): i32 { var a: Shape = Circle(3); match (a) { Circle(r) => { return r * r * 3; }, Square(w) => { return w * w; } } return 0; }", 27, ""},
		{"enum-second-variant", "enum Shape { Circle(i32), Square(i32) } function main(): i32 { var a: Shape = Square(4); match (a) { Circle(r) => { return r * r * 3; }, Square(w) => { return w * w; } } return 0; }", 16, ""},
		{"enum-unit-variant", "enum Opt { Has(i32), Nil } function main(): i32 { var n: Opt = Nil; match (n) { Has(v) => { return v; }, Nil => { return 9; } } return 0; }", 9, ""},
		// Wide enum payload slots (S1): a single i64 / f64 variant payload
		// stores + binds at full width (8-byte slot), so a value above 2^31 (or
		// a float's fractional bits) round-trips — a 4-byte i32 slot would
		// truncate it. The bound `x` is a 64-bit local (declared i64/f64,
		// recognised by is_i64_expr / is_f64_expr).
		{"enum-i64-payload", "enum Box { Big(i64) } function main(): i32 { var b: Box = Big(5000000000); match (b) { Big(x) => { if (x == 5000000000) { return 42; } return 1; } } return 0; }", 42, ""},
		{"enum-i64-payload-arith", "enum Box { Big(i64) } function main(): i32 { var b: Box = Big(4200000000); match (b) { Big(x) => { return (x / 100000000) as i32; } } return 0; }", 42, ""},
		{"enum-f64-payload", "enum FBox { F(f64) } function main(): i32 { var b: FBox = F(3.5); match (b) { F(x) => { return (x * 4.0) as i32; } } return 0; }", 14, ""},
		// Multi-payload variants (S4): a case carries >1 payload, stored at
		// successive 4-byte slots and bound by a multi-binding match `V(a, b, …)`.
		// The parser keeps every payload (`__ev`, `__ev1`, …); the checker's E015
		// allows the matching arity; codegen binds each field at struct_field_off(j).
		{"enum-multi-payload-2", "enum Ev { Pair(i32, i32), Single(i32), Stop } function main(): i32 { var p: Ev = Pair(3, 4); match (p) { Pair(a, b) => { return a * 10 + b; }, Single(x) => { return x; }, Stop => { return 0; } } return 0; }", 34, ""},
		{"enum-multi-payload-3", "enum T3 { Tri(i32, i32, i32), Z } function main(): i32 { var t: T3 = Tri(1, 2, 3); match (t) { Tri(a, b, c) => { return a * 100 + b * 10 + c; }, Z => { return 0; } } return 0; }", 123, ""},
		{"enum-multi-payload-second-arm", "enum Ev { Pair(i32, i32), Single(i32), Stop } function main(): i32 { var p: Ev = Single(9); match (p) { Pair(a, b) => { return a + b; }, Single(x) => { return x * 5; }, Stop => { return 0; } } return 0; }", 45, ""},
		// Enum receiver method — dispatches via the enum type (a var typed
		// `Shape` now records `Shape`, so its methods resolve).
		{"enum-method", "enum Shape { Circle(i32), Square(i32) } function (s: Shape) area(): i32 { match (s) { Circle(r) => { return r * r * 3; }, Square(w) => { return w * w; } } } function main(): i32 { var a: Shape = Circle(3); var b: Shape = Square(4); return a.area() + b.area(); }", 43, ""},
		// `@derive(Display)` on an enum: `Variant(payload)` / `Variant`.
		{"enum-derive-display-payload", "@derive(Display) enum Opt { Has(i32), Nil } function main(): i32 { var h: Opt = Has(7); write(h.to_string()); return 0; }", 0, "Has(7)"},
		{"enum-derive-display-unit", "@derive(Display) enum Opt { Has(i32), Nil } function main(): i32 { var n: Opt = Nil; write(n.to_string()); return 0; }", 0, "Nil"},
		// Primitive-receiver user methods: `self.x.eq(other.x)` on an i32
		// field/payload dispatches to `impl Eq for i32` ($i32__eq) — the
		// receiver isn't a struct, so without this it fell back to 0.
		// Enables `@derive(Eq/Ord)` on structs (and var-typed enums) on
		// wasm. See docs/TRAITS.md.
		{"derive-eq-struct", "trait Eq { function eq(self: Self, other: Self): boolean; } impl Eq for i32 { function eq(self: Self, other: Self): boolean { return self == other; } } @derive(Eq) struct P { x: i32, y: i32 } function main(): i32 { var a: P = P { x: 1, y: 2 }; var b: P = P { x: 1, y: 2 }; var c: P = P { x: 1, y: 9 }; var r: i32 = 0; if (a.eq(b)) { r = r + 1; } if (!a.eq(c)) { r = r + 2; } return r; }", 3, ""},
		{"derive-ord-struct", "trait Ord { function cmp(self: Self, other: Self): i32; } impl Ord for i32 { function cmp(self: Self, other: Self): i32 { if (self < other) { return 0 - 1; } if (self > other) { return 1; } return 0; } } @derive(Ord) struct P { x: i32, y: i32 } function main(): i32 { var a: P = P { x: 1, y: 2 }; var c: P = P { x: 1, y: 9 }; if (a.cmp(c) < 0) { if (c.cmp(a) > 0) { if (a.cmp(a) == 0) { return 42; } } } return 0; }", 42, ""},
		{"derive-eq-enum", "trait Eq { function eq(self: Self, other: Self): boolean; } impl Eq for i32 { function eq(self: Self, other: Self): boolean { return self == other; } } @derive(Eq) enum Opt { Has(i32), Nil } function main(): i32 { var a: Opt = Has(5); var a2: Opt = Has(5); var b: Opt = Has(6); var n: Opt = Nil; var n2: Opt = Nil; var r: i32 = 0; if (a.eq(a2)) { r = r + 1; } if (!a.eq(b)) { r = r + 2; } if (!a.eq(n)) { r = r + 4; } if (n.eq(n2)) { r = r + 8; } return r; }", 15, ""},
		// `@derive(Ord)` on an enum (variant order then payload), via the
		// var-typed receiver form.
		{"derive-ord-enum", "trait Ord { function cmp(self: Self, other: Self): i32; } impl Ord for i32 { function cmp(self: Self, other: Self): i32 { if (self < other) { return 0 - 1; } if (self > other) { return 1; } return 0; } } @derive(Ord) enum Lvl { Low(i32), High } function main(): i32 { var a: Lvl = Low(1); var a2: Lvl = Low(2); var h: Lvl = High; var lo: Lvl = Low(0); var a3: Lvl = Low(1); var r: i32 = 0; if (a.cmp(a2) < 0) { r = r + 1; } if (a.cmp(h) < 0) { r = r + 2; } if (h.cmp(lo) > 0) { r = r + 4; } if (a.cmp(a3) == 0) { r = r + 8; } return r; }", 15, ""},
		// INLINE variant-call receivers (`Has(5).eq(…)`, `Nil.eq(…)`,
		// `Low(1).cmp(…)`, `Circle(3).area()`) — previously the one wasm
		// gap: struct_type_of couldn't recover the enum from a bare variant
		// constructor, so dispatch fell to the i32 path (pointer compare).
		// enum_of_variant now maps the variant to its enum via the enum
		// methods' match arms, so inline receivers dispatch statically.
		{"inline-variant-eq", "trait Eq { function eq(self: Self, other: Self): boolean; } impl Eq for i32 { function eq(self: Self, other: Self): boolean { return self == other; } } @derive(Eq) enum Opt { Has(i32), Nil } function main(): i32 { var r: i32 = 0; if (Has(5).eq(Has(5))) { r = r + 1; } if (!Has(5).eq(Has(6))) { r = r + 2; } if (!Has(5).eq(Nil)) { r = r + 4; } if (Nil.eq(Nil)) { r = r + 8; } return r; }", 15, ""},
		{"inline-variant-display", "@derive(Display) enum Opt { Has(i32), Nil } function main(): i32 { write(Has(7).to_string()); write(\"|\"); write(Nil.to_string()); return 0; }", 0, "Has(7)|Nil"},
		{"inline-variant-ord", "trait Ord { function cmp(self: Self, other: Self): i32; } impl Ord for i32 { function cmp(self: Self, other: Self): i32 { if (self < other) { return 0 - 1; } if (self > other) { return 1; } return 0; } } @derive(Ord) enum Lvl { Low(i32), High } function main(): i32 { var r: i32 = 0; if (Low(1).cmp(Low(2)) < 0) { r = r + 1; } if (Low(9).cmp(High) < 0) { r = r + 2; } if (High.cmp(Low(0)) > 0) { r = r + 4; } if (Low(3).cmp(Low(3)) == 0) { r = r + 8; } return r; }", 15, ""},
		{"inline-enum-method", "enum Shape { Circle(i32), Square(i32) } function (s: Shape) area(): i32 { match (s) { Circle(r) => { return r * r * 3; }, Square(w) => { return w * w; } } } function main(): i32 { return Circle(3).area() + Square(4).area(); }", 43, ""},
		// `dyn Trait` — a trait object whose concrete type varies at runtime.
		// The wasm backend is static-dispatch, so `emit_dyn_dispatch` reads
		// the receiver's runtime struct id (the tag at offset 0) and branches
		// to the matching `$Struct__method`, over every struct that
		// implements the method (mirroring the native runtime shape-compare).
		// A heterogeneous `dyn Shape[]` summed via a loop: 3*3 + 2*5 = 19.
		{"dyn-object-heterogeneous", "trait Shape { function area(self: Self): i32; } struct Circle { r: i32 } struct Rect { w: i32, h: i32 } impl Shape for Circle { function area(self: Self): i32 { return self.r * self.r; } } impl Shape for Rect { function area(self: Self): i32 { return self.w * self.h; } } function sum(xs: dyn Shape[]): i32 { var t: i32 = 0; for x in xs { t = t + x.area(); } return t; } function main(): i32 { var xs: dyn Shape[] = [Circle { r: 3 }, Rect { w: 2, h: 5 }]; return sum(xs); }", 19, ""},
		// A single `dyn Shape` parameter dispatches the same way: 4*4 + 3*6 = 34.
		{"dyn-object-param", "trait Shape { function area(self: Self): i32; } struct Circle { r: i32 } struct Rect { w: i32, h: i32 } impl Shape for Circle { function area(self: Self): i32 { return self.r * self.r; } } impl Shape for Rect { function area(self: Self): i32 { return self.w * self.h; } } function describe(s: dyn Shape): i32 { return s.area(); } function main(): i32 { var c: dyn Shape = Circle { r: 4 }; var r: dyn Shape = Rect { w: 3, h: 6 }; return describe(c) + describe(r); }", 34, ""},
		// A `dyn Trait[]` of unit structs with a string-returning method —
		// guards that the heterogeneous loop var stays on the runtime path
		// (a struct-literal initializer must NOT pin it to the first variant).
		{"dyn-object-string-method", "trait Named { function name(self: Self): string; } struct Cat { } struct Dog { } impl Named for Cat { function name(self: Self): string { return \"cat\"; } } impl Named for Dog { function name(self: Self): string { return \"dog\"; } } function main(): i32 { var xs: dyn Named[] = [Cat {}, Dog {}, Cat {}]; for x in xs { write(x.name()); } return 0; }", 0, "catdogcat"},
		// Generic-struct monomorphisation reaches the wasm backend through
		// the shared module_with_builtins pass: `Box[i32]` / `Box[string]`
		// become concrete `Box__i32` / `Box__string` clones, and wasm's
		// static dispatch (struct_type_of -> $Box__i32__to_string) routes
		// each to its own helper. Both clones coexist with a shared `v`.
		{"generic-struct-display-i32", "@derive(Display) struct Box[T] { v: T } function main(): i32 { var b: Box[i32] = Box { v: 5 }; write(b.to_string()); return 0; }", 0, "Box { v: 5 }"},
		{"generic-struct-display-string", "@derive(Display) struct Box[T] { v: T } function main(): i32 { var b: Box[string] = Box { v: \"hi\" }; write(b.to_string()); return 0; }", 0, "Box { v: hi }"},
		{"generic-struct-display-both", "@derive(Display) struct Box[T] { v: T } function main(): i32 { var a: Box[i32] = Box { v: 5 }; var b: Box[string] = Box { v: \"hi\" }; write(a.to_string()); write(\"|\"); write(b.to_string()); return 0; }", 0, "Box { v: 5 }|Box { v: hi }"},
		{"generic-struct-derive-eq", "trait Eq { function eq(self: Self, other: Self): boolean; } impl Eq for i32 { function eq(self: Self, other: Self): boolean { return self == other; } } @derive(Eq) struct Box[T] { v: T } function main(): i32 { var a: Box[i32] = Box { v: 5 }; var b: Box[i32] = Box { v: 5 }; var c: Box[i32] = Box { v: 9 }; var r: i32 = 0; if (a.eq(b)) { r = r + 1; } if (!a.eq(c)) { r = r + 2; } return r; }", 3, ""},
		// Parametric `impl[T: Show] Show for Box[T]` cloned per concrete T:
		// `Box__i32` dispatches `self.v.show()` to `impl Show for i32`,
		// `Box__string` to `impl Show for string`.
		{"generic-struct-parametric-impl", "trait Show { function show(self: Self): string; } impl Show for i32 { function show(self: Self): string { return self.to_string(); } } impl Show for string { function show(self: Self): string { return self; } } struct Box[T] { v: T } impl[T: Show] Show for Box[T] { function show(self: Self): string { return \"Box(\" + self.v.show() + \")\"; } } function main(): i32 { var a: Box[i32] = Box { v: 7 }; var b: Box[string] = Box { v: \"hi\" }; write(a.show()); write(\"|\"); write(b.show()); return 0; }", 0, "Box(7)|Box(hi)"},
		{"tostring-expr", "function main(): i32 { write((3 * 14).to_string()); return 0; }", 0, "42"},
		{"fstring-int", "function main(): i32 { var n: i32 = 42; write(f\"n is {n}!\"); return 0; }", 0, "n is 42!"},
		{"fstring-two", "function main(): i32 { var a: i32 = 3; var b: i32 = 4; write(f\"{a}+{b}={a + b}\"); return 0; }", 0, "3+4=7"},
		{"fstring-string-interp", "function main(): i32 { var who: string = \"world\"; write(f\"hello {who}\"); return 0; }", 0, "hello world"},
		{"fstring-only-interp", "function main(): i32 { var n: i32 = 9; write(f\"{n}\"); return 0; }", 0, "9"},

		// Integration capstone: a word-frequency counter combining split,
		// a string-keyed i32-valued map, get_or accumulation, len, and an
		// f-string — exercising many features together.
		{"integration-word-count", "function main(): i32 { var text: string = \"the cat sat on the mat the cat ran\"; var words: string[] = text.split(\" \"); var counts = map_new(8); var i: i32 = 0; while (i < words.len()) { var w: string = words[i]; counts = counts.insert(w, counts.get_or(w, 0) + 1); i = i + 1; } print_int(counts.get_or(\"the\", 0)); print_int(counts.get_or(\"cat\", 0)); print_int(counts.get_or(\"mat\", 0)); write(f\" total={counts.len()}\"); return 0; }", 0, "321 total=6"},
		// Higher-order: a reduce over an array taking an `fn` param, with a
		// plain lambda and a capturing closure (factor), reported via f-string.
		{"integration-reduce-closure", "function reduce(xs: i32[], init: i32, f: fn): i32 { var acc: i32 = init; var i: i32 = 0; while (i < xs.len()) { acc = f(acc, xs[i]); i = i + 1; } return acc; } function main(): i32 { var xs = [1, 2, 3, 4, 5]; var factor: i32 = 10; var sum = reduce(xs, 0, function(a: i32, b: i32): i32 { return a + b; }); var scaled = reduce(xs, 0, function(a: i32, b: i32): i32 { return a + b * factor; }); write(f\"sum={sum} scaled={scaled}\"); return 0; }", 0, "sum=15 scaled=150"},
		// Structs + methods + array-of-structs + for-in + f-string.
		{"integration-struct-method", "struct Pt { x: i32, y: i32 } function (p: Pt) dist2(): i32 { return p.x * p.x + p.y * p.y; } function main(): i32 { var pts = [Pt { x: 3, y: 4 }, Pt { x: 1, y: 1 }]; var total: i32 = 0; for p in pts { total = total + p.dist2(); } write(f\"total={total}\"); return 0; }", 0, "total=27"},
		// Struct-array indexing: pts[i].field resolves the element struct type.
		{"struct-array-index", "struct Pt { x: i32, y: i32 } function main(): i32 { var pts = [Pt { x: 5, y: 6 }, Pt { x: 7, y: 8 }]; print_int(pts[0].x); print_int(pts[1].y); return 0; }", 0, "58"},

		// Hardening pass 2: feature combinations from real programs.
		{"nested-struct", "struct Inner { v: i32 } struct Outer { inner: Inner, name: string } function main(): i32 { var o = Outer { inner: Inner { v: 7 }, name: \"hi\" }; print_int(o.inner.v); write(o.name); return 0; }", 0, "7hi"},
		{"struct-array-field", "struct Bag { items: i32[] } function main(): i32 { var b = Bag { items: [10, 20, 30] }; print_int(b.items.len()); print_int(b.items[1]); return 0; }", 0, "320"},
		// A struct field whose type is a *struct* array: indexing it and
		// reading the element's field (`bag.items[i].x`) must resolve the
		// element struct type (it previously emitted a bogus (i32.const 0)).
		// Also covers a recursive struct (a node holding a child array).
		{"struct-array-struct-field", "struct Pt { x: i32, y: i32 } struct Bag { items: Pt[] } function main(): i32 { var b = Bag { items: [Pt { x: 5, y: 6 }, Pt { x: 7, y: 8 }] }; print_int(b.items[0].x); print_int(b.items[1].y); return 0; }", 0, "58"},
		{"struct-array-field-recursive", "struct SExpr { kind: i32, items: SExpr[] } function main(): i32 { var leaf = SExpr { kind: 2, items: [] }; var lst = SExpr { kind: 0, items: [leaf, leaf] }; return lst.items.len() + lst.items[0].kind; }", 4, ""},
		// A struct-array *param* (`ts: Tk[]`) is a struct-array local too, so
		// `ts[i].field` and `for t in ts { … t.field … }` resolve the element
		// struct type (it previously read a bogus (i32.const 0)).
		{"struct-array-param-index", "struct Tk { kind: i32, text: string } function first(ts: Tk[]): i32 { return ts[0].kind; } function main(): i32 { var ts = [Tk { kind: 5, text: \"a\" }, Tk { kind: 9, text: \"b\" }]; print_int(first(ts)); return 0; }", 0, "5"},
		{"struct-array-param-for", "struct Tk { kind: i32, text: string } function sumk(ts: Tk[]): i32 { var s = 0; for t in ts { s = s + t.kind; } return s; } function main(): i32 { var ts = [Tk { kind: 5, text: \"a\" }, Tk { kind: 9, text: \"b\" }]; print_int(sumk(ts)); return 0; }", 0, "14"},
		{"struct-string-field", "struct P { name: string, age: i32 } function main(): i32 { var p = P { name: \"sam\", age: 30 }; write(f\"{p.name} is {p.age}\"); return 0; }", 0, "sam is 30"},
		{"fn-returns-struct", "struct V { a: i32, b: i32 } function mk(n: i32): V { return V { a: n, b: n * 2 }; } function main(): i32 { var v = mk(5); print_int(v.a + v.b); return 0; }", 0, "15"},
		{"recursion-fib", "function fib(n: i32): i32 { if (n < 2) { return n; } return fib(n - 1) + fib(n - 2); } function main(): i32 { print_int(fib(10)); return 0; }", 0, "55"},
		{"mutual-recursion", "function ev(n: i32): boolean { if (n == 0) { return true; } return od(n - 1); } function od(n: i32): boolean { if (n == 0) { return false; } return ev(n - 1); } function main(): i32 { if (ev(10)) { print_int(1); } else { print_int(0); } return 0; }", 0, "1"},
		{"option-question-chain", "function lookup(k: i32): Option[i32] { if (k > 0) { return Some(k * 10); } return None; } function step(k: i32): Option[i32] { var v = lookup(k)?; return Some(v + 1); } function main(): i32 { match (step(5)) { Some(r) => { print_int(r); }, None => { print_int(0); } } match (step(0 - 1)) { Some(r) => { print_int(r); }, None => { print_int(99); } } return 0; }", 0, "5199"},
		{"result-match-string", "function parse(ok: boolean): Result[i32, string] { if (ok) { return Ok(42); } return Err(\"bad input\"); } function main(): i32 { match (parse(false)) { Ok(v) => { print_int(v); }, Err(e) => { write(e); } } return 0; }", 0, "bad input"},
		{"string-builder-loop", "function main(): i32 { var s: string = \"\"; var i: i32 = 0; while (i < 4) { s = s + f\"[{i}]\"; i = i + 1; } write(s); print_int(s.split(\"]\").len()); return 0; }", 0, "[0][1][2][3]5"},

		// Hardening pass 3: more real-program shapes.
		{"struct-update-spread", "struct C { r: i32, g: i32, b: i32 } function main(): i32 { var base = C { r: 1, g: 2, b: 3 }; var c2 = C { ...base, g: 99 }; print_int(c2.r); print_int(c2.g); print_int(c2.b); return 0; }", 0, "1993"},
		{"union-match-method", "struct Circle { r: i32 } struct Square { s: i32 } type Shape = Circle | Square; function (c: Circle) area(): i32 { return c.r * c.r * 3; } function (sq: Square) area(): i32 { return sq.s * sq.s; } function describe(sh: Shape): i32 { match (sh) { Circle(c) => { return c.area(); }, Square(s) => { return s.area(); } } return 0; } function main(): i32 { print_int(describe(Circle { r: 2 })); print_int(describe(Square { s: 5 })); return 0; }", 0, "1225"},
		{"closure-captures-array", "function main(): i32 { var xs = [10, 20, 30]; var get = function(i: i32): i32 { return xs[i]; }; print_int(get(0) + get(2)); return 0; }", 0, "40"},
		{"array-2d", "function main(): i32 { var grid = [[1, 2], [3, 4]]; print_int(grid[0][1]); print_int(grid[1][0]); return 0; }", 0, "23"},
		// Nested-array *type annotations* `i32[][]` must parse (the second
		// `[]` was left on the cursor, dropping the `var` binding) — the
		// literal + iteration already worked unannotated (regression:
		// nested-array parser fix).
		{"nested-array-annotated", "function main(): i32 { var grid: i32[][] = [[1, 2], [3, 4], [5, 6]]; var sum = 0; for row in grid { for v in row { sum = sum + v; } } return sum; }", 21, ""},
		{"nested-array-triple-annotated", "function main(): i32 { var cube: i32[][][] = [[[1]], [[2, 3]]]; var sum = 0; for plane in cube { for row in plane { for v in row { sum = sum + v; } } } return sum; }", 6, ""},
		{"nested-option", "function f(b: boolean): Option[Option[i32]] { if (b) { return Some(Some(5)); } return Some(None); } function main(): i32 { match (f(true)) { Some(inner) => { match (inner) { Some(v) => { print_int(v); }, None => { print_int(0); } } }, None => { print_int(9); } } return 0; }", 0, "5"},
		{"recursion-string", "function rep(s: string, n: i32): string { if (n <= 0) { return \"\"; } return s + rep(s, n - 1); } function main(): i32 { write(rep(\"ab\", 3)); return 0; }", 0, "ababab"},
		{"split-join-roundtrip", "function main(): i32 { var parts = \"a,b,c\".split(\",\"); write(parts.join(\"-\")); print_int(parts.len()); return 0; }", 0, "a-b-c3"},
		{"nested-loop-break", "function main(): i32 { var c: i32 = 0; var i: i32 = 0; while (i < 5) { var j: i32 = 0; while (j < 5) { if (j == 3) { break; } c = c + 1; j = j + 1; } i = i + 1; } print_int(c); return 0; }", 0, "15"},
		{"tuple-destructure-call", "function mm(): (i32, i32) { return (3, 7); } function main(): i32 { var (a, b) = mm(); print_int(a); print_int(b); return 0; }", 0, "37"},
		{"tuple-destructure-literal", "function main(): i32 { var (x, y) = (11, 22); print_int(x + y); return 0; }", 0, "33"},

		// Hardening pass 4: const declarations (the parser desugars a const
		// to a zero-arg function; a bare reference lowers to a call, with
		// the const's return type driving string / i64 / f64 typing).
		{"const-i32", "const LIMIT: i32 = 100; function main(): i32 { print_int(LIMIT + 1); return 0; }", 0, "101"},
		{"const-string-fstring", "const NAME: string = \"bob\"; function main(): i32 { write(f\"hello {NAME}\"); return 0; }", 0, "hello bob"},
		{"const-i64", "const BIG: i64 = 5000000000; function main(): i32 { print_int(BIG + 1); return 0; }", 0, "5000000001"},
		{"const-f64", "const HALF: f64 = 3.5; function main(): i32 { print_int((HALF * 2.0) as i32); return 0; }", 0, "7"},
		{"const-shadowed-by-local", "const X: i32 = 5; function main(): i32 { var X: i32 = 99; print_int(X); return 0; }", 0, "99"},
		// More combinations (all already worked; locked in as regressions).
		{"continue-while", "function main(): i32 { var s: i32 = 0; var i: i32 = 0; while (i < 10) { i = i + 1; if (i % 2 == 0) { continue; } s = s + i; } print_int(s); return 0; }", 0, "25"},
		{"push-loop", "function main(): i32 { var xs: i32[] = []; var i: i32 = 0; while (i < 5) { xs = xs.append(i * i); i = i + 1; } var s: i32 = 0; for v in xs { s = s + v; } print_int(s); return 0; }", 0, "30"},
		{"nested-struct-method", "struct Inner { v: i32 } function (n: Inner) dbl(): i32 { return n.v * 2; } struct Outer { inner: Inner } function main(): i32 { var o = Outer { inner: Inner { v: 7 } }; print_int(o.inner.dbl()); return 0; }", 0, "14"},
		{"wildcard-match", "function main(): i32 { var m = Map { 1: 10 }; match (m.get(2)) { Some(v) => { print_int(v); }, _ => { print_int(99); } } return 0; }", 0, "99"},
		{"string-compare", "function main(): i32 { if (\"apple\" < \"banana\") { print_int(1); } if (\"zebra\" > \"ant\") { print_int(2); } return 0; }", 0, "12"},
		{"fstring-method-interp", "function main(): i32 { var s: string = \"hello\"; write(f\"upper={s.to_ascii_upper()}\"); return 0; }", 0, "upper=HELLO"},
		{"array-of-tuples", "function main(): i32 { var ps = [(1, 2), (3, 4)]; var t = ps[1]; print_int(t.0 + t.1); return 0; }", 0, "7"},
		// A `(T, U)[]` *annotation* must parse: the parenthesized tuple type
		// followed by `[]` previously left the `[]` on the cursor, so the
		// `var`'s local was never declared ("unknown local"). Now the
		// trailing `[]` is consumed.
		{"tuple-array-annotated", "function main(): i32 { var ps: (i32, i32)[] = [(1, 2), (3, 4)]; var t = ps[1]; return t.0 + t.1; }", 7, ""},
		{"tuple-array-for", "function main(): i32 { var ps: (i32, i32)[] = [(10, 20), (30, 40)]; var s: i32 = 0; for p in ps { s = s + p.0; } return s; }", 40, ""},
		// Closure arrays `(() => R)[]`: a closure read from an `fn[]` element
		// (`var c = fns[i]` / `for f in fns`) is itself callable via
		// call_indirect. (Needs the paren-fn-type parse + fn[]-element typing.)
		{"closure-array-call", "function mk(s: i32): () => i32 { return function(): i32 { return s; }; } function main(): i32 { var fns: (() => i32)[] = [mk(42), mk(9)]; var c = fns[0]; return c(); }", 42, ""},
		{"closure-array-for", "function mk(s: i32): () => i32 { return function(): i32 { return s; }; } function main(): i32 { var fns: (() => i32)[] = [mk(10), mk(20), mk(12)]; var s: i32 = 0; for f in fns { s = s + f(); } return s; }", 42, ""},
		{"closure-array-arg", "function adder(n: i32): (i32) => i32 { return function(x: i32): i32 { return x + n; }; } function main(): i32 { var fs: ((i32) => i32)[] = [adder(10), adder(20)]; var g = fs[1]; return g(22); }", 42, ""},
		{"method-chain-struct", "struct Acc { total: i32 } function (a: Acc) add(n: i32): Acc { return Acc { total: a.total + n }; } function main(): i32 { var r = Acc { total: 0 }.add(5).add(10).add(20); print_int(r.total); return 0; }", 0, "35"},
		{"early-return-loop", "function find(xs: i32[], target: i32): i32 { var i: i32 = 0; while (i < xs.len()) { if (xs[i] == target) { return i; } i = i + 1; } return 0 - 1; } function main(): i32 { print_int(find([5, 10, 15, 20], 15)); print_int(find([1, 2], 9)); return 0; }", 0, "2-1"},

		// Hardening pass 5: string char-access s[i] (byte load) + more.
		{"string-char-access", "function main(): i32 { var s: string = \"ABC\"; var sum: i32 = 0; var i: i32 = 0; while (i < s.len()) { sum = sum + (s[i] as i32); i = i + 1; } print_int(sum); return 0; }", 0, "198"},
		{"string-char-compare", "function main(): i32 { var s: string = \"a1b2\"; var digits: i32 = 0; var i: i32 = 0; while (i < s.len()) { if (s[i] >= 48 && s[i] <= 57) { digits = digits + 1; } i = i + 1; } print_int(digits); return 0; }", 0, "2"},
		{"elseif-chain", "function grade(n: i32): string { if (n >= 90) { return \"A\"; } else if (n >= 80) { return \"B\"; } else if (n >= 70) { return \"C\"; } else { return \"F\"; } } function main(): i32 { write(grade(95)); write(grade(85)); write(grade(50)); return 0; }", 0, "ABF"},
		{"three-variant-union", "struct A { v: i32 } struct B { v: i32 } struct C { v: i32 } type T = A | B | C; function f(t: T): i32 { match (t) { A(a) => { return a.v + 1; }, B(b) => { return b.v + 2; }, C(c) => { return c.v + 3; } } return 0; } function main(): i32 { print_int(f(A { v: 10 })); print_int(f(B { v: 10 })); print_int(f(C { v: 10 })); return 0; }", 0, "111213"},
		{"struct-mutate-via-fn", "struct Counter { n: i32 } function bump(c: Counter): i32 { c.n = c.n + 1; return 0; } function main(): i32 { var c = Counter { n: 5 }; bump(c); bump(c); print_int(c.n); return 0; }", 0, "7"},
		{"array-elem-field-set", "struct Pt { x: i32, y: i32 } function main(): i32 { var pts = [Pt { x: 1, y: 2 }, Pt { x: 3, y: 4 }]; pts[0].x = 99; print_int(pts[0].x); print_int(pts[1].x); return 0; }", 0, "993"},
		{"string-methods-combo", "function main(): i32 { var s: string = \"hello world\"; if (s.starts_with(\"hello\")) { print_int(1); } if (s.contains(\"o w\")) { print_int(2); } print_int(s.index_of(\"world\")); return 0; }", 0, "126"},

		// Hardening pass 6: bitwise operators (i32 + i64).
		{"bitwise-i32", "function main(): i32 { print_int(12 & 10); print_int(12 | 10); print_int(12 ^ 10); print_int(5 << 2); print_int(40 >> 2); return 0; }", 0, "81462010"},
		{"bitwise-i64", "function main(): i32 { var a: i64 = 12; var b: i64 = 10; print_int((a & b) as i32); print_int((a << 2) as i32); return 0; }", 0, "848"},
		// Generics with explicit type args: call site, construction, and a
		// generic receiver type (`Box[T]` / `Box[i32]` strip to `Box`).
		{"generic-call-typearg", "function identity[T](x: T): T { return x; } function main(): i32 { print_int(identity[i32](7)); return 0; }", 0, "7"},
		{"generic-struct-construct", "struct Box[T] { val: T } function main(): i32 { var b = Box[i32] { val: 42 }; print_int(b.val); return 0; }", 0, "42"},
		{"generic-receiver-method", "struct Box[T] { val: T } function (b: Box[i32]) get(): i32 { return b.val; } function main(): i32 { var b = Box[i32] { val: 42 }; print_int(b.get()); return 0; }", 0, "42"},
		{"generic-method-T-receiver", "struct Box[T] { val: T } function (b: Box[T]) doubled(): i32 { return b.val * 2; } function main(): i32 { var b = Box { val: 21 }; print_int(b.doubled()); return 0; }", 0, "42"},
		// Char-processing programs (now that s[i] byte access works).
		{"count-vowels", "function isvowel(c: i32): boolean { return c == 97 || c == 101 || c == 105 || c == 111 || c == 117; } function main(): i32 { var s: string = \"hello world\"; var n: i32 = 0; var i: i32 = 0; while (i < s.len()) { if (isvowel((s[i] as i32))) { n = n + 1; } i = i + 1; } print_int(n); return 0; }", 0, "3"},
		{"string-reverse", "function main(): i32 { var s: string = \"abcde\"; var out: string = \"\"; var i: i32 = s.len() - 1; while (i >= 0) { out = out + s[i:i + 1]; i = i - 1; } write(out); return 0; }", 0, "edcba"},

		// Hardening pass 7: compound assignment (incl. the array-element
		// form, which previously dropped the old value), hex / escape /
		// unary, and complex conditions.
		{"compound-assign-var", "function main(): i32 { var x: i32 = 10; x += 5; x -= 2; x *= 2; x /= 3; x %= 5; print_int(x); return 0; }", 0, "3"},
		{"compound-assign-array", "function main(): i32 { var xs = [1, 2, 3]; xs[1] += 10; xs[2] *= 5; print_int(xs[1]); print_int(xs[2]); return 0; }", 0, "1215"},
		{"compound-assign-field", "struct C { n: i32 } function main(): i32 { var c = C { n: 5 }; c.n += 3; c.n *= 2; print_int(c.n); return 0; }", 0, "16"},
		{"hex-literal", "function main(): i32 { print_int(0xFF); print_int(0x10); return 0; }", 0, "25516"},
		{"escape-sequences", "function main(): i32 { write(\"a\\tb\\nc\"); return 0; }", 0, "a\tb\nc"},
		// \xNN hex byte escapes (string + f-string), via string_from_bytes_unchecked.
		{"hex-escape", "function main(): i32 { write(\"\\x48\\x69\\x21\"); return 0; }", 0, "Hi!"},
		{"hex-escape-fstring", "function main(): i32 { var n: i32 = 7; write(f\"\\x41{n}\\x5a\"); return 0; }", 0, "A7Z"},
		{"deep-nesting", "function main(): i32 { print_int(((1 + 2) * (3 + 4)) - ((5 - 1) / 2)); return 0; }", 0, "19"},
		{"neg-float-compare", "function main(): i32 { var a: f64 = 0.0 - 2.5; if (a < 0.0) { print_int(1); } if (a > (0.0 - 3.0)) { print_int(2); } return 0; }", 0, "12"},
		{"while-complex-cond", "function main(): i32 { var i: i32 = 0; var j: i32 = 10; while (i < j && j > 0) { i = i + 1; j = j - 1; } print_int(i); return 0; }", 0, "5"},

		// Enums (C-style unit variants): `Color.Green` builds a variant box
		// reusing the struct-union machinery; `match` dispatches by variant.
		{"enum-match", "enum Color { Red, Green, Blue } function main(): i32 { var c = Color.Blue; match (c) { Red => { print_int(10); }, Green => { print_int(20); }, Blue => { print_int(30); } } return 0; }", 0, "30"},
		{"enum-inline-match", "enum Color { Red, Green, Blue } function main(): i32 { match (Color.Red) { Red => { print_int(10); }, Green => { print_int(20); }, Blue => { print_int(30); } } return 0; }", 0, "10"},
		{"enum-fn-arg", "enum Dir { N, S, E, W } function rank(d: Dir): i32 { match (d) { N => { return 1; }, S => { return 2; }, E => { return 3; }, W => { return 4; } } return 0; } function main(): i32 { print_int(rank(Dir.E)); print_int(rank(Dir.W)); return 0; }", 0, "34"},

		// Hardening pass 8: void calls, short-circuit &&/||, nested closures.
		{"void-function", "function greet(name: string): void { write(\"hi \"); write(name); } function main(): i32 { greet(\"sam\"); return 0; }", 0, "hi sam"},
		{"void-method", "struct L { } function (l: L) log(n: i32): void { print_int(n); } function main(): i32 { var l = L {}; l.log(5); l.log(6); return 0; }", 0, "56"},
		{"short-circuit-and-or", "function side(): boolean { print_int(9); return true; } function main(): i32 { if (false && side()) { print_int(1); } if (true || side()) { print_int(2); } return 0; }", 0, "2"},
		{"short-circuit-guard", "function main(): i32 { var xs = [10, 20]; var i: i32 = 5; if (i < xs.len() && xs[i] > 0) { print_int(1); } else { print_int(0); } return 0; }", 0, "0"},
		{"nested-closure", "function main(): i32 { var add = function(a: i32): i32 { var inner = function(b: i32): i32 { return a + b; }; return inner(10); }; print_int(add(5)); return 0; }", 0, "15"},
		{"negative-literal", "function main(): i32 { var x: i32 = -5; print_int(x); print_int(-3 + 10); return 0; }", 0, "-57"},
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
			// Fixed env vars so `env(name)` cases have something to read
			// (harmless for the other cases).
			// Fixed env vars + trailing argv ("ALPHA" "BETA") so env()/
			// args() cases have something to read (harmless otherwise).
			cmd := exec.Command("wasmtime", "run", "--env", "FERNTEST=hello123", "--env", "EMPTYVAR=", "--dir", dir, watPath, "ALPHA", "BETA")
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
