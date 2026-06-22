package e2e

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestSelfHostIRx86Run exercises the first IR-consuming backend
// (examples/self_host/ir_x86.fern, slice 3 of the IR rebuild —
// docs/RC-PERCEUS-SELF-HOST-IR-REBUILD.md): the ir_x86_run driver lowers a
// program's `main` to the stack IR (irlower) and emits a complete,
// freestanding x86-64 program directly from the Op[]. This is the first time
// the self-host emits machine code from the IR rather than the AST.
//
// End-to-end, mirroring the asm_run harness: build the driver once via the
// production x86-64 backend; for each case pipe the source in, capture the
// emitted asm, gcc-assemble it into a static ELF, run it, and assert the
// inner exit code matches — proving AST -> IR -> x86-64 produces a working
// executable whose value agrees with the IR interpreter (slice 2) and the
// AST emit path on the straight-line i32 subset. Out-of-subset programs
// lower to a bail that exits 200.
func TestSelfHostIRx86Run(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	for _, name := range []string{"util.fern", "astwalk.fern", "lexer.fern", "parser.fern", "ir.fern", "irlower.fern", "ir_x86.fern", "ir_x86_run.fern"} {
		src, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), src, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	// Build the driver once via the production x86-64 backend.
	driverBin := buildSelfHostBin(t, gcc, dir, "ir_x86_run.fern", "driver")

	cases := []struct {
		name     string
		source   string
		expected int
	}{
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
		{"unary-minus-net-positive", "function main(): i32 { var x = 10; return -x + 13; }", 3},
		{"chained", "function main(): i32 { var a = 1; var b = a + 1; var c = b + 1; var d = c + 1; return d * 10; }", 40},
		// Structured control flow (slice 4): if / else lower to real x86
		// conditional branches + labels.
		{"if-taken", "function main(): i32 { var x = 1; if (5 < 10) { x = 7; } return x; }", 7},
		{"if-not-taken", "function main(): i32 { var x = 1; if (10 < 5) { x = 7; } return x; }", 1},
		{"if-else-then", "function main(): i32 { var x = 0; if (1 < 2) { x = 3; } else { x = 9; } return x; }", 3},
		{"if-else-else", "function main(): i32 { var x = 0; if (2 < 1) { x = 3; } else { x = 9; } return x; }", 9},
		{"early-return", "function main(): i32 { var x = 5; if (x > 3) { return 100; } return x; }", 100},
		{"no-early-return", "function main(): i32 { var x = 2; if (x > 3) { return 100; } return x; }", 2},
		{"nested-if", "function main(): i32 { var x = 5; if (x > 0) { if (x > 3) { x = 100; } else { x = 50; } } return x; }", 100},
		{"if-chain", "function main(): i32 { var n = 2; var r = 0; if (n == 1) { r = 10; } else { if (n == 2) { r = 20; } else { r = 30; } } return r; }", 20},
		// Loops (slice 5): while -> block/loop/br/br_if -> real x86 jumps + labels.
		{"while-count", "function main(): i32 { var i = 0; while (i < 3) { i = i + 1; } return i; }", 3},
		{"while-sum", "function main(): i32 { var i = 1; var s = 0; while (i <= 5) { s = s + i; i = i + 1; } return s; }", 15},
		{"while-factorial", "function main(): i32 { var i = 1; var f = 1; while (i <= 5) { f = f * i; i = i + 1; } return f; }", 120},
		{"while-zero-iters", "function main(): i32 { var i = 10; while (i < 5) { i = i + 100; } return i; }", 10},
		{"sum-to-100", "function main(): i32 { var i = 0; var s = 0; while (i < 100) { i = i + 1; s = s + i; } return s % 256; }", 186},
		{"if-in-loop", "function main(): i32 { var i = 0; var c = 0; while (i < 10) { if (i > 4) { c = c + 1; } i = i + 1; } return c; }", 5},
		{"nested-loop", "function main(): i32 { var i = 0; var t = 0; while (i < 3) { var j = 0; while (j < 3) { t = t + 1; j = j + 1; } i = i + 1; } return t; }", 9},
		// Range-for `for i in LOW..HIGH` (closes #2699 self-host IR slice):
		// the parser desugars to __range(LOW, HIGH) and irlower lowers it to a
		// counted loop over the half-open interval -> real x86 jumps + labels.
		{"range-sum", "function main(): i32 { var s = 0; for i in 0..5 { s = s + i; } return s; }", 10},
		{"range-count", "function main(): i32 { var c = 0; for i in 0..10 { c = c + 1; } return c; }", 10},
		{"range-nonzero-low", "function main(): i32 { var s = 0; for i in 3..7 { s = s + i; } return s; }", 18},
		{"range-empty", "function main(): i32 { var c = 7; for i in 5..5 { c = c + 1; } return c; }", 7},
		{"range-reversed", "function main(): i32 { var c = 9; for i in 9..3 { c = c + 1; } return c; }", 9},
		{"range-hi-expr", "function main(): i32 { var n = 4; var s = 0; for i in 1..n + 1 { s = s + i; } return s; }", 10},
		{"range-nested", "function main(): i32 { var t = 0; for i in 0..3 { for j in 0..3 { t = t + 1; } } return t; }", 9},
		{"range-hi-once", "function side(): i32 { return 4; } function main(): i32 { var c = 0; for i in 0..side() { c = c + 1; } return c; }", 4},
		// `loop { }` infinite loop (#2676 loop-form) -> desugars to while(true).
		{"loop-break", "function main(): i32 { var i = 0; loop { i = i + 1; if (i >= 7) { break; } } return i; }", 7},
		{"asc-arr-nonempty", "function main(): i32 { var a = [3, 4] as i32[]; return a[0] + a[1]; }", 7},
		{"asc-arr-empty", "function main(): i32 { var a = [] as i32[]; a = [5, 10]; return a[0] + a[1]; }", 15},
		{"asc-arr-len", "function main(): i32 { var a = [] as i32[]; return a.len(); }", 0},
		{"asc-arr-rc", "function main(): i32 { var a = [3, 4] as i32[]; var b = a; return __rc(a); }", 2},
		{"asc-arr-alias-use", "function main(): i32 { var a = [3, 4] as i32[]; var b = a; return b[0] + b[1] + a.len(); }", 9},
		// Type ascription `E as T` in NON-binding positions (#2669): the `as`
		// is identity, lowered as the operand. arg position (the ascripted
		// array is passed/borrowed), return position (move-on-return off the
		// ascription), and nested (index of a parenthesised ascription).
		{"asc-arg", "function sum(a: i32[]): i32 { var i = 0; var s = 0; while (i < a.len()) { s = s + a[i]; i = i + 1; } return s; } function main(): i32 { var arr = [10, 20, 30]; return sum(arr as i32[]); }", 60},
		{"asc-ret", "function make(): i32[] { var a = [10, 20, 30]; return a as i32[]; } function main(): i32 { var x = make(); return x[0] + x[2]; }", 40},
		{"asc-nested-index", "function main(): i32 { var a = [3, 4]; return (a as i32[])[0] + (a as i32[])[1]; }", 7},
		// break / continue inside a `for` loop (#2788): the index advances at the
		// TOP of the loop, so `continue` (br-to-header) re-runs the advance and
		// `break` exits. Range-for and array-foreach forms.
		{"range-continue", "function main(): i32 { var s = 0; for i in 0..10 { if (i == 3) { continue; } s = s + i; } return s; }", 42},
		{"range-break", "function main(): i32 { var s = 0; for i in 0..10 { if (i == 7) { break; } s = s + i; } return s; }", 21},
		{"range-break-continue", "function main(): i32 { var s = 0; for i in 0..10 { if (i == 3) { continue; } if (i == 7) { break; } s = s + i; } return s; }", 18},
		{"range-continue-count", "function main(): i32 { var c = 0; for i in 0..6 { if (i % 2 == 0) { continue; } c = c + 1; } return c; }", 3},
		{"foreach-continue", "function main(): i32 { var a = [5, 10, 15, 20, 25]; var t = 0; for x in a { if (x == 15) { continue; } t = t + x; } return t; }", 60},
		{"foreach-break", "function main(): i32 { var a = [5, 10, 15, 20, 25]; var t = 0; for x in a { if (x == 20) { break; } t = t + x; } return t; }", 30},
		{"foreach-break-continue", "function main(): i32 { var a = [5, 10, 15, 20, 25]; var t = 0; for x in a { if (x == 15) { continue; } if (x == 25) { break; } t = t + x; } return t; }", 35},
		{"range-nested-break", "function main(): i32 { var t = 0; for i in 0..3 { for j in 0..3 { if (j == 2) { break; } t = t + 1; } } return t; }", 6},
		// `for x in <EXPR>` over a non-ident iterable: array literal and a call
		// returning an array are snapshotted into a hidden local, then iterated.
		{"foreach-literal", "function main(): i32 { var s = 0; for x in [1, 2, 3, 4] { s = s + x; } return s; }", 10},
		{"foreach-call", "function mk(): i32[] { return [10, 20, 30]; } function main(): i32 { var s = 0; for y in mk() { s = s + y; } return s; }", 60},
		{"foreach-literal-break", "function main(): i32 { var s = 0; for x in [5, 10, 15, 20] { if (x == 15) { break; } s = s + x; } return s; }", 15},
		{"foreach-call-continue", "function mk(): i32[] { return [1, 2, 3, 4, 5]; } function main(): i32 { var s = 0; for x in mk() { if (x % 2 == 0) { continue; } s = s + x; } return s; }", 9},
		{"loop-continue", "function main(): i32 { var i = 0; var s = 0; loop { i = i + 1; if (i > 10) { break; } if (i % 2 == 1) { continue; } s = s + i; } return s; }", 30},
		// Direct calls + multi-function programs + recursion (slice 6) -> real
		// x86 call/ret with the SysV integer-register arg convention.
		{"simple-call", "function helper(): i32 { return 5; } function main(): i32 { return helper(); }", 5},
		{"call-args", "function add(a: i32, b: i32): i32 { return a + b; } function main(): i32 { return add(4, 5); }", 9},
		{"call-three-args", "function f(a: i32, b: i32, c: i32): i32 { return a * 100 + b * 10 + c; } function main(): i32 { return f(1, 2, 3) % 256; }", 123 % 256},
		{"call-compute", "function compute(a: i32): i32 { var b = a * 2; var c = b + 1; return c; } function main(): i32 { return compute(5); }", 11},
		{"factorial", "function fact(n: i32): i32 { if (n <= 1) { return 1; } return n * fact(n - 1); } function main(): i32 { return fact(5); }", 120},
		{"fib", "function fib(n: i32): i32 { if (n < 2) { return n; } return fib(n - 1) + fib(n - 2); } function main(): i32 { return fib(8); }", 21},
		{"mutual-recursion", "function is_even(n: i32): i32 { if (n == 0) { return 1; } return is_odd(n - 1); } function is_odd(n: i32): i32 { if (n == 0) { return 0; } return is_even(n - 1); } function main(): i32 { return is_even(6); }", 1},
		{"loop-call", "function sq(x: i32): i32 { return x * x; } function main(): i32 { var i = 1; var s = 0; while (i <= 4) { s = s + sq(i); i = i + 1; } return s; }", 30},
		// i32 array literals + indexing (slice 8) -> bump-allocated heap.
		{"arr-index", "function main(): i32 { var a = [10, 20, 30]; return a[0] + a[2]; }", 40},
		{"arr-loop-sum", "function main(): i32 { var a = [5, 10, 15, 20, 25]; var i = 0; var s = 0; while (i < 5) { s = s + a[i]; i = i + 1; } return s; }", 75},
		{"arr-expr-elements", "function main(): i32 { var x = 4; var a = [x, x * 2, x + 100]; return a[1] + a[2]; }", 112},
		{"arr-two", "function main(): i32 { var a = [1, 2]; var b = [100, 200]; return a[1] + b[0]; }", 102},
		// .len() + index-assignment (slice 9).
		{"arr-len", "function main(): i32 { var a = [10, 20, 30]; return a.len(); }", 3},
		{"arr-len-loop", "function main(): i32 { var a = [4, 8, 12, 16]; var i = 0; var s = 0; while (i < a.len()) { s = s + a[i]; i = i + 1; } return s; }", 40},
		{"set-index", "function main(): i32 { var a = [10, 20, 30]; a[1] = 99; return a[0] + a[1] + a[2]; }", 139},
		{"set-index-fill", "function main(): i32 { var a = [0, 0, 0, 0, 0]; var i = 0; while (i < 5) { a[i] = i * i; i = i + 1; } return a[0] + a[1] + a[2] + a[3] + a[4]; }", 30},
		// RC counting (slice 10): __rc(a) reads the refcount header. A fresh
		// array is rc=1; each alias (var b = a) emits __fern_rc_inc -> rc++.
		{"rc-fresh", "function main(): i32 { var a = [10, 20, 30]; return __rc(a); }", 1},
		{"rc-one-alias", "function main(): i32 { var a = [10, 20, 30]; var b = a; return __rc(a); }", 2},
		{"rc-two-aliases", "function main(): i32 { var a = [1, 2]; var b = a; var c = a; return __rc(a); }", 3},
		{"rc-alias-same-buffer", "function main(): i32 { var a = [1, 2]; var b = a; return __rc(b); }", 2},
		// rc header is transparent to ordinary array use (no free yet).
		{"rc-transparent", "function main(): i32 { var a = [10, 20, 30]; var b = a; return b[0] + b[2] + a.len(); }", 43},
		// Exit dec-sweep + underflow detector (slice 11). __rc_underflow reads
		// the over-release counter. Clean / balanced programs report 0; a
		// deliberate double-release is detected (1). No free yet.
		{"rc-clean-no-underflow", "function main(): i32 { var a = [10, 20, 30]; var b = a; return __rc_underflow(); }", 0},
		{"rc-balanced-manual-dec", "function main(): i32 { var a = [1, 2, 3]; var b = a; __rc_dec(a); __rc_dec(b); return __rc_underflow(); }", 0},
		{"rc-detects-overrelease", "function main(): i32 { var a = [1, 2, 3]; __rc_dec(a); __rc_dec(a); return __rc_underflow(); }", 1},
		{"rc-after-one-dec", "function main(): i32 { var a = [1, 2, 3]; var b = a; __rc_dec(a); return __rc(a); }", 1},
		// Free path + reuse (slice 12): at rc==0 the block returns to a
		// size-class freelist; a same-size alloc reuses it. (a - b == 0 means
		// b reused a's freed block — the in-place-reuse / peak-memory win.)
		{"reuse-same-block", "function main(): i32 { var a = [1, 2, 3]; __rc_dec(a); var b = [4, 5, 6]; var d = a - b; if (d == 0) { return 1; } return 0; }", 1},
		{"reuse-values-ok", "function main(): i32 { var a = [1, 2, 3]; __rc_dec(a); var b = [4, 5, 6]; return b[0] + b[1] + b[2]; }", 15},
		{"no-reuse-when-live", "function main(): i32 { var a = [1, 2, 3]; var b = [4, 5, 6]; var d = a - b; if (d == 0) { return 1; } return 0; }", 0},
		{"diff-size-no-reuse", "function main(): i32 { var a = [1, 2, 3]; __rc_dec(a); var b = [4, 5]; var d = a - b; if (d == 0) { return 1; } return 0; }", 0},
		// Move-on-return (slice 13): a returned array is moved to the caller —
		// excluded from the callee's exit dec-sweep, so it survives (rc=1) and
		// isn't freed. The uaf-guard case is the discriminator: without the
		// move, the callee frees the buffer, the caller's same-size alloc
		// reuses it, and x is corrupted (would read 2 instead of 40).
		{"mov-basic", "function make(): i32[] { var a = [10, 20, 30]; return a; } function main(): i32 { var x = make(); return x[0] + x[2]; }", 40},
		{"mov-uaf-guard", "function make(): i32[] { var a = [10, 20, 30]; return a; } function main(): i32 { var x = make(); var y = [1, 1, 1]; return x[0] + x[2]; }", 40},
		{"mov-len", "function make(): i32[] { var a = [5, 6, 7, 8]; return a; } function main(): i32 { var x = make(); return x.len(); }", 4},
		{"mov-then-mutate", "function make(): i32[] { var a = [1, 2, 3]; return a; } function main(): i32 { var x = make(); x[1] = 99; return x[0] + x[1] + x[2]; }", 103},
		// Array params with borrow semantics (slice 14): a callee borrows an
		// array param (slot < n_params) — never frees it; the caller retains
		// ownership. borrow-noreuse is the discriminator: if the callee wrongly
		// freed the borrowed array, the caller's later alloc would reuse it and
		// corrupt arr (would read != 11).
		{"param-sum", "function sum(a: i32[]): i32 { var i = 0; var s = 0; while (i < a.len()) { s = s + a[i]; i = i + 1; } return s; } function main(): i32 { var arr = [10, 20, 30]; return sum(arr); }", 60},
		{"param-borrow-then-use", "function get0(a: i32[]): i32 { return a[0]; } function main(): i32 { var arr = [5, 6, 7]; var x = get0(arr); var y = arr[1]; return x + y; }", 11},
		{"param-two-arrays", "function pick(a: i32[], b: i32[]): i32 { return a[0] + b[1]; } function main(): i32 { var p = [1, 2]; var q = [10, 20]; return pick(p, q); }", 21},
		{"param-borrow-noreuse", "function len_of(a: i32[]): i32 { return a.len(); } function main(): i32 { var arr = [3, 4, 5]; var n = len_of(arr); var z = [9, 9, 9]; return arr[0] + arr[2] + n; }", 11},
		// Reclamation bounds peak memory (the Perceus payoff): __heap_used()
		// reports bytes bump-allocated; freelist reuse does not bump. Three
		// arrays each freed before the next all reuse ONE 20-byte block (peak
		// 20); three LIVE arrays bump three blocks (60).
		{"heap-reuse-bounded", "function main(): i32 { var a = [1, 2, 3]; __rc_dec(a); var b = [4, 5, 6]; __rc_dec(b); var c = [7, 8, 9]; return __heap_used(); }", 20},
		{"heap-live-grows", "function main(): i32 { var a = [1, 2, 3]; var b = [4, 5, 6]; var c = [7, 8, 9]; return __heap_used(); }", 60},
		{"heap-one-array", "function main(): i32 { var a = [1, 2, 3]; return __heap_used(); }", 20},
		// ir_x86 is an i32-only backend (64-bit values — i64 and f64 alike — are
		// out of its subset). f64 locals / arithmetic / casts lower in irlower
		// now, but f64 modulo has no float form and still bails main's lowering
		// -> emit_module exits 200 (so no f64 op reaches this i32-only emitter).
		{"f64-mod-bails", "function main(): i32 { var x: f64 = 5.5; var y: f64 = x % 2.0; if (y > 0.0) { return 1; } return 2; }", 200},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(driverBin)
			} else {
				cmd = exec.Command(runner[0], append(append([]string{}, runner[1:]...), driverBin)...)
			}
			cmd.Stdin = bytes.NewReader([]byte(tc.source))
			emittedAsm, err := cmd.Output()
			if err != nil || len(emittedAsm) == 0 {
				t.Fatalf("driver failed for %q: %v", tc.source, err)
			}
			innerAsm := filepath.Join(dir, "inner.s")
			innerBin := filepath.Join(dir, "inner")
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
				inner = exec.Command(runner[0], append(append([]string{}, runner[1:]...), innerBin)...)
			}
			_ = inner.Run()
			if inner.ProcessState == nil || !inner.ProcessState.Exited() {
				t.Fatalf("inner did not exit normally for %q", tc.source)
			}
			if code := inner.ProcessState.ExitCode(); code != tc.expected {
				t.Errorf("inner exit code = %d, want %d\n--- source ---\n%s\n--- asm ---\n%s", code, tc.expected, tc.source, emittedAsm)
			}
		})
	}
}
