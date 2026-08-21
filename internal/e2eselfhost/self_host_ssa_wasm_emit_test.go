package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestSelfHostSSAEmitWasm exercises the self-hosted SSA → wasm backend
// (examples/self_host/ssa_wasm.fern): the ssa_wasm_emit_run driver parses a
// program, lowers each function to SSA, optimises it, and prints a WASI core
// module in text format (WAT). For each case the test validates the WAT
// (wasm-tools, when present) and runs it with `wasmtime run`, asserting the
// process exit code equals the program's value — end-to-end proof that the
// full self-hosted pipeline (AST → SSA → optimise → WAT → execute) is
// correct on the wasm backend, making wasm the third consumer of the shared
// SSA IR (after x86-64 and arm64).
//
// Scope mirrors ssa_wasm.fern's subset: the integer ops (const / copy /
// binary / unary / call / phi over ret / br / brif), the heap (alloc /
// load_elem / store_elem) — arrays, strings, structs, tuples, methods,
// i32 maps, struct-union match, push / slice — string build / equality
// (concat / streq), print (bytes + trailing newline via fd_write), closures
// (funcaddr / call_indirect via a function table), and f64 floats (f64
// locals / params / results + ops). The backend now covers the whole SSA
// subset; the cases are a subset of TestSelfHostSSAEmitX86_64's matrix, with
// all wanted values < 126 (wasmtime's WASI exit range).
func TestSelfHostSSAEmitWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host SSA→wasm e2e")
	}
	gcc, runner := x86_64Tooling(t)

	dir := t.TempDir()
	copySelfHostDriver(t, dir, "ssa_wasm_emit_run.fern")
	bin := buildSelfHostBin(t, gcc, dir, "ssa_wasm_emit_run.fern", "ssa_wasm_emit_run")
	wasmtools, _ := exec.LookPath("wasm-tools")

	cases := []struct {
		name string
		src  string
		want int
	}{
		{"const", "function main(): i32 { return 42; }", 42},
		{"arith", "function main(): i32 { return 2 + 3 * 4; }", 14},
		// Option / Result (Some/None/Ok/Err): 2-word tag+payload boxes,
		// constructed + matched (payload bound from word 1).
		{"option-result", "function get(b: boolean): Result[i32] { if (b) { return Ok(42); } return Err(7); } function opt(b: boolean): Option[i32] { if (b) { return Some(5); } return None; } function main(): i32 { var r = 0; match (get(true)) { Ok(v) => { r = r + v; }, Err(e) => { r = r + 100 + e; } } match (get(false)) { Ok(v) => { r = r + v; }, Err(e) => { r = r + e; } } match (opt(true)) { Some(x) => { r = r + x; }, None => { r = r + 1000; } } match (opt(false)) { Some(x) => { r = r + x; }, None => { r = r + 9; } } return r; }", 63},

		{"parens", "function main(): i32 { return (1 + 2) * 3; }", 9},
		{"locals", "function main(): i32 { var x = 10; var y = x - 3; return y * 2; }", 14},
		{"division", "function main(): i32 { return 84 / 2; }", 42},
		{"modulo", "function main(): i32 { return 23 % 5; }", 3},
		{"bitwise", "function main(): i32 { return (6 & 3) | 8; }", 10},
		{"shift", "function main(): i32 { return 1 << 4; }", 16},
		{"xor", "function main(): i32 { return 12 ^ 10; }", 6},
		{"comparison", "function main(): i32 { return 7 > 3; }", 1},
		{"logical-and", "function main(): i32 { var a = 1; var b = 0; if (a > 0 && b == 0) { return 1; } return 0; }", 1},
		{"logical-or", "function main(): i32 { var a = 0; if (a > 5 || a == 0) { return 1; } return 0; }", 1},
		{"unary-not", "function main(): i32 { var x = 5; if (!(x > 9)) { return 1; } return 0; }", 1},
		{"unary-neg", "function main(): i32 { var x = 0 - 7; return 0 - x; }", 7},
		{"if-else", "function main(): i32 { var x = 0; if (5 < 10) { x = 7; } else { x = 9; } return x; }", 7},
		{"early-return", "function main(): i32 { var x = 5; if (x > 3) { return 100; } return x; }", 100},
		{"nested-if", "function main(): i32 { var x = 5; if (x > 0) { if (x > 3) { x = 100; } else { x = 50; } } return x; }", 100},
		{"while-sum", "function main(): i32 { var i = 1; var s = 0; while (i <= 5) { s = s + i; i = i + 1; } return s; }", 15},
		{"while-factorial", "function main(): i32 { var i = 1; var f = 1; while (i <= 5) { f = f * i; i = i + 1; } return f; }", 120},
		{"if-in-loop", "function main(): i32 { var i = 0; var c = 0; while (i < 10) { if (i > 4) { c = c + 1; } i = i + 1; } return c; }", 5},
		{"nested-loop", "function main(): i32 { var i = 0; var t = 0; while (i < 3) { var j = 0; while (j < 3) { t = t + 1; j = j + 1; } i = i + 1; } return t; }", 9},
		{"break", "function main(): i32 { var i = 0; while (i < 100) { if (i == 5) { break; } i = i + 1; } return i; }", 5},
		{"continue", "function main(): i32 { var i = 0; var s = 0; while (i < 10) { i = i + 1; if (i == 5) { continue; } s = s + i; } return s; }", 50},
		// Multi-function: argument passing + call/return + recursion.
		{"call", "function add(a: i32, b: i32): i32 { return a + b; } function main(): i32 { return add(3, 4); }", 7},
		{"call-expr", "function sq(x: i32): i32 { return x * x; } function main(): i32 { return sq(5) + sq(3); }", 34},
		{"recursion", "function fact(n: i32): i32 { if (n <= 1) { return 1; } return n * fact(n - 1); } function main(): i32 { return fact(5); }", 120},
		{"fib", "function fib(n: i32): i32 { if (n < 2) { return n; } return fib(n - 1) + fib(n - 2); } function main(): i32 { var s = 0; var i = 0; while (i < 10) { s = s + fib(i); i = i + 1; } return s; }", 88},
		{"all-return-helper", "function sign(n: i32): i32 { if (n < 0) { return 0 - 1; } else if (n == 0) { return 0; } else { return 1; } } function main(): i32 { return sign(0 - 5) + 10 * sign(7); }", 9},
		// No-capture lambdas lift to top-level functions and are called directly.
		{"lambda-call", "function main(): i32 { var f = function (x: i32): i32 { return x + 1; }; return f(5); }", 6},
		{"lambda-compose", "function main(): i32 { var inc = function (x: i32): i32 { return x + 1; }; var dbl = function (x: i32): i32 { return x * 2; }; return inc(dbl(10)); }", 21},
		// Heap (alloc / load_elem / store_elem): arrays. All wanted values stay
		// below 126 — wasmtime rejects a WASI exit status outside [0,126).
		{"arr-index", "function main(): i32 { var a = [10, 20, 30]; return a[1]; }", 20},
		{"arr-sum-ends", "function main(): i32 { var a = [10, 20, 30]; return a[0] + a[2]; }", 40},
		{"arr-computed-index", "function main(): i32 { var a = [3, 7, 11, 15]; var i = 2; return a[i]; }", 11},
		{"arr-loop-sum", "function main(): i32 { var a = [5, 10, 15, 20, 25]; var i = 0; var s = 0; while (i < 5) { s = s + a[i]; i = i + 1; } return s; }", 75},
		{"arr-len", "function main(): i32 { var a = [10, 20, 30]; return a.len(); }", 3},
		{"arr-with", "function main(): i32 { var a = [1, 2, 3]; a = a.with(1, 20); return a[0] + a[1] + a[2]; }", 24},
		{"cell-get-set", "function main(): i32 { var c: Cell[i32] = cell_new(0); c.set(c.get() + 5); c.set(c.get() * 2); return c.get(); }", 10},
		// Cell[string] — single-pointer string slot, same machinery as i32.
		{"cell-string", "function main(): i32 { var c: Cell[string] = cell_new(\"ab\"); c.set(\"xyz\"); return c.get().len(); }", 3},
		{"for-sum", "function main(): i32 { var a = [5, 10, 15]; var s = 0; for x in a { s = s + x; } return s; }", 30},
		{"for-break", "function main(): i32 { var a = [1, 2, 3, 4, 5]; var s = 0; for x in a { if (x > 3) { break; } s = s + x; } return s; }", 6},
		{"for-continue", "function main(): i32 { var a = [1, 2, 3, 4]; var s = 0; for x in a { if (x == 2) { continue; } s = s + x; } return s; }", 8},
		// Indexed assignment, runtime-sized alloc, push, slice.
		{"set-index-swap", "function main(): i32 { var a = [7, 3]; var t = a[0]; a[0] = a[1]; a[1] = t; return a[0] * 10 + a[1]; }", 37},
		{"set-index-compound", "function main(): i32 { var a = [10, 20, 30]; a[0] += 5; a[1] -= 4; a[2] *= 2; return a[0] + a[1] + a[2]; }", 91},
		{"new-array-fixed", "function main(): i32 { var b = __new_array(3); b[0] = 10; b[1] = 20; b[2] = 30; return b[0] + b[1] + b[2] + b.len(); }", 63},
		{"array-push", "function main(): i32 { var a = [1, 2]; a = a.append(3); a = a.append(4); return a[0] + a[1] + a[2] + a[3] + a.len(); }", 14},
		{"slice-array", "function main(): i32 { var a = [10, 20, 30, 40, 50]; var b = a[1:4]; return b[0] + b[1] + b[2] + b.len(); }", 93},
		{"slice-empty", "function main(): i32 { var a = [7, 8, 9]; var b = a[0:0]; return b.len() + a[1]; }", 8},
		// Open-ended high bound `x[lo:]` (parser desugars to `x.len()`).
		{"slice-open-array", "function main(): i32 { var a = [10, 20, 30, 40, 50]; var b = a[2:]; return b[0] + b[1] + b[2] + b.len(); }", 123},
		{"slice-open-string-eq", "function main(): i32 { var s = \"as_f64\"; if (s[3:] == \"f64\") { return 7; } return 0; }", 7},
		// Arrays across calls; returning arrays.
		{"arr-param-sum", "function sum(a: i32[]): i32 { var i = 0; var s = 0; while (i < a.len()) { s = s + a[i]; i = i + 1; } return s; } function main(): i32 { var xs = [5, 10, 15, 20]; return sum(xs); }", 50},
		{"return-array", "function make(): i32[] { return [10, 20, 30]; } function main(): i32 { var a = make(); return a[1]; }", 20},
		{"return-array-len", "function mk(): i32[] { return [1, 2, 3, 4]; } function main(): i32 { return mk().len(); }", 4},
		// Strings (byte arrays): index, byte loop, string param.
		{"str-len", "function main(): i32 { var s = \"hello\"; return s.len(); }", 5},
		{"str-param", "function slen(s: string): i32 { return s.len(); } function main(): i32 { var s = \"wxyz\"; return slen(s); }", 4},
		{"str-index", "function main(): i32 { var s = \"hello\"; return s[0] as i32; }", 104},
		// Structs: i32 + pointer fields, params, returns, methods.
		{"struct-sum", "struct Point { x: i32, y: i32 } function main(): i32 { var p = Point { x: 7, y: 9 }; return p.x + p.y; }", 16},
		{"struct-array-field", "struct Box { tag: i32, data: i32[] } function main(): i32 { var b = Box { tag: 1, data: [10, 20, 30] }; return b.data[1] + b.tag; }", 21},
		{"struct-param", "struct Point { x: i32, y: i32 } function dist(p: Point): i32 { return p.x + p.y; } function main(): i32 { var p = Point { x: 3, y: 4 }; return dist(p); }", 7},
		{"struct-return", "struct Point { x: i32, y: i32 } function mk(): Point { return Point { x: 5, y: 6 }; } function main(): i32 { var p: Point = mk(); return p.x + p.y; }", 11},
		{"method-basic", "struct Counter { n: i32 } function (c: Counter) get(): i32 { return c.n; } function (c: Counter) plus(d: i32): i32 { return c.n + d; } function main(): i32 { var c = Counter { n: 40 }; return c.get() + c.plus(2) - c.n; }", 42},
		{"method-chained", "struct Box { v: i32 } function (b: Box) bump(): Box { return Box { v: b.v + 1 }; } function main(): i32 { var b = Box { v: 10 }; var c = b.bump().bump(); return c.v; }", 12},
		// Tuples.
		{"tuple-pair", "function main(): i32 { var t = (3, 4); return t.0 + t.1; }", 7},
		{"tuple-destructure", "function main(): i32 { var (a, b) = (5, 6); return a + b; }", 11},
		// Enums + struct-union match.
		{"match-area", "struct Circle { r: i32 } struct Square { side: i32 } type Shape = Circle | Square; function area(sh: Shape): i32 { match (sh) { Circle(c) => { return c.r * c.r * 3; }, Square(s) => { return s.side * s.side; } } return 0; } function main(): i32 { var a: Shape = Circle { r: 4 }; var b: Shape = Square { side: 5 }; return area(a) + area(b); }", 73},
		// i32-keyed maps (open-addressing helpers built from heap + call ops).
		{"map-literal-get", "function main(): i32 { var m = Map { 1: 40, 2: 50, 3: 60 }; return m.get_or(2, 0) + m.get_or(9, 7) + m.len(); }", 60},
		{"map-iter-sum", "function main(): i32 { var m = Map { 1: 10, 2: 20 }; var s = 0; for (k, v) in m { s = s + k + v; } return s; }", 33},
		// String build (concat) + equality (streq) via the runtime helpers.
		{"concat-len", "function main(): i32 { var a = \"foo\"; var b = \"bar\"; var c = a + b; return c.len(); }", 6},
		{"concat-content", "function main(): i32 { var c = \"ab\" + \"cd\"; if (c == \"abcd\") { return 1; } return 0; }", 1},
		{"concat-chained", "function main(): i32 { var s = \"a\" + \"b\" + \"c\" + \"de\"; return s.len(); }", 5},
		{"streq-dispatch", "function kind(s: string): i32 { if (s == \"add\") { return 1; } if (s == \"sub\") { return 2; } return 0; } function main(): i32 { return kind(\"sub\") + 10 * kind(\"add\"); }", 12},
		{"streq-content-key", "function main(): i32 { var k = \"fo\" + \"o\"; if (k == \"foo\") { return 7; } return 0; }", 7},
		{"call-result-string", "function greet(): string { return \"hello\"; } function main(): i32 { return greet().len() + (greet() + \"!\").len(); }", 11},
		{"string-array-concat", "function main(): i32 { var a = [\"foo\", \"bar\"]; var c = a[0] + a[1]; return c.len(); }", 6},
		// print (bytes + trailing newline via fd_write) — exit code checked here;
		// the stdout/newline contract is pinned by the CLI test's emit-ssa-wasm.
		{"print-then-return", "function main(): i32 { print(\"hi\"); return 7; }", 7},
		// Closures via the function table (funcaddr / call_indirect). No-capture
		// function values go through an env-dropping wrapper; capturing ones take
		// __env directly. Covers indirect calls, fn-by-name, predicates, and
		// returned / escaping / capturing closures.
		{"lambda-indirect", "function apply(f: (i32) => i32, x: i32): i32 { return f(x); } function main(): i32 { var inc = function (n: i32): i32 { return n + 1; }; return apply(inc, 41); }", 42},
		{"lambda-indirect-dispatch", "function apply2(f: (i32) => i32, x: i32): i32 { return f(x) + f(x + 1); } function main(): i32 { var dbl = function (n: i32): i32 { return n * 2; }; var sq = function (n: i32): i32 { return n * n; }; return apply2(dbl, 10) + apply2(sq, 3); }", 67},
		{"lambda-indirect-loop", "function run(f: (i32) => i32): i32 { var s = 0; var i = 0; while (i < 4) { s = s + f(i); i = i + 1; } return s; } function main(): i32 { var t = function (n: i32): i32 { return n * 10; }; return run(t); }", 60},
		{"fn-value-by-name", "function work(): i32 { return 42; } function run(f: () => i32): i32 { return f(); } function main(): i32 { return run(work); }", 42},
		{"fn-value-predicate", "function is_big(n: i32): i32 { if (n > 10) { return 1; } return 0; } function count_if(a: i32[], pred: (i32) => i32): i32 { var c = 0; for x in a { if (pred(x) == 1) { c = c + 1; } } return c; } function main(): i32 { var a = [5, 20, 8, 30, 15]; return count_if(a, is_big); }", 3},
		{"closure-returned", "function maker(): (i32) => i32 { var f = function (n: i32): i32 { return n + 100; }; return f; } function main(): i32 { var g = maker(); return g(5); }", 105},
		{"closure-escape-arg", "function apply(f: (i32) => i32, x: i32): i32 { return f(x); } function main(): i32 { var k = 100; var add_k = function (n: i32): i32 { return n + k; }; return apply(add_k, 5); }", 105},
		{"closure-escape-return", "function adder(a: i32): (i32) => i32 { var f = function (b: i32): i32 { return a + b; }; return f; } function main(): i32 { var add10 = adder(10); var add20 = adder(20); return add10(5) + add20(7); }", 42},
		{"closure-capture-multicall", "function main(): i32 { var k = 10; var f = function (x: i32): i32 { return x + k; }; return f(1) + f(2); }", 23},
		// f64 floats: f64 locals/params/results map to wasm f64 locals + ops
		// (f64.add / f64.lt / f64.convert_i32_s / i32.trunc_f64_s). Results cast
		// to i32 to surface as the exit code.
		{"float-add", "function main(): i32 { var x = 1.5; var y = x + 2.5; return y as i32; }", 4},
		{"float-chain", "function main(): i32 { var x = 1.5; var y = x + 2.5; var z = y * 2.0; return z as i32; }", 8},
		{"float-compare", "function main(): i32 { var a = 3.5; if (a > 2.0) { return 1; } return 0; }", 1},
		{"int-to-float", "function main(): i32 { var n = 7; var x = n as f64; return (x + 0.5) as i32; }", 7},
		{"float-loop", "function main(): i32 { var sum = 0.0; var i = 0; while (i < 4) { sum = sum + 1.5; i = i + 1; } return sum as i32; }", 6},
		{"float-neg", "function main(): i32 { var a = 4.0; var b = 0.0 - a; return (0.0 - b) as i32; }", 4},
		{"float-param", "function half(x: f64): f64 { return x / 2.0; } function main(): i32 { return half(9.0) as i32; }", 4},
		{"float-two-args", "function add(a: f64, b: f64): f64 { return a + b; } function main(): i32 { return add(3.5, 3.5) as i32; }", 7},
		{"float-recursion", "function pow2(n: i32): f64 { if (n <= 0) { return 1.0; } return pow2(n - 1) * 2.0; } function main(): i32 { return (pow2(3) - 2.0) as i32; }", 6},
		// Struct spread (functional update).
		{"struct-spread", "struct P { x: i32, y: i32, z: i32 } function (p: P) with_y(v: i32): P { return P { ...p, y: v }; } function main(): i32 { var p = P { x: 1, y: 2, z: 3 }; var q = p.with_y(20); return q.x + q.y + q.z; }", 24},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			emit := runX86_64Bin(runner, bin)
			emit.Stdin = strings.NewReader(tc.src)
			wat, err := emit.Output()
			if err != nil {
				t.Fatalf("emit driver failed for %q: %v", tc.src, err)
			}
			watPath := filepath.Join(dir, "prog.wat")
			if err := os.WriteFile(watPath, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			if wasmtools != "" {
				if out, err := exec.Command(wasmtools, "validate", watPath).CombinedOutput(); err != nil {
					t.Fatalf("wasm-tools validate failed for %q: %v\n%s\n--- WAT ---\n%s", tc.src, err, out, wat)
				}
			}
			cmd := exec.Command("wasmtime", "run", watPath)
			_ = cmd.Run()
			if cmd.ProcessState == nil || !cmd.ProcessState.Exited() {
				t.Fatalf("wasm program did not exit normally for %q", tc.src)
			}
			if got := cmd.ProcessState.ExitCode(); got != tc.want {
				t.Errorf("SSA→wasm of %q = %d, want %d\n--- WAT ---\n%s", tc.src, got, tc.want, wat)
			}
		})
	}
}
