package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestSelfHostSSALiftEmit is the end-to-end proof of the stack-IR -> SSA LIFT
// feeding real codegen: the ssa_lift_emit_run driver builds a hand-coded
// ir.Op[] program, lifts it to SSA (ssa_lift.lift_from_ir), runs the existing
// ssa.optimize, and emits assembly via ssa_x86 / ssa_arm64. This test
// assembles that output (gcc -static -nostdlib [-no-pie]) and runs it (arm64
// under qemu), asserting the exit code equals the program's value — so the
// lifted SSA is proven not merely interpretable (eval_func) but accepted by the
// existing optimiser and BOTH production backends all the way to executing
// native code. Each program is run twice per target: once with the default
// slot-addressed emit, once with -regalloc (the linear-scan allocator) over the
// lifted SSA. The lifted SSA is target-agnostic — the same SFunc feeds either
// backend, exactly as build_func's output does in ssa_emit_run. Programs range
// from single-function integer control flow up to multi-function ones that
// exercise cross-function call_direct (callsum) and self-recursion (factrec),
// all lifted and emitted together so the calls resolve.
func TestSelfHostSSALiftEmit(t *testing.T) {
	x86gcc, x86runner := x86_64Tooling(t)
	armgcc, qemu := arm64Tooling(t)

	dir := t.TempDir()
	copySelfHostDriver(t, dir, "ssa_lift_emit_run.fern")
	bin := buildSelfHostBin(t, x86gcc, dir, "ssa_lift_emit_run.fern", "ssa_lift_emit_run")

	// emit runs the driver (which executes natively on x86-64) with the given
	// args and returns the emitted assembly.
	emit := func(t *testing.T, args ...string) []byte {
		t.Helper()
		var cmd *exec.Cmd
		if len(x86runner) == 0 {
			cmd = exec.Command(bin, args...)
		} else {
			cmd = exec.Command(x86runner[0], append(append(x86runner[1:], bin), args...)...)
		}
		out, err := cmd.Output()
		if err != nil {
			t.Fatalf("emit driver failed for %v: %v", args, err)
		}
		return out
	}
	// run assembles asm and executes it, returning the process exit code.
	run := func(t *testing.T, asm []byte, gcc string, pie bool, mk func(string, ...string) *exec.Cmd, tag string) int {
		t.Helper()
		asmPath := filepath.Join(dir, "le_"+tag+".s")
		binPath := filepath.Join(dir, "le_"+tag)
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
		prog string
		want int
	}{
		{"arith", 13},     // (3+4)*2-1 — straight-line
		{"loopsum", 15},   // sum 1..5 — loop-header phis
		{"branch", 42},    // void if-merge
		{"breakloop", 42}, // break out of a loop
		{"callsum", 42},   // cross-function call_direct: main() -> add(20,22)
		{"factrec", 120},  // self-recursion: fact(5)
		{"arrsum", 30},    // i32 array: arr_make + arr_get (slice 2)
		{"structsum", 42}, // scalar struct: struct_make + struct_get (slice 3)
		{"tuplesum", 42},  // tuple: tuple_make + tuple_get (slice 4)
		{"f64cmp", 1},     // f64: const_f64 + fmul/fadd/fgt (slice 5)
		{"castrt", 15},    // i32<->f64 casts: i32_to_f64 + f64_to_i32 (slice 6)
		{"strcat", 6},     // string: const_str + str_concat + str_len (slice 7)
		{"strbuf", 5},     // string builder: strbuf_reset/append/take (slice 8)
		{"exitprog", 42},  // process: exit (diverging inst) (slice 9)
		{"strindex", 66},  // string index: str_index -> load_elem (slice 10)
		{"optval", 42},    // Option: opt_make + opt_payload (slice 11)
		{"argslen", 1},    // args: the argv string[] (slice 12)
		{"closure", 15},   // closure: const_func + arr_make box + call_indirect (slice 13)
	}
	for _, tc := range cases {
		tc := tc
		// x86-64 (native): default + regalloc.
		t.Run("x86_64/"+tc.prog, func(t *testing.T) {
			if len(x86runner) != 0 {
				t.Skip("emitted x86-64 runs natively; skipping under an exec runner")
			}
			mk := func(b string, a ...string) *exec.Cmd { return exec.Command(b, a...) }
			if got := run(t, emit(t, tc.prog), x86gcc, true, mk, "x86-"+tc.prog); got != tc.want {
				t.Errorf("x86-64 lift->emit %s = %d, want %d", tc.prog, got, tc.want)
			}
			if got := run(t, emit(t, tc.prog, "-regalloc"), x86gcc, true, mk, "x86-ra-"+tc.prog); got != tc.want {
				t.Errorf("x86-64 lift->emit -regalloc %s = %d, want %d", tc.prog, got, tc.want)
			}
		})
		// arm64 (qemu): default + regalloc.
		t.Run("arm64/"+tc.prog, func(t *testing.T) {
			mk := func(b string, a ...string) *exec.Cmd { return runArm64Bin(qemu, b, a...) }
			if got := run(t, emit(t, tc.prog, "-target", "arm64-linux"), armgcc, false, mk, "arm-"+tc.prog); got != tc.want {
				t.Errorf("arm64 lift->emit %s = %d, want %d", tc.prog, got, tc.want)
			}
			if got := run(t, emit(t, tc.prog, "-target", "arm64-linux", "-regalloc"), armgcc, false, mk, "arm-ra-"+tc.prog); got != tc.want {
				t.Errorf("arm64 lift->emit -regalloc %s = %d, want %d", tc.prog, got, tc.want)
			}
		})
	}
}
