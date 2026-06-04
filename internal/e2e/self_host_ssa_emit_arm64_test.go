package e2e

import (
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
	for _, name := range []string{"lexer.fern", "parser.fern", "ssa.fern", "ssa_x86.fern", "ssa_arm64.fern", "ssa_emit_run.fern"} {
		src, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), src, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
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
		{"arith", "function main(): i32 { return 2 + 3 * 4; }", 14},
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
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			asm := runDriver(t, tc.src, "-target", "arm64")
			asmPath := filepath.Join(dir, "prog.s")
			binPath := filepath.Join(dir, "prog")
			if err := os.WriteFile(asmPath, asm, 0o644); err != nil {
				t.Fatalf("write asm: %v", err)
			}
			if out, err := exec.Command(gcc, "-static", "-nostdlib", asmPath, "-o", binPath).CombinedOutput(); err != nil {
				t.Fatalf("gcc failed for %q: %v\n%s\n--- asm ---\n%s", tc.src, err, out, asm)
			}
			cmd := runArm64Bin(qemu, binPath)
			_ = cmd.Run()
			if cmd.ProcessState == nil || !cmd.ProcessState.Exited() {
				t.Fatalf("emitted program did not exit normally for %q", tc.src)
			}
			if got := cmd.ProcessState.ExitCode(); got != tc.want {
				t.Errorf("SSA→arm64 of %q = %d, want %d", tc.src, got, tc.want)
			}
		})
	}
}
