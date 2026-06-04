package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestSelfHostSSAPrint exercises the SSA backends' `print` op + the
// __fern_ssa_print write-syscall helper: it assembles emitted code and
// captures STDOUT (not just the exit code), asserting the program's output.
// Covers x86-64 (native) and arm64 (under qemu) — the first effectful
// programs the SSA pipeline produces.
func TestSelfHostSSAPrint(t *testing.T) {
	x86gcc, x86runner := x86_64Tooling(t)
	armgcc, qemu := arm64Tooling(t)

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
		want string
	}{
		{"hello", "function main(): i32 { print(\"hello\\n\"); return 0; }", "hello\n"},
		{"two-prints", "function main(): i32 { print(\"ab\"); print(\"cd\"); return 0; }", "abcd"},
		{"print-in-loop", "function main(): i32 { var i = 0; while (i < 3) { print(\"x\"); i = i + 1; } return 0; }", "xxx"},
		{"print-then-loop", "function main(): i32 { print(\"hi\\n\"); var s = 0; var i = 0; while (i < 5) { s = s + i; i = i + 1; } return s; }", "hi\n"},
		{"print-string-var", "function main(): i32 { var msg = \"done\\n\"; print(msg); return 0; }", "done\n"},
	}

	run := func(t *testing.T, asm []byte, gcc string, pie bool, runner func(string) *exec.Cmd) string {
		t.Helper()
		asmPath := filepath.Join(dir, "p.s")
		binPath := filepath.Join(dir, "p")
		if err := os.WriteFile(asmPath, asm, 0o644); err != nil {
			t.Fatalf("write asm: %v", err)
		}
		gccArgs := []string{"-static", "-nostdlib"}
		if pie {
			gccArgs = append(gccArgs, "-no-pie")
		}
		gccArgs = append(gccArgs, asmPath, "-o", binPath)
		if out, err := exec.Command(gcc, gccArgs...).CombinedOutput(); err != nil {
			t.Fatalf("gcc failed: %v\n%s\n--- asm ---\n%s", err, out, asm)
		}
		out, _ := runner(binPath).Output()
		return string(out)
	}

	for _, tc := range cases {
		t.Run("x86_64/"+tc.name, func(t *testing.T) {
			if len(x86runner) != 0 {
				t.Skip("emitted x86-64 runs natively; skipping under an exec runner")
			}
			got := run(t, emit(t, tc.src), x86gcc, true, func(b string) *exec.Cmd { return exec.Command(b) })
			if got != tc.want {
				t.Errorf("x86-64 print of %q = %q, want %q", tc.src, got, tc.want)
			}
		})
		t.Run("arm64/"+tc.name, func(t *testing.T) {
			got := run(t, emit(t, tc.src, "-target", "arm64"), armgcc, false, func(b string) *exec.Cmd { return runArm64Bin(qemu, b) })
			if got != tc.want {
				t.Errorf("arm64 print of %q = %q, want %q", tc.src, got, tc.want)
			}
		})
	}
}
