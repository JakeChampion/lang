package e2eselfhost

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestSelfHostSSAEmitX86_64 exercises the self-hosted SSA → x86-64 backend
// (examples/self_host/ssa_x86.fern): the ssa_emit_run driver parses a
// program, lowers each function to SSA, optimises it, and prints x86-64
// assembly. This test assembles that output with `gcc -static -nostdlib
// -no-pie` and runs it, asserting the process exit code equals the
// program's value — end-to-end proof that the full self-hosted pipeline
// (AST → SSA → optimise → x86-64 machine code → execute) is correct, the
// first step of emitting from SSA rather than straight from the AST.
//
// The driver is built natively via the Go x86-64 backend; the emitted
// assembly runs natively, so the test skips under an exec runner.
func TestSelfHostSSAEmitX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("emitted x86-64 runs natively; skipping under an exec runner")
	}
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "ssa_emit_run.fern")
	bin := buildSelfHostBin(t, gcc, dir, "ssa_emit_run.fern", "ssa_emit_run")

	cases := []struct {
		name string
		src  string
		want int
	}{
		{"const", "function main(): i32 { return 42; }", 42},
		// Heap headroom: a fresh 8-element array each iteration (~21.6 MiB of
		// leak-everything bump allocation) exceeds the SSA backend's old
		// 16 MiB arena — this case segfaults on that and only passes with the
		// 1 GiB heap, pinning the size that a self-hosted SSA compiler needs.
		{"heap-beyond-16mib", "function main(): i32 { var s = 0; var i = 0; while (i < 300000) { var a = [i, 0, 0, 0, 0, 0, 0, 0]; if (a[0] == i) { s = s + 1; } i = i + 1; } return s - 299993; }", 7},
		// exit(code): a dedicated SSA op → the exit syscall (must survive DCE
		// though its result is unused). Plain, conditional, and computed-arg.
		{"exit-code", "function main(): i32 { exit(7); return 0; }", 7},
		{"exit-conditional", "function main(): i32 { var x = 5; if (x > 3) { exit(9); } return 0; }", 9},
		{"exit-computed", "function main(): i32 { var n = 3 + 4; exit(n); return 1; }", 7},
		// f64_bits / f64_from_bits — bit reinterpret f64<->i64 (a pure 8-byte
		// pass-through). Round-trips a float through its bit pattern.
		{"f64-bits-roundtrip", "function main(): i32 { var x = 3.5; var b = f64_bits(x); var y = f64_from_bits(b); if (y == 3.5) { return 7; } return 0; }", 7},
		// strbuf — the global string builder (reset / append / take). Build a
		// string across appends; reuse the buffer across takes with a reset.
		{"strbuf-build", "function main(): i32 { strbuf_reset(); strbuf_append(\"ab\"); strbuf_append(\"cde\"); var s = strbuf_take(); if (s == \"abcde\") { return s.len(); } return 0; }", 5},
		{"strbuf-reuse", "function main(): i32 { strbuf_reset(); strbuf_append(\"xy\"); var a = strbuf_take(); strbuf_reset(); strbuf_append(\"zzz\"); var b = strbuf_take(); return a.len() * 10 + b.len(); }", 23},
		{"arith", "function main(): i32 { return 2 + 3 * 4; }", 14},
		// Option / Result (Some/None/Ok/Err): 2-word tag+payload boxes,
		// constructed + matched (payload bound from word 1).
		{"option-result", "function get(b: boolean): Result[i32] { if (b) { return Ok(42); } return Err(7); } function opt(b: boolean): Option[i32] { if (b) { return Some(5); } return None; } function main(): i32 { var r = 0; match (get(true)) { Ok(v) => { r = r + v; }, Err(e) => { r = r + 100 + e; } } match (get(false)) { Ok(v) => { r = r + v; }, Err(e) => { r = r + e; } } match (opt(true)) { Some(x) => { r = r + x; }, None => { r = r + 1000; } } match (opt(false)) { Some(x) => { r = r + x; }, None => { r = r + 9; } } return r; }", 63},

		// A local var shadowing a top-level function name must read the var,
		// not take the function's address (build_func ExprIdent shadowing).
		{"local-shadows-fn", "function w(): i32 { return 99; } function main(): i32 { var w = 3; var s = 0; var i = 0; while (i < w) { s = s + i; i = i + 1; } return s + w; }", 6},

		{"parens", "function main(): i32 { return (1 + 2) * 3; }", 9},
		{"locals", "function main(): i32 { var x = 10; var y = x - 3; return y * 2; }", 14},
		{"division", "function main(): i32 { return 84 / 2; }", 42},
		{"modulo", "function main(): i32 { return 23 % 5; }", 3},
		{"bitwise", "function main(): i32 { return (6 & 3) | 8; }", 10},
		{"shift", "function main(): i32 { return 1 << 4; }", 16},
		{"comparison", "function main(): i32 { return 7 > 3; }", 1},
		{"if-else", "function main(): i32 { var x = 0; if (5 < 10) { x = 7; } else { x = 9; } return x; }", 7},
		{"early-return", "function main(): i32 { var x = 5; if (x > 3) { return 100; } return x; }", 100},
		{"nested-if", "function main(): i32 { var x = 5; if (x > 0) { if (x > 3) { x = 100; } else { x = 50; } } return x; }", 100},
		{"while-sum", "function main(): i32 { var i = 1; var s = 0; while (i <= 5) { s = s + i; i = i + 1; } return s; }", 15},
		{"while-factorial", "function main(): i32 { var i = 1; var f = 1; while (i <= 5) { f = f * i; i = i + 1; } return f; }", 120},
		{"if-in-loop", "function main(): i32 { var i = 0; var c = 0; while (i < 10) { if (i > 4) { c = c + 1; } i = i + 1; } return c; }", 5},
		{"nested-loop", "function main(): i32 { var i = 0; var t = 0; while (i < 3) { var j = 0; while (j < 3) { t = t + 1; j = j + 1; } i = i + 1; } return t; }", 9},
		// Range-for `for i in LOW..HIGH`: the parser emits a synthetic
		// __range(LOW, HIGH) for-iter that the IR path lowers, but the SSA
		// backend's StmtFor builder only iterates arrays — an undesugared
		// __range iter emitted `call __fn___range` (a link error).
		// parser.desugar_ranges_func (run in ssa.build_func_seeded) rewrites
		// it to a counting while-loop. Covers continue/break/empty/nested.
		{"range-sum", "function main(): i32 { var s = 0; for i in 0..5 { s = s + i; } return s; }", 10},
		{"range-continue", "function main(): i32 { var s = 0; for i in 0..10 { if (i % 2 == 1) { continue; } s = s + i; } return s; }", 20},
		{"range-break", "function main(): i32 { var s = 0; for i in 0..100 { if (i == 5) { break; } s = s + i; } return s; }", 10},
		{"range-empty", "function main(): i32 { var c = 7; for i in 5..5 { c = c + 1; } return c; }", 7},
		{"range-nested", "function main(): i32 { var t = 0; for i in 0..3 { for j in 0..3 { t = t + 1; } } return t; }", 9},
		// Multi-function: System V argument passing + call/return.
		{"call", "function add(a: i32, b: i32): i32 { return a + b; } function main(): i32 { return add(3, 4); }", 7},
		// Default parameter values — fill_default_args_module (run in the SSA
		// driver) completes the omitted trailing argument.
		{"default-one", "function inc(n: i32, by: i32 = 1): i32 { return n + by; } function main(): i32 { return inc(6); }", 7},
		{"default-multi", "function box(w: i32, h: i32 = 2, d: i32 = 3): i32 { return w * 100 + h * 10 + d; } function main(): i32 { return box(1); }", 123},
		{"call-expr", "function sq(x: i32): i32 { return x * x; } function main(): i32 { return sq(5) + sq(3); }", 34},
		{"recursion", "function fact(n: i32): i32 { if (n <= 1) { return 1; } return n * fact(n - 1); } function main(): i32 { return fact(5); }", 120},
		// break / continue lower to extra loop edges; codegen must handle the
		// multi-predecessor phis.
		{"break", "function main(): i32 { var i = 0; while (i < 100) { if (i == 5) { break; } i = i + 1; } return i; }", 5},
		{"continue", "function main(): i32 { var i = 0; var s = 0; while (i < 10) { i = i + 1; if (i == 5) { continue; } s = s + i; } return s; }", 50},
		// Heap arrays: alloc + element load/store with pointer-width values.
		{"arr-index", "function main(): i32 { var a = [10, 20, 30]; return a[1]; }", 20},
		{"arr-sum-ends", "function main(): i32 { var a = [10, 20, 30]; return a[0] + a[2]; }", 40},
		{"arr-computed-index", "function main(): i32 { var a = [3, 7, 11, 15]; var i = 2; return a[i]; }", 11},
		{"arr-loop-sum", "function main(): i32 { var a = [5, 10, 15, 20, 25]; var i = 0; var s = 0; while (i < 5) { s = s + a[i]; i = i + 1; } return s; }", 75},
		{"arr-two", "function main(): i32 { var a = [1, 2]; var b = [100, 200]; return a[1] + b[0]; }", 102},
		{"arr-len", "function main(): i32 { var a = [10, 20, 30]; return a.len(); }", 3},
		{"arr-with", "function main(): i32 { var a = [1, 2, 3]; a = a.with(1, 20); return a[0] + a[1] + a[2]; }", 24},
		{"cell-get-set", "function main(): i32 { var c: Cell[i32] = cell_new(0); c.set(c.get() + 5); c.set(c.get() * 2); return c.get(); }", 10},
		// Shared mutation through a (non-void; the SSA backend doesn't emit
		// void user fns yet) param: each call bumps the shared cell, 10→13.
		{"cell-shared", "function bump(c: Cell[i32]): i32 { c.set(c.get() + 1); return c.get(); } function main(): i32 { var c: Cell[i32] = cell_new(10); var a = bump(c); var b = bump(c); return bump(c); }", 13},
		// Cell[string] — a string is a single pointer on the self-host SSA
		// backend, so the slot is one word like i32. "ab" overwritten to
		// "xyz"; get().len() = 3.
		{"cell-string", "function main(): i32 { var c: Cell[string] = cell_new(\"ab\"); c.set(\"xyz\"); return c.get().len(); }", 3},
		// A Cell-typed STRUCT FIELD: `b.ctr.get()` / `.set()` must resolve the
		// field's "Cell[i32]" to the normalized "cell" type so the cell method
		// path fires (a type_of_expr field-normalisation gap that made
		// wasm__emit_expr — the last whole-compiler build_func holdout — bail).
		{"cell-struct-field", "struct Box { ctr: Cell[i32], label: string } function main(): i32 { var b = Box { ctr: cell_new(10), label: \"x\" }; b.ctr.set(b.ctr.get() + 5); return b.ctr.get() + b.label.len(); }", 16},
		{"arr-with-chain", "function main(): i32 { var a = [0, 0, 0]; a = a.with(0, 5); a = a.with(2, 7); return a[0] * 10 + a[2]; }", 57},
		{"arr-len-loop", "function main(): i32 { var a = [4, 8, 12, 16]; var i = 0; var s = 0; while (i < a.len()) { s = s + a[i]; i = i + 1; } return s; }", 40},
		// for-in loops (build_for desugar → counted while). Index advance at
		// the top of the body so `continue` still steps; nested loops phi a
		// variable written only in the inner loop; iterates array bytes too.
		{"for-sum", "function main(): i32 { var a = [5, 10, 15]; var s = 0; for x in a { s = s + x; } return s; }", 30},
		{"for-empty-body", "function main(): i32 { var a = [5, 10]; for x in a { } return a.len(); }", 2},
		{"for-nested", "function main(): i32 { var rows = [1, 2, 3]; var cols = [10, 20]; var t = 0; for r in rows { for c in cols { t = t + r * c; } } return t; }", 180},
		{"for-break", "function main(): i32 { var a = [1, 2, 3, 4, 5]; var s = 0; for x in a { if (x > 3) { break; } s = s + x; } return s; }", 6},
		{"for-continue", "function main(): i32 { var a = [1, 2, 3, 4]; var s = 0; for x in a { if (x == 2) { continue; } s = s + x; } return s; }", 8},
		{"for-string-bytes", "function main(): i32 { var t = 0; for b in \"AB\" { t = t + b; } return t; }", 131},
		// for over an array-typed param / a struct's array field.
		{"for-param", "function sum(a: i32[]): i32 { var s = 0; for x in a { s = s + x; } return s; } function main(): i32 { var xs = [5, 10, 15, 20]; return sum(xs); }", 50},
		{"for-struct-array-field", "struct Box { tag: i32, data: i32[] } function main(): i32 { var b = Box { tag: 100, data: [1, 2, 3] }; var s = 0; for x in b.data { s = s + x; } return s; }", 6},
		// Typed-array element access: indexing a struct/string array recovers
		// the element type so `a[i].field` / `a[i] + …` / `a[i] == …` resolve.
		{"array-of-struct-field", "struct P { x: i32, y: i32 } function main(): i32 { var a = [P { x: 1, y: 2 }, P { x: 10, y: 20 }]; return a[0].x + a[1].y; }", 21},
		{"array-of-struct-iter", "struct P { x: i32, y: i32 } function main(): i32 { var a = [P { x: 1, y: 2 }, P { x: 3, y: 4 }, P { x: 5, y: 6 }]; var t = 0; for p in a { t = t + p.x + p.y; } return t; }", 21},
		{"array-of-struct-param", "struct P { x: i32, y: i32 } function sumx(a: P[]): i32 { var t = 0; var i = 0; while (i < a.len()) { t = t + a[i].x; i = i + 1; } return t; } function main(): i32 { var a = [P { x: 10, y: 0 }, P { x: 20, y: 0 }]; return sumx(a); }", 30},
		{"array-of-struct-string-field", "struct Named { id: i32, label: string } function main(): i32 { var a = [Named { id: 1, label: \"hello\" }, Named { id: 2, label: \"hi\" }]; return a[0].label.len() + a[1].label.len() + a[1].id; }", 9},
		{"string-array-concat", "function main(): i32 { var a = [\"foo\", \"bar\"]; var c = a[0] + a[1]; return c.len(); }", 6},
		// Tuples: fixed-arity heap box, `t.i` positional access — incl. a
		// string element and a tuple returned across a call.
		{"tuple-pair", "function main(): i32 { var t = (3, 4); return t.0 + t.1; }", 7},
		{"tuple-triple", "function main(): i32 { var t = (10, 20, 30); return t.0 * 100 + t.1 * 10 + t.2; }", 1230 % 256},
		{"tuple-string-elem", "function main(): i32 { var t = (42, \"hello\"); return t.0 + t.1.len(); }", 47},
		{"tuple-return", "function pair(): (i32, i32) { return (6, 9); } function main(): i32 { var t = pair(); return t.0 + t.1; }", 15},
		// Tuple destructuring `var (a, b) = t`, incl. from a function's tuple
		// return.
		{"tuple-destructure", "function main(): i32 { var (a, b) = (5, 6); return a + b; }", 11},
		{"tuple-destructure-call", "function pair(): (i32, i32) { return (7, 8); } function main(): i32 { var (lo, hi) = pair(); return hi - lo; }", 1},
		// No-capture lambdas: `var f = function(...){...}` lifts to a top-level
		// function (collect_lambdas) and `f(...)` is a direct call to it.
		{"lambda-call", "function main(): i32 { var f = function (x: i32): i32 { return x + 1; }; return f(5); }", 6},
		{"lambda-compose", "function main(): i32 { var inc = function (x: i32): i32 { return x + 1; }; var dbl = function (x: i32): i32 { return x * 2; }; return inc(dbl(10)); }", 21},
		{"lambda-loop", "function main(): i32 { var f = function (a: i32, b: i32): i32 { return a * b + 1; }; var s = 0; var i = 0; while (i < 4) { s = s + f(i, 2); i = i + 1; } return s; }", 16},
		// Capturing lambdas: free variables (read-only, resolvable type) become
		// trailing params on the lifted function, passed at the call site.
		{"lambda-capture-local", "function main(): i32 { var n = 10; var f = function (x: i32): i32 { return x + n; }; return f(5); }", 15},
		{"lambda-capture-params", "function add(a: i32, b: i32): i32 { var f = function (x: i32): i32 { return x + a + b; }; return f(100); } function main(): i32 { return add(3, 7); }", 110},
		{"lambda-capture-loop", "function main(): i32 { var base = 1000; var f = function (x: i32): i32 { return base + x; }; var s = 0; var i = 0; while (i < 3) { s = s + f(i); i = i + 1; } return s; }", 3003 % 256},
		{"lambda-capture-string", "function main(): i32 { var prefix = \"hello\"; var f = function (n: i32): i32 { return prefix.len() + n; }; return f(37); }", 42},
		// Higher-order functions: a no-capture lambda passed as a `(T) => R`
		// param (a function value / code pointer) and called indirectly
		// (funcaddr + call_indirect). The same indirect site dispatches to
		// different targets (lambda-indirect-dispatch).
		{"lambda-indirect", "function apply(f: (i32) => i32, x: i32): i32 { return f(x); } function main(): i32 { var inc = function (n: i32): i32 { return n + 1; }; return apply(inc, 41); }", 42},
		{"lambda-indirect-dispatch", "function apply2(f: (i32) => i32, x: i32): i32 { return f(x) + f(x + 1); } function main(): i32 { var dbl = function (n: i32): i32 { return n * 2; }; var sq = function (n: i32): i32 { return n * n; }; return apply2(dbl, 10) + apply2(sq, 3); }", 67},
		{"lambda-indirect-loop", "function run(f: (i32) => i32): i32 { var s = 0; var i = 0; while (i < 4) { s = s + f(i); i = i + 1; } return s; } function main(): i32 { var t = function (n: i32): i32 { return n * 10; }; return run(t); }", 60},
		// Function values: a top-level function name used as a value (its
		// address, via build_func's gfn: seed) and a closure returned across a
		// call (`maker(): (T)=>R`), both called indirectly.
		{"fn-value-by-name", "function work(): i32 { return 42; } function run(f: () => i32): i32 { return f(); } function main(): i32 { return run(work); }", 42},
		{"fn-value-predicate", "function is_big(n: i32): i32 { if (n > 10) { return 1; } return 0; } function count_if(a: i32[], pred: (i32) => i32): i32 { var c = 0; for x in a { if (pred(x) == 1) { c = c + 1; } } return c; } function main(): i32 { var a = [5, 20, 8, 30, 15]; return count_if(a, is_big); }", 3},
		{"closure-returned", "function maker(): (i32) => i32 { var f = function (n: i32): i32 { return n + 100; }; return f; } function main(): i32 { var g = maker(); return g(5); }", 105},
		// Escaping CAPTURING closures: the closure is boxed [fn_addr, cap…] at
		// its binding; the box flows through a `(T)=>R` param / return and the
		// indirect call passes it as the env the lifted body reads captures from.
		{"closure-escape-arg", "function apply(f: (i32) => i32, x: i32): i32 { return f(x); } function main(): i32 { var k = 100; var add_k = function (n: i32): i32 { return n + k; }; return apply(add_k, 5); }", 105},
		{"closure-escape-return", "function adder(a: i32): (i32) => i32 { var f = function (b: i32): i32 { return a + b; }; return f; } function main(): i32 { var add10 = adder(10); var add20 = adder(20); return add10(5) + add20(7); }", 42},
		{"closure-capture-multicall", "function main(): i32 { var k = 10; var f = function (x: i32): i32 { return x + k; }; return f(1) + f(2); }", 23},
		// Receiver methods `function (r: T) m(...)`: the receiver binds as an
		// implicit param 0; a `recv.m(args)` call dispatches to the mangled
		// symbol "T__m" passing the receiver first. Covers a method reading its
		// receiver's fields, a method calling another method, a method returning
		// the receiver type (chained), and a method used in a loop.
		{"method-basic", "struct Counter { n: i32 } function (c: Counter) get(): i32 { return c.n; } function (c: Counter) plus(d: i32): i32 { return c.n + d; } function main(): i32 { var c = Counter { n: 40 }; return c.get() + c.plus(2) - c.n; }", 42},
		{"method-calls-method", "struct Lex { s: string, i: i32 } function (l: Lex) at_end(): boolean { return l.i >= l.s.len(); } function (l: Lex) cur(): i32 { if (l.at_end()) { return 0 - 1; } return l.s[l.i] as i32; } function main(): i32 { var l = Lex { s: \"AB\", i: 0 }; return l.cur(); }", 65},
		{"method-chained", "struct Box { v: i32 } function (b: Box) bump(): Box { return Box { v: b.v + 1 }; } function main(): i32 { var b = Box { v: 10 }; var c = b.bump().bump(); return c.v; }", 12},
		{"method-loop", "struct Lex { s: string, i: i32 } function (l: Lex) at_end(): boolean { return l.i >= l.s.len(); } function (l: Lex) peek(): i32 { return l.s[l.i] as i32; } function (l: Lex) adv(): Lex { return Lex { s: l.s, i: l.i + 1 }; } function main(): i32 { var l = Lex { s: \"hello\", i: 0 }; var sum = 0; while (!l.at_end()) { sum = sum + l.peek(); l = l.adv(); } return sum; }", 532 % 256},
		// A call's result carries the callee's declared return type, so a
		// struct-returning call's field access and a string-returning call's
		// .len() / concat resolve.
		{"call-result-struct-field", "struct P { x: i32, y: i32 } function mk(a: i32): P { return P { x: a, y: a * 2 }; } function main(): i32 { return mk(7).x + mk(7).y; }", 21},
		{"call-result-string", "function greet(): string { return \"hello\"; } function main(): i32 { return greet().len() + (greet() + \"!\").len(); }", 11},
		// string_from_bytes_unchecked(i32[]) → a string (a copy of the byte array, which
		// shares the length-prefixed layout). The last lexer gap on the path to
		// full self-host SSA coverage.
		{"string-from-bytes", "function main(): i32 { var s = string_from_bytes_unchecked([72, 105]); var t = \"x\" + string_from_bytes_unchecked([89]) + \"z\"; return s.len() * 100 + t.len() + (s[1] as i32); }", 52},
		{"string-from-bytes-eq", "function main(): i32 { var s = string_from_bytes_unchecked([65, 66, 67, 68]); if (s == \"ABCD\") { return s.len() + 90; } return 0; }", 94},
		// __new_array(n): runtime-sized allocation (alloc op size in args[0]).
		{"new-array-fixed", "function main(): i32 { var b = __new_array(3); b[0] = 10; b[1] = 20; b[2] = 30; return b[0] + b[1] + b[2] + b.len(); }", 63},
		{"new-array-dynamic", "function main(): i32 { var n = 5; var b = __new_array(n); var i = 0; while (i < n) { b[i] = i * i; i = i + 1; } var s = 0; var j = 0; while (j < b.len()) { s = s + b[j]; j = j + 1; } return s; }", 30},
		// arr.append(x) → __ssa_arr_push helper (copy into a fresh __new_array,
		// append). Returns the new array; injected only when called.
		{"array-push", "function main(): i32 { var a = [1, 2]; a = a.append(3); a = a.append(4); return a[0] + a[1] + a[2] + a[3] + a.len(); }", 14},
		{"array-push-loop", "function main(): i32 { var a = [0]; var i = 1; while (i <= 5) { a = a.append(i * i); i = i + 1; } var s = 0; var j = 0; while (j < a.len()) { s = s + a[j]; j = j + 1; } return s; }", 55},
		{"array-push-for", "function main(): i32 { var a = [10]; a = a.append(20); a = a.append(30); var s = 0; for x in a { s = s + x; } return s; }", 60},
		{"array-push-string", "function main(): i32 { var a = [\"ab\"]; a = a.append(\"cde\"); return a[0].len() + a[1].len() + a.len(); }", 7},
		// a[lo:hi] slicing → __ssa_arr_slice (fresh array holding a[lo..hi-1]);
		// a string slice is a substring (a string is a byte array).
		{"slice-array", "function main(): i32 { var a = [10, 20, 30, 40, 50]; var b = a[1:4]; return b[0] + b[1] + b[2] + b.len(); }", 93},
		{"slice-for", "function main(): i32 { var a = [1, 2, 3, 4, 5, 6]; var sum = 0; var b = a[2:5]; for x in b { sum = sum + x; } return sum; }", 12},
		{"slice-empty", "function main(): i32 { var a = [7, 8, 9]; var b = a[0:0]; return b.len() + a[1]; }", 8},
		{"slice-string-eq", "function main(): i32 { var s = \"hello\"; if (s[1:4] == \"ell\") { return 7; } return 0; }", 7},
		{"slice-string-len", "function main(): i32 { var s = \"hello world\"; var a = s[0:5]; var b = s[6:11]; return a.len() + b.len(); }", 10},
		// Open-ended high bound `x[lo:]` — the parser desugars the omitted
		// end to `x.len()`, so SSA build_func lowers it like any slice.
		{"slice-open-array", "function main(): i32 { var a = [10, 20, 30, 40, 50]; var b = a[2:]; return b[0] + b[1] + b[2] + b.len(); }", 123},
		{"slice-open-string-eq", "function main(): i32 { var s = \"as_f64\"; if (s[3:] == \"f64\") { return 7; } return 0; }", 7},
		{"slice-open-string-len", "function main(): i32 { var s = \"hello world\"; return s[6:].len(); }", 5},
		// Indexed assignment `arr[i] = v` (parser desugar → __set_index →
		// store_elem): constant index, computed RHS, loop-fill, swap, and
		// compound `+=`.
		{"set-index", "function main(): i32 { var a = [10, 20, 30]; a[1] = 99; return a[0] + a[1] + a[2]; }", 139},
		{"set-index-fill", "function main(): i32 { var a = [0, 0, 0, 0, 0]; var i = 0; while (i < 5) { a[i] = i * i; i = i + 1; } return a[0] + a[1] + a[2] + a[3] + a[4]; }", 30},
		{"set-index-swap", "function main(): i32 { var a = [7, 3]; var t = a[0]; a[0] = a[1]; a[1] = t; return a[0] * 10 + a[1]; }", 37},
		{"set-index-compound", "function main(): i32 { var a = [10, 20, 30]; a[0] += 5; a[1] -= 4; a[2] *= 2; return a[0] + a[1] + a[2]; }", 91},
		// Mutating an array param through a pointer: the callee writes, the
		// caller sees it (shared heap buffer).
		{"set-index-param", "function bump(a: i32[]): i32 { a[0] = a[0] + 100; return 0; } function main(): i32 { var xs = [5, 6, 7]; var z = bump(xs); return xs[0] + z; }", 105},
		// i32-keyed maps. A `Map { … }` literal desugars to
		// map_new_i32(8).insert(…)…; the lowering routes the constructor + the
		// set/get_or/has/len methods to injected association-array helpers
		// (__ssa_map_*), emitted only when a program references a map.
		{"map-literal-get", "function main(): i32 { var m = Map { 1: 40, 2: 50, 3: 60 }; return m.get_or(2, 0) + m.get_or(9, 7) + m.len(); }", 60},
		{"map-has", "function main(): i32 { var m = Map { 5: 1, 7: 1 }; var r = 0; if (m.has(5)) { r = r + 10; } if (m.has(6)) { r = r + 100; } if (m.has(7)) { r = r + 1; } return r; }", 11},
		// set after construction: insert a new key, update an existing key —
		// the buffer is mutated in place (fixed capacity, no realloc).
		{"map-set-update", "function main(): i32 { var m = Map { 1: 10 }; m = m.insert(2, 20); m = m.insert(1, 99); return m.get_or(1, 0) + m.get_or(2, 0) + m.len(); }", 121},
		{"map-loop-build", "function main(): i32 { var m = Map { 0: 0 }; var i = 1; while (i <= 5) { m = m.insert(i, i * i); i = i + 1; } return m.get_or(3, 0) + m.get_or(5, 0) + m.len(); }", 40},
		{"map-miss-default", "function main(): i32 { var m = Map { 1: 1 }; return m.get_or(42, 7) + m.len(); }", 8},
		// Maps across calls: the handle is an i32[] pointer param. get_or on a
		// passed map, and len() on a Map-typed param (dispatches to the helper,
		// not the array length load).
		{"map-param-get", "function total(m: Map[i32, i32], a: i32, b: i32): i32 { return m.get_or(a, 0) + m.get_or(b, 0); } function main(): i32 { var m = Map { 1: 11, 2: 22, 3: 33 }; return total(m, 1, 3); }", 44},
		{"map-param-len", "function sz(m: Map[i32, i32]): i32 { return m.len(); } function main(): i32 { var m = Map { 1: 1, 2: 2, 3: 3, 4: 4 }; return sz(m) * 10 + m.get_or(2, 0); }", 42},
		// `for (k, v) in m` iteration: build_for sees the comma-joined loop
		// variable and walks entries by index via __ssa_map_key_at/_val_at.
		{"map-iter-sum", "function main(): i32 { var m = Map { 1: 10, 2: 20 }; var s = 0; for (k, v) in m { s = s + k + v; } return s; }", 33},
		{"map-iter-values", "function main(): i32 { var m = Map { 1: 100, 2: 50, 3: 30 }; var s = 0; for (k, v) in m { s = s + v; } return s; }", 180},
		{"map-iter-built", "function main(): i32 { var m = Map { 0: 0 }; var i = 1; while (i <= 4) { m = m.insert(i, i * 10); i = i + 1; } var sum = 0; for (k, v) in m { sum = sum + v; } return sum; }", 100},
		// break / continue inside the iteration body.
		{"map-iter-break-continue", "function main(): i32 { var m = Map { 1: 5, 2: 6, 3: 7, 4: 8 }; var s = 0; for (k, v) in m { if (k == 2) { continue; } if (k == 4) { break; } s = s + v; } return s; }", 12},
		// Iterating a Map passed across a call.
		{"map-iter-param", "function sumv(m: Map[i32, i32]): i32 { var s = 0; for (k, v) in m { s = s + v; } return s; } function main(): i32 { var m = Map { 1: 11, 2: 22, 3: 33 }; return sumv(m); }", 66},
		// keys() / values() snapshot a map's columns into fresh __new_array
		// arrays (now possible with dynamic allocation): iterate / index them.
		{"map-keys-values", "function main(): i32 { var m = Map { 1: 10, 2: 20, 3: 30 }; var ks = m.keys(); var vs = m.values(); var s = 0; for k in ks { s = s + k; } for v in vs { s = s + v; } return s + ks.len(); }", 69},
		{"map-values-index", "function main(): i32 { var m = Map { 5: 10, 6: 20 }; m = m.insert(7, 30); var vs = m.values(); var s = 0; var i = 0; while (i < vs.len()) { s = s + vs[i]; i = i + 1; } return s + vs.len(); }", 63},
		{"map-keys-after-delete", "function main(): i32 { var m = Map { 9: 1 }; m.without(9); var ks = m.keys(); return ks.len() + 42; }", 42},
		// String-keyed maps: `Map { "a": … }` → map_new().insert()… → the
		// __ssa_smap_* helpers, which compare keys by content (__streq) rather
		// than pointer. Same buffer layout as i32, so len / iteration reuse the
		// i32 helpers; the value type is i32.
		{"smap-literal-get", "function main(): i32 { var m = Map { \"a\": 10, \"b\": 20, \"c\": 30 }; return m.get_or(\"b\", 0) + m.get_or(\"z\", 7) + m.len(); }", 30},
		{"smap-set-has", "function main(): i32 { var m = Map { \"x\": 1 }; m = m.insert(\"y\", 2); m = m.insert(\"x\", 99); var r = 0; if (m.has(\"x\")) { r = r + m.get_or(\"x\", 0); } if (m.has(\"q\")) { r = r + 1000; } return r + m.get_or(\"y\", 0) + m.len(); }", 103},
		// Content comparison: the lookup key is built at runtime (concat), so
		// it's a different pointer than the stored literal — must still match.
		{"smap-content-key", "function main(): i32 { var m = Map { \"foo\": 42 }; var k = \"fo\" + \"o\"; return m.get_or(k, 0); }", 42},
		{"smap-param-delete", "function lookup(m: Map[string, i32], k: string): i32 { return m.get_or(k, 0); } function main(): i32 { var m = Map { \"hi\": 5, \"bye\": 9 }; m.without(\"hi\"); return lookup(m, \"bye\") + m.len(); }", 10},
		{"smap-iter", "function main(): i32 { var m = Map { \"a\": 100, \"b\": 50, \"c\": 30 }; var s = 0; for (k, v) in m { s = s + v + k.len(); } return s; }", 183},
		// delete: removes a key (swap-with-last, count--), missing key is a
		// no-op, and delete composes with set / iteration.
		{"map-delete", "function main(): i32 { var m = Map { 1: 10, 2: 20, 3: 30 }; m.without(2); var r = 0; if (m.has(2)) { r = r + 1000; } r = r + m.len() * 100; r = r + m.get_or(1, 0); r = r + m.get_or(3, 0); return r; }", 240},
		{"map-delete-missing", "function main(): i32 { var m = Map { 1: 10, 2: 20 }; m.without(99); return m.len() * 10 + m.get_or(2, 0); }", 40},
		{"map-delete-readd-iter", "function main(): i32 { var m = Map { 1: 10, 2: 20, 3: 30 }; m.without(3); m = m.insert(4, 40); m.without(1); var s = 0; for (k, v) in m { s = s + v; } return s + m.len(); }", 62},
		// Passing arrays to functions: pointer-typed (64-bit) params.
		{"arr-param-index", "function get(a: i32[], i: i32): i32 { return a[i]; } function main(): i32 { var xs = [10, 20, 30]; return get(xs, 1); }", 20},
		{"arr-param-sum", "function sum(a: i32[]): i32 { var i = 0; var s = 0; while (i < a.len()) { s = s + a[i]; i = i + 1; } return s; } function main(): i32 { var xs = [5, 10, 15, 20]; return sum(xs); }", 50},
		{"arr-param-two", "function dot2(a: i32[], b: i32[]): i32 { return a[0] * b[0] + a[1] * b[1]; } function main(): i32 { var p = [2, 3]; var q = [10, 20]; return dot2(p, q); }", 80},
		// Strings (byte arrays): index, byte-sum loop, and a string param.
		{"str-byte-sum", "function main(): i32 { var s = \"AAA\"; var i = 0; var t = 0; while (i < s.len()) { t = t + (s[i] as i32); i = i + 1; } return t; }", 195},
		{"str-param", "function slen(s: string): i32 { return s.len(); } function main(): i32 { var s = \"wxyz\"; return slen(s); }", 4},
		// Returning pointers (arrays / strings) from functions.
		{"return-array", "function make(): i32[] { return [10, 20, 30]; } function main(): i32 { var a = make(); return a[1]; }", 20},
		{"return-array-len", "function mk(): i32[] { return [1, 2, 3, 4]; } function main(): i32 { return mk().len(); }", 4},
		{"return-string", "function greet(): string { return \"hello\"; } function main(): i32 { var s = greet(); return s.len(); }", 5},
		{"return-array-piped", "function mk(): i32[] { return [5, 10, 15]; } function sum(a: i32[]): i32 { var i = 0; var t = 0; while (i < a.len()) { t = t + a[i]; i = i + 1; } return t; } function main(): i32 { return sum(mk()); }", 30},
		// Structs: i32 fields and pointer (string / array) fields.
		{"struct-sum", "struct Point { x: i32, y: i32 } function main(): i32 { var p = Point { x: 7, y: 9 }; return p.x + p.y; }", 16},
		{"struct-string-field", "struct Named { id: i32, label: string } function main(): i32 { var n = Named { id: 5, label: \"hello\" }; return n.label.len(); }", 5},
		{"struct-array-field", "struct Box { tag: i32, data: i32[] } function main(): i32 { var b = Box { tag: 1, data: [10, 20, 30] }; return b.data[1] + b.tag; }", 21},
		// Struct params / returns (cross-function struct pointers).
		{"struct-param", "struct Point { x: i32, y: i32 } function dist(p: Point): i32 { return p.x + p.y; } function main(): i32 { var p = Point { x: 3, y: 4 }; return dist(p); }", 7},
		{"struct-return", "struct Point { x: i32, y: i32 } function mk(): Point { return Point { x: 5, y: 6 }; } function main(): i32 { var p: Point = mk(); return p.x + p.y; }", 11},
		{"struct-passthrough", "struct P { a: i32, b: i32 } function id(p: P): P { return p; } function main(): i32 { var q = P { a: 8, b: 9 }; var r: P = id(q); return r.b; }", 9},
		{"struct-param-string", "struct Named { id: i32, label: string } function llen(n: Named): i32 { return n.label.len(); } function main(): i32 { var n = Named { id: 1, label: \"abcd\" }; return llen(n); }", 4},
		// All-paths-return helper (dispatch where every arm returns).
		{"all-return-helper", "function sign(n: i32): i32 { if (n < 0) { return 0 - 1; } else if (n == 0) { return 0; } else { return 1; } } function main(): i32 { return sign(0 - 5) + 10 * sign(7); }", 9},
		// String equality driving dispatch (content comparison via streq).
		{"streq-dispatch", "function kind(s: string): i32 { if (s == \"add\") { return 1; } if (s == \"sub\") { return 2; } return 0; } function main(): i32 { return kind(\"sub\") + 10 * kind(\"add\"); }", 12},
		// enums + match: a variant-dispatching helper (tag + payload fields).
		{"match-area", "struct Circle { r: i32 } struct Square { side: i32 } type Shape = Circle | Square; function area(sh: Shape): i32 { match (sh) { Circle(c) => { return c.r * c.r * 3; }, Square(s) => { return s.side * s.side; } } return 0; } function main(): i32 { var a: Shape = Circle { r: 4 }; var b: Shape = Square { side: 5 }; return area(a) + area(b); }", 73},
		// Struct spread (functional update): non-overridden fields copied from base.
		{"struct-spread", "struct P { x: i32, y: i32, z: i32 } function (p: P) with_y(v: i32): P { return P { ...p, y: v }; } function main(): i32 { var p = P { x: 1, y: 2, z: 3 }; var q = p.with_y(20); return q.x + q.y + q.z; }", 24},
		// f64 floats (intra-function): literals lower to .rodata .double + SSE2
		// (movsd / addsd / …), comparisons via ucomisd, casts via cvtsi2sd /
		// cvttsd2si. Results are cast to i32 to surface as the exit code. Loop /
		// if float vars exercise the 64-bit phi-edge moves.
		{"float-add", "function main(): i32 { var x = 1.5; var y = x + 2.5; return y as i32; }", 4},
		{"float-arith-chain", "function main(): i32 { var x = 1.5; var y = x + 2.5; var z = y * 2.0; return z as i32; }", 8},
		{"float-sub", "function main(): i32 { var a = 5.5; var b = 2.5; return (a - b) as i32; }", 3},
		{"float-div", "function main(): i32 { var a = 9.0; var b = 2.0; return (a / b) as i32; }", 4},
		{"float-neg", "function main(): i32 { var a = 4.0; var b = 0.0 - a; return (0.0 - b) as i32; }", 4},
		{"int-to-float", "function main(): i32 { var n = 7; var x = n as f64; return (x + 0.5) as i32; }", 7},
		{"float-compare-gt", "function main(): i32 { var a = 3.5; if (a > 2.0) { return 1; } return 0; }", 1},
		{"float-compare-eq", "function main(): i32 { var a = 1.5; var b = 1.5; if (a == b) { return 1; } return 0; }", 1},
		{"float-reassign", "function main(): i32 { var a = 1.0; a = a * 3.0; return a as i32; }", 3},
		{"float-loop-accumulate", "function main(): i32 { var sum = 0.0; var i = 0; while (i < 4) { sum = sum + 1.5; i = i + 1; } return sum as i32; }", 6},
		{"float-if-phi", "function main(): i32 { var x = 0.0; if (3 > 1) { x = 5.5; } else { x = 1.0; } return x as i32; }", 5},
		// f64 call ABI: float params arrive in xmm0…, float returns come back
		// in xmm0, mixed int/float args fill the two register sequences
		// independently (System V).
		{"float-param", "function half(x: f64): f64 { return x / 2.0; } function main(): i32 { return half(9.0) as i32; }", 4},
		{"float-two-args", "function add(a: f64, b: f64): f64 { return a + b; } function main(): i32 { return add(3.5, 3.5) as i32; }", 7},
		{"float-ret-used", "function mk(): f64 { return 5.0; } function main(): i32 { var x = mk(); var y = x * 2.0; return y as i32; }", 10},
		{"float-recursion", "function pow2(n: i32): f64 { if (n <= 0) { return 1.0; } return pow2(n - 1) * 2.0; } function main(): i32 { return (pow2(3) - 2.0) as i32; }", 6},
		{"float-mixed-args", "function f(a: i32, b: f64, c: i32): f64 { return (a as f64) + b + (c as f64); } function main(): i32 { var r = f(3, 2.5, 5); return (r as i32) + 1; }", 11},
	}

	// run assembles `asm` and asserts the program exits with tc.want.
	run := func(t *testing.T, src string, want int, asm []byte) {
		t.Helper()
		asmPath := filepath.Join(dir, "prog.s")
		binPath := filepath.Join(dir, "prog")
		if err := os.WriteFile(asmPath, asm, 0o644); err != nil {
			t.Fatalf("write asm: %v", err)
		}
		if out, err := exec.Command(gcc, "-static", "-nostdlib", "-no-pie", asmPath, "-o", binPath).CombinedOutput(); err != nil {
			t.Fatalf("gcc failed for %q: %v\n%s\n--- asm ---\n%s", src, err, out, asm)
		}
		cmd := exec.Command(binPath)
		_ = cmd.Run()
		if cmd.ProcessState == nil || !cmd.ProcessState.Exited() {
			t.Fatalf("emitted program did not exit normally for %q", src)
		}
		if got := cmd.ProcessState.ExitCode(); got != want {
			t.Errorf("SSA→x86-64 of %q = %d, want %d", src, got, want)
		}
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			emit := exec.Command(bin)
			emit.Stdin = strings.NewReader(tc.src)
			asm, err := emit.Output()
			if err != nil {
				t.Fatalf("emit driver failed for %q: %v", tc.src, err)
			}
			run(t, tc.src, tc.want, asm)
		})
	}

	// Re-run every case through the register allocator (-regalloc). The default
	// driver run above leaves the allocator off, but the CLI (fern.try_ssa)
	// uses it — so without this pass, allocator bugs (e.g. the loop-invariant
	// live-range clobber where a value live across a loop shared a register
	// with a loop-body temp) ship untested. Same programs, same expected exits.
	for _, tc := range cases {
		t.Run("regalloc/"+tc.name, func(t *testing.T) {
			emit := exec.Command(bin, "-regalloc")
			emit.Stdin = strings.NewReader(tc.src)
			asm, err := emit.Output()
			if err != nil {
				t.Fatalf("emit -regalloc driver failed for %q: %v", tc.src, err)
			}
			run(t, tc.src, tc.want, asm)
		})
	}

	// Scaling: a module with many functions must emit in roughly linear time
	// rather than OOM. Before the fix, build_func re-derived the whole-module
	// var-type seed (every global function + receiver method) on every one of
	// the n calls — O(n²)+ persistent-vt pushes — and emit_program folded each
	// function body into one growing string (O(functions·total)); together they
	// killed the process (exit 137) on a few hundred functions. With the seed
	// computed once and the asm joined balanced, 600 functions emit fine. Each
	// h{i} computes ((x + i%9) * 3) - i%5; main sums two of them: h1(2)=8,
	// h7(3)=28 → 36.
	t.Run("scaling-600-functions", func(t *testing.T) {
		var b strings.Builder
		for i := 0; i < 600; i++ {
			fmt.Fprintf(&b, "function h%d(x: i32): i32 { var s = x; s = s + %d; s = s * 3; s = s - %d; return s; }\n", i, i%9, i%5)
		}
		b.WriteString("function main(): i32 { return (h1(2) + h7(3)) % 256; }\n")
		src := b.String()
		emit := exec.Command(bin)
		emit.Stdin = strings.NewReader(src)
		asm, err := emit.Output()
		if err != nil {
			t.Fatalf("emit driver failed for 600-function module: %v", err)
		}
		if len(asm) == 0 {
			t.Fatalf("emit produced empty output for 600-function module")
		}
		run(t, "scaling-600-functions", 36, asm)
	})

	// Scaling: a single large function must optimise in roughly linear time
	// rather than O(n²). The optimiser passes updated a per-value scratch table
	// (cset/cval, the live-range tables, …) through a copying env_set_at — O(n)
	// per write, O(n²) per pass — and const_fold snapshotted constants once per
	// round, folding only one link of a dependent chain per round (another
	// O(n²)). A 400-statement `s = s + k` chain (which folds to a single
	// constant) took seconds and OOM'd not far above; with the inline-updating
	// const_fold and the in-place env_put it is milliseconds. Expected value:
	// the running sum of j%7, mod 256.
	t.Run("scaling-large-function", func(t *testing.T) {
		const n = 400
		var b strings.Builder
		b.WriteString("function main(): i32 {\n  var s = 0;\n")
		sum := 0
		for j := 0; j < n; j++ {
			fmt.Fprintf(&b, "  s = s + %d;\n", j%7)
			sum += j % 7
		}
		b.WriteString("  return s % 256;\n}\n")
		emit := exec.Command(bin)
		emit.Stdin = strings.NewReader(b.String())
		asm, err := emit.Output()
		if err != nil {
			t.Fatalf("emit driver failed for 400-statement function: %v", err)
		}
		if len(asm) == 0 {
			t.Fatalf("emit produced empty output for 400-statement function")
		}
		run(t, "scaling-large-function", sum%256, asm)
	})

	// File I/O — read_file / write_file lowered to the syscall runtime. These
	// run through both the default driver and -regalloc (the CLI path), since
	// the helpers clobber caller-saved registers around the call and the
	// allocator must spill live values across it.
	emitFileIO := func(t *testing.T, src string, args ...string) []byte {
		t.Helper()
		emit := exec.Command(bin, args...)
		emit.Stdin = strings.NewReader(src)
		asm, err := emit.Output()
		if err != nil {
			t.Fatalf("emit driver failed for %q: %v", src, err)
		}
		return asm
	}
	for _, mode := range []struct {
		name string
		args []string
	}{{"default", nil}, {"regalloc", []string{"-regalloc"}}} {
		mode := mode
		// Round-trip: write "hello, fern" to an absolute path under the test's
		// temp dir, read it back, and compare via streq — exercising the SSA
		// [len, byte-per-word] string layout end-to-end. Returns 42 on a clean
		// match; a Go-side read confirms write_file produced the exact bytes.
		t.Run("file-io-roundtrip/"+mode.name, func(t *testing.T) {
			ioPath := filepath.Join(dir, "io_roundtrip_"+mode.name+".txt")
			_ = os.Remove(ioPath)
			const content = "hello, fern"
			src := fmt.Sprintf("function main(): i32 { match (write_file(%q, %q)) { Err(e) => { return 1; }, Ok(_) => {} } match (read_file(%q)) { Ok(s) => { if (s == %q) { return 42; } return 2; }, Err(e) => { return 3; } } }", ioPath, content, ioPath, content)
			run(t, "file-io-roundtrip", 42, emitFileIO(t, src, mode.args...))
			got, err := os.ReadFile(ioPath)
			if err != nil {
				t.Fatalf("write_file did not create %s: %v", ioPath, err)
			}
			if string(got) != content {
				t.Errorf("write_file wrote %q, want %q", got, content)
			}
		})
		// read_file on a file the test pre-wrote (externally-produced bytes):
		// returns len + first byte = 10 + 'a'.
		t.Run("file-io-read-external/"+mode.name, func(t *testing.T) {
			ioPath := filepath.Join(dir, "io_external_"+mode.name+".txt")
			const content = "abcdefghij" // 10 bytes
			if err := os.WriteFile(ioPath, []byte(content), 0o644); err != nil {
				t.Fatalf("seed file: %v", err)
			}
			src := fmt.Sprintf("function main(): i32 { match (read_file(%q)) { Ok(s) => { return s.len() + s[0]; }, Err(e) => { return 0; } } }", ioPath)
			run(t, "file-io-read-external", 10+int('a'), emitFileIO(t, src, mode.args...))
		})
		// read_file on a missing path → Err.
		t.Run("file-io-read-missing/"+mode.name, func(t *testing.T) {
			ioPath := filepath.Join(dir, "does_not_exist.txt")
			src := fmt.Sprintf("function main(): i32 { match (read_file(%q)) { Ok(s) => { return 0; }, Err(e) => { return 7; } } }", ioPath)
			run(t, "file-io-read-missing", 7, emitFileIO(t, src, mode.args...))
		})
	}
}
