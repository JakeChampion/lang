package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestSelfHostIRLowerRoundTrip exercises the self-hosted AST -> stack-IR
// lowering (examples/self_host/irlower.fern, slice 2 of the IR rebuild —
// docs/RC-PERCEUS-SELF-HOST-IR-REBUILD.md). The irlower_run driver parses a
// program, lowers `main` to the stack IR via lower_func, evaluates the Op[]
// with the IR interpreter (eval_ops), and returns the result as its exit
// code. Each case asserts AST -> IR -> eval reproduces the program's value —
// the IR analogue of the ssa_run round-trip, proving the lowering +
// interpreter are semantics-preserving on the straight-line i32 subset.
// Constructs outside the subset (control flow, calls, f64 signatures) make
// lower_func bail (exit 200). (f64 LOCALS lower now; this round-trip
// evaluator is integer-only, so f64-local run coverage lives in the IR-path
// differential suites.)
//
// The driver is built natively via the Go x86-64 backend and fed each
// program on stdin; its exit code is the IR-computed result.
func TestSelfHostIRLowerRoundTrip(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("irlower_run driver runs natively; skipping under an exec runner")
	}
	dir := t.TempDir()
	for _, name := range []string{"lexer.fern", "parser.fern", "util.fern", "astwalk.fern", "ir.fern", "irlower.fern", "irlower_run.fern"} {
		src, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), src, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	bin := buildSelfHostBin(t, gcc, dir, "irlower_run.fern", "irlower_run")

	run := func(t *testing.T, src string, args ...string) int {
		t.Helper()
		cmd := exec.Command(bin, args...)
		cmd.Stdin = strings.NewReader(src)
		_ = cmd.Run()
		if cmd.ProcessState == nil || !cmd.ProcessState.Exited() {
			t.Fatalf("irlower_run did not exit normally for %q (args %v)", src, args)
		}
		return cmd.ProcessState.ExitCode()
	}

	cases := []struct {
		name string
		src  string
		want int
	}{
		// Straight-line i32: literals, arithmetic, precedence, locals,
		// reassignment, the comparison + bitwise operators, unary ! and -.
		{"const", "function main(): i32 { return 42; }", 42},
		{"arith-precedence", "function main(): i32 { return 2 + 3 * 4; }", 14},
		{"parens", "function main(): i32 { return (1 + 2) * 3; }", 9},
		{"locals", "function main(): i32 { var x = 2 + 3 * 4; var y = x - 5; return y * 2; }", 18},
		{"reassign", "function main(): i32 { var x = 5; x = x + 3; return x; }", 8},
		{"nested-locals", "function main(): i32 { var a = 3; var b = a * a; var c = b + a; return c; }", 12},
		{"modulo", "function main(): i32 { return 23 % 5; }", 3},
		{"division", "function main(): i32 { return 84 / 2; }", 42},
		{"comparison", "function main(): i32 { return 5 < 10; }", 1},
		{"comparison-false", "function main(): i32 { return 10 < 5; }", 0},
		{"ge", "function main(): i32 { return 7 >= 7; }", 1},
		{"ne", "function main(): i32 { return 3 != 4; }", 1},
		{"bitwise", "function main(): i32 { return (6 & 3) | 8; }", 10},
		{"shift", "function main(): i32 { return 1 << 4; }", 16},
		{"shift-right", "function main(): i32 { return 240 >> 2; }", 60},
		{"xor", "function main(): i32 { return 12 ^ 10; }", 6},
		{"unary-not", "function main(): i32 { return !(5 > 10); }", 1},
		{"unary-not-false", "function main(): i32 { return !(5 < 10); }", 0},
		{"unary-minus-net-positive", "function main(): i32 { var x = 10; return -x + 13; }", 3},
		{"chained", "function main(): i32 { var a = 1; var b = a + 1; var c = b + 1; var d = c + 1; return d * 10; }", 40},
		// Structured control flow: if / else (slice 4) lowers to if/else/end.
		{"if-taken", "function main(): i32 { var x = 1; if (5 < 10) { x = 7; } return x; }", 7},
		{"if-not-taken", "function main(): i32 { var x = 1; if (10 < 5) { x = 7; } return x; }", 1},
		{"if-else-then", "function main(): i32 { var x = 0; if (1 < 2) { x = 3; } else { x = 9; } return x; }", 3},
		{"if-else-else", "function main(): i32 { var x = 0; if (2 < 1) { x = 3; } else { x = 9; } return x; }", 9},
		{"early-return", "function main(): i32 { var x = 5; if (x > 3) { return 100; } return x; }", 100},
		{"no-early-return", "function main(): i32 { var x = 2; if (x > 3) { return 100; } return x; }", 2},
		{"nested-if", "function main(): i32 { var x = 5; if (x > 0) { if (x > 3) { x = 100; } else { x = 50; } } return x; }", 100},
		{"if-chain", "function main(): i32 { var n = 2; var r = 0; if (n == 1) { r = 10; } else { if (n == 2) { r = 20; } else { r = 30; } } return r; }", 20},
		// Loops (slice 5): while lowers to block/loop/br/br_if.
		{"while-count", "function main(): i32 { var i = 0; while (i < 3) { i = i + 1; } return i; }", 3},
		{"while-sum", "function main(): i32 { var i = 1; var s = 0; while (i <= 5) { s = s + i; i = i + 1; } return s; }", 15},
		{"while-factorial", "function main(): i32 { var i = 1; var f = 1; while (i <= 5) { f = f * i; i = i + 1; } return f; }", 120},
		{"while-zero-iters", "function main(): i32 { var i = 10; while (i < 5) { i = i + 100; } return i; }", 10},
		{"if-in-loop", "function main(): i32 { var i = 0; var c = 0; while (i < 10) { if (i > 4) { c = c + 1; } i = i + 1; } return c; }", 5},
		{"nested-loop", "function main(): i32 { var i = 0; var t = 0; while (i < 3) { var j = 0; while (j < 3) { t = t + 1; j = j + 1; } i = i + 1; } return t; }", 9},
		// Range-for `for i in LOW..HIGH` (closes #2699 self-host IR slice):
		// the parser desugars to a synthetic __range(LOW, HIGH) call, which
		// irlower lowers to a counted loop over the half-open interval. A
		// correct exit code (not the 200 bail) proves the IR path handled it.
		{"range-sum", "function main(): i32 { var s = 0; for i in 0..5 { s = s + i; } return s; }", 10},
		{"range-count", "function main(): i32 { var c = 0; for i in 0..10 { c = c + 1; } return c; }", 10},
		{"range-nonzero-low", "function main(): i32 { var s = 0; for i in 3..7 { s = s + i; } return s; }", 18},
		{"range-empty", "function main(): i32 { var c = 7; for i in 5..5 { c = c + 1; } return c; }", 7},
		{"range-reversed", "function main(): i32 { var c = 9; for i in 9..3 { c = c + 1; } return c; }", 9},
		{"range-hi-expr", "function main(): i32 { var n = 4; var s = 0; for i in 1..n + 1 { s = s + i; } return s; }", 10},
		{"range-lo-expr", "function main(): i32 { var b = 2; var s = 0; for i in b..b + 3 { s = s + i; } return s; }", 9},
		{"range-nested", "function main(): i32 { var t = 0; for i in 0..3 { for j in 0..3 { t = t + 1; } } return t; }", 9},
		{"range-body-if", "function main(): i32 { var c = 0; for i in 0..10 { if (i > 4) { c = c + 1; } } return c; }", 5},
		{"range-hi-once", "function side(): i32 { return 4; } function main(): i32 { var c = 0; for i in 0..side() { c = c + 1; } return c; }", 4},
		// `loop { }` infinite loop (closes #2676 loop-form, self-host IR slice):
		// desugars to `while (true)`, so it rides the existing StmtWhile lowering
		// (block/loop/br_if) — break/continue work as in any while loop.
		{"loop-break", "function main(): i32 { var i = 0; loop { i = i + 1; if (i >= 7) { break; } } return i; }", 7},
		{"loop-continue", "function main(): i32 { var i = 0; var s = 0; loop { i = i + 1; if (i > 10) { break; } if (i % 2 == 1) { continue; } s = s + i; } return s; }", 30},
		// Direct calls + multi-function programs + recursion (slice 6).
		{"simple-call", "function helper(): i32 { return 5; } function main(): i32 { return helper(); }", 5},
		{"call-args", "function add(a: i32, b: i32): i32 { return a + b; } function main(): i32 { return add(4, 5); }", 9},
		{"call-compute", "function compute(a: i32): i32 { var b = a * 2; var c = b + 1; return c; } function main(): i32 { return compute(5); }", 11},
		{"factorial", "function fact(n: i32): i32 { if (n <= 1) { return 1; } return n * fact(n - 1); } function main(): i32 { return fact(5); }", 120},
		{"fib", "function fib(n: i32): i32 { if (n < 2) { return n; } return fib(n - 1) + fib(n - 2); } function main(): i32 { return fib(8); }", 21},
		{"mutual-recursion", "function is_even(n: i32): i32 { if (n == 0) { return 1; } return is_odd(n - 1); } function is_odd(n: i32): i32 { if (n == 0) { return 0; } return is_even(n - 1); } function main(): i32 { return is_even(6); }", 1},
		{"loop-call", "function sq(x: i32): i32 { return x * x; } function main(): i32 { var i = 1; var s = 0; while (i <= 4) { s = s + sq(i); i = i + 1; } return s; }", 30},
		// i32 array literals + indexing (slice 8).
		{"arr-index", "function main(): i32 { var a = [10, 20, 30]; return a[0] + a[2]; }", 40},
		{"arr-computed-index", "function main(): i32 { var a = [3, 7, 11, 15]; var i = 2; return a[i]; }", 11},
		{"arr-loop-sum", "function main(): i32 { var a = [5, 10, 15, 20, 25]; var i = 0; var s = 0; while (i < 5) { s = s + a[i]; i = i + 1; } return s; }", 75},
		// .len() + index-assignment (slice 9).
		{"arr-len", "function main(): i32 { var a = [10, 20, 30]; return a.len(); }", 3},
		{"set-index", "function main(): i32 { var a = [10, 20, 30]; a[1] = 99; return a[0] + a[1] + a[2]; }", 139},
		{"set-index-swap", "function main(): i32 { var a = [7, 3]; var t = a[0]; a[0] = a[1]; a[1] = t; return a[0] * 10 + a[1]; }", 37},
		// RC counting (slice 10): __rc reads the header; aliasing increments it.
		{"rc-fresh", "function main(): i32 { var a = [10, 20, 30]; return __rc(a); }", 1},
		{"rc-one-alias", "function main(): i32 { var a = [10, 20, 30]; var b = a; return __rc(a); }", 2},
		{"rc-two-aliases", "function main(): i32 { var a = [1, 2]; var b = a; var c = a; return __rc(a); }", 3},
		// Exit dec-sweep + underflow detector (slice 11).
		{"rc-clean-no-underflow", "function main(): i32 { var a = [10, 20, 30]; var b = a; return __rc_underflow(); }", 0},
		{"rc-balanced-manual-dec", "function main(): i32 { var a = [1, 2, 3]; var b = a; __rc_dec(a); __rc_dec(b); return __rc_underflow(); }", 0},
		{"rc-detects-overrelease", "function main(): i32 { var a = [1, 2, 3]; __rc_dec(a); __rc_dec(a); return __rc_underflow(); }", 1},
		// Free path + reuse (slice 12): a freed block is reused by a same-size
		// alloc (a - b == 0); values stay correct.
		{"reuse-same-block", "function main(): i32 { var a = [1, 2, 3]; __rc_dec(a); var b = [4, 5, 6]; var d = a - b; if (d == 0) { return 1; } return 0; }", 1},
		{"reuse-values-ok", "function main(): i32 { var a = [1, 2, 3]; __rc_dec(a); var b = [4, 5, 6]; return b[0] + b[1] + b[2]; }", 15},
		{"no-reuse-when-live", "function main(): i32 { var a = [1, 2, 3]; var b = [4, 5, 6]; var d = a - b; if (d == 0) { return 1; } return 0; }", 0},
		// Reclamation bounds peak memory: __heap_used() = bytes bumped; freed
		// blocks are reused, not re-bumped.
		{"heap-reuse-bounded", "function main(): i32 { var a = [1, 2, 3]; __rc_dec(a); var b = [4, 5, 6]; __rc_dec(b); var c = [7, 8, 9]; return __heap_used(); }", 20},
		{"heap-live-grows", "function main(): i32 { var a = [1, 2, 3]; var b = [4, 5, 6]; var c = [7, 8, 9]; return __heap_used(); }", 60},
		// f64 LOCALS / arithmetic / comparison / i32<->f64 casts now lower; f64
		// modulo has no float form, so it's still out of subset -> lower bails
		// (200). (f64 lower+run coverage lives in self_host_ir_x86_test / the
		// IR-path differential suites; the round-trip evaluator here is
		// integer-only, so it can only exercise float programs that bail.)
		{"f64-mod-bails", "function main(): i32 { var x: f64 = 5.5; var y: f64 = x % 2.0; if (y > 0.0) { return 1; } return 2; }", 200},
		// Scalar (i32 / boolean) structs: a literal lowers to struct_make and
		// field reads/writes to struct_get / struct_set, which the round-trip
		// evaluator now models as a [rc, f0, f1, …] box on its word-heap (leak-
		// only, like every backend's AST path). Before this the evaluator had no
		// case for these ops and fell into the binary-op default, underflowing
		// the stack -> SIGABRT instead of a clean value. A 0-field struct_make is
		// a bare enum variant box (`E.A`).
		{"struct-field-read", "struct P { x: i32, y: i32 } function main(): i32 { var p = P { x: 7, y: 35 }; return p.x + p.y; }", 42},
		{"struct-lit-unused", "struct P { x: i32 } function main(): i32 { var p = P { x: 7 }; return 5; }", 5},
		{"struct-nested", "struct P { x: i32 } struct Q { p: P } function main(): i32 { var q = Q { p: P { x: 9 } }; return q.p.x; }", 9},
		{"struct-bool-field", "struct F { a: boolean, n: i32 } function main(): i32 { var f = F { a: true, n: 8 }; if (f.a) { return f.n; } return 0; }", 8},
		{"struct-mutate", "struct P { x: i32 } function main(): i32 { var p = P { x: 1 }; p.x = 41; return p.x + 1; }", 42},
		{"struct-update", "struct P { x: i32, y: i32 } function main(): i32 { var a = P { x: 1, y: 2 }; var b = P { ...a, y: 40 }; return b.x + b.y; }", 41},
		{"struct-in-loop", "struct P { x: i32 } function main(): i32 { var s = 0; var i = 0; while (i < 4) { var p = P { x: i }; s = s + p.x; i = i + 1; } return s; }", 6},
		{"enum-bare-construct", "enum E { A, B } function main(): i32 { var e = E.A; return 5; }", 5},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := run(t, tc.src); got != tc.want {
				t.Errorf("IR lower+eval of %q = %d, want %d", tc.src, got, tc.want)
			}
		})
	}

	// -dump prints the lowered op stream (render_op, one per line). This pins
	// the exact lowering for a representative program: a `var` binds slot 0
	// (operands pushed before the store), and `return` lowers its value then
	// emits `return`.
	t.Run("dump", func(t *testing.T) {
		const src = "function main(): i32 { var x = 2 + 3; return x * 10; }"
		// Integer literals lower to const_i32_text (source text spliced into the
		// immediate), so hex literals and the full u32 range survive — see
		// op_const_i32_text / the IR backends.
		const want = "const_i32_text 2\n" +
			"const_i32_text 3\n" +
			"add\n" +
			"store_local 0\n" +
			"load_local 0\n" +
			"const_i32_text 10\n" +
			"mul\n" +
			"return\n"
		cmd := exec.Command(bin, "-dump")
		cmd.Stdin = strings.NewReader(src)
		out, _ := cmd.Output()
		if got := string(out); got != want {
			t.Errorf("lowered op stream mismatch:\n--- got ---\n%s\n--- want ---\n%s", got, want)
		}
		// exit code is ops.len() in -dump mode.
		if code := cmd.ProcessState.ExitCode(); code != 8 {
			t.Errorf("dump op count = %d, want 8", code)
		}
	})
}
