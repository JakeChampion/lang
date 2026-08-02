package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestSelfHostSSAArgs exercises the SSA backends' `args` op + the
// __fern_ssa_args runtime: it materialises the SSA string[] of command-line
// arguments (argv[0] first) from the argv pointer the SSA `_start` saves,
// building each element as a fresh SSA [len, byte-per-word] string. The test
// runs the emitted binary with a controlled argv and asserts the exit code,
// covering x86-64 (native) and arm64 (under qemu).
func TestSelfHostSSAArgs(t *testing.T) {
	x86gcc, x86runner := x86_64Tooling(t)
	armgcc, qemu := arm64Tooling(t)

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
		name    string
		src     string
		runArgs []string // extra argv passed to the emitted binary (argv[0] is the path)
		want    int
	}{
		// args().len() == argc: argv[0] (the program path) + the extras.
		{"argc-no-extras", "function main(): i32 { return args().len(); }", nil, 1},
		{"argc-three-extras", "function main(): i32 { return args().len(); }", []string{"a", "b", "c"}, 4},
		// args()[1] is the first user argument; verify its string content
		// (length and bytes) materialised correctly: len*10 + first byte.
		{"arg1-content", "function main(): i32 { var a = args(); if (a.len() < 2) { return 99; } var s = a[1]; return s.len() * 10 + (s[0] as i32); }", []string{"hello"}, 154}, // 5*10 + 'h'(104)
		// Iterate the args, summing their lengths (skips argv[0] via a guard on
		// index): exercises args()[i] across the whole vector.
		{"sum-extra-lens", "function main(): i32 { var a = args(); var s = 0; var i = 1; while (i < a.len()) { s = s + a[i].len(); i = i + 1; } return s; }", []string{"ab", "cde", "f"}, 6},
	}

	run := func(t *testing.T, asm []byte, gcc string, pie bool, mk func(string, ...string) *exec.Cmd, runArgs []string) int {
		t.Helper()
		asmPath := filepath.Join(dir, "ar.s")
		binPath := filepath.Join(dir, "ar")
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
		cmd := mk(binPath, runArgs...)
		_ = cmd.Run()
		if cmd.ProcessState == nil || !cmd.ProcessState.Exited() {
			t.Fatalf("emitted program did not exit normally")
		}
		return cmd.ProcessState.ExitCode()
	}

	for _, tc := range cases {
		tc := tc
		t.Run("x86_64/"+tc.name, func(t *testing.T) {
			if len(x86runner) != 0 {
				t.Skip("emitted x86-64 runs natively; skipping under an exec runner")
			}
			got := run(t, emit(t, tc.src), x86gcc, true, func(b string, a ...string) *exec.Cmd { return exec.Command(b, a...) }, tc.runArgs)
			if got != tc.want {
				t.Errorf("x86-64 args of %q = %d, want %d", tc.src, got, tc.want)
			}
		})
		t.Run("arm64/"+tc.name, func(t *testing.T) {
			got := run(t, emit(t, tc.src, "-target", "arm64"), armgcc, false, func(b string, a ...string) *exec.Cmd { return runArm64Bin(qemu, b, a...) }, tc.runArgs)
			if got != tc.want {
				t.Errorf("arm64 args of %q = %d, want %d", tc.src, got, tc.want)
			}
		})
	}
}
