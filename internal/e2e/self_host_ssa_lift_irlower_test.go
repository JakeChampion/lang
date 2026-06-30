package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestSelfHostSSALiftIRLower is the production-shaped end-to-end gate for the
// stack-IR -> SSA lift: it lowers a Fern SOURCE the way the real compiler does
// (AST -> ir.Op[] via irlower.lower_func_for, the asm_ir backend's input), then
// LIFTS that real IR to SSA, optimises, and emits via ssa_x86 / ssa_arm64. The
// emitted binary's exit code is checked against the native interpreter's result
// for the same source (the differential oracle). Where TestSelfHostSSALiftEmit
// drives hand-built ir.Op[], this drives ACTUAL irlower output — so it proves
// the lift consumes the real production IR, not synthetic ops, all the way to
// running native code on both backends.
//
// Coverage is the lift's current subset: integer control flow (straight-line,
// loops, if-merge, break, cross-function calls, recursion) plus string literals
// + length (const_str / str_len, which lower RC-free). Out-of-subset programs
// make the driver exit non-zero; only in-subset programs are listed here.
func TestSelfHostSSALiftIRLower(t *testing.T) {
	x86gcc, x86runner := x86_64Tooling(t)
	armgcc, qemu := arm64Tooling(t)

	dir := t.TempDir()
	for _, name := range []string{
		"util.fern", "lexer.fern", "parser.fern", "astwalk.fern",
		"ir.fern", "ssa.fern", "ssa_x86.fern", "ssa_arm64.fern",
		"irlower.fern", "ssa_lift.fern", "ssa_lift_irlower_run.fern",
	} {
		src, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), src, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	bin := buildSelfHostBin(t, x86gcc, dir, "ssa_lift_irlower_run.fern", "ssa_lift_irlower_run")

	// emit feeds the source to the driver on stdin and returns the emitted asm.
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
	run := func(t *testing.T, asm []byte, gcc string, pie bool, mk func(string, ...string) *exec.Cmd, tag string) int {
		t.Helper()
		asmPath := filepath.Join(dir, "il_"+tag+".s")
		binPath := filepath.Join(dir, "il_"+tag)
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
		cmd := mk(binPath)
		_ = cmd.Run()
		if cmd.ProcessState == nil || !cmd.ProcessState.Exited() {
			t.Fatalf("emitted program did not exit normally")
		}
		return cmd.ProcessState.ExitCode()
	}

	cases := []struct {
		name string
		src  string
	}{
		{"arith", `function main(): i32 { return (3 + 4) * 2 - 1; }`},
		{"loopsum", `function main(): i32 { var i = 1; var acc = 0; while (i <= 5) { acc = acc + i; i = i + 1; } return acc; }`},
		{"branch", `function main(): i32 { var x = 0; if (7 > 3) { x = 42; } return x; }`},
		{"breakloop", `function main(): i32 { var i = 0; while (i < 100) { if (i == 42) { break; } i = i + 1; } return i; }`},
		{"callsum", `function add(a: i32, b: i32): i32 { return a + b; } function main(): i32 { return add(20, 22); }`},
		{"factrec", `function fact(n: i32): i32 { if (n <= 1) { return 1; } return n * fact(n - 1); } function main(): i32 { return fact(5); }`},
		{"strlen", `function main(): i32 { var s: string = "hello"; return s.len(); }`},
		{"strlen2", `function main(): i32 { return ("abcd").len() + ("xy").len(); }`},
		{"strpick", `function main(): i32 { var s: string = "hi"; var t: string = "world"; if (s.len() < t.len()) { return t.len(); } return s.len(); }`},
	}
	for _, tc := range cases {
		tc := tc
		ref := runInterpExit(t, tc.src) // independent oracle: the interpreter
		t.Run("x86_64/"+tc.name, func(t *testing.T) {
			if len(x86runner) != 0 {
				t.Skip("emitted x86-64 runs natively; skipping under an exec runner")
			}
			mk := func(b string, a ...string) *exec.Cmd { return exec.Command(b, a...) }
			if got := run(t, emit(t, tc.src), x86gcc, true, mk, "x86-"+tc.name); got != ref {
				t.Errorf("x86-64 irlower->lift %s = %d, interp = %d", tc.name, got, ref)
			}
		})
		t.Run("arm64/"+tc.name, func(t *testing.T) {
			mk := func(b string, a ...string) *exec.Cmd { return runArm64Bin(qemu, b, a...) }
			if got := run(t, emit(t, tc.src, "-target", "arm64"), armgcc, false, mk, "arm-"+tc.name); got != ref {
				t.Errorf("arm64 irlower->lift %s = %d, interp = %d", tc.name, got, ref)
			}
		})
	}
}
