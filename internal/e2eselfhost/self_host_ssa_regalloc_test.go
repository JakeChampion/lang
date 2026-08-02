package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestSelfHostSSARegallocArm64 exercises the linear-scan register allocator
// (examples/self_host/ssa.fern's regalloc_linear) through the arm64 backend
// (`-target arm64 -regalloc`). It checks two things:
//
//   - correctness: the register-allocated code, assembled and run, produces
//     the same exit code as the program's value (allocation is a
//     semantics-preserving rewrite of where values live);
//   - effect: for loop-heavy programs the allocator keeps loop-carried
//     values in registers, so the emitted code makes strictly fewer stack
//     accesses (`[sp` operands) than the spill-everything baseline.
func TestSelfHostSSARegallocArm64(t *testing.T) {
	x86gcc, x86runner := x86_64Tooling(t)
	gcc, qemu := arm64Tooling(t)

	dir := t.TempDir()
	copySelfHostDriver(t, dir, "ssa_emit_run.fern")
	bin := buildSelfHostBin(t, x86gcc, dir, "ssa_emit_run.fern", "ssa_emit_run")

	emit := func(t *testing.T, src string, args ...string) []byte {
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
		{"arith", "function main(): i32 { return 2 + 3 * 4; }", 14},
		{"locals", "function main(): i32 { var x = 10; var y = x - 3; return y * 2; }", 14},
		{"if-else", "function main(): i32 { var x = 0; if (5 < 10) { x = 7; } else { x = 9; } return x; }", 7},
		{"nested-if", "function main(): i32 { var x = 5; if (x > 0) { if (x > 3) { x = 100; } else { x = 50; } } return x; }", 100},
		{"while-sum", "function main(): i32 { var i = 1; var s = 0; while (i <= 5) { s = s + i; i = i + 1; } return s; }", 15},
		{"while-factorial", "function main(): i32 { var i = 1; var f = 1; while (i <= 5) { f = f * i; i = i + 1; } return f; }", 120},
		{"nested-loop", "function main(): i32 { var i = 0; var t = 0; while (i < 3) { var j = 0; while (j < 3) { t = t + 1; j = j + 1; } i = i + 1; } return t; }", 9},
		{"call", "function add(a: i32, b: i32): i32 { return a + b; } function main(): i32 { return add(3, 4); }", 7},
		// Values live across a call are force-spilled; the result must still
		// be correct.
		{"recursion", "function fact(n: i32): i32 { if (n <= 1) { return 1; } return n * fact(n - 1); } function main(): i32 { return fact(5); }", 120},
		{"call-spanning", "function id(x: i32): i32 { return x; } function main(): i32 { var a = 5; var b = id(7); return a + b; }", 12},
		// Heap arrays with register allocation: pointer-width (64-bit) values
		// must use the x-register views.
		{"arr-loop-sum", "function main(): i32 { var a = [5, 10, 15, 20, 25]; var i = 0; var s = 0; while (i < 5) { s = s + a[i]; i = i + 1; } return s; }", 75},
		// A for-in loop's desugared counter + element load round-trip through
		// the allocator. (Heavier register pressure — deeply nested for-loops
		// with several loop-carried array loads — can still exceed the
		// linear-scan allocator's coarse interval model and is left to the
		// spill baseline; that's a pre-existing regalloc limitation, not
		// for-specific.)
		{"for-sum", "function main(): i32 { var a = [5, 10, 15]; var s = 0; for x in a { s = s + x; } return s; }", 30},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			asm := emit(t, tc.src, "-target", "arm64", "-regalloc")
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
				t.Errorf("SSA→arm64 -regalloc of %q = %d, want %d", tc.src, got, tc.want)
			}
		})
	}

	// The allocator must measurably cut stack traffic on loop-carried
	// values: count `[sp` operands with and without -regalloc.
	t.Run("reduces-stack-traffic", func(t *testing.T) {
		loops := []string{
			"function main(): i32 { var i = 1; var s = 0; while (i <= 5) { s = s + i; i = i + 1; } return s; }",
			"function main(): i32 { var i = 1; var f = 1; while (i <= 5) { f = f * i; i = i + 1; } return f; }",
			"function main(): i32 { var i = 0; var t = 0; while (i < 3) { var j = 0; while (j < 3) { t = t + 1; j = j + 1; } i = i + 1; } return t; }",
		}
		for _, src := range loops {
			spill := strings.Count(string(emit(t, src, "-target", "arm64")), "[sp")
			ra := strings.Count(string(emit(t, src, "-target", "arm64", "-regalloc")), "[sp")
			if ra >= spill {
				t.Errorf("regalloc did not reduce stack traffic for %q: spill=%d regalloc=%d", src, spill, ra)
			}
		}
	})
}

// TestSelfHostSSARegallocX86_64 is the x86-64 counterpart: the same
// linear-scan allocator (caller-saved %r10d/%r11d) wired into the x86-64
// backend. Assembles + runs each program under -regalloc and asserts the
// exit code, then checks that register allocation cuts rbp-slot traffic.
func TestSelfHostSSARegallocX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("emitted x86-64 runs natively; skipping under an exec runner")
	}
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "ssa_emit_run.fern")
	bin := buildSelfHostBin(t, gcc, dir, "ssa_emit_run.fern", "ssa_emit_run")

	emit := func(t *testing.T, src string, args ...string) []byte {
		t.Helper()
		cmd := exec.Command(bin, args...)
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
		{"arith", "function main(): i32 { return 2 + 3 * 4; }", 14},
		{"locals", "function main(): i32 { var x = 10; var y = x - 3; return y * 2; }", 14},
		{"if-else", "function main(): i32 { var x = 0; if (5 < 10) { x = 7; } else { x = 9; } return x; }", 7},
		{"while-sum", "function main(): i32 { var i = 1; var s = 0; while (i <= 5) { s = s + i; i = i + 1; } return s; }", 15},
		{"while-factorial", "function main(): i32 { var i = 1; var f = 1; while (i <= 5) { f = f * i; i = i + 1; } return f; }", 120},
		{"nested-loop", "function main(): i32 { var i = 0; var t = 0; while (i < 3) { var j = 0; while (j < 3) { t = t + 1; j = j + 1; } i = i + 1; } return t; }", 9},
		{"recursion", "function fact(n: i32): i32 { if (n <= 1) { return 1; } return n * fact(n - 1); } function main(): i32 { return fact(5); }", 120},
		{"call-spanning", "function id(x: i32): i32 { return x; } function main(): i32 { var a = 5; var b = id(7); return a + b; }", 12},
		// Heap arrays with register allocation: pointer-width (64-bit) values
		// must use the 64-bit register views.
		{"arr-loop-sum", "function main(): i32 { var a = [5, 10, 15, 20, 25]; var i = 0; var s = 0; while (i < 5) { s = s + a[i]; i = i + 1; } return s; }", 75},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			asm := emit(t, tc.src, "-regalloc")
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
				t.Errorf("SSA→x86-64 -regalloc of %q = %d, want %d", tc.src, got, tc.want)
			}
		})
	}

	t.Run("reduces-stack-traffic", func(t *testing.T) {
		loops := []string{
			"function main(): i32 { var i = 1; var s = 0; while (i <= 5) { s = s + i; i = i + 1; } return s; }",
			"function main(): i32 { var i = 1; var f = 1; while (i <= 5) { f = f * i; i = i + 1; } return f; }",
		}
		for _, src := range loops {
			spill := strings.Count(string(emit(t, src)), "(%rbp)")
			ra := strings.Count(string(emit(t, src, "-regalloc")), "(%rbp)")
			if ra >= spill {
				t.Errorf("regalloc did not reduce stack traffic for %q: spill=%d regalloc=%d", src, spill, ra)
			}
		}
	})
}
