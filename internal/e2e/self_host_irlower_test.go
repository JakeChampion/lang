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
// Constructs outside the subset (control flow, calls, floats) make
// lower_func bail (exit 200).
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
		// Still out of subset -> lower_func bails (200): loops, calls, floats.
		{"while-bails", "function main(): i32 { var i = 0; while (i < 3) { i = i + 1; } return i; }", 200},
		{"call-bails", "function main(): i32 { return foo(); }", 200},
		{"float-bails", "function main(): i32 { var x = 1.5; return 2; }", 200},
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
		const want = "const_i32 2\n" +
			"const_i32 3\n" +
			"add\n" +
			"store_local 0\n" +
			"load_local 0\n" +
			"const_i32 10\n" +
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
