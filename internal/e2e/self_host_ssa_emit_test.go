package e2e

import (
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
	for _, name := range []string{"lexer.fern", "parser.fern", "ssa.fern", "ssa_x86.fern", "ssa_arm64.fern", "ssa_emit_run.fern"} {
		src, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), src, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	bin := buildSelfHostBin(t, gcc, dir, "ssa_emit_run.fern", "ssa_emit_run")

	cases := []struct {
		name string
		src  string
		want int
	}{
		{"const", "function main(): i32 { return 42; }", 42},
		{"arith", "function main(): i32 { return 2 + 3 * 4; }", 14},
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
		// Multi-function: System V argument passing + call/return.
		{"call", "function add(a: i32, b: i32): i32 { return a + b; } function main(): i32 { return add(3, 4); }", 7},
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
		// __new_array(n): runtime-sized allocation (alloc op size in args[0]).
		{"new-array-fixed", "function main(): i32 { var b = __new_array(3); b[0] = 10; b[1] = 20; b[2] = 30; return b[0] + b[1] + b[2] + b.len(); }", 63},
		{"new-array-dynamic", "function main(): i32 { var n = 5; var b = __new_array(n); var i = 0; while (i < n) { b[i] = i * i; i = i + 1; } var s = 0; var j = 0; while (j < b.len()) { s = s + b[j]; j = j + 1; } return s; }", 30},
		// arr.push(x) → __ssa_arr_push helper (copy into a fresh __new_array,
		// append). Returns the new array; injected only when called.
		{"array-push", "function main(): i32 { var a = [1, 2]; a = a.push(3); a = a.push(4); return a[0] + a[1] + a[2] + a[3] + a.len(); }", 14},
		{"array-push-loop", "function main(): i32 { var a = [0]; var i = 1; while (i <= 5) { a = a.push(i * i); i = i + 1; } var s = 0; var j = 0; while (j < a.len()) { s = s + a[j]; j = j + 1; } return s; }", 55},
		{"array-push-for", "function main(): i32 { var a = [10]; a = a.push(20); a = a.push(30); var s = 0; for x in a { s = s + x; } return s; }", 60},
		{"array-push-string", "function main(): i32 { var a = [\"ab\"]; a = a.push(\"cde\"); return a[0].len() + a[1].len() + a.len(); }", 7},
		// a[lo:hi] slicing → __ssa_arr_slice (fresh array holding a[lo..hi-1]);
		// a string slice is a substring (a string is a byte array).
		{"slice-array", "function main(): i32 { var a = [10, 20, 30, 40, 50]; var b = a[1:4]; return b[0] + b[1] + b[2] + b.len(); }", 93},
		{"slice-for", "function main(): i32 { var a = [1, 2, 3, 4, 5, 6]; var sum = 0; var b = a[2:5]; for x in b { sum = sum + x; } return sum; }", 12},
		{"slice-empty", "function main(): i32 { var a = [7, 8, 9]; var b = a[0:0]; return b.len() + a[1]; }", 8},
		{"slice-string-eq", "function main(): i32 { var s = \"hello\"; if (s[1:4] == \"ell\") { return 7; } return 0; }", 7},
		{"slice-string-len", "function main(): i32 { var s = \"hello world\"; var a = s[0:5]; var b = s[6:11]; return a.len() + b.len(); }", 10},
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
		// map_new_i32(8).set(…)…; the lowering routes the constructor + the
		// set/get_or/has/len methods to injected association-array helpers
		// (__ssa_map_*), emitted only when a program references a map.
		{"map-literal-get", "function main(): i32 { var m = Map { 1: 40, 2: 50, 3: 60 }; return m.get_or(2, 0) + m.get_or(9, 7) + m.len(); }", 60},
		{"map-has", "function main(): i32 { var m = Map { 5: 1, 7: 1 }; var r = 0; if (m.has(5)) { r = r + 10; } if (m.has(6)) { r = r + 100; } if (m.has(7)) { r = r + 1; } return r; }", 11},
		// set after construction: insert a new key, update an existing key —
		// the buffer is mutated in place (fixed capacity, no realloc).
		{"map-set-update", "function main(): i32 { var m = Map { 1: 10 }; m.set(2, 20); m.set(1, 99); return m.get_or(1, 0) + m.get_or(2, 0) + m.len(); }", 121},
		{"map-loop-build", "function main(): i32 { var m = Map { 0: 0 }; var i = 1; while (i <= 5) { m.set(i, i * i); i = i + 1; } return m.get_or(3, 0) + m.get_or(5, 0) + m.len(); }", 40},
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
		{"map-iter-built", "function main(): i32 { var m = Map { 0: 0 }; var i = 1; while (i <= 4) { m.set(i, i * 10); i = i + 1; } var sum = 0; for (k, v) in m { sum = sum + v; } return sum; }", 100},
		// break / continue inside the iteration body.
		{"map-iter-break-continue", "function main(): i32 { var m = Map { 1: 5, 2: 6, 3: 7, 4: 8 }; var s = 0; for (k, v) in m { if (k == 2) { continue; } if (k == 4) { break; } s = s + v; } return s; }", 12},
		// Iterating a Map passed across a call.
		{"map-iter-param", "function sumv(m: Map[i32, i32]): i32 { var s = 0; for (k, v) in m { s = s + v; } return s; } function main(): i32 { var m = Map { 1: 11, 2: 22, 3: 33 }; return sumv(m); }", 66},
		// keys() / values() snapshot a map's columns into fresh __new_array
		// arrays (now possible with dynamic allocation): iterate / index them.
		{"map-keys-values", "function main(): i32 { var m = Map { 1: 10, 2: 20, 3: 30 }; var ks = m.keys(); var vs = m.values(); var s = 0; for k in ks { s = s + k; } for v in vs { s = s + v; } return s + ks.len(); }", 69},
		{"map-values-index", "function main(): i32 { var m = Map { 5: 10, 6: 20 }; m.set(7, 30); var vs = m.values(); var s = 0; var i = 0; while (i < vs.len()) { s = s + vs[i]; i = i + 1; } return s + vs.len(); }", 63},
		{"map-keys-after-delete", "function main(): i32 { var m = Map { 9: 1 }; m.delete(9); var ks = m.keys(); return ks.len() + 42; }", 42},
		// String-keyed maps: `Map { "a": … }` → map_new().set()… → the
		// __ssa_smap_* helpers, which compare keys by content (__streq) rather
		// than pointer. Same buffer layout as i32, so len / iteration reuse the
		// i32 helpers; the value type is i32.
		{"smap-literal-get", "function main(): i32 { var m = Map { \"a\": 10, \"b\": 20, \"c\": 30 }; return m.get_or(\"b\", 0) + m.get_or(\"z\", 7) + m.len(); }", 30},
		{"smap-set-has", "function main(): i32 { var m = Map { \"x\": 1 }; m.set(\"y\", 2); m.set(\"x\", 99); var r = 0; if (m.has(\"x\")) { r = r + m.get_or(\"x\", 0); } if (m.has(\"q\")) { r = r + 1000; } return r + m.get_or(\"y\", 0) + m.len(); }", 103},
		// Content comparison: the lookup key is built at runtime (concat), so
		// it's a different pointer than the stored literal — must still match.
		{"smap-content-key", "function main(): i32 { var m = Map { \"foo\": 42 }; var k = \"fo\" + \"o\"; return m.get_or(k, 0); }", 42},
		{"smap-param-delete", "function lookup(m: Map[string, i32], k: string): i32 { return m.get_or(k, 0); } function main(): i32 { var m = Map { \"hi\": 5, \"bye\": 9 }; m.delete(\"hi\"); return lookup(m, \"bye\") + m.len(); }", 10},
		{"smap-iter", "function main(): i32 { var m = Map { \"a\": 100, \"b\": 50, \"c\": 30 }; var s = 0; for (k, v) in m { s = s + v + k.len(); } return s; }", 183},
		// delete: removes a key (swap-with-last, count--), missing key is a
		// no-op, and delete composes with set / iteration.
		{"map-delete", "function main(): i32 { var m = Map { 1: 10, 2: 20, 3: 30 }; m.delete(2); var r = 0; if (m.has(2)) { r = r + 1000; } r = r + m.len() * 100; r = r + m.get_or(1, 0); r = r + m.get_or(3, 0); return r; }", 240},
		{"map-delete-missing", "function main(): i32 { var m = Map { 1: 10, 2: 20 }; m.delete(99); return m.len() * 10 + m.get_or(2, 0); }", 40},
		{"map-delete-readd-iter", "function main(): i32 { var m = Map { 1: 10, 2: 20, 3: 30 }; m.delete(3); m.set(4, 40); m.delete(1); var s = 0; for (k, v) in m { s = s + v; } return s + m.len(); }", 62},
		// Passing arrays to functions: pointer-typed (64-bit) params.
		{"arr-param-index", "function get(a: i32[], i: i32): i32 { return a[i]; } function main(): i32 { var xs = [10, 20, 30]; return get(xs, 1); }", 20},
		{"arr-param-sum", "function sum(a: i32[]): i32 { var i = 0; var s = 0; while (i < a.len()) { s = s + a[i]; i = i + 1; } return s; } function main(): i32 { var xs = [5, 10, 15, 20]; return sum(xs); }", 50},
		{"arr-param-two", "function dot2(a: i32[], b: i32[]): i32 { return a[0] * b[0] + a[1] * b[1]; } function main(): i32 { var p = [2, 3]; var q = [10, 20]; return dot2(p, q); }", 80},
		// Strings (byte arrays): index, byte-sum loop, and a string param.
		{"str-byte-sum", "function main(): i32 { var s = \"AAA\"; var i = 0; var t = 0; while (i < s.len()) { t = t + s[i]; i = i + 1; } return t; }", 195},
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
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Run the driver to produce assembly on stdout.
			emit := exec.Command(bin)
			emit.Stdin = strings.NewReader(tc.src)
			asm, err := emit.Output()
			if err != nil {
				t.Fatalf("emit driver failed for %q: %v", tc.src, err)
			}
			asmPath := filepath.Join(dir, "prog.s")
			binPath := filepath.Join(dir, "prog")
			if err := os.WriteFile(asmPath, asm, 0o644); err != nil {
				t.Fatalf("write asm: %v", err)
			}
			if out, err := exec.Command(gcc, "-static", "-nostdlib", "-no-pie", asmPath, "-o", binPath).CombinedOutput(); err != nil {
				t.Fatalf("gcc failed for %q: %v\n%s\n--- asm ---\n%s", tc.src, err, out, asm)
			}
			cmd := exec.Command(binPath)
			_ = cmd.Run()
			if cmd.ProcessState == nil || !cmd.ProcessState.Exited() {
				t.Fatalf("emitted program did not exit normally for %q", tc.src)
			}
			if got := cmd.ProcessState.ExitCode(); got != tc.want {
				t.Errorf("SSA→x86-64 of %q = %d, want %d", tc.src, got, tc.want)
			}
		})
	}
}
