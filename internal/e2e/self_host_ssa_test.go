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
// AST→SSA builder are semantics-preserving. Slice 1 is straight-line i32
// (params/locals/arith/cmp/bitwise/calls + a trailing return); constructs
// outside that subset make build_func bail (exit 200).
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
		// Outside the slice-1 subset → build_func bails (200).
		{"if-bails", "function main(): i32 { if (true) { return 1; } return 2; }", 200},
		{"while-bails", "function main(): i32 { var i = 0; while (i < 3) { i = i + 1; } return i; }", 200},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cmd := exec.Command(bin)
			cmd.Stdin = strings.NewReader(tc.src)
			_ = cmd.Run()
			if cmd.ProcessState == nil || !cmd.ProcessState.Exited() {
				t.Fatalf("ssa_run did not exit normally for %q", tc.src)
			}
			if got := cmd.ProcessState.ExitCode(); got != tc.want {
				t.Errorf("SSA eval of %q = %d, want %d", tc.src, got, tc.want)
			}
		})
	}
}
