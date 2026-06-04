package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestSelfHostSSARoundTrip exercises the self-hosted SSA layer
// (examples/self_host/ssa.fern): the ssa_run driver parses a program,
// lowers `main` to SSA via build_func, evaluates it with the SSA
// interpreter, and returns the result as its exit code. Each case asserts
// AST → SSA → eval reproduces the program's value — proving the IR + the
// AST→SSA builder are semantics-preserving. The subset covers straight-line
// i32 (params/locals/arith/cmp/bitwise/calls), if/else (CFG + merge phi),
// and while loops (loop-header phi + back-edge). Constructs outside the
// subset (e.g. float literals) make build_func bail (exit 200).
//
// Slice 4 adds the optimisation passes: every program is also run with
// -opt and must evaluate to the same value (copy-propagation +
// constant-folding + DCE are semantics-preserving), and a shrinks-ir
// sub-test asserts the passes collapse foldable programs to far fewer
// instructions via the driver's -count mode.
//
// The driver is built natively via the Go x86-64 backend and fed each
// program on stdin; its exit code is the SSA-computed result.
func TestSelfHostSSARoundTrip(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("ssa_run driver runs natively; skipping under an exec runner")
	}
	dir := t.TempDir()
	for _, name := range []string{"lexer.fern", "parser.fern", "ssa.fern", "ssa_run.fern"} {
		src, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), src, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	bin := buildSelfHostBin(t, gcc, dir, "ssa_run.fern", "ssa_run")

	cases := []struct {
		name string
		src  string
		want int
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
		{"bitwise", "function main(): i32 { return (6 & 3) | 8; }", 10},
		{"shift", "function main(): i32 { return 1 << 4; }", 16},
		// Control flow: if / else lower to a CFG with phi at the merge.
		{"if-taken", "function main(): i32 { var x = 1; if (5 < 10) { x = 7; } return x; }", 7},
		{"if-not-taken", "function main(): i32 { var x = 1; if (10 < 5) { x = 7; } return x; }", 1},
		{"if-else-then", "function main(): i32 { var x = 0; if (true) { x = 3; } else { x = 9; } return x; }", 3},
		{"if-else-else", "function main(): i32 { var x = 0; if (false) { x = 3; } else { x = 9; } return x; }", 9},
		{"phi-plus", "function main(): i32 { var x = 0; if (true) { x = 10; } else { x = 20; } return x + 1; }", 11},
		{"early-return", "function main(): i32 { var x = 5; if (x > 3) { return 100; } return x; }", 100},
		{"no-early-return", "function main(): i32 { var x = 2; if (x > 3) { return 100; } return x; }", 2},
		{"nested-if", "function main(): i32 { var x = 5; if (x > 0) { if (x > 3) { x = 100; } else { x = 50; } } return x; }", 100},
		{"two-var-phi", "function main(): i32 { var a = 1; var b = 2; if (false) { a = 10; b = 20; } else { a = 30; } return a + b; }", 32},
		// While loops: loop-header phi + back-edge.
		{"while-count", "function main(): i32 { var i = 0; while (i < 3) { i = i + 1; } return i; }", 3},
		{"while-sum", "function main(): i32 { var i = 1; var s = 0; while (i <= 5) { s = s + i; i = i + 1; } return s; }", 15},
		{"while-factorial", "function main(): i32 { var i = 1; var f = 1; while (i <= 5) { f = f * i; i = i + 1; } return f; }", 120},
		{"while-zero-iters", "function main(): i32 { var i = 10; while (i < 5) { i = i + 100; } return i; }", 10},
		{"while-invariant-read", "function main(): i32 { var n = 7; var i = 0; var s = 0; while (i < n) { s = s + n; i = i + 1; } return s; }", 49},
		{"if-in-loop", "function main(): i32 { var i = 0; var c = 0; while (i < 10) { if (i > 4) { c = c + 1; } i = i + 1; } return c; }", 5},
		{"nested-loop", "function main(): i32 { var i = 0; var t = 0; while (i < 3) { var j = 0; while (j < 3) { t = t + 1; j = j + 1; } i = i + 1; } return t; }", 9},
		// Still outside the subset → build_func bails (200).
		{"float-bails", "function main(): i32 { var x = 1.5; return 0; }", 200},
	}

	run := func(t *testing.T, src string, args ...string) int {
		t.Helper()
		cmd := exec.Command(bin, args...)
		cmd.Stdin = strings.NewReader(src)
		_ = cmd.Run()
		if cmd.ProcessState == nil || !cmd.ProcessState.Exited() {
			t.Fatalf("ssa_run did not exit normally for %q (args %v)", src, args)
		}
		return cmd.ProcessState.ExitCode()
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Raw SSA eval matches the program's value …
			if got := run(t, tc.src); got != tc.want {
				t.Errorf("SSA eval of %q = %d, want %d", tc.src, got, tc.want)
			}
			// … and the optimiser is semantics-preserving: -opt evals the
			// same. (For out-of-subset bails the result is the same 200.)
			if got := run(t, tc.src, "-opt"); got != tc.want {
				t.Errorf("optimised SSA eval of %q = %d, want %d", tc.src, got, tc.want)
			}
		})
	}

	// The optimiser must actually shrink the IR: copy-propagation +
	// constant-folding + DCE collapse these to far fewer instructions.
	// `-count` returns the post-(opt) instruction total as the exit code.
	t.Run("shrinks-ir", func(t *testing.T) {
		shrink := []struct {
			name    string
			src     string
			wantOpt int // exact instruction count after optimisation
		}{
			// 2 + 3*4 folds to a single const_int; everything else is dead.
			{"fold-arith", "function main(): i32 { return 2 + 3 * 4; }", 1},
			// Whole chain folds to one const; the unused var is DCE'd.
			{"fold-chain", "function main(): i32 { var a = 2; var b = a + 3; var c = b * 10; return c; }", 1},
			// Dead `var b = 99 * 99` removed; `a` folds to a const.
			{"dce-unused", "function main(): i32 { var a = 1 + 2; var b = 99 * 99; return a; }", 1},
		}
		for _, sc := range shrink {
			t.Run(sc.name, func(t *testing.T) {
				raw := run(t, sc.src, "-count")
				opt := run(t, sc.src, "-opt", "-count")
				if opt != sc.wantOpt {
					t.Errorf("%q: optimised inst count = %d, want %d", sc.src, opt, sc.wantOpt)
				}
				if opt >= raw {
					t.Errorf("%q: optimiser did not shrink IR (raw=%d opt=%d)", sc.src, raw, opt)
				}
			})
		}
	})
}
