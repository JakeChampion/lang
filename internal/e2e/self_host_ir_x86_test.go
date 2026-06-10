package e2e

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/jakechampion/lang/internal/checker"
	"github.com/jakechampion/lang/internal/codegen/x86_64"
	"github.com/jakechampion/lang/internal/constfold"
	"github.com/jakechampion/lang/internal/modload"
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
	prog, _, err := modload.Load(filepath.Join(dir, "ir_x86_run.fern"))
	if err != nil {
		t.Fatalf("modload: %v", err)
	}
	if err := constfold.Fold(prog); err != nil {
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
	if out, err := exec.Command(gcc, "-static", "-nostdlib", "-no-pie", driverAsm, "-o", driverBin).CombinedOutput(); err != nil {
		t.Fatalf("driver gcc: %v\n%s", err, out)
	}

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
		// Still out of subset -> lower bails -> emit_module exits 200.
		{"float-bails", "function main(): i32 { var x = 1.5; return 2; }", 200},
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
