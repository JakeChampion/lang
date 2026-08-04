package e2eselfhost

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestSelfHostSSAEmitArm64 exercises the self-hosted SSA → arm64 backend
// (examples/self_host/ssa_arm64.fern): the ssa_emit_run driver, given
// `-target arm64`, lowers each function to SSA, optimises it, and prints
// AArch64 assembly. This test assembles that output with `gcc -static
// -nostdlib` and runs it (natively on arm64, else under qemu-aarch64),
// asserting the process exit code equals the program's value — arm64
// parity for the SSA backend on the project's DEFAULT target.
//
// The driver itself is built + run via the x86-64 host toolchain (it only
// produces text); the arm64 toolchain assembles and runs the emitted code.
func TestSelfHostSSAEmitArm64(t *testing.T) {
	x86gcc, x86runner := x86_64Tooling(t)
	gcc, qemu := arm64Tooling(t)

	dir := t.TempDir()
	copySelfHostDriver(t, dir, "ssa_emit_run.fern")
	bin := buildSelfHostBin(t, x86gcc, dir, "ssa_emit_run.fern", "ssa_emit_run")

	// runDriver pipes src into the (x86-64) driver, respecting an exec
	// runner when the host isn't x86-64, and returns its stdout (asm).
	runDriver := func(t *testing.T, src string, args ...string) []byte {
		t.Helper()
		var cmd *exec.Cmd
		if len(x86runner) == 0 {
			cmd = exec.Command(bin, args...)
		} else {
			cmd = exec.Command(x86runner[0], append(append(x86runner[1:], bin), args...)...)
		}
		cmd.Stdin = strings.NewReader(src)
		out, err := cmd.Output()
		if err != nil {
			t.Fatalf("emit driver failed for %q: %v", src, err)
		}
		return out
	}

	cases := []struct {
		name string
		src  string
		want int
	}{
		{"const", "function main(): i32 { return 42; }", 42},
		// Heap headroom: ~21.6 MiB of leak-everything bump allocation exceeds
		// the SSA backend's old 16 MiB arena, pinning the 1 GiB heap a
		// self-hosted SSA compiler needs (segfaults on the old size).
		{"heap-beyond-16mib", "function main(): i32 { var s = 0; var i = 0; while (i < 300000) { var a = [i, 0, 0, 0, 0, 0, 0, 0]; if (a[0] == i) { s = s + 1; } i = i + 1; } return s - 299993; }", 7},
		// exit(code): a dedicated SSA op → the exit syscall (93), must survive DCE.
		{"exit-code", "function main(): i32 { exit(7); return 0; }", 7},
		{"exit-conditional", "function main(): i32 { var x = 5; if (x > 3) { exit(9); } return 0; }", 9},
		{"exit-computed", "function main(): i32 { var n = 3 + 4; exit(n); return 1; }", 7},
		// f64_bits / f64_from_bits — bit reinterpret f64<->i64 (8-byte pass-through).
		{"f64-bits-roundtrip", "function main(): i32 { var x = 3.5; var b = f64_bits(x); var y = f64_from_bits(b); if (y == 3.5) { return 7; } return 0; }", 7},
		// strbuf — global string builder (reset / append / take).
		{"strbuf-build", "function main(): i32 { strbuf_reset(); strbuf_append(\"ab\"); strbuf_append(\"cde\"); var s = strbuf_take(); if (s == \"abcde\") { return s.len(); } return 0; }", 5},
		{"strbuf-reuse", "function main(): i32 { strbuf_reset(); strbuf_append(\"xy\"); var a = strbuf_take(); strbuf_reset(); strbuf_append(\"zzz\"); var b = strbuf_take(); return a.len() * 10 + b.len(); }", 23},
		{"arith", "function main(): i32 { return 2 + 3 * 4; }", 14},
		// Option / Result (Some/None/Ok/Err): 2-word tag+payload boxes,
		// constructed + matched (payload bound from word 1).
		{"option-result", "function get(b: boolean): Result[i32] { if (b) { return Ok(42); } return Err(7); } function opt(b: boolean): Option[i32] { if (b) { return Some(5); } return None; } function main(): i32 { var r = 0; match (get(true)) { Ok(v) => { r = r + v; }, Err(e) => { r = r + 100 + e; } } match (get(false)) { Ok(v) => { r = r + v; }, Err(e) => { r = r + e; } } match (opt(true)) { Some(x) => { r = r + x; }, None => { r = r + 1000; } } match (opt(false)) { Some(x) => { r = r + x; }, None => { r = r + 9; } } return r; }", 63},

		// A local var shadowing a top-level function name must read the var,
		// not take the function's address (build_func ExprIdent shadowing).
		{"local-shadows-fn", "function w(): i32 { return 99; } function main(): i32 { var w = 3; var s = 0; var i = 0; while (i < w) { s = s + i; i = i + 1; } return s + w; }", 6},

		{"locals", "function main(): i32 { var x = 10; var y = x - 3; return y * 2; }", 14},
		{"division", "function main(): i32 { return 84 / 2; }", 42},
		{"modulo", "function main(): i32 { return 23 % 5; }", 3},
		{"bitwise", "function main(): i32 { return (6 & 3) | 8; }", 10},
		{"shift", "function main(): i32 { return 1 << 4; }", 16},
		{"negative-const", "function main(): i32 { var x = 0 - 7; return x + 100; }", 93},
		{"comparison", "function main(): i32 { return 7 > 3; }", 1},
		{"if-else", "function main(): i32 { var x = 0; if (5 < 10) { x = 7; } else { x = 9; } return x; }", 7},
		{"nested-if", "function main(): i32 { var x = 5; if (x > 0) { if (x > 3) { x = 100; } else { x = 50; } } return x; }", 100},
		{"while-sum", "function main(): i32 { var i = 1; var s = 0; while (i <= 5) { s = s + i; i = i + 1; } return s; }", 15},
		{"while-factorial", "function main(): i32 { var i = 1; var f = 1; while (i <= 5) { f = f * i; i = i + 1; } return f; }", 120},
		{"nested-loop", "function main(): i32 { var i = 0; var t = 0; while (i < 3) { var j = 0; while (j < 3) { t = t + 1; j = j + 1; } i = i + 1; } return t; }", 9},
		{"call", "function add(a: i32, b: i32): i32 { return a + b; } function main(): i32 { return add(3, 4); }", 7},
		{"recursion", "function fact(n: i32): i32 { if (n <= 1) { return 1; } return n * fact(n - 1); } function main(): i32 { return fact(5); }", 120},
		// break / continue lower to extra loop edges.
		{"break", "function main(): i32 { var i = 0; while (i < 100) { if (i == 5) { break; } i = i + 1; } return i; }", 5},
		{"continue", "function main(): i32 { var i = 0; var s = 0; while (i < 10) { i = i + 1; if (i == 5) { continue; } s = s + i; } return s; }", 50},
		// Heap arrays: alloc + element load/store with pointer-width values.
		{"arr-index", "function main(): i32 { var a = [10, 20, 30]; return a[1]; }", 20},
		{"arr-with", "function main(): i32 { var a = [1, 2, 3]; a = a.with(1, 20); return a[0] + a[1] + a[2]; }", 24},
		{"arr-computed-index", "function main(): i32 { var a = [3, 7, 11, 15]; var i = 2; return a[i]; }", 11},
		{"arr-loop-sum", "function main(): i32 { var a = [5, 10, 15, 20, 25]; var i = 0; var s = 0; while (i < 5) { s = s + a[i]; i = i + 1; } return s; }", 75},
		{"arr-two", "function main(): i32 { var a = [1, 2]; var b = [100, 200]; return a[1] + b[0]; }", 102},
		{"arr-len-loop", "function main(): i32 { var a = [4, 8, 12, 16]; var i = 0; var s = 0; while (i < a.len()) { s = s + a[i]; i = i + 1; } return s; }", 40},
		// for-in loops (build_for desugar → counted while): element bind,
		// empty body, nested (inner-only write), break / continue, string
		// bytes, and a for over an array param.
		{"for-sum", "function main(): i32 { var a = [5, 10, 15]; var s = 0; for x in a { s = s + x; } return s; }", 30},
		{"for-empty-body", "function main(): i32 { var a = [5, 10]; for x in a { } return a.len(); }", 2},
		{"for-nested", "function main(): i32 { var rows = [1, 2, 3]; var cols = [10, 20]; var t = 0; for r in rows { for c in cols { t = t + r * c; } } return t; }", 180},
		{"for-break", "function main(): i32 { var a = [1, 2, 3, 4, 5]; var s = 0; for x in a { if (x > 3) { break; } s = s + x; } return s; }", 6},
		{"for-continue", "function main(): i32 { var a = [1, 2, 3, 4]; var s = 0; for x in a { if (x == 2) { continue; } s = s + x; } return s; }", 8},
		{"for-string-bytes", "function main(): i32 { var t = 0; for b in \"AB\" { t = t + b; } return t; }", 131},
		{"for-param", "function sum(a: i32[]): i32 { var s = 0; for x in a { s = s + x; } return s; } function main(): i32 { var xs = [5, 10, 15, 20]; return sum(xs); }", 50},
		// Typed-array element access: `a[i].field` / `a[i] + …` on struct /
		// string arrays.
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
		// Tuple destructuring `var (a, b) = t`, incl. from a tuple return.
		{"tuple-destructure", "function main(): i32 { var (a, b) = (5, 6); return a + b; }", 11},
		{"tuple-destructure-call", "function pair(): (i32, i32) { return (7, 8); } function main(): i32 { var (lo, hi) = pair(); return hi - lo; }", 1},
		// No-capture lambdas lift to top-level functions; `f(...)` is a direct call.
		{"lambda-call", "function main(): i32 { var f = function (x: i32): i32 { return x + 1; }; return f(5); }", 6},
		{"lambda-compose", "function main(): i32 { var inc = function (x: i32): i32 { return x + 1; }; var dbl = function (x: i32): i32 { return x * 2; }; return inc(dbl(10)); }", 21},
		{"lambda-loop", "function main(): i32 { var f = function (a: i32, b: i32): i32 { return a * b + 1; }; var s = 0; var i = 0; while (i < 4) { s = s + f(i, 2); i = i + 1; } return s; }", 16},
		// Capturing lambdas: read-only free vars become trailing call args.
		{"lambda-capture-local", "function main(): i32 { var n = 10; var f = function (x: i32): i32 { return x + n; }; return f(5); }", 15},
		{"lambda-capture-params", "function add(a: i32, b: i32): i32 { var f = function (x: i32): i32 { return x + a + b; }; return f(100); } function main(): i32 { return add(3, 7); }", 110},
		{"lambda-capture-string", "function main(): i32 { var prefix = \"hello\"; var f = function (n: i32): i32 { return prefix.len() + n; }; return f(37); }", 42},
		// Higher-order: no-capture lambda as a `(T)=>R` value, called indirectly.
		{"lambda-indirect", "function apply(f: (i32) => i32, x: i32): i32 { return f(x); } function main(): i32 { var inc = function (n: i32): i32 { return n + 1; }; return apply(inc, 41); }", 42},
		{"lambda-indirect-dispatch", "function apply2(f: (i32) => i32, x: i32): i32 { return f(x) + f(x + 1); } function main(): i32 { var dbl = function (n: i32): i32 { return n * 2; }; var sq = function (n: i32): i32 { return n * n; }; return apply2(dbl, 10) + apply2(sq, 3); }", 67},
		{"lambda-indirect-loop", "function run(f: (i32) => i32): i32 { var s = 0; var i = 0; while (i < 4) { s = s + f(i); i = i + 1; } return s; } function main(): i32 { var t = function (n: i32): i32 { return n * 10; }; return run(t); }", 60},
		// Function values: named function as a value, and a returned closure.
		{"fn-value-by-name", "function work(): i32 { return 42; } function run(f: () => i32): i32 { return f(); } function main(): i32 { return run(work); }", 42},
		{"fn-value-predicate", "function is_big(n: i32): i32 { if (n > 10) { return 1; } return 0; } function count_if(a: i32[], pred: (i32) => i32): i32 { var c = 0; for x in a { if (pred(x) == 1) { c = c + 1; } } return c; } function main(): i32 { var a = [5, 20, 8, 30, 15]; return count_if(a, is_big); }", 3},
		{"closure-returned", "function maker(): (i32) => i32 { var f = function (n: i32): i32 { return n + 100; }; return f; } function main(): i32 { var g = maker(); return g(5); }", 105},
		// Escaping capturing closures (boxed [fn_addr, cap…], env passed at the
		// indirect call).
		{"closure-escape-arg", "function apply(f: (i32) => i32, x: i32): i32 { return f(x); } function main(): i32 { var k = 100; var add_k = function (n: i32): i32 { return n + k; }; return apply(add_k, 5); }", 105},
		{"closure-escape-return", "function adder(a: i32): (i32) => i32 { var f = function (b: i32): i32 { return a + b; }; return f; } function main(): i32 { var add10 = adder(10); var add20 = adder(20); return add10(5) + add20(7); }", 42},
		{"closure-capture-multicall", "function main(): i32 { var k = 10; var f = function (x: i32): i32 { return x + k; }; return f(1) + f(2); }", 23},
		// Receiver methods `function (r: T) m(...)` — receiver as implicit
		// param 0, `recv.m(args)` → call "T__m" with the receiver first.
		{"method-basic", "struct Counter { n: i32 } function (c: Counter) get(): i32 { return c.n; } function (c: Counter) plus(d: i32): i32 { return c.n + d; } function main(): i32 { var c = Counter { n: 40 }; return c.get() + c.plus(2) - c.n; }", 42},
		// A Cell-typed struct field: b.ctr.get()/.set() resolves the "Cell[i32]"
		// field type to "cell" (the type_of_expr field-normalisation fix that
		// closed the last whole-compiler build_func holdout, wasm__emit_expr).
		{"cell-struct-field", "struct Box { ctr: Cell[i32], label: string } function main(): i32 { var b = Box { ctr: cell_new(10), label: \"x\" }; b.ctr.set(b.ctr.get() + 5); return b.ctr.get() + b.label.len(); }", 16},
		{"method-calls-method", "struct Lex { s: string, i: i32 } function (l: Lex) at_end(): boolean { return l.i >= l.s.len(); } function (l: Lex) cur(): i32 { if (l.at_end()) { return 0 - 1; } return l.s[l.i] as i32; } function main(): i32 { var l = Lex { s: \"AB\", i: 0 }; return l.cur(); }", 65},
		{"method-chained", "struct Box { v: i32 } function (b: Box) bump(): Box { return Box { v: b.v + 1 }; } function main(): i32 { var b = Box { v: 10 }; var c = b.bump().bump(); return c.v; }", 12},
		{"method-loop", "struct Lex { s: string, i: i32 } function (l: Lex) at_end(): boolean { return l.i >= l.s.len(); } function (l: Lex) peek(): i32 { return l.s[l.i] as i32; } function (l: Lex) adv(): Lex { return Lex { s: l.s, i: l.i + 1 }; } function main(): i32 { var l = Lex { s: \"hello\", i: 0 }; var sum = 0; while (!l.at_end()) { sum = sum + l.peek(); l = l.adv(); } return sum; }", 532 % 256},
		// Call result carries the callee's return type (struct field / string).
		{"call-result-struct-field", "struct P { x: i32, y: i32 } function mk(a: i32): P { return P { x: a, y: a * 2 }; } function main(): i32 { return mk(7).x + mk(7).y; }", 21},
		{"call-result-string", "function greet(): string { return \"hello\"; } function main(): i32 { return greet().len() + (greet() + \"!\").len(); }", 11},
		// string_from_bytes_unchecked(i32[]) → a string (copy; shared byte-array layout).
		{"string-from-bytes", "function main(): i32 { var s = string_from_bytes_unchecked([72, 105]); var t = \"x\" + string_from_bytes_unchecked([89]) + \"z\"; return s.len() * 100 + t.len() + (s[1] as i32); }", 52},
		{"string-from-bytes-eq", "function main(): i32 { var s = string_from_bytes_unchecked([65, 66, 67, 68]); if (s == \"ABCD\") { return s.len() + 90; } return 0; }", 94},
		// __new_array(n): runtime-sized allocation (alloc op size in args[0]).
		{"new-array-fixed", "function main(): i32 { var b = __new_array(3); b[0] = 10; b[1] = 20; b[2] = 30; return b[0] + b[1] + b[2] + b.len(); }", 63},
		{"new-array-dynamic", "function main(): i32 { var n = 5; var b = __new_array(n); var i = 0; while (i < n) { b[i] = i * i; i = i + 1; } var s = 0; var j = 0; while (j < b.len()) { s = s + b[j]; j = j + 1; } return s; }", 30},
		// arr.append(x) → __ssa_arr_push (copy into fresh __new_array, append).
		{"array-push", "function main(): i32 { var a = [1, 2]; a = a.append(3); a = a.append(4); return a[0] + a[1] + a[2] + a[3] + a.len(); }", 14},
		{"array-push-loop", "function main(): i32 { var a = [0]; var i = 1; while (i <= 5) { a = a.append(i * i); i = i + 1; } var s = 0; var j = 0; while (j < a.len()) { s = s + a[j]; j = j + 1; } return s; }", 55},
		{"array-push-string", "function main(): i32 { var a = [\"ab\"]; a = a.append(\"cde\"); return a[0].len() + a[1].len() + a.len(); }", 7},
		// a[lo:hi] slicing → __ssa_arr_slice (substring for a string).
		{"slice-array", "function main(): i32 { var a = [10, 20, 30, 40, 50]; var b = a[1:4]; return b[0] + b[1] + b[2] + b.len(); }", 93},
		{"slice-for", "function main(): i32 { var a = [1, 2, 3, 4, 5, 6]; var sum = 0; var b = a[2:5]; for x in b { sum = sum + x; } return sum; }", 12},
		{"slice-string-eq", "function main(): i32 { var s = \"hello\"; if (s[1:4] == \"ell\") { return 7; } return 0; }", 7},
		// Open-ended high bound `x[lo:]` (parser desugars to `x.len()`).
		{"slice-open-array", "function main(): i32 { var a = [10, 20, 30, 40, 50]; var b = a[2:]; return b[0] + b[1] + b[2] + b.len(); }", 123},
		{"slice-open-string-eq", "function main(): i32 { var s = \"as_f64\"; if (s[3:] == \"f64\") { return 7; } return 0; }", 7},
		// Indexed assignment `arr[i] = v` (→ __set_index → store_elem):
		// constant index, loop-fill, swap, compound, and cross-call mutation
		// through a shared array param.
		{"set-index", "function main(): i32 { var a = [10, 20, 30]; a[1] = 99; return a[0] + a[1] + a[2]; }", 139},
		{"set-index-fill", "function main(): i32 { var a = [0, 0, 0, 0, 0]; var i = 0; while (i < 5) { a[i] = i * i; i = i + 1; } return a[0] + a[1] + a[2] + a[3] + a[4]; }", 30},
		{"set-index-swap", "function main(): i32 { var a = [7, 3]; var t = a[0]; a[0] = a[1]; a[1] = t; return a[0] * 10 + a[1]; }", 37},
		{"set-index-compound", "function main(): i32 { var a = [10, 20, 30]; a[0] += 5; a[1] -= 4; a[2] *= 2; return a[0] + a[1] + a[2]; }", 91},
		{"set-index-param", "function bump(a: i32[]): i32 { a[0] = a[0] + 100; return 0; } function main(): i32 { var xs = [5, 6, 7]; var z = bump(xs); return xs[0] + z; }", 105},
		// i32-keyed maps (Map literal → map_new_i32().insert()… → injected
		// __ssa_map_* association-array helpers): get_or / has / len, set
		// (insert + update), loop-build, miss-default, and maps across calls.
		{"map-literal-get", "function main(): i32 { var m = Map { 1: 40, 2: 50, 3: 60 }; return m.get_or(2, 0) + m.get_or(9, 7) + m.len(); }", 60},
		{"map-has", "function main(): i32 { var m = Map { 5: 1, 7: 1 }; var r = 0; if (m.has(5)) { r = r + 10; } if (m.has(6)) { r = r + 100; } if (m.has(7)) { r = r + 1; } return r; }", 11},
		{"map-set-update", "function main(): i32 { var m = Map { 1: 10 }; m = m.insert(2, 20); m = m.insert(1, 99); return m.get_or(1, 0) + m.get_or(2, 0) + m.len(); }", 121},
		{"map-loop-build", "function main(): i32 { var m = Map { 0: 0 }; var i = 1; while (i <= 5) { m = m.insert(i, i * i); i = i + 1; } return m.get_or(3, 0) + m.get_or(5, 0) + m.len(); }", 40},
		{"map-miss-default", "function main(): i32 { var m = Map { 1: 1 }; return m.get_or(42, 7) + m.len(); }", 8},
		{"map-param-get", "function total(m: Map[i32, i32], a: i32, b: i32): i32 { return m.get_or(a, 0) + m.get_or(b, 0); } function main(): i32 { var m = Map { 1: 11, 2: 22, 3: 33 }; return total(m, 1, 3); }", 44},
		{"map-param-len", "function sz(m: Map[i32, i32]): i32 { return m.len(); } function main(): i32 { var m = Map { 1: 1, 2: 2, 3: 3, 4: 4 }; return sz(m) * 10 + m.get_or(2, 0); }", 42},
		// `for (k, v) in m` iteration (entry walk via __ssa_map_key_at/_val_at):
		// key+value sum, values-only, build-then-iterate, break/continue in the
		// body, and iterating a Map passed across a call.
		{"map-iter-sum", "function main(): i32 { var m = Map { 1: 10, 2: 20 }; var s = 0; for (k, v) in m { s = s + k + v; } return s; }", 33},
		{"map-iter-values", "function main(): i32 { var m = Map { 1: 100, 2: 50, 3: 30 }; var s = 0; for (k, v) in m { s = s + v; } return s; }", 180},
		{"map-iter-built", "function main(): i32 { var m = Map { 0: 0 }; var i = 1; while (i <= 4) { m = m.insert(i, i * 10); i = i + 1; } var sum = 0; for (k, v) in m { sum = sum + v; } return sum; }", 100},
		{"map-iter-break-continue", "function main(): i32 { var m = Map { 1: 5, 2: 6, 3: 7, 4: 8 }; var s = 0; for (k, v) in m { if (k == 2) { continue; } if (k == 4) { break; } s = s + v; } return s; }", 12},
		{"map-iter-param", "function sumv(m: Map[i32, i32]): i32 { var s = 0; for (k, v) in m { s = s + v; } return s; } function main(): i32 { var m = Map { 1: 11, 2: 22, 3: 33 }; return sumv(m); }", 66},
		// keys() / values() snapshot the map's columns into fresh arrays.
		{"map-keys-values", "function main(): i32 { var m = Map { 1: 10, 2: 20, 3: 30 }; var ks = m.keys(); var vs = m.values(); var s = 0; for k in ks { s = s + k; } for v in vs { s = s + v; } return s + ks.len(); }", 69},
		{"map-values-index", "function main(): i32 { var m = Map { 5: 10, 6: 20 }; m = m.insert(7, 30); var vs = m.values(); var s = 0; var i = 0; while (i < vs.len()) { s = s + vs[i]; i = i + 1; } return s + vs.len(); }", 63},
		// String-keyed maps (__ssa_smap_*, key compare by content via __streq).
		{"smap-literal-get", "function main(): i32 { var m = Map { \"a\": 10, \"b\": 20, \"c\": 30 }; return m.get_or(\"b\", 0) + m.get_or(\"z\", 7) + m.len(); }", 30},
		{"smap-set-has", "function main(): i32 { var m = Map { \"x\": 1 }; m = m.insert(\"y\", 2); m = m.insert(\"x\", 99); var r = 0; if (m.has(\"x\")) { r = r + m.get_or(\"x\", 0); } if (m.has(\"q\")) { r = r + 1000; } return r + m.get_or(\"y\", 0) + m.len(); }", 103},
		{"smap-content-key", "function main(): i32 { var m = Map { \"foo\": 42 }; var k = \"fo\" + \"o\"; return m.get_or(k, 0); }", 42},
		{"smap-param-delete", "function lookup(m: Map[string, i32], k: string): i32 { return m.get_or(k, 0); } function main(): i32 { var m = Map { \"hi\": 5, \"bye\": 9 }; m.without(\"hi\"); return lookup(m, \"bye\") + m.len(); }", 10},
		{"smap-iter", "function main(): i32 { var m = Map { \"a\": 100, \"b\": 50, \"c\": 30 }; var s = 0; for (k, v) in m { s = s + v + k.len(); } return s; }", 183},
		// delete: removes a key (swap-with-last, count--), missing key no-op,
		// and composes with set / iteration.
		{"map-delete", "function main(): i32 { var m = Map { 1: 10, 2: 20, 3: 30 }; m.without(2); var r = 0; if (m.has(2)) { r = r + 1000; } r = r + m.len() * 100; r = r + m.get_or(1, 0); r = r + m.get_or(3, 0); return r; }", 240},
		{"map-delete-missing", "function main(): i32 { var m = Map { 1: 10, 2: 20 }; m.without(99); return m.len() * 10 + m.get_or(2, 0); }", 40},
		{"map-delete-readd-iter", "function main(): i32 { var m = Map { 1: 10, 2: 20, 3: 30 }; m.without(3); m = m.insert(4, 40); m.without(1); var s = 0; for (k, v) in m { s = s + v; } return s + m.len(); }", 62},
		// Passing arrays to functions: pointer-typed (64-bit) params.
		{"arr-param-sum", "function sum(a: i32[]): i32 { var i = 0; var s = 0; while (i < a.len()) { s = s + a[i]; i = i + 1; } return s; } function main(): i32 { var xs = [5, 10, 15, 20]; return sum(xs); }", 50},
		{"arr-param-two", "function dot2(a: i32[], b: i32[]): i32 { return a[0] * b[0] + a[1] * b[1]; } function main(): i32 { var p = [2, 3]; var q = [10, 20]; return dot2(p, q); }", 80},
		// Strings (byte arrays): byte-sum loop and a string param.
		{"str-byte-sum", "function main(): i32 { var s = \"AAA\"; var i = 0; var t = 0; while (i < s.len()) { t = t + (s[i] as i32); i = i + 1; } return t; }", 195},
		{"str-param", "function slen(s: string): i32 { return s.len(); } function main(): i32 { var s = \"wxyz\"; return slen(s); }", 4},
		// Returning pointers (arrays / strings) from functions.
		{"return-array", "function make(): i32[] { return [10, 20, 30]; } function main(): i32 { var a = make(); return a[1]; }", 20},
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
		// String equality driving dispatch (content comparison via streq).
		{"streq-dispatch", "function kind(s: string): i32 { if (s == \"add\") { return 1; } if (s == \"sub\") { return 2; } return 0; } function main(): i32 { return kind(\"sub\") + 10 * kind(\"add\"); }", 12},
		// enums + match: a variant-dispatching helper (tag + payload fields).
		{"match-area", "struct Circle { r: i32 } struct Square { side: i32 } type Shape = Circle | Square; function area(sh: Shape): i32 { match (sh) { Circle(c) => { return c.r * c.r * 3; }, Square(s) => { return s.side * s.side; } } return 0; } function main(): i32 { var a: Shape = Circle { r: 4 }; var b: Shape = Square { side: 5 }; return area(a) + area(b); }", 73},
		// Struct spread (functional update): non-overridden fields copied from base.
		{"struct-spread", "struct P { x: i32, y: i32, z: i32 } function (p: P) with_y(v: i32): P { return P { ...p, y: v }; } function main(): i32 { var p = P { x: 1, y: 2, z: 3 }; var q = p.with_y(20); return q.x + q.y + q.z; }", 24},
		// f64 floats: `.double` rodata + FP regs (fadd / fcmp+cset / scvtf /
		// fcvtzs / fneg), f64 call ABI (d0… params, d0 result). Results cast to
		// i32 to surface as the exit code.
		{"float-add", "function main(): i32 { var x = 1.5; var y = x + 2.5; return y as i32; }", 4},
		{"float-chain", "function main(): i32 { var x = 1.5; var y = x + 2.5; var z = y * 2.0; return z as i32; }", 8},
		{"float-sub", "function main(): i32 { var a = 5.5; var b = 2.5; return (a - b) as i32; }", 3},
		{"float-div", "function main(): i32 { var a = 9.0; var b = 2.0; return (a / b) as i32; }", 4},
		{"float-neg", "function main(): i32 { var a = 4.0; var b = 0.0 - a; return (0.0 - b) as i32; }", 4},
		{"int-to-float", "function main(): i32 { var n = 7; var x = n as f64; return (x + 0.5) as i32; }", 7},
		{"float-compare-gt", "function main(): i32 { var a = 3.5; if (a > 2.0) { return 1; } return 0; }", 1},
		{"float-compare-le", "function main(): i32 { var a = 2.0; if (a <= 2.0) { return 1; } return 0; }", 1},
		{"float-loop", "function main(): i32 { var sum = 0.0; var i = 0; while (i < 4) { sum = sum + 1.5; i = i + 1; } return sum as i32; }", 6},
		{"float-param", "function half(x: f64): f64 { return x / 2.0; } function main(): i32 { return half(9.0) as i32; }", 4},
		{"float-two-args", "function add(a: f64, b: f64): f64 { return a + b; } function main(): i32 { return add(3.5, 3.5) as i32; }", 7},
		{"float-recursion", "function pow2(n: i32): f64 { if (n <= 0) { return 1.0; } return pow2(n - 1) * 2.0; } function main(): i32 { return (pow2(3) - 2.0) as i32; }", 6},
	}

	// run assembles arm64 `asm` (under qemu) and asserts the exit code.
	run := func(t *testing.T, src string, want int, asm []byte) {
		t.Helper()
		asmPath := filepath.Join(dir, "prog.s")
		binPath := filepath.Join(dir, "prog")
		if err := os.WriteFile(asmPath, asm, 0o644); err != nil {
			t.Fatalf("write asm: %v", err)
		}
		if out, err := exec.Command(gcc, "-static", "-nostdlib", asmPath, "-o", binPath).CombinedOutput(); err != nil {
			t.Fatalf("gcc failed for %q: %v\n%s\n--- asm ---\n%s", src, err, out, asm)
		}
		cmd := runArm64Bin(qemu, binPath)
		_ = cmd.Run()
		if cmd.ProcessState == nil || !cmd.ProcessState.Exited() {
			t.Fatalf("emitted program did not exit normally for %q", src)
		}
		if got := cmd.ProcessState.ExitCode(); got != want {
			t.Errorf("SSA→arm64 of %q = %d, want %d", src, got, want)
		}
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			run(t, tc.src, tc.want, runDriver(t, tc.src, "-target", "arm64"))
		})
	}

	// Re-run every case through the register allocator (-regalloc) — the path
	// the CLI uses. Guards the allocator on arm64 (x12-x15 + the shared
	// liveness fixes) the same way the x86 suite does.
	for _, tc := range cases {
		t.Run("regalloc/"+tc.name, func(t *testing.T) {
			run(t, tc.src, tc.want, runDriver(t, tc.src, "-regalloc", "-target", "arm64"))
		})
	}

	// Scaling: a module with many functions must emit in roughly linear time
	// rather than OOM (the seed-once / balanced-join fix). Mirrors the x86
	// suite; h{i} computes ((x + i%9) * 3) - i%5, main sums h1(2)=8 + h7(3)=28.
	t.Run("scaling-600-functions", func(t *testing.T) {
		var b strings.Builder
		for i := 0; i < 600; i++ {
			fmt.Fprintf(&b, "function h%d(x: i32): i32 { var s = x; s = s + %d; s = s * 3; s = s - %d; return s; }\n", i, i%9, i%5)
		}
		b.WriteString("function main(): i32 { return (h1(2) + h7(3)) % 256; }\n")
		asm := runDriver(t, b.String(), "-target", "arm64")
		if len(asm) == 0 {
			t.Fatalf("emit produced empty output for 600-function module")
		}
		run(t, "scaling-600-functions", 36, asm)
	})

	// Scaling: a single large function must optimise in roughly linear time
	// rather than O(n²) (the inline const_fold + in-place env_put fix). Mirrors
	// the x86 suite; a 400-statement fold chain → the running sum of j%7 mod 256.
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
		asm := runDriver(t, b.String(), "-target", "arm64")
		if len(asm) == 0 {
			t.Fatalf("emit produced empty output for 400-statement function")
		}
		run(t, "scaling-large-function", sum%256, asm)
	})

	// File I/O — read_file / write_file lowered to the arm64 Linux syscall
	// runtime, run under qemu through both the default and -regalloc paths.
	// (qemu-aarch64 user-mode forwards file syscalls to the host FS.)
	for _, mode := range []struct {
		name string
		args []string
	}{{"default", []string{"-target", "arm64"}}, {"regalloc", []string{"-regalloc", "-target", "arm64"}}} {
		mode := mode
		// Round-trip: write then read back and compare via streq, exercising the
		// SSA [len, byte-per-word] string layout end-to-end; a Go-side read
		// confirms the on-disk bytes.
		t.Run("file-io-roundtrip/"+mode.name, func(t *testing.T) {
			ioPath := filepath.Join(dir, "io_roundtrip_"+mode.name+".txt")
			_ = os.Remove(ioPath)
			const content = "hello, fern"
			src := fmt.Sprintf("function main(): i32 { match (write_file(%q, %q)) { Err(e) => { return 1; }, Ok(_) => {} } match (read_file(%q)) { Ok(s) => { if (s == %q) { return 42; } return 2; }, Err(e) => { return 3; } } }", ioPath, content, ioPath, content)
			run(t, "file-io-roundtrip", 42, runDriver(t, src, mode.args...))
			got, err := os.ReadFile(ioPath)
			if err != nil {
				t.Fatalf("write_file did not create %s: %v", ioPath, err)
			}
			if string(got) != content {
				t.Errorf("write_file wrote %q, want %q", got, content)
			}
		})
		// read_file on a file the test pre-wrote: len + first byte = 10 + 'a'.
		t.Run("file-io-read-external/"+mode.name, func(t *testing.T) {
			ioPath := filepath.Join(dir, "io_external_"+mode.name+".txt")
			const content = "abcdefghij"
			if err := os.WriteFile(ioPath, []byte(content), 0o644); err != nil {
				t.Fatalf("seed file: %v", err)
			}
			src := fmt.Sprintf("function main(): i32 { match (read_file(%q)) { Ok(s) => { return s.len() + s[0]; }, Err(e) => { return 0; } } }", ioPath)
			run(t, "file-io-read-external", 10+int('a'), runDriver(t, src, mode.args...))
		})
		// read_file on a missing path → Err.
		t.Run("file-io-read-missing/"+mode.name, func(t *testing.T) {
			ioPath := filepath.Join(dir, "does_not_exist.txt")
			src := fmt.Sprintf("function main(): i32 { match (read_file(%q)) { Ok(s) => { return 0; }, Err(e) => { return 7; } } }", ioPath)
			run(t, "file-io-read-missing", 7, runDriver(t, src, mode.args...))
		})
	}
}
